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

// A trap, with the host's own explanation of it from the diagnostic stream.
focus.AssertContractTrappedWith(t, tx, "Error(Contract, #4)")

// The error itself: which category, and which code.
focus.AssertContractError(t, meta, xdr.ScErrorTypeSceContract, 101)
```

A non-match lists the events that *were* emitted, so you are not left guessing.
`AssertContractTrappedWith` takes a Soroban RPC `getTransaction` response, or a
`focus.TrapEvidence{Result, Meta}` pair when the result and meta are separate
fixtures, and searches the rendered host diagnostics.

`AssertContractError` answers the question a trap leaves open: not *that* the
contract failed but *how*. It reads the Soroban return value and the host's
error diagnostic, and names both sides of the comparison:

```
    contract_test.go:42: AssertContractError: got ScErrorTypeContract(101), want ScErrorTypeContract(404)
```

The code is the number the error carries: a contract's own code for
`ScErrorTypeContract`, and the numeric value of an `xdr.ScErrorCode` for the
host's categories — `uint32(xdr.ScErrorCodeScecMissingValue)` for a missing
storage entry.

### Typed memos, sources and signers

The display forms (`"text: hello"`, `"id: 42"`) make a wrong memo *type* read
like a wrong memo *value*, so the typed assertions name the value the way the
caller holds it:

```go
focus.AssertMemoText(t, env, "invoice-42")
focus.AssertMemoID(t, env, 42)
focus.AssertMemoHash(t, env, hash)     // a hash memo or a return memo
focus.AssertNoMemo(t, env)

focus.AssertOperationSourceAccount(t, env, 0, "GABC…")   // empty want: no override

focus.AssertSignedBy(t, env, "GABC…")                     // by public key
focus.AssertSignatureHints(t, env, [4]byte{0xde, 0xad, 0xbe, 0xef})
```

An operation may name a source account of its own, which replaces the
transaction's for that operation alone; an empty want asserts that it names
none, and the failure says which account the operation falls back to.

A Stellar signature carries a four-byte hint rather than a public key, so a
signature is attributed to a signer by the hint the protocol derives from that
signer's key — the last four bytes of it.

### Ignoring what legitimately varies

Without this, the library is unusable on real suites: a rebuilt transaction
differs in its sequence number, signatures and time bounds every run.

```go
focus.IgnoreVolatile()      // sequence + signatures + time bounds
focus.IgnoreSequence()
focus.IgnoreSignatures()
focus.IgnorePaths("**.Signatures", "operations[*].sourceaccount")
focus.NormalizePath("**.SeqNum", func(v any) any { return v.(int64) / 100 * 100 })
```

Patterns match the dotted paths lens reports: `*` is one segment, `**` is any
depth, and indexes accept `[*]`. Within a segment, `*`, `?` and `[abc]`/`[!abc]`
classes work like globs; matching is case-insensitive and patterns are anchored
to the tail of a path too, so `operations[*].sourceaccount` needs no prefix.

Two deliberate behaviours:

- **Ignored paths are always named in the failure output.** A silently
  discarded difference is how an assertion library stops being trustworthy.
- **An ignore pattern that matches nothing is reported as a failure**, even
  when the values are otherwise equal. A stale pattern is a test quietly
  weakening, and this is the only moment anyone would notice.

### Comparing arrays as sets

Some XDR arrays are ordered and some are not. The signatures on a multi-signed
transaction arrive in signing order, which is not part of what the transaction
means, and a positional comparison reports one difference per element for what
is really the same envelope.

```go
focus.AssertEnvelopeEqualUnordered(t, want, got)   // signatures compared as a set
focus.AssertEnvelopeEqual(t, want, got, focus.SortPath("**.Signatures"))
```

The arrays at the matching paths are sorted before the diff, so `[sigA, sigB]`
and `[sigB, sigA]` compare equal. Everything else is still compared the usual
way: a missing signature is still a difference.

The same two deliberate behaviours apply. The paths that were compared as
unordered sets are named in the failure output, and a sort pattern that
matches no array is reported as a failure, because a rule that quietly does
nothing is the same problem as a stale ignore pattern.

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

### Table-driven golden tests

A suite of fixtures is usually a table, and a table needs the failure to say
which row broke. `AssertAll` runs every case, reports the whole table, and
attaches the row's index, name and description to the failure:

```go
g := golden.New(t, golden.WithDir("testdata"))

g.AssertAll([]golden.Case{
	{Name: "payment_v1", Value: BuildPayment(dest, "100"), Description: "the first payment"},
	{Name: "payment_v2", Value: BuildPayment(dest, "200"), Description: "a payment that moved"},
})
```

```console
    golden: table summary — 2 case(s), 1 failed:
      [1/2] payment_v1: ok
      [2/2] payment_v2: DRIFTED (a payment that moved)
    golden: [2/2] payment_v2 (a payment that moved) failed:
    AssertEqualAs: envelopes differ — 1 difference
      ~ V1.Tx.Operations[0].Body.PaymentOp.Amount
          - 1000000000 100.0000000 (stroops: 1000000000)
          + 2000000000 200.0000000 (stroops: 2000000000)
```

Every case runs even when an earlier one fails, so one broken fixture does
not hide the state of the rest. A case may carry its own assertion options,
which are merged over the suite's:

```go
g.AssertAll([]golden.Case{
	{Name: "payment", Value: got, Options: []focus.Option{focus.IgnoreSequence()}},
})
```

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

`verify` exits `2` on drift and prints the structural diff, `1` when a fixture
could not be checked at all, and `0` when everything checked still matches. A
hand-written fixture is reported as skipped rather than quietly passing a
check it was never eligible for.

Defaults to **testnet**; use `--network mainnet` or `--rpc-url`.

### Linting fixtures

A fixture that was hand-edited, or merged badly, can be subtly corrupt:
invalid JSON, a missing field, an XDR that is not base64, a payload that
decodes as another type. Those errors surface when a test fails — weeks
later, in a suite nobody remembers writing.

`focus lint` checks for them statically. No network, no chain, no test run:

```sh
focus lint testdata/
```

```console
$ focus lint testdata/
testdata/envelope.golden.json            ok
testdata/result.golden.json              xdr: does not decode as a TransactionResult: …

2 checked: 1 clean, 1 with problems
```

It checks that the file is valid JSON and holds only fields a fixture has,
that the format is the one this version reads, that the type and the XDR are
present, that the XDR is valid base64 and decodes as the type the fixture
claims, and that a capture timestamp is RFC3339.

It exits `1` when a fixture has a problem and `0` when everything is clean,
so it can be a step in CI.

### Finding the fixtures

A directory is searched all the way down, so a suite that keeps its fixtures
in `testdata/envelopes/`, `testdata/results/` and `testdata/soroban/` is
checked in full:

```sh
focus verify testdata/
```

A path may also be a pattern, where `**` stands for any number of
directories:

```sh
focus verify 'testdata/**/*.golden.json'
focus verify 'testdata/*/envelope.golden.json'
```

Hidden directories are skipped, and a `.focusignore` file lists the paths to
leave out — a directory of scratch or benchmark fixtures, say:

```
# not real chain data
bench
scratch/**
```

### Capturing in bulk

Recording twenty transactions one command at a time means twenty processes
and twenty connections. `--batch` reads the hashes from a file instead and
captures them a few at a time:

```sh
focus capture tx --batch hashes.txt --out testdata/ --concurrency 4
```

```console
$ cat hashes.txt
# the transactions of the fixture suite
2b2e276a8f99a2927580eca2c8d665c0adaef9526476f5549269798380a66648
failed_payment 3c3f387b9f0ab3a38690fdb3d9d776d1dbebf063758a66583a6799491b77759

$ focus capture tx --batch hashes.txt --out testdata/
[1/2] captured tx 2b2e276a -> testdata/2b2e276a_envelope.golden.json
[2/2] captured tx 3c3f387b -> testdata/failed_payment_3c3f387b_envelope.golden.json

2 transaction(s): 2 captured, 0 not found, 0 failed
```

A line is a hash, optionally preceded by a prefix for the fixtures it
produces. Blank lines and `#` comments are ignored, and `-` reads the hashes
from standard input. A transaction that cannot be captured is reported and
the batch carries on; `--concurrency` caps how many captures are in flight at
once, and `--rate` spaces them out for an endpoint that rate-limits.

### Encoding a fixture offline

A payload that is already in hand — in a debug log, a test case, an RPC
response — becomes a reviewable fixture without a network:

```sh
focus encode "AAAAAgAAA..." --type TransactionEnvelope --out testdata/payment.golden.json
cat envelope.xdr | focus encode --type TransactionEnvelope --out testdata/payment.golden.json --description "a payment fixture"
```

The payload may be base64 or hex. The XDR type is detected when `--type` is
not given, which is ambiguous for short payloads, so naming it is better
wherever the type is known. With no `--out` the fixture is printed to
standard output.

### Configuration

Flags, `FOCUS_*` environment variables and a config file are three spellings
of the same settings, resolved in that order:

```sh
focus verify testdata/ --network mainnet
FOCUS_NETWORK=mainnet focus verify testdata/
focus verify testdata/ --config focus.yaml   # or .focus.yaml in the working directory
```

```yaml
# focus.yaml
network: mainnet
rpc-url: https://mainnet.sorobanrpc.com
timeout: 45s
color: never
out: testdata
prefix: nightly
force: false
```

An explicit `--config` must exist; a missing `.focus.yaml` is simply ignored.
Invalid settings fail before any request is made.

`focus encode` writes one file rather than a directory, so its output
setting is spelled `out-file` (`FOCUS_OUT_FILE`), while `--out` on the
command line stays `--out`.

### Refreshing fixtures

The XDR in a fixture is the source of truth; the decoded tree beside it is
what a human reads. When `stellar-xdr-lens` improves its formatting, or the
protocol gains a field, that tree goes stale — and the only way to fix it
today is to re-record every fixture, which re-records the data too.

```sh
# Re-render the decoded section of every fixture, leaving the XDR alone
focus refresh testdata/

# See what would change, without writing anything
focus refresh testdata/ --dry-run
```

```console
$ focus refresh testdata/
testdata/envelope.golden.json            updated
testdata/result.golden.json              unchanged

2 checked: 1 updated, 1 unchanged
```

The format, the type, the description, the XDR and the capture provenance all
keep their values; only the decoded section is rewritten. A fixture that is
already current is not rewritten at all, so the command is safe to run over a
whole `testdata` directory and leaves an empty pull request when there is
nothing to fix.

## Snapshots

Failure-message formatting is snapshotted so a layout change is a reviewable
diff rather than a silent change in what users see:

```go
focus.AssertFailureSnapshot(t, "envelope_amount_mismatch", func(tb focus.TB) {
	focus.AssertEnvelopeEqual(tb, want, got)
})
```

Create or update snapshots with `FOCUS_UPDATE_SNAPSHOTS=1 go test ./...`. They
live under `testdata/snapshots` as plain text, `.snap` files.

## Testing offline

`go test ./...` makes **no network calls**. The RPC client sits behind an
interface with an in-memory fake, so the whole suite runs offline and
deterministically — including the tests of `focus capture` and `focus verify`.

## Examples

### Full Lifecycle (TypeScript)

`examples/lifecycle.ts` demonstrates the complete Stellar transaction lifecycle
on testnet using `@stellar/stellar-sdk`:

1. **FUND** — Create a claimable balance with predicates
2. **CLAIM** — Claim the balance by the designated recipient
3. **ATTEST** — Record the claim on a Soroban contract
4. **SETTLE** — Finalize via contract call

```bash
cd examples
npm install
export FUNDER_SECRET="S..."      # testnet account with XLM
export CLAIMANT_SECRET="S..."    # testnet recipient
# Optional for attest/settle:
export ATTESTOR_SECRET="S..."
export ATTESTATION_CONTRACT_ID="C..."
npm run dev
```

The script prints transaction hashes at each step. View them on
[Stellar Expert](https://stellar.expert/explorer/testnet) to verify.

**CI** runs `npm run typecheck` in `examples/` to keep the example compiling.

See [examples/README.md](examples/README.md) for full setup and a sample
Soroban contract for the attest/settle steps.

## Project status

**Early but real.** v0.1 does what this README says. The library API may shift
before v1.0.

- Assertions for envelopes, results, fields and Soroban events.
- Golden files with `-focus.update`; capture and verify against a live network.
- Failure-message snapshots, a unified flag/env/config file, documented exit
  codes, and an offline end-to-end suite for the command.
- Soroban values render as the XDR wire shape — there is no contract-spec
  decoding yet. See [docs/roadmap.md](docs/roadmap.md).

## Contributing

Adding an assertion or a fixture source is designed to be a one-file change.
See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/architecture.md](docs/architecture.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
