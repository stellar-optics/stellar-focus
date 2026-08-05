# Roadmap

An honest account of where this is going. Dates are intentions, not commitments.

## Where things stand (v0.1)

Working and tested:

- Structural equality assertions for envelopes, results and metas, with
  failure output that names the changed field path
- Field assertions: source, fee, sequence, memo, operation counts and types,
  signature count, fee-bump
- Result assertions using Horizon's short codes, with lens's plain-English
  explanation and hint
- Soroban: event matching, event counts, return values, contract traps
- Ignore and normalize rules, with applied ignores disclosed and stale ones
  reported
- `require` mirrors with `Fatalf` semantics
- Golden files in reviewable JSON, with `-focus.update`
- `focus capture` and `focus verify` against a live network
- Dependency-light library, enforced by test
- No assertion panics, enforced by test

Known limits, stated plainly:

- **No contract-spec decoding.** Soroban values render as the XDR wire shape,
  so an event payload shows its structure rather than the contract's field
  names. This is the biggest gap and it is shared with the sibling projects.
- **Ledger entry capture is thin.** `focus capture ledger-entry` records the
  entry data but has no convenience for constructing the `LedgerKey` you need
  to pass it.
- **No ledger-change assertions.** You can assert on results and events, but
  not yet on the ledger entries a transaction created, modified or removed.
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

### Ledger-change assertions

`AssertEntryCreated`, `AssertEntryModified`, `AssertBalanceChanged`. Transaction
meta already carries the ledger entry changes; this is mostly a matter of
designing assertions that read well, which is the same work as everything else
here.

### A short-code index

`AssertResultCode(t, result, "paymnt_underfunded")` currently fails with a
confusing message rather than "no such result code", because focus has no way
to enumerate valid short codes — lens does not export that mapping.

Adding `lens.ReasonByCode(short string) (Reason, bool)` upstream would let
focus both validate the expectation and explain it, which turns a typo in a
test from a puzzling failure into an obvious one. Filed as the main upstream
ask.

### Horizon as a capture source

`capture.Source` is a small interface and Horizon reaches further back than
RPC's retention window, which matters for fixtures of older transactions.

### Fuzz helpers

A `focus.Fuzz` seed corpus of real captured XDR would make it easy for other
projects to fuzz their own decoders. The corpus already exists in `testdata`;
what is missing is a documented way to consume it.

## Later, and less certain

### Assertion on transaction *simulation*

Soroban RPC can simulate a transaction before submission. Asserting on the
simulated result — footprint, resource usage, return value — would let tests
catch resource regressions without submitting anything. Needs care to stay
offline-testable.

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
