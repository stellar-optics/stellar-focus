package focus_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func closeMeta(version int32, protocol uint32, closeTime uint64) xdr.LedgerCloseMeta {
	header := xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{
		LedgerVersion: xdr.Uint32(protocol),
		ScpValue:      xdr.StellarValue{CloseTime: xdr.TimePoint(closeTime)},
	}}
	switch version {
	case 0:
		return xdr.LedgerCloseMeta{V: version, V0: &xdr.LedgerCloseMetaV0{LedgerHeader: header}}
	case 1:
		return xdr.LedgerCloseMeta{V: version, V1: &xdr.LedgerCloseMetaV1{LedgerHeader: header}}
	default:
		return xdr.LedgerCloseMeta{V: version, V2: &xdr.LedgerCloseMetaV2{LedgerHeader: header}}
	}
}

func TestLedgerCloseAssertionsAllVersions(t *testing.T) {
	for _, version := range []int32{0, 1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			meta := closeMeta(version, 20, 1_700_000_000)
			tb := faketb.New()
			if !focus.AssertLedgerCloseMetaProtocol(tb, meta, 20) || !focus.AssertLedgerCloseTime(tb, meta, 1_699_999_999, 1_700_000_001) {
				t.Fatalf("assertions failed: %s", tb.Output())
			}
		})
	}
}

func TestLedgerCloseTimeFailureShowsUTCAndUnix(t *testing.T) {
	tb := faketb.New()
	if focus.AssertLedgerCloseTime(tb, closeMeta(2, 20, 1_700_000_000), 1_700_000_001, 1_700_000_002) {
		t.Fatal("out-of-range close time passed")
	}
	for _, want := range []string{"1700000000", "2023-11-14T22:13:20Z"} {
		if !strings.Contains(tb.Output(), want) {
			t.Errorf("failure missing %q: %s", want, tb.Output())
		}
	}
}
