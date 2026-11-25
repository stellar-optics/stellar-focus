# Building Offline Replay Test Suites from Captured Fixtures

A common challenge when developing on Stellar and Soroban is testing transaction parsing, decoding, and business logic against real network transactions without making tests dependent on live RPC nodes, network connectivity, or ephemeral ledger state.

`stellar-focus` solves this by providing an end-to-end workflow: capture real transactions from the network as golden fixtures, automatically generate hermetic table-driven Go replay tests (`_test.go`), and execute them completely offline in CI.

---

## 1. End-to-End Replay Workflow

```
+-------------------+      focus capture tx       +------------------------+
| Stellar / Soroban | --------------------------> |   testdata/*.golden    |
|   Network (RPC)   |                             |     (JSON fixtures)    |
+-------------------+                             +------------------------+
                                                              |
                                                              | codegen / GenerateReplayTest
                                                              v
+-------------------+          go test            +------------------------+
| Green CI or Clean | <-------------------------- |    *_replay_test.go    |
|   Field-Level Diff|                             |  (Offline Test Suite)  |
+-------------------+                             +------------------------+
```

### Step 1: Capture the Live Transaction

Capture a real transaction from Testnet or Mainnet using the CLI:

```bash
# Capture transaction envelope, result, and metadata:
focus capture tx 2b2e276a6b57… --network testnet --out testdata/
```

This generates:
- `testdata/2b2e276a….envelope.golden.json`
- `testdata/2b2e276a….result.golden.json`
- `testdata/2b2e276a….meta.golden.json`

Each fixture records:
- The base64 raw XDR payload.
- A human-readable, formatted decoded XDR view.
- Provenance metadata (ledger sequence number, network passphrase, capture timestamp).

### Step 2: Generate Replay Test Boilerplate

Use `pkg/capture` codegen functions to produce deterministic Go test files from the saved fixtures:

```go
package main

import (
    "log"
    "github.com/stellar-optics/stellar-focus/pkg/capture"
    "github.com/stellar-optics/stellar-focus/pkg/golden"
)

func main() {
    fixture, err := golden.Load("testdata/2b2e276a.envelope.golden.json")
    if err != nil {
        log.Fatal(err)
    }

    testSource, err := capture.GenerateReplayTest(&capture.CaptureFixture{
        Name:        "SwapPayment",
        Fixture:     fixture,
        FixturePath: "testdata/2b2e276a.envelope.golden.json",
        Package:     "replay_test",
    })
    if err != nil {
        log.Fatal(err)
    }

    // Write to replay_test.go
    log.Println("Generated test:\n", testSource)
}
```

For batch processing multiple fixtures in a directory, use `capture.WriteReplayTests`:

```go
paths, err := capture.WriteReplayTests(fixtures, "generated_tests/")
```

### Step 3: Run the Tests Offline

Execute your generated test suite without network access:

```bash
go test -v ./...
```

The generated test parses the stored golden fixture, decodes the raw XDR payload, and validates it against the fixture's expectations.

---

## 2. Determinism and Hermetic Guarantees

Generated replay test suites guarantee:
1. **Zero Network Calls:** Generated tests make no RPC calls, HTTP requests, or external socket connections. They run cleanly in firewalled CI environments.
2. **Deterministic Code Generation:** `capture.GenerateReplayTest` generates `gofmt`-formatted Go source without timestamps, random seeds, or map-ordering indeterminism. Regenerating an existing fixture produces zero git diff.
3. **Drift Detection:** If your application's XDR decoding, operation handling, or fee calculation logic changes, the test fails loudly with an exact field-level diff:

```
--- FAIL: TestReplay_SwapPayment (0.01s)
    swap_test.go:42: AssertEnvelopeEqual: envelopes differ — 1 difference
        ~ V1.Tx.Operations[0].Body.PaymentOp.Amount
            - 1000000000 100.0000000
            + 1500000000 150.0000000
```

---

## 3. Integrating Replay Suites into CI Checks

Include replay suites in your continuous integration pipeline to catch regressions across Stellar SDK bumps or protocol upgrades:

```yaml
# .github/workflows/ci.yml
name: Replay Tests
on: [push, pull_request]

jobs:
  replay:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.x'
          cache: true

      - name: Run Hermetic Replay Tests
        run: go test -v ./testdata/... ./replay/...
```

### Periodic Drift Verification (`focus verify`)

To ensure that your stored fixtures still reflect on-chain reality (or detect unexpected hard forks or archive reorganizations), run `focus verify` as a scheduled cron job:

```bash
focus verify testdata/ --network testnet
```
