# Ignore Rules Reference

`focus` compares two values field by field, and some fields legitimately vary
between the value a test built and the value that came off the network — sequence
numbers, signatures, time bounds, ledger-close metadata. The ignore rules say
which differences are not failures, and this guide is the reference for the
syntax, the presets, and how the rules are reported.

---

## Built-in presets

Each preset is a shorthand for the patterns it stands for:

| Option | Ignores |
|---|---|
| `focus.IgnoreVolatile()` | sequence numbers, signatures and time bounds — the common combination for comparing a rebuilt transaction |
| `focus.IgnoreSequence()` | `**.SeqNum` |
| `focus.IgnoreSignatures()` | `**.Signatures` |
| `focus.IgnoreTimeBounds()` | `**.TimeBounds`, `**.MinTime`, `**.MaxTime` |
| `focus.IgnoreLedgerHeader()` | volatile ledger-close metadata in ledger headers |
| `focus.IgnoreFee()` | `**.Fee`, `**.FeeCharged` |
| `focus.IgnoreSourceAccount()` | transaction and operation source accounts |
| `focus.IgnoreSorobanAuth()` | Soroban authorization credential trees, while still comparing the authorized invocation |

`IgnoreVolatile` composes the sequence, signatures and time bounds presets. It
is what most tests comparing a rebuilt transaction want.

Soroban authorization entries carry a nonce and a signature that are fresh on
every run; `IgnoreSorobanAuth` suppresses the credentials tree while still
comparing the invocation itself, and discloses the ignored credential paths in
the failure output like any other ignore rule.

## Custom patterns

`focus.IgnorePaths(pattern ...string)` takes the patterns; every preset is just
a shorthand over it.

Patterns are matched against the dotted paths lens reports for a change, such as
`V1.Tx.Operations[0].Body.PaymentOp.Amount` or `V1.Tx.Signatures[0].Signature`.

| Pattern | Meaning |
|---|---|
| `V1.Tx.SeqNum` | an exact path |
| `**.SeqNum` | that field at any depth |
| `V1.Tx.*` | exactly one path segment |
| `Operations[*]` | any index at this position |
| `operations[*].sourceaccount` | tail-anchored: no `V1.Tx.` prefix needed |
| `**.Auth[*].Credentials.**` | any depth, any index, nested |

Matching rules:

- `*` matches exactly one path segment; `**` matches any number of segments at
  any depth, and may appear anywhere rather than only at the end.
- Within a segment, `*`, `?` and character classes work like globs: `*` any run
  of characters, `?` one character, `[abc]` a set, `[!abc]` (or `[^abc]`) its
  complement, `-` a range. A `[` with no closing `]` is treated as a literal.
- An index may be wildcarded, as in `Operations[*]`; a bare name also covers its
  indexed segments, so `Signatures` covers `Signatures[0]`.
- Matching is case-insensitive, so `operations[*].sourceaccount` may be written
  the way the field reads out loud.
- A pattern is anchored to the whole path or any tail of it, so it does not need
  a `V1.Tx.` prefix or a leading `**` to reach the part the test author was
  thinking about.

### Pass/fail examples

| Pattern | Matches | Does not match |
|---|---|---|
| `**.SeqNum` | `V1.Tx.SeqNum`, or a `SeqNum` leaf at any depth | a field actually named differently, e.g. `V1.Tx.SequenceNumber` |
| `operations[*].sourceaccount` | `V1.Tx.Operations[0].SourceAccount` (tail-anchored) | a source account at a depth the pattern does not reach |
| `Signatures` | `V1.Tx.Signatures` and `V1.Tx.Signatures[0]` | unrelated signature-shaped fields elsewhere, unless `**.Signatures` is used |
| `V1.Tx.*.Amount` | one-segment child under `V1.Tx` — not the nested payment amount | `V1.Tx.Operations[0].Body.PaymentOp.Amount` |

## Ignore vs. normalize vs. sort

Three options relax a comparison; they answer different questions.

- **Ignore** (`focus.IgnorePaths`, presets): drop differences at matching paths.
  The difference is *not a failure*. Ignored paths are always named in the
  failure output — `(ignored: **.Signatures, ...)`.
- **Normalize** (`focus.NormalizePath`): rewrite values at matching paths before
  comparison, for cases an ignore rule is too blunt for — round a timestamp, map
  an address to a placeholder. A normalizer that makes both sides equal drops
  the change silently; one that does not keeps it as a difference.
- **Sort** (`focus.SortPath`, `focus.SortSigners`, `focus.SortFootprint`): treat
  an array as an unordered set before comparing, so `[sigA, sigB]` and
  `[sigB, sigA]` compare equal. Signature order on a multi-signed transaction is
  not part of what the transaction means.

## Reporting applied and unused rules

The behaviour is deliberate and worth knowing before writing a suite:

- **Applied rules are named in the failure output.** A failing diff ends with
  `(ignored: **.SeqNum, V1.Tx.Fee)`.
- **A rule that matches nothing is a failure, not a silent no-op.** When values
  match but a pattern matched nothing, the assertion reports the stale patterns
  and fails — a stale pattern is a test quietly weakening.
- **Sort rules are checked the same way**, including the paths they compared as
  unordered sets.

```go
focus.AssertEnvelopeEqual(t, want, got, focus.IgnoreVolatile())
// or, for one field:
focus.AssertEnvelopeEqual(t, want, got, focus.IgnorePaths("**.Signatures", "operations[*].sourceaccount"))
```

Patterns are validated when the option is constructed; a malformed pattern
panics immediately rather than silently matching nothing.

## Next steps

- The recipes that decide *which* ignore to reach for:
  [Assertions Cookbook](assertions_cookbook.md).
- How field ignoring differs from address normalization:
  [Capture, Anonymize and Replay](capture_and_anonymization.md).
