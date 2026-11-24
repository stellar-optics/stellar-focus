package cli

import (
	"errors"
	"fmt"
)

// Exit codes returned by the focus command.
//
// The distinction that matters to a script is between "the command could not
// do its job" and "the command did its job and found what it was looking
// for". A CI job should retry or alert differently on an unreachable endpoint
// than on a fixture that has genuinely drifted, so the two cases do not share
// a code.
const (
	// ExitSuccess means the command completed and found nothing wrong.
	ExitSuccess = 0

	// ExitFailure means the command could not complete: bad usage, an
	// unreadable file, an unreachable endpoint, or an unexpected error.
	ExitFailure = 1

	// ExitDrift means verification completed and found at least one fixture
	// that no longer matches the chain. It is a successful check with a
	// negative result, not an inability to check.
	ExitDrift = 2
)

// Sentinel errors let callers classify an outcome with errors.Is without
// depending on the concrete error type or on the message text.
var (
	// ErrDrift marks a check that completed and found drift.
	ErrDrift = errors.New("fixtures no longer match the chain")
	// ErrUsage marks an invalid flag, environment variable or config file.
	ErrUsage = errors.New("invalid usage")
)

// exitError carries an explicit process exit code while remaining an ordinary
// Go error that can be wrapped and inspected.
type exitError struct {
	code int
	msg  string
	// cause is the sentinel the error classifies as, if any.
	cause error
}

// Error implements error.
func (e *exitError) Error() string { return e.msg }

// Unwrap returns the sentinel cause, so errors.Is works through wrapping.
func (e *exitError) Unwrap() error { return e.cause }

// driftErrorf reports a completed verification that found stale fixtures.
func driftErrorf(format string, args ...any) error {
	return &exitError{
		code:  ExitDrift,
		msg:   fmt.Sprintf(format, args...),
		cause: ErrDrift,
	}
}

// usageErrorf reports a flag, environment or config problem. It exits with
// ExitFailure because the command never ran, and keeps ErrUsage available so
// callers can distinguish a configuration mistake from an operational one.
func usageErrorf(format string, args ...any) error {
	return &exitError{
		code:  ExitFailure,
		msg:   fmt.Sprintf(format, args...),
		cause: ErrUsage,
	}
}

// failureErrorf reports an operational failure with an explicit exit code.
func failureErrorf(format string, args ...any) error {
	return &exitError{
		code: ExitFailure,
		msg:  fmt.Sprintf(format, args...),
	}
}

// ExitCode maps an error to a process exit code and reports whether main
// should print the error message.
//
// A nil error is success and prints nothing. An *exitError contributes its
// code even when wrapped, so a command can add context with fmt.Errorf("%w")
// without losing the intended exit status. Every other error is a failure and
// is printed, matching the behaviour of an ordinary main.
func ExitCode(err error) (code int, printable bool) {
	if err == nil {
		return ExitSuccess, false
	}

	var coded *exitError
	if errors.As(err, &coded) {
		return coded.code, true
	}
	return ExitFailure, true
}

// IsDrift reports whether err represents detected fixture drift rather than a
// failure to run the check.
func IsDrift(err error) bool {
	return errors.Is(err, ErrDrift)
}

// IsUsage reports whether err represents invalid flags, environment or
// configuration.
func IsUsage(err error) bool {
	return errors.Is(err, ErrUsage)
}
