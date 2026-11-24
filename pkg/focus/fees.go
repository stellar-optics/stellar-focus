package focus

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Fee assertions read the fees a transaction was actually charged rather than
// the fee bid a test declares. On Soroban the two are far apart — the bid
// includes a refundable resource budget, the charge is what execution used —
// so a test that cares about cost is usually asserting on the result and the
// meta, not on the envelope.

// AssertConsumedFeeWithin checks that the fee a transaction was charged does
// not exceed limit, in stroops.
//
// Unlike AssertFee, which checks the fee bid on an envelope, this reads
// FeeCharged from the result: the inclusion fee plus the non-refundable and
// refundable resource fees the source account actually paid. It is the
// assertion to reach for when the point of the test is a fee ceiling.
func AssertConsumedFeeWithin(t TB, result Value, limit int64) bool {
	t.Helper()

	s, ok := summarize(t, "AssertConsumedFeeWithin", result, "TransactionResult")
	if !ok {
		return false
	}
	if s.FeeCharged <= limit {
		return true
	}

	t.Errorf("AssertConsumedFeeWithin: fee charged is %s, want at most %s (over by %s)",
		stroops(s.FeeCharged), stroops(limit), stroops(s.FeeCharged-limit))
	return false
}

// AssertRefundFeeEqual checks the refundable resource fee charged for a
// Soroban transaction, in stroops, as recorded in its transaction meta.
//
// Refundable fees cover what is charged against actual usage — rent, events
// and the return value — while non-refundable fees cover the resources the
// transaction declared. A failed transaction never consumes the refundable
// part, so this doubles as an assertion that a transaction did not get far
// enough to spend it.
func AssertRefundFeeEqual(t TB, meta Value, want int64) bool {
	t.Helper()

	fees, ok := sorobanResourceFees(t, "AssertRefundFeeEqual", meta)
	if !ok {
		return false
	}
	if got := int64(fees.TotalRefundableResourceFeeCharged); got != want {
		t.Errorf("AssertRefundFeeEqual: refundable resource fee is %s, want %s\n\n%s",
			stroops(got), stroops(want), indent(describeResourceFees(fees)))
		return false
	}
	return true
}

// sorobanResourceFees extracts the resource fee breakdown from a transaction
// meta, reporting why when the meta does not carry one.
func sorobanResourceFees(t TB, assertion string, meta Value) (xdr.SorobanTransactionMetaExtV1, bool) {
	var fees xdr.SorobanTransactionMetaExtV1

	soroban, ok := sorobanMeta(t, assertion, meta)
	if !ok {
		return fees, false
	}

	var has bool
	if !guard(t, assertion, func() { fees, has = soroban.Ext.GetV1() }) {
		return fees, false
	}
	if !has {
		t.Errorf("%s: this Soroban meta carries no resource fee breakdown "+
			"(its extension is empty), so the refundable fee was not recorded", assertion)
		return fees, false
	}
	return fees, true
}

// describeResourceFees renders the whole fee breakdown, because refundable and
// non-refundable fees move for different reasons and a mismatch is far easier
// to read with both on screen.
func describeResourceFees(fees xdr.SorobanTransactionMetaExtV1) string {
	var b strings.Builder
	fmt.Fprintf(&b, "non-refundable resource fee: %s\n",
		stroops(int64(fees.TotalNonRefundableResourceFeeCharged)))
	fmt.Fprintf(&b, "refundable resource fee:     %s\n",
		stroops(int64(fees.TotalRefundableResourceFeeCharged)))
	fmt.Fprintf(&b, "  of which rent:             %s",
		stroops(int64(fees.RentFeeCharged)))
	return b.String()
}
