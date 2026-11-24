package focus

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Field assertions are built on lens.Explain rather than reaching into the
// XDR directly. That means the value a test asserts on is exactly the value
// `lens explain` prints for the same transaction, so a failing assertion and
// a manual investigation never disagree about what the transaction says.

// summarize decodes a value and explains it, reporting a test failure on the
// way if either step fails.
func summarize(t TB, assertion string, v Value, typeName string) (*lens.Summary, bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "", v, typeName)
	if !ok {
		return nil, false
	}

	var (
		summary *lens.Summary
		err     error
	)
	if !guard(t, assertion, func() {
		summary, err = lens.Explain(decoded)
	}) {
		return nil, false
	}
	if err != nil {
		t.Errorf("%s: could not summarise the %s: %v", assertion, decoded.Type, err)
		return nil, false
	}
	return summary, true
}

// AssertSourceAccount checks the transaction's source account, as a strkey
// address.
//
// A fee-bump envelope is read through to the inner transaction, so this is
// the account the inner transaction is sourced from — the account the
// protocol charges the fee to.
func AssertSourceAccount(t TB, envelope Value, want string, opts ...Option) bool {
	t.Helper()
	c := newConfig(opts...)
	s, ok := summarize(t, "AssertSourceAccount", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if s.Source != want {
		t.Errorf("%s", mismatchMessage("AssertSourceAccount", "source account", want, s.Source, c))
		return false
	}
	return true
}

// AssertFee checks the fee bid, in stroops.
func AssertFee(t TB, envelope Value, want int64) bool {
	t.Helper()
	s, ok := summarize(t, "AssertFee", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if s.Fee != want {
		t.Errorf("AssertFee: fee is %s, want %s", stroops(s.Fee), stroops(want))
		return false
	}
	return true
}

// AssertSequence checks the transaction's sequence number.
func AssertSequence(t TB, envelope Value, want int64, opts ...Option) bool {
	t.Helper()
	c := newConfig(opts...)
	s, ok := summarize(t, "AssertSequence", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if s.SeqNum != want {
		t.Errorf("%s", mismatchMessage("AssertSequence", "sequence number", want, s.SeqNum, c))
		return false
	}
	return true
}

// AssertMemo checks the transaction memo.
//
// The expected value uses the same form lens prints, so a text memo is
// "text: hello", an id memo "id: 42" and a hash memo "hash: <hex>". An empty
// string asserts that there is no memo.
//
// A fee-bump envelope is read through to the inner transaction, so this is
// the inner transaction's memo.
func AssertMemo(t TB, envelope Value, want string, opts ...Option) bool {
	t.Helper()
	c := newConfig(opts...)
	s, ok := summarize(t, "AssertMemo", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if s.Memo != want {
		msg := mismatchMessage("AssertMemo", "memo", want, s.Memo, c)
		if want != "" && !strings.Contains(want, ":") {
			msg += "\n(memos are written as \"text: …\", \"id: …\" or \"hash: …\")"
		}
		t.Errorf("%s", msg)
		return false
	}
	return true
}

// AssertOperationCount checks how many operations the transaction carries.
func AssertOperationCount(t TB, envelope Value, want int) bool {
	t.Helper()
	s, ok := summarize(t, "AssertOperationCount", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if len(s.Operations) != want {
		t.Errorf("AssertOperationCount: transaction has %d operation(s), want %d\n%s",
			len(s.Operations), want, indent(operationList(s)))
		return false
	}
	return true
}

// AssertOperationTypes checks the operation types, in order, using the
// snake_case names Horizon uses: "payment", "change_trust",
// "invoke_host_function".
//
// A fee-bump envelope is read through to the inner transaction, so these are
// the inner transaction's operations.
func AssertOperationTypes(t TB, envelope Value, want ...string) bool {
	t.Helper()
	s, ok := summarize(t, "AssertOperationTypes", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}

	got := make([]string, 0, len(s.Operations))
	for _, op := range s.Operations {
		got = append(got, op.Type)
	}

	if len(got) != len(want) || !equalStrings(got, want) {
		t.Errorf("AssertOperationTypes: operations are [%s], want [%s]\n%s",
			strings.Join(got, ", "), strings.Join(want, ", "), indent(operationList(s)))
		return false
	}
	return true
}

// AssertOperationType checks the type of a single operation by index.
func AssertOperationType(t TB, envelope Value, index int, want string) bool {
	t.Helper()
	s, ok := summarize(t, "AssertOperationType", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if index < 0 || index >= len(s.Operations) {
		t.Errorf("AssertOperationType: no operation at index %d; the transaction has %d",
			index, len(s.Operations))
		return false
	}
	if got := s.Operations[index].Type; got != want {
		t.Errorf("AssertOperationType: operation %d is %q, want %q", index, got, want)
		return false
	}
	return true
}

// AssertSignatureCount checks how many signatures the envelope carries.
func AssertSignatureCount(t TB, envelope Value, want int, opts ...Option) bool {
	t.Helper()
	c := newConfig(opts...)
	s, ok := summarize(t, "AssertSignatureCount", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if s.SignatureCt != want {
		t.Errorf("%s", mismatchMessage("AssertSignatureCount", "signature count", want, s.SignatureCt, c))
		return false
	}
	return true
}

// AssertOperationSourceAccount checks the source account of the operation at
// opIndex.
//
// An operation may name a source account of its own, and that account then
// replaces the transaction's source account for that operation alone. An
// operation that names none inherits the transaction's.
//
// want is the strkey of the account the operation is sourced from. An empty
// want asserts that the operation names no source account of its own.
//
// The two are different questions, so the failure says which one it
// answered: an operation with no source account of its own is sourced from
// the transaction's source account, and the message names that account rather
// than leaving the reader to work it out.
//
// A muxed source account is reported in its "M…" form. The account it is a
// muxed form of is accepted as well, because the muxed id says which account
// was used, not whose account it is.
func AssertOperationSourceAccount(t TB, envelope Value, opIndex int, want string, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertOperationSourceAccount"
	c := newConfig(opts...)

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return false
	}

	var (
		ops      []xdr.Operation
		txSource string
	)
	if !guard(t, assertion, func() {
		ops = env.Operations()
		txSource = envelopeSource(env)
	}) {
		return false
	}
	if opIndex < 0 || opIndex >= len(ops) {
		t.Errorf("%s: no operation at index %d; the transaction has %d operation(s)",
			c.assertionName(assertion), opIndex, len(ops))
		return false
	}

	source := ops[opIndex].SourceAccount
	if source == nil {
		if want == "" {
			return true
		}
		t.Errorf("%s: operation %d names no source account of its own, so it is sourced from the "+
			"transaction's source account %s.\nPass an empty want to assert that the operation names "+
			"no source account of its own.", c.assertionName(assertion), opIndex, txSource)
		return false
	}

	names, err := muxedAccountNames(*source)
	if err != nil {
		t.Errorf("%s: operation %d has a source account that could not be read: %v",
			c.assertionName(assertion), opIndex, err)
		return false
	}

	if want == "" {
		t.Errorf("%s: operation %d is sourced from %s, want no source account of its own",
			c.assertionName(assertion), opIndex, names[0])
		return false
	}
	for _, name := range names {
		if name == want {
			return true
		}
	}

	t.Errorf("%s", mismatchMessage(assertion,
		fmt.Sprintf("source account of operation %d", opIndex), want, names[0], c))
	return false
}

// muxedAccountNames renders the strkeys an account can be named by: its own,
// and, for a muxed account, the account it is a muxed form of.
func muxedAccountNames(m xdr.MuxedAccount) ([]string, error) {
	own, err := m.GetAddress()
	if err != nil {
		return nil, err
	}
	names := []string{own}

	if m.Type == xdr.CryptoKeyTypeKeyTypeMuxedEd25519 {
		account := m.ToAccountId()
		if plain, err := account.GetAddress(); err == nil && plain != own {
			names = append(names, plain)
		}
	}
	return names, nil
}

// envelopeSource renders the source account of an envelope, which for a
// fee-bump envelope is the inner transaction's.
//
// It reads a generated accessor, so it runs behind guard.
func envelopeSource(env *xdr.TransactionEnvelope) string {
	source := env.SourceAccount()
	addr, err := source.GetAddress()
	if err != nil {
		return "(unreadable)"
	}
	return addr
}

// AssertFeeBump checks whether the transaction is wrapped in a fee bump.
func AssertFeeBump(t TB, envelope Value, want bool) bool {
	t.Helper()
	s, ok := summarize(t, "AssertFeeBump", envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	if got := s.FeeBumpSource != ""; got != want {
		if want {
			t.Errorf("AssertFeeBump: transaction is not fee-bumped, want it to be")
		} else {
			t.Errorf("AssertFeeBump: transaction is fee-bumped by %s, want it not to be", s.FeeBumpSource)
		}
		return false
	}
	return true
}

// InnerEnvelope returns the inner transaction of a fee-bump envelope.
//
// A fee-bump envelope wraps an inner transaction in an outer fee payer and
// a fee bid. The inner transaction is the one the protocol executes, so a
// test that wants to assert on it needs it as a value of its own.
//
// The result is the inner envelope in the form every assertion accepts:
// base64 XDR, so it can be handed straight to another assertion.
//
// An envelope that is not fee-bumped has no inner transaction. That is
// reported rather than returned as an empty value, because a check that
// silently compared nothing is a check that always passes.
func InnerEnvelope(t TB, envelope Value) (Value, bool) {
	t.Helper()
	const assertion = "InnerEnvelope"

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return nil, false
	}
	if !env.IsFeeBump() {
		t.Errorf("%s: the transaction is not fee-bumped, so it has no inner transaction", assertion)
		return nil, false
	}

	var (
		inner string
		err   error
	)
	if !guard(t, assertion, func() {
		inner, err = innerEnvelopeXDR(env)
	}) {
		return nil, false
	}
	if err != nil {
		t.Errorf("%s: could not read the inner transaction: %v", assertion, err)
		return nil, false
	}
	return inner, true
}

// AssertFeeBumpInner runs a check against the inner transaction of a
// fee-bump envelope.
//
// The assertions that read an envelope's fields — the source account, the
// memo, the operations — read through a fee bump to the inner transaction,
// since that is the transaction the protocol executes. This assertion is
// for the checks that have to be made on the inner transaction explicitly:
// it hands the inner transaction to a check of the caller's choosing.
//
// The check is a function, so it can be any assertion:
//
//	focus.AssertFeeBumpInner(t, envelope, func(inner focus.Value) bool {
//		return focus.AssertOperationTypes(t, inner, "payment")
//	})
//
// The check reports its own failure; this assertion reports that the
// envelope had no inner transaction to check.
func AssertFeeBumpInner(t TB, envelope Value, innerCheck func(inner Value) bool) bool {
	t.Helper()
	const assertion = "AssertFeeBumpInner"

	if innerCheck == nil {
		t.Errorf("%s: no check was given for the inner transaction", assertion)
		return false
	}

	inner, ok := InnerEnvelope(t, envelope)
	if !ok {
		return false
	}
	if !innerCheck(inner) {
		t.Errorf("%s: the check on the inner transaction failed", assertion)
		return false
	}
	return true
}

// innerEnvelopeXDR renders the inner transaction of a fee-bump envelope as
// base64 XDR.
//
// A fee bump's inner transaction is always a V1 transaction, so it can be
// rebuilt as an ordinary envelope and read by any assertion. It calls the
// generated accessors, so it must run behind guard.
func innerEnvelopeXDR(env *xdr.TransactionEnvelope) (string, error) {
	inner := &xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1:   env.FeeBump.Tx.InnerTx.V1,
	}
	b, err := inner.MarshalBinary()
	if err != nil {
		return "", err
	}
	return base64Encode(b), nil
}

// Summarize returns the lens summary of a transaction, as an escape hatch for
// assertions this package does not provide.
//
// It reports a test failure and returns nil when the value cannot be read, so
// a caller can use the result directly after a nil check.
func Summarize(t TB, v Value) *lens.Summary {
	t.Helper()
	s, ok := summarize(t, "Summarize", v, "")
	if !ok {
		return nil
	}
	return s
}

// operationList renders the transaction's operations, so a count or type
// mismatch shows what is actually there rather than only what is missing.
func operationList(s *lens.Summary) string {
	if len(s.Operations) == 0 {
		return "(no operations)"
	}
	var b strings.Builder
	for _, op := range s.Operations {
		fmt.Fprintf(&b, "[%d] %s", op.Index, op.Type)
		if op.Detail != "" && op.Detail != op.Type {
			fmt.Fprintf(&b, " — %s", op.Detail)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// stroops renders an amount as both XLM and the raw integer, because fee
// discussions happen in both units.
func stroops(v int64) string {
	whole := v / 10_000_000
	frac := v % 10_000_000
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d (%d.%07d XLM)", v, whole, frac)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
