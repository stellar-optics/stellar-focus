# Ledger Keys & State Snapshot Inspection

`pkg/ledgerkey` builds the base64-encoded `LedgerKey` that addresses one entry
in the ledger state, and every state assertion in `pkg/focus` is keyed the same
way. This guide is the reference for both halves: how to build a key from values
a test already holds, and how to assert on the state a transaction left behind.

The failure message is the product: when an assertion reports that no ledger
entry changed, the message lists what the transaction did change, so a red test
says "your key was wrong" or "the entry did not move" without either of them
being a guess.

---

## Why a LedgerKey

A ledger entry is addressed by a binary key the protocol derives from the
entry's contents. The key is a union — which *kind* of entry it is selects the
arm, and the arm's fields carry the identity. Assembling those unions by hand
means filling in nested union arms and pointers for every kind, which is exactly
the kind of code that drifts from the SDKs and breaks silently.

`pkg/ledgerkey` does the assembly once and hands back the canonical base64 XDR:

- it is the key `focus capture ledger-entry <base64-ledger-key>` reads back
  through a source;
- it is the form `capture.Source` matches recorded ledger entries by;
- it is what `focus.AssertLedgerKeyEqual` compares a package-built key against.

```go
key, err := ledgerkey.Account(address)
// key: base64 XDR of the account's LedgerKey, e.g. "AAAAAAAB..."

key, err = ledgerkey.Trustline(address, asset)
key, err = ledgerkey.ContractData(contractID, "counter", xdr.ContractDataDurabilityPersistent)
key, err = ledgerkey.ContractCode(hash)
key := ledgerkey.ConfigSetting(xdr.ConfigSettingIdConfigSettingStateArchival)
key := ledgerkey.EvictionIterator()
```

## Key builders

### Account

```go
key, err := ledgerkey.Account("GA5XIGA5C7Z4…")
```

The account key wraps the `AccountId` the address decodes to. Muxed accounts
name the underlying account, so a muxed address builds the same key as its
G… form.

### Trustline

```go
key, err = ledgerkey.Trustline(address, asset)
```

The asset must be native or a credit asset; a liquidity pool share asset is not
a trustline key and the builder rejects it.

### ContractData

```go
key, err = ledgerkey.ContractData(
    "CBG…",                 // contract id, strkey "C…"
    "counter",              // key: ScVal, base64 XDR, raw XDR bytes, or a plain string
    xdr.ContractDataDurabilityPersistent,
)
```

The durability argument selects the storage arm of the contract-data key:
`xdr.ContractDataDurabilityPersistent` or
`xdr.ContractDataDurabilityTemporary`. A temporary entry expires with the
state archival window; a persistent one does not.

### ContractCode

```go
key, err = ledgerkey.ContractCode(hash) // hash is [32]byte
```

### ConfigSetting and EvictionIterator

Config settings are addressed by their `ConfigSettingID` alone — the id is what
identifies the upgradeable value:

```go
key := ledgerkey.ConfigSetting(xdr.ConfigSettingIdConfigSettingStateArchival)
cursor := ledgerkey.EvictionIterator() // the CONFIG_SETTING_EVICTION_ITERATOR cursor
```

Every id in the `ConfigSettingId` enum builds a key; an id the enum does not
define is not a setting the protocol can hold, so the builder returns the empty
string for it.

## Durability: temporary vs persistent

The contract-data key is a union over durability, and the two arms are not
interchangeable:

```go
// persistent: survives until the entry is explicitly removed
key, err = ledgerkey.ContractData(contractID, keyValue, xdr.ContractDataDurabilityPersistent)

// temporary: evicted with the archival window
key, err = ledgerkey.ContractData(contractID, keyValue, xdr.ContractDataDurabilityTemporary)
```

An entry restored from the Soroban archival state counts as created — it is new
to the live ledger — and an entry removed by the transaction counts as deleted.

## Asserting on ledger state

### `AssertLedgerKeyEqual`

Compares a key built by `pkg/ledgerkey` against one read out of a transaction
meta or a captured fixture, structurally, with the failure naming the field
that differed:

```go
focus.AssertLedgerKeyEqual(t, want, got)
```

### Lifecycle transitions

`AssertLedgerEntryState` asks whether the entry identified by the key went
through a given transition in the transaction:

```go
focus.AssertLedgerEntryState(t, meta, key, focus.StateCreated) // or StateUpdated / StateDeleted
```

The finer-grained checks are the same question per transition:

```go
focus.AssertEntryCreated(t, meta, key)
focus.AssertEntryModified(t, meta, key)
focus.AssertEntryDeleted(t, meta, key)
```

A key may be passed as an `xdr.LedgerKey`, an `xdr.LedgerEntry`, or as base64
XDR / raw bytes / a decoded `*lens.Value`. A plain G… address names that
account's entry.

### Balance changes

Balance assertions read the ledger changes of a meta rather than a single key,
so they work across the native asset and every trustline at once:

```go
focus.AssertBalanceChanged(t, meta, account, "native", delta)
focus.AssertBalanceChangeRange(t, meta, account, asset, minDelta, maxDelta)
focus.AssertBalanceForfeited(t, meta, account, asset, amount)
```

`AssertBalanceChanged` takes the net delta across every ledger change in the
meta — for a transaction source that includes the fee. Pass zero to assert an
account was left alone. `AssertBalanceChangeRange` takes inclusive bounds,
which is how a test absorbs dynamic fees or DEX slippage; a zero bound is an
ordinary bound, not an unchecked side.

## Reading state changes out of TransactionMeta

The meta of a captured transaction is what carries the ledger entry changes; the
state assertions take it as their first argument and reduce it internally. When
a test holds the envelope and the meta separately — two fixtures, as
`focus capture` writes them — the assertions take each where it belongs:

```go
focus.AssertLedgerEntryState(t, meta, ledgerkey.Account(addr), focus.StateUpdated)
focus.AssertBalanceChanged(t, meta, addr, "native", -100_0000000)
```

When a key should name a specific entry but the test does not know its value
yet, build it from the values the test does have and let the assertion compare:

```go
want := ledgerkey.Trustline(debtor, usdcAsset) // built once, reused
focus.AssertLedgerEntryState(t, meta, want, focus.StateCreated)
```

## CLI surface

```sh
# A key from an address, as base64 XDR
focus ledger-key account GA5XIGA5C7Z4…

# Record an entry the source has
focus capture ledger-entry <base64-ledger-key> --out testdata/

# Record the Soroban simulateTransaction response for an envelope
focus capture simulate <envelope-xdr> --out testdata/
```

## Next steps

- Capture a real transaction that exercised the state change: [Capture,
  Anonymize and Replay](capture_and_anonymization.md).
- The full set of ledger assertions and their failure output:
  [Assertions Cookbook](assertions_cookbook.md).
- How ignore rules treat volatile ledger fields:
  [Ignore Rules Reference](ignore_rules_reference.md).
