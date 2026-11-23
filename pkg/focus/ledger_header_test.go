package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// headerWithFields builds a ledger header carrying the network parameters the
// header assertions read.
func headerWithFields(seq, baseFee, baseReserve uint32) xdr.LedgerHeader {
	return xdr.LedgerHeader{
		LedgerSeq:   xdr.Uint32(seq),
		BaseFee:     xdr.Uint32(baseFee),
		BaseReserve: xdr.Uint32(baseReserve),
	}
}

func TestAssertLedgerHeaderPasses(t *testing.T) {
	t.Parallel()

	header := headerWithFields(8100, 100, 5000)
	entry := xdr.LedgerHeaderHistoryEntry{Header: header}

	for _, tc := range []struct {
		name  string
		value focus.Value
	}{
		{"header", header},
		{"pointer to header", &header},
		{"history entry", entry},
		{"pointer to history entry", &entry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertLedgerHeader(tb, tc.value, 8100, 100, 5000) {
				t.Fatalf("AssertLedgerHeader reported a mismatch:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("passing assertion reported a failure:\n%s", tb.Output())
			}
		})
	}
}

func TestAssertLedgerHeaderReportsEveryMismatch(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if focus.AssertLedgerHeader(tb, headerWithFields(8100, 100, 5000), 8101, 200, 10000) {
		t.Fatal("AssertLedgerHeader passed for three wrong values")
	}
	if got := tb.ErrorCount(); got != 3 {
		t.Errorf("reported %d mismatches, want 3:\n%s", got, tb.Output())
	}

	out := tb.Output()
	for _, want := range []string{
		"sequence is 8100, want 8101",
		"base fee is 100 (0.0000100 XLM), want 200 (0.0000200 XLM)",
		"base reserve is 5000 (0.0005000 XLM), want 10000 (0.0010000 XLM)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure is missing %q:\n%s", want, out)
		}
	}
}

func TestAssertLedgerHeaderRejectsOtherValues(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if focus.AssertLedgerHeader(tb, "not xdr", 1, 100, 5000) {
		t.Fatal("AssertLedgerHeader passed for an unreadable value")
	}
	if tb.ErrorCount() == 0 {
		t.Fatal("unreadable value reported no failure")
	}
}
