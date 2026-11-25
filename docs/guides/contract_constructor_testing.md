# Testing Soroban contract constructors

Soroban runs a contract's optional `__constructor` during deployment. A
successful constructor runs atomically with the deployment transaction, so
tests can verify its effects from that transaction's metadata without another
RPC request or a later ledger snapshot.

The examples below use `focus.AssertConstructorState` and
`focus.AssertContractOwner`. They follow the cases in
[`pkg/focus/constructor_test.go`](../../pkg/focus/constructor_test.go).

## What deployment metadata records

There is no separate “constructor was called” record in transaction metadata.
Instead, the successful deployment's `TransactionMetaV3` records the ledger
changes produced by the transaction: creation of the contract-instance entry,
plus changes for contract data written by the constructor. In V3, these changes
can appear in `TxChangesBefore`, per-operation `Operations[].Changes`, or
`TxChangesAfter`. `stellar-focus` reads the transaction changes across the
metadata versions it supports.

The instance entry contains the contract instance and its instance storage.
Persistent and temporary storage are represented by separate contract-data
entries. The instance entry is also how the assertion distinguishes a
deployment from an unrelated transaction that happens to change contract data.

## Assert the owner and initialized storage

`AssertContractOwner` checks the conventional `owner` storage key for an
account or contract address. Use `AssertConstructorState` when the owner uses a
different key or when the test should check other constructor values too:

```go
// For a contract that stores its owner under "owner":
focus.AssertContractOwner(t, deployMeta, contractID, adminAddress)

// For a contract that stores its admin under "admin":
focus.AssertConstructorState(t, deployMeta, focus.ConstructorState{
    ContractID: contractID,
    Owner:      adminAddress,
    OwnerKey:   "admin",
    Entries: map[string]any{
        "decimals": uint32(7),
        "name":     "FocusToken",
        "supply":   uint64(1_000_000),
        "paused":   false,
    },
})
```

`ContractID` is the deployed contract's `C…` strkey. `Owner` is the expected
account (`G…`) or contract (`C…`) address. `OwnerKey` defaults to `"owner"`;
keys are matched case-insensitively. Leave `Owner` empty to skip the owner
check.

`Entries` checks each named key against the state found in the instance,
persistent, or temporary storage. Values may be Go booleans, integers, and
strings, or XDR `ScVal` values and supported encoded forms. A strkey string
value is treated as an address. The assertion checks only the keys supplied:
other constructor-written keys are allowed. A mismatch reports the key,
expected and actual values, and the storage location.

## Test deployment failures

Malformed constructor arguments can be rejected before the constructor
finishes; failed authorization can also trap the deployment. Such a deployment
does not provide successful initialized state to assert. Test the failure from
the result or diagnostics instead of expecting `AssertConstructorState` to
pass:

```go
// When the Soroban error is present in the transaction metadata:
focus.AssertContractError(t, failedMeta, xdr.ScErrorTypeContract, expectedCode)

// When checking a trap recorded in the diagnostics:
focus.AssertContractPanicReason(t, failedMeta, expectedReason)
```

Use the error category and code (or diagnostic reason) that the contract and
host actually produce for the case under test. Argument-decoding failures may
occur before a contract error is recorded; in that case assert the failed
operation/result using the result value available to the test. Do not infer a
successful initialization from the absence of a particular storage key.

These are separate checks: a failure assertion establishes why deployment
failed, while a constructor-state assertion checks the state left by a
successful deployment.
