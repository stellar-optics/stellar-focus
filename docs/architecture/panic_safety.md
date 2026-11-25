# Panic Safety Architecture in `stellar-focus`

A core architectural invariant of `stellar-focus` is **zero unhandled panics**: no input passed to an assertion—whether it is `nil`, corrupted base64, truncated XDR, or an unexpected union discriminant—should crash the test runner.

When an assertion panics, the Go testing runtime aborts the entire test process, preventing subsequent tests from running and concealing actionable diagnostic reports. In `stellar-focus`, panics are intercepted, wrapped into structured test failures, and presented with clear actionable bug-reporting guidance.

---

## 1. Why XDR Inspection Panics

In Go code generated from Stellar XDR specifications, union structs and pointer fields frequently omit nil-checks or discriminant validation. For example, accessing:

```go
op.Body.PaymentOp.Amount
```

will panic with a nil pointer dereference if `op.Body.Type` is not `OperationTypePayment`. If an assertion library does not protect every accessor call, invalid or unexpected XDR causes a hard crash deep inside the SDK.

---

## 2. The `SafeExec` and `SafeCall` Harness

The panic-safety harness in `pkg/focus/safe_exec.go` converts any recovered panic into an ordinary `testing.TB` error failure.

### `focus.SafeExec`

`SafeExec` executes an assertion body returning a boolean. If a panic occurs, it intercepts the panic via `defer recover()`, reports the failure through `TB.Errorf`, and safely returns `false`:

```go
func AssertCustomProperty(t TB, envelope Value) bool {
    t.Helper()
    const assertion = "AssertCustomProperty"

    return SafeExec(t, assertion, func() bool {
        // Inspection logic that might touch delicate XDR fields
        return inspectEnvelope(envelope)
    })
}
```

### `focus.SafeCall`

`SafeCall` is designed for helper functions that report test failures directly to `t` and do not return a boolean status:

```go
func InspectDiagnostics(t TB, meta Value) {
    t.Helper()
    const assertion = "InspectDiagnostics"

    SafeCall(t, assertion, func() {
        printDiagnostics(t, meta)
    })
}
```

---

## 3. `PanicError` Structure

When a panic occurs, `SafeExec` and `SafeCall` wrap the recovered value in a `*PanicError`:

```go
type PanicError struct {
    Assertion string // Name of the running assertion
    Recovered any    // Value passed to panic(...)
    Stack     []byte // Goroutine stack captured at recovery time
}
```

### Unwrapping and Error Chains
`PanicError` implements `Unwrap() error`. If the panicking code called `panic(err)`, `errors.Is(err, target)` and `errors.As(err, &target)` work seamlessly across the recovery boundary.

### Stack Snippets
To keep test output clean and deterministic, full goroutine stacks are truncated using `StackSnippet(maxBytes)`:

```go
snippet := panicErr.StackSnippet(1024)
```

### Failure Output
Every panic recovered by the harness produces a standardized message asking the user for a reproduction payload:

```
AssertCustomProperty: internal error while inspecting the value: runtime error: invalid memory address or nil pointer dereference
This is a bug in stellar-focus; please report it with the XDR that triggered it.
```

---

## 4. Context Timeout and Runaway Loop Protection

Assertions that evaluate recursive structures, complex contract authorization trees, or diagnostic event timelines must guard against circular references or unbounded iteration.

`stellar-focus` enforces timeout protection and bounded traversal:
1. **Bounded recursion:** Depth counters prevent stack overflows on circular references.
2. **Context deadlines:** Operations that decode or verify deeply nested XDR trees operate with bounded context timeouts. If a deadline expires, the matcher aborts with a descriptive timeout error rather than hanging CI indefinitely.

---

## 5. Authoring Panic-Safe Assertion Extensions

When contributing new assertion helpers to `stellar-focus`, follow these rules:

1. **Always wrap accessor calls in `guard` or `SafeExec`:** Never invoke generated XDR accessors on an unchecked discriminant without a recovery frame.
2. **Mark test helpers:** Always invoke `t.Helper()` at the start of your assertion function so error lines point to the user's test code.
3. **Validate input types gracefully:** Use `coerce` / `envelopeFrom` / `operationFrom` to extract typed values from generic `Value` arguments.
4. **Test against hostile inputs:** Every new assertion must include tests in `*_test.go` verifying behavior with `nil`, empty strings, and malformed XDR.
