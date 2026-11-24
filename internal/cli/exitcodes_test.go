package cli

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeValues(t *testing.T) {
	t.Parallel()

	// The numbers are part of the command's contract: a CI script branches
	// on them, so a change here needs a migration note, not a quiet edit.
	if ExitSuccess != 0 {
		t.Errorf("ExitSuccess = %d, want 0", ExitSuccess)
	}
	if ExitFailure != 1 {
		t.Errorf("ExitFailure = %d, want 1", ExitFailure)
	}
	if ExitDrift != 2 {
		t.Errorf("ExitDrift = %d, want 2", ExitDrift)
	}
}

func TestExitCodeMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		err           error
		wantCode      int
		wantPrintable bool
	}{
		{
			name:          "success",
			err:           nil,
			wantCode:      ExitSuccess,
			wantPrintable: false,
		},
		{
			name:          "plain failure is printed",
			err:           errors.New("boom"),
			wantCode:      ExitFailure,
			wantPrintable: true,
		},
		{
			name:          "wrapped plain failure keeps its code",
			err:           fmt.Errorf("reading fixture: %w", errors.New("boom")),
			wantCode:      ExitFailure,
			wantPrintable: true,
		},
		{
			name:          "explicit failure",
			err:           failureErrorf("could not write %s", "out.json"),
			wantCode:      ExitFailure,
			wantPrintable: true,
		},
		{
			name:          "drift",
			err:           driftErrorf("%d fixture(s) drifted", 3),
			wantCode:      ExitDrift,
			wantPrintable: true,
		},
		{
			name:          "wrapped drift keeps its code",
			err:           fmt.Errorf("verify testdata: %w", driftErrorf("1 fixture drifted")),
			wantCode:      ExitDrift,
			wantPrintable: true,
		},
		{
			name:          "usage failure",
			err:           usageErrorf("unknown network %q", "venus"),
			wantCode:      ExitFailure,
			wantPrintable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, printable := ExitCode(tc.err)
			if code != tc.wantCode || printable != tc.wantPrintable {
				t.Errorf("ExitCode(%v) = (%d, %v), want (%d, %v)",
					tc.err, code, printable, tc.wantCode, tc.wantPrintable)
			}
		})
	}
}

func TestDriftErrorClassification(t *testing.T) {
	t.Parallel()

	err := driftErrorf("%d fixture(s) no longer match the chain", 2)
	if !IsDrift(err) {
		t.Error("IsDrift() = false for a drift error")
	}
	if IsUsage(err) {
		t.Error("IsUsage() = true for a drift error")
	}
	if !errors.Is(err, ErrDrift) {
		t.Error("errors.Is(err, ErrDrift) = false")
	}
	if got := err.Error(); got != "2 fixture(s) no longer match the chain" {
		t.Errorf("Error() = %q, want the caller's message only", got)
	}

	// Wrapping for context must not lose the classification.
	wrapped := fmt.Errorf("verifying testdata: %w", err)
	if !IsDrift(wrapped) {
		t.Error("IsDrift() = false after wrapping")
	}
	if code, _ := ExitCode(wrapped); code != ExitDrift {
		t.Errorf("ExitCode() = %d after wrapping, want %d", code, ExitDrift)
	}
}

func TestUsageErrorClassification(t *testing.T) {
	t.Parallel()

	err := usageErrorf("color must be %q", "never")
	if !IsUsage(err) {
		t.Error("IsUsage() = false for a usage error")
	}
	if IsDrift(err) {
		t.Error("IsDrift() = true for a usage error")
	}
	if !errors.Is(err, ErrUsage) {
		t.Error("errors.Is(err, ErrUsage) = false")
	}
	if code, printable := ExitCode(err); code != ExitFailure || !printable {
		t.Errorf("ExitCode() = (%d, %v), want (%d, true)", code, printable, ExitFailure)
	}
}

func TestExitErrorWrapping(t *testing.T) {
	t.Parallel()

	err := &exitError{
		code:  ExitDrift,
		msg:   "fixtures drifted",
		cause: ErrDrift,
	}

	var coded *exitError
	if !errors.As(err, &coded) {
		t.Fatal("errors.As could not recover *exitError")
	}
	if coded.code != ExitDrift {
		t.Errorf("code = %d, want %d", coded.code, ExitDrift)
	}
	if !errors.Is(err, ErrDrift) {
		t.Error("Unwrap did not expose ErrDrift")
	}
}
