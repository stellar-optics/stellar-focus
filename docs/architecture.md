# Architecture

How stellar-focus fits together, and where to make the changes contributors
most often want to make:

- [Adding an assertion](#adding-an-assertion)
- [Adding a fixture source](#adding-a-fixture-source)

## The organising principle

**The failure message is the product.**

Everything else — the input coercion, the ignore rules, the golden format — is
in service of one thing: when a test goes red, the developer should know what
changed without leaving the terminal. A general-purpose assertion library
already compares two strings correctly. The only reason to adopt this one is
that it understands XDR, and it demonstrates that in the failure output.

So the design questions that get the most weight here are *what does this
print when it fails*, not *what does this return when it passes*.

## Package layout

```
cmd/focus/            main; maps errors to exit codes
internal/cli/
  cli.go            root command, capture, version
  verify.go         verify: re-check stored fixtures against the chain
internal/faketb/      recording focus.TB — lets focus test its own messages
internal/fakesource/  in-memory capture.Source, so the CLI tests run offline
pkg/focus/
  focus.go            TB, Value coercion, panic guard, Encode
  safe_exec.go        panic-safe harness: SafeExec, SafeCall, PanicError
  terminal.go         ANSI colour helpers, field highlighting, TTY detection
  options.go          ignore and normalize options
  ignore_rules.go     ignore path matching: wildcards, globs, case-insensitive
  message.go          failure-message rendering        ← the product
  equal.go            structural equality assertions   ← seam: new assertions
  fields.go           envelope field assertions
  timebounds.go       time and ledger bound assertions
  memo.go             memo value assertions
  op_order.go         operation sequence assertions
  signatures.go       signer presence and threshold assertions
  results.go          transaction result-code assertions
  result_codes.go     per-operation result-code assertions
  soroban.go          events, return values
  constructor.go      deployment state and owner binding (__constructor)
  diagnostic_order.go diagnostic stream order and timelines
  simulation.go       simulated resource budget assertions
  fees.go             consumed and refundable fee assertions
  require/            Fatalf mirrors of everything above
  require/audit.go    AST parity audit between focus and require
pkg/golden/           golden files, -focus.update
pkg/capture/
  capture.go        Source interface, RPC impl, Verify  ← seam: new sources
  rpc.go            the Soroban RPC Source
  cache.go          Scraper: rate-limited fetches, .focus/cache
  bundle.go         multi-transaction capture with a manifest
  codegen.go        deterministic replay-test generation from fixtures
```

### Why the CLI is not in pkg/

`pkg/focus` is imported into other people's test suites. A test helper that
drags a CLI framework into a production module's dependency graph is a helper
people resent, so Cobra lives only in `cmd/` and `internal/cli`, which the
library never imports.

The permitted dependency set is: the standard library, `stellar/go-stellar-sdk`,
and `stellar-xdr-lens`. That is enforced by `TestLibraryStaysDependencyLight`
(compiled graph via `go list -deps`) and by the AST check in
`pkg/focus/deps_lint_test.go` (import specs, pinpointing file and line)
rather than left to discipline.

## The TB seam

```go
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Name() string
}
```

Taking an interface rather than `*testing.T` buys three things:

1. Assertions work in `testing.B` and `testing.F`.
2. A project can wrap them in its own helpers.
3. **focus can test its own failure messages**, by passing
   `internal/faketb.TB`, which records instead of failing. Since the output is
   the contract, this is not a convenience — it is what makes the contract
   testable at all.

`Helper()` is called by every assertion so `go test` reports the caller's
file and line rather than a line inside this library.

## Value coercion

Assertions take `Value` (an alias for `any`) and accept a base64 string, raw
`[]byte`, any `xdr.*` value implementing `encoding.BinaryMarshaler`, or a
`*lens.Value`.

This trades static type safety for ergonomics, deliberately. Test code holds
XDR in whichever form the system under test produced, and requiring a
conversion at every call site is friction in exactly the common case. An
unrecognised type fails immediately with a message naming what *is* accepted,
so the mistake surfaces as an input error rather than a confusing diff.

Coercion happens in one place, `coerce`, so every assertion produces the same
shape of message when an input is wrong.

## Never panic

Generated XDR accessors dereference union arms without checking the
discriminant, so a malformed value can panic deep inside the SDK. A panic
escaping into someone's test binary destroys the output of every other test in
the package.

Every assertion runs its inspection behind `guard`, which converts a panic
into an ordinary test failure that also asks for a bug report.
`pkg/focus/safe_exec.go` generalises this as `SafeExec`/`SafeCall` with a
`PanicError` that unwraps to the original cause for `errors.Is`/`errors.As`.
`TestAssertionsNeverPanic` feeds nil, empty strings, non-base64, truncated
payloads, wrong types and mismatched XDR through every exported assertion.

## Terminal output

Colour highlights exactly which field mismatched. `pkg/focus/terminal.go`
provides the ANSI helpers (`HighlightPath`, `FormatFieldMismatch`,
`StripANSI`/`ContainsANSI`) and TTY detection (`SupportsColorWriter`,
`PaletteForWriter`, `ColorEnabled`), matching lens's `ColorPalette` so a
focus failure and `lens diff` highlight the same way. Test output is
usually captured, so colour defaults to off via `focus.WithColor("never")`
and is enabled explicitly with `focus.WithColor("always")`; `auto` honours
`NO_COLOR` and `TERM=dumb`.

## Ignore rules

Without them the library is unusable on real suites — a rebuilt transaction
differs in sequence number, signatures and time bounds every run.

Patterns match `lens.Change.Path`, the dotted path lens already reports
(`V1.Tx.Operations[0].Body.PaymentOp.Amount`). Because lens does the
traversal, focus only has to *filter a list*, which keeps `ignore_rules.go`
small. The pattern language is deliberately forgiving, since an ignore
pattern that does not match is worse than useless:

- `*` matches one segment, `**` matches any number of segments, and an index
  can be wildcarded (`Operations[*]`);
- within a segment, `*`, `?` and character classes (`[abc]`, `[!abc]`) work
  like globs;
- matching is case-insensitive, so patterns can be written the way the field
  reads (`operations[*].sourceaccount`);
- a pattern is anchored to the tail of a path as well as its whole, so
  `operations[*].sourceaccount` does not need the `V1.Tx.` prefix.

Two rules that exist for trust rather than function:

- **Applied ignores are named in the failure output.** A difference that
  vanished without explanation makes every future failure less believable.
- **An ignore that matches nothing fails the assertion**, even on an otherwise
  passing comparison. A stale pattern is a test silently weakening; the moment
  it stops matching is the only moment anyone would notice.

## Diagnostic event order and timelines

A Soroban transaction's meta carries a *diagnostic* stream alongside the events
the contract chose to emit: the frames the host opened and closed (`fn_call`,
`fn_return`), the lines the contract logged, and the events of calls that
failed — which survive nowhere else. `diagnostic_order.go` asserts on that
stream, and on the two things about it that can be checked: the order of the
events, and the ledger the stream belongs to.

**Order is the assertion.** The stream is the host's account of what the call
did as it did it, so the right events in the wrong order describe a different
program. `AssertDiagnosticEventOrder` matches the wanted events as a
subsequence: each is looked for at or after the position the previous one was
found at, so listing every event pins every position and listing two pins only
their relative order. A failure names the wanted event that went unmatched,
where the search had reached, and — the case worth catching — that the same
event is present earlier in the stream, which is what a swap looks like.

**A diagnostic event has no timestamp.** Its XDR is a bool and a contract
event; there is no wall clock in it. The protocol's only time coordinate is the
ledger the transaction was applied in, so that is what
`AssertDiagnosticEventTimeline` checks, together with the close time that gives
the ledger its place in the chain. Both are *passed in* rather than looked up:
reading a clock or asking a node would make the assertion neither deterministic
nor offline, and `focus capture` already records exactly these two values as
fixture provenance (`golden.Provenance.Captured`).

The ledger is read from the meta rather than taken on trust. A transaction
writes the entries it changes during the ledger it executes in, so their
`LastModifiedLedgerSeq` is that ledger — and the *newest* pin is the one that
answers the question, because an entry restored out of the Soroban archival
state can keep an older last-modified ledger. A meta that pins no ledger, like
an empty stream, is reported as unplaceable rather than quietly passing a check
it was never able to make.

Reading the stream is one seam, `diagnosticStream`: it resolves the value
through the same `coerce` every assertion uses, walks it with the SDK's
`GetDiagnosticEvents` behind `guard`, and decorates each event with its index,
call depth and a pre-rendered description. Pre-rendering is deliberate — the
description is built while the value is known to be well-formed, so printing a
failure never calls a generated accessor on a value that could panic.

## Contract deployment state

A contract's `__constructor` runs once, inside the deployment transaction that
creates it, so everything it writes is in that transaction's meta. `constructor.go`
reads it there: no RPC call, no simulator, no clock. That is what makes a
deployment test able to say *which* state the contract was initialised with,
rather than only that `create_contract` succeeded.

Two places can hold that state, and both are read:

- the contract's **instance entry**, whose `ScContractInstance.Storage` map is
  instance storage — where a constructor that uses `env.storage().instance()`
  puts an owner or an admin;
- the separate **ContractData entries** a constructor creates for persistent and
  temporary storage.

Reading is done through the same flattened ledger changes the entry assertions
use (`ledgerChanges`), so both see the transaction the same way. Later writes to
a key replace earlier ones, so the state read is the state after the transaction,
and `state` changes — the value *before* an update — are skipped rather than
mistaken for something the transaction wrote.

The assertion requires the instance entry. It is the protocol's marker that this
transaction deployed the contract, so without it the state being asserted on
belongs to some other transaction, and saying so is more useful than reporting
every expected entry as missing.

Storage keys are matched against the key's symbol or string value, ignoring
case: a `#[contracttype] enum DataKey` variant serialises as `Owner` while the
field it backs reads `owner`, and a test should not have to know which one the
contract happened to use. An owner, though, must be an address — a value that is
not one is reported as the wrong *kind* of value, with the address forms named,
because that is almost always a constructor storing the wrong thing.

A failure shows each difference, the value wanted, the value found and where it
was found (`instance storage`, or the durability that holds it), then lists the
whole state the constructor wrote. The listing is the point: it is what turns
"owner is wrong" into "owner is wrong, and here is what else it wrote".

## Golden format

```json
{ "format", "type", "description", "xdr", "captured", "decoded" }
```

Field order is the file order, chosen so a diff shows identity first and the
large decoded tree last.

**`xdr` is the source of truth; `decoded` is for humans.** Comparisons never
read `decoded` — doing so would make assertions depend on lens's formatting,
so a cosmetic change in lens would fail everyone's tests. `decoded` exists
purely so a pull-request diff is reviewable, which is the whole reason not to
store a bare blob.

`format` is checked on load, so a file written by a future version is rejected
rather than silently misread.

### Fixtures in JSON, YAML or text

That data has three spellings, and `pkg/golden/serializer.go` is the seam
between them. Everything above the serializer — `Assert`, a failing diff,
`focus verify` — works with a `*golden.Fixture` and never learns which file it
came from:

```go
type Serializer interface {
	Format() Format
	Marshal(f *Fixture) ([]byte, error)
	Unmarshal(data []byte) (*Fixture, error)
}
```

| Format | Written as | Read as |
| --- | --- | --- |
| `json` | indented JSON, the default | `encoding/json` |
| `yaml` | block-style YAML | the subset below |
| `text` | one `dotted.path: value` leaf per line | dotted paths unflattened |

The text form uses the vocabulary lens already names a changed field with, so a
fixture reads like the failure message that produced it and
grep `Operations\[0\]` finds an operation without a parser in the loop. YAML is
the same tree in the shape a human hand-edits.

**The format is a property of the file name, not of the package.** `Save` and
`Load` take the extension (`FormatForPath`), `WithFormat` sets it for a `Golden`
and moves the default suffix with it, and `LoadAs`/`SaveAs` take it explicitly.
An unrecognised extension is read as JSON, so every fixture written before
these formats existed still loads.

Two properties hold for all three, and are tested as such:

- **Deterministic.** No clock, no network, and no map iteration order reaches
the output — decoded keys are sorted, and the fixture's own fields are written
in a fixed order — so the same fixture always produces the same bytes.
- **Normalized.** `Marshal` runs its output through `NormalizeWhitespace`:
  line endings become `\n`, trailing horizontal whitespace goes, blank-line
  runs collapse, and the file ends with exactly one newline. A CRLF checkout or
  a stray trailing space cannot become diff noise, and the readers normalize
  what they read for the same reason.

### Why the YAML is a documented subset

`pkg/golden` is imported into other people's test suites, and the permitted
dependency set is the standard library, the Stellar SDK and lens — enforced by
`TestLibraryStaysDependencyLight`. A YAML library would be inherited by every
consumer, so the YAML here is written and read by this package instead.

The supported subset is exactly what the writer emits — block mappings, block
sequences, single-line scalars, and the flow forms `{}` and `[]` — plus the
compact `- key: value` entry a hand-writer is likely to type, which is expanded
to the block form before parsing. Anchors, aliases, tags, block scalars and
multi-document streams are not read. Reading is deliberately strict: a tab
indent, an indentation jump, a duplicate key, or a path that is both a value
and a container is an error naming the line, because a fixture that silently
lost a field is a test that silently stopped checking something.

The emitted subset is ordinary YAML, not a private dialect: it loads with any
standard parser, which is what makes it worth hand-editing.

Scalars are written bare when that is unambiguous and as a JSON string literal
otherwise. Quoting is what keeps a round trip lossless: `testnet` stays a
string, `58002` stays a number, and the string `"58002"` is quoted so it does
not come back as one. Numbers are carried as `json.Number` end to end, so an
int64 amount is never rounded through a `float64` on the way.

Adding a fourth format is one file and one entry in `SerializerFor`; nothing in
`pkg/focus`, the CLI, or the comparison path changes.

## Capture and verify

`capture.Source` is the only thing in the repository that opens a network
connection:

```go
type Source interface {
	Name() string
	Network() string
	Endpoint() string
	Transaction(ctx context.Context, hash string) ([]Record, error)
	LedgerEntry(ctx context.Context, key string) ([]Record, error)
}
```

Everything above it is offline by construction, which is why `go test ./...`
needs no network — including the tests of the capture and verify commands,
which run against `internal/fakesource`.

`Capture` takes the timestamp as a parameter rather than reading the clock, so
fixtures are byte-stable and a test can assert on the whole file.

`Verify` refuses fixtures with no provenance instead of passing them. A
hand-written fixture is not stale; it is simply not checkable this way, and
saying so is more useful than a green tick that means nothing.

## Adding an assertion

1. Put it in the file matching its subject — `fields.go`, `results.go`,
   `soroban.go` — or start a new one.
2. Take `TB` first, return `bool`, call `t.Helper()`.
3. Resolve inputs through `resolve` or `summarize` so bad input produces the
   standard message.
4. Wrap anything that touches generated XDR accessors in `guard` (or
   `SafeExec`/`SafeCall` for standalone inspection).
5. Build the failure message from `message.go` helpers, and **write a test
   that asserts on the message text**, not merely that it failed.
6. Add the mirror in `require/` and keep `require.Audit` green: every
   `Assert* bool` needs a same-signature `Fatalf` counterpart
   (`TestAuditPassesOnRepo` enforces this).

Prefer building on `lens.Explain` over reaching into XDR directly: it means
the value a test asserts on is the same value `lens explain` prints, so an
assertion and a manual investigation can never disagree.

## Adding a fixture source

Implement `capture.Source` and wire it into `internal/cli`. Horizon and a
local core instance are the obvious candidates. Return one `Record` per
payload — an envelope, a result and a meta are three fixtures, not one — and
populate `Ledger` and `TxHash` so `focus verify` can re-fetch later.

Nothing in `pkg/golden` or `pkg/focus` needs to change.

## Testing approach

Fixtures in `testdata/` are real captured mainnet transactions, shared with
the sibling repositories so all three exercise the same data.

The suite targets properties that would make the library untrustworthy if
violated:

- failure messages name the changed path, show enriched values, and disclose
  ignored paths (`TestAssertEnvelopeEqualFailureMessage`);
- ignore patterns accept globs, case-insensitive names and tail-anchored
  paths (`TestIgnorePathsGlobAndTailMatching`);
- a stale ignore pattern is reported (`TestStaleIgnorePatternIsReported`);
- no assertion panics on hostile input (`TestAssertionsNeverPanic`);
- golden files are line-oriented and carry real field names
  (`TestGoldenFileIsReviewable`);
- every fixture format round-trips losslessly — including a large int64, a
  string that looks like a number, empty containers and a key containing a path
  separator — produces identical bytes on every run, and comes out already
  whitespace-normalized (`TestEveryFormatRoundTrips`,
  `TestEveryFormatRoundTripsAwkwardValues`, `TestEveryFormatIsDeterministic`,
  `TestOutputIsWhitespaceNormalized`);
- a `Golden` configured for YAML or text still fails with the field-level diff
  and the re-record instruction, and `-focus.update` records all three formats
  (`TestGoldenUsesTheConfiguredFormat`,
  `TestGoldenUpdateWritesEveryFormat`);
- captures are deterministic (`TestCaptureIsDeterministic`);
- the library stays dependency-light (`TestLibraryStaysDependencyLight` and
  the AST check `TestLibraryImportsStayLightAST`, which also bans heavy
  assertion/CLI frameworks);
- colourised failures highlight the mismatched field and strip cleanly to the
  plain message (`TestTerminalAssertionOutputViaFakeTB`);
- panics become ordinary failures with a bug-report prompt (`TestSafeExec*`);
- every `Assert*` has a `Fatalf` mirror with a matching signature
  (`TestAuditPassesOnRepo`);
- diagnostic streams are matched in order, with an absence and a swap named
  differently (`TestAssertDiagnosticEventOrderMessages`);
- a diagnostic stream is pinned to the ledger its meta pins, and a stream that
  cannot be placed is reported rather than passed
  (`TestAssertDiagnosticEventTimelineMessages`);
- constructor state is read from instance, persistent and temporary storage,
  with a missing entry, a wrong value and a wrong owner each named, and the
  state written listed underneath (`TestAssertConstructorStatePasses`,
  `TestAssertConstructorStateFailureMessages`);
- an owner binding must be an address, and one that is not is reported as the
  wrong kind of value (`TestAssertContractOwnerFailureMessages`);
- a meta that never deployed the contract — a classic meta, a wrong contract id,
  no instance entry — is reported as such rather than as a missing entry
  (`TestAssertConstructorStateDiagnosesBadInput`).
