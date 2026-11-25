# Testing Soroban Preflight Simulation and Resource Footprints

Before submitting a Soroban smart contract transaction to the network, clients invoke `simulateTransaction` on a Soroban RPC node. The simulation preflight computes:
1. **CPU and memory resource usage** (CPU instructions and disk read/write bytes).
2. **Resource fees and refundable fees**.
3. **Storage footprint** (`xdr.LedgerFootprint` containing read-only and read-write keys).
4. **State restoration requirements** (archived entries requiring restoration).

Testing that your application accurately simulates transactions, budgets for execution limits, and declares precise storage footprints is critical to preventing on-chain transaction failures and excessive fees.

---

## 1. Capturing Simulation Fixtures

`stellar-focus` provides tools to record simulation responses from live testnet/mainnet RPC endpoints so test suites can run completely offline.

### Via CLI: `focus capture simulate`

```bash
# Capture the preflight response for a transaction envelope:
focus capture simulate "AAAAAgAAAAB..." --out testdata/ --rpc-url https://soroban-testnet.stellar.org
```

This writes a JSON fixture in format `focus/simulation/v1` containing:
- `network` and `endpoint` metadata for provenance.
- The original request payload.
- The full RPC `SimulateTransactionResponse` (return values, diagnostic events, auth trees, and `TransactionDataXDR`).

### Via Go API: `capture.Simulate`

```go
src, err := capture.NewRPCSource("https://soroban-testnet.stellar.org", "Test SDF Network ; September 2015")
if err != nil {
    t.Fatal(err)
}

sim, err := capture.Simulate(ctx, src, protocol.SimulateTransactionRequest{
    Transaction: envelopeBase64,
})
if err != nil {
    t.Fatalf("simulation failed: %v", err)
}

// Persist the fixture for offline testing
if err := sim.Save("testdata/swap_simulation.json"); err != nil {
    t.Fatal(err)
}
```

Offline tests load saved simulations with `capture.LoadCaptureSimulation("testdata/swap_simulation.json")`.

---

## 2. Storage Footprint Assertions

Soroban executes transactions with strict state access controls. If a contract attempts to read or write a ledger entry not declared in its footprint, execution halts with a host error.

### `focus.AssertFootprintExact`

Verifies that the envelope or simulation response declares *exactly* the specified read-only and read-write ledger keys, with no extra or missing keys:

```go
func TestSwapFootprintExact(t *testing.T) {
    wantReadOnly := []string{
        "AAAAFAAAAA...", // Token contract instance key (base64)
    }
    wantReadWrite := []string{
        "AAAAFAAAAB...", // User balance ledger entry (base64)
        "AAAAFAAAAC...", // Pool balance ledger entry (base64)
    }

    focus.AssertFootprintExact(t, envelope, wantReadOnly, wantReadWrite)
}
```

### `focus.AssertFootprintContains`

When testing a component that only cares about specific keys (e.g. verifying an account's trustline or balance entry is touched), use `AssertFootprintContains`:

```go
func TestLiquidityPoolTouchesReserves(t *testing.T) {
    requiredReadOnly := []string{poolConfigKey}
    requiredReadWrite := []string{poolReserveKey}

    focus.AssertFootprintContains(t, envelope, requiredReadOnly, requiredReadWrite)
}
```

### Detecting Footprint Collisions (`focus.AssertNoFootprintConflicts`)

When orchestrating parallel transactions or multi-transaction batches, transactions with overlapping write sets (`Write/Write` or `Read/Write`) will fail if submitted to the same ledger. `AssertNoFootprintConflicts` ensures concurrent transactions can execute safely:

```go
func TestConcurrentTransactionsConflictFree(t *testing.T) {
    txs := []xdr.TransactionEnvelope{txA, txB, txC}
    focus.AssertNoFootprintConflicts(t, txs)
}
```

---

## 3. Resource Budget Assertions

### `focus.AssertSimulationResourcesWithin`

Checks that the simulated resource footprint does not exceed allocated CPU instruction or memory/ledger I/O budgets:

```go
func TestExecutionBudget(t *testing.T) {
    // Accepts protocol.SimulateTransactionResponse, *capture.Simulation,
    // or xdr.SorobanTransactionData
    maxCPU := uint32(5_000_000)      // Max 5M CPU instructions
    maxLedgerBytes := uint32(65_536)  // Max 64KB disk read/write bytes

    focus.AssertSimulationResourcesWithin(t, simResponse, maxCPU, maxLedgerBytes)
}
```

#### Failure Output:
When resources exceed the budget, the assertion clearly pinpoints the overrun:

```
AssertSimulationResourcesWithin: simulated resources exceed the budget

  CPU instructions     6500000   budget    5000000   OVER by 1500000
  ledger bytes           45000   budget      65536   ok

  footprint: 3 read-only key(s), 2 read-write key(s)
```

### `focus.AssertSimulationSucceeded`

Ensures the preflight simulation was accepted by the host and did not reject with a contract trap or invocation error:

```go
func TestSimulationSuccessful(t *testing.T) {
    focus.AssertSimulationSucceeded(t, simulateResult)
}
```

---

## 4. Decoding Simulation Footprints Offline

`capture.Simulation` provides helper methods to inspect decoded footprint and resource metrics:

```go
sim, err := capture.LoadCaptureSimulation("testdata/sim.json")
if err != nil {
    t.Fatal(err)
}

footprint, err := sim.ResourceFootprint()
if err != nil {
    t.Fatal(err)
}

fmt.Printf("Instructions: %d\n", footprint.Instructions)
fmt.Printf("Disk Read:     %d bytes\n", footprint.DiskReadBytes)
fmt.Printf("Disk Write:    %d bytes\n", footprint.WriteBytes)
fmt.Printf("Resource Fee:  %d stroops\n", footprint.ResourceFee)
fmt.Printf("Read-only keys: %v\n", footprint.ReadOnly)
fmt.Printf("Read-write keys: %v\n", footprint.ReadWrite)
```
