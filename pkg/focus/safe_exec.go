// Package focus provides XDR-aware assertions for Go tests.
//
// This file implements the panic-safe execution harness. Generated XDR
// accessors dereference union arms without checking the discriminant, so a
// malformed value can panic deep inside the SDK. A panic escaping into
// someone's test binary destroys the output of every other test in the
// package, so every assertion runs its inspection behind guard/SafeExec,
// which converts a panic into an ordinary test failure.
//
// For complete architectural details and guidelines for authoring panic-safe
// assertion extensions, see docs/architecture/panic_safety.md.
package focus

import (
	"fmt"
	"runtime/debug"
)

// PanicError describes a panic recovered by SafeExec or SafeCall.
//
// Recovered holds the value passed to panic. When it is an error, Unwrap
// returns it so callers can use errors.Is and errors.As against the original
// cause. Stack holds the goroutine stack at recovery time, truncated by
// StackSnippet for failure messages.
type PanicError struct {
	// Assertion names the assertion that was running when the panic occurred.
	Assertion string
	// Recovered is the value passed to panic.
	Recovered any
	// Stack is the raw goroutine stack captured at recovery time.
	Stack []byte
}

// Error implements error.
func (e *PanicError) Error() string {
	return fmt.Sprintf("%s: internal error while inspecting the value: %v", e.Assertion, e.Recovered)
}

// Unwrap returns the recovered value when it is an error, so errors.Is and
// errors.As see through the harness to the original cause.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Recovered.(error); ok {
		return err
	}
	return nil
}

// StackSnippet returns at most the first maxBytes of the captured stack,
// trimmed for inclusion in a failure message.
func (e *PanicError) StackSnippet(maxBytes int) string {
	if maxBytes <= 0 || len(e.Stack) <= maxBytes {
		return string(e.Stack)
	}
	return string(e.Stack[:maxBytes]) + "\n\t... (stack truncated)"
}

// panicAsError converts a recovered panic value into an error carrying the
// assertion name. A nil recovery returns nil so callers can distinguish "no
// panic" from "panic(nil)"; note that since Go 1.21 panic(nil) recovers as a
// non-nil *runtime.PanicNilError, so it is still reported.
func panicAsError(assertion string, recovered any) error {
	if recovered == nil {
		return nil
	}
	return &PanicError{
		Assertion: assertion,
		Recovered: recovered,
		Stack:     debug.Stack(),
	}
}

// reportPanic records a recovered panic as an ordinary test failure.
//
// The message shape matches guard so all panic paths produce one consistent
// text that asks for a bug report with the triggering XDR.
func reportPanic(t TB, err error) {
	t.Helper()
	t.Errorf("%v\nThis is a bug in stellar-focus; please report it with the XDR that triggered it.", err)
}

// SafeExec runs fn and converts any panic into a test failure.
//
// It reports through TB.Errorf and returns false when fn panics or returns
// false, and true only when fn completes without panicking and returns true.
// The failure message matches guard, asking for a bug report with the
// triggering XDR, so every panic path in the library reads the same way.
//
// SafeExec is offline-first and deterministic: it performs no I/O and no
// network calls, and the same panic always produces the same message prefix
// (the stack is captured for debugging but not printed, keeping output
// stable).
func SafeExec(t TB, assertion string, fn func() bool) (ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if err := panicAsError(assertion, r); err != nil {
				reportPanic(t, err)
			} else {
				// Defensive: recover returned nil but was invoked from a
				// panicking goroutine (e.g. panic(nil) on older Go). Report
				// generically rather than silently passing.
				t.Errorf("%s: internal error while inspecting the value: panic(nil)\n"+
					"This is a bug in stellar-focus; please report it with the XDR that triggered it.",
					assertion)
			}
			ok = false
		}
	}()
	return fn()
}

// SafeCall runs fn and converts any panic into a test failure.
//
// Unlike SafeExec, fn returns nothing; SafeCall returns true when fn
// completed without panicking and false when a panic was recovered and
// reported. Use it for inspection helpers that report failures themselves
// and have no boolean result to propagate.
func SafeCall(t TB, assertion string, fn func()) (ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if err := panicAsError(assertion, r); err != nil {
				reportPanic(t, err)
			} else {
				t.Errorf("%s: internal error while inspecting the value: panic(nil)\n"+
					"This is a bug in stellar-focus; please report it with the XDR that triggered it.",
					assertion)
			}
			ok = false
		}
	}()
	fn()
	return true
}
