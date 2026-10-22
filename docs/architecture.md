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
  e2e_test.go         builds and runs the real binary, offline
internal/cli/
  cli.go            root command, capture, version
  flags.go          flag, FOCUS_* env and config-file resolution  ← seam: new settings
  exitcodes.go      the exit-code contract and error classification
  verify.go         verify: re-check stored fixtures against the chain
internal/faketb/      recording focus.TB — lets focus test its own messages
internal/fakesource/  in-memory capture.Source, so the CLI tests run offline
pkg/focus/
  focus.go            TB, Value coercion, panic guard, Encode
  safe_exec.go        panic-safe harness: SafeExec, SafeCall, PanicError
  terminal.go         ANSI colour helpers, field highlighting, TTY detection
  snapshot.go         snapshot testing for failure messages
  options.go          ignore and normalize options
  ignore_rules.go     ignore path matching: wildcards, globs, case-insensitive
  message.go          failure-message rendering        ← the product
  equal.go            structural equality assertions   ← seam: new assertions
  fields.go           envelope field assertions
  timebounds.go       time and ledger bound assertions
  memo.go             memo value assertions
  op_order.go         operation sequence assertions
  signatures.go       signer presence, hint and threshold assertions
  results.go          transaction result-code assertions
  result_codes.go     per-operation result-code assertions
  soroban.go          events, return values
  auth.go             contract invocation authorizations
  ledger_entries.go   entry created/modified/deleted assertions
  balance.go          balance changes and asset resolution
  claimable.go        claimable balance creation
  managedata.go       account data key-value assertions
  liquidity_pool.go   pool reserve movements and LP share mint/burn
  dex_offers.go       offer creation and DEX trade matches
  clawback.go         clawback operations, forfeiture and revocation
  sponsorship.go      who pays an entry's reserve
  inflation.go        inflation payouts and ledger fee-pool assertions
  soroban.go          events, return values, contract errors
  spec.go             contract specifications: field names, and ContractSpec
  traps.go            contract traps and the host diagnostics that explain them
  constructor.go      deployment state and owner binding (__constructor)
  diagnostic_order.go diagnostic stream order and timelines
  simulation.go       simulated resource budget assertions
  fees.go             consumed and refundable fee assertions
  adapters.go         testify/assert and gomock-style adapters around assertions
  require/            Fatalf mirrors of everything above
  require/audit.go    AST parity audit between focus and require
pkg/golden/           golden files, -focus.update
pkg/fuzz/             seed corpora for downstream fuzzers
  fuzz.go            embedded corpus: Corpus, CorpusFor, Types
  testdata/          the captured mainnet XDR the corpus is built from
pkg/capture/
  capture.go        Source interface, RPC impl, Verify  ← seam: new sources
  rpc.go            the Soroban RPC Source
  horizon.go        the Horizon Source
  file_source.go    a Source over a directory of XDR dumps, for offline use
  archive_source.go the history archive Source, and RPC-then-archive fallback
  sanitize.go       masking of memos, data values and secret keys at capture
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

## CLI configuration

A setting has three spellings — a flag, a `FOCUS_`-prefixed environment
variable, and a config-file key — and `internal/cli/flags.go` is the only
place that decides which one wins:

```
explicit flag  >  environment variable  >  config file  >  built-in default
```

That order is Viper's, but it is stated here because it is a contract, not an
implementation detail: a script that passes `--network mainnet` must get
mainnet even when a repository config file says testnet. `newConfig` builds a
*new* Viper instance rather than touching the global one, so the CLI never
shares configuration state with anything else that imports Viper.

Every key is the flag name (`--rpc-url` becomes `FOCUS_RPC_URL` and the YAML
key `rpc-url`), so help text, environment and config file use the same
vocabulary. The capture settings (`--out`, `--prefix`, `--force`) live on the
`capture` parent command so both subcommands share one definition rather than
each re-declaring them.

An explicit `--config` must exist; a missing `.focus.yaml` in the working
directory is not an error, because most projects have no focus config at all.
Values are validated once, in a persistent pre-run hook, before a command does
any work or opens a connection — an unknown network, a non-positive timeout or
an unrecognised colour fails immediately and offline.

## Exit codes

`internal/cli/exitcodes.go` owns the command's exit-code contract:

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | the command could not run: bad usage, unreadable file, unreachable endpoint |
| 2 | `verify` ran and found fixtures that no longer match the chain |

The distinction that matters is between *could not check* and *checked and
found something wrong*: a CI job should retry or alert differently on an
unreachable endpoint than on genuine drift, so the two do not share a code.
Errors carry sentinel causes (`ErrDrift`, `ErrUsage`) so callers can classify
with `errors.Is`, and the code survives wrapping because `ExitCode` uses
`errors.As`. `cmd/focus/main.go` stays tiny: it asks for the code and whether
to print, then exits.

The codes live here rather than in each command so any focus command compares
data the same way: `verify` uses `ExitDrift` for fixtures that moved, and a
future `focus diff` (see roadmap) would take its "values differ" code from the
same place rather than inventing a second convention.

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

The structural diff itself is rendered by `message.go`, not by lens's
formatter. Each change gets the field path on its own line and then one
labelled line per side — `want │ …` beside `got  │ …` — because a reader
asking "which of these was I expecting?" is not answered by a `-`/`+` sign.
The labels sit in a fixed column so several changes line up, and the colours
follow the palette (`want` in the removal colour, `got` in the addition
colour) so stripping the escapes leaves the plain message byte for byte.
A value present on one side only renders a single labelled side rather than
an empty pair, and the elision notice for `WithMaxChanges` stays a single
line. If the walker reports a change with no path — the two sides are
different kinds of value — the path column reads `(value)`.

## Snapshot testing

Because the failure message is the product, a refactor that quietly changes
its layout is a regression even when every assertion still returns the right
boolean. `pkg/focus/snapshot.go` pins that text: `AssertSnapshot` compares a
string with a file under `testdata/snapshots` (`.snap` by default), and
`AssertFailureSnapshot` runs an assertion against a recording `TB` and
snapshots what it reported.

Snapshots are deliberately plain text. A reviewed `.snap` shows exactly what
a developer will see in a red test, and reviewing its diff is reviewing the
formatting change. `FOCUS_UPDATE_SNAPSHOTS=1 go test ./...` rewrites the
files, the same way `-focus.update` rewrites golden fixtures; an explicit
`WithSnapshotUpdate` wins over the environment for a single call.

Three choices keep the mechanism trustworthy:

- **A missing snapshot is a failure, not an empty expectation.** An absent
  file is how a new snapshot is created, and it must not pass as though the
  output matched nothing.
- **Only line endings and the final newline are normalised.** A CRLF checkout
  does not produce noise, but a change in indentation or trailing whitespace
  inside a line — exactly what a visual snapshot is for — still fails.
- **Names cannot escape their directory.** Absolute paths and `.` or `..`
  segments are rejected, so copied input cannot make a test read or write
  outside `testdata/snapshots`.

`TestFailureMessageSnapshot` is the integration point: it snapshots a real
`AssertEnvelopeEqual` failure, so a change to the diff layout is a red test
with a one-line snapshot review rather than a silent change in what users see.

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

## Contract traps and host diagnostics

A trap is the host aborting an invocation: the contract never returns, so
there is no return value to assert on. `AssertContractTrapped` answers the
first half of the question from the transaction result — an operation result
of `invoke_host_function_trapped` — but the result stops there. *Why* the call
died is recorded only in the host's diagnostic stream, as an `error` event
whose payload is an `ScError`.

`AssertContractTrappedWith` (`traps.go`) checks both halves and takes the
substring a test expects to find, such as `Error(Contract, #4)`.

**Evidence has two shapes, and both are offline.** A Soroban RPC
`getTransaction` response carries everything in one value (`ResultXDR` for the
trap, `DiagnosticEventsXDR` for the stream), so it is accepted directly. A
captured fixture set keeps the result and the meta in separate files, so
`TrapEvidence{Result, Meta}` is how those two are handed over together. The
switch in `trapEvidence` is the seam: another carrier is one more case, and
nothing above it needs to change.

**A bare result is refused, not passed.** A `TransactionResult` records that
an operation trapped and carries no diagnostics at all. Returning true would
mean the assertion quietly skipped the check it was created for, so it fails
with the carrier it needs named instead. The same reasoning rejects a blank
expected substring, which would match every stream.

**The substring is matched against what the failure prints.** Events are
decorated once, by `decorateDiagnostics`, with the same one-line description
`describeDiagnosticEvent` builds for the ordering assertions, and the match
runs over those rendered lines, so what was searched and what is shown can
never disagree. `describeScError` renders a host error the way the SDK and
the Stellar CLI do: `Error(Contract, #4)` for a contract's own code,
`Error(WasmVm, InvalidAction)` for a host-side fault. Reading the union arms
directly rather than through the generated accessors keeps a malformed value
renderable instead of panicking. Matching is case-sensitive; a hit that
differs only in case is called out in the failure, because that is almost
always the mistake, and an empty diagnostic stream is reported as
unsearchable rather than treated as a pass.

## Contract specifications: the names the contract chose

A Soroban payload on the wire is a map of symbol keys, and an ScVal
discriminant says what each value is. That is enough to compare two
payloads, and not enough to read one: the developer who wrote the contract
knows the payload as `{from: Address, amount: u128}`, and a failure that
says `ScMapEntry` does not tell them which field is wrong.

`spec.go` reads a contract's specification — the `ScSpecEntry` list that
names its structs, unions, enums, events and functions — and turns it into
the names a failure message uses.

**The specification is an input, not a mode.** `Event.Spec` and
`AssertReturnValueWithSpec` take one; every other path is unchanged. A test
that cannot supply a specification still works, and a specification never
changes *what* matches, only how it is described. That is the rule the
tests pin: the same matcher passes and fails the same way with and without
one.

**The specification is indexed once, per assertion.** `newSpecIndex` walks
the entries into maps by name; an entry whose union arm is missing is
skipped rather than fatal, because a broken specification should cost the
names it carries, not the whole assertion. `resolveType` then turns a type
definition into the *shape* of the value it describes — named fields, a
sequence, or a plain value — and every comparison and every rendering goes
through that shape. One shape, two uses: it decides how a wanted value is
compared, and how an actual value is printed.

**Both names are shown.** The contract's field names and the XDR type are
printed together (`{from: GCJNH…ZST6, amount: u128(100)} [ScValTypeScvMap]`),
because a reader may know one and not the other. The failure names the
field that differs (`amount: want u128(200), got u128(100)`) rather than
leaving the reader to compare two maps.

**The specification can come from the contract itself.**
`focus.ContractSpec(wasm)` reads the `contractspec` custom section out of a
compiled contract, so a test that has the contract's bytes needs no second
file. The WebAssembly binary format is walked directly rather than decoded:
the section is the only part of the module that matters here, and a
dependency to parse the rest of it would cost more than it saves.

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

## Contract invocation authorizations

A Soroban transaction states who may act on its behalf through the
`SorobanAuthorizationEntry` list on each `invoke_host_function` operation:
one entry per address the contract required authorization from, naming the
invoker (its credentials) and the invocation tree that invoker authorized
(its root invocation, with every call beneath it). `auth.go` asserts on that
list, read from the envelope the test already holds — no RPC call, no
simulator, no clock — so an invocation test is deterministic and offline for
the same reason a deployment test is.

The expected invoker is a strkey account or contract address. An entry with
`SOROBAN_CREDENTIALS_SOURCE_ACCOUNT` authorizes as the transaction's source
account — the inner transaction's, for a fee bump — so passing the source
account matches either credential kind; the address credentials (v1, v2 and
the delegated form) resolve to the address they carry. Credentials whose
discriminant claims an address the value does not carry never resolve to an
invoker: they are reported as what they are, not silently skipped.

The expected call is a matcher in the shape the package's events already
use — contract, function and arguments, with any field left open matching
anything. Matching walks the entry's whole invocation tree, because an
authorization covers its root invocation and every call beneath it, and a
test may assert on any of them. Arguments convert the way
`AssertConstructorState` converts storage values — a strkey becomes an
address, a plain string a symbol — and are compared by canonical XDR
encoding, the same trick constructor state uses so a generated `Equals`
call can never panic on an unexpected arm.

Two things the assertion deliberately does not do:

- **It does not verify signatures.** What a signature signs is derived from
  the network passphrase, which an assertion is never handed; verifying it
  would also make the result depend on a fact outside the envelope. The
  listing instead shows each entry's nonce and expiration ledger, so a test
  can pin those.
- **It does not judge entries the expectation did not mention.** A
  transaction may authorize more than the call under test; the assertion is
  about this invoker and this call.

A failure lists every authorization the envelope carries — invoker,
credential kind, nonce and expiration, and the invocation tree it authorizes
— and, when a partial match narrows the problem, the single difference that
failed, with the value wanted and the value found. The listing is rendered
while the values are known to be well-formed, so printing a failure never
calls a generated accessor on a value that could panic; the guard runs over
the expectation as well as the envelope, because an expected argument is
usually a hand-built `xdr.ScVal` and the generated encoder dereferences
unset union arms.
## Revocation, sponsorship and fee-pool history

Three subjects share a property: the operation is not the evidence, the meta
or the result is.

**Clawback** (`clawback.go`) forfeits an issued asset, either from a
trustline or by revoking a claimable balance before it is claimed. The
operation assertions (`AssertClawbackOp`,
`AssertClawbackClaimableBalanceOp`) read the envelope, accepting a fee-bump
wrapper and a V0 envelope, and rejecting the native asset because it cannot
be clawed back. The meta assertions (`AssertBalanceForfeited`,
`AssertClaimableBalanceRevoked`) are the ones that prove the value moved: a
full clawback removes the trustline, and a revocation removes the balance
entry, so the assertion reads the flattened ledger changes and reports a
balance that merely shrank as exactly that. Balance ids are accepted as the
id, a `LedgerKey` or a `LedgerEntry` for the balance, or any of those as
base64, bytes or a decoded `*lens.Value`, since a test usually holds the
entry the create transaction produced.

**Sponsorship** (`sponsorship.go`) is recorded on the entry itself, in
`LedgerEntryExtensionV1.SponsoringID`, and a revoke-sponsorship operation
can transfer, establish or revoke depending on the entry's prior state.
`AssertEntrySponsoredBy` therefore walks the entry's changes, reads the
state before the first write and the entry's final value, and names the
transition it found — established, transferred, revoked or unchanged — so
"sponsored by the wrong account" and "not sponsored at all" never read the
same. A removed entry carries no sponsorship to inspect and is reported as
such, and the sponsor must be a real account address rather than an empty
string that could quietly match an unsponsored entry.

**Inflation and the fee pool** (`inflation.go`) are protocol history: the
payouts an inflation operation actually made live in its operation result,
and the pool they came from lives in the ledger header. Assertions take
those values directly (`AssertInflationPayouts`, `AssertInflationPaid`,
`AssertFeePool`, `AssertFeePoolChanged`) rather than fetching a ledger, so
they are as offline as the rest. The dormant-inflation outcome,
`InflationResultCodeInflationNotTime`, is treated as "no payouts", which is
what a suite on a current network sees; a failed transaction or a missing
inflation operation is reported as such rather than as an empty payout list.
## Ledger entry assertions, by entry kind

Every assertion that reads the ledger sits on top of `ledger_entries.go`,
which flattens a meta's `LedgerEntryChange` list once and sanitises it: each
change is reduced to a canonical key id, the account it belongs to, a
pre-rendered label, and the entry value, so the assertions above it match on
strings and integers and never call a generated accessor on a value that could
panic. `balance.go`, `trustline.go`, `claimable.go`, `managedata.go`,
`liquidity_pool.go`, `dex_offers.go`, `clawback.go` and `sponsorship.go` all
read that one form, which is why a failure from any of them can list the same
ledger changes underneath it.

What they have in common is the reason they read the meta at all: for each of
these entry kinds, the thing a test wants to assert on does not exist in the
envelope.

### Claimable balances

A balance id is derived by the protocol from the operation, the source account
and the sequence number, so the transaction that creates a claimable balance
cannot name it — the meta is the first place the id exists.
`AssertClaimableBalanceCreated` therefore matches on what the test does know:
the claimant and the amount. The claimant has to be *among* the balance's
claimants rather than its only one, because a balance claimable by either of
two accounts is claimable by each.

The predicate guarding a claim is deliberately **not** checked. Whether it is
satisfiable depends on a clock, and reading one would make the assertion
neither deterministic nor offline. It is rendered in the failure listing
instead — compactly, including the composite `and`/`or`/`not` forms — because a
balance with the right claimant and an unexpected predicate is the usual reason
a test is looking here.

The other end of a balance's life — the entry being gone — is a single ledger
change, whichever operation caused it, and `AssertClaimableBalanceRevoked` in
`clawback.go` already asserts exactly that. There is deliberately no second
name for it here: one ledger fact, one assertion, and a test that needs to know
*which* operation removed the balance pairs that assertion with
`AssertClawbackClaimableBalanceOp` or an operation-result assertion.

### Account data entries

Manage-data is last-write-wins, and assignment and deletion share one
operation. A transaction can set a name twice, or set and then delete it, and
only the meta says which value survived — so `managedata.go` reduces a meta's
changes to the state of each entry *after* the transaction and asserts against
that. An entry written and then deleted does not satisfy
`AssertAccountDataEqual`; `AssertAccountDataDeleted` is the assertion for it.

A data value is opaque bytes, so it is compared as bytes. There is no XDR
coercion on the value argument: a string is the value, never base64 to decode,
which is the only reading that does not surprise someone storing `"v2"`.
Rendering is the other half of that decision — quoted text where the bytes are
text, hex where they are not, always with the length, and the **first byte that
differs** named on its own line. A trailing newline, a padded field or the
wrong one of two similar values is invisible in a side-by-side of two quoted
strings, and that is precisely the class of bug a data assertion catches.

`AssertAccountDataEqual` takes no account, matching the manage-data vocabulary
where a test usually knows there is one account writing data; where more than
one account has an entry with that name, any of them holding the value
satisfies it and `AssertAccountDataEqualFor` pins down which is meant.

### Liquidity pools

A deposit operation names the maximum amounts it will put in and the price
range it will accept; the protocol then decides the actual amounts from the
pool's reserves at execution. A withdrawal names the shares to burn and gets
back whatever share of the reserves they represent. So the amounts a test wants
to pin down exist only as the difference between the pool entry before the
transaction and after it, which is what `liquidity_pool.go` measures — across
the whole transaction, so a meta that deposits twice is checked against the
total.

`PoolChange` holds magnitudes rather than signed deltas, and the direction
comes from the assertion name. That makes the call site read as what happened,
and it lets a direction mistake be reported as one: a negative field is an
input error, and a movement the other way names the other assertion.

**Shares are the direction signal.** Reserves move for a trade as well, so what
separates a deposit from a swap is that a deposit mints pool shares and a swap
does not. A reserve movement with no share movement is therefore reported as a
trade against the pool rather than as a wrong amount, which is the difference
between a useful failure and a confusing one.

### Offers and trades

An offer that crosses the spread is filled on submission, and what reaches the
ledger is the remainder — or nothing at all. So "the offer was created" and
"the trade happened" are different claims about the same operation, and
`dex_offers.go` keeps them as separate assertions: `AssertOfferCreated` reads
the `OfferEntry` the transaction left behind, and says so when there is none,
naming `AssertTradeExecuted` as the assertion a completely filled offer needs.
The offer id is the other reason this reads the ledger: a new offer is
submitted with id 0 and the protocol assigns the real one.

`AssertTradeExecuted` accepts either a result or a meta, and the difference is
stated in the failure rather than papered over:

- A **result** carries the protocol's own record of each match — a `ClaimAtom`
  per fill, with both assets and both amounts — across the four operations that
  can match (the two manage-offer forms, the passive variant, and the two path
  payments). It can answer every field of `Trade`, including trades against a
  liquidity pool, which exist nowhere else. Prefer it.
- A **meta** carries the offer entries that changed. The amount an offer gave up
  is exact, being the difference between its state and its last recorded value,
  but what it received is recorded nowhere in the meta. Deriving it from the
  price would mean reimplementing the protocol's rounding, so `AmountBought` is
  reported as a question a meta cannot answer, naming the result instead. A
  meta also cannot say *why* an offer shrank: an owner reducing their own offer
  looks exactly like one that was matched.

A `Trade`'s amounts and assets are from the matched side's point of view, the
way the protocol records them: `Sold` is what the offer or pool gave up. For
the submitting account the two are the other way round, which is documented
loudly because getting it backwards is the easiest mistake to make here.

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

Fixtures are also checked against the embedded `pkg/golden/fixture.schema.json`
when loaded, and callers can use `golden.Validate` before writing generated
fixtures. This keeps validation offline and adds no schema dependency. The
schema checks the fixture shape, provenance fields and unexpected properties;
the format version remains checked by the package for a version-specific
error.

`golden.WithVariables` renders Go template placeholders in loaded fixture
strings before comparison. Missing variables fail the assertion rather than
leaving a placeholder in the expected XDR:

```go
golden.New(t, golden.WithVariables(map[string]string{
  "AccountID": accountID,
})).Assert("payment", actual)
```

`golden.Discover` returns sorted, nested fixture names for `.golden.json`
files. `golden.Run` uses those names as subtests and asks a callback for each
actual value. `golden.ExportFuzzSeeds` writes each fixture's XDR as a standard
Go fuzz corpus string seed into the directory supplied by the caller, such as
`testdata/fuzz/FuzzEnvelope`. These helpers only read or write local files;
they do not contact a network.

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

Four capture shapes extend `Source` with their own seams so the same offline
rule holds:

- `capture.SimulationSource` (`pkg/capture/simulate.go`) serves preflight
  `simulateTransaction` responses. `Simulate` reports a host-rejected
  invocation through `ErrSimulationFailed` — carrying the host's error text —
  rather than an unmarked success, and `Records` splits the response into one
  record per XDR payload: the `SorobanTransactionData` (which carries the
  resource footprint), each `DiagnosticEvent`, each authorization node in the
  tree, and each host function return value.
- `capture.HeaderSource` (`pkg/capture/ledger_header.go`) serves the latest
  ledger header, the anchor a historical capture is pinned to.
  `ParseLedgerHeader` decodes the base64 `LedgerHeaderHistoryEntry` into the
  fields assertions are named for — protocol version, base fee, base reserve,
  total coins, sequence — and refuses a failed decode rather than returning a
  zero struct an assertion would pass against nothing.
- `capture.Mutate` (`pkg/capture/mutate.go`) turns a real captured XDR into a
  deterministically malformed variation: for a given seed and step count the
  same corruptions (bit flips, zeroed bytes, `0xFF` fills, truncation) always
  produce the same mutant, and `Mutations` reports exactly which offsets were
  touched so a resilience test can name what it exercised.
- `capture.ContractWASMSource` (`pkg/capture/contract_wasm.go`) serves
  deployed contract code. `ContractCodeKey` turns a contract id into the
  base64 `LedgerKey` that names its WASM, and refuses an id that is not 32
  hex bytes rather than sending a malformed key that would come back "not
  found" and read as "not deployed". The record keeps the whole
  `LedgerEntryData` so verification re-checks the entry the network returns
  rather than a projection of it; `ParseContractWASM` decodes the code hash
  and bytes for assertions, and reports `ErrNotContractCode` for an entry
  that turns out to hold something else. Before writing anything, the
  capture checks the bytes against the ledger's own code hash — contract
  code is addressed by the hash of its bytes, so this is free — and fails
  with `ErrContractCodeHashMismatch` instead of archiving a contract that
  does not match the one the ledger names.
- `capture.HorizonSource` (`pkg/capture/horizon.go`) serves Horizon's REST
  API, which reaches further back than a Soroban RPC endpoint's retention
  window and covers every transaction type. It returns the same three
  records per transaction — envelope, result, meta — so a fixture captured
  from Horizon is indistinguishable from one captured from RPC. The CLI
  chooses between them with `--source rpc|horizon`; each source has its own
  default endpoint per network, because an RPC endpoint and a Horizon
  endpoint are different services.
- `capture.FileSource` (`pkg/capture/file_source.go`) replays dumps from a
  directory — `{hash}.{envelope,result,meta}.xdr` and `{key}.entry.xdr` —
  so a fixture can be captured or verified with no network at all. The CLI
  selects it with `--file-source <dir>`.
- `capture.ArchivalSource` (`pkg/capture/archive_source.go`) reads a
  history archive's checkpoint files. An archive is not indexed by hash, so
  it implements the optional `LedgerTransactionSource` and needs the ledger
  a transaction closed in; `FetchTransaction` passes that hint where a
  source can use it, and `Verify` takes it from the fixture's provenance.
  The envelope is found by hashing each candidate with the passphrase the
  archive records. `capture.FallbackSource` asks RPC first and the archive
  on `ErrNotFound`; the CLI builds one when `--archive-url` is given.
- Provenance records the network passphrase: from a source that reports it
  (`NetworkPassphraseSource`), else from `golden.KnownPassphrases`, else
  from `capture.WithPassphrase` (`--passphrase`). `Verify` refuses a
  fixture whose passphrase the source contradicts.
- `capture.Sanitizer` (`pkg/capture/sanitize.go`) rewrites records before
  they become fixtures, masking text memos, data values and secret-key
  shaped strings. Sanitized and anonymized fixtures are marked in their
  provenance and are not verified against the chain.

## The fuzz corpus

`pkg/fuzz` hands the repository's captured XDR to a downstream fuzzer. The
corpus lives in `pkg/fuzz/testdata`, embedded with `//go:embed`, so it is
available wherever the module is imported and no filesystem path can be got
wrong.

The fixtures moved there rather than being copied: one corpus, one home. The
manifest beside them says which XDR type each file holds, so `CorpusFor`
can select by type without inferring it from a filename, and a test asserts
that the embedded corpus and the files on disk are the same payloads — a
corpus that drifts from its captures is worse than no corpus.

One seed is built rather than captured: the chain's meta was not captured
with the transactions, and a meta is where the interesting structures live —
contract events and ScVal payloads. It is built deterministically, so the
corpus does not change from run to run.

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

## Assertion adapters

`pkg/focus/adapters.go` lets focus assertions be reported by ecosystems that
do not take a `TB`, without any of them entering this repository's
dependency graph — the compiled-graph and AST tests fail the build if
testify or gomock appears in `pkg/focus`'s imports, test files included.

Two shapes are provided:

- **Errors.** `focus.Check` runs any assertion against a private recording
  TB and returns its failure text as an error; `ErrSucceeded`, `ErrResultCode`
  and the rest of the `results.go` set are shorthand around the corresponding
  assertions. testify's `assert.NoError(t, err)` and gocheck's
  `assert(c, IsNil, err)` then print the same descriptive diff a direct focus
  failure would have printed.
- **Matchers.** `focus.Matcher` declares exactly the method set both gomock
  modules require — `Matches(x any) error` plus `String() string` — and
  `Match` plus the built-ins (`MatchSequence`, `MatchSourceAccount`,
  `MatchResultCode`, …) build values that satisfy it structurally. A
  generated mock accepts them as argument expectations; a miss returns the
  underlying assertion's own failure text, and `String()` is what gomock
  prints for the expectation.

Adapters obey the same rules as assertions: never panicking (a panic inside
the assertion is recovered by `SafeCall` and returned as an error carrying
the bug-report prompt), no I/O, no network. `adapters_test.go` proves each
adapter's error is byte-identical to what the direct assertion reports to
`internal/faketb`.

## The `focus assert` command

`focus assert <tx-xdr>` is a terminal assistant that turns a transaction
someone already has into the assertions describing it: it decodes the base64
with lens, offers one suggestion per field the transaction carries, and
prints the kept ones as paste-ready Go (`cmd/focus/interactive.go`).

What it promises:

- **stdout is generated code, stderr is the conversation.** Prompts,
  suggestions and counts go to stderr, so `focus assert … > snippet.go`
  yields nothing but calls, and `--all` emits everything with no prompting.
- **Suggestions are honest.** Each is built from the same `lens.Explain`
  summary the assertions compare against, optional fields (memo, fee-bump
  wrapper, time and ledger bounds) are offered only when the transaction
  carries them, and `--var` names the generated variable. Every suggestion
  keeps the assertion behind it, so the tests can run each one against its
  own transaction (passes) and against a different one (fails with a
  descriptive diff) through `internal/faketb`.
- **End of input keeps the rest.** Answers are `y` or Enter, `n`, `a`, `q`;
  when stdin ends, the remaining suggestions are kept, so a short script
  never truncates the output.
- **Offline.** Decode, summary and every suggested assertion run locally;
  there is no network call on this path.

The command is assembled in `cmd/focus` and registered with the root
command through `cli.Execute`'s extra-command parameter, which keeps the
interactive flow beside `main` while `internal/cli` stays about fixtures.

## Adding a fixture source

Implement `capture.Source` and wire it into `internal/cli`. RPC, Horizon,
a dump directory and a history archive exist; a local core instance is the
obvious next candidate. Return one `Record` per
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
- failure-message formatting is pinned by snapshots, so a layout regression is
  a reviewable diff (`TestFailureMessageSnapshot`, `TestAssertSnapshot*`);
- CLI settings resolve in exactly one order — flag over environment over config
  file over default — and an invalid value fails before any request
  (`TestFlagOverridesEnvironment`, `TestFlagOverridesConfigFile`,
  `TestConfigValidation`);
- exit codes separate "could not check" from "checked and found drift"
  (`TestExitCodeMapping`, `TestVerifyDriftExitsWithDriftCode`,
  `TestVerifyReportsUncheckableFixtures`);
- the built binary is exercised end to end with no network
  (`cmd/focus/e2e_test.go`);
- a structural diff renders each change side by side, labelled `want`/`got`,
  with colour stripping back to the plain message and no line growing past
  200 characters (`TestFailureMessageSideBySide`,
  `TestFailureMessageAddedAndRemoved`, `TestFailureMessageColorized`);
- a clawback is matched by from, asset and amount with every mismatch named,
  and forfeiture is proven from the meta — including a full clawback that
  removes the trustline (`TestAssertClawbackOp`,
  `TestAssertClawbackOpFailureMessage`, `TestAssertBalanceForfeited`);
- a revoked claimable balance is matched by id, key or entry, and a balance
  left present is reported as surviving
  (`TestAssertClaimableBalanceRevoked`);
- sponsorship reads the entry's extension and names the transition —
  established, transferred, revoked — rather than only the final sponsor
  (`TestAssertEntrySponsoredBy`, `TestAssertEntrySponsoredByFailureMessages`);
- inflation payouts are compared in order, the not-time outcome means no
  payouts, and a fee-pool change is signed and order-checked
  (`TestAssertInflationPayouts`, `TestAssertInflationPaid`,
  `TestAssertFeePool`, `TestAssertFeePoolChanged`);
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
- an adapter's error is byte-identical to the failure the direct assertion
  reports to `internal/faketb`, and adapters never panic on hostile input
  (`TestErrAdaptersAgreeWithTheDirectAssertions`, `TestMatchersNeverPanic`);
- assertions generated by `focus assert` hold against their own transaction
  and fail with a descriptive diff against a different one
  (`TestGeneratedAssertionsHoldAndFailDescriptively`);
- a diagnostic stream is pinned to the ledger its meta pins, and a stream that
  cannot be placed is reported rather than passed
  (`TestAssertDiagnosticEventTimelineMessages`);
- a contract trap is proven from the result and explained from the host's
  diagnostics, searched both through an RPC response and through a `TrapEvidence`
  pair of fixtures, with a bare result, a blank substring and an empty stream
  each reported rather than passed, and a case-only near miss named
  (`TestAssertContractTrappedWithPasses`,
  `TestAssertContractTrappedWithFailureMessages`);
- host errors render as `Error(Contract, #4)` and `Error(WasmVm, InvalidAction)`,
  with an arm outside the enum named by number rather than left blank
  (`TestDescribeScErrorRendering`, `TestDescribeScError`);
- a claimable balance is matched by claimant and amount, with a wrong claimant,
  a wrong amount and a balance nobody can claim each named differently, and the
  claim predicates — including the composite forms — listed underneath
  (`TestAssertClaimableBalanceCreated*`, `TestClaimPredicateRendering`);
- account data is compared as bytes, with the first differing byte named and a
  trailing or missing byte reported rather than hidden
  (`TestAssertAccountDataEqualFailureMessages`,
  `TestAccountDataValueRendering`);
- a pool movement is measured across the whole transaction, and a withdrawal
  asserted as a deposit, or a swap asserted as either, names what it actually
  was (`TestAssertLiquidityPoolFailureMessages`);
- an offer that was filled completely is reported as a trade rather than as a
  missing entry, and a meta is reported as unable to establish what a matched
  offer was paid (`TestAssertOfferCreatedFailureMessages`,
  `TestAssertTradeExecutedFromMeta`);
- constructor state is read from instance, persistent and temporary storage,
  with a missing entry, a wrong value and a wrong owner each named, and the
  state written listed underneath (`TestAssertConstructorStatePasses`,
  `TestAssertConstructorStateFailureMessages`);
- an owner binding must be an address, and one that is not is reported as the
  wrong kind of value (`TestAssertContractOwnerFailureMessages`);
- a meta that never deployed the contract — a classic meta, a wrong contract id,
  no instance entry — is reported as such rather than as a missing entry
  (`TestAssertConstructorStateDiagnosesBadInput`);
- an authorization mismatch names the invoker and the call, lists the
  authorizations present — credential kind, nonce and expiration — and
  distinguishes a transaction that never invoked a contract from one whose
  call required no authorization (`TestAssertContractAuthValid*`).
