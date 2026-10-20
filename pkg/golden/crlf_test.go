package golden_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

func TestNormalizeLineEndings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"crlf to lf", "a\r\nb\r\n", "a\nb\n"},
		{"lone cr to lf", "a\rb", "a\nb"},
		{"mixed endings", "a\r\nb\rc\nd", "a\nb\nc\nd"},
		{"lf untouched", "a\nb\n", "a\nb\n"},
		{"no endings", "flat", "flat"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := string(golden.NormalizeLineEndings([]byte(tc.in))); got != tc.want {
				t.Errorf("NormalizeLineEndings(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeLineEndingsIsIdempotent keeps double normalization — Load
// followed by a caller normalizing again — from ever drifting.
func TestNormalizeLineEndingsIsIdempotent(t *testing.T) {
	t.Parallel()

	once := golden.NormalizeLineEndings([]byte("a\r\nb\rc\n"))
	if twice := golden.NormalizeLineEndings(once); string(twice) != string(once) {
		t.Errorf("second pass changed the content: %q -> %q", once, twice)
	}
}

// withCRLF rewrites a saved golden file the way a Windows checkout would.
func withCRLF(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	crlf := strings.ReplaceAll(string(raw), "\n", "\r\n")
	if err := os.WriteFile(path, []byte(crlf), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestLoadNormalizesCRLFCheckout is the cross-platform promise: the same
// fixture, checked out with CRLF endings, must load to the same content.
func TestLoadNormalizesCRLFCheckout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(buildEnvelope(t, 100), "TransactionEnvelope", "crlf", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	withCRLF(t, path)

	loaded, err := golden.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.ContainsRune(string(loaded.Decoded), '\r') {
		t.Error("decoded section still carries CR after loading a CRLF file")
	}
	if loaded.XDR != fixture.XDR {
		t.Error("the stored XDR does not survive CRLF normalization")
	}
}

// TestAssertPassesAgainstCRLFCheckout is the failure this issue exists to
// remove: an assertion that failed on Windows only, with nothing saying why.
func TestAssertPassesAgainstCRLFCheckout(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, 100)
	dir := t.TempDir()

	fixture, err := golden.Build(env, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	path := filepath.Join(dir, "env.golden.json")
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	withCRLF(t, path)

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if !g.Assert("env", env) {
		t.Errorf("assertion failed against a CRLF checkout of a matching golden file:\n%s", tb.Output())
	}
}

// TestAssertFailureStaysDescriptiveOnCRLF proves normalization does not
// swallow real differences: a stale fixture checked out with CRLF must still
// report which field moved and how to re-record.
func TestAssertFailureStaysDescriptiveOnCRLF(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fixture, err := golden.Build(buildEnvelope(t, 100), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	path := filepath.Join(dir, "env.golden.json")
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	withCRLF(t, path)

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if g.Assert("env", buildEnvelope(t, 200)) {
		t.Fatal("Assert() passed against a stale golden file")
	}

	out := tb.Output()
	for _, want := range []string{"Body.PaymentOp.Amount", "is out of date", "-focus.update"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure does not contain %q:\n%s", want, out)
		}
	}
}
