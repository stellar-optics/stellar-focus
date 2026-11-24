package focus

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Inflation assertions cover the one protocol-level operation that has no
// envelope counterpart to compare against: the network's own disbursement of
// the fee pool. An inflation transaction's result carries the payouts the
// ledger actually made, and the ledger header carries the fee pool those
// payouts came from. Both are historical data — a closed ledger's header and
// its transaction results — so these assertions are offline like the rest,
// and they are the reason a test can pin down protocol behaviour that no
// operation's parameters predict.
//
// Inflation itself is a dormant mechanism: contemporary networks do not pay
// it and report InflationResultCodeInflationNotTime.
// AssertInflationPayouts treats that as "no payouts", so a suite can also
// state what an off-network result means.

// InflationPayout is one expected inflation payout: the destination account
// and the stroop amount it received.
//
// Destination accepts the same forms as everywhere else: a strkey address, an
// xdr.AccountId or an xdr.MuxedAccount.
type InflationPayout struct {
	Destination Value
	Amount      int64
}

// AssertInflationPayouts checks that an inflation operation's result paid
// exactly the wanted destinations, in order, with exactly the wanted amounts.
//
// The result must be a TransactionResult whose inflation operation succeeded,
// or whose inflation result code is InflationResultCodeInflationNotTime — the
// "inflation did not run" outcome of current networks. In that case the wanted
// list must be empty, since no payouts were made. A transaction-level or
// operation-level failure is reported as such rather than as a mismatch.
//
// Destinations are compared by account address, not by XDR identity, so a
// muxed form of the same account matches. The failure shows the payouts the
// result actually carries beside the ones wanted, so a missing, extra or
// reordered payout is readable at a glance.
func AssertInflationPayouts(t TB, result Value, want []InflationPayout) bool {
	t.Helper()
	const assertion = "AssertInflationPayouts"

	got, ran, ok := inflationPayouts(t, assertion, result)
	if !ok {
		return false
	}

	wantAddr := make([]string, len(want))
	for i, w := range want {
		addr, ok := resolveAccount(t, assertion, fmt.Sprintf("want[%d].Destination", i), w.Destination)
		if !ok {
			return false
		}
		wantAddr[i] = addr
	}

	if !ran {
		if len(want) == 0 {
			return true
		}
		t.Errorf("%s: inflation did not run — the result reports %s — but %d payout(s) were wanted\n\n%s",
			assertion, inflationCodeName(xdr.InflationResultCodeInflationNotTime), len(want),
			indent(describeInflationWant(wantAddr, want)))
		return false
	}

	if len(got) == len(want) {
		equal := true
		for i := range got {
			addr, err := accountAddress(got[i].Destination)
			if err != nil || addr != wantAddr[i] || int64(got[i].Amount) != want[i].Amount {
				equal = false
				break
			}
		}
		if equal {
			return true
		}
	}

	t.Errorf("%s: inflation paid %d payout(s), want %d\n\n%s\n\n%s",
		assertion, len(got), len(want),
		indent(describeInflationGot(got)),
		indent(describeInflationWant(wantAddr, want)))
	return false
}

// AssertInflationPaid checks that an inflation operation's result paid one
// account exactly amount stroops, whether as a single payout or, for an
// account that appears repeatedly, as the sum of its payouts.
//
// It accepts the same result forms as AssertInflationPayouts, including the
// InflationResultCodeInflationNotTime outcome, which never pays anyone. Use
// AssertInflationPayouts when the full payout list matters; this assertion is
// for the common "the account got its share" check.
func AssertInflationPaid(t TB, result Value, account Value, amount int64) bool {
	t.Helper()
	const assertion = "AssertInflationPaid"

	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}
	got, ran, ok := inflationPayouts(t, assertion, result)
	if !ok {
		return false
	}

	var total int64
	var matched int
	if !guard(t, assertion, func() {
		for _, p := range got {
			if dest, err := accountAddress(p.Destination); err == nil && dest == addr {
				total += int64(p.Amount)
				matched++
			}
		}
	}) {
		return false
	}

	if total == amount {
		return true
	}
	if matched == 0 {
		if !ran {
			t.Errorf("%s: inflation did not run — the result reports %s — so %s received nothing, want %s",
				assertion, inflationCodeName(xdr.InflationResultCodeInflationNotTime), shorten(addr),
				amountStroops(xdr.Int64(amount), " XLM"))
			return false
		}
		t.Errorf("%s: inflation made no payout to %s, want %s\n\n%s",
			assertion, shorten(addr), amountStroops(xdr.Int64(amount), " XLM"),
			indent(describeInflationGot(got)))
		return false
	}
	t.Errorf("%s: inflation paid %s a total of %s, want %s\n\n%s",
		assertion, shorten(addr), amountStroops(xdr.Int64(total), " XLM"),
		amountStroops(xdr.Int64(amount), " XLM"),
		indent(describeInflationGot(got)))
	return false
}

// AssertFeePool checks the value of a ledger header's fee pool.
//
// header accepts an xdr.LedgerHeader, or one as base64 XDR, raw bytes or a
// decoded *lens.Value. The failure names the ledger the header belongs to and
// both amounts, since a fee-pool assertion is usually about one specific
// ledger and a bare mismatch would not say which.
func AssertFeePool(t TB, header Value, want int64) bool {
	t.Helper()
	const assertion = "AssertFeePool"

	h, ok := resolveLedgerHeader(t, assertion, header)
	if !ok {
		return false
	}
	got := int64(h.FeePool)
	if got == want {
		return true
	}
	t.Errorf("%s: ledger %d fee pool is %s, want %s",
		assertion, h.LedgerSeq,
		amountStroops(h.FeePool, " XLM"), amountStroops(xdr.Int64(want), " XLM"))
	return false
}

// AssertFeePoolChanged checks that a later ledger header's fee pool differs
// from an earlier one's by delta stroops.
//
// delta is the net movement the transactions between the two ledgers made,
// signed: inflation draws the fee pool down, fees push it up. The headers are
// also checked for order — asserting a change from a later ledger to an
// earlier one reads the delta backwards, and silently negating it would hide
// the mistake.
func AssertFeePoolChanged(t TB, before, after Value, delta int64) bool {
	t.Helper()
	const assertion = "AssertFeePoolChanged"

	from, ok := resolveLedgerHeader(t, assertion, before)
	if !ok {
		return false
	}
	to, ok := resolveLedgerHeader(t, assertion, after)
	if !ok {
		return false
	}
	if to.LedgerSeq < from.LedgerSeq {
		t.Errorf("%s: headers are out of order — before is ledger %d and after is ledger %d; "+
			"pass them oldest first so the delta reads forwards",
			assertion, from.LedgerSeq, to.LedgerSeq)
		return false
	}

	got := int64(to.FeePool) - int64(from.FeePool)
	if got == delta {
		return true
	}
	t.Errorf("%s: fee pool moved from %s in ledger %d to %s in ledger %d, a change of %s, want %s",
		assertion,
		amountStroops(from.FeePool, " XLM"), from.LedgerSeq,
		amountStroops(to.FeePool, " XLM"), to.LedgerSeq,
		amountStroops(xdr.Int64(got), " XLM"), amountStroops(xdr.Int64(delta), " XLM"))
	return false
}

// resolveLedgerHeader coerces a ledger header argument and reports a test
// failure when it cannot.
func resolveLedgerHeader(t TB, assertion string, v Value) (xdr.LedgerHeader, bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "header", v, "LedgerHeader")
	if !ok {
		return xdr.LedgerHeader{}, false
	}
	h, ok := decoded.Raw.(*xdr.LedgerHeader)
	if !ok {
		t.Errorf("%s: value is a %s, want a LedgerHeader", assertion, decoded.Type)
		return xdr.LedgerHeader{}, false
	}
	return *h, true
}

// inflationPayouts extracts the payouts an inflation operation result carries.
//
// ran reports whether inflation actually executed: it is false for the
// InflationResultCodeInflationNotTime outcome, in which case the payout list
// is empty by definition. A missing operation, a failed transaction or an
// unreadable result is a reported failure instead.
func inflationPayouts(t TB, assertion string, result Value) (payouts []xdr.InflationPayout, ran, ok bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "", result, "TransactionResult")
	if !ok {
		return nil, false, false
	}
	tr, ok := decoded.Raw.(*xdr.TransactionResult)
	if !ok {
		t.Errorf("%s: value is a %s, want a TransactionResult; inflation payouts live in a result",
			assertion, decoded.Type)
		return nil, false, false
	}

	var (
		opResults []xdr.OperationResult
		hasOps    bool
		infl      xdr.InflationResult
		hasInfl   bool
	)
	if !guard(t, assertion, func() {
		opResults, hasOps = tr.Result.GetResults()
		if !hasOps {
			return
		}
		for _, op := range opResults {
			if r, found := op.Tr.GetInflationResult(); found {
				infl = r
				hasInfl = true
				return
			}
		}
	}) {
		return nil, false, false
	}
	if !hasOps || tr.Result.Code != xdr.TransactionResultCodeTxSuccess {
		t.Errorf("%s: the transaction failed — its result is %s — so no inflation payout was made",
			assertion, txResultCodeLabel(tr))
		return nil, false, false
	}
	if !hasInfl {
		t.Errorf("%s: the result holds no inflation operation result; it covers %d operation result(s)\n\n%s",
			assertion, len(opResults), indent(describeOperationResults(opResults)))
		return nil, false, false
	}

	switch infl.Code {
	case xdr.InflationResultCodeInflationSuccess:
		var p []xdr.InflationPayout
		if !guard(t, assertion, func() { p, _ = infl.GetPayouts() }) {
			return nil, false, false
		}
		return p, true, true
	case xdr.InflationResultCodeInflationNotTime:
		return nil, false, true
	default:
		t.Errorf("%s: the inflation operation did not succeed — its code is %s (%d)",
			assertion, inflationCodeName(infl.Code), int32(infl.Code))
		return nil, false, false
	}
}

// inflationCodeName names an inflation result code in words, falling back to
// the generated string for codes this package does not know.
func inflationCodeName(code xdr.InflationResultCode) string {
	switch code {
	case xdr.InflationResultCodeInflationSuccess:
		return "inflation_success — the ledger paid inflation"
	case xdr.InflationResultCodeInflationNotTime:
		return "inflation_not_time — it is not inflation time"
	default:
		return code.String()
	}
}

// txResultCodeLabel renders a transaction result's code without calling a
// generated accessor that could panic. It must run inside guard only if the
// union arm is suspect; here the code is a plain field, so it is safe.
func txResultCodeLabel(tr *xdr.TransactionResult) string {
	return fmt.Sprintf("%s (%d)", tr.Result.Code.String(), int32(tr.Result.Code))
}

// describeInflationGot lists the payouts a result carried.
func describeInflationGot(payouts []xdr.InflationPayout) string {
	if len(payouts) == 0 {
		return "inflation paid no one"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "payouts made (%d):\n", len(payouts))
	for i, p := range payouts {
		addr, err := accountAddress(p.Destination)
		if err != nil {
			addr = "(unreadable)"
		}
		fmt.Fprintf(&b, "  [%d] %s  %s\n", i, shorten(addr), amountStroops(p.Amount, " XLM"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeInflationWant lists the payouts a test wanted.
func describeInflationWant(addrs []string, want []InflationPayout) string {
	if len(want) == 0 {
		return "no payouts wanted"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "payouts wanted (%d):\n", len(want))
	for i, w := range want {
		fmt.Fprintf(&b, "  [%d] %s  %s\n", i, shorten(addrs[i]),
			amountStroops(xdr.Int64(w.Amount), " XLM"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeOperationResults lists the operation results of a failed lookup, so
// a missing inflation result shows what the transaction did instead.
func describeOperationResults(results []xdr.OperationResult) string {
	if len(results) == 0 {
		return "the result carries no operation results"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "operation results present (%d):\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&b, "  [%d] %s (code %d)\n", i, r.Code.String(), int32(r.Code))
	}
	return strings.TrimRight(b.String(), "\n")
}
