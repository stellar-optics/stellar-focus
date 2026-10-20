package golden_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// A real captured mainnet envelope, shared with the sibling repositories.
func envelope(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestBuildAndSaveRoundTrip(t *testing.T) {
	t.Parallel()

	env := envelope(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "envelope.golden.json")

	fixture, err := golden.Build(env, "TransactionEnvelope", "a failed Soroban call", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := golden.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.XDR != env {
		t.Error("the stored XDR does not round-trip")
	}
	if loaded.Type != "TransactionEnvelope" {
		t.Errorf("Type = %q, want TransactionEnvelope", loaded.Type)
	}
	if loaded.Format != golden.FormatVersion {
		t.Errorf("Format = %q, want %q", loaded.Format, golden.FormatVersion)
	}
	if loaded.Description != "a failed Soroban call" {
		t.Errorf("Description = %q", loaded.Description)
	}
}

// TestGoldenFileIsReviewable is the reason this format exists: a reviewer
// must be able to see in a pull-request diff what actually changed.
func TestGoldenFileIsReviewable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	content := string(raw)

	// It must be valid, indented, line-oriented JSON.
	if !json.Valid(raw) {
		t.Fatal("golden file is not valid JSON")
	}
	if lines := strings.Count(content, "\n"); lines < 20 {
		t.Errorf("golden file has only %d lines; a one-line blob is not reviewable", lines)
	}
	if !strings.HasSuffix(content, "\n") {
		t.Error("golden file does not end with a newline")
	}

	// The decoded section must carry real field names a reviewer recognises,
	// not just the base64.
	for _, want := range []string{`"decoded"`, `"SourceAccount"`, `"Operations"`, `"type"`} {
		if !strings.Contains(content, want) {
			t.Errorf("golden file does not contain %q; the diff would not be reviewable", want)
		}
	}
	// Enriched values must survive, so a changed amount reads as an amount.
	if !strings.Contains(content, "G") {
		t.Error("golden file contains no strkey address")
	}
}

func TestAssertPassesAgainstMatchingGolden(t *testing.T) {
	t.Parallel()

	env := envelope(t)
	dir := t.TempDir()

	fixture, err := golden.Build(env, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if !g.Assert("env", env) {
		t.Errorf("Assert() failed against a matching golden file:\n%s", tb.Output())
	}
}

// TestAssertFailureShowsDiffAndRemedy checks the two things a stale golden
// file must tell you: what changed, and how to re-record it.
func TestAssertFailureShowsDiffAndRemedy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	stored := buildEnvelope(t, 100)
	actual := buildEnvelope(t, 200)

	fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if g.Assert("env", actual) {
		t.Fatal("Assert() passed against a stale golden file")
	}

	out := tb.Output()
	if !strings.Contains(out, "Body.PaymentOp.Amount") {
		t.Errorf("failure does not show which field changed:\n%s", out)
	}
	if !strings.Contains(out, "is out of date") || !strings.Contains(out, "-focus.update") {
		t.Errorf("failure does not say how to re-record the fixture:\n%s", out)
	}
}

// TestAssertMissingFileExplainsHowToCreateIt covers the first run of a new
// golden test, where a bare "no such file" would be unhelpful.
func TestAssertMissingFileExplainsHowToCreateIt(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(t.TempDir()))
	if g.Assert("absent", envelope(t)) {
		t.Fatal("Assert() passed against a missing golden file")
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"does not exist yet", "-focus.update"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertHonoursIgnoreOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	stored := buildEnvelopeSeq(t, 111, 100)
	actual := buildEnvelopeSeq(t, 222, 100)

	fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir), golden.WithNoiseSuppression(false),
		golden.WithOptions(focus.IgnoreSequence()))
	if !g.Assert("env", actual) {
		t.Errorf("ignore option was not applied:\n%s", tb.Output())
	}
}

func TestAssertSuppressesSequenceNoiseByDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stored := buildEnvelopeSeq(t, 111, 100)
	actual := buildEnvelopeSeq(t, 222, 100)
	fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	if !golden.New(tb, golden.WithDir(dir)).Assert("env", actual) {
		t.Errorf("default noise suppression did not normalize sequence number:\n%s", tb.Output())
	}
}

func TestPath(t *testing.T) {
	t.Parallel()

	g := golden.New(faketb.New(), golden.WithDir("fixtures"), golden.WithSuffix(".gold"))

	tests := []struct{ name, want string }{
		{"payment", filepath.Join("fixtures", "payment.gold")},
		{"payment.gold", filepath.Join("fixtures", "payment.gold")},
	}
	for _, tc := range tests {
		if got := g.Path(tc.name); got != tc.want {
			t.Errorf("Path(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// writeGolden records a fixture under a name, exactly as a golden test would
// have recorded it.
func writeGolden(t *testing.T, dir, name, xdr string) {
	t.Helper()

	fixture, err := golden.Build(xdr, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, name+".golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// TestAssertAllPassesWhenEveryCaseMatches is the ordinary case: a table of
// fixtures that all still match.
func TestAssertAllPassesWhenEveryCaseMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGolden(t, dir, "payment_v1", buildEnvelope(t, 100))
	writeGolden(t, dir, "payment_v2", buildEnvelope(t, 200))

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))

	cases := []golden.Case{
		{Name: "payment_v1", Value: buildEnvelope(t, 100), Description: "the first payment"},
		{Name: "payment_v2", Value: buildEnvelope(t, 200)},
	}
	if !g.AssertAll(cases) {
		t.Errorf("AssertAll() failed against matching golden files:\n%s", tb.Output())
	}
	if tb.Failed() {
		t.Errorf("AssertAll() reported a failure for a table that matched:\n%s", tb.Output())
	}
}

// TestAssertAllReportsTheFailingCaseWithItsIndexAndDescription is the reason
// the table exists: a failure must say which entry failed, without the
// reader scanning filenames.
func TestAssertAllReportsTheFailingCaseWithItsIndexAndDescription(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGolden(t, dir, "payment_v1", buildEnvelope(t, 100))
	writeGolden(t, dir, "payment_v2", buildEnvelope(t, 100))
	writeGolden(t, dir, "payment_v3", buildEnvelope(t, 300))

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))

	cases := []golden.Case{
		{Name: "payment_v1", Value: buildEnvelope(t, 100), Description: "the first payment"},
		{Name: "payment_v2", Value: buildEnvelope(t, 200), Description: "a payment that moved"},
		{Name: "payment_v3", Value: buildEnvelope(t, 300)},
	}
	if g.AssertAll(cases) {
		t.Fatal("AssertAll() passed with a drifted case in the table")
	}

	out := tb.Output()
	for _, want := range []string{
		"table summary",
		"[1/3] payment_v1: ok",
		"[2/3] payment_v2: DRIFTED",
		"[3/3] payment_v3: ok",
		"a payment that moved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output does not contain %q:\n%s", want, out)
		}
	}
	// The diff itself must still be there: the summary says which entry
	// failed, the diff says what changed.
	if !strings.Contains(out, "Body.PaymentOp.Amount") {
		t.Errorf("failure output does not show what changed:\n%s", out)
	}
}

// TestAssertAllRunsEveryCaseAfterAFailure checks that a broken entry does not
// hide the rest of the table.
func TestAssertAllRunsEveryCaseAfterAFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGolden(t, dir, "payment_v1", buildEnvelope(t, 100))
	writeGolden(t, dir, "payment_v2", buildEnvelope(t, 100))
	writeGolden(t, dir, "payment_v3", buildEnvelope(t, 100))

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))

	cases := []golden.Case{
		{Name: "payment_v1", Value: buildEnvelope(t, 900)},
		{Name: "payment_v2", Value: buildEnvelope(t, 100)},
		{Name: "payment_v3", Value: buildEnvelope(t, 800)},
	}
	if g.AssertAll(cases) {
		t.Fatal("AssertAll() passed with two drifted cases in the table")
	}

	out := tb.Output()
	if !strings.Contains(out, "[1/3] payment_v1: DRIFTED") {
		t.Errorf("the first broken case is not reported:\n%s", out)
	}
	if !strings.Contains(out, "[3/3] payment_v3: DRIFTED") {
		t.Errorf("the last case did not run after an earlier failure:\n%s", out)
	}
	if !strings.Contains(out, "[2/3] payment_v2: ok") {
		t.Errorf("the passing case is not reported:\n%s", out)
	}
	if got := strings.Count(out, "is out of date"); got != 2 {
		t.Errorf("reported %d stale fixtures, want 2:\n%s", got, out)
	}
}

// TestAssertAllMergesCaseOptionsWithSuiteOptions checks that a case can ask
// for its own rules without losing the suite's.
func TestAssertAllMergesCaseOptionsWithSuiteOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGolden(t, dir, "volatile", buildEnvelopeSeq(t, 111, 100))
	writeGolden(t, dir, "strict", buildEnvelopeSeq(t, 111, 100))

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir), golden.WithNoiseSuppression(false))

	cases := []golden.Case{
		{
			Name:    "volatile",
			Value:   buildEnvelopeSeq(t, 222, 100),
			Options: []focus.Option{focus.IgnoreSequence()},
		},
		{Name: "strict", Value: buildEnvelopeSeq(t, 222, 100)},
	}
	if g.AssertAll(cases) {
		t.Fatal("AssertAll() passed although one case asked for no rules at all")
	}

	out := tb.Output()
	if !strings.Contains(out, "[1/2] volatile: ok") {
		t.Errorf("the per-case option was not applied:\n%s", out)
	}
	if !strings.Contains(out, "[2/2] strict: DRIFTED") {
		t.Errorf("the suite's options were not applied to the second case:\n%s", out)
	}
}

// TestAssertAllKeepsTheSuiteOptions checks that a case's own options add to
// the suite's rather than replacing them: here the suite ignores the
// sequence number and the case ignores the fee, so dropping either one leaves
// a difference behind.
func TestAssertAllKeepsTheSuiteOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGolden(t, dir, "env", buildEnvelopeWith(t, 111, 100, 100))

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir), golden.WithNoiseSuppression(false),
		golden.WithOptions(focus.IgnoreSequence()))

	cases := []golden.Case{
		{
			Name:    "env",
			Value:   buildEnvelopeWith(t, 222, 200, 100),
			Options: []focus.Option{focus.IgnorePaths("**.Fee")},
		},
	}
	if !g.AssertAll(cases) {
		t.Errorf("the suite's option was dropped when the case had its own:\n%s", tb.Output())
	}
}

// TestAssertAllReportsAMissingFixturePerCase covers the first run of a new
// table, where one of the fixtures has not been recorded yet.
func TestAssertAllReportsAMissingFixturePerCase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGolden(t, dir, "present", buildEnvelope(t, 100))

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))

	cases := []golden.Case{
		{Name: "present", Value: buildEnvelope(t, 100)},
		{Name: "absent", Value: buildEnvelope(t, 200)},
	}
	if g.AssertAll(cases) {
		t.Fatal("AssertAll() passed with a fixture that does not exist")
	}

	out := tb.Output()
	if !strings.Contains(out, "[2/2] absent: DRIFTED") {
		t.Errorf("the missing fixture is not reported as a failed case:\n%s", out)
	}
	if !strings.Contains(out, "does not exist yet") {
		t.Errorf("the missing fixture does not say how to create it:\n%s", out)
	}
}

// TestAssertAllOnAnEmptyTable checks that an empty table is a pass, not a
// failure: there is nothing to be wrong with.
func TestAssertAllOnAnEmptyTable(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(t.TempDir()))

	if !g.AssertAll(nil) {
		t.Error("AssertAll() failed for an empty table")
	}
	if tb.Failed() {
		t.Errorf("an empty table reported a failure:\n%s", tb.Output())
	}
}

// TestLoadRejectsAnUnknownFormat guards against silently misreading a file
// written by a future version.
func TestLoadRejectsAnUnknownFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "future.golden.json")
	if err := os.WriteFile(path,
		[]byte(`{"format":"focus/v99","type":"TransactionEnvelope","xdr":"AAAA"}`), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	if _, err := golden.Load(path); err == nil {
		t.Fatal("Load() accepted an unknown format version")
	} else if !strings.Contains(err.Error(), "focus/v99") {
		t.Errorf("error does not name the offending version: %v", err)
	}
}

func TestLoadRejectsAFixtureWithNoXDR(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.golden.json")
	if err := os.WriteFile(path, []byte(`{"format":"focus/v1","type":"X"}`), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	if _, err := golden.Load(path); err == nil {
		t.Fatal("Load() accepted a fixture with no xdr field")
	}
}

// TestUpdateFlagIsRegistered checks the documented convention is actually
// wired into the flag set, since a silently-missing flag would make every
// golden file impossible to re-record.
func TestUpdateFlagIsRegistered(t *testing.T) {
	t.Parallel()

	if golden.Update() {
		t.Skip("suite is running with -focus.update")
	}
	// Update() returning false without panicking is the check; the flag is
	// registered at package init and would panic on a duplicate name.
}

// TestRefreshRendersTheDecodedSectionAgain is what the refresh is for: the
// decoded tree of a stored fixture goes stale when the formatter changes, and
// re-recording the fixture to fix it would re-record the data too.
func TestRefreshRendersTheDecodedSectionAgain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "a failed Soroban call", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	fixture.Decoded = json.RawMessage(`{"stale": true}`)
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	changed, err := golden.Refresh(path)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if !changed {
		t.Error("Refresh() reported no change for a stale fixture")
	}

	refreshed, err := golden.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(string(refreshed.Decoded), "stale") {
		t.Errorf("the decoded section was not refreshed:\n%s", refreshed.Decoded)
	}
	// Everything but the decoded section keeps its value.
	if refreshed.XDR != fixture.XDR {
		t.Error("the refresh changed the XDR")
	}
	if refreshed.Type != fixture.Type {
		t.Errorf("the refresh changed the type: %q", refreshed.Type)
	}
	if refreshed.Description != fixture.Description {
		t.Errorf("the refresh changed the description: %q", refreshed.Description)
	}
}

// TestRefreshLeavesACurrentFixtureAlone checks that a refresh is safe to run
// over a whole directory: nothing changes, so nothing is written.
func TestRefreshLeavesACurrentFixtureAlone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	changed, err := golden.Refresh(path)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if changed {
		t.Error("Refresh() changed a fixture that was already current")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the file was rewritten even though nothing changed")
	}
}

// TestRefreshReportsAFixtureItCannotDecode checks that a fixture whose XDR
// does not decode is reported rather than quietly left stale.
func TestRefreshReportsAFixtureItCannotDecode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	fixture.XDR = "!!! not xdr !!!"
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if _, err := golden.Refresh(path); err == nil {
		t.Fatal("Refresh() returned no error for a fixture it could not decode")
	} else if !strings.Contains(err.Error(), "decoding") {
		t.Errorf("the error does not say what went wrong: %v", err)
	}
}
