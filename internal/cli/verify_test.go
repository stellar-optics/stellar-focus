package cli

import (
	"encoding/json"
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

// TestVerifyJSONFormatCarriesPathLevelDiffs checks that a drifted entry
// carries the differences themselves — the field, the kind of change and the
// value on each side — and not only their count. That is what lets a CI job
// quote a path-level diff without parsing the rendered terminal output.
func TestVerifyJSONFormatCarriesPathLevelDiffs(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	// The chain has moved on: same hash, different content.
	src.Transactions[testHash] = driftedRecords(t)

	stdout, stderr, err := run(t, "verify", dir, "--format", "json")
	if err == nil {
		t.Fatal("verify reported success despite drift")
	}
	if code, _ := ExitCode(err); code != ExitDrift {
		t.Errorf("exit code %d, want %d", code, ExitDrift)
	}
	if stderr != "" {
		t.Errorf("the JSON run wrote to stderr:\n%s", stderr)
	}

	var report VerifyReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}

	var drifted *VerifyItemResult
	for i := range report.Results {
		if report.Results[i].Status == statusDrifted {
			drifted = &report.Results[i]
		}
	}
	if drifted == nil {
		t.Fatalf("no drifted fixture in the report:\n%s", stdout)
	}
	if len(drifted.Diffs) != drifted.DiffCount {
		t.Fatalf("drifted fixture reports %d differences and %d diff entries, want the same non-zero count:\n%s",
			drifted.DiffCount, len(drifted.Diffs), stdout)
	}

	paths := make(map[string]bool, len(drifted.DiffPaths))
	for _, path := range drifted.DiffPaths {
		paths[path] = true
	}

	var withValues int
	for _, diff := range drifted.Diffs {
		if diff.Path == "" {
			t.Errorf("a diff entry does not name its field:\n%s", stdout)
		}
		if !paths[diff.Path] {
			t.Errorf("diff for %q is not in diff_paths %v", diff.Path, drifted.DiffPaths)
		}
		switch diff.Op {
		case "modified", "added", "removed", "type_changed":
		default:
			t.Errorf("diff for %q has unknown op %q", diff.Path, diff.Op)
		}
		if diff.Before != nil || diff.After != nil {
			withValues++
		}
	}
	if withValues == 0 {
		t.Errorf("no diff entry carries a value on either side:\n%s", stdout)
	}

	// The wire format is part of the contract, so check the keys a script
	// reads rather than only the struct that produced them.
	var raw struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	var found bool
	for _, result := range raw.Results {
		if string(result["status"]) != `"`+statusDrifted+`"` {
			continue
		}
		found = true
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(result["diffs"], &entries); err != nil {
			t.Fatalf("diffs is not an array of objects: %v\n%s", err, stdout)
		}
		for _, entry := range entries {
			for _, key := range []string{"path", "op", "before", "after"} {
				if _, ok := entry[key]; !ok {
					t.Errorf("a diff entry has no %q key:\n%s", key, stdout)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no drifted result on the wire:\n%s", stdout)
	}
}

// TestVerifyConcurrencyKeepsOutputOrder is the promise --concurrency makes:
// checking several fixtures at once changes how long a run takes, not what
// it prints. The same fixtures with one worker and with four must produce
// byte-identical output, in fixture order, on both streams.
func TestVerifyConcurrencyKeepsOutputOrder(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	names := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	for _, name := range names {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("creating %s: %v", sub, err)
		}
		captureIntoDir(t, sub)
	}

	// Two fixtures drift, so the run mixes statuses and writes to both
	// streams. A parallel run that reported in completion order would show
	// up here as output that no longer matches the serial run.
	for _, name := range []string{"bravo", "echo"} {
		path := filepath.Join(dir, name, "envelope.golden.json")
		f, err := golden.Load(path)
		if err != nil {
			t.Fatalf("loading %s: %v", path, err)
		}
		f.XDR = fixtureXDR(t, "tx_success.env.txt")
		if err := golden.Save(path, f); err != nil {
			t.Fatalf("saving %s: %v", path, err)
		}
	}

	serialOut, serialErr, serialRunErr := run(t, "verify", dir, "--concurrency", "1")
	parallelOut, parallelErr, parallelRunErr := run(t, "verify", dir, "--concurrency", "4")

	for name, err := range map[string]error{"serial": serialRunErr, "parallel": parallelRunErr} {
		if err == nil {
			t.Fatalf("%s run reported success despite two drifted fixtures", name)
		}
		if code, _ := ExitCode(err); code != ExitDrift {
			t.Errorf("%s run exited %d, want %d", name, code, ExitDrift)
		}
	}
	if serialOut != parallelOut {
		t.Errorf("stdout depends on the worker count:\nserial:\n%s\nparallel:\n%s",
			serialOut, parallelOut)
	}
	if serialErr != parallelErr {
		t.Errorf("stderr depends on the worker count:\nserial:\n%s\nparallel:\n%s",
			serialErr, parallelErr)
	}
	if !strings.Contains(parallelOut, "12 checked: 10 ok, 2 drifted") {
		t.Errorf("summary does not describe the run:\n%s", parallelOut)
	}

	// Progress is in fixture order, not in whichever check finished
	// first. WalkDir is lexical, so the directories are already in the
	// order the report must use.
	last := -1
	for _, name := range names {
		if name == "bravo" || name == "echo" {
			continue
		}
		idx := strings.Index(parallelOut, filepath.Join(dir, name, "envelope.golden.json"))
		if idx < 0 {
			t.Errorf("%s was not reported:\n%s", name, parallelOut)
			continue
		}
		if idx < last {
			t.Errorf("%s is reported out of fixture order:\n%s", name, parallelOut)
		}
		last = idx
	}
}

// TestVerifyConcurrencyFlagShorthand checks the -j spelling, which is what a
// person types when they want a faster run.
func TestVerifyConcurrencyFlagShorthand(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	stdout, stderr, err := run(t, "verify", dir, "-j", "2")
	if err != nil {
		t.Fatalf("verify -j 2: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "2 checked: 2 ok") {
		t.Errorf("summary line missing:\n%s", stdout)
	}
}

// TestVerifyRejectsABadConcurrency checks that a worker count that cannot
// run is refused before the network is contacted.
func TestVerifyRejectsABadConcurrency(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)
	src.Calls = 0

	_, _, err := run(t, "verify", dir, "--concurrency", "0")
	if err == nil {
		t.Fatal("a concurrency of zero was accepted")
	}
	if !strings.Contains(err.Error(), "concurrency") {
		t.Errorf("error %q does not name the setting at fault", err)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for an invalid concurrency", src.Calls)
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

// TestVerifyWithConcurrency checks that --concurrency parallelizes the check
// without changing the result: every concurrency prints the same lines in
// the same order, and all fixtures are still checked.
func TestVerifyWithConcurrency(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir); err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	var previous string
	for _, concurrency := range []string{"1", "2", "4"} {
		stdout, stderr, err := run(t, "verify", dir, "--concurrency", concurrency)
		if err != nil {
			t.Fatalf("verify --concurrency %s failed: %v\nstderr: %s", concurrency, err, stderr)
		}
		if previous != "" && stdout != previous {
			t.Errorf("verify output changed with concurrency %s:\nbefore:\n%s\nafter:\n%s",
				concurrency, previous, stdout)
		}
		previous = stdout
	}
	if !strings.Contains(previous, "2 checked: 2 ok, 0 drifted, 0 skipped") {
		t.Errorf("unexpected summary:\n%s", previous)
	}
}

// TestVerifyRejectsZeroConcurrency checks that --concurrency 0 is refused
// before any fixture is checked.
func TestVerifyRejectsZeroConcurrency(t *testing.T) {
	withFake(t, sourceWithTx(t))

	_, _, err := run(t, "verify", t.TempDir(), "--concurrency", "0")
	if err == nil {
		t.Fatal("verify accepted --concurrency 0")
	}
	if !strings.Contains(err.Error(), "concurrency must be at least 1") {
		t.Errorf("error does not name the limit: %v", err)
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
