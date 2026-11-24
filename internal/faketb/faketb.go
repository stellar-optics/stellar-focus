// Package faketb provides a recording implementation of focus.TB.
package faketb

import (
	"fmt"
	"strings"
	"sync"
)

// TB records what an assertion reported.
type TB struct {
	// Errors holds every message passed to Errorf, formatted.
	Errors []string
	// Fatals holds every message passed to Fatalf, formatted.
	Fatals []string
	// Helpers counts Helper calls, so tests can check assertions mark
	// themselves as helpers and keep file:line pointing at the caller.
	Helpers int
	// TestName is returned by Name.
	TestName string
	// mu protects concurrent access to the recorded messages.
	mu sync.Mutex
	// fatalCalled is set when Fatalf is called, to simulate test halting.
	fatalCalled bool
}

// New returns a recording TB.
func New() *TB { return &TB{TestName: "TestFake"} }

// Helper implements focus.TB.
func (t *TB) Helper() { t.Helpers++ }

// Errorf implements focus.TB.
func (t *TB) Errorf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Errors = append(t.Errors, fmt.Sprintf(format, args...))
}

// Fatalf implements focus.TB.
//
// It records rather than aborting. A real Fatalf ends the goroutine, which a
// fake cannot reproduce without complicating every test that uses it; tests
// here assert on what was reported instead.
func (t *TB) Fatalf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Fatals = append(t.Fatals, fmt.Sprintf(format, args...))
	t.fatalCalled = true
}

// Name implements focus.TB.
func (t *TB) Name() string { return t.TestName }

// Failed reports whether anything was recorded.
func (t *TB) Failed() bool { return len(t.Errors) > 0 || len(t.Fatals) > 0 }

// Output joins everything recorded, for substring assertions.
func (t *TB) Output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(append(append([]string{}, t.Errors...), t.Fatals...), "\n")
}

// Message returns the single recorded message, or fails the description when
// the count is not one. Most tests expect exactly one failure, and asserting
// that keeps a test from passing on the wrong message.
func (t *TB) Message() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	all := append(append([]string{}, t.Errors...), t.Fatals...)
	switch len(all) {
	case 1:
		return all[0], nil
	case 0:
		return "", fmt.Errorf("no failure was reported")
	default:
		return "", fmt.Errorf("expected exactly one failure, got %d:\n%s",
			len(all), strings.Join(all, "\n---\n"))
	}
}

// ErrorCount returns the number of Errorf calls recorded.
func (t *TB) ErrorCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.Errors)
}

// FatalCount returns the number of Fatalf calls recorded.
func (t *TB) FatalCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.Fatals)
}

// TotalCount returns the total number of failures recorded (Errors + Fatals).
func (t *TB) TotalCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.Errors) + len(t.Fatals)
}

// FatalfCalled reports whether Fatalf was called (simulating test halt).
func (t *TB) FatalfCalled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fatalCalled
}

// LastError returns the most recent Errorf message, or empty string if none.
func (t *TB) LastError() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.Errors) == 0 {
		return ""
	}
	return t.Errors[len(t.Errors)-1]
}

// LastFatal returns the most recent Fatalf message, or empty string if none.
func (t *TB) LastFatal() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.Fatals) == 0 {
		return ""
	}
	return t.Fatals[len(t.Fatals)-1]
}

// ErrorsContaining returns all Errorf messages containing the substring.
func (t *TB) ErrorsContaining(substr string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var result []string
	for _, err := range t.Errors {
		if strings.Contains(err, substr) {
			result = append(result, err)
		}
	}
	return result
}

// FatalsContaining returns all Fatalf messages containing the substring.
func (t *TB) FatalsContaining(substr string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var result []string
	for _, fatal := range t.Fatals {
		if strings.Contains(fatal, substr) {
			result = append(result, fatal)
		}
	}
	return result
}

// Reset clears all recorded messages and state, allowing reuse of the TB.
func (t *TB) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Errors = nil
	t.Fatals = nil
	t.Helpers = 0
	t.fatalCalled = false
}

// AssertErrorCount fails the test if the error count doesn't match expected.
func (t *TB) AssertErrorCount(expected int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	actual := len(t.Errors)
	if actual != expected {
		return fmt.Errorf("expected %d errors, got %d", expected, actual)
	}
	return nil
}

// AssertFatalCount fails the test if the fatal count doesn't match expected.
func (t *TB) AssertFatalCount(expected int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	actual := len(t.Fatals)
	if actual != expected {
		return fmt.Errorf("expected %d fatals, got %d", expected, actual)
	}
	return nil
}

// AssertTotalCount fails the test if the total count doesn't match expected.
func (t *TB) AssertTotalCount(expected int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	actual := len(t.Errors) + len(t.Fatals)
	if actual != expected {
		return fmt.Errorf("expected %d total failures, got %d", expected, actual)
	}
	return nil
}

// AssertErrorContaining fails if no Errorf message contains the substring.
func (t *TB) AssertErrorContaining(substr string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, err := range t.Errors {
		if strings.Contains(err, substr) {
			return nil
		}
	}
	return fmt.Errorf("no error message contains %q", substr)
}

// AssertFatalContaining fails if no Fatalf message contains the substring.
func (t *TB) AssertFatalContaining(substr string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, fatal := range t.Fatals {
		if strings.Contains(fatal, substr) {
			return nil
		}
	}
	return fmt.Errorf("no fatal message contains %q", substr)
}

// AssertNoErrors fails if any Errorf messages were recorded.
func (t *TB) AssertNoErrors() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.Errors) > 0 {
		return fmt.Errorf("expected no errors, got %d: %v", len(t.Errors), t.Errors)
	}
	return nil
}

// AssertNoFatals fails if any Fatalf messages were recorded.
func (t *TB) AssertNoFatals() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.Fatals) > 0 {
		return fmt.Errorf("expected no fatals, got %d: %v", len(t.Fatals), t.Fatals)
	}
	return nil
}

// AssertNoFailures fails if any failures were recorded.
func (t *TB) AssertNoFailures() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.Errors) > 0 || len(t.Fatals) > 0 {
		return fmt.Errorf("expected no failures, got %d errors and %d fatals", len(t.Errors), len(t.Fatals))
	}
	return nil
}
