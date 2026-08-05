// Package faketb provides a recording implementation of focus.TB.
//
// This library's output is its contract, so its tests assert on the text of
// failure messages rather than only on whether an assertion passed. That
// requires capturing what would have been reported instead of failing the
// real test, which is what this package is for.
package faketb

import (
	"fmt"
	"strings"
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
}

// New returns a recording TB.
func New() *TB { return &TB{TestName: "TestFake"} }

// Helper implements focus.TB.
func (t *TB) Helper() { t.Helpers++ }

// Errorf implements focus.TB.
func (t *TB) Errorf(format string, args ...any) {
	t.Errors = append(t.Errors, fmt.Sprintf(format, args...))
}

// Fatalf implements focus.TB.
//
// It records rather than aborting. A real Fatalf ends the goroutine, which a
// fake cannot reproduce without complicating every test that uses it; tests
// here assert on what was reported instead.
func (t *TB) Fatalf(format string, args ...any) {
	t.Fatals = append(t.Fatals, fmt.Sprintf(format, args...))
}

// Name implements focus.TB.
func (t *TB) Name() string { return t.TestName }

// Failed reports whether anything was recorded.
func (t *TB) Failed() bool { return len(t.Errors) > 0 || len(t.Fatals) > 0 }

// Output joins everything recorded, for substring assertions.
func (t *TB) Output() string {
	return strings.Join(append(append([]string{}, t.Errors...), t.Fatals...), "\n")
}

// Message returns the single recorded message, or fails the description when
// the count is not one. Most tests expect exactly one failure, and asserting
// that keeps a test from passing on the wrong message.
func (t *TB) Message() (string, error) {
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
