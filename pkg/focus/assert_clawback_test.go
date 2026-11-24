package focus_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestAssertClawbackClaimableBalanceMeta(t *testing.T) {
	id := testClaimableBalanceID()
	entry := claimableBalanceEntryValue(t, id, 500_0000000)
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(entry), removedChange(ledgerKeyOf(t, entry))})
	decodeMeta := func(encoded string) xdr.TransactionMeta {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var decoded xdr.TransactionMeta
		if err := decoded.UnmarshalBinary(raw); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	metaXDR := decodeMeta(meta)
	tb := faketb.New()
	if !focus.AssertClawbackClaimableBalanceMeta(tb, metaXDR, id) {
		t.Fatalf("removed balance was not accepted: %s", tb.Output())
	}

	tb = faketb.New()
	if focus.AssertClawbackClaimableBalanceMeta(tb, metaXDR, otherClaimableBalanceID()) {
		t.Fatal("different balance ID passed")
	}
	if !strings.Contains(tb.Output(), "made no ledger change") {
		t.Errorf("ID mismatch failure was unclear: %s", tb.Output())
	}

	tb = faketb.New()
	if focus.AssertClawbackClaimableBalanceMeta(tb, decodeMeta(metaV2(t)), id) {
		t.Fatal("missing deletion passed")
	}
	if !strings.Contains(tb.Output(), "made no ledger change") {
		t.Errorf("missing deletion failure was unclear: %s", tb.Output())
	}
}
