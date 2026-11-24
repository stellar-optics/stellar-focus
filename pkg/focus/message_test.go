package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// TestFailureMessageSideBySide checks the shape of the improved diff: every
// changed field names itself once and then shows the value wanted beside the
// value found, so the reader never has to work out which sign meant which.
func TestFailureMessageSideBySide(t *testing.T) {
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

	for _, want := range []string{
		"~ V1.Tx.Operations[0].Body.PaymentOp.Amount",
		"want │ 1000000000",
		"got  │ 2000000000",
		"100.0000000",
		"200.0000000",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}

	// The wanted value is shown before the found one, matching the reading
	// order of "want X, got Y".
	wantIdx := strings.Index(msg, "want │")
	gotIdx := strings.Index(msg, "got  │")
	if wantIdx < 0 || gotIdx < 0 || wantIdx > gotIdx {
		t.Errorf("want and got are not shown side by side in order:\n%s", msg)
	}
}

func TestFailureMessageDiffContext(t *testing.T) {
	t.Parallel()

	want := buildEnvelope(t, paymentOp(t, 100_0000000))
	got := buildEnvelope(t, paymentOp(t, 200_0000000))

	tb := faketb.New()
	focus.AssertEnvelopeEqual(tb, want, got, focus.WithDiffContext(1))
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading assertion message: %v", err)
	}
	if !strings.Contains(msg, "Body.PaymentOp.Asset = native") {
		t.Errorf("one context line does not show the neighboring asset field:\n%s", msg)
	}
	if strings.Index(msg, "Body.PaymentOp.Asset = native") > strings.Index(msg, "~ V1.Tx.Operations[0].Body.PaymentOp.Amount") {
		t.Errorf("preceding context appears after the changed field:\n%s", msg)
	}

	tb = faketb.New()
	focus.AssertEnvelopeEqual(tb, want, got, focus.WithDiffContext(0))
	msg, err = tb.Message()
	if err != nil {
		t.Fatalf("reading assertion message: %v", err)
	}
	if strings.Contains(msg, "Body.PaymentOp.Asset = native") {
		t.Errorf("zero context lines still show neighboring fields:\n%s", msg)
	}

	want = buildEnvelope(t, paymentOp(t, 100_0000000))
	got = buildEnvelope(t, creditPaymentOp(t, 100_0000000))
	tb = faketb.New()
	focus.AssertEnvelopeEqual(tb, want, got, focus.WithDiffContext(1))
	msg, err = tb.Message()
	if err != nil {
		t.Fatalf("reading assertion message: %v", err)
	}
	if !strings.Contains(msg, "~ V1.Tx.Operations[0].Body.PaymentOp.Asset") ||
		strings.Index(msg, "Body.PaymentOp.Amount =") < strings.Index(msg, "~ V1.Tx.Operations[0].Body.PaymentOp.Asset") {
		t.Errorf("following context does not appear after the changed field:\n%s", msg)
	}
}

// TestFailureMessageAddedAndRemoved checks that a value present on one side
// only is shown with a single labelled side rather than as an empty pair.
func TestFailureMessageAddedAndRemoved(t *testing.T) {
	t.Parallel()

	// sideOf returns the lines rendered under the change at path: the first
	// line holds the path, the following indented lines hold its sides.
	sideOf := func(t *testing.T, msg, path string) []string {
		t.Helper()
		lines := strings.Split(msg, "\n")
		for i, line := range lines {
			if !strings.Contains(line, path) {
				continue
			}
			var out []string
			for _, next := range lines[i+1:] {
				if !strings.HasPrefix(next, "      ") {
					break
				}
				out = append(out, next)
			}
			return out
		}
		t.Fatalf("message does not mention %q:\n%s", path, msg)
		return nil
	}

	t.Run("added", func(t *testing.T) {
		t.Parallel()

		want := mustMarshal(t, envelopeWithMemo(t, xdr.Memo{Type: xdr.MemoTypeMemoNone}))
		got := mustMarshal(t, envelopeWithMemo(t, textMemo("hi")))

		tb := faketb.New()
		if focus.AssertEnvelopeEqual(tb, want, got) {
			t.Fatal("assertion returned true for envelopes that differ by a memo")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		lines := sideOf(t, msg, "+ V1.Tx.Memo.Text")
		if len(lines) != 1 || !strings.Contains(lines[0], "got  │") {
			t.Errorf("added value does not have exactly one found side:\n%s", strings.Join(lines, "\n"))
		}
	})

	t.Run("removed", func(t *testing.T) {
		t.Parallel()

		want := mustMarshal(t, envelopeWithMemo(t, textMemo("hi")))
		got := mustMarshal(t, envelopeWithMemo(t, xdr.Memo{Type: xdr.MemoTypeMemoNone}))

		tb := faketb.New()
		if focus.AssertEnvelopeEqual(tb, want, got) {
			t.Fatal("assertion returned true for envelopes that differ by a memo")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		lines := sideOf(t, msg, "- V1.Tx.Memo.Text")
		if len(lines) != 1 || !strings.Contains(lines[0], "want │") {
			t.Errorf("removed value does not have exactly one wanted side:\n%s", strings.Join(lines, "\n"))
		}
	})
}

// TestFailureMessageColorized checks that colour reaches the sides and the
// paths, and that stripping it reproduces the plain message exactly.
func TestFailureMessageColorized(t *testing.T) {
	t.Parallel()

	want := buildEnvelope(t, paymentOp(t, 100_0000000))
	got := buildEnvelope(t, paymentOp(t, 200_0000000))

	plainTB := faketb.New()
	focus.AssertEnvelopeEqual(plainTB, want, got)
	plain, err := plainTB.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}

	coloredTB := faketb.New()
	focus.AssertEnvelopeEqual(coloredTB, want, got, focus.WithColor("always"))
	colored, err := coloredTB.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}

	if !focus.ContainsANSI(colored) {
		t.Fatalf("coloured message has no ANSI:\n%s", colored)
	}
	if !strings.Contains(colored, focus.ANSIRed) || !strings.Contains(colored, focus.ANSIGreen) {
		t.Errorf("coloured message does not colour the two sides:\n%q", colored)
	}
	if stripped := focus.StripANSI(colored); stripped != plain {
		t.Errorf("stripped coloured message differs from the plain one:\nplain:\n%s\nstripped:\n%s",
			plain, stripped)
	}
}

// TestFailureMessageStaysReadable guards the promise that no single line of a
// failure grows into a wall, however many changes a diff holds.
func TestFailureMessageStaysReadable(t *testing.T) {
	t.Parallel()

	want := buildEnvelopeSeq(t,
		1,
		paymentOp(t, 1), paymentOp(t, 2), paymentOp(t, 3),
		paymentOp(t, 4), paymentOp(t, 5), paymentOp(t, 6),
	)
	got := buildEnvelopeSeq(t,
		2,
		paymentOp(t, 10), paymentOp(t, 20), paymentOp(t, 30),
		paymentOp(t, 40), paymentOp(t, 50), paymentOp(t, 60),
	)

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got, focus.WithMaxChanges(3)) {
		t.Fatal("assertion returned true for differing envelopes")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, line := range strings.Split(msg, "\n") {
		if len(line) > 200 {
			t.Errorf("message contains a %d-character line:\n%s", len(line), line)
		}
	}
	if !strings.Contains(msg, "and 4 more difference(s); raise the limit with focus.WithMaxChanges") {
		t.Errorf("message does not mention the elided differences:\n%s", msg)
	}
}

// TestFailureMessageLabel checks the contextual label appears beside the
// assertion name, so a failure inside a loop or a multi-step flow names the
// step it came from.
func TestFailureMessageLabel(t *testing.T) {
	t.Parallel()

	want := buildEnvelope(t, paymentOp(t, 100_0000000))
	got := buildEnvelope(t, paymentOp(t, 200_0000000))

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got, focus.WithLabel("step 3: fee refund")) {
		t.Fatal("assertion returned true for differing envelopes")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "AssertEnvelopeEqual [step 3: fee refund]: envelopes differ") {
		t.Errorf("failure headline does not carry the label:\n%s", msg)
	}

	// An empty label is the same as not setting one.
	tb = faketb.New()
	focus.AssertEnvelopeEqual(tb, want, got, focus.WithLabel(""))
	unlabeled, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(unlabeled, "AssertEnvelopeEqual: envelopes differ") ||
		strings.Contains(unlabeled, "[]") {
		t.Errorf("empty label changed the headline:\n%s", unlabeled)
	}

	// The scalar failure helpers carry the label too.
	tb = faketb.New()
	focus.AssertSequence(tb, want, 999, focus.WithLabel("step 1: sequence"))
	scalar, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(scalar, "AssertSequence [step 1: sequence]: sequence number is") {
		t.Errorf("scalar failure does not carry the label:\n%s", scalar)
	}
}
