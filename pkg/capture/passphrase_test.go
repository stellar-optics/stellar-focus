package capture_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

const (
	capturedHash         = "abc123"
	testnetPassphrase    = "Test SDF Network ; September 2015"
	standalonePassphrase = "Standalone Network ; February 2017"
)

// silentSource hides the fake's NetworkPassphrase method, standing in for a
// source such as Horizon that does not report a passphrase.
type silentSource struct{ capture.Source }

func captureOne(t *testing.T, src capture.Source) []capture.Record {
	t.Helper()
	records, err := src.Transaction(context.Background(), capturedHash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	return records
}

func TestCaptureFallsBackToTheKnownNetworkPassphrase(t *testing.T) {
	t.Parallel()

	const hash = capturedHash
	src := silentSource{sourceWithTransaction(t, hash)}

	fixtures, err := capture.Capture(context.Background(), src, captureOne(t, src), capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if got := fixtures[0].Captured.NetworkPassphrase; got != testnetPassphrase {
		t.Errorf("captured passphrase = %q, want the testnet passphrase", got)
	}

	// A source that cannot report a passphrase still verifies a fixture
	// from a network whose passphrase is known.
	if got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0]); got.Err != nil || !got.Match {
		t.Errorf("Verify() = %+v, want a match", got)
	}
}

func TestCaptureLeavesAnUnknownNetworkPassphraseEmpty(t *testing.T) {
	t.Parallel()

	const hash = capturedHash
	fake := fakesource.New("standalone", "http://localhost:8000/rpc")
	fake.Transactions = sourceWithTransaction(t, hash).Transactions
	src := silentSource{fake}

	fixtures, err := capture.Capture(context.Background(), src, captureOne(t, src), capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if got := fixtures[0].Captured.NetworkPassphrase; got != "" {
		t.Errorf("captured passphrase = %q, want none for a network focus cannot name", got)
	}
}

func TestWithPassphraseStampsAnExplicitPassphrase(t *testing.T) {
	t.Parallel()

	const hash = capturedHash
	fake := fakesource.New("standalone", "http://localhost:8000/rpc")
	fake.Transactions = sourceWithTransaction(t, hash).Transactions
	src := capture.WithPassphrase(silentSource{fake}, standalonePassphrase)

	fixtures, err := capture.Capture(context.Background(), src, captureOne(t, src), capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if got := fixtures[0].Captured.NetworkPassphrase; got != standalonePassphrase {
		t.Fatalf("captured passphrase = %q, want %q", got, standalonePassphrase)
	}

	if got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0]); got.Err != nil || !got.Match {
		t.Errorf("Verify() = %+v, want a match", got)
	}

	// The same fixture checked without the passphrase cannot be verified,
	// and says how to fix that.
	got := capture.Verify(context.Background(), silentSource{fake}, "envelope.golden.json", fixtures[0])
	if got.Err == nil || !strings.Contains(got.Err.Error(), "--passphrase") {
		t.Errorf("Verify() error = %v, want a hint to pass --passphrase", got.Err)
	}
}

func TestWithPassphraseRejectsAPassphraseTheSourceContradicts(t *testing.T) {
	t.Parallel()

	const hash = capturedHash
	fake := sourceWithTransaction(t, hash)
	fake.Passphrase = testnetPassphrase
	src := capture.WithPassphrase(fake, standalonePassphrase)

	_, err := capture.Capture(context.Background(), src, captureOne(t, src), capturedAt)
	if err == nil {
		t.Fatal("Capture() succeeded with a passphrase the network contradicts")
	}
	for _, want := range []string{"passphrase mismatch", standalonePassphrase, testnetPassphrase} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestWithPassphraseIsANoOpWhenEmpty(t *testing.T) {
	t.Parallel()

	src := sourceWithTransaction(t, "abc123")
	if got := capture.WithPassphrase(src, ""); got != capture.Source(src) {
		t.Errorf("WithPassphrase(src, \"\") wrapped the source; want it returned unchanged")
	}
}
