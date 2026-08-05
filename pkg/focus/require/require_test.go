package require_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}

func envelope(t *testing.T, amount int64) string {
	t.Helper()

	var src, dst xdr.AccountId
	if err := src.SetAddress("GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"); err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	if err := dst.SetAddress("GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ"); err != nil {
		t.Fatalf("parsing destination: %v", err)
	}

	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: src.ToMuxedAccount(),
				Fee:           100,
				SeqNum:        1,
				Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations: []xdr.Operation{{Body: xdr.OperationBody{
					Type: xdr.OperationTypePayment,
					PaymentOp: &xdr.PaymentOp{
						Destination: dst.ToMuxedAccount(),
						Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
						Amount:      xdr.Int64(amount),
					},
				}}},
			},
		},
	}
	s, err := xdr.MarshalBase64(env)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return s
}

// TestEveryMirrorReportsFatally is the contract of this package: the same
// checks as pkg/focus, reported through Fatalf instead of Errorf.
//
// A mirror that quietly used Errorf would let a test continue past a failure
// the caller explicitly asked to stop on, so every wrapper is covered rather
// than a representative sample.
func TestEveryMirrorReportsFatally(t *testing.T) {
	t.Parallel()

	env := envelope(t, 100)
	other := envelope(t, 200)
	result := fixture(t, "tx_failed.res.txt")

	tests := []struct {
		name string
		call func(tb *faketb.TB)
	}{
		{"EnvelopeEqual", func(tb *faketb.TB) { require.EnvelopeEqual(tb, env, other) }},
		{"Equal", func(tb *faketb.TB) { require.Equal(tb, env, other) }},
		{"EqualAs", func(tb *faketb.TB) { require.EqualAs(tb, "TransactionEnvelope", env, other) }},
		{"ResultEqual", func(tb *faketb.TB) { require.ResultEqual(tb, result, "bad") }},
		{"MetaEqual", func(tb *faketb.TB) { require.MetaEqual(tb, "bad", "bad") }},
		{"SourceAccount", func(tb *faketb.TB) { require.SourceAccount(tb, env, "GWRONG") }},
		{"Fee", func(tb *faketb.TB) { require.Fee(tb, env, 999) }},
		{"Sequence", func(tb *faketb.TB) { require.Sequence(tb, env, 999) }},
		{"Memo", func(tb *faketb.TB) { require.Memo(tb, env, "text: nope") }},
		{"OperationCount", func(tb *faketb.TB) { require.OperationCount(tb, env, 9) }},
		{"OperationTypes", func(tb *faketb.TB) { require.OperationTypes(tb, env, "change_trust") }},
		{"SignatureCount", func(tb *faketb.TB) { require.SignatureCount(tb, env, 9) }},
		{"Succeeded", func(tb *faketb.TB) { require.Succeeded(tb, result) }},
		{"Failed", func(tb *faketb.TB) { require.Failed(tb, "bad") }},
		{"ResultCode", func(tb *faketb.TB) { require.ResultCode(tb, result, "tx_bad_seq") }},
		{"OperationFailed", func(tb *faketb.TB) { require.OperationFailed(tb, result, 0, "payment_underfunded") }},
		{"OperationSucceeded", func(tb *faketb.TB) { require.OperationSucceeded(tb, result, 0) }},
		{"EventEmitted", func(tb *faketb.TB) { require.EventEmitted(tb, "bad", focusEvent()) }},
		{"EventCount", func(tb *faketb.TB) { require.EventCount(tb, "bad", 1) }},
		{"ReturnValue", func(tb *faketb.TB) { require.ReturnValue(tb, "bad", "x") }},
		{"ContractTrapped", func(tb *faketb.TB) { require.ContractTrapped(tb, "bad") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			tc.call(tb)

			if len(tb.Fatals) == 0 {
				t.Errorf("%s did not report fatally; require must stop the test", tc.name)
			}
			if len(tb.Errors) != 0 {
				t.Errorf("%s reported via Errorf; require must use Fatalf:\n%s",
					tc.name, strings.Join(tb.Errors, "\n"))
			}
		})
	}
}

// TestMirrorsStaySilentOnSuccess guards against a wrapper that fails
// unconditionally, which the test above would not catch.
func TestMirrorsStaySilentOnSuccess(t *testing.T) {
	t.Parallel()

	env := envelope(t, 100)

	tb := faketb.New()
	require.EnvelopeEqual(tb, env, env)
	require.Fee(tb, env, 100)
	require.Sequence(tb, env, 1)
	require.OperationCount(tb, env, 1)
	require.OperationTypes(tb, env, "payment")

	if tb.Failed() {
		t.Errorf("a passing assertion reported a failure:\n%s", tb.Output())
	}
}

// TestMirrorsMarkThemselvesAsHelpers keeps go test pointing at the caller.
func TestMirrorsMarkThemselvesAsHelpers(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	env := envelope(t, 100)
	require.EnvelopeEqual(tb, env, env)

	if tb.Helpers == 0 {
		t.Error("require wrapper did not call TB.Helper")
	}
}

// TestMessagesMatchTheAssertPackage checks that only severity differs, not
// content — a divergence would mean two message formats to maintain.
func TestMessagesMatchTheAssertPackage(t *testing.T) {
	t.Parallel()

	env := envelope(t, 100)
	other := envelope(t, 200)

	tb := faketb.New()
	require.EnvelopeEqual(tb, env, other)

	msg := tb.Output()
	for _, want := range []string{"AssertEnvelopeEqual", "Body.PaymentOp.Amount", "0.0000100", "0.0000200"} {
		if !strings.Contains(msg, want) {
			t.Errorf("require lost %q from the message:\n%s", want, msg)
		}
	}
}

// focusEvent returns an empty matcher, used where an assertion only needs to
// be driven to its failure path.
func focusEvent() focus.Event { return focus.Event{} }
