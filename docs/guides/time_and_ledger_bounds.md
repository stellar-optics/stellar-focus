# Testing TimeBounds and LedgerBounds Validation Patterns

Transactions on the Stellar network declare validity constraints using `TimeBounds` (Unix timestamps) and `LedgerBounds` (ledger sequence numbers). In integration and unit tests, relying on `time.Now()` or live clock comparisons is a frequent source of flaky test runs in CI: a test may pass locally but fail under runner latency or across leap seconds.

`stellar-focus` provides static, clock-free assertion helpers that verify bounds deterministically against decoded XDR without touching the system clock.

---

## 1. Static TimeBounds Verification

A transaction declares time bounds in Unix epoch seconds (`MinTime` and `MaxTime`). The Stellar protocol defines `0` as unbounded:
- `MinTime: 0` means there is no lower bound (valid from the beginning of time).
- `MaxTime: 0` means there is no upper bound (the transaction never expires).
- `MinTime: 0, MaxTime: 0` means the transaction has no time bounds at all.

### `focus.AssertTimeBounds`

`AssertTimeBounds` checks that an envelope declares exactly the expected `MinTime` and `MaxTime` timestamps:

```go
func TestPaymentTimeBounds(t *testing.T) {
    // env can be a base64 string, []byte, *xdr.TransactionEnvelope, or *lens.Value
    var env xdr.TransactionEnvelope = buildPaymentTransaction()

    // Assert that the transaction is valid between 1700000000 and 1700003600 (1 hour window)
    focus.AssertTimeBounds(t, env, 1700000000, 1700003600)
}
```

#### Failure Output:
When the bounds differ, the assertion names both the raw seconds and human-readable UTC timestamps:

```
AssertTimeBounds: time bounds differ

  got   valid from 2023-11-14T22:13:20Z until 2023-11-14T23:13:20Z
  want  valid from 2023-11-14T22:13:20Z until 2023-11-15T00:13:20Z

  max time is 1700003600 (2023-11-14T23:13:20Z), want 1700007200 (2023-11-15T00:13:20Z)
```

### Asserting Validity Window (`AssertTimeBoundsWindow`)

To verify the width of the validity window regardless of the exact absolute timestamps, use `AssertTimeBoundsWindow`:

```go
func TestTransactionWindow(t *testing.T) {
    env := buildExpiringTransaction()

    // Assert that the envelope allows a 5-minute validity window
    focus.AssertTimeBoundsWindow(t, env, 5*time.Minute)
}
```

A window requires both bounds to be non-zero. If the envelope declares only one side or no bounds at all, the assertion fails explicitly noting that a window requires both bounds.

### Asserting No Expiration (`AssertNoTimeBounds`)

For infinite-lifetime transactions or offline authorizations that do not expire:

```go
func TestUnboundedTransaction(t *testing.T) {
    env := buildPerpetualEnvelope()

    // Asserts that MinTime == 0 && MaxTime == 0 (or preconditions are unset)
    focus.AssertNoTimeBounds(t, env)
}
```

---

## 2. LedgerBounds Verification

Protocol 19 (CAP-0021) introduced `LedgerBounds` as part of `PreconditionsV2`. Instead of timestamps, ledger bounds specify the minimum and maximum ledger sequence numbers in which a transaction may be included:

```go
func TestContractCallLedgerBounds(t *testing.T) {
    env := buildContractCallEnvelope()

    // Assert transaction is valid between ledger 50,000,000 and 50,000,100
    focus.AssertLedgerBounds(t, env, 50_000_000, 50_000_100)
}
```

As with time bounds, `0` indicates unbounded:
- `maxLedger: 0` asserts that the transaction accepts any subsequent ledger once valid.
- `0, 0` asserts that no ledger bounds are enforced.

#### Failure Output:
```
AssertLedgerBounds: ledger bounds differ

  got   valid until ledger 50000100 (no lower bound)
  want  ledger bounds 50000000..50000100

  min ledger is 0 (unbounded), want 50000000
```

---

## 3. Extra Signers & Sequence Aging (CAP-0021)

V2 preconditions also allow transactions to require extra signers and enforce sequence aging without requiring immediate ledger state transitions.

### `focus.AssertExtraSigners`

Extra signers are additional public keys (beyond the source account) required to validate a transaction before execution. Pass the expected strkey addresses:

```go
func TestMultisigPreconditions(t *testing.T) {
    env := buildPreconditionedEnvelope()

    focus.AssertExtraSigners(t, env,
        "GA6HCMBLTZS5VYYBCATRBRZ3ACJROEZG4FXX4PAUDA3BHGNIIIPNC7US",
        "GCFXHS4GMXBYRBAP57JMOKSKUEBLW7CG5TGDKNXUDRHRYAZHTX6OAAAO",
    )
}
```

### `focus.AssertSequenceAge`

`AssertSequenceAge` asserts on `MinSeqAge`: the minimum duration (in seconds) that must elapse since the source account sequence number was updated:

```go
func TestSequenceAging(t *testing.T) {
    env := buildTimeLockedEnvelope()

    // Assert that at least 1 hour must have passed since the last sequence bump
    focus.AssertSequenceAge(t, env, 1*time.Hour)
}
```

---

## 4. Fee-Bump Envelopes

Fee-bump transactions (`xdr.EnvelopeTypeEnvelopeTypeTxFeeBump`) wrap an inner transaction. The network checks validity windows against the **inner transaction's** preconditions, not the outer fee-bump envelope.

`stellar-focus` automatically detects fee-bump envelopes and inspects the inner transaction's time and ledger bounds transparently:

```go
func TestFeeBumpBounds(t *testing.T) {
    feeBumpEnv := wrapWithFeeBump(innerEnv, feePayer)

    // Checks the inner transaction's timebounds directly:
    focus.AssertTimeBounds(t, feeBumpEnv, 1700000000, 1700003600)
}
```

---

## 5. Clock-Free Testing Best Practices

1. **Avoid `time.Now()` in assertions:** Never write `focus.AssertTimeBounds(t, env, time.Now().Unix(), ...)`. If the builder uses `time.Now()`, inject a frozen clock in your transaction builder interface.
2. **Prefer relative windows:** When testing builders that accept timeouts (e.g. `builder.SetTimeout(300)`), use `focus.AssertTimeBoundsWindow(t, env, 300*time.Second)`.
3. **Assert unboundedness explicitly:** Always verify that infinite-life or escrow transactions declare no bounds with `focus.AssertNoTimeBounds`.
