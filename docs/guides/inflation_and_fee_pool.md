# Testing Inflation Payouts and Ledger Fee-Pool Distribution

Although inflation was deprecated in Protocol 12, developers maintaining historical blockchain indexers, archive replay tools, or private Stellar test networks still need to assert on inflation payouts and ledger fee-pool balance movements. This guide documents the assertions available in `stellar-focus` for these scenarios.

## Inflation Operation Assertions

### `focus.AssertInflationPayouts`

An inflation operation creates payouts to designated accounts from the fee pool. Use `AssertInflationPayouts` to verify that the correct accounts received the expected amounts.

```go
func TestInflationPayouts(t *testing.T) {
    // results is the TransactionResult from the inflation transaction
    focus.AssertInflationPayouts(t, results, []focus.InflationPayout{
        {Account: "GBTC7…ZYXW", Amount: 100000000},  // 100 XLM
        {Account: "GCJNH…XSTU", Amount: 50000000},   // 50 XLM
    })
}
```

#### Failure output when a payout diverges:

```
AssertInflationPayouts: payout diverged — 1 difference

  ~ V1.Results[0].TrxResult.OperationResult[0].InflationResult.payouts[0]
      want  100000000 (100.0000000 XLM)
      got   90000000  ( 90.0000000 XLM)
```

#### How it works:

1. The inflation result is found in `TransactionResult.results` at the operation index where the inflation occurred.
2. The payouts array is inspected; each entry has an `destination` account and an `amount` in stroops.
3. The assertion compares the expected payouts against the actual ones, naming the field path that differs.
4. Amounts are shown in both stroops and XLM.

#### Expected payouts format:

```go
type InflationPayout struct {
    // Account is the stroop-free account ID (strkey format)
    Account string
    // Amount is in stroops (1 XLM = 1000000 stroops)
    Amount uint64
}
```

### `focus.AssertNoInflationPayouts`

Assert that an inflation operation produced no payouts, or that the payouts list is empty:

```go
focus.AssertNoInflationPayouts(t, results, 0)  // expects zero payouts
```

---

## Fee Pool Distribution

### `focus.AssertLedgerFeePool`

The fee pool is a reserve of XLM that funds inflation payouts and transaction fee refunds. Its balance changes across ledger closes. Use `AssertLedgerFeePool` to compare the fee pool before and after a ledger header.

```go
func TestFeePoolEvolution(t *testing.T) {
    // headerBefore and headerAfter are *ledger_header.LedgerHeader (or decoded forms)
    focus.AssertLedgerFeePool(t, headerBefore, headerAfter, 1_000_000_000)
}
```

#### Failure output when the fee pool changed by an unexpected amount:

```
AssertLedgerFeePool: fee pool changed by unexpected amount

  want  1000000000 (100.0000000 XLM) increase
  got   950000000  ( 95.0000000 XLM) increase  — 50000000 difference
```

#### How it works:

1. The fee pool balance is decoded from the `LedgerHeader` XDR at two points: before and after the ledger close.
2. The assertion computes the difference (increase or decrease) and compares it against the expected delta.
3. A budget of zero leaves that dimension unchecked; non-zero values must match exactly.

#### Ledger header fields involved:

| Field | Meaning |
|---|---|
| `FeePool` | Current fee pool balance in stroops |
| `BaseFee` | Base fee for the ledger in stroops |
| `BaseReserve` | Base reserve for accounts in stroops |
| `MaxTimeBounds` | Maximum time bounds in seconds |
| `MinTimeBounds` | Minimum time bounds in seconds |

---

## Deprecation Warnings

### Inflation Deprecation Status

Since Protocol 12, inflation is deprecated but still supported. `stellar-focus` flags the deprecation status in diagnostic outputs. When reading a ledger header or result that involves inflation, the assertion output may include a note:

```
Note: inflation is deprecated (Protocol 12+); this transaction was created on a network running Protocol 15+
```

This does not affect the assertion logic — the payout and fee-pool amounts are still decoded and compared normally. It is informational, helping operators understand whether their test network is running a protocol version that still supports inflation.

### How focus Flags Deprecation

The deprecation status is derived from the protocol version in the ledger header. If the protocol version is ≥ 12, the output includes the deprecation note. For protocol versions < 12, inflation is fully active and no note is shown.

---

## Test Case Examples

### Example 1: Verifying Inflation Payouts from a Captured Fixture

```go
func TestInflationFromFixture(t *testing.T) {
    g := golden.New(t, golden.WithType("TransactionResult"))
    
    // Load the fixture; got is the result from your code
    got := BuildInflationTransaction()  // your generator
    
    g.Assert("inflation_result", got)
    
    // Or, manually assert on the payouts:
    focus.AssertInflationPayouts(t, got.Results, []focus.InflationPayout{
        {Account: "GBTC7…ZYXW", Amount: 100000000},
    })
}
```

### Example 2: Tracking Fee Pool Across Ledger Closes

```go
func TestFeePoolChange(t *testing.T) {
    // header1 and header2 are LedgerHeader values from two consecutive ledgers
    focus.AssertLedgerFeePool(t, header1, header2, 0)  // expect no change
    
    // Or expect a specific change:
    focus.AssertLedgerFeePool(t, header1, header2, 50000000)  // expect 50 XLM decrease
}
```

### Example 3: Combined Test — Inflation + Fee Pool

```go
func TestInflationRound(t *testing.T) {
    // 1. Capture an inflation transaction
    // 2. Verify payouts
    focus.AssertInflationPayouts(t, results, []focus.InflationPayout{
        {Account: "dest1...", Amount: 100000000},
        {Account: "dest2...", Amount: 50000000},
    })
    
    // 3. Check the fee pool before/after
    focus.AssertLedgerFeePool(t, beforeHeader, afterHeader, 0)
}
```

---

## Related Assertions

| Assertion | Subject | Reference |
|---|---|---|
| `AssertInflationPayouts` | Inflation payouts | This guide |
| `AssertNoInflationPayouts` | No payouts | This guide |
| `AssertLedgerFeePool` | Fee pool before/after | This guide |
| `AssertOperationFailed` | Operation result codes (includes `inflation_idempotent`) | pkg/focus/results.go |
| `AssertLedgerBounds` | Ledger bounds (may involve inflation TTL) | pkg/focus/timebounds.go |
| `AssertSorobanResources` | Resource budgets (inflation has none, but useful for context) | pkg/focus/soroban.go |

---

## Inflation Payout Structure

An inflation payout entry in the XDR looks like:

```go
type InflationResult struct {
    Payouts []InflationPayout
    // ... other fields
}

type InflationPayout struct {
    Destination account.ID  // strkey format
    Amount      uint64       // stroops
}
```

The `focus.InflationPayout` type in the assertion helper matches this structure, with the `Account` field accepted in strkey format (e.g., `"GBTC7…ZYXW"`) for readability.

## Archive-Aware Verification

When verifying fixtures against a history archive (not just RPC), the fee pool values are read from the ledger header at the ledger close referenced by each fixture. Use `--archive-url` with `focus verify` to check fee-pool changes against archival data:

```sh
focus verify testdata/ --archive-url https://history.stellar.org/prd/core-live/core_live_001
```

This is essential for indexers and replay tools that need to track fee-pool evolution across thousands of ledgers.