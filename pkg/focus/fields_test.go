package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// operationWithSource builds a payment operation that names a source account
// of its own.
func operationWithSource(tb testing.TB) xdr.Operation {
	tb.Helper()

	op := paymentOp(tb, 100)
	muxed := mustMuxed(tb, testDest)
	op.SourceAccount = &muxed
	return op
}

// operationWithMuxedSource builds a payment operation whose source account is
// a muxed form of an account, which the protocol records in its own "M…"
// form.
func operationWithMuxedSource(tb testing.TB, source string, id uint64) xdr.Operation {
	tb.Helper()

	aid := mustAccountID(tb, source)
	op := paymentOp(tb, 100)
	op.SourceAccount = &xdr.MuxedAccount{
		Type: xdr.CryptoKeyTypeKeyTypeMuxedEd25519,
		Med25519: &xdr.MuxedAccountMed25519{
			Ed25519: *aid.Ed25519,
			Id:      xdr.Uint64(id),
		},
	}
	return op
}

// hintOf returns the four-byte hint the protocol derives from a signer's
// public key, in the form the signature assertions take.
func hintOf(t *testing.T, address string) [4]byte {
	t.Helper()
	return [4]byte(signerHint(t, address))
}

// TestAssertOperationSourceAccountPass covers the two questions the assertion
// asks: which account an operation is sourced from, and whether it names one
// of its own.
func TestAssertOperationSourceAccountPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		envelope xdr.TransactionEnvelope
		opIndex  int
		want     string
	}{
		{
			name: "operation names a source account",
			envelope: envelopeValue(t,
				operationWithSource(t),
				paymentOp(t, 200)),
			opIndex: 0,
			want:    testDest,
		},
		{
			name: "operation names no source account",
			envelope: envelopeValue(t,
				paymentOp(t, 100),
				operationWithSource(t)),
			opIndex: 1,
			want:    testDest,
		},
		{
			name: "no source account anywhere",
			envelope: envelopeValue(t,
				paymentOp(t, 100)),
			opIndex: 0,
			want:    "",
		},
		{
			name: "a muxed source account is named by the account it belongs to",
			envelope: envelopeValue(t,
				operationWithMuxedSource(t, testDest, 7)),
			opIndex: 0,
			want:    testDest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertOperationSourceAccount(tb, tc.envelope, tc.opIndex, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertOperationSourceAccountFail pins what a failing assertion says.
//
// The two failures are different questions, so they are worded differently:
// an operation that names no source account of its own is sourced from the
// transaction's, and the message has to say so rather than leave the reader
// comparing two addresses that were never going to be equal.
func TestAssertOperationSourceAccountFail(t *testing.T) {
	t.Parallel()

	overridden := envelopeValue(t, operationWithSource(t))
	inherited := envelopeValue(t, paymentOp(t, 100))

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "a different source account",
			call: func(tb focus.TB) bool {
				return focus.AssertOperationSourceAccount(tb, overridden, 0, testIssuer)
			},
			contains: []string{
				"AssertOperationSourceAccount: source account of operation 0 is",
				testDest,
				testIssuer,
			},
		},
		{
			name: "an operation that names no source account",
			call: func(tb focus.TB) bool {
				return focus.AssertOperationSourceAccount(tb, inherited, 0, testDest)
			},
			contains: []string{
				"operation 0 names no source account of its own",
				"transaction's source account",
				testSource,
			},
		},
		{
			name: "an operation that names one",
			call: func(tb focus.TB) bool {
				return focus.AssertOperationSourceAccount(tb, overridden, 0, "")
			},
			contains: []string{
				"operation 0 is sourced from",
				"want no source account of its own",
			},
		},
		{
			name: "an index past the last operation",
			call: func(tb focus.TB) bool {
				return focus.AssertOperationSourceAccount(tb, overridden, 5, testDest)
			},
			contains: []string{
				"no operation at index 5; the transaction has 1 operation(s)",
			},
		},
		{
			name: "a value that is not an envelope",
			call: func(tb focus.TB) bool {
				return focus.AssertOperationSourceAccount(tb, 42, 0, testDest)
			},
			contains: []string{
				"envelope value could not be read",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
				t.Fatal("assertion passed, want a failure")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestAssertSignatureHintsPass covers the hints an envelope carries, in both
// signature sets of a fee-bump transaction.
func TestAssertSignatureHintsPass(t *testing.T) {
	t.Parallel()

	inner := hintOf(t, testSource)
	outer := hintOf(t, testDest)

	tests := []struct {
		name     string
		envelope xdr.TransactionEnvelope
		hints    [][4]byte
	}{
		{
			name:     "one hint",
			envelope: signedEnvelope(t, testSource),
			hints:    [][4]byte{inner},
		},
		{
			name:     "two hints",
			envelope: signedEnvelope(t, testSource, testDest),
			hints:    [][4]byte{inner, outer},
		},
		{
			name: "a fee-bump transaction's outer signature",
			envelope: feeBumpEnvelopeValue(t,
				signedEnvelope(t, testSource),
				[]xdr.DecoratedSignature{decoratedSignature(t, testDest)},
				nil),
			hints: [][4]byte{outer},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertSignatureHints(tb, tc.envelope, tc.hints...) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertSignatureHintsFail checks that a missing hint is named, and that
// the hints the envelope does carry are listed.
func TestAssertSignatureHintsFail(t *testing.T) {
	t.Parallel()

	present := hintOf(t, testSource)
	missing := hintOf(t, testIssuer)

	envelope := signedEnvelope(t, testSource)

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "a hint that is not there",
			call: func(tb focus.TB) bool {
				return focus.AssertSignatureHints(tb, envelope, missing)
			},
			contains: []string{
				"AssertSignatureHints: 1 of 1 expected hint(s) have no matching signature",
				"expected hints (1)",
				"no signature",
				"signatures carried (1)",
			},
		},
		{
			name: "one hint of two is missing",
			call: func(tb focus.TB) bool {
				return focus.AssertSignatureHints(tb, envelope, present, missing)
			},
			contains: []string{
				"1 of 2 expected hint(s) have no matching signature",
			},
		},
		{
			name: "no hints at all",
			call: func(tb focus.TB) bool {
				return focus.AssertSignatureHints(tb, envelope)
			},
			contains: []string{
				"no signature hints were given",
			},
		},
		{
			name: "a value that is not an envelope",
			call: func(tb focus.TB) bool {
				return focus.AssertSignatureHints(tb, 42, present)
			},
			contains: []string{
				"envelope value could not be read",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
				t.Fatal("assertion passed, want a failure")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestAssertSignedByPass covers the signers an envelope can be checked
// against by their public key.
func TestAssertSignedByPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		envelope xdr.TransactionEnvelope
		keys     []string
	}{
		{
			name:     "one signer",
			envelope: signedEnvelope(t, testSource),
			keys:     []string{testSource},
		},
		{
			name:     "two signers",
			envelope: signedEnvelope(t, testSource, testDest),
			keys:     []string{testSource, testDest},
		},
		{
			name: "a fee-bump transaction's outer signature",
			envelope: feeBumpEnvelopeValue(t,
				envelopeValue(t, paymentOp(t, 100)),
				[]xdr.DecoratedSignature{decoratedSignature(t, testDest)},
				nil),
			keys: []string{testDest},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertSignedBy(tb, tc.envelope, tc.keys...) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertSignedByFail checks that a signer that did not sign is named,
// and that a key that is not a key at all is called out.
func TestAssertSignedByFail(t *testing.T) {
	t.Parallel()

	envelope := signedEnvelope(t, testSource)

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "a signer that did not sign",
			call: func(tb focus.TB) bool {
				return focus.AssertSignedBy(tb, envelope, testIssuer)
			},
			contains: []string{
				"AssertSignedBy: 1 of 1 expected signer(s) have no matching signature",
				"expected signers (1)",
				"no signature",
			},
		},
		{
			name: "no keys at all",
			call: func(tb focus.TB) bool {
				return focus.AssertSignedBy(tb, envelope)
			},
			contains: []string{
				"no public keys were given",
			},
		},
		{
			name: "a key that is not a key",
			call: func(tb focus.TB) bool {
				return focus.AssertSignedBy(tb, envelope, "not-a-key")
			},
			contains: []string{
				"wantPublicKeys[0] could not be read as a signer",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
				t.Fatal("assertion passed, want a failure")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestFieldAssertionsRequireFatalf is the documented difference between the
// two packages: require stops the test, and its message is unchanged.
func TestFieldAssertionsRequireFatalf(t *testing.T) {
	t.Parallel()

	envelope := envelopeValue(t, operationWithSource(t))

	tb := faketb.New()
	require.OperationSourceAccount(tb, envelope, 0, testIssuer)
	if len(tb.Fatals) != 1 || len(tb.Errors) != 0 {
		t.Fatalf("require reported through Errorf; want one Fatalf (errors=%d fatals=%d)",
			len(tb.Errors), len(tb.Fatals))
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading the failure message: %v", err)
	}
	if !strings.Contains(msg, "source account of operation 0") {
		t.Errorf("require lost the field name:\n%s", msg)
	}
}

// feeBumpWithMemo builds a fee-bump envelope whose inner transaction
// carries a text memo, so a test can tell the inner transaction's memo from
// the wrapper's.
func feeBumpWithMemo(tb testing.TB, memo string) xdr.TransactionEnvelope {
	tb.Helper()

	inner := envelopeValue(tb, paymentOp(tb, 100))
	text := memo
	inner.V1.Tx.Memo = xdr.Memo{Type: xdr.MemoTypeMemoText, Text: &text}
	return feeBumpEnvelopeValue(tb, inner, nil, nil)
}

// TestFieldAssertionsReadThroughAFeeBump checks the documented behaviour of
// the field assertions on a fee-bump envelope: they read the inner
// transaction, because that is the transaction the protocol executes.
func TestFieldAssertionsReadThroughAFeeBump(t *testing.T) {
	t.Parallel()

	feeBump := feeBumpWithMemo(t, "hello")

	tests := []struct {
		name string
		call func(tb focus.TB) bool
	}{
		{"source account", func(tb focus.TB) bool { return focus.AssertSourceAccount(tb, feeBump, testSource) }},
		{"memo", func(tb focus.TB) bool { return focus.AssertMemo(tb, feeBump, "text: hello") }},
		{"operation types", func(tb focus.TB) bool { return focus.AssertOperationTypes(tb, feeBump, "payment") }},
		{"fee bump", func(tb focus.TB) bool { return focus.AssertFeeBump(tb, feeBump, true) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !tc.call(tb) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("the fee payer is not the source account", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSourceAccount(tb, feeBump, testDest) {
			t.Error("the fee payer's account was read as the source account")
		}
	})
}

// TestInnerEnvelopeReturnsTheInnerTransaction checks that the inner
// transaction comes back as a value every assertion can read.
func TestInnerEnvelopeReturnsTheInnerTransaction(t *testing.T) {
	t.Parallel()

	inner := envelopeValue(t, paymentOp(t, 100))
	feeBump := feeBumpEnvelopeValue(t, inner, nil, nil)

	tb := faketb.New()
	got, ok := focus.InnerEnvelope(tb, feeBump)
	if !ok {
		t.Fatalf("InnerEnvelope failed:\n%s", tb.Output())
	}

	// The inner transaction is the one that was wrapped, so it compares
	// equal to the envelope it was built from.
	want := mustMarshal(t, xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx:         inner.V1.Tx,
			Signatures: inner.V1.Signatures,
		},
	})
	if got != want {
		t.Errorf("InnerEnvelope = %v, want the inner transaction %v", got, want)
	}

	// And it can be handed to another assertion.
	if !focus.AssertOperationTypes(faketb.New(), got, "payment") {
		t.Error("the inner transaction cannot be asserted on")
	}
}

// TestInnerEnvelopeRejectsAPlainEnvelope checks that a transaction with no
// inner transaction is reported rather than returned as something empty.
func TestInnerEnvelopeRejectsAPlainEnvelope(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if _, ok := focus.InnerEnvelope(tb, envelopeValue(t, paymentOp(t, 100))); ok {
		t.Fatal("InnerEnvelope accepted a transaction that is not fee-bumped")
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "not fee-bumped") {
		t.Errorf("message does not explain the problem:\n%s", msg)
	}
}

// TestAssertFeeBumpInnerRunsTheCheckOnTheInnerTransaction is what the
// assertion is for: a check made on the transaction the fee bump wraps.
func TestAssertFeeBumpInnerRunsTheCheckOnTheInnerTransaction(t *testing.T) {
	t.Parallel()

	feeBump := feeBumpWithMemo(t, "hello")

	t.Run("the check sees the inner transaction", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		ok := focus.AssertFeeBumpInner(tb, feeBump, func(inner focus.Value) bool {
			return focus.AssertOperationTypes(tb, inner, "payment") &&
				focus.AssertMemo(tb, inner, "text: hello")
		})
		if !ok {
			t.Errorf("the check on the inner transaction failed:\n%s", tb.Output())
		}
		if tb.Failed() {
			t.Errorf("a passing check reported a failure:\n%s", tb.Output())
		}
	})

	t.Run("a failing check is reported", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		ok := focus.AssertFeeBumpInner(tb, feeBump, func(inner focus.Value) bool {
			return focus.AssertOperationTypes(tb, inner, "change_trust")
		})
		if ok {
			t.Error("the assertion passed although the check failed")
		}
		if !tb.Failed() {
			t.Error("the failing check reported nothing")
		}
	})

	t.Run("a transaction with no inner transaction", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		ok := focus.AssertFeeBumpInner(tb, envelopeValue(t, paymentOp(t, 100)), func(focus.Value) bool {
			t.Error("the check ran although there is no inner transaction")
			return true
		})
		if ok {
			t.Error("the assertion passed although there is no inner transaction")
		}
	})

	t.Run("no check at all", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertFeeBumpInner(tb, feeBump, nil) {
			t.Error("the assertion passed although no check was given")
		}
		if !tb.Failed() {
			t.Error("the missing check was not reported")
		}
	})
}

// TestAssertFeeBumpInnerRequireMirror checks the fatal mirror of the new
// assertion.
func TestAssertFeeBumpInnerRequireMirror(t *testing.T) {
	t.Parallel()

	feeBump := feeBumpWithMemo(t, "hello")

	tb := faketb.New()
	require.FeeBumpInner(tb, feeBump, func(inner focus.Value) bool {
		return focus.AssertOperationTypes(tb, inner, "change_trust")
	})
	if len(tb.Fatals) == 0 {
		t.Errorf("require did not report fatally:\n%s", tb.Output())
	}
}
