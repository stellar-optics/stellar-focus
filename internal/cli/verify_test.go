package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// captureIntoDir seeds a directory with the fixtures of one captured
// transaction, exactly as `focus capture tx` would.
func captureIntoDir(t *testing.T, dir string) {
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
	captureIntoDir(t, dir)

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
	captureIntoDir(t, dir)

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
	captureIntoDir(t, dir)

	// The transaction ages out of the endpoint's retention window.
	delete(src.Transactions, testHash)

	_, stderr, err := run(t, "verify", dir)
	if err == nil {
		t.Fatal("verify reported success for a fixture that is gone from the network")
	}
	if code, _ := ExitCode(err); code != ExitDrift {
		t.Errorf("exit code %d, want %d", code, ExitDrift)
	}
	if !strings.Contains(stderr, "GONE") {
		t.Errorf("gone fixture not reported as GONE:\n%s", stderr)
	}
}

func TestVerifyReportsCorruptFixtures(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

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

// TestVerifyReportsUncheckableFixtures checks that a failure to reach the
// endpoint is reported as an inability to check, not as drift and not as a
// pass. A CI job needs to tell those three outcomes apart.
func TestVerifyReportsUncheckableFixtures(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	// The endpoint is unreachable: that is not fixture drift, and the
	// command must not claim the fixtures changed.
	src.Err = errors.New("endpoint unreachable")

	stdout, _, err := run(t, "verify", dir)
	if err == nil {
		t.Fatal("verify reported success when no fixture could be checked")
	}
	if code, _ := ExitCode(err); code != ExitFailure {
		t.Errorf("exit code %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stdout, "0 drifted, 0 skipped, 2 errored") {
		t.Errorf("endpoint failure was not counted as uncheckable:\n%s", stdout)
	}
}

func TestVerifyRejectsAnUnknownNetwork(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	_, _, err := run(t, "verify", dir, "--network", "venus")
	if err == nil {
		t.Fatal("unknown network accepted")
	}
	if !strings.Contains(err.Error(), "unknown network") {
		t.Errorf("error %q does not explain the problem", err)
	}
}

func TestVerifyDriftExitsWithDriftCode(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	// The chain moves: the same hash now returns different envelope XDR,
	// while the result still matches.
	src.Transactions[testHash] = driftedRecords(t)

	_, _, err := run(t, "verify", dir)
	if err == nil {
		t.Fatal("verify reported success despite drift")
	}
	code, printable := ExitCode(err)
	if code != ExitDrift || !printable {
		t.Errorf("ExitCode = (%d, %v), want (%d, true)", code, printable, ExitDrift)
	}
	if !IsDrift(err) {
		t.Errorf("drift error does not classify as ErrDrift: %v", err)
	}
}

// TestVerifySinceChecksOnlyRecentFixtures checks that --since narrows a run
// to the fixtures captured after a threshold, and that the fixtures left out
// are reported rather than quietly passing.
func TestVerifySinceChecksOnlyRecentFixtures(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)
	src.Calls = 0

	// The fixtures were captured at the fake clock's time, so a threshold
	// after it leaves nothing to check.
	stdout, _, err := run(t, "verify", dir, "--since", "2026-08-06T00:00:00Z")
	if err != nil {
		t.Fatalf("verify with a threshold after the capture: %v", err)
	}
	if !strings.Contains(stdout, "FILTERED") {
		t.Errorf("the old fixtures were not reported as filtered:\n%s", stdout)
	}
	if !strings.Contains(stdout, "2 filtered") {
		t.Errorf("the summary does not count the filtered fixtures:\n%s", stdout)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for fixtures that were filtered out", src.Calls)
	}

	// A threshold before the capture leaves both fixtures to check.
	src.Calls = 0
	stdout, _, err = run(t, "verify", dir, "--since", "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatalf("verify with a threshold before the capture: %v", err)
	}
	if !strings.Contains(stdout, "2 checked: 2 ok") {
		t.Errorf("the recent fixtures were not checked:\n%s", stdout)
	}
	if src.Calls == 0 {
		t.Error("the network was not contacted for the fixtures that were in range")
	}
}

// TestVerifySinceAcceptsADuration checks that a duration is counted back
// from now, which is how a run is narrowed to the last day's fixtures.
func TestVerifySinceAcceptsADuration(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	// The fixtures are made to look as if they were captured a week ago.
	for _, name := range []string{"envelope.golden.json", "result.golden.json"} {
		ageFixture(t, filepath.Join(dir, name), "2026-07-29T12:00:00Z")
	}
	src.Calls = 0

	// A window of two days leaves them out; a window of a month does not.
	stdout, _, err := run(t, "verify", dir, "--since", "48h")
	if err != nil {
		t.Fatalf("verify --since 48h: %v", err)
	}
	if !strings.Contains(stdout, "2 filtered") {
		t.Errorf("the fixtures outside the window were not filtered:\n%s", stdout)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for fixtures outside the window", src.Calls)
	}

	stdout, _, err = run(t, "verify", dir, "--since", "720h")
	if err != nil {
		t.Fatalf("verify --since 720h: %v", err)
	}
	if !strings.Contains(stdout, "2 checked: 2 ok") {
		t.Errorf("the fixtures inside the window were not checked:\n%s", stdout)
	}
}

// ageFixture rewrites a fixture's capture time, so a run can be narrowed to
// the fixtures captured after a threshold.
func ageFixture(t *testing.T, path, at string) {
	t.Helper()

	f, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	if f.Captured == nil {
		t.Fatalf("%s carries no provenance, so it has no capture time", path)
	}
	f.Captured.At = at
	if err := golden.Save(path, f); err != nil {
		t.Fatalf("saving %s: %v", path, err)
	}
}

// TestVerifyRejectsAnUnusableSince checks that a threshold that cannot be
// read is refused before any fixture is loaded.
func TestVerifyRejectsAnUnusableSince(t *testing.T) {
	withFake(t, sourceWithTx(t))

	_, _, err := run(t, "verify", t.TempDir(), "--since", "last tuesday")
	if err == nil {
		t.Fatal("a threshold that cannot be read was accepted")
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("the error does not name the setting at fault: %v", err)
	}
}

// TestVerifyFilterSelectsFixtures checks that --filter narrows a run by
// filename or by the metadata a fixture carries.
func TestVerifyFilterSelectsFixtures(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)
	src.Calls = 0

	// A glob picks one fixture out of the two.
	stdout, _, err := run(t, "verify", dir, "--filter", "envelope*")
	if err != nil {
		t.Fatalf("verify --filter envelope*: %v", err)
	}
	if !strings.Contains(stdout, "1 checked: 1 ok, 0 drifted, 0 skipped, 1 filtered") {
		t.Errorf("the summary does not describe a narrowed run:\n%s", stdout)
	}
	if src.Calls != 1 {
		t.Errorf("the network was contacted %d time(s), want 1", src.Calls)
	}

	// A metadata selector picks them all.
	src.Calls = 0
	stdout, _, err = run(t, "verify", dir, "--filter", "network:testnet")
	if err != nil {
		t.Fatalf("verify --filter network:testnet: %v", err)
	}
	if !strings.Contains(stdout, "2 checked: 2 ok") {
		t.Errorf("the metadata selector did not match the fixtures:\n%s", stdout)
	}

	// A metadata selector that matches nothing leaves nothing to check.
	src.Calls = 0
	stdout, _, err = run(t, "verify", dir, "--filter", "network:mainnet")
	if err != nil {
		t.Fatalf("verify --filter network:mainnet: %v", err)
	}
	if !strings.Contains(stdout, "2 filtered") {
		t.Errorf("the summary does not count the filtered fixtures:\n%s", stdout)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for fixtures that were filtered out", src.Calls)
	}
}

// TestVerifyDriftedOnlyRechecksWhatDrifted is the workflow the flag exists
// for: a run finds what drifted, the drift is fixed, and the next run only
// looks at the fixtures that drifted.
func TestVerifyDriftedOnlyRechecksWhatDrifted(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	// The chain moves: the envelope no longer matches what was captured.
	src.Transactions[testHash] = driftedRecords(t)

	if _, _, err := run(t, "verify", dir); err == nil {
		t.Fatal("verify reported success despite drift")
	}

	// The chain moves back, and the next run looks only at what drifted.
	src.Transactions[testHash] = sourceWithTx(t).Transactions[testHash]
	src.Calls = 0

	stdout, _, err := run(t, "verify", dir, "--drifted-only")
	if err != nil {
		t.Fatalf("verify --drifted-only: %v", err)
	}
	if !strings.Contains(stdout, "1 checked: 1 ok") {
		t.Errorf("the drifted fixture was not re-checked:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 skipped") {
		t.Errorf("the fixture that matched was not skipped:\n%s", stdout)
	}
	if src.Calls != 1 {
		t.Errorf("the network was contacted %d time(s), want 1", src.Calls)
	}

	// Now that everything matches, a further run leaves it all alone.
	src.Calls = 0
	stdout, _, err = run(t, "verify", dir, "--drifted-only")
	if err != nil {
		t.Fatalf("verify --drifted-only: %v", err)
	}
	if !strings.Contains(stdout, "2 skipped") {
		t.Errorf("the fixtures that matched were not skipped:\n%s", stdout)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for fixtures that had already matched", src.Calls)
	}
}

// TestVerifyDriftedOnlyRechecksAChangedFixture checks that a fixture that
// has changed since the last check is looked at again: the outcome cached
// for the old content says nothing about a fixture that has changed.
func TestVerifyDriftedOnlyRechecksAChangedFixture(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	if _, _, err := run(t, "verify", dir); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// The fixture changes on disk, keeping its provenance but not its
	// content.
	path := filepath.Join(dir, "envelope.golden.json")
	f, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	f.XDR = fixtureXDR(t, "tx_success.env.txt")
	if err := golden.Save(path, f); err != nil {
		t.Fatalf("saving %s: %v", path, err)
	}
	src.Calls = 0

	_, stderr, err := run(t, "verify", dir, "--drifted-only")
	if err == nil {
		t.Fatal("verify --drifted-only did not notice the changed fixture")
	}
	if src.Calls == 0 {
		t.Error("the changed fixture was not re-checked")
	}
	if !strings.Contains(stderr, "DRIFTED") {
		t.Errorf("the changed fixture was not reported as drifted:\n%s", stderr)
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
// moved: same hash, different envelope, same result.
func driftedRecords(t *testing.T) []capture.Record {
	t.Helper()
	return []capture.Record{
		{
			Name: "envelope", Type: "TransactionEnvelope",
			XDR: fixtureXDR(t, "tx_success.env.txt"), TxHash: testHash,
		},
		{
			Name: "result", Type: "TransactionResult",
			XDR: fixtureXDR(t, "tx_failed.res.txt"), TxHash: testHash,
		},
	}
}
