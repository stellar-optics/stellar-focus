package capture_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// capturedAt is fixed so fixtures produced by these tests are byte-stable.
var capturedAt = time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

func envelopeXDR(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func resultXDR(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tx_failed.res.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func sourceWithTransaction(t *testing.T, hash string) *fakesource.Source {
	t.Helper()
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	src.Transactions[hash] = []capture.Record{
		{
			Name: "envelope", Type: "TransactionEnvelope", XDR: envelopeXDR(t),
			Ledger: 1234, TxHash: hash, Description: "envelope",
		},
		{
			Name: "result", Type: "TransactionResult", XDR: resultXDR(t),
			Ledger: 1234, TxHash: hash, Description: "result",
		},
	}
	return src
}

func TestCaptureStampsProvenance(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)

	records, err := src.Transaction(context.Background(), hash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}

	fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("captured %d fixtures, want 2", len(fixtures))
	}

	for _, f := range fixtures {
		if f.Captured == nil {
			t.Fatal("fixture has no provenance; a captured fixture must be traceable")
		}
		if f.Captured.Network != capture.NetworkTestnet {
			t.Errorf("network = %q, want testnet", f.Captured.Network)
		}
		if f.Captured.TxHash != hash {
			t.Errorf("txHash = %q, want %q", f.Captured.TxHash, hash)
		}
		if f.Captured.Ledger != 1234 {
			t.Errorf("ledger = %d, want 1234", f.Captured.Ledger)
		}
		if f.Captured.At != "2026-08-05T12:00:00Z" {
			t.Errorf("at = %q, want the supplied timestamp", f.Captured.At)
		}
		if f.Captured.Endpoint != "https://fake.example" {
			t.Errorf("endpoint = %q", f.Captured.Endpoint)
		}
		if f.XDR == "" || f.Decoded == nil {
			t.Error("fixture is missing its XDR or decoded form")
		}
	}
}

// TestCaptureIsDeterministic matters because a capture that produced a
// different file each run would make every refresh look like a change.
func TestCaptureIsDeterministic(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	records, err := src.Transaction(context.Background(), hash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}

	dir := t.TempDir()
	render := func(name string) string {
		fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
		if err != nil {
			t.Fatalf("Capture() error = %v", err)
		}
		path := filepath.Join(dir, name)
		if err := golden.Save(path, fixtures[0]); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		return string(b)
	}

	if render("a.json") != render("b.json") {
		t.Error("two captures of the same data produced different files")
	}
}

func TestVerifyMatches(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	records, _ := src.Transaction(context.Background(), hash)
	fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0])
	if got.Err != nil {
		t.Fatalf("Verify() error = %v", got.Err)
	}
	if !got.Match {
		t.Errorf("Verify() reported a mismatch for unchanged data; diff = %v", got.Diff)
	}
}

// TestVerifyDetectsDrift covers the point of the command: a fixture that no
// longer matches the chain must be reported, with the change named.
func TestVerifyDetectsDrift(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	records, _ := src.Transaction(context.Background(), hash)
	fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	// The chain now returns a different envelope for the same hash.
	src.Transactions[hash] = []capture.Record{{
		Name: "envelope", Type: "TransactionEnvelope",
		XDR:    fixtureFromFile(t, "tx_success.env.txt"),
		Ledger: 1234, TxHash: hash,
	}}

	got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0])
	if got.Err != nil {
		t.Fatalf("Verify() error = %v", got.Err)
	}
	if got.Match {
		t.Fatal("Verify() reported a match for changed data")
	}
	if got.Diff == nil || len(got.Diff.Changes) == 0 {
		t.Fatal("Verify() reported no diff explaining the mismatch")
	}
}

// TestVerifyRefusesFixturesWithNoProvenance keeps a hand-written fixture from
// silently passing a check it was never eligible for.
func TestVerifyRefusesFixturesWithNoProvenance(t *testing.T) {
	t.Parallel()

	f, err := golden.Build(envelopeXDR(t), "TransactionEnvelope", "hand written", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	got := capture.Verify(context.Background(), src, "hand.golden.json", f)

	if got.Err == nil {
		t.Fatal("Verify() accepted a fixture with no provenance")
	}
	if got.Match {
		t.Error("Verify() reported a match it could not have checked")
	}
	if !strings.Contains(got.Err.Error(), "provenance") {
		t.Errorf("error does not explain why: %v", got.Err)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for an unverifiable fixture", src.Calls)
	}
}

func TestVerifyReportsAMissingTransaction(t *testing.T) {
	t.Parallel()

	src := sourceWithTransaction(t, "abc123")
	records, _ := src.Transaction(context.Background(), "abc123")
	fixtures, _ := capture.Capture(context.Background(), src, records, capturedAt)

	// The transaction has aged out of the endpoint's retention window.
	delete(src.Transactions, "abc123")

	got := capture.Verify(context.Background(), src, "env.golden.json", fixtures[0])
	if got.Err == nil {
		t.Fatal("Verify() succeeded against a transaction that is gone")
	}
	if !errors.Is(got.Err, capture.ErrNotFound) {
		t.Errorf("error does not wrap ErrNotFound: %v", got.Err)
	}
}

func TestResolveEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		network  string
		override string
		want     string
		wantErr  bool
	}{
		{"testnet default", capture.NetworkTestnet, "", capture.DefaultEndpoints[capture.NetworkTestnet], false},
		{"mainnet default", capture.NetworkMainnet, "", capture.DefaultEndpoints[capture.NetworkMainnet], false},
		{"override wins", capture.NetworkTestnet, "http://localhost:8000", "http://localhost:8000", false},
		{"unknown network", "wonderland", "", "", true},
		{"unknown network with override", "wonderland", "http://x", "http://x", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := capture.ResolveEndpoint(tc.network, tc.override)
			if tc.wantErr {
				if err == nil {
					t.Fatal("ResolveEndpoint() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveEndpoint() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDefaultIsTestnet guards against a tool that reaches for mainnet unless
// told otherwise.
func TestDefaultIsTestnet(t *testing.T) {
	t.Parallel()

	if !strings.Contains(capture.DefaultEndpoints[capture.NetworkTestnet], "testnet") {
		t.Error("the testnet endpoint does not look like testnet")
	}
}

func fixtureFromFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}
