package golden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
)

// envelopeXDR is a real captured envelope, used where a test needs a payload
// that decodes as a TransactionEnvelope.
func envelopeXDR(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// TestAssertAllRecordsEachFixtureWithItsOwnDescription checks that a table
// writes its own notes into the fixtures it records, and falls back to the
// suite's description for the entries that have none.
func TestAssertAllRecordsEachFixtureWithItsOwnDescription(t *testing.T) {
	original := *updateFlag
	*updateFlag = true
	t.Cleanup(func() { *updateFlag = original })

	dir := t.TempDir()
	tb := faketb.New()
	g := New(tb, WithDir(dir), WithDescription("the suite's description"))

	cases := []Case{
		{Name: "payment", Value: envelopeXDR(t), Description: "the first payment"},
		{Name: "other", Value: envelopeXDR(t)},
	}
	if !g.AssertAll(cases) {
		t.Fatalf("AssertAll() failed while recording:\n%s", tb.Output())
	}
	if tb.Failed() {
		t.Fatalf("recording a table reported a failure:\n%s", tb.Output())
	}

	described, err := Load(filepath.Join(dir, "payment.golden.json"))
	if err != nil {
		t.Fatalf("loading the recorded fixture: %v", err)
	}
	if described.Description != "the first payment" {
		t.Errorf("Description = %q, want %q", described.Description, "the first payment")
	}

	fallback, err := Load(filepath.Join(dir, "other.golden.json"))
	if err != nil {
		t.Fatalf("loading the recorded fixture: %v", err)
	}
	if fallback.Description != "the suite's description" {
		t.Errorf("Description = %q, want %q", fallback.Description, "the suite's description")
	}
}
