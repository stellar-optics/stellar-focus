package focus_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// fixture reads real captured mainnet XDR, shared with the sibling
// repositories.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name+".txt"))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}

// mustMarshal builds base64 XDR from a concrete value.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	s, err := xdr.MarshalBase64(v)
	if err != nil {
		t.Fatalf("marshalling %T: %v", v, err)
	}
	return s
}

func TestAssertEqualPasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		assert  func(focus.TB, focus.Value, focus.Value) bool
	}{
		{"envelope", "tx_failed.env", func(tb focus.TB, w, g focus.Value) bool {
			return focus.AssertEnvelopeEqual(tb, w, g)
		}},
		{"result", "tx_failed.res", func(tb focus.TB, w, g focus.Value) bool {
			return focus.AssertResultEqual(tb, w, g)
		}},
		{"auto-detected", "tx_success.env", func(tb focus.TB, w, g focus.Value) bool {
			return focus.AssertEqual(tb, w, g)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := fixture(t, tc.fixture)
			tb := faketb.New()
			if !tc.assert(tb, payload, payload) {
				t.Errorf("assertion returned false for identical values")
			}
			if tb.Failed() {
				t.Errorf("identical values reported a failure:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertEnvelopeEqualFailureMessage is the most important test in the
// package: the failure message is the product, so its content is asserted
// rather than merely its existence.
func TestAssertEnvelopeEqualFailureMessage(t *testing.T) {
	t.Parallel()

	want := buildEnvelope(t, paymentOp(t, 100_0000000))
	got := buildEnvelope(t, paymentOp(t, 200_0000000))

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got) {
		t.Fatal("assertion returned true for differing envelopes")
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}

	// 1. It says what kind of thing differed, and how much.
	if !strings.Contains(msg, "AssertEnvelopeEqual: envelopes differ") {
		t.Errorf("message does not open with the assertion and subject:\n%s", msg)
	}
	if !strings.Contains(msg, "1 difference") {
		t.Errorf("message does not report the number of differences:\n%s", msg)
	}

	// 2. It names the exact field path, which is the whole reason this
	//    library exists rather than a base64 string comparison.
	if !strings.Contains(msg, "Body.PaymentOp.Amount") {
		t.Errorf("message does not name the changed field path:\n%s", msg)
	}

	// 3. It shows both values, enriched — stroops rendered as XLM is the
	//    difference between a readable diff and an arithmetic exercise.
	for _, want := range []string{"100.0000000", "200.0000000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not show the enriched value %q:\n%s", want, msg)
		}
	}

	// 4. It offers the raw payloads for pasting into lens, truncated.
	if !strings.Contains(msg, "want  ") || !strings.Contains(msg, "got   ") {
		t.Errorf("message does not show the raw payloads:\n%s", msg)
	}
	if !strings.Contains(msg, "bytes base64") {
		t.Errorf("message does not truncate the payloads with a size:\n%s", msg)
	}

	// 5. It never dumps a full base64 wall, which is the failure mode being
	//    replaced.
	for _, line := range strings.Split(msg, "\n") {
		if len(line) > 200 {
			t.Errorf("message contains a %d-character line; output must stay readable:\n%s",
				len(line), line)
		}
	}
}

// TestFailureMessageNamesIgnoredPaths guards the promise that a suppressed
// difference is always disclosed.
func TestFailureMessageNamesIgnoredPaths(t *testing.T) {
	t.Parallel()

	want := buildEnvelopeSeq(t, 111, paymentOp(t, 100_0000000))
	got := buildEnvelopeSeq(t, 222, paymentOp(t, 200_0000000))

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got, focus.IgnoreSequence()) {
		t.Fatal("assertion returned true despite a real difference")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}

	if !strings.Contains(msg, "ignored:") || !strings.Contains(msg, "**.SeqNum") {
		t.Errorf("message does not disclose the ignored path:\n%s", msg)
	}
	if strings.Contains(msg, "SeqNum\n") && strings.Contains(msg, "~ V1.Tx.SeqNum") {
		t.Errorf("ignored path still appears as a difference:\n%s", msg)
	}
	if !strings.Contains(msg, "Amount") {
		t.Errorf("the real difference was lost:\n%s", msg)
	}
}

func TestIgnoreOptionsSuppressDifferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		got  string
		opt  focus.Option
	}{
		{
			name: "sequence",
			want: buildEnvelopeSeq(t, 1, paymentOp(t, 100)),
			got:  buildEnvelopeSeq(t, 2, paymentOp(t, 100)),
			opt:  focus.IgnoreSequence(),
		},
		{
			name: "explicit path",
			want: buildEnvelopeSeq(t, 1, paymentOp(t, 100)),
			got:  buildEnvelopeSeq(t, 2, paymentOp(t, 100)),
			opt:  focus.IgnorePaths("V1.Tx.SeqNum"),
		},
		{
			name: "wildcard path",
			want: buildEnvelopeSeq(t, 1, paymentOp(t, 100)),
			got:  buildEnvelopeSeq(t, 2, paymentOp(t, 100)),
			opt:  focus.IgnorePaths("**.SeqNum"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertEnvelopeEqual(tb, tc.want, tc.got, tc.opt) {
				t.Errorf("difference was not suppressed:\n%s", tb.Output())
			}
		})
	}
}

// TestStaleIgnorePatternIsReported covers the anti-rot rule: a pattern that
// matches nothing is reported even when the values are otherwise equal,
// because a stale ignore is a test quietly weakening.
func TestStaleIgnorePatternIsReported(t *testing.T) {
	t.Parallel()

	payload := fixture(t, "tx_failed.env")

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, payload, payload, focus.IgnorePaths("V1.Tx.NoSuchField")) {
		t.Fatal("a stale ignore pattern was accepted silently")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"matched nothing", "NoSuchField"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not flag the stale pattern (%q):\n%s", want, msg)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	want := buildEnvelopeSeq(t, 1000, paymentOp(t, 100))
	got := buildEnvelopeSeq(t, 1001, paymentOp(t, 100))

	// Round sequence numbers to the nearest hundred so a small drift is
	// tolerated while a large one still fails.
	round := focus.NormalizePath("**.SeqNum", func(v any) any {
		n, ok := v.(int64)
		if !ok {
			return v
		}
		return n / 100 * 100
	})

	tb := faketb.New()
	if !focus.AssertEnvelopeEqual(tb, want, got, round) {
		t.Errorf("normalized values were still reported as different:\n%s", tb.Output())
	}

	// A difference outside the tolerance must still fail.
	far := buildEnvelopeSeq(t, 9999, paymentOp(t, 100))
	tb2 := faketb.New()
	if focus.AssertEnvelopeEqual(tb2, want, far, round) {
		t.Error("normalization suppressed a difference it should not have")
	}
}

func TestAssertEqualAcceptsEveryDocumentedInputForm(t *testing.T) {
	t.Parallel()

	env := envelopeValue(t, paymentOp(t, 100))
	b64 := mustMarshal(t, env)
	raw, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	forms := map[string]focus.Value{
		"base64 string": b64,
		"raw bytes":     raw,
		"xdr value":     env,
		"xdr pointer":   &env,
	}

	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			tb := faketb.New()
			if !focus.AssertEnvelopeEqual(tb, b64, form) {
				t.Errorf("form %s was not accepted:\n%s", name, tb.Output())
			}
		})
	}
}

func TestAssertEqualRejectsBadInput(t *testing.T) {
	t.Parallel()

	valid := fixture(t, "tx_failed.env")

	tests := []struct {
		name        string
		want, got   focus.Value
		wantMessage string
	}{
		{"nil want", nil, valid, "want value could not be read"},
		{"nil got", valid, nil, "got value could not be read"},
		{"empty string", "", valid, "empty string"},
		{"not base64", "!!!not base64!!!", valid, "want value could not be read"},
		{"unsupported type", 42, valid, "unsupported type"},
		{"wrong xdr type", fixture(t, "tx_failed.res"), valid, "could not be read"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertEnvelopeEqual(tb, tc.want, tc.got) {
				t.Fatal("assertion returned true for invalid input")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			if !strings.Contains(msg, tc.wantMessage) {
				t.Errorf("message = %q, want it to mention %q", msg, tc.wantMessage)
			}
			// A bad input must never produce a diff, which would be confusing.
			if strings.Contains(msg, "differ —") {
				t.Errorf("invalid input produced a diff instead of an input error:\n%s", msg)
			}
		})
	}
}

// TestAssertionsMarkThemselvesAsHelpers keeps go test reporting the caller's
// file and line rather than a line inside this library.
func TestAssertionsMarkThemselvesAsHelpers(t *testing.T) {
	t.Parallel()

	payload := fixture(t, "tx_failed.env")
	tb := faketb.New()
	focus.AssertEnvelopeEqual(tb, payload, payload)

	if tb.Helpers == 0 {
		t.Error("assertion did not call TB.Helper, so failures would point inside focus")
	}
}

func TestWithMaxChangesElides(t *testing.T) {
	t.Parallel()

	want := buildEnvelopeSeq(t, 1, paymentOp(t, 100), paymentOp(t, 200), paymentOp(t, 300))
	got := buildEnvelopeSeq(t, 1, paymentOp(t, 111), paymentOp(t, 222), paymentOp(t, 333))

	tb := faketb.New()
	focus.AssertEnvelopeEqual(tb, want, got, focus.WithMaxChanges(1))
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "more difference") {
		t.Errorf("elision was not reported:\n%s", msg)
	}
	if !strings.Contains(msg, "WithMaxChanges") {
		t.Errorf("elision does not say how to see the rest:\n%s", msg)
	}
}

func TestWithColor(t *testing.T) {
	t.Parallel()

	want := buildEnvelopeSeq(t, 1, paymentOp(t, 100))
	got := buildEnvelopeSeq(t, 1, paymentOp(t, 200))

	plain := faketb.New()
	focus.AssertEnvelopeEqual(plain, want, got)
	if strings.Contains(plain.Output(), "\x1b[") {
		t.Error("default output contains ANSI escapes; test output is usually captured")
	}

	coloured := faketb.New()
	focus.AssertEnvelopeEqual(coloured, want, got, focus.WithColor("always"))
	if !strings.Contains(coloured.Output(), "\x1b[") {
		t.Error("WithColor(always) produced no ANSI escapes")
	}
}
