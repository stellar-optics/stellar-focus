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

### Round-trip checks

A value can decode cleanly and still not survive being re-encoded: a dropped
union arm, a mutated field or a reordered array all pass a decoder and fail a
serializer. `AssertXDRRoundTrip` re-encodes the decoded value, decodes it
again, and diffs the two trees:

```go
focus.AssertXDRRoundTrip(t, envelope, "TransactionEnvelope")
```

The failure names the field path that changed, the same way the equality
assertions do, so a lossy encoder is a readable diff rather than two base64
strings to compare by eye.

### Ledger keys

`pkg/ledgerkey` builds the base64-encoded `LedgerKey` that `focus capture
ledger-entry` and `capture.Source` expect, without assembling nested union
arms by hand:

```go
key, err := ledgerkey.Account(address)
key, err = ledgerkey.Trustline(address, asset)
key, err = ledgerkey.ContractData(contractID, "counter", xdr.ContractDataDurabilityPersistent)
key, err = ledgerkey.ContractCode(hash)

focus.AssertLedgerKeyEqual(t, key, want)
```

`AssertLedgerKeyEqual` accepts a key built by the package and one read out of
a transaction meta or captured fixture, and names the field that differs.

### Preconditions

A transaction can promise the network a window in which it is valid. The
assertions read that promise out of the envelope — from the V1
preconditions or from the older V0 field — and check it:

```go
focus.AssertTimeBounds(t, envelope, minTime, maxTime)   // exactly these bounds
focus.AssertTimeBoundsWindow(t, envelope, 5*time.Minute) // a window this long
focus.AssertNoTimeBounds(t, envelope)                    // never expires
focus.AssertLedgerBounds(t, envelope, minLedger, maxLedger)
```

A bound of zero means "unbounded", the protocol's own convention, so `0, 0`
asserts that the transaction declares no bounds at all. The bounds are shown
both as the seconds the protocol stores and as the UTC dates a person reads:

```
    submit_test.go:44: AssertTimeBounds: time bounds differ

      got   valid from 2025-10-09T12:26:40Z until 2025-10-09T12:31:40Z
      want  no time bounds (valid indefinitely)

      min time is 0 (unbounded)
      max time is 0 (unbounded)
```

A fee-bump envelope is read through to the inner transaction, since that is
the one whose validity the protocol checks.

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
focus.AssertDiagnosticEventEmitted(t, meta, focus.Event{
	Topics: []any{focus.DiagnosticFnCall, nil, "transfer"},
})
focus.AssertReturnValue(t, meta, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &seven})
focus.AssertContractTrapped(t, result)

// A trap, with the host's own explanation of it from the diagnostic stream.
focus.AssertContractTrappedWith(t, tx, "Error(Contract, #4)")

// The error itself: which category, and which code.
focus.AssertContractError(t, meta, xdr.ScErrorTypeSceContract, 101)
```

A non-match lists the events that *were* emitted, so you are not left guessing.
The same matcher works against the diagnostic stream, where the events of a
failed call survive: `AssertDiagnosticEventEmitted` finds the `fn_call`,
`fn_return`, `log` and error events the host recorded, and
`focus.DiagnosticEvents` returns the whole decorated stream — position, depth
and call outcome included — for checks this package does not provide.
`AssertContractTrappedWith` takes a Soroban RPC `getTransaction` response, or a
`focus.TrapEvidence{Result, Meta}` pair when the result and meta are separate
fixtures, and searches the rendered host diagnostics.

#### The whole event sequence

`AssertEventEmitted` matches one event anywhere in the meta. When the order is
the point — `init`, `deposit`, `transfer`, `audit` — assert the whole sequence:

```go
focus.AssertEventsEqual(t, meta, []focus.Event{
	{Topics: []any{"init"}},
	{Topics: []any{"deposit"}, Data: scAmount(100)},
	{Topics: []any{"audit"}},
})
```

The count has to match too, and each event is matched at its own index. A
failure names the event that deviated and prints what was emitted beside what
was wanted:

```
AssertEventsEqual: event 1 of 3 does not match

  actual:   topics["deposit"]  data u32(100)
  expected: topics["withdraw"]  data u32(100)
```

Options relax the comparison for the whole list — `focus.IgnoreEventData()`,
`focus.IgnoreEventTopics()` and `focus.IgnoreEventContractID()` — and whatever
they relax is named in the failure, because an assertion that quietly stops
checking something is how a test starts passing for the wrong reason.

#### Resource budgets

```go
focus.AssertSorobanResources(t, envelope, 100_000, 64_000, 64_000)
focus.AssertResourceFee(t, envelope, 100_000_000)
```

`AssertSorobanResources` checks the limits the transaction bids — CPU
instructions, ledger bytes read and written — against the budgets a test
gives, and names every dimension that went over. A budget of zero leaves that
dimension unchecked. `AssertResourceFee` pins the refundable resource fee, in
stroops and in XLM.

### Operations

`AssertOperationTypes` checks the discriminator of an operation. The typed
assertions check what the operation does:

```go
focus.AssertPayment(t, envelope, 0, dest, 100_0000000, "native")
focus.AssertCreateAccount(t, envelope, 0, dest, 20_0000000)
focus.AssertChangeTrust(t, envelope, 0, "USDC:GA2J…HVAW", 1000_0000000)
```

Amounts are in stroops, and a failure shows both units:

```
AssertPayment: operation 0 amount is 1000000000 (100.0000000 XLM), want 2000000000 (200.0000000 XLM)
```

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

#### Naming the fields the contract chose

A Soroban payload on the wire is a map of symbol keys, and a failure that
shows only that is a failure you have to decode in your head. Give the
assertion the contract's specification and it will speak the contract's
language instead:

```go
spec, err := focus.ContractSpec(contractWasm)   // or build the entries yourself
if err != nil {
	t.Fatal(err)
}

focus.AssertEventEmitted(t, meta, focus.Event{
	Topics: []any{"transfer"},
	Data:   map[string]any{"from": from, "amount": 100},
	Spec:   spec,
})
```

A mismatch then names the field, not the wire:

```console
AssertEventEmitted: no event matched topics["transfer"]  data {amount: u128(200), from: GCJNH…ZST6}

  events present (1):
    [0] CBTG6…DXKK  topics["transfer"]  data {from: GCJNH…ZST6, amount: u128(100)} [ScValTypeScvMap]

  differences from the closest event:
    amount: want u128(200), got u128(100)
```

Both names are shown — the contract's field names and the XDR type — so the
message can be read either way. The specification is optional everywhere: a
test that cannot supply one still works, and the specification never changes
*what* matches, only how it is described.

`AssertReturnValueWithSpec` does the same for a return value, using the
contract function's output type:

```go
focus.AssertReturnValueWithSpec(t, meta, map[string]any{"amount": 42}, "withdraw", spec)
```

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

### Fee bumps

A fee-bump envelope wraps an inner transaction in an outer fee payer and a fee
bid. The field assertions read through the wrapper — the source account, the
memo and the operations are the inner transaction's, because that is the
transaction the protocol executes — and `AssertFeeBump` says which it is:

```go
focus.AssertFeeBump(t, envelope, true)
```

When a check has to be made on the inner transaction itself, the inner
transaction can be asked for:

```go
inner, ok := focus.InnerEnvelope(t, envelope)

focus.AssertFeeBumpInner(t, envelope, func(inner focus.Value) bool {
	return focus.AssertOperationTypes(t, inner, "payment")
})
```

### Ignoring what legitimately varies

Without this, the library is unusable on real suites: a rebuilt transaction
differs in its sequence number, signatures and time bounds every run.

```go
focus.IgnoreVolatile()      // sequence + signatures + time bounds
focus.IgnoreSequence()
focus.IgnoreSignatures()
focus.IgnoreSorobanAuth()   // volatile Soroban authorization credentials
focus.IgnorePaths("**.Signatures", "operations[*].sourceaccount")
focus.NormalizePath("**.SeqNum", func(v any) any { return v.(int64) / 100 * 100 })
```

Soroban authorization entries carry a nonce and a signature that are fresh on
every run. `IgnoreSorobanAuth` suppresses the credentials tree while still
comparing the authorized invocation, and discloses the ignored credential
paths in the failure output like any other ignore rule.

Patterns match the dotted paths lens reports: `*` is one segment, `**` is any
depth, and indexes accept `[*]`. Within a segment, `*`, `?` and `[abc]`/`[!abc]`
classes work like globs; matching is case-insensitive and patterns are anchored
to the tail of a path too, so `operations[*].sourceaccount` needs no prefix.
Patterns are validated when the option is built, so an unclosed bracket, empty
segment or invalid index panics immediately instead of silently matching
nothing.

Two deliberate behaviours:

- **Ignored paths are always named in the failure output.** A silently
  discarded difference is how an assertion library stops being trustworthy.
- **An ignore pattern that matches nothing is reported as a failure**, even
  when the values are otherwise equal. A stale pattern is a test quietly
  weakening, and this is the only moment anyone would notice.

### Normalizing dynamic addresses

Accounts and contracts generated per run have different strkeys every time.
Ignoring every source account makes the comparison pass but stops proving who
paid whom, so map the generated keys to stable names instead:

```go
focus.AssertEnvelopeEqual(t, want, got, focus.NormalizeAddress(map[string]string{
	aliceKey: "ALICE_ADDRESS",
	bobKey:   "BOB_ADDRESS",
}))
```

`NormalizeAddress` rewrites source accounts, destinations, account fields and
contract IDs before the diff. Contract IDs are held as `C…` strkeys but lens
renders them as hex, so both spellings resolve to the same alias. Everything
else is still compared normally.

### Naming the step that failed

An assertion in a loop or a multi-step flow can say which step produced the
failure:

```go
focus.AssertEnvelopeEqual(t, want, got, focus.WithLabel("step 3: fee refund"))
```

The label appears beside the assertion name in the headline:

```
AssertEnvelopeEqual [step 3: fee refund]: envelopes differ — 1 difference
```

The same option works with `focus/require`, `golden.WithOptions` and the
golden `require` package, because they all forward assertion options.

### Comparing arrays as sets

Some XDR arrays are ordered and some are not. The signatures on a multi-signed
transaction arrive in signing order, which is not part of what the transaction
means, and a positional comparison reports one difference per element for what
is really the same envelope.

```go
focus.AssertEnvelopeEqualUnordered(t, want, got)   // signatures compared as a set
focus.AssertEnvelopeEqual(t, want, got, focus.SortPath("**.Signatures"))
focus.AssertEqualAs(t, "AccountEntry", want, got, focus.SortSigners())
focus.AssertEqualAs(t, "SorobanTransactionData", want, got, focus.SortFootprint())
```

`SortSigners` and `SortFootprint` are the two common wrappers. When an array
has a natural identity, `SortPath` takes a key function that receives the
decoded element as a `*lens.Node`:

```go
focus.SortPath("**.Signers", func(elem any) string {
	n, _ := elem.(*lens.Node)
	return n.Child("Key").String()
})
```

The arrays at the matching paths are sorted before the diff, so `[sigA, sigB]`
and `[sigB, sigA]` compare equal. Everything else is still compared the usual
way: a missing signature is still a difference. A key function that panics
falls back to canonical ordering rather than taking the test binary down.

The same two deliberate behaviours apply. The paths that were compared as
unordered sets are named in the failure output, and a sort pattern that
matches no array is reported as a failure, because a rule that quietly does
nothing is the same problem as a stale ignore pattern.

### `require` for fatal assertions

```go
import "github.com/stellar-optics/stellar-focus/pkg/focus/require"

require.EnvelopeEqual(t, want, got)   // stops the test; same message
```

Same split, same naming as testify's `assert` and `require`. The golden files
have their own mirror, [`pkg/golden/require`](#fatal-golden-assertions).

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

### Fatal golden assertions

`Assert` reports a failure and lets the test continue, which is what you
want in a table test that should report every problem in one run. When
everything after the comparison would be meaningless without it, use
`Require` instead:

```go
g := golden.New(t, golden.WithType("TransactionEnvelope"))
g.Require("payment", BuildPayment(dest, "100"))
```

It reports the same diagnostic — the same diff, in the same layout, with the
same `-focus.update` instructions — and then stops the test. The package
form does the same in one call:

```go
import "github.com/stellar-optics/stellar-focus/pkg/golden/require"

require.Assert(t, "payment", BuildPayment(dest, "100"))
```

```go
g := require.New(t, golden.WithType("TransactionEnvelope"))
g.Assert("payment", BuildPayment(dest, "100"))
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

### Checking less of it

A repository with hundreds of fixtures does not need all of them re-fetched on
every commit, so a run can be narrowed:

```sh
focus verify testdata/ --since 48h              # only fixtures captured recently
focus verify testdata/ --since 2026-10-01T00:00:00Z
focus verify testdata/ --filter 'envelope*'     # only fixtures matching a glob
focus verify testdata/ --filter network:testnet # …or a metadata selector
focus verify testdata/ --drifted-only           # only what drifted last time
```

A fixture left out is reported as `FILTERED`, with the reason, so a narrowed
run never looks like a run that found nothing wrong.

`--drifted-only` remembers what each run found, in `.focus/verify-cache.json`.
Only the fixtures that drifted or could not be checked are looked at again —
and a fixture whose contents have changed since the last check is checked too,
because the old outcome says nothing about a fixture that has moved. The cache
is a speed-up, never a source of truth; delete it and the next run checks
everything.

### Verifying in a pipeline

The report above is written for a person. A pipeline wants a document it can
read, so `--format json` prints one:

```sh
focus verify testdata/ --format json
```

```json
{
  "total": 3,
  "matched": 1,
  "drifted": 1,
  "skipped": 1,
  "errored": 0,
  "results": [
    {
      "path": "testdata/envelope.golden.json",
      "status": "matched",
      "type": "TransactionEnvelope"
    },
    {
      "path": "testdata/result.golden.json",
      "status": "drifted",
      "type": "TransactionResult",
      "diff_count": 2,
      "diff_paths": ["V1.Tx.Operations[0].Body.PaymentOp.Amount"]
    },
    {
      "path": "testdata/handwritten.golden.json",
      "status": "skipped",
      "type": "TransactionEnvelope",
      "error": "no capture provenance, so there is nothing to re-fetch"
    }
  ]
}
```

Nothing else is printed: the progress lines and the rendered diff are for the
terminal, and the document carries the same information in a form a script can
use — the counts, the status of each fixture, and the paths that changed.

The exit status is the same in either format — `2` on drift, `1` when a
fixture could not be checked, `0` otherwise — so a pipeline can branch on the
exit code and read the document for the detail, or branch on the document
alone.

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
### Capturing from Horizon

Soroban RPC only keeps recent history, so a fixture for an old transaction
cannot be captured from it at all. Horizon reaches further back and covers
every transaction type, so it can be the source instead:

```sh
focus capture tx 2b2e276a8f99a292… --source horizon
```

`--source` is `rpc` (the default) or `horizon`. Each has its own default
endpoint per network, and `--rpc-url` overrides the one in use.

### Capturing from a history archive

RPC keeps a few weeks of history; a Stellar history archive keeps all of it.
`--archive-url` falls back to an archive for any transaction the source no
longer has. An archive is laid out by checkpoint rather than indexed by hash,
so give the ledger the transaction closed in:

```sh
focus capture tx 2b2e276a8f99a292… --network mainnet --ledger 3966820 \
  --archive-url https://history.stellar.org/prd/core-live/core_live_001
```

An archive holds envelopes and results, not meta. `focus verify` with
`--archive-url` uses the ledger each fixture recorded, so no `--ledger` is
needed there.

### Verifying offline

`--file-source <dir>` reads XDR dumps from a directory instead of the
network, so fixtures can be checked on an air-gapped CI runner:

```sh
focus verify testdata/ --file-source testdata/dumps/
```

A transaction is read from `{hash}.envelope.xdr`, `{hash}.result.xdr` and
`{hash}.meta.xdr`, a ledger entry from `{key}.entry.xdr` with the key in
URL-safe base64. Each file holds base64 XDR or the raw bytes; missing parts
are skipped.

### Network passphrases

A fixture records the passphrase of the network it came from, since a
transaction's hash and signatures are bound to it. RPC reports it; for
testnet, mainnet and futurenet it is known; for anything else, give it:

```sh
focus capture tx 2b2e276a8f99a292… --network standalone \
  --rpc-url http://localhost:8000/rpc --passphrase "Standalone Network ; February 2017"
```

### Sanitizing a capture

A capture from a staging network can carry data that must not be committed.
`--sanitize` masks it before the fixture is written:

```sh
focus capture tx 2b2e276a8f99a292… --sanitize
```

`--mask-memo` replaces text memos with `[REDACTED_MEMO]`, `--scrub-data`
replaces data entry values with `[REDACTED_DATA]`, and `--scrub-keys`
replaces anything shaped like a secret key (`S` and 55 more characters) with
`[REDACTED_SECRET_KEY]`; `--sanitize` is all three. The decoded section is
rendered from the masked XDR, so it is clean too.A sanitized fixture no longer matches the chain, so `focus verify` skips it.

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
source: rpc
rpc-url: https://mainnet.sorobanrpc.com
timeout: 45s
color: never
out: testdata
prefix: nightly
force: false
```

An explicit `--config` must exist; a missing `.focus.yaml` is simply ignored.
Invalid settings fail before any request is made.

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

## Fuzzing

The XDR this project has been reading is a seed corpus worth having: real
mainnet transactions, captured and kept as fixtures. `pkg/fuzz` hands them to
a fuzzer, embedded, so there is no path to get wrong:

```go
import "github.com/stellar-optics/stellar-focus/pkg/fuzz"

func FuzzDecodeEnvelope(f *testing.F) {
	for _, seed := range fuzz.CorpusFor("TransactionEnvelope") {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var env xdr.TransactionEnvelope
		_ = xdr.SafeUnmarshal(data, &env)
	})
}
```

```console
$ go test -fuzz FuzzDecodeEnvelope -fuzztime 30s
```

`fuzz.Corpus()` returns every seed, `fuzz.CorpusFor(type)` the seeds of one
XDR type, and `fuzz.Types()` the types the corpus covers.

## Project status

**Early but real.** v0.1 does what this README says. The library API may shift
before v1.0.

- Assertions for envelopes, results, fields and Soroban events.
- Golden files with `-focus.update`; capture and verify against a live network.
- Failure-message snapshots, a unified flag/env/config file, documented exit
  codes, and an offline end-to-end suite for the command.
- Soroban values can be matched and rendered by the contract's own field
  names, when the test supplies the contract's specification.
- Capture from Soroban RPC or from Horizon, and an embedded fuzz corpus of
  real mainnet XDR.

## Contributing

Adding an assertion or a fixture source is designed to be a one-file change.
See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/architecture.md](docs/architecture.md).

## License

Apache-2.0. See [LICENSE](LICENSE).

## Final Implementation
