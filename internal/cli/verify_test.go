package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// captureIntoDir seeds a directory with the fixtures of one captured
// transaction, exactly as `focus capture tx` would.
func captureIntoDir(t *testing.T, src *fakesource.Source, dir string) {
	t.Helper()
	stdout, stderr, err := run(t, "capture", "tx", testHash, "--out", dir)
	if err != nil {
		t.Fatalf("capture tx: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
}

func TestVerifyAcceptsAnExplicitFile(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, src, dir)

	path := filepath.Join(dir, "envelope.golden.json")
	stdout, stderr, err := run(t, "verify", path)
	if err != nil {
		t.Fatalf("verify on an explicit file: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "1 checked: 1 ok, 0 drifted, 0 skipped") {
		t.Errorf("summary line missing:\n%s", stdout)
	}
}

func TestVerifyCombinesFilesAndDirectories(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, src, dir)

	envelope := filepath.Join(dir, "envelope.golden.json")
	stdout, stderr, err := run(t, "verify", envelope, dir)
	if err != nil {
		t.Fatalf("verify: %v\nstderr: %s", err, stderr)
	}
	// The explicit file plus the directory's two fixtures.
	if !strings.Contains(stdout, "3 checked: 3 ok, 0 drifted, 0 skipped") {
		t.Errorf("summary line missing:\n%s", stdout)
	}
}

func TestVerifyDefaultsToTestdataWithoutArgs(t *testing.T) {
	withFake(t, sourceWithTx(t))

	empty := t.TempDir()
	t.Chdir(empty)

	_, _, err := run(t, "verify")
	if err == nil {
		t.Fatal("verify succeeded with no testdata directory")
	}
	if !strings.Contains(err.Error(), "testdata") {
		t.Errorf("error %q does not name the directory it looked in", err)
	}
}

func TestVerifyFlagsGoneFixtures(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, src, dir)

	// The transaction ages out of the endpoint's retention window.
	delete(src.Transactions, testHash)

	_, stderr, err := run(t, "verify", dir)
	if err == nil {
		t.Fatal("verify reported success for a fixture that is gone from the network")
	}
	if code, _ := ExitCode(err); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr, "GONE") {
		t.Errorf("gone fixture not reported as GONE:\n%s", stderr)
	}
}

func TestVerifyReportsCorruptFixtures(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, src, dir)

	corrupt := filepath.Join(dir, "corrupt.golden.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("writing corrupt fixture: %v", err)
	}

	_, stderr, err := run(t, "verify", dir)
	if err == nil {
		t.Fatal("verify reported success with a corrupt fixture in the directory")
	}
	if !strings.Contains(stderr, "ERROR") {
		t.Errorf("corrupt fixture not reported as ERROR:\n%s", stderr)
	}
	if !strings.Contains(stderr, "corrupt.golden.json") {
		t.Errorf("corrupt fixture not named:\n%s", stderr)
	}
}

func TestVerifySkipsWhenTheEndpointFails(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, src, dir)

	// The endpoint is unreachable: that is not fixture drift, and the
	// command must not claim the fixtures changed.
	src.Err = errors.New("endpoint unreachable")

	stdout, _, err := run(t, "verify", dir)
	if err != nil {
		t.Fatalf("verify failed on an unreachable endpoint: %v", err)
	}
	if !strings.Contains(stdout, "0 drifted, 2 skipped") {
		t.Errorf("endpoint failure counted as drift:\n%s", stdout)
	}
}

func TestVerifyRejectsAnUnknownNetwork(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, src, dir)

	_, _, err := run(t, "verify", dir, "--network", "venus")
	if err == nil {
		t.Fatal("unknown network accepted")
	}
	if !strings.Contains(err.Error(), "unknown network") {
		t.Errorf("error %q does not explain the problem", err)
	}
}

func TestVerifyDriftExitsOne(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, src, dir)

	// The chain moves: the same hash now returns different XDR.
	src.Transactions[testHash] = driftedRecords()

	_, _, err := run(t, "verify", dir)
	if err == nil {
		t.Fatal("verify reported success despite drift")
	}
	code, printable := ExitCode(err)
	if code != 1 || !printable {
		t.Errorf("ExitCode = (%d, %v), want (1, true)", code, printable)
	}
}

func TestCollectFixtures(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		return path
	}
	envelope := mustWrite("envelope.golden.json")
	mustWrite("notes.txt") // not a fixture; must not be picked up

	// A directory contributes only fixture files.
	paths, err := collectFixtures([]string{dir})
	if err != nil {
		t.Fatalf("collectFixtures(dir): %v", err)
	}
	if len(paths) != 1 || paths[0] != envelope {
		t.Errorf("dir scan = %v, want only %s", paths, envelope)
	}

	// A file argument passes through unchanged, whatever its name.
	paths, err = collectFixtures([]string{envelope})
	if err != nil {
		t.Fatalf("collectFixtures(file): %v", err)
	}
	if len(paths) != 1 || paths[0] != envelope {
		t.Errorf("file arg = %v, want %s", paths, envelope)
	}

	// A missing path is an error, not silently skipped.
	if _, err := collectFixtures([]string{filepath.Join(dir, "nope")}); err == nil {
		t.Error("missing path accepted")
	}
}

// driftedRecords is what the fake endpoint returns after the chain has
// moved: same hash, different payload.
func driftedRecords() []capture.Record {
	return []capture.Record{{
		Name: "envelope", Type: "TransactionEnvelope",
		XDR: "AAAAchanged", TxHash: testHash,
	}}
}
