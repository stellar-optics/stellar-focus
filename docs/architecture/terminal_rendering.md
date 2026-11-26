# Terminal failure rendering

`pkg/focus/terminal.go` and `pkg/focus/message.go` format assertion failures for readable terminal output. The message text stays useful when color is disabled; color only adds ANSI SGR sequences around paths and values.

## Automatic color detection

In `auto` mode, focus delegates terminal detection to the XDR lens formatter. Color is enabled for a terminal writer unless `NO_COLOR` is set or `TERM=dumb`. A pipe or redirected output is not treated as a terminal. The `NO_COLOR` check follows the standard: any set value, including an empty value, disables automatic color.

## Explicit color modes

- `WithColor("auto")` uses terminal detection and the `NO_COLOR` and `TERM=dumb` rules.
- `WithColor("always")` emits color even when output is redirected.
- `WithColor("never")` keeps output plain.

Unknown mode strings disable color.

## Field highlighting

For a changed field, the path is yellow, the expected (`want`) value is green, and the actual (`got`) value is red. Stripping ANSI sequences from colored output yields the same message text as plain mode.

Example, shown with symbolic color labels so it remains legible in Markdown:

```text
AssertField: [yellow]Account.Flags[reset] is [red]0[reset], want [green]1[reset]
```

The corresponding SGR codes are `\x1b[33m` (yellow path), `\x1b[31m` (red actual value), `\x1b[32m` (green expected value), and `\x1b[0m` (reset). The terminal helpers also expose bold (`\x1b[1m`), cyan (`\x1b[36m`), and dim (`\x1b[90m`) styles.

Complex XDR values use the same distinction in side-by-side diffs: removed or actual values are red, and added or expected values are green. Only the changed subfields are emphasized, leaving surrounding context plain.

## Screenshot-style example

```text
want                         got
Flags: AUTH_REVOCABLE         Flags: AUTH_REQUIRED
        [green]REVOCABLE[reset]        [red]REQUIRED[reset]
```

The code examples above stand in for terminal screenshots: they show the exact text and color roles without depending on a terminal theme or font.
