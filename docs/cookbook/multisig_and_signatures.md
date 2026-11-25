# Testing multisig accounts and signatures

Stellar account state has a master-key weight, four signing thresholds, and a
set of secondary signers. A transaction envelope separately carries decorated
signatures. `stellar-focus` provides assertions for both views:

- `AssertThresholdsEqual` and `AssertSignersEqual` inspect the account entry
  left in transaction metadata.
- `AssertSignaturesPresent`, `AssertSignatureThreshold`, and `AssertSignedBy`
  inspect signatures attached to an envelope.

These examples follow the cases in
[`pkg/focus/signers_test.go`](../../pkg/focus/signers_test.go) and
[`pkg/focus/signatures_test.go`](../../pkg/focus/signatures_test.go).
The exported assertion names used here are `AssertThresholdsEqual` and
`AssertSignersEqual`; there are no separate `AssertAccountThresholds` or
`AssertSignerWeight` functions in the current API.

## Assert account thresholds

Use `AssertThresholdsEqual` to check all four values. The order is master
weight, low threshold, medium threshold, and high threshold:

```go
focus.AssertThresholdsEqual(t, meta, accountID, focus.Thresholds{
    Master: 1,
    Low:    2,
    Medium: 3,
    High:   4,
})
```

`accountID` may be a strkey account address, `xdr.AccountId`, or
`xdr.MuxedAccount`. The assertion reads the account entry from the transaction
metadata, so the transaction must have changed that account. The master key is
the account's own key; it is not included in the secondary signer list.

## Check added, changed, and removed signer weights

`AssertSignersEqual` compares the complete secondary signer set and weights
left on the account by the transaction:

```go
focus.AssertSignersEqual(t, meta, accountID, map[string]uint32{
    signerA: 2,
    signerB: 1,
})
```

The map keys are signer strkeys and the values are their weights. This
post-transaction assertion covers a `SetOptionsOp` that added or modified a
signer, and also detects a removed signer because it will not be present in the
expected set. It checks the resulting account entry, not the operation in
isolation; use operation assertions as well when the test must prove which
operation caused the change. A `nil` map expects no secondary signers.

## Hash, pre-authorized transaction, and signed-payload signers

The account-state assertion accepts signer strkeys for the signer kinds
supported by Stellar. In particular, hash-X signers use `X…`, pre-authorized
transaction signers use `T…`, and Ed25519 signed-payload signers use `P…`.
An `X…` key represents a hash-X signer (the hash of its preimage); a `T…` key
represents the transaction hash authorized in advance. These are key forms,
not separate assertion functions.
They can be included in the same exact-set check:

```go
focus.AssertSignersEqual(t, meta, accountID, map[string]uint32{
    hashXSigner:         2, // X…
    preAuthTxSigner:     1, // T…
    signedPayloadSigner: 1, // P…
})
```

The envelope assertions can check that signatures attributed to expected
signers are present. `AssertSignaturesPresent` accepts these same strkeys
(among other supported forms):

```go
focus.AssertSignaturesPresent(t, envelope,
    hashXSigner,
    preAuthTxSigner,
    signedPayloadSigner,
)

// When any two of three known signers may satisfy the policy:
focus.AssertSignatureThreshold(t, envelope, 2, signerA, signerB, signerC)
```

`AssertSignedBy` is a string-only alternative when all listed signer strkeys
must be represented in the envelope. SEP-0040 signed-payload keys are
represented by their `P…` strkey; there is no separate
`AssertSignedPayloadSigner` function. These assertions match the four-byte
signature hints derived from signer keys; they do not cryptographically verify
signatures or the signed payload. A test that must prove signature validity
needs the appropriate verification context, including the network passphrase
and payload.
