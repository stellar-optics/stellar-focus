# Terminal failure rendering

"The failure message is the product" is this library's founding principle
(see [the organising principle](../architecture.md#the-organising-principle)).
This page documents the two files that put it into practice:
`pkg/focus/terminal.go`, which owns colour, and `pkg/focus/message.go`, which
owns layout.

## Colour is off by default

A focus assertion reports through `t.Errorf`, which a test framework
captures rather than writing to a terminal. Colourising captured output by
default would mean every CI log and `go test` run fills with raw ANSI
escapes, so `WithColor`'s default is `"never"`:

```go
focus.AssertEnvelopeEqual(t, want, got, focus.WithColor("always"))
```

`WithColor` accepts three modes, matching the sibling `lens` tool so a focus
failure and `lens diff` agree on when colour is appropriate:

- `"never"` (the default) — no escape sequences, ever.
- `"always"` — colour unconditionally, for a human re-running a failing test
  directly in a terminal.
- `"auto"` — in `config.palette()` this currently behaves exactly like
  `"never"`: a value reported through `TB.Errorf` has no writer to detect a
  TTY on, so there is nothing for "auto" to detect. Auto-detection does exist
  in this package, but it answers a different question — see
  [`SupportsColorWriter`](#supportscolorwriter-and-writer-detection) below,
  which is for code that writes to an `io.Writer` directly rather than
  through `TB`.

### `NO_COLOR` and `TERM=dumb`

Two helpers in `terminal.go` apply the convention outside the `TB.Errorf`
path, for callers that render to a real writer:

- `ColorEnabled(mode string) bool` resolves a `WithColor`-style mode without
  a writer to inspect. `"always"` is always on; `"auto"` is on unless
  `NO_COLOR` is set in the environment (any value — the
  [NO_COLOR](https://no-color.org) convention) or `TERM=dumb`; every other
  mode, including `"never"`, is off.
- `SupportsColorWriter(w io.Writer) bool` additionally requires `w` to be a
  character device, so output redirected to a file or a pipe never gets
  escape codes even when the environment would otherwise allow them. It
  delegates to `lensformat.ShouldColor` so focus and `lens` detect a
  terminal the same way.

`PaletteForWriter(w io.Writer, mode string) lensformat.Palette` combines the
two: it resolves the palette for `w` under `mode`, honouring `NO_COLOR` and
`TERM=dumb` for `"auto"` and requiring a character device, exactly as
`SupportsColorWriter` does.

## The colour palette

`config.palette()` resolves a `*config`'s `colorMode` to a
`lensformat.Palette`: `"always"` returns `lensformat.ColorPalette`, and
every other mode — including `"auto"`, for the reason above — returns
`lensformat.NoColor`, whose fields are all the empty string, making every
colour a no-op.

`lensformat.ColorPalette` is the 16-colour scheme shared with `lens diff`:

| Field    | Colour  | Used for                                   |
| -------- | ------- | ------------------------------------------- |
| `Key`    | cyan    | field and map keys                          |
| `Type`   | magenta | XDR type names                              |
| `Str`    | green   | string values                               |
| `Num`    | yellow  | numeric values                              |
| `Change` | yellow  | a changed field's path                      |
| `Add`    | green   | the "got" side of a difference               |
| `Remove` | red     | the "want" side of a difference              |
| `Dim`    | grey    | labels, notes and elision counts            |

The constants in `terminal.go` (`ANSIRed`, `ANSIGreen`, `ANSIYellow`, …) back
this palette and are also used standalone by a few package-level helpers —
`HighlightPath` (yellow, for a field path) and `FormatFieldMismatch` (green
for "want", red for "got") — that render a single scalar mismatch without
building a full `lensformat.Palette`.

## Field highlighting: want is red, got is green

`renderChange` in `message.go` colours each side of a structural difference
using the *removal* colour for "want" and the *addition* colour for "got":

```go
writeChangeSide(b, p, "want", p.Remove, ch.Before, ch.BeforeNote)
writeChangeSide(b, p, "got", p.Add, ch.After, ch.AfterNote)
```

That reads as a diff rather than as a verdict: "want" is the value being
removed from consideration, the old side of the comparison, so it takes the
removal colour (red in `ColorPalette`); "got" is what the transaction
actually produced, the value the diff is adding to explain the failure, so
it takes the addition colour (green). A field that was added outright (no
prior value to compare against) shows only the "got" line in the addition
colour; a field that was removed outright shows only "want" in the removal
colour. This also means a focus failure's colouring matches `lens diff`
line for line, since both read `lensformat.Palette` the same way.

The field path itself — the dotted or indexed location such as
`V1.Tx.Operations[0].Body.PaymentOp.Amount` — is highlighted separately, in
the *change* colour (yellow), by `HighlightPath`. A path gets its own colour
rather than reusing want/got's red or green because it is naming a
location, not a value: using the same colour as one of the two sides would
suggest the path itself was a part of the comparison that changed.

Everything in this section composes down to plain text when colour is off:
`StripANSI` removes every ANSI escape sequence from a rendered string, and
`ContainsANSI` reports whether any are present, so a test can assert on the
same message text whether or not colour was requested.

## `HighlightMismatchField`

For a caller that already has a rendered failure string and wants to
emphasise one field within it after the fact — rather than composing colour
in while building the message — `HighlightMismatchField(msg, field string)`
wraps every occurrence of `field` in `msg` with `HighlightPath`'s yellow. An
empty `field`, or one that does not appear in `msg`, returns `msg`
unchanged.

## `SupportsColorWriter` and writer detection

Most of this library never writes to a terminal directly: assertions report
through `TB`, which a test framework owns. The writer-aware helpers exist for
the CLI and for callers rendering a `lens`-style diff to their own
`io.Writer`, where "is this actually a terminal" is answerable and worth
answering — redirecting `focus` output to a log file should not fill it with
escape codes just because the shell's `TERM` says otherwise.
