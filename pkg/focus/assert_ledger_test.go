package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// buildLedgerCloseMeta builds a LedgerCloseMeta of the given union arm (0, 1
// or 2) with the given protocol version and close time, and nothing else —
// every other field is left at its zero value, since the assertions under
// test never read them.
func buildLedgerCloseMeta(t *testing.T, v int32, protocol uint32, closeTimeUnix int64) xdr.LedgerCloseMeta {
	t.Helper()

	header := xdr.LedgerHeaderHistoryEntry{
		Header: xdr.LedgerHeader{
			LedgerVersion: xdr.Uint32(protocol),
			LedgerSeq:     xdr.Uint32(1_000_000),
			ScpValue: xdr.StellarValue{
				CloseTime: xdr.TimePoint(closeTimeUnix),
			},
		},
	}

	switch v {
	case 0:
		return xdr.LedgerCloseMeta{V: 0, V0: &xdr.LedgerCloseMetaV0{LedgerHeader: header}}
	case 1:
		return xdr.LedgerCloseMeta{V: 1, V1: &xdr.LedgerCloseMetaV1{LedgerHeader: header}}
	case 2:
		return xdr.LedgerCloseMeta{V: 2, V2: &xdr.LedgerCloseMetaV2{LedgerHeader: header}}
	default:
		t.Fatalf("buildLedgerCloseMeta: unsupported arm %d", v)
		return xdr.LedgerCloseMeta{}
	}
}

func TestAssertLedgerCloseMetaProtocol(t *testing.T) {
	t.Parallel()

	for _, arm := range []int32{0, 1, 2} {
		meta := buildLedgerCloseMeta(t, arm, 22, 1_700_000_000)

		tb := faketb.New()
		if !focus.AssertLedgerCloseMetaProtocol(tb, meta, 22) {
			t.Errorf("arm V%d: expected protocol match to pass:\n%s", arm, tb.Output())
		}

		tb = faketb.New()
		if focus.AssertLedgerCloseMetaProtocol(tb, meta, 23) {
			t.Errorf("arm V%d: expected protocol mismatch to fail", arm)
		}
		if !strings.Contains(tb.Output(), "protocol version 22, want 23") {
			t.Errorf("arm V%d: failure message does not name both versions:\n%s", arm, tb.Output())
		}
	}
}

func TestAssertLedgerCloseTime(t *testing.T) {
	t.Parallel()

	for _, arm := range []int32{0, 1, 2} {
		meta := buildLedgerCloseMeta(t, arm, 22, 1_700_000_500)

		tb := faketb.New()
		if !focus.AssertLedgerCloseTime(tb, meta, 1_700_000_000, 1_700_001_000) {
			t.Errorf("arm V%d: expected close time within bounds to pass:\n%s", arm, tb.Output())
		}

		// The bounds are inclusive.
		tb = faketb.New()
		if !focus.AssertLedgerCloseTime(tb, meta, 1_700_000_500, 1_700_000_500) {
			t.Errorf("arm V%d: expected an exact bound to pass:\n%s", arm, tb.Output())
		}

		tb = faketb.New()
		if focus.AssertLedgerCloseTime(tb, meta, 1_700_001_000, 1_700_002_000) {
			t.Errorf("arm V%d: expected a close time before the window to fail", arm)
		}
		if !strings.Contains(tb.Output(), "2023-11-14T22:21:40Z") {
			t.Errorf("arm V%d: failure message does not render the close time as RFC3339:\n%s",
				arm, tb.Output())
		}
	}
}

func TestAssertLedgerCloseTimeRejectsReversedBounds(t *testing.T) {
	t.Parallel()

	meta := buildLedgerCloseMeta(t, 0, 22, 1_700_000_500)
	tb := faketb.New()
	if focus.AssertLedgerCloseTime(tb, meta, 100, 50) {
		t.Fatal("expected reversed bounds to fail")
	}
	if !strings.Contains(tb.Output(), "bounds are reversed") {
		t.Errorf("failure message does not explain the reversed bounds:\n%s", tb.Output())
	}
}
