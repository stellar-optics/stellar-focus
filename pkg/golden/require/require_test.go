package require_test

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
	"github.com/stellar-optics/stellar-focus/pkg/golden/require"
)

// Real mainnet addresses, so the strkey enrichers in lens actually fire and
// the failure messages under test are the ones users will see.
const (
	testSource = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	testDest   = "GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ"
)

// envelope builds a minimal valid v1 envelope carrying one payment.
func envelope(t *testing.T, amount int64) string {
	t.Helper()

	var src, dst xdr.AccountId
	if err := src.SetAddress(testSource); err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	if err := dst.SetAddress(testDest); err != nil {
		t.Fatalf("parsing destination: %v", err)
	}

	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: src.ToMuxedAccount(),
				Fee:           100,
				SeqNum:        xdr.SequenceNumber(12345),
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

	b, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("marshalling envelope: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// store writes a golden file holding the given envelope and returns the
// directory it is in.
func store(t *testing.T, env string) string {
	t.Helper()

	dir := t.TempDir()
	fixture, err := golden.Build(env, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return dir
}

// TestAssertStopsTheTestOnFailure is the point of the package: the failure
// is reported fatally, so the test does not carry on with a fixture that
// does not match.
func TestAssertStopsTheTestOnFailure(t *testing.T) {
	t.Parallel()

	dir := store(t, envelope(t, 100))

	tb := faketb.New()
	require.Assert(tb, "env", envelope(t, 200), golden.WithDir(dir))

	if len(tb.Fatals) == 0 {
		t.Fatalf("the failure was not reported fatally:\n%s", tb.Output())
	}
	if len(tb.Errors) != 0 {
		t.Errorf("the failure was reported through Errorf as well:\n%s", tb.Output())
	}
	if !tb.Failed() {
		t.Error("the fake TB does not consider the test failed")
	}
}

// TestAssertKeepsTheDiagnostic checks that stopping the test does not cost
// the reader the diff or the command that fixes it.
func TestAssertKeepsTheDiagnostic(t *testing.T) {
	t.Parallel()

	dir := store(t, envelope(t, 100))

	tb := faketb.New()
	require.Assert(tb, "env", envelope(t, 200), golden.WithDir(dir))

	out := tb.Output()
	for _, want := range []string{
		"Body.PaymentOp.Amount",
		"is out of date",
		"-focus.update",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the fatal message does not contain %q:\n%s", want, out)
		}
	}
}

// TestAssertSaysTheSameThingAsTheNonFatalOne checks that the fatal variant
// did not grow a second, different message: the reader should not have to
// learn two diagnostics for the same problem.
func TestAssertSaysTheSameThingAsTheNonFatalOne(t *testing.T) {
	t.Parallel()

	dir := store(t, envelope(t, 100))
	actual := envelope(t, 200)

	plain := faketb.New()
	golden.New(plain, golden.WithDir(dir)).Assert("env", actual)

	fatal := faketb.New()
	require.Assert(fatal, "env", actual, golden.WithDir(dir))

	if len(plain.Errors) == 0 {
		t.Fatalf("the non-fatal assertion reported nothing:\n%s", plain.Output())
	}
	for _, message := range plain.Errors {
		if !strings.Contains(fatal.Output(), message) {
			t.Errorf("the fatal message lost %q:\nnon-fatal: %s\nfatal:     %s",
				message, plain.Output(), fatal.Output())
		}
	}
}

// TestAssertIsSilentWhenTheFixtureMatches checks the ordinary case: a
// fixture that matches says nothing at all.
func TestAssertIsSilentWhenTheFixtureMatches(t *testing.T) {
	t.Parallel()

	dir := store(t, envelope(t, 100))

	tb := faketb.New()
	require.Assert(tb, "env", envelope(t, 100), golden.WithDir(dir))

	if tb.Failed() {
		t.Errorf("a matching fixture reported a failure:\n%s", tb.Output())
	}
}

// TestAssertReportsAMissingFixtureFatally covers the first run of a new
// golden test, where the reader needs to be told how to record the file.
func TestAssertReportsAMissingFixtureFatally(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	require.Assert(tb, "absent", envelope(t, 100), golden.WithDir(t.TempDir()))

	if len(tb.Fatals) == 0 {
		t.Fatalf("a missing fixture was not reported fatally:\n%s", tb.Output())
	}
	out := tb.Output()
	for _, want := range []string{"does not exist yet", "-focus.update"} {
		if !strings.Contains(out, want) {
			t.Errorf("the fatal message does not contain %q:\n%s", want, out)
		}
	}
}

// TestNewAssertStopsTheTest checks the constructor form, which is what a
// test with several golden files uses.
func TestNewAssertStopsTheTest(t *testing.T) {
	t.Parallel()

	dir := store(t, envelope(t, 100))

	tb := faketb.New()
	g := require.New(tb, golden.WithDir(dir))

	if got := g.Path("env"); got != filepath.Join(dir, "env.golden.json") {
		t.Errorf("Path = %q, want the file in %q", got, dir)
	}

	if g.Assert("env", envelope(t, 200)) {
		t.Error("Assert() passed against a stale golden file")
	}
	if len(tb.Fatals) == 0 {
		t.Errorf("the failure was not reported fatally:\n%s", tb.Output())
	}
}

// TestAssertAsNamesTheType covers the explicit-type form: it behaves like
// Assert, but does not have to detect the XDR type from the payload.
func TestAssertAsNamesTheType(t *testing.T) {
	t.Parallel()

	dir := store(t, envelope(t, 100))

	tb := faketb.New()
	require.AssertAs(tb, "TransactionEnvelope", "env", envelope(t, 200), golden.WithDir(dir))

	if len(tb.Fatals) == 0 {
		t.Fatalf("AssertAs did not report the mismatch fatally:\n%s", tb.Output())
	}
	if !strings.Contains(tb.Output(), "Body.PaymentOp.Amount") {
		t.Errorf("AssertAs lost the diff:\n%s", tb.Output())
	}

	tb = faketb.New()
	require.AssertAs(tb, "TransactionEnvelope", "env", envelope(t, 100), golden.WithDir(dir))
	if tb.Failed() {
		t.Errorf("AssertAs reported a failure for a matching fixture:\n%s", tb.Output())
	}
}

// TestAssertMarksItselfAsAHelper checks that the failure is reported at the
// caller's line, not inside the package.
func TestAssertMarksItselfAsAHelper(t *testing.T) {
	t.Parallel()

	dir := store(t, envelope(t, 100))

	tb := faketb.New()
	require.Assert(tb, "env", envelope(t, 200), golden.WithDir(dir))

	if tb.Helpers == 0 {
		t.Error("the assertion did not call TB.Helper")
	}
}
