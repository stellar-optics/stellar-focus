package golden_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// writeFixture writes a fixture file with the given content and returns its
// path.
func writeFixture(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fixture.golden.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

// lintFields returns the fields the lint reported problems in.
func lintFields(problems []golden.LintError) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.Field)
	}
	return out
}

func TestLintAcceptsACleanFixture(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "a fixture", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if problems := golden.Lint(path); len(problems) > 0 {
		t.Errorf("a clean fixture was reported as broken: %v", problems)
	}
}

// TestLintReportsCorruptedFixtures is what the linter is for: a fixture
// damaged by a hand-edit or a bad merge should say what is wrong with it.
func TestLintReportsCorruptedFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		field    string
		contains string
	}{
		{
			name:     "invalid JSON",
			content:  `{"format": "focus/v1", "type": "TransactionEnvelope",}`,
			contains: "invalid JSON",
		},
		{
			name:     "unknown field",
			content:  `{"format": "focus/v1", "type": "TransactionEnvelope", "xdr": "", "extra": 1}`,
			field:    "extra",
			contains: "not a field of a fixture",
		},
		{
			name:     "wrong format version",
			content:  `{"format": "focus/v2", "type": "TransactionEnvelope", "xdr": "AAAA"}`,
			field:    "format",
			contains: "focus/v1",
		},
		{
			name:     "missing type",
			content:  `{"format": "focus/v1", "xdr": "AAAA"}`,
			contains: "type",
		},
		{
			name:     "missing xdr",
			content:  `{"format": "focus/v1", "type": "TransactionEnvelope"}`,
			contains: "xdr",
		},
		{
			name:     "xdr is not base64",
			content:  `{"format": "focus/v1", "type": "TransactionEnvelope", "xdr": "!!! not base64 !!!"}`,
			field:    "xdr",
			contains: "not valid base64",
		},
		{
			name:     "xdr does not decode as the declared type",
			content:  `{"format": "focus/v1", "type": "TransactionResult", "xdr": "AAAA"}`,
			field:    "xdr",
			contains: "does not decode as a TransactionResult",
		},
		{
			name: "timestamp is not RFC3339",
			content: `{"format": "focus/v1", "type": "TransactionEnvelope", "xdr": "AAAA", ` +
				`"captured": {"network": "testnet", "at": "yesterday"}}`,
			field:    "captured.at",
			contains: "RFC3339",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeFixture(t, tc.content)
			problems := golden.Lint(path)
			if len(problems) == 0 {
				t.Fatal("the linter reported no problem with a broken fixture")
			}

			joined := ""
			for _, p := range problems {
				joined += p.Error() + "\n"
			}
			if !strings.Contains(joined, tc.contains) {
				t.Errorf("the problems do not mention %q:\n%s", tc.contains, joined)
			}
			if tc.field != "" && !strings.Contains(joined, tc.field) {
				t.Errorf("the problems do not name the field %q:\n%s", tc.field, joined)
			}
		})
	}
}

// TestLintNamesTheLineOfASyntaxError checks that a broken file says where it
// is broken: a line number turns "invalid JSON" into an edit.
func TestLintNamesTheLineOfASyntaxError(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "{\n  \"format\": \"focus/v1\",\n  \"type\": ,\n}\n")

	problems := golden.Lint(path)
	if len(problems) == 0 {
		t.Fatal("the linter reported no problem with a broken file")
	}
	if problems[0].Line != 3 {
		t.Errorf("the problem is reported at line %d, want 3: %v", problems[0].Line, problems[0])
	}
}

// TestLintChecksTheTypeItClaims is the check that catches a fixture whose
// payload was replaced with something else.
func TestLintChecksTheTypeItClaims(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	fixture.Type = "TransactionResult"
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	problems := golden.Lint(path)
	if len(problems) == 0 {
		t.Fatal("the linter accepted a fixture whose payload is another type")
	}
	if fields := lintFields(problems); len(fields) == 0 || fields[0] != "xdr" {
		t.Errorf("the problem is not about the XDR: %v", problems)
	}
}

// TestLintAcceptsAJSONFixtureWithADecodedSection checks that the decoded
// section is not linted: it is a rendering, and the XDR beside it is what
// the lint checks.
func TestLintAcceptsAJSONFixtureWithADecodedSection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	fixture.Decoded = json.RawMessage(`{"anything": [1, 2, 3]}`)
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if problems := golden.Lint(path); len(problems) > 0 {
		t.Errorf("a fixture with a decoded section was reported as broken: %v", problems)
	}
}

func TestStaleRuleDetectorAndWarnings(t *testing.T) {
	t.Parallel()

	ruleActive := golden.TrackIgnore("tx.memo", "payment_test.go:20")
	ruleActive.Hits = 5

	ruleStale := golden.TrackIgnore("tx.time_bounds.max_time", "payment_test.go:42")
	ruleStale.Hits = 0

	rules := []*golden.TrackedIgnoreRule{ruleActive, ruleStale}
	stale := golden.DetectStaleRules(rules)

	if len(stale) != 1 {
		t.Fatalf("DetectStaleRules got %d rules, want 1", len(stale))
	}
	if stale[0].Pattern != "tx.time_bounds.max_time" {
		t.Errorf("stale rule pattern = %q, want 'tx.time_bounds.max_time'", stale[0].Pattern)
	}

	warning := golden.FormatStaleRuleWarning(stale[0].Pattern, stale[0].Location)
	wantWarning := "WARNING: ignore rule 'tx.time_bounds.max_time' was never matched in payment_test.go:42"
	if warning != wantWarning {
		t.Errorf("FormatStaleRuleWarning() = %q, want %q", warning, wantWarning)
	}
}

func TestPruneReportFormatting(t *testing.T) {
	t.Parallel()

	rules := []*golden.TrackedIgnoreRule{
		golden.TrackIgnore("tx.time_bounds.max_time", "payment_test.go:42"),
		golden.TrackIgnore("tx.signatures", "transfer_test.go:15"),
	}

	report := golden.PruneReport(rules)
	for _, expected := range []string{
		"Fixture Ignore Rules Pruning Report:",
		"tx.time_bounds.max_time (registered at payment_test.go:42)",
		"tx.signatures (registered at transfer_test.go:15)",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("PruneReport missing %q:\n%s", expected, report)
		}
	}
}

func TestGoldenWithIgnorePathsTracksRegistrationLocation(t *testing.T) {
	t.Parallel()

	g := golden.New(t, golden.WithIgnorePaths("tx.memo", "tx.fee"))
	rules := g.TrackedRules()

	if len(rules) != 2 {
		t.Fatalf("expected 2 tracked rules, got %d", len(rules))
	}
	if rules[0].Pattern != "tx.memo" || rules[1].Pattern != "tx.fee" {
		t.Errorf("unexpected patterns: %+v", rules)
	}
	for _, r := range rules {
		if !strings.HasPrefix(r.Location, "lint_test.go:") {
			t.Errorf("expected location in lint_test.go, got %q", r.Location)
		}
	}
}
