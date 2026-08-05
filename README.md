# stellar-focus

A Go testing toolkit for Stellar and Soroban: assert on XDR, manage golden fixtures, and capture real chain data as test data.

[![CI](https://github.com/stellar-optics/stellar-focus/actions/workflows/ci.yml/badge.svg)](https://github.com/stellar-optics/stellar-focus/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/stellar-optics/stellar-focus.svg)](https://pkg.go.dev/github.com/stellar-optics/stellar-focus)
[![Go Report Card](https://goreportcard.com/badge/github.com/stellar-optics/stellar-focus)](https://goreportcard.com/report/github.com/stellar-optics/stellar-focus)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Part of [stellar-optics](https://github.com/stellar-optics) — Go tools that make Stellar and Soroban data legible. Siblings: [stellar-xdr-lens](https://github.com/stellar-optics/stellar-xdr-lens) (decode, explain, diff) and [stellar-prism](https://github.com/stellar-optics/stellar-prism) (tail live data).

## Why this exists

Testing Go code that produces Stellar XDR means comparing two base64 strings.
When they differ, `assert.Equal` tells you this:

```
    Error:  Not equal:
            expected: "AAAAAgAAAACS05bpJAmO8a9VODCyfbOCmh2YkUIwQLxzJkMlvAwp/AAAAGQAAAAAAAAAbwAAAAAAAAAAAAAAAQAAAAAAAAABAAAAAB4f..."
            actual  : "AAAAAgAAAACS05bpJAmO8a9VODCyfbOCmh2YkUIwQLxzJkMlvAwp/AAAAGQAAAAAAAAA3gAAAAAAAAAAAAAAAQAAAAAAAAABAAAAAB4f..."
```

That is technically correct and completely useless. focus tells you which
field changed:

```
--- FAIL: TestPaymentEnvelope (0.00s)
    payment_test.go:29: AssertEnvelopeEqual: envelopes differ — 1 difference
        (ignored: **.SeqNum)

          TransactionEnvelope

          ~ V1.Tx.Operations[0].Body.PaymentOp.Amount
              - 1000000000 100.0000000 (stroops: 1000000000)
              + 2000000000 200.0000000 (stroops: 2000000000)

        want  AAAAAgAAAACS05bpJAmO8a9V…  (172 bytes base64)
        got   AAAAAgAAAACS05bpJAmO8a9V…  (172 bytes base64)
    payment_test.go:30: AssertFee: fee is 100 (0.0000100 XLM), want 200 (0.0000200 XLM)
    payment_test.go:31: AssertOperationTypes: operations are [payment], want [payment, change_trust]
          [0] payment — pay 200.0000000 XLM to GAPB7…WYXZ
```

That is real output from a real `go test` run, not a mock-up. Note what it
does: names the field path, shows the amount in XLM as well as stroops, says
which ignore rule fired, keeps the base64 truncated but available, and reports
all three failures in one run.

Diffs are rendered by [stellar-xdr-lens](https://github.com/stellar-optics/stellar-xdr-lens),
so the diff in a failing test is the same one `lens diff` prints at the
command line.

## Install

```sh
go get github.com/stellar-optics/stellar-focus
```

Requires Go 1.25 or later.

**The library carries no assertion framework, no diff library, and no CLI
framework.** Test helpers get pulled into other people's projects, so
`pkg/focus` depends only on the standard library, the Stellar SDK and lens.
[A test enforces this](pkg/focus/deps_test.go). The `focus` command lives in a
separate package the library never imports.

## Assertions

```go
func TestBuildPayment(t *testing.T) {
	got := BuildPayment(dest, "100")   // whatever your code returns

	focus.AssertEnvelopeEqual(t, wantXDR, got, focus.IgnoreVolatile())
	focus.AssertOperationTypes(t, got, "payment")
	focus.AssertFee(t, got, 100)
	focus.AssertMemo(t, got, "text: invoice-42")
}
```

Every assertion accepts XDR in whatever form your code produced it — a base64
string, raw `[]byte`, any `xdr.*` value, or an already-decoded `*lens.Value`.

### Results, in plain English

```go
focus.AssertOperationFailed(t, result, 1, "payment_underfunded")
```

```
    submit_test.go:31: AssertOperationFailed: operation 1 failed with
        "payment_no_trust", want "payment_underfunded"

          got:  payment_no_trust — The destination has no trustline for this asset.
                → The destination must establish a trustline before it can receive a non-native asset.
          want: payment_underfunded
```

Codes are the short forms Horizon returns, and every one of the protocol's 208
result codes carries lens's plain-English explanation and hint.

### Soroban

```go
focus.AssertEventEmitted(t, meta, focus.Event{
	ContractID: "CBGSB…",
	Topics:     []any{"transfer", nil},   // nil matches any topic
})
focus.AssertReturnValue(t, meta, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &seven})
focus.AssertContractTrapped(t, result)
```

A non-match lists the events that *were* emitted, so you are not left guessing.

### Ignoring what legitimately varies

Without this, the library is unusable on real suites: a rebuilt transaction
differs in its sequence number, signatures and time bounds every run.

```go
focus.IgnoreVolatile()      // sequence + signatures + time bounds
focus.IgnoreSequence()
focus.IgnoreSignatures()
focus.IgnorePaths("**.Signatures", "V1.Tx.Operations[*].SourceAccount")
focus.NormalizePath("**.SeqNum", func(v any) any { return v.(int64) / 100 * 100 })
```

Patterns match the dotted paths lens reports: `*` is one segment, `**` is any
depth, and indexes accept `[*]`.

Two deliberate behaviours:

- **Ignored paths are always named in the failure output.** A silently
  discarded difference is how an assertion library stops being trustworthy.
- **An ignore pattern that matches nothing is reported as a failure**, even
  when the values are otherwise equal. A stale pattern is a test quietly
  weakening, and this is the only moment anyone would notice.

### `require` for fatal assertions

```go
import "github.com/stellar-optics/stellar-focus/pkg/focus/require"

require.EnvelopeEqual(t, want, got)   // stops the test; same message
```

Same split, same naming as testify's `assert` and `require`.

### Assertions never panic

Malformed XDR, a nil value, a wrong type — all reported as ordinary test
failures. A panic inside your test binary would destroy the output of every
other test in the package, so [a test feeds hostile input through every
exported assertion](pkg/focus/assertions_test.go).

## Golden files

```go
func TestEnvelopeGolden(t *testing.T) {
	g := golden.New(t, golden.WithType("TransactionEnvelope"))
	g.Assert("payment", BuildPayment(dest, "100"))
}
```

```console
$ go test ./...
    payment_test.go:15: golden: testdata/payment.golden.json does not exist yet.
        Create it by running:

            go test ./... -focus.update

$ go test ./... -focus.update
ok      demo    0.012s
```

**Fixtures are reviewable JSON, not opaque blobs.** The base64 XDR is the
source of truth for comparison; the decoded form sits beside it so a
pull-request diff shows which field changed:

```json
{
  "format": "focus/v1",
  "type": "TransactionEnvelope",
  "description": "a successful Soroban invocation",
  "xdr": "AAAAAgAAAACS05bpJAmO8a9VODCyfbOCmh2Yk…",
  "captured": {
    "network": "testnet",
    "ledger": 3966820,
    "txHash": "2b2e276a…",
    "at": "2026-08-05T12:00:00Z"
  },
  "decoded": {
    "type": "TransactionEnvelope",
    "value": { "V1": { "Tx": { "SourceAccount": "GCJNH…ZST6", "Fee": 58002, … } } }
  }
}
```

A golden test nobody can review is a golden test nobody updates carefully.

The flag is namespaced `-focus.update` so it never collides with your own
`-update`.

## Capture

```sh
go install github.com/stellar-optics/stellar-focus/cmd/focus@latest

# Record a real transaction from testnet as fixtures
focus capture tx 2b2e276a8f99a292… --out testdata/

# Later: check they still match the chain
focus verify testdata/
```

```console
$ focus verify testdata/
testdata/envelope.golden.json            ok
testdata/result.golden.json              ok
testdata/handwritten.golden.json         SKIP    no capture provenance, so there is nothing to re-fetch

3 checked: 2 ok, 0 drifted, 1 skipped
```

`verify` exits non-zero on drift and prints the structural diff. A
hand-written fixture is reported as skipped rather than quietly passing a
check it was never eligible for.

Defaults to **testnet**; use `--network mainnet` or `--rpc-url`.

## Testing offline

`go test ./...` makes **no network calls**. The RPC client sits behind an
interface with an in-memory fake, so the whole suite runs offline and
deterministically — including the tests of `focus capture` and `focus verify`.

## Project status

**Early but real.** v0.1 does what this README says. The library API may shift
before v1.0.

- Assertions for envelopes, results, fields and Soroban events.
- Golden files with `-focus.update`; capture and verify against a live network.
- Soroban values render as the XDR wire shape — there is no contract-spec
  decoding yet. See [docs/roadmap.md](docs/roadmap.md).

## Contributing

Adding an assertion or a fixture source is designed to be a one-file change.
See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/architecture.md](docs/architecture.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
