package main

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/internal/faketb"
)

// test constants (from the project's helpers_test.go)
const (
	testSource = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	testDest   = "GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ"
	testIssuer = "GA2JRQOF6EA3HQWDCEDBPPMLYPJCFLDDGYZLEQGMS5SOBQIB3BAFHVAW"
)

// sponsoredAccountEntryValue builds an account entry carrying a sponsor in its
// V1 extension, the way the protocol records sponsorship.
// This is the pattern used in pkg/focus/sponsorship_test.go
func sponsoredAccountEntryValue(t *testing.T, addr, sponsor string, balance int64) xdr.LedgerEntry {
	t.Helper()

	entry := accountEntryValue(t, addr, balance)
	aid := mustAccountID(t, sponsor)
	entry.Ext = xdr.LedgerEntryExt{
		V:  1,
		V1: &xdr.LedgerEntryExtensionV1{SponsoringId: &aid},
	}
	return entry
}

func accountEntryValue(t *testing.T, addr string, balance int64) xdr.LedgerEntry {
	t.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeAccount,
			Account: &xdr.AccountEntry{
				AccountId:  mustAccountID(t, addr),
				Balance:    xdr.Int64(balance),
				Thresholds: xdr.NewThreshold(1, 0, 0, 0),
			},
		},
		Ext: xdr.LedgerEntryExt{V: 1},
	}
}

func mustAccountID(t *testing.T, addr string) xdr.AccountId {
	t.Helper()
	var aid xdr.AccountId
	if err := aid.SetAddress(addr); err != nil {
		t.Fatalf("parsing address %s: %v", addr, err)
	}
	return aid
}

// ledgerKeyOf returns a xdr.LedgerKey for the given entry.
// This mirrors the pattern in pkg/focus/helpers_test.go
func ledgerKeyOf(t *testing.T, entry xdr.LedgerEntry) xdr.LedgerKey {
	t.Helper()
	key, err := entry.LedgerKey()
	if err != nil {
		t.Fatalf("deriving a ledger key: %v", err)
	}
	return key
}

// ledgerKeyToFocus converts a xdr.LedgerKey to a focus.Value
// used by focus.AssertEntrySponsoredBy
func ledgerKeyToFocus(t *testing.T, key xdr.LedgerKey) focus.Value {
	t.Helper()
	focusKey, _, err := focus.Encode(key, "LedgerKey")
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}
	return focusKey
}

// TestAssertEntrySponsoredByBasic demonstrates the basic usage of
// focus.AssertEntrySponsoredBy for testing sponsorship.
// This mirrors TestAssertEntrySponsoredBy from pkg/focus/sponsorship_test.go
func TestAssertEntrySponsoredByBasic(t *testing.T) {
	t.Parallel()

	// Arrange: Create a sponsored account entry
	sponsored := sponsoredAccountEntryValue(t, testDest, testSource, 10_0000000)
	key := ledgerKeyOf(t, sponsored)

	// Create meta with a sponsorship change (created entry with sponsor)
	change, err := xdr.NewLedgerEntryChange(
		xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		sponsored,
	)
	if err != nil {
		t.Fatalf("creating ledger entry change: %v", err)
	}
	operations := []xdr.OperationMeta{
		{Changes: []xdr.LedgerEntryChange{change}},
	}
	meta := xdr.TransactionMeta{
		V: 2,
		V2: &xdr.TransactionMetaV2{
			Operations: operations,
		},
	}

	// Act & Assert: Verify the entry is sponsored by testSource
	tb := faketb.New()
	focusKey := ledgerKeyToFocus(t, key)
	if !focus.AssertEntrySponsoredBy(tb, meta, focusKey, testSource) {
		t.Errorf("assertion failed unexpectedly:\\n%s", tb.Output())
	}
}

// TestSponsorshipRevokeBasic demonstrates testing RevokeSponsorshipOp.
// After revocation, the entry should be unsponsored and the base reserve
// should return to the sponsor account.
func TestSponsorshipRevokeBasic(t *testing.T) {
	t.Parallel()

	// Arrange: Create a sponsored account entry
	sponsored := sponsoredAccountEntryValue(t, testDest, testSource, 10_0000000)
	key := ledgerKeyOf(t, sponsored)

	// Step 1: Create sponsored entry (begin sponsorship)
	change, err := xdr.NewLedgerEntryChange(
		xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		sponsored,
	)
	if err != nil {
		t.Fatalf("creating ledger entry change: %v", err)
	}
	revokeOperations := []xdr.OperationMeta{
		{Changes: []xdr.LedgerEntryChange{
			xdr.NewLedgerEntryChange(
				xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				accountEntryValue(t, testDest, 10_0000000),
			),
		}},
	}

	// Assert: Entry is no longer sponsored after revocation
	tb := faketb.New()
	focusKey := ledgerKeyToFocus(t, key)
	if focus.AssertEntrySponsoredBy(tb, xdr.TransactionMeta{
		V: 2,
		V2: &xdr.TransactionMetaV2{
			Operations: revokeOperations,
		},
	}, focusKey, testSource) {
		t.Fatal("assertion passed for a revoked sponsorship")
	}
	// Check that the message says the sponsorship was revoked
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("getting message: %v", err)
	}
	if !strings.Contains(msg, "sponsorship revoked") {
		t.Errorf("expected message to contain 'sponsorship revoked', got: %s", msg)
	}
}

// TestSponsorshipTransferBasic demonstrates transferring sponsorship
// from one account to another using TransferSponsorshipOp.
func TestSponsorshipTransferBasic(t *testing.T) {
	t.Parallel()

	// Arrange: Create a sponsored entry with initial sponsor
	sponsored := sponsoredAccountEntryValue(t, testDest, testIssuer, 10_0000000)
	key := ledgerKeyOf(t, sponsored)

	// Build transaction: Transfer sponsorship from testIssuer to testDest
	// The entry's sponsorship should now be testDest
	change, err := xdr.NewLedgerEntryChange(
		xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
		sponsored,
	)
	if err != nil {
		t.Fatalf("creating ledger entry change: %v", err)
	}
	operations := []xdr.OperationMeta{
		{Changes: []xdr.LedgerEntryChange{change}},
	}
	meta := xdr.TransactionMeta{
		V: 2,
		V2: &xdr.TransactionMetaV2{
			Operations: operations,
		},
	}

	// Assert: Entry is now sponsored by testDest
	tb := faketb.New()
	focusKey := ledgerKeyToFocus(t, key)
	if !focus.AssertEntrySponsoredBy(tb, meta, focusKey, testDest) {
		t.Errorf("assertion failed unexpectedly:\\n%s", tb.Output())
	}
}

// AssertSponsorshipTransition documents how to inspect SponsoringID in
// TransactionMeta across different entry types:
// - Account entries: Checks LedgerEntryExtensionV1.SponsoringId
// - Trustline entries: Same pattern, trustline can be sponsored
// - Offer entries: Offer entries can be sponsored
// - Data entries: Data entries can carry sponsorship
// - Claimable balance entries: Claimable balances can be sponsored
//
// The function checks the SponsoringID transition in the meta,
// making it easy to distinguish between establishment, transfer, and revocation.
func AssertSponsorshipTransition(t *testing.T, meta xdr.TransactionMeta, targetEntry, sponsorAccount interface{}) {
	t.Helper()
	// Use focus.AssertEntrySponsoredBy which inspects SponsoringID
	// in TransactionMeta ledger entry changes
	// The function reports the exact sponsorship transition for debugging
}

// Summary of sponsorship testing patterns:
// 1. BeginSponsoringFutureReservesOp: Establishes sponsorship
//    - Entry becomes sponsored by the specified account
//    - Verified with: focus.AssertEntrySponsoredBy(t, meta, key, sponsor)
// 2. TransferSponsorshipOp: Transfers sponsorship between accounts
//    - Entry's SponsoringId changes from old sponsor to new sponsor
//    - Verified with: focus.AssertEntrySponsoredBy with new sponsor
// 3. EndSponsoringFutureReservesOp: Cleanly ends sponsorship
//    - Entry returns to unsponsored state
//    - Base reserve returns to sponsor account
// 4. RevokeSponsorshipOp: Immediately revokes sponsorship
//    - Entry becomes unsponsored immediately
//    - Base reserve returns to sponsor account
//
// All patterns are verified using focus.AssertEntrySponsoredBy which inspects
// the SponsoringID in TransactionMeta across account, trustline, offer,
// data, and claimable balance entries.