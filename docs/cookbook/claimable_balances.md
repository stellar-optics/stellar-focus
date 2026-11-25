# Testing claimable balance lifecycles

A claimable balance separates creating a payment from receiving it. The
creation transaction puts a `ClaimableBalanceEntry` in the ledger; a later
claim or clawback removes that entry. Tests should use transaction metadata to
check the ledger changes, and the transaction result to check whether an
operation succeeded.

The examples use the assertions in
[`pkg/focus/claimable.go`](../../pkg/focus/claimable.go) and
[`pkg/focus/clawback.go`](../../pkg/focus/clawback.go).

## Assert creation

`AssertClaimableBalanceCreated` looks for a created or restored balance in
`TransactionMeta`, then checks that the specified account is one of its
claimants and that its amount matches. Amounts are raw stroops:

```go
focus.AssertClaimableBalanceCreated(t, createMeta, claimant, 25_0000000)
```

The assertion accepts a strkey account address, `xdr.AccountId`, or
`xdr.MuxedAccount` as the claimant. It does not assert an asset, balance ID,
the complete claimant list, or an exact predicate tree. A matching claimant
may be one of several claimants, and a restored entry counts as created.
Failure output lists created balance IDs, assets, amounts, claimants, and
human-readable predicates to help diagnose an unexpected entry.

When those additional fields are part of the contract under test, select the
created entry from the metadata in the test (including the appropriate
`TransactionMeta` version) and compare the expected and actual
`xdr.ClaimableBalanceEntry` with `focus.AssertEqualAs`:

```go
want := xdr.ClaimableBalanceEntry{
    BalanceId: expectedID,
    Claimants: expectedClaimants,
    Asset:     expectedAsset,
    Amount:    xdr.Int64(25_0000000),
    // Set any other fields required by the fixture.
}
got := createdClaimableBalance(t, createMeta, expectedID) // test-owned metadata selector
focus.AssertEqualAs(t, "ClaimableBalanceEntry", want, got)
```

`createdClaimableBalance` above represents a test helper that finds the
matching created ledger change. Comparing the complete entry checks the
balance ID, asset, amount, claimant order, and each claimant's predicate;
compare selected fields instead if irrelevant XDR extension fields should not
be part of the test.

## Understand predicate trees

Each V0 claimant has one `ClaimPredicate`. The predicate is part of the
claimant's eligibility rule; it is not evaluated by
`AssertClaimableBalanceCreated`. Eligibility can depend on ledger close time,
so an offline assertion can confirm the stored tree but should not guess
whether it is true at some future ledger.

For example, claimant A can have an absolute-time predicate while claimant B
has a compound predicate requiring both a relative-time condition and the
negation of another absolute-time condition:

```text
claimant A
└── BEFORE_ABSOLUTE_TIME(1_800_000_000)

claimant B
└── AND
    ├── BEFORE_RELATIVE_TIME(86_400)
    └── NOT
        └── BEFORE_ABSOLUTE_TIME(1_700_000_000)
```

The logical operators compose child predicates as follows:

```text
AND: every child must hold            OR: at least one child must hold
└── predicate 1                       ├── predicate 1
└── predicate 2                       └── predicate 2

NOT: the child condition is negated    BEFORE_ABSOLUTE_TIME(t):
└── predicate                          claim must occur before time t
```

`ClaimPredicateTypeClaimPredicateAnd`, `...Or`, and `...Not` are nested XDR
unions; `ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime` carries the
absolute Unix timestamp. The XDR tree's child order is significant when
checking the serialized entry. `claimable.go` renders these forms in failure
messages but does not expose a predicate-specific assertion, so use the
entry-level comparison above when the exact tree matters.

## Claim and revoke

To prove that a claim operation succeeded, check its type in the envelope and
its result code in the transaction result. Then check that the corresponding
balance was removed in the transaction metadata:

```go
focus.AssertOperationTypes(t, claimEnvelope, "claim_claimable_balance")
focus.AssertOperationResultCode(t, claimResult, 0, "claim_claimable_balance_success")
focus.AssertClaimableBalanceRevoked(t, claimMeta, balanceID)
```

`AssertClaimableBalanceRevoked` requires a removal of that balance's ledger
entry. A claim and a clawback both remove a claimable balance, so the removal
assertion alone does not identify which operation caused it. Pair it with the
operation type and successful result when testing a claim; for a clawback,
also use `AssertClawbackClaimableBalanceOp`.

The result and metadata are separate evidence: a successful operation result
does not identify the removed balance, while a removed entry alone does not
establish that the transaction claimed it rather than clawed it back.
