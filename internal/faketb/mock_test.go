package faketb_test

import (
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
)

// TestNewTB tests that New returns a TB with default values.
func TestNewTB(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.TestName != "TestFake" {
		t.Errorf("TestName = %q, want %q", tb.TestName, "TestFake")
	}
	if tb.Errors != nil {
		t.Errorf("Errors = %v, want nil", tb.Errors)
	}
	if tb.Fatals != nil {
		t.Errorf("Fatals = %v, want nil", tb.Fatals)
	}
	if tb.Helpers != 0 {
		t.Errorf("Helpers = %d, want 0", tb.Helpers)
	}
	if tb.Failed() {
		t.Error("Failed() = true, want false for new TB")
	}
}

// TestHelper tests that Helper increments the counter.
func TestHelper(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Helper()
	if tb.Helpers != 1 {
		t.Errorf("Helpers = %d, want 1", tb.Helpers)
	}
	tb.Helper()
	tb.Helper()
	if tb.Helpers != 3 {
		t.Errorf("Helpers = %d, want 3", tb.Helpers)
	}
}

// TestErrorf tests that Errorf records formatted messages.
func TestErrorf(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Errorf("error %d", 1)
	tb.Errorf("error %s", "two")

	if len(tb.Errors) != 2 {
		t.Errorf("len(Errors) = %d, want 2", len(tb.Errors))
	}
	if tb.Errors[0] != "error 1" {
		t.Errorf("Errors[0] = %q, want %q", tb.Errors[0], "error 1")
	}
	if tb.Errors[1] != "error two" {
		t.Errorf("Errors[1] = %q, want %q", tb.Errors[1], "error two")
	}
	if !tb.Failed() {
		t.Error("Failed() = false, want true after Errorf")
	}
}

// TestFatalf tests that Fatalf records formatted messages and marks fatalCalled.
func TestFatalf(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Fatalf("fatal %d", 1)
	tb.Fatalf("fatal %s", "two")

	if len(tb.Fatals) != 2 {
		t.Errorf("len(Fatals) = %d, want 2", len(tb.Fatals))
	}
	if tb.Fatals[0] != "fatal 1" {
		t.Errorf("Fatals[0] = %q, want %q", tb.Fatals[0], "fatal 1")
	}
	if tb.Fatals[1] != "fatal two" {
		t.Errorf("Fatals[1] = %q, want %q", tb.Fatals[1], "fatal two")
	}
	if !tb.Failed() {
		t.Error("Failed() = false, want true after Fatalf")
	}
	if !tb.FatalfCalled() {
		t.Error("FatalfCalled() = false, want true after Fatalf")
	}
}

// TestName tests that Name returns the test name.
func TestName(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.Name() != "TestFake" {
		t.Errorf("Name() = %q, want %q", tb.Name(), "TestFake")
	}
}

// TestOutput tests that Output joins all messages with newlines (Errors first, then Fatals).
func TestOutput(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Errorf("error 1")
	tb.Fatalf("fatal 1")
	tb.Errorf("error 2")

	output := tb.Output()
	// Output joins Errors first, then Fatals
	expected := "error 1\nerror 2\nfatal 1"
	if output != expected {
		t.Errorf("Output() = %q, want %q", output, expected)
	}
}

// TestMessageSingleError tests Message with exactly one error.
func TestMessageSingleError(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Errorf("single error")

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("Message() error: %v", err)
	}
	if msg != "single error" {
		t.Errorf("Message() = %q, want %q", msg, "single error")
	}
}

// TestMessageSingleFatal tests Message with exactly one fatal.
func TestMessageSingleFatal(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Fatalf("single fatal")

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("Message() error: %v", err)
	}
	if msg != "single fatal" {
		t.Errorf("Message() = %q, want %q", msg, "single fatal")
	}
}

// TestMessageNoFailures tests Message with no failures.
func TestMessageNoFailures(t *testing.T) {
	t.Parallel()

	tb := faketb.New()

	_, err := tb.Message()
	if err == nil {
		t.Fatal("Message() error = nil, want error")
	}
	if err.Error() != "no failure was reported" {
		t.Errorf("Message() error = %q, want %q", err.Error(), "no failure was reported")
	}
}

// TestMessageMultipleFailures tests Message with multiple failures.
func TestMessageMultipleFailures(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Errorf("error 1")
	tb.Errorf("error 2")

	_, err := tb.Message()
	if err == nil {
		t.Fatal("Message() error = nil, want error")
	}
	expected := "expected exactly one failure, got 2"
	if err.Error()[:len(expected)] != expected {
		t.Errorf("Message() error = %q, want to start with %q", err.Error(), expected)
	}
}

// TestErrorCount tests ErrorCount returns the number of errors.
func TestErrorCount(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.ErrorCount() != 0 {
		t.Errorf("ErrorCount() = %d, want 0", tb.ErrorCount())
	}
	tb.Errorf("error 1")
	if tb.ErrorCount() != 1 {
		t.Errorf("ErrorCount() = %d, want 1", tb.ErrorCount())
	}
	tb.Errorf("error 2")
	if tb.ErrorCount() != 2 {
		t.Errorf("ErrorCount() = %d, want 2", tb.ErrorCount())
	}
}

// TestFatalCount tests FatalCount returns the number of fatals.
func TestFatalCount(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.FatalCount() != 0 {
		t.Errorf("FatalCount() = %d, want 0", tb.FatalCount())
	}
	tb.Fatalf("fatal 1")
	if tb.FatalCount() != 1 {
		t.Errorf("FatalCount() = %d, want 1", tb.FatalCount())
	}
	tb.Fatalf("fatal 2")
	if tb.FatalCount() != 2 {
		t.Errorf("FatalCount() = %d, want 2", tb.FatalCount())
	}
}

// TestTotalCount tests TotalCount returns the total number of failures.
func TestTotalCount(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.TotalCount() != 0 {
		t.Errorf("TotalCount() = %d, want 0", tb.TotalCount())
	}
	tb.Errorf("error 1")
	if tb.TotalCount() != 1 {
		t.Errorf("TotalCount() = %d, want 1", tb.TotalCount())
	}
	tb.Fatalf("fatal 1")
	if tb.TotalCount() != 2 {
		t.Errorf("TotalCount() = %d, want 2", tb.TotalCount())
	}
	tb.Errorf("error 2")
	if tb.TotalCount() != 3 {
		t.Errorf("TotalCount() = %d, want 3", tb.TotalCount())
	}
}

// TestFatalfCalled tests FatalfCalled reports whether Fatalf was called.
func TestFatalfCalled(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.FatalfCalled() {
		t.Error("FatalfCalled() = true, want false initially")
	}
	tb.Errorf("error")
	if tb.FatalfCalled() {
		t.Error("FatalfCalled() = true, want false after Errorf")
	}
	tb.Fatalf("fatal")
	if !tb.FatalfCalled() {
		t.Error("FatalfCalled() = false, want true after Fatalf")
	}
}

// TestLastError tests LastError returns the most recent error.
func TestLastError(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.LastError() != "" {
		t.Errorf("LastError() = %q, want empty string", tb.LastError())
	}
	tb.Errorf("first error")
	if tb.LastError() != "first error" {
		t.Errorf("LastError() = %q, want %q", tb.LastError(), "first error")
	}
	tb.Errorf("second error")
	if tb.LastError() != "second error" {
		t.Errorf("LastError() = %q, want %q", tb.LastError(), "second error")
	}
}

// TestLastFatal tests LastFatal returns the most recent fatal.
func TestLastFatal(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if tb.LastFatal() != "" {
		t.Errorf("LastFatal() = %q, want empty string", tb.LastFatal())
	}
	tb.Fatalf("first fatal")
	if tb.LastFatal() != "first fatal" {
		t.Errorf("LastFatal() = %q, want %q", tb.LastFatal(), "first fatal")
	}
	tb.Fatalf("second fatal")
	if tb.LastFatal() != "second fatal" {
		t.Errorf("LastFatal() = %q, want %q", tb.LastFatal(), "second fatal")
	}
}

// TestErrorsContaining tests ErrorsContaining returns matching errors.
func TestErrorsContaining(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Errorf("error about payment")
	tb.Errorf("error about trustline")
	tb.Errorf("another payment error")

	matches := tb.ErrorsContaining("payment")
	if len(matches) != 2 {
		t.Errorf("len(ErrorsContaining(payment)) = %d, want 2", len(matches))
	}
	if matches[0] != "error about payment" {
		t.Errorf("matches[0] = %q, want %q", matches[0], "error about payment")
	}
	if matches[1] != "another payment error" {
		t.Errorf("matches[1] = %q, want %q", matches[1], "another payment error")
	}

	noMatches := tb.ErrorsContaining("nonexistent")
	if len(noMatches) != 0 {
		t.Errorf("len(ErrorsContaining(nonexistent)) = %d, want 0", len(noMatches))
	}
}

// TestFatalsContaining tests FatalsContaining returns matching fatals.
func TestFatalsContaining(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Fatalf("fatal about payment")
	tb.Fatalf("fatal about trustline")
	tb.Fatalf("another payment fatal")

	matches := tb.FatalsContaining("payment")
	if len(matches) != 2 {
		t.Errorf("len(FatalsContaining(payment)) = %d, want 2", len(matches))
	}
}

// TestReset tests Reset clears all recorded messages and state.
func TestReset(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Errorf("error 1")
	tb.Fatalf("fatal 1")
	tb.Helper()
	tb.Helper()

	if tb.ErrorCount() != 1 {
		t.Errorf("ErrorCount before Reset = %d, want 1", tb.ErrorCount())
	}
	if tb.FatalCount() != 1 {
		t.Errorf("FatalCount before Reset = %d, want 1", tb.FatalCount())
	}
	if tb.Helpers != 2 {
		t.Errorf("Helpers before Reset = %d, want 2", tb.Helpers)
	}
	if tb.FatalfCalled() != true {
		t.Error("FatalfCalled before Reset = false, want true")
	}

	tb.Reset()

	if tb.ErrorCount() != 0 {
		t.Errorf("ErrorCount after Reset = %d, want 0", tb.ErrorCount())
	}
	if tb.FatalCount() != 0 {
		t.Errorf("FatalCount after Reset = %d, want 0", tb.FatalCount())
	}
	if tb.Helpers != 0 {
		t.Errorf("Helpers after Reset = %d, want 0", tb.Helpers)
	}
	if tb.FatalfCalled() != false {
		t.Error("FatalfCalled after Reset = true, want false")
	}
	if tb.Failed() {
		t.Error("Failed() = true, want false after Reset")
	}
}

// TestAssertErrorCount tests AssertErrorCount validates error count.
func TestAssertErrorCount(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if err := tb.AssertErrorCount(0); err != nil {
		t.Errorf("AssertErrorCount(0) error: %v", err)
	}
	tb.Errorf("error 1")
	if err := tb.AssertErrorCount(1); err != nil {
		t.Errorf("AssertErrorCount(1) error: %v", err)
	}
	err := tb.AssertErrorCount(0)
	if err == nil {
		t.Error("AssertErrorCount(0) error = nil, want error")
	}
	if err.Error() != "expected 0 errors, got 1" {
		t.Errorf("AssertErrorCount(0) error = %q, want %q", err.Error(), "expected 0 errors, got 1")
	}
}

// TestAssertFatalCount tests AssertFatalCount validates fatal count.
func TestAssertFatalCount(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if err := tb.AssertFatalCount(0); err != nil {
		t.Errorf("AssertFatalCount(0) error: %v", err)
	}
	tb.Fatalf("fatal 1")
	if err := tb.AssertFatalCount(1); err != nil {
		t.Errorf("AssertFatalCount(1) error: %v", err)
	}
	err := tb.AssertFatalCount(0)
	if err == nil {
		t.Error("AssertFatalCount(0) error = nil, want error")
	}
	if err.Error() != "expected 0 fatals, got 1" {
		t.Errorf("AssertFatalCount(0) error = %q, want %q", err.Error(), "expected 0 fatals, got 1")
	}
}

// TestAssertTotalCount tests AssertTotalCount validates total count.
func TestAssertTotalCount(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if err := tb.AssertTotalCount(0); err != nil {
		t.Errorf("AssertTotalCount(0) error: %v", err)
	}
	tb.Errorf("error 1")
	tb.Fatalf("fatal 1")
	if err := tb.AssertTotalCount(2); err != nil {
		t.Errorf("AssertTotalCount(2) error: %v", err)
	}
	err := tb.AssertTotalCount(0)
	if err == nil {
		t.Error("AssertTotalCount(0) error = nil, want error")
	}
	if err.Error() != "expected 0 total failures, got 2" {
		t.Errorf("AssertTotalCount(0) error = %q, want %q", err.Error(), "expected 0 total failures, got 2")
	}
}

// TestAssertErrorContaining tests AssertErrorContaining validates substring.
func TestAssertErrorContaining(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Errorf("error about payment")

	if err := tb.AssertErrorContaining("payment"); err != nil {
		t.Errorf("AssertErrorContaining(payment) error: %v", err)
	}
	err := tb.AssertErrorContaining("trustline")
	if err == nil {
		t.Error("AssertErrorContaining(trustline) error = nil, want error")
	}
	if err.Error() != "no error message contains \"trustline\"" {
		t.Errorf("AssertErrorContaining(trustline) error = %q, want %q", err.Error(), "no error message contains \"trustline\"")
	}
}

// TestAssertFatalContaining tests AssertFatalContaining validates substring.
func TestAssertFatalContaining(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	tb.Fatalf("fatal about payment")

	if err := tb.AssertFatalContaining("payment"); err != nil {
		t.Errorf("AssertFatalContaining(payment) error: %v", err)
	}
	err := tb.AssertFatalContaining("trustline")
	if err == nil {
		t.Error("AssertFatalContaining(trustline) error = nil, want error")
	}
}

// TestAssertNoErrors tests AssertNoErrors fails if any errors recorded.
func TestAssertNoErrors(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if err := tb.AssertNoErrors(); err != nil {
		t.Errorf("AssertNoErrors() error: %v", err)
	}
	tb.Errorf("error 1")
	err := tb.AssertNoErrors()
	if err == nil {
		t.Error("AssertNoErrors() error = nil, want error")
	}
	if err.Error() != "expected no errors, got 1: [error 1]" {
		t.Errorf("AssertNoErrors() error = %q, want %q", err.Error(), "expected no errors, got 1: [error 1]")
	}
}

// TestAssertNoFatals tests AssertNoFatals fails if any fatals recorded.
func TestAssertNoFatals(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if err := tb.AssertNoFatals(); err != nil {
		t.Errorf("AssertNoFatals() error: %v", err)
	}
	tb.Fatalf("fatal 1")
	err := tb.AssertNoFatals()
	if err == nil {
		t.Error("AssertNoFatals() error = nil, want error")
	}
}

// TestAssertNoFailures tests AssertNoFailures fails if any failures recorded.
func TestAssertNoFailures(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if err := tb.AssertNoFailures(); err != nil {
		t.Errorf("AssertNoFailures() error: %v", err)
	}
	tb.Errorf("error 1")
	err := tb.AssertNoFailures()
	if err == nil {
		t.Error("AssertNoFailures() error = nil, want error")
	}
	tb.Reset()
	tb.Fatalf("fatal 1")
	err = tb.AssertNoFailures()
	if err == nil {
		t.Error("AssertNoFailures() error = nil, want error")
	}
}

// TestConcurrentAccess tests thread-safe access to TB methods.
func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	done := make(chan bool, 100)

	// Write from multiple goroutines
	for i := 0; i < 50; i++ {
		go func(n int) {
			tb.Errorf("error %d", n)
			done <- true
		}(i)
	}
	for i := 0; i < 50; i++ {
		go func(n int) {
			tb.Fatalf("fatal %d", n)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}

	if tb.ErrorCount() != 50 {
		t.Errorf("ErrorCount = %d, want 50", tb.ErrorCount())
	}
	if tb.FatalCount() != 50 {
		t.Errorf("FatalCount = %d, want 50", tb.FatalCount())
	}
	if tb.TotalCount() != 100 {
		t.Errorf("TotalCount = %d, want 100", tb.TotalCount())
	}
}
