# Sponsorship Testing Cookbook

This cookbook documents how to test Stellar sponsorship operations (CAP-0033),
including `BeginSponsoringFutureReservesOp`, `EndSponsoringFutureReservesOp`,
and `RevokeSponsorshipOp`.

## Overview

Stellar Protocol 14 introduced sponsorship, enabling an account to pay the base
reserve for entries owned by another account. Testing sponsorship operations
involves asserting across three distinct lifecycle phases:

1. **Establishing sponsorship** - `BeginSponsoringFutureReservesOp`
2. **Transferring sponsorship** - transferring the sponsor role
3. **Revoking sponsorship** - `RevokeSponsorshipOp`

The core assertion function is `focus.AssertEntrySponsoredBy`, which inspects
the `SponsoringID` in `TransactionMeta` ledger entry changes to verify
sponsorship transitions.

---

## 1. Testing BeginSponsoringFutureReservesOp & EndSponsoringFutureReservesOp

### Sandwich Operations

A common pattern in sponsorship testing is the "sandwich" pattern: a
`BeginSponsoringFutureReservesOp` is followed by operations that use the
sponsored entries, and then an `EndSponsoringFutureReservesOp` that cleanly
revokes the sponsorship. Every `begin` operation must have a strictly paired
`end` operation.

### Assertion Pattern

To verify a complete sponsorship sandwich:

```go
// 1. Begin sponsorship: account A begins sponsoring account B's entries
// 2. Perform operations using the sponsored entries
// 3. End sponsorship: account A ends sponsorship of account B's entries

// Assert the entry is sponsored by the expected account after begin
focus.AssertEntrySponsoredBy(t, meta, key, sponsorAddr)

// After end sponsorship, the entry should no longer be sponsored
// (or sponsored by a different account, depending on state)
```

### Key Verification

- Every `BeginSponsoringFutureReservesOp` must have a corresponding
  `EndSponsoringFutureReservesOp` in the same transaction
- The meta changes should show a sponsorship transition from "established"
  to "revoked" for the same entry
- Use `focus.AssertEntrySponsoredBy` before and after to verify the lifecycle

---

## 2. Verifying Ledger Meta Changes with AssertSponsorshipTransition

The `focus.AssertEntrySponsoredBy` function inspects the `SponsoringID` field
in `TransactionMeta` across various entry types. Here's how it works for
different ledger entry types:

### Account Entries

After a sponsorship operation, the account entry's `LedgerEntryExtensionV1`
will contain the sponsor's `SponsoringId`. The assertion checks that the entry
is sponsored by the expected account.

### Trustline Entries

Trustline entries carry sponsorship in the same way. After a
`BeginSponsoringFutureReservesOp` for a trustline, the entry's
`SponsoringId` will be set to the sponsor's account ID.

### Offer Entries

Offer entries can also be sponsored. The `SponsoringId` in the offer's
extension indicates who pays the reserve.

### Data Entries

Data entries holding custom data can be sponsored similarly. The sponsorship
transition is recorded in the meta's ledger entry changes.

### Claimable Balance Entries

Claimable balance entries can be sponsored as well. The `SponsoringId`
indicates the sponsor, and the assertion will verify the sponsorship state.

### Inspecting SponsoringID in TransactionMeta

The `focus.AssertEntrySponsoredBy` function works by:

1. Parsing the `TransactionMeta` to find ledger entry changes
2. Looking for changes to the target entry identified by its ledger key
3. Reading the `SponsoringId` from the entry's `V1` extension
4. Comparing the sponsor against the expected account

The function reports the exact sponsorship transition, e.g.:

- `"sponsorship established by GA2JR...HVAW"`
- `"sponsorship transferred from GA2JR...HVAW to GB5K...YXZ"`
- `"sponsorship revoked from GA2JR...HVAW"`

This makes it easy to distinguish between establishment, transfer, and revocation
in test failure messages.

---

## 3. Testing RevokeSponsorshipOp and Reserve Return

### RevokeSponsorshipOp

When `RevokeSponsorshipOp` is applied, the base reserve that was previously
locked with the sponsored entries returns to the sponsor's account. Testing
this requires verifying:

1. The sponsorship is revoked (entry no longer has `SponsoringId`)
2. The sponsor's balance increases by the base reserve amount
3. The sponsored account can now pay its own reserves

### Assertion Pattern

```go
// Before revocation: entry is sponsored
focus.AssertEntrySponsoredBy(t, meta, key, sponsorAddr)

// After revocation: entry should be unsponsored
// The entry's SponsoringId should be nil
```

### Verifying Reserve Return

To verify the base reserve returns to the sponsor:

1. Record the sponsor's balance before the transaction
2. After the transaction, check that the sponsor's account balance has
   increased by exactly the base reserve (currently 500 stroops, or 0.00005 XLM)
3. Verify the entry is no longer sponsored

The `focus.AssertEntrySponsoredBy` function will report `"sponsorship revoked
from <sponsor>"` in the failure message if the revocation did not occur as
expected, making it easy to debug.

---

## 4. Complete Lifecycle Test Example

Here's a complete test demonstrating the full sponsorship lifecycle:

```go
func TestSponsorshipLifecycle(t *testing.T) {
    t.Parallel()
    
    // Arrange: Set up sponsored account and sponsor
    sponsor := "GB7G ... sponsor address"
    sponsored := "GABC ... sponsored address"
    
    // Act: Create a transaction that begins sponsorship,
    // performs operations, then ends sponsorship
    txBuilder := buildSponsorshipLifecycleTx(sponsor, sponsored)
    txBytes, err := txBuilder.Build()
    if err != nil {
        t.Fatalf("building transaction: %v", err)
    }
    
    // Submit and get meta
    meta := submitTransaction(txBytes)
    
    // Get the sponsored account's ledger key
    key := ledgerKeyOf(t, sponsored)
    
    // Assert: Entry is sponsored after begin
    focus.AssertEntrySponsoredBy(t, meta, key, sponsor)
    
    // Assert: After end sponsorship, entry is no longer sponsored
    // This would need a separate transaction or meta inspection
}
```

---

## 5. Common Patterns and Gotchas

### Ensure Every Begin Has a Paired End

When testing sponsorship sandwiches, always verify:

1. The transaction contains both `BeginSponsoringFutureReservesOp` and
   `EndSponsoringFutureReservesOp`
2. The meta changes show a complete cycle: unsponsored → sponsored → unsponsored
3. No orphaned sponsorship exists (every `begin` has a corresponding `end`)

### Check Entry Types

Different entry types (account, trustline, offer, data, claimable balance)
all carry sponsorship the same way via `SponsoringId` in their `V1`
extension. The `focus.AssertEntrySponsoredBy` function works uniformly across
all entry types.

### Read the Transition, Not Just the State

Because a `RevokeSponsorshipOp` can transfer, establish, or revoke a sponsorship
depending on the entry's state, always inspect the `SponsoringId` transition
in the meta rather than assuming the result code tells the full story.

### Use Helper Functions

The codebase provides helpful functions in `pkg/focus/sponsorship.go`:

- `focus.AssertEntrySponsoredBy` - main assertion function
- `entrySponsorship` - reads sponsorship from an entry
- `describeSponsorshipTransition` - describes what the transaction did

These helpers ensure you get consistent, readable error messages when
assertions fail.

---

## 6. Running the Tests

To run the existing sponsorship tests:

```bash
go test -v ./pkg/focus/ -run TestAssertEntrySponsoredBy
```

To add new tests, follow the patterns established in
`pkg/focus/sponsorship_test.go` and add your test cases to
`examples/sponsorship_test.go` or your own test file.

---