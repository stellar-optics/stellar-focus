# Roadmap

An honest account of where this is going. Dates are intentions, not commitments.

## Where things stand (v0.1)

Working and tested:

- Structural equality assertions for envelopes, results and metas, with
  failure output that names the changed field path
- Field assertions: source, fee, sequence, memo, operation counts and types,
  signature count, fee-bump
- Time- and ledger-bound assertions, comparing the window a precondition sets
  against the window a test expects
- Memo assertions by value: text, ID, 32-byte hash and return memos
- Operation-sequence assertions, matching the exact ordered operation types
- Signer assertions: which keys attached a signature — including pre-auth-tx,
  hash-x and signed-payload signers, and both signature sets of a fee-bump
  envelope — plus M-of-N signature thresholds
- Result assertions using Horizon's short codes, with lens's plain-English
  explanation and hint; operation result codes can be checked by short name,
  and an unknown code or an XDR constant name is called out as such
- Soroban: event matching, event counts, return values, contract traps
- Soroban diagnostic streams: the order of a contract call's diagnostic events
  — call frames, logs, and the events of calls that failed — and the ledger the
  stream was emitted in, with that ledger's close time
- Soroban simulation resource budgets, from either the transaction's
  `SorobanTransactionData` or a simulate-transaction response
- Contract deployment state: the storage a `__constructor` writes — instance,
  persistent and temporary — and the owner it binds, read from the deployment
  transaction's meta with no network call
- Contract invocation authorizations: who authorized which call — the invoker
  behind every credential form (source account, address, address v2,
  delegated) and the invocation tree covered, read from the envelope with no
  network call
- Consumed fee ceilings and the refundable resource fee breakdown
- Ignore and normalize rules — including globs, index wildcards and
  case-insensitive, tail-anchored patterns — with applied ignores disclosed
  and stale ones reported
- `require` mirrors with `Fatalf` semantics, with 100% parity enforced by an
  AST audit (`pkg/focus/require/audit.go`)
- Golden files in reviewable JSON, with `-focus.update`
- Fixtures in JSON, YAML or plain text — chosen by file extension, or with
  `WithFormat` — all three lossless, deterministic and whitespace-normalized,
  with the text form using lens's dotted-path vocabulary
- `focus capture` and `focus verify` against a live network
- Dependency-light library, enforced by compiled-graph and AST tests
- No assertion panics, enforced by test via a panic-safe harness
  (`SafeExec`/`SafeCall` in `pkg/focus/safe_exec.go`)
- Colourized terminal output highlighting the mismatched field
  (`pkg/focus/terminal.go`, opt-in via `WithColor("always")`)

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
- **Diagnostic events carry no per-event timestamp.** The protocol records no
  wall clock on a diagnostic event, so a stream is placed in time by the ledger
  it was applied in and that ledger's close time, which a test supplies from
  capture provenance, rather than by a timestamp on each event.
- **Authorization signatures are not verified.** `AssertContractAuthValid`
  checks the structure and identity of an authorization — who authorized
  which call — from the envelope the test already holds. What a signature
  signs derives from the network passphrase, which an offline assertion is
  not given, so the nonce and expiration ledger are shown for a test to pin
  rather than checked here.
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

### Fuzz helpers

A `focus.Fuzz` seed corpus of real captured XDR would make it easy for other
projects to fuzz their own decoders. The corpus already exists in `testdata`;
what is missing is a documented way to consume it.

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
