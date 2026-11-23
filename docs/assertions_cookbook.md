# Stellar Operation Assertions Cookbook

Copy-pasteable recipes for the classic Stellar operations a test suite most
often asserts on, using the idiomatic `focus.Assert…` calls. Each recipe states
the scenario, the code, and the failure you should expect when it goes red.

Amounts are in **stroops** (1 XLM = 10,000,000 stroops); assets are named either
as `"native"` / `"XLM"` or as `"CODE:ISSUER"`, where the issuer is the G…
address of the issuing account.

---

## Recipe 1: Assert a single payment

The payment itself is the assertion — who paid, to whom, how much, in which asset.

```go
focus.AssertPayment(t, envelope, 0, dest, amount, "native")
```

- `opIndex` is the zero-based operation index of the payment.
- `amount` is in stroops; the failure renders it in human units (`100.0000000 XLM`).
- `wantAsset` accepts `"native"`, `"XLM"`, or `"USDC:GA2J…HVAW"`.

It reports the mismatch through the standard failure message, with the field
path (`V1.Tx.Operations[0].Body.PaymentOp.Amount`) in the diff.

## Recipe 2: Assert a multi-hop payment chain

A payment that hops through several accounts is several payments. Assert the
operation sequence first, then each hop:

```go
focus.AssertOperationTypes(t, envelope, "payment", "payment")

focus.AssertPayment(t, envelope, 0, hop1, amount1, "native")
focus.AssertPayment(t, envelope, 1, hop2, amount2, "native")
```

`AssertOperationTypes` checks the discriminator and the order, using the
snake_case names Horizon uses (`"payment"`, `"change_trust"`, `"manage_offer"`,
`"create_claimable_balance"`, …). A fee-bump envelope is read through to the
inner transaction, so these assert on the inner one.

## Recipe 3: Assert a payment failed for a specific reason

Production failures are the interesting ones: the payment did not land because
of the reason the failure report names.

```go
focus.AssertOperationFailed(t, result, 0, "payment_no_trust")
```

`AssertOperationFailed` takes the transaction result, the operation index and
Horizon's short code. The full set of result codes is covered by
`AssertResultEqual`, `AssertResultCode` and `AssertFailedOperations`, and each
short code carries a plain-English explanation and hint in the failure output.

## Recipe 4: Assert account creation and its starting balance

`AssertCreateAccount` checks the destination and the starting balance — the
balance the account holds the moment it is created, before any fee is taken:

```go
focus.AssertCreateAccount(t, envelope, 0, dest, 20_0000000)
```

## Recipe 5: Assert trustline creation and limit adjustments

Trustlines are asserted three ways, because the operation, the ledger entry and
the flags are different questions:

```go
// The operation: which asset, and to what limit.
focus.AssertChangeTrust(t, envelope, 0, "USDC:GA2J…HVAW", 1000_0000000)

// The ledger entry: the trustline moved from one limit to another.
focus.AssertTrustlineModified(t, meta, account, "USDC:GA2J…HVAW", 1000_0000000)

// The flags: authorized / issued / balances, if the test cares which arm moved.
focus.AssertTrustlineFlags(t, meta, account, "USDC:GA2J…HVAW", wantFlags)
```

The largest possible limit means "no limit"; it is reported as such rather than
as a raw number.

## Recipe 6: Assert an account merge and the remaining reserves

An account merge removes the merged (destination) account entry and leaves the
survivor holding the funds. Assert both ends of that on the meta:

```go
// The merged account's entry is gone.
focus.AssertEntryDeleted(t, meta, ledgerkey.Account(mergedAddr))

// The surviving account's balance absorbed the funds; the delta is the
// amount that moved, in stroops, net of fees.
focus.AssertBalanceChanged(t, meta, source, "native", delta)
```

The balance delta is the net change across every ledger change in the meta, so
a merge that also charged a fee is accounted for without the test adding the
fee by hand.

## Recipe 7: Assert DEX offer placement

An offer is placed with `manage_offer`; the assertion reads it from the meta:

```go
focus.AssertOfferCreated(t, meta, focus.Offer{
    Seller:   seller,
    Selling: "native",
    Buying:   "USDC:GA2J…HVAW",
    Amount:   100_0000000,          // amount of Selling still on the book
    Price:    "3/2",                 // Buying per Selling, as N/D
})
```

To assert that a specific offer operation is or is not passive:

```go
focus.AssertPassiveOffer(t, op, true) // or false: an order-book offer
```

## Recipe 8: Assert DEX trade execution

A trade is asserted from the transaction result — the claim atoms carry both
halves of the match — or from the meta when only the offers that changed matter:

```go
focus.AssertTradeExecuted(t, result, focus.Trade{
    OfferID:      42,
    Sold:         "native",
    Bought:       "USDC:GA2J…HVAW",
    AmountSold:   100_0000000,
    AmountBought: 150_0000000,
})

// Or: the price ratio of the matched offer, exactly, as a fraction.
focus.AssertOfferPriceRatio(t, op, 3, 2) // numerator/denominator
```

The amounts are read from the matched side's point of view — `Sold` is what the
offer gave up, `Bought` what it received.

## Recipe 9: Assert claimable balance creation and claimant predicate

```go
focus.AssertClaimableBalanceCreated(t, meta, claimant, amount)
```

The claimant predicate itself is asserted separately, positionally against the
claimant index of the balance:

```go
focus.AssertClaimPredicateTree(t, opOrEntry, claimantIndex, predicate)
```

A balance claimable by either of two accounts is claimable by each of them, and
a balance restored from the Soroban archival state counts as created.

## Recipe 10: Assert memo presence and value

```go
focus.AssertMemo(t, envelope, "text: invoice-42") // or id:/hash: forms
focus.AssertNoMemo(t, envelope)
focus.AssertMemoText(t, envelope, "invoice-42")
focus.AssertMemoHash(t, envelope, hash)
focus.AssertMemoID(t, envelope, 42)
```

Memos are written as `"text: …"`, `"id: …"` or `"hash: …"`.

## Recipe 11: Assert the fee

```go
focus.AssertFee(t, envelope, 100) // stroops
```

## Recipe 12: Assert the operation source account

An operation may name its own source account, which then replaces the
transaction's source for that operation alone; an operation that names none
inherits the transaction's:

```go
focus.AssertOperationSourceAccount(t, envelope, 0, "GABC…") // empty want: no override
focus.AssertSourceAccount(t, envelope, "GABC…")
```

---

## Building a table of cases

Recipes compose into table-driven suites, one row per transaction under test:

```go
tests := []struct {
    name string
    envelope string
}{
    {"mainnet_payment", capturedXDR},
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        focus.AssertOperationTypes(t, tt.envelope, "payment")
        focus.AssertPayment(t, tt.envelope, 0, dest, amount, "native")
    })
}
```

For golden-file based suites, `focus.AssertEnvelopeEqual` plus
`focus.IgnoreVolatile()` is the usual pair — see the
[Ignore Rules Reference](ignore_rules_reference.md).
