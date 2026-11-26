# Author Guide: Writing Fake Testing Harnesses with faketb and fakesource

This guide explains how `stellar-focus` tests its own assertion failure messages using zero-dependency fakes, and how downstream developers can adopt the same patterns.

## Architectural Motivation

`stellar-focus` carries no assertion framework, no diff library, and no CLI framework. Test helpers are imported into other people's projects, so the library must remain dependency-light. This means we cannot rely on `testify`, `golang/test`, or any other external testing package within the assertion logic itself.

To test assertion failure messages without importing a testing framework into `pkg/focus`, the library uses two in-memory fakes:

1. **`internal/faketb.TB`** — a recording implementation of `focus.TB` that captures every `Errorf`/`Fatalf` call instead of aborting the test.
2. **`internal/fakesource.Source`** — an in-memory `capture.Source` that simulates RPC/Horizon responses without network calls.

These fakes allow the assertion code to remain pure Go (no `t.Error`, no `t.Fatalf`) while still being fully testable.

---

## The faketb.TB Harness

`faketb` records assertion output so tests can assert on the text actually produced.

### Structure

```go
type TB struct {
    Errors    []string   // every Errorf message, formatted
    Fatals    []string   // every Fatalf message, formatted
    Helpers   int        // Helper() call count
    TestName  string     // returned by Name()
    fatalCalled bool    // set when Fatalf is called
    mu        sync.Mutex
}
```

### Key Methods

| Method | Purpose |
|---|---|
| `New()` | Returns a fresh `*TB` with `TestName: "TestFake"` |
| `Helper()` | increments `Helpers` count; keeps file:line pointing at the caller |
| `Errorf(format, args...)` | Records a formatted error message; test continues |
| `Fatalf(format, args...)` | Records a formatted fatal message; `fatalCalled = true`; test continues |
| `Name() string` | Returns the test name set at construction |
| `Failed() bool` | Returns `true` if any errors or fatals were recorded |
| `Output() string` | Joins all errors and fatals with newlines; used as the test's `t.Log()` output |
| `Message() (string, error)` | Returns the single recorded message, or fails if count ≠ 1 |
| `ErrorCount() int` | Number of `Errorf` calls |
| `FatalCount() int` | Number of `Fatalf` calls |
| `TotalCount() int` | `Errors + Fatals` |
| `LastError() string` | Most recent `Errorf` message (or `""`) |
| `LastFatal() string` | Most recent `Fatalf` message (or `""`) |
| `ErrorsContaining(substr string) []string` | All `Errorf` messages containing `substr` |
| `FatalsContaining(substr string) []string` | All `Fatalf` messages containing `substr` |
| `Reset()` | Clears all recorded messages and state for reuse |
| `AssertErrorCount(expected int) error` | Fails if error count doesn't match |
| `AssertFatalCount(expected int) error` | Fails if fatal count doesn't match |
| `AssertTotalCount(expected int) error` | Fails if total count doesn't match |
| `AssertErrorContaining(substr string) error` | Fails if no error message contains `substr` |
| `AssertFatalContaining(substr string) error` | Fails if no fatal message contains `substr` |
| `AssertNoErrors() error` | Fails if any `Errorf` messages were recorded |
| `AssertNoFatals() error` | Fails if any `Fatalf` messages were recorded |
| `AssertNoFailures() error` | Fails if any errors or fatals were recorded |

### Testing Assertion Messages with faketb

The primary pattern for testing failure messages:

```go
package mytest

import (
    "testing"
    "strings"

    "github.com/stellar-optics/stellar-focus/pkg/focus"
    "github.com/stellar-optics/stellar-focus/pkg/faketb"
)

func TestAssertEnvelopeEqualMessage(t *testing.T) {
    tb := faketb.New()
    focus.AssertEnvelopeEqual(tb, want, got)

    msg, err := tb.Message()
    if err != nil {
        t.Fatalf("%v", err)
    }
    if !strings.Contains(msg, "Body.PaymentOp.Amount") {
        t.Errorf("message does not name the changed field:\n%s", msg)
    }
}
```

**Why this pattern?** The assertion code calls `t.Errorf`/`t.Fatalf` through the `focus.TB` interface. The fake records the messages instead of aborting. The test then inspects what was recorded, checking for specific field paths, error codes, or user-facing text.

### faketb in focus's Own Tests

`stellar-focus` tests its own assertion messages using this pattern. Example from `internal/faketb/mock_test.go`:

```go
// Example pattern used in focus's test suite
tb := faketb.New()
focus.AssertEnvelopeEqual(tb, want, got)
msg, _ := tb.Message()
// assert on msg content
```

The `faketb` package has 100% test coverage, and every new assertion should have a corresponding test asserting on its failure message text.

---

## The fakesource.Source

`fakesource` simulates a `capture.Source` in memory, so `focus capture` and `focus verify` can be tested without network access.

### Structure

```go
type Source struct {
    Transactions map[string][]capture.Record   // hash -> records
    Entries      map[string][]capture.Record   // ledger key -> records
    Simulations  map[string]*protocol.SimulateTransactionResponse
    Err          error                           // returned by every call
    SimulationErr error                           // returned by every simulation
    Passphrase   string
    PassphraseErr error
    network, endpoint string
    Calls             int         // fetch count, for assertions
    mu                sync.Mutex
}
```

### Construction

```go
func New(network, endpoint string) *Source
```

Creates a fake pointed at a nominal network (e.g., "testnet", "mainnet").

### Key Methods (implementing `capture.Source` and `simulate.Source`)

| Method | Signature | Purpose |
|---|---|---|
| `Name() string` | `()` | Returns `"fake"` |
| `Network() string` | `()` | Returns the configured network |
| `Endpoint() string` | `()` | Returns the configured endpoint |
| `NetworkPassphrase(ctx) (string, error)` | | Returns the passphrase (or `PassphraseErr`) |
| `Transaction(ctx, hash) ([]capture.Record, error)` | | Returns canned transaction records |
| `LedgerEntry(ctx, key) ([]capture.Record, error)` | | Returns canned ledger entry records |
| `SimulateTransaction(ctx, req protocol.SimulateTransactionRequest) (*protocol.SimulateTransactionResponse, error)` | | Returns canned simulation response |

### Testing Capture/Verify with fakesource

The fake is used to test the capture and verify commands offline. Example pattern:

```go
package mytest

import (
    "testing"
    "github.com/stellar-optics/stellar-focus/pkg/capture"
    "github.com/stellar-optics/stellar-focus/pkg/fakesource"
)

func TestCaptureWithFakeSource(t *testing.T) {
    src := fakesource.New("testnet", "https://horizon.testnet.stellar.org")
    
    // Can a transaction be fetched?
    records, err := src.Transaction(context.Background(), "some-hash")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    // Verify the source reports the right network
    if src.Network() != "testnet" {
        t.Errorf("wrong network: %s", src.Network())
    }
    
    // Verify calls count
    if src.Calls != 1 {
        t.Errorf("expected 1 call, got %d", src.Calls)
    }
}
```

### Downstream Adoption

Developers building custom assertion libraries can adopt the same pattern:

1. Create a `TB` fake that records `Errorf`/`Fatalf` calls (see `internal/faketb`).
2. Create a `Source` fake that implements the required interfaces (see `internal/fakesource`).
3. Wire the fake into your test setup instead of a real `*testing.T` or real endpoint.
4. Assert on recorded messages rather than test outcomes.

This keeps your library dependency-light while still providing rich failure messages.

---

## Usage in Downstream Testing

When importing `stellar-focus` into a downstream project, the faketb and fakesource patterns let you:

1. **Test assertion messages without network**: Use `faketb.New()` to capture what your assertions report, and verify the text contains expected field paths, codes, or hints.

2. **Test capture logic offline**: Use `fakesource.New(network, endpoint)` to simulate RPC responses, testing that your capture code handles canned data correctly.

3. **Keep dependency graph clean**: Since the fakes live in `stellar-focus`'s own repository, you don't need to bring in a mocking framework. Just import `github.com/stellar-optics/stellar-focus/pkg/faketb` and/or `github.com/stellar-optics/stellar-focus/pkg/fakesource`.

4. **Example pattern** (from CONTRIBUTING.md):

```go
tb := faketb.New()
focus.AssertEnvelopeEqual(tb, want, got)
msg, err := tb.Message()
if err != nil {
    t.Fatalf("%v", err)
}
if !strings.Contains(msg, "operations[0].body.paymentOp.amount") {
    t.Errorf("unexpected message: %s", msg)
}
```

This is the pattern used throughout `stellar-focus`'s own test suite to ensure every assertion produces clear, helpful failure output.

## Contributing New Assertions

When adding a new assertion to `pkg/focus`:

1. **Write the assertion** following the library's patterns (see `Add an assertion` in CONTRIBUTING.md).
2. **Test the failure message** using `faketb`:
   - Call the assertion with `faketb.New()`.
   - Call `tb.Message()` and assert on the returned text.
   - Ensure the message names the changed field path.
   - Ensure the message includes both stroops and XLM where relevant.
   - Ensure ignore rules are disclosed in the failure output.
3. **Add a mirror in `pkg/focus/require`** if the assertion has one.
4. **Run `go test -race ./...`** to confirm no panics or race conditions.

The two rules that matter most (from CONTRIBUTING.md) are:
- **The failure message is the product** — a confusing or unhelpful message is a bug.
- **The library stays dependency-light** — no new dependencies beyond the standard library, `go-stellar-sdk`, and `stellar-xdr-lens`.