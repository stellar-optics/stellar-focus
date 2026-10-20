# Roadmap

An honest account of where this is going. Dates are intentions, not commitments.

## Where things stand (v0.1)

Working and tested:

- Structural equality assertions for envelopes, results and metas, with
  failure output that names the changed field path
- Field assertions: source, fee, sequence, memo, operation counts and types,
  signature count, fee-bump, and the source account an individual operation
  names — including the account it falls back to when it names none
- Time- and ledger-bound assertions, comparing the window a precondition sets
  against the window a test expects
- Memo assertions by value: text, ID, 32-byte hash and return memos, each in
  the form the caller already holds it, plus the assertion that there is none
- Operation-sequence assertions, matching the exact ordered operation types
- Signer assertions: which keys attached a signature — including pre-auth-tx,
  hash-x and signed-payload signers, and both signature sets of a fee-bump
  envelope — plus M-of-N signature thresholds, and the same question asked by
  four-byte signature hint
- Result assertions using Horizon's short codes, with lens's plain-English
  explanation and hint; operation result codes can be checked by short name,
  and an unknown code or an XDR constant name is called out as such
- Soroban: event matching, event counts, return values, contract traps
- Contract errors by category and code: the `ScError` a failed invocation
  recorded — a contract's own error code, or one of the host's named codes —
  read from the Soroban return value or from the host's error diagnostic
- Contract traps explained from the host's own diagnostics: the result proves
  the trap and the diagnostic stream says why — `Error(Contract, #4)`,
  `Error(WasmVm, InvalidAction)` and the rest — read from a `getTransaction`
  response or from a result/meta fixture pair with no network call
- Soroban diagnostic streams: the order of a contract call's diagnostic events
  — call frames, logs, and the events of calls that failed — and the ledger the
  stream was emitted in, with that ledger's close time
- Soroban simulation resource budgets, from either the transaction's
  `SorobanTransactionData` or a simulate-transaction response
- Contract deployment state: the storage a `__constructor` writes — instance,
  persistent and temporary — and the owner it binds, read from the deployment
  transaction's meta with no network call
- Clawback assertions: the clawback operations themselves, balance forfeiture
  proven from the meta — including a full clawback that removes the trustline
  — and claimable balance revocation by id, key or entry
- Sponsorship assertions: who pays an entry's reserve, read from the entry's
  `SponsoringID` across the meta's changes, with the transition it made —
  established, transferred, revoked — named on failure
- Inflation and fee-pool assertions: the payouts an inflation operation made,
  per-account totals, and the fee pool before, after and between ledger
  headers, all from supplied results and headers with no network call
- Claimable balances: the balance a transaction created, matched by claimant
  and amount, with each balance's claimants and claim predicates listed in a
  failure (the removal is covered by the clawback assertions above)
- Account data (manage-data): the key-value entries a transaction left on an
  account, compared as bytes, with the first differing byte named and
  deletion asserted separately from assignment
- Liquidity pools: the reserve movement and the LP shares minted or burned by
  a deposit or a withdrawal, measured across the whole transaction, with a
  swap against the pool reported as a swap rather than as a wrong amount
- Offers and DEX trades: the `OfferEntry` a manage-offer left on the book, and
  the matches a transaction made — read from a result's claim atoms, including
  trades against a liquidity pool, or reconstructed from the offers a meta
  shows changing
- Consumed fee ceilings and the refundable resource fee breakdown
- Ignore and normalize rules — including globs, index wildcards and
  case-insensitive, tail-anchored patterns — with applied ignores disclosed
  and stale ones reported
- `require` mirrors with `Fatalf` semantics, with 100% parity enforced by an
  AST audit (`pkg/focus/require/audit.go`)
- Golden files in reviewable JSON, with `-focus.update`
- Golden comparisons that normalize volatile sequence and time fields by
  default, with an opt-out, plus a benchmark of the structural comparison path
- Fixtures in JSON, YAML or plain text — chosen by file extension, or with
  `WithFormat` — all three lossless, deterministic and whitespace-normalized,
  with the text form using lens's dotted-path vocabulary
- Golden fixture schema validation, Go-template interpolation, recursive
  directory discovery and table-driven assertion running, plus deterministic
  export to Go fuzz seed corpora
- `focus capture` and `focus verify` against a live network
- Table-driven golden tests: `golden.AssertAll` runs every case in a table,
  reports the whole table on failure — `[2/5] payment_v2: DRIFTED` — and
  repeats each failure with the row's index, name and description attached,
  so a broken row is found without scanning filenames (`pkg/golden/golden.go`)
- Recursive fixture discovery for `verify`, `lint` and `refresh`: a directory
  is searched all the way down, so nested fixtures are no longer skipped; a
  path may be a pattern, with `**` standing for any number of directories;
  and a `.focusignore` file lists the paths to leave out
  (`internal/cli/collect.go`)
- Bulk capture: `focus capture tx --batch` reads the hashes from a file and
  captures them with a worker pool, reporting each transaction as it lands
  and carrying on past one that cannot be captured (`internal/cli/batch.go`)
- Offline fixture generation: `focus encode` turns a base64 or hex payload
  into a reviewable fixture, with the XDR type detected when it is not named
  and no network involved
- Unified CLI configuration: flags, `FOCUS_*` environment variables and a
  config file, with one documented precedence order and validation before any
  request (`internal/cli/flags.go`)
- A documented exit-code contract: 0 success, 1 the command could not run, 2
  `verify` ran and found drift, with sentinel errors for classification
  (`internal/cli/exitcodes.go`). **Migration note:** `focus verify` used to
  exit 1 for both drift and "could not check"; it now exits 2 for drift and
  keeps 1 for a check that could not run, so a script that treated `1` as
  "drift" should test for `2`.
- Snapshot testing for failure-message formatting, so a layout refactor shows
  up as a reviewable `.snap` diff rather than passing silently
  (`pkg/focus/snapshot.go`)
- End-to-end tests that build the real command and run it in temporary
  directories, entirely offline (`cmd/focus/e2e_test.go`)
- Coordinated multi-transaction bundles: one manifest ties a set of related
  captures together, fetched all-or-nothing and re-verified member by member
  (`pkg/capture/bundle.go`)
- Rate-limited live scraping with an on-disk cache under `.focus/cache`, so a
  repetitive capture run is fast for the caller and gentle on the endpoint
  (`pkg/capture/cache.go`)
- Deterministic replay tests generated straight from captured fixtures: same
  fixture in, byte-identical `_test.go` out, red with a field diff when the
  fixture or a decoder moves (`pkg/capture/codegen.go`)
- Transaction preflight simulation capture: `simulateTransaction` responses
  recorded as per-payload fixtures — transaction data with its resource
  footprint, diagnostic events, authorization-tree nodes and host function
  returns — with the host's rejection surfaced as `ErrSimulationFailed`
  (`pkg/capture/simulate.go`)
- Latest-ledger header capture with the assertable fields decoded from the
  XDR: protocol version, base fee, base reserve, total coins and sequence
  (`pkg/capture/ledger_header.go`)
- Deterministic corrupted-envelope mutator over real captures: a fixed seed
  and step count always regenerates the same malformed variant, and the
  report names each corrupted offset (`pkg/capture/mutate.go`)
- Contract WASM archive capture: the ledger entry that names a deployed
  contract's code, fetched by contract id and recorded whole so the bytes
  travel with the transaction fixtures they belong to. The stored code hash
  is checked against the bytes before anything is written, so a truncated
  capture reports `ErrContractCodeHashMismatch` rather than archiving a
  contract that does not match the one the ledger names
  (`pkg/capture/contract_wasm.go`)
- Dependency-light library, enforced by compiled-graph and AST tests
- No assertion panics, enforced by test via a panic-safe harness
  (`SafeExec`/`SafeCall` in `pkg/focus/safe_exec.go`)
- Colourized terminal output highlighting the mismatched field, with the
  structural diff rendering each change side by side as labelled `want`/`got`
  values that strip cleanly to plain text (`pkg/focus/terminal.go`,
  `pkg/focus/message.go`; opt-in via `WithColor("always")`)

Known limits, stated plainly:

- **No contract-spec decoding.** Soroban values render as the XDR wire shape,
  so an event payload shows its structure rather than the contract's field
  names. This is the biggest gap and it is shared with the sibling projects.
- **YAML is a subset.** Write and read by `pkg/golden` itself, since a YAML
  library would be inherited by everyone who imports these test helpers. Block
  mappings, block sequences, single-line scalars and `{}`/`[]` are supported;
  anchors, aliases, tags, block scalars and multi-document streams are rejected
  with a message naming the line. JSON remains the default and the format every
  existing fixture is written in.
- **Ledger entry capture is thin.** `focus capture ledger-entry` records the
  entry data but has no convenience for constructing the `LedgerKey` you need
  to pass it.
- **A meta cannot fully describe a trade.** It records the offers that changed,
  so the amount a matched offer gave up is exact while what it was paid is not
  recorded at all, and an owner reducing their own offer is indistinguishable
  from one that was matched. `AssertTradeExecuted` says so and names the
  transaction result, whose claim atoms carry both halves of every match;
  deriving the missing half from the offer price would mean reimplementing the
  protocol's rounding, which is not a thing a test helper should be doing.
- **Claim predicates are not evaluated.** Whether a claimable balance is
  claimable *now* depends on a clock, and assertions do not read one. The
  predicate is shown in the failure listing instead.
- **Diagnostic events carry no per-event timestamp.** The protocol records no
  wall clock on a diagnostic event, so a stream is placed in time by the ledger
  it was applied in and that ledger's close time, which a test supplies from
  capture provenance, rather than by a timestamp on each event.
- **The library API is not frozen.** Expect breaking changes before v1.0.

## Near term

### Contract-spec-aware assertions

The one thing that would most improve Soroban tests: given a contract's
`ScSpecEntry` definitions, match events and return values by field name
instead of wire structure, and render them the same way in failures.

This belongs in [stellar-xdr-lens](https://github.com/stellar-optics/stellar-xdr-lens)
rather than here, since `lens decode` and `prism events` would benefit
identically — but focus is where the ergonomic payoff is largest, so it is
worth pushing for.

### Horizon as a capture source

`capture.Source` is a small interface and Horizon reaches further back than
RPC's retention window, which matters for fixtures of older transactions.

## Later, and less certain

### More of the simulation response

Budget assertions cover CPU instructions and the ledger I/O bytes in the
transaction's footprint. The rest of a simulate-transaction response — the
footprint entries themselves, the returned auth, the minimum resource fee —
is still only visible through the decoded value.

### A wider YAML subset

The YAML reader is deliberately strict: it reads what the writer emits, and
rejects the rest with a message naming the line rather than guessing. Flow
style (`[a, b]`), block scalars (`|`, `>`) and multi-document streams are the
constructs most likely to appear in a hand-written fixture, and each is a
small, self-contained addition once there is a fixture that needs it —
rejecting is the safe default until then.

### A `focus diff` command

Almost certainly not worth building: `lens diff` already exists and does
exactly this. Mentioned here so the question stops being asked.

## Explicitly not planned

- **A general-purpose assertion library.** focus asserts on XDR. For
  everything else use the standard library or whichever assertion package you
  already have; this one should not grow a `AssertStringEqual`.
- **Reimplementing XDR decoding or diffing.** That is lens's job. Two
  implementations would eventually disagree, and then a failing test would not
  mean what it says.
- **Network access in the assertion path.** Assertions must be deterministic
  and offline. Only `focus capture` and `focus verify` touch the network, and
  that boundary is not moving.
- **A mocking framework for Horizon or RPC.** Out of scope; the interfaces in
  the sibling repositories are already small enough to fake directly.

## Contributing to the roadmap

Adding an assertion or a fixture source is designed to be a one-file change —
see [architecture.md](architecture.md). If you want something here sooner,
open an issue describing the test you were trying to write and where focus got
in the way. A concrete case is far more useful than a feature request.
