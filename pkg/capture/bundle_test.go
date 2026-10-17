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

// sorobanEnvXDR is a second, distinct, valid envelope, so a bundle (or a
// drift) has more than one real payload to work with.
func sorobanEnvXDR(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tx_soroban.env.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// sourceWithTwoTransactions returns a source holding two distinct
// transactions, so a bundle has more than one member to coordinate.
func sourceWithTwoTransactions(t *testing.T) *fakesource.Source {
	t.Helper()
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	src.Transactions["hash-one"] = []capture.Record{{
		Name: "envelope", Type: "TransactionEnvelope", XDR: envelopeXDR(t),
		Ledger: 100, TxHash: "hash-one", Description: "one",
	}}
	src.Transactions["hash-two"] = []capture.Record{{
		Name: "envelope", Type: "TransactionEnvelope", XDR: sorobanEnvXDR(t),
		Ledger: 200, TxHash: "hash-two", Description: "two",
	}}
	return src
}

func TestCaptureBundleWritesFixturesAndManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)

	bundle, written, err := capture.GatherBundle(context.Background(), src, capture.BundleRequest{
		Hashes:      []string{"hash-one", "hash-two"},
		Description: "a settlement, end to end",
		Prefix:      "settlement",
	}, dir, capturedAt)
	if err != nil {
		t.Fatalf("GatherBundle: %v", err)
	}

	// Two fixtures plus the manifest.
	if len(written) != 3 {
		t.Fatalf("wrote %d paths (%v), want 3", len(written), written)
	}
	if len(bundle.Members) != 2 {
		t.Fatalf("bundle has %d members, want 2", len(bundle.Members))
	}

	// Order follows the request, so a reviewer reads the set in the order
	// the transactions happened.
	if bundle.Members[0].TxHash != "hash-one" || bundle.Members[1].TxHash != "hash-two" {
		t.Errorf("member order %q, %q; want request order",
			bundle.Members[0].TxHash, bundle.Members[1].TxHash)
	}

	// Every member names a file that exists and loads.
	for _, m := range bundle.Members {
		f, loadErr := golden.Load(filepath.Join(dir, m.Name))
		if loadErr != nil {
			t.Fatalf("member %s: %v", m.Name, loadErr)
		}
		if f.Captured == nil || f.Captured.TxHash != m.TxHash {
			t.Errorf("member %s fixture provenance %v, want tx %s", m.Name, f.Captured, m.TxHash)
		}
		if m.CapturedAt != capturedAt.UTC().Format(time.RFC3339) {
			t.Errorf("member %s capturedAt %q, want the shared capture time", m.Name, m.CapturedAt)
		}
	}

	// The manifest on disk round-trips.
	reloaded, err := capture.LoadBundle(filepath.Join(dir, capture.ManifestName("settlement")))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	if reloaded.Description != "a settlement, end to end" {
		t.Errorf("description %q", reloaded.Description)
	}
	if reloaded.Network != capture.NetworkTestnet {
		t.Errorf("network %q, want testnet", reloaded.Network)
	}
	if len(reloaded.Members) != 2 {
		t.Errorf("reloaded manifest has %d members, want 2", len(reloaded.Members))
	}
}

func TestCaptureBundleRejectsAnEmptyRequest(t *testing.T) {
	t.Parallel()

	_, _, err := capture.GatherBundle(context.Background(),
		fakesource.New(capture.NetworkTestnet, "https://fake.example"),
		capture.BundleRequest{}, t.TempDir(), capturedAt)
	if err == nil {
		t.Fatal("empty bundle request succeeded")
	}
}

func TestCaptureBundleRejectsDuplicateHashes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)

	_, _, err := capture.GatherBundle(context.Background(), src, capture.BundleRequest{
		Hashes: []string{"hash-one", "hash-one"},
	}, dir, capturedAt)
	if err == nil {
		t.Fatal("duplicate hashes succeeded")
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("reading dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("rejected request still wrote %d files", len(entries))
	}
}

func TestCaptureBundleFetchesEverythingBeforeWriting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)
	// The second hash is missing, so the fetch phase fails after the
	// first transaction was already retrieved.
	_, _, err := capture.GatherBundle(context.Background(), src, capture.BundleRequest{
		Hashes: []string{"hash-one", "hash-missing"},
	}, dir, capturedAt)
	if !errors.Is(err, capture.ErrNotFound) {
		t.Fatalf("error %v, want ErrNotFound", err)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("reading dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("failed bundle left %d files behind; a failed capture must leave the directory untouched", len(entries))
	}
}

func TestCaptureBundleRefusesToClobber(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)

	req := capture.BundleRequest{Hashes: []string{"hash-one"}}
	if _, _, err := capture.GatherBundle(context.Background(), src, req, dir, capturedAt); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if _, _, err := capture.GatherBundle(context.Background(), src, req, dir, capturedAt); err == nil {
		t.Fatal("second capture overwrote the bundle without --force")
	}

	req.Force = true
	if _, _, err := capture.GatherBundle(context.Background(), src, req, dir, capturedAt); err != nil {
		t.Fatalf("capture with Force: %v", err)
	}
}

func TestManifestNameDefaultsToBundle(t *testing.T) {
	t.Parallel()

	if got := capture.ManifestName(""); got != "bundle"+capture.BundleSuffix {
		t.Errorf("ManifestName(\"\") = %q", got)
	}
	if got := capture.ManifestName("settlement"); got != "settlement"+capture.BundleSuffix {
		t.Errorf("ManifestName(\"settlement\") = %q", got)
	}
}

func TestSaveBundleReportsAnUnwritablePath(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}

	err := capture.SaveBundle(filepath.Join(blocker, "sub", "x.bundle.json"), &capture.Bundle{
		Format: capture.BundleFormat,
	})
	if err == nil {
		t.Fatal("SaveBundle succeeded with a file in the way")
	}
}

func TestLoadBundleReportsMalformedJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "broken.bundle.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	if _, err := capture.LoadBundle(path); err == nil {
		t.Fatal("malformed manifest loaded successfully")
	}
}

func TestCaptureBundleReportsAnUnwritableDirectory(t *testing.T) {
	t.Parallel()

	// The fetch phase succeeds; the write phase must fail loudly rather
	// than reporting a bundle it never wrote.
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}

	_, _, err := capture.GatherBundle(context.Background(), sourceWithTwoTransactions(t),
		capture.BundleRequest{Hashes: []string{"hash-one"}},
		filepath.Join(blocker, "sub"), capturedAt)
	if err == nil {
		t.Fatal("GatherBundle succeeded with a file in the way")
	}
}

func TestLoadBundleRejectsForeignFormats(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.bundle.json")
	if err := os.WriteFile(path, []byte(`{"format":"focus/bundle/v999"}`), 0o644); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	if _, err := capture.LoadBundle(path); err == nil {
		t.Fatal("foreign format loaded successfully")
	}
}

func TestLoadBundleReportsAMissingFile(t *testing.T) {
	t.Parallel()

	if _, err := capture.LoadBundle(filepath.Join(t.TempDir(), "nope.bundle.json")); err == nil {
		t.Fatal("missing manifest loaded successfully")
	}
}

func TestVerifyBundleChecksEveryMember(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)

	if _, _, err := capture.GatherBundle(context.Background(), src, capture.BundleRequest{
		Hashes: []string{"hash-one", "hash-two"},
	}, dir, capturedAt); err != nil {
		t.Fatalf("GatherBundle: %v", err)
	}

	bundle, err := capture.LoadBundle(filepath.Join(dir, capture.ManifestName("bundle")))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}

	results := capture.VerifyBundle(context.Background(), src, dir, bundle)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("verify %s: %v", r.Path, r.Err)
		}
		if !r.Match {
			t.Errorf("verify %s did not match an unchanged fixture", r.Path)
		}
	}
	if capture.BundleGone(results) != 0 {
		t.Errorf("unchanged bundle reports %d gone members", capture.BundleGone(results))
	}
}

func TestVerifyBundleReportsDriftWithADiff(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)

	if _, _, err := capture.GatherBundle(context.Background(), src, capture.BundleRequest{
		Hashes: []string{"hash-one"},
	}, dir, capturedAt); err != nil {
		t.Fatalf("GatherBundle: %v", err)
	}

	// The chain moves: the same hash now returns different XDR.
	src.Transactions["hash-one"] = []capture.Record{{
		Name: "envelope", Type: "TransactionEnvelope", XDR: sorobanEnvXDR(t),
		Ledger: 999, TxHash: "hash-one",
	}}

	bundle, err := capture.LoadBundle(filepath.Join(dir, capture.ManifestName("bundle")))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}

	results := capture.VerifyBundle(context.Background(), src, dir, bundle)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("drift reported as error: %v", results[0].Err)
	}
	if results[0].Match {
		t.Fatal("changed fixture reported a match")
	}
	if results[0].Diff == nil {
		t.Error("drift came with no diff; the failure message is the product")
	}
}

func TestVerifyBundleReportsAMissingMember(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)

	if _, _, err := capture.GatherBundle(context.Background(), src, capture.BundleRequest{
		Hashes: []string{"hash-one"},
	}, dir, capturedAt); err != nil {
		t.Fatalf("GatherBundle: %v", err)
	}

	bundle, err := capture.LoadBundle(filepath.Join(dir, capture.ManifestName("bundle")))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}

	// A member whose fixture file vanished is a finding, not a skip.
	if err := os.Remove(filepath.Join(dir, bundle.Members[0].Name)); err != nil {
		t.Fatalf("removing member: %v", err)
	}

	results := capture.VerifyBundle(context.Background(), src, dir, bundle)
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("missing member results: %+v, want one result with an error", results)
	}
	if !strings.Contains(results[0].Err.Error(), "loading bundle member") {
		t.Errorf("error %q does not name the missing member", results[0].Err)
	}
}

func TestBundleGoneCountsOnlyGoneMembers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := sourceWithTwoTransactions(t)
	// hash-one captured, then removed from the network.
	if _, _, err := capture.GatherBundle(context.Background(), src, capture.BundleRequest{
		Hashes: []string{"hash-one", "hash-two"},
	}, dir, capturedAt); err != nil {
		t.Fatalf("GatherBundle: %v", err)
	}
	delete(src.Transactions, "hash-one")

	bundle, err := capture.LoadBundle(filepath.Join(dir, capture.ManifestName("bundle")))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}

	results := capture.VerifyBundle(context.Background(), src, dir, bundle)
	if got := capture.BundleGone(results); got != 1 {
		t.Errorf("BundleGone = %d, want 1 (results: %+v)", got, results)
	}
}
