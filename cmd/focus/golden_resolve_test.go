package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/cli"
	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// testEnvelope returns a real captured envelope, shared with the sibling
// repositories. Two of them differ, which is all a conflict needs.
func testEnvelope(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "pkg", "fuzz", "testdata", name)
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("..", "..", "testdata", name)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}

func saveFixture(t *testing.T, dir, name, xdr string) {
	t.Helper()
	fixture, err := golden.Build(xdr, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	path := filepath.Join(dir, name)
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// conflictDirs sets up one golden file and a candidate proposing a
// different envelope under the same name.
func conflictDirs(t *testing.T) (goldenDir, candidateDir, stored, proposed string) {
	t.Helper()
	const name = "env.golden.json"
	goldenDir = t.TempDir()
	candidateDir = t.TempDir()
	stored = testEnvelope(t, "tx_failed.env.txt")
	proposed = testEnvelope(t, "tx_success.env.txt")
	saveFixture(t, goldenDir, name, stored)
	saveFixture(t, candidateDir, name, proposed)
	return goldenDir, candidateDir, stored, proposed
}

func resolve(t *testing.T, in, candidate string, paths ...string) (out, errOut string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	err = resolveGoldenUpdates(resolveRequest{
		candidate: candidate,
		paths:     paths,
		color:     "never",
		in:        strings.NewReader(in),
		out:       &outBuf,
		errOut:    &errBuf,
	})
	return outBuf.String(), errBuf.String(), err
}

// runGoldenCmd executes the command tree exactly as the CLI does, with stdin
// supplied, so flag plumbing and stream wiring are covered too.
func runGoldenCmd(t *testing.T, stdin string, args ...string) (out, errOut string, err error) {
	t.Helper()
	cmd := newGoldenCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestGoldenCommandIsRegisteredWithExecute is the wiring: the command exists
// at the root the CLI actually runs, not only in isolation.
func TestGoldenCommandIsRegisteredWithExecute(t *testing.T) {
	t.Parallel()

	var out, errBuf bytes.Buffer
	if err := cli.Execute([]string{"golden", "--help"}, &out, &errBuf, newGoldenCmd()); err != nil {
		t.Fatalf("Execute(golden --help) error = %v", err)
	}
	if !strings.Contains(out.String(), "resolve") {
		t.Errorf("golden help does not mention resolve:\n%s", out.String())
	}

	out.Reset()
	errBuf.Reset()
	if err := cli.Execute([]string{"--help"}, &out, &errBuf, newGoldenCmd()); err != nil {
		t.Fatalf("Execute(--help) error = %v", err)
	}
	if !strings.Contains(out.String(), "golden") {
		t.Errorf("root help does not list the golden command:\n%s", out.String())
	}
}

func TestResolveRequiresCandidateFlag(t *testing.T) {
	t.Parallel()

	_, _, err := runGoldenCmd(t, "", "resolve")
	if err == nil {
		t.Fatal("resolve without --candidate succeeded")
	}
	if !strings.Contains(err.Error(), "--candidate") {
		t.Errorf("error does not name the missing flag: %v", err)
	}
}

// TestResolvePromptsShowsDiffAndAccepts is the core of the issue: a visible,
// field-level diff and an accepted proposal replacing the golden file.
func TestResolvePromptsShowsDiffAndAccepts(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir, _, proposed := conflictDirs(t)

	out, errOut, err := resolve(t, "y\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}

	target := filepath.Join(goldenDir, "env.golden.json")
	loaded, err := golden.Load(target)
	if err != nil {
		t.Fatalf("Load() after accept: %v", err)
	}
	if loaded.XDR != proposed {
		t.Error("accepting the proposal did not update the golden file")
	}

	for _, want := range []string{
		target, "TransactionEnvelope", "~ ",
		"accepted " + target,
		"1 proposed: 1 accepted, 0 rejected, 0 unchanged, 0 skipped, 0 unresolved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "accept?") == false {
		t.Errorf("no prompt was shown before accepting:\n%s", out)
	}
	if errOut != "" {
		t.Errorf("unexpected stderr:\n%s", errOut)
	}
}

// TestResolveRejectKeepsGoldenAndAssertionsAccurate carries the faketb
// acceptance criterion: after a rejection the stored fixture still passes,
// the proposal still fails, and the failure still names the changed field
// and the remedy.
func TestResolveRejectKeepsGoldenAndAssertionsAccurate(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir, stored, proposed := conflictDirs(t)

	out, errOut, err := resolve(t, "n\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "kept "+filepath.Join(goldenDir, "env.golden.json")) {
		t.Errorf("output does not record the rejection:\n%s", out)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(goldenDir))
	if !g.Assert("env", stored) {
		t.Errorf("rejecting left the stored fixture failing:\n%s", tb.Output())
	}

	tb = faketb.New()
	g = golden.New(tb, golden.WithDir(goldenDir))
	if g.Assert("env", proposed) {
		t.Fatal("rejecting still passes an assertion against the proposal")
	}
	out = tb.Output()
	for _, want := range []string{"~ Type", "EnvelopeTypeEnvelopeTypeTx", "is out of date", "-focus.update"} {
		if !strings.Contains(out, want) {
			t.Errorf("assertion failure does not contain %q:\n%s", want, out)
		}
	}
}

// TestResolveAcceptAllPromptsOnce accepts every remaining conflict after a
// single "a", without asking again.
func TestResolveAcceptAllPromptsOnce(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir := t.TempDir(), t.TempDir()
	stored, proposed := testEnvelope(t, "tx_failed.env.txt"), testEnvelope(t, "tx_success.env.txt")
	for _, name := range []string{"a.golden.json", "b.golden.json"} {
		saveFixture(t, goldenDir, name, stored)
		saveFixture(t, candidateDir, name, proposed)
	}

	out, errOut, err := resolve(t, "a\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if prompts := strings.Count(out, "accept?"); prompts != 1 {
		t.Errorf("accept-all still prompted %d times:\n%s", prompts, out)
	}
	if !strings.Contains(out, "2 proposed: 2 accepted, 0 rejected, 0 unchanged, 0 skipped, 0 unresolved") {
		t.Errorf("summary does not report both proposals accepted:\n%s", out)
	}
	for _, name := range []string{"a.golden.json", "b.golden.json"} {
		loaded, err := golden.Load(filepath.Join(goldenDir, name))
		if err != nil {
			t.Fatalf("Load(%s): %v", name, err)
		}
		if loaded.XDR != proposed {
			t.Errorf("%s was not updated by accept-all", name)
		}
	}
}

// TestResolveQuitLeavesTheRestUnresolved: quitting is a decision to stop,
// not a decision for the remaining conflicts.
func TestResolveQuitLeavesTheRestUnresolved(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir := t.TempDir(), t.TempDir()
	stored, proposed := testEnvelope(t, "tx_failed.env.txt"), testEnvelope(t, "tx_success.env.txt")
	for _, name := range []string{"a.golden.json", "b.golden.json"} {
		saveFixture(t, goldenDir, name, stored)
		saveFixture(t, candidateDir, name, proposed)
	}

	out, errOut, err := resolve(t, "q\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "left unresolved") ||
		!strings.Contains(out, "0 accepted, 0 rejected, 0 unchanged, 0 skipped, 2 unresolved") {
		t.Errorf("summary does not report both conflicts unresolved:\n%s", out)
	}
	loaded, err := golden.Load(filepath.Join(goldenDir, "a.golden.json"))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.XDR != stored {
		t.Error("quitting modified a golden file")
	}
}

// TestResolveRepromptsOnAnUnrecognisedAnswer keeps a mistyped key from
// being taken for a decision.
func TestResolveRepromptsOnAnUnrecognisedAnswer(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir, _, _ := conflictDirs(t)

	out, errOut, err := resolve(t, "huh\ny\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "please answer y, n, a or q") {
		t.Errorf("unrecognised answer was not challenged:\n%s", out)
	}
	if prompts := strings.Count(out, "accept?"); prompts != 2 {
		t.Errorf("prompt shown %d times, want 2:\n%s", prompts, out)
	}
	if !strings.Contains(out, "1 accepted") {
		t.Errorf("the recognised answer was not applied:\n%s", out)
	}
}

// TestResolveStopsWhenInputCloses: a caller that supplies no answers gets a
// stopped run, not a silently half-resolved directory.
func TestResolveStopsWhenInputCloses(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir, stored, _ := conflictDirs(t)

	out, errOut, err := resolve(t, "", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "input closed; stopping") ||
		!strings.Contains(out, "1 unresolved") {
		t.Errorf("closed input did not stop the run cleanly:\n%s", out)
	}
	loaded, err := golden.Load(filepath.Join(goldenDir, "env.golden.json"))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.XDR != stored {
		t.Error("a closed input still modified the golden file")
	}
}

// TestResolveTreatsIdenticalCandidateAsUnchanged covers the no-conflict
// path: nothing is asked and nothing is written.
func TestResolveTreatsIdenticalCandidateAsUnchanged(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir, stored, _ := conflictDirs(t)
	// Replace the proposal with a byte-identical copy of the golden file.
	raw, err := os.ReadFile(filepath.Join(goldenDir, "env.golden.json"))
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "env.golden.json"), raw, 0o600); err != nil {
		t.Fatalf("writing candidate: %v", err)
	}

	out, errOut, err := resolve(t, "", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if strings.Contains(out, "accept?") {
		t.Errorf("an identical candidate prompted for a decision:\n%s", out)
	}
	if !strings.Contains(out, "0 accepted, 0 rejected, 1 unchanged, 0 skipped, 0 unresolved") {
		t.Errorf("summary does not count the identical candidate:\n%s", out)
	}
	_ = stored
}

// TestResolveTreatsCRLFAsUnchanged is the cross-platform promise from the
// resolver's side: a CRLF copy of a fixture is the same fixture, not a
// conflict.
func TestResolveTreatsCRLFAsUnchanged(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir, _, _ := conflictDirs(t)
	path := filepath.Join(goldenDir, "env.golden.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	candidate := filepath.Join(candidateDir, "env.golden.json")
	if err := os.WriteFile(candidate,
		[]byte(strings.ReplaceAll(string(raw), "\n", "\r\n")), 0o600); err != nil {
		t.Fatalf("writing candidate: %v", err)
	}

	out, errOut, err := resolve(t, "", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "1 unchanged") {
		t.Errorf("a CRLF copy was reported as a conflict:\n%s", out)
	}
	if strings.Contains(out, "accept?") {
		t.Errorf("a CRLF copy prompted for a decision:\n%s", out)
	}
}

// TestResolveSkipsCandidateWithNoGoldenFile keeps a stray proposal from
// inventing a new fixture file.
func TestResolveSkipsCandidateWithNoGoldenFile(t *testing.T) {
	t.Parallel()

	goldenDir := t.TempDir()
	candidateDir := t.TempDir()
	saveFixture(t, goldenDir, "kept.golden.json", testEnvelope(t, "tx_failed.env.txt"))
	saveFixture(t, candidateDir, "ghost.golden.json", testEnvelope(t, "tx_success.env.txt"))

	out, errOut, err := resolve(t, "", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "no golden file to update; skipped") ||
		!strings.Contains(out, "1 skipped") {
		t.Errorf("orphan proposal was not reported as skipped:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(goldenDir, "ghost.golden.json")); statErr == nil {
		t.Error("a skipped proposal still created a golden file")
	}
	_ = errOut
}

// TestResolveReportsAnUnreadableCandidate: a proposal that is not a fixture
// must never be written anywhere, and the run must fail loudly.
func TestResolveReportsAnUnreadableCandidate(t *testing.T) {
	t.Parallel()

	goldenDir := t.TempDir()
	candidateDir := t.TempDir()
	stored := testEnvelope(t, "tx_failed.env.txt")
	saveFixture(t, goldenDir, "env.golden.json", stored)
	if err := os.WriteFile(filepath.Join(candidateDir, "env.golden.json"),
		[]byte("this is not a fixture"), 0o600); err != nil {
		t.Fatalf("writing candidate: %v", err)
	}

	_, errOut, err := resolve(t, "", candidateDir, goldenDir)
	if err == nil {
		t.Fatal("an unreadable proposal did not fail the run")
	}
	if !strings.Contains(err.Error(), "could not be processed") {
		t.Errorf("error does not count the failure: %v", err)
	}
	if !strings.Contains(errOut, "not a golden fixture") {
		t.Errorf("stderr does not explain the rejection:\n%s", errOut)
	}
	loaded, loadErr := golden.Load(filepath.Join(goldenDir, "env.golden.json"))
	if loadErr != nil {
		t.Fatalf("Load(): %v", loadErr)
	}
	if loaded.XDR != stored {
		t.Error("an unreadable proposal modified the golden file")
	}
}

// TestResolveMetadataOnlyDifferenceSaysSo: an XDR-identical proposal is
// still a real update, but the diff must say that nothing in the payload
// moved rather than printing an empty one.
func TestResolveMetadataOnlyDifferenceSaysSo(t *testing.T) {
	t.Parallel()

	goldenDir := t.TempDir()
	candidateDir := t.TempDir()
	xdr := testEnvelope(t, "tx_failed.env.txt")

	stored, err := golden.Build(xdr, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(goldenDir, "env.golden.json"), stored); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	proposed, err := golden.Build(xdr, "TransactionEnvelope", "renamed by hand", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(candidateDir, "env.golden.json"), proposed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	out, errOut, err := resolve(t, "y\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "payload unchanged; only metadata or formatting differs") {
		t.Errorf("metadata-only difference was not explained:\n%s", out)
	}
	if !strings.Contains(out, "1 accepted") {
		t.Errorf("the metadata-only update was not accepted:\n%s", out)
	}
}

// TestResolveAcceptsCompressedProposalForPlainGolden covers the
// interoperability between the two halves of this work: a .gz proposal
// updates the plain fixture, stored plain.
func TestResolveAcceptsCompressedProposalForPlainGolden(t *testing.T) {
	t.Parallel()

	goldenDir := t.TempDir()
	candidateDir := t.TempDir()
	stored := testEnvelope(t, "tx_failed.env.txt")
	proposed := testEnvelope(t, "tx_success.env.txt")
	saveFixture(t, goldenDir, "env.golden.json", stored)
	saveFixture(t, candidateDir, "env.golden.json.gz", proposed)

	out, errOut, err := resolve(t, "y\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "1 accepted") {
		t.Errorf("compressed proposal was not accepted:\n%s", out)
	}

	target := filepath.Join(goldenDir, "env.golden.json")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	if bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
		t.Error("accepting a compressed proposal compressed the plain golden file")
	}
	loaded, err := golden.Load(target)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.XDR != proposed {
		t.Error("the proposal did not land in the plain golden file")
	}
}

// TestResolveKeepsCompressedGoldenCompressed: accepting into a repository
// that stores fixtures gzipped must not decompress them behind the user's
// back.
func TestResolveKeepsCompressedGoldenCompressed(t *testing.T) {
	t.Parallel()

	goldenDir := t.TempDir()
	candidateDir := t.TempDir()
	stored := testEnvelope(t, "tx_failed.env.txt")
	proposed := testEnvelope(t, "tx_success.env.txt")
	saveFixture(t, goldenDir, "env.golden.json.gz", stored)
	saveFixture(t, candidateDir, "env.golden.json", proposed)

	out, errOut, err := resolve(t, "y\n", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("resolveGoldenUpdates() error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "1 accepted") {
		t.Errorf("proposal was not accepted:\n%s", out)
	}

	target := filepath.Join(goldenDir, "env.golden.json.gz")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	if !bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
		t.Error("accepting into a compressed repository wrote plain bytes")
	}
	loaded, err := golden.Load(target)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.XDR != proposed {
		t.Error("the proposal did not land in the compressed golden file")
	}
}

// TestResolveReportsEmptyInputs keeps both sides of the resolution from
// silently matching nothing.
func TestResolveReportsEmptyInputs(t *testing.T) {
	t.Parallel()

	emptyCandidate := t.TempDir()
	if _, _, err := resolve(t, "", emptyCandidate, t.TempDir()); err == nil {
		t.Error("an empty candidate directory did not fail")
	}

	_, candidateDir, _, _ := conflictDirs(t)
	if _, _, err := resolve(t, "", candidateDir, t.TempDir()); err == nil {
		t.Error("golden paths without fixtures did not fail")
	}
	if _, _, err := resolve(t, "", filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing candidate did not fail")
	}
}

// TestResolveRunsThroughTheCommandTree exercises the cobra wiring — flags,
// stdin, output streams — end to end on a real conflict.
func TestResolveRunsThroughTheCommandTree(t *testing.T) {
	t.Parallel()

	goldenDir, candidateDir, _, proposed := conflictDirs(t)

	out, errOut, err := runGoldenCmd(t, "y\n",
		"resolve", "--candidate", candidateDir, goldenDir)
	if err != nil {
		t.Fatalf("command error = %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "1 accepted") {
		t.Errorf("command output does not report the acceptance:\n%s", out)
	}
	loaded, err := golden.Load(filepath.Join(goldenDir, "env.golden.json"))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.XDR != proposed {
		t.Error("the command did not apply the acceptance")
	}
}
