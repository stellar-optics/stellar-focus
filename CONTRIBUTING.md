# Contributing

Thanks for considering a contribution. Adding an assertion or a fixture source
is designed to be a one-file change.

## Quick start

```sh
git clone https://github.com/stellar-optics/stellar-focus
cd stellar-focus
go build ./...
go test ./...
```

You need **Go 1.25 or later**, the floor required by
`github.com/stellar/go-stellar-sdk`.

**The test suite makes no network calls.** The only code that touches a
network sits behind `capture.Source`, and the tests use an in-memory fake, so
`go test ./...` works offline and gives the same result every run. Please keep
it that way.

## Running the checks CI runs

```sh
gofmt -l .                  # must print nothing
go vet ./...
go test -race ./...
golangci-lint run ./...
```

Install the linter if you need it:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

For coverage:

```sh
go test -race -covermode=atomic -coverprofile=coverage.out \
  -coverpkg=./pkg/...,./internal/cli ./...
go tool cover -html=coverage.out
```

## The two rules that matter most

### 1. The failure message is the product

A general-purpose assertion library already compares values correctly. The
only reason anyone adopts this one is that its output understands XDR.

So: **a change to a failure message needs a test asserting on the new text.**
Not that the assertion failed — what it *said*. `internal/faketb` records
messages instead of failing, which is what makes this testable:

```go
tb := faketb.New()
focus.AssertEnvelopeEqual(tb, want, got)

msg, err := tb.Message()
if err != nil {
    t.Fatalf("%v", err)
}
if !strings.Contains(msg, "Body.PaymentOp.Amount") {
    t.Errorf("message does not name the changed field:\n%s", msg)
}
```

A failure message that is confusing or unhelpful is a bug, and is worth an
issue on its own.

### 2. The library stays dependency-light

`pkg/focus`, `pkg/focus/require` and `pkg/golden` may depend only on the
standard library, `stellar/go-stellar-sdk` and `stellar-xdr-lens`. No
assertion framework, no diff library, no CLI framework.

Test helpers get imported into other people's projects, and one that drags
Cobra into a production dependency graph is one people resent.
`TestLibraryStaysDependencyLight` enforces this; if you need a new dependency,
say why in the pull request.

## Good first contributions

See [docs/architecture.md](docs/architecture.md) for the details.

### Add an assertion

Put it in the file matching its subject, take `TB` first, return `bool`, call
`t.Helper()`, resolve inputs through `resolve` or `summarize`, wrap anything
touching generated XDR accessors in `guard`, write a test asserting on the
message, and add the mirror in `require/`.

Prefer building on `lens.Explain` over reaching into XDR directly, so the value
a test asserts on is the same value `lens explain` prints.

### Add a fixture source

Implement `capture.Source` and wire it into `internal/cli`. Horizon is the
obvious candidate — it reaches further back than RPC's retention window.

### Improve a failure message

Genuinely valuable. If an assertion failed and you could not tell why, that is
the bug.

## Project conventions

### Go only

The whole project is Go — library, CLI, tests and tooling. No other language,
no JS build step. Shell is fine for trivial CI glue.

Otherwise ordinary idiomatic Go: small interfaces, errors wrapped with `%w`,
`context.Context` through anything doing I/O, no reflection-heavy magic, no DI
framework.

### Do not reimplement XDR decoding or diffing

Everything XDR goes through
[stellar-xdr-lens](https://github.com/stellar-optics/stellar-xdr-lens). If you
need something lens cannot do, the fix usually belongs there — open an issue
in either repository and we will work out which. Two implementations that
disagree would make a failing test mean something other than what it says.

### Assertions must never panic

A panic in your test binary destroys the output of every other test in the
package. Wrap anything that touches generated XDR accessors in `guard`, and add
your assertion to `TestAssertionsNeverPanic`.

### Tests

Table-driven is the house style:

```go
tests := []struct {
	name string
	// inputs and expectations
}{
	{name: "...", /* ... */},
}

for _, tc := range tests {
	t.Run(tc.name, func(t *testing.T) {
		t.Parallel()
		// ...
	})
}
```

**Never add a test that makes a network call.**

### Commit messages

[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary in the imperative mood>

<body explaining why, not what — the diff already says what>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `chore`, `ci`.
Scopes: `focus`, `golden`, `capture`, `require`, `cli`, `docs`.

Examples:

```
feat(focus): add AssertEntryCreated for ledger changes
fix(golden): report the path when a fixture fails to parse
docs(architecture): explain why xdr is the source of truth, not decoded
```

Commit granularly. One logical change per commit.

### Branch naming

```
<type>/<short-description>
```

For example `feat/ledger-change-assertions`, `fix/stale-ignore-message`.

## Pull requests

Before opening one:

- [ ] `go build ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `golangci-lint run ./...` reports no issues
- [ ] `gofmt -l .` prints nothing
- [ ] New behaviour has tests, and no test touches the network
- [ ] A changed failure message has a test asserting on its text
- [ ] Public API changes carry doc comments
- [ ] Commits follow Conventional Commits

Include the linked issue, what changed and why, and **evidence it works** —
actual output, not a claim that you ran it. For a message change, paste the
before and after.

Please open an issue before starting anything large.

## Security

Do not report security issues in a public issue. See [SECURITY.md](SECURITY.md).

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).

## Licence

By contributing, you agree that your contributions are licensed under
Apache-2.0, the same licence as the project.
