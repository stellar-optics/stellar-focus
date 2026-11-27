# Pull Request: Sponsorship Testing Documentation and Tests

## Summary

This PR adds documentation and test examples for testing Stellar sponsorship operations (CAP-0033), including `BeginSponsoringFutureReservesOp`, `EndSponsoringFutureReservesOp`, and `RevokeSponsorshipOp`.

## Changes Made

### 1. New File: `docs/cookbook/sponsorship_testing.md`

Created comprehensive documentation covering:

- **Sandbox Operations**: Documentation of `BeginSponsoringFutureReservesOp` and `EndSponsoringFutureReservesOp` with the sandwich pattern pattern (every begin must have a strictly paired end)
- **Ledger Meta Changes**: Explanation of how `focus.AssertEntrySponsoredBy` inspects `SponsoringID` in `TransactionMeta` across account, trustline, offer, data, and claimable balance entries
- **Revocation Assertions**: How to test `RevokeSponsorshipOp` and assert that the base reserve returns to the sponsor account
- **Complete Lifecycle**: Full sponsorship lifecycle testing patterns
- **Common Patterns and Gotchas**: Ensuring every begin has a paired end, checking entry types, reading transitions rather than just state

### 2. New File: `examples/sponsorship_test.go`

Created runnable test examples demonstrating:

- `TestAssertEntrySponsoredByBasic`: Basic usage of `focus.AssertEntrySponsoredBy`
- `TestSponsorshipRevokeBasic`: Testing `RevokeSponsorshipOp` and verifying base reserve return
- `TestSponsorshipTransferBasic`: Testing sponsorship transfer between accounts
- `AssertSponsorshipTransition`: Documentation function for inspecting SponsoringID transitions

The test examples follow the existing patterns from `pkg/focus/sponsorship_test.go` and `pkg/focus/helpers_test.go`, using the project's test constants and helper functions.

## Acceptance Criteria

- [x] `docs/cookbook/sponsorship_testing.md` created with complete, runnable code examples
- [x] `examples/sponsorship_test.go` with runnable test cases
- [x] Coverage of both sandwich operations and ledger entry metadata transitions

## Testing

To run the sponsorship tests:

```bash
go test -v ./examples/ -run TestSponsorship
```

The documentation provides detailed patterns for:
- Testing sandwich operations (begin/end pair)
- Verifying ledger meta changes with `AssertSponsorshipTransition`
- Testing revocation and base reserve return
- Distinguishing between establishment, transfer, and revocation in failure messages