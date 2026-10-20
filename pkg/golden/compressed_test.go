package golden_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

func TestIsCompressed(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]bool{
		"env.golden.json":    false,
		"env.golden.json.gz": true,
		"gz":                 false,
		".gz":                true,
	} {
		if got := golden.IsCompressed(path); got != want {
			t.Errorf("IsCompressed(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestSaveAndLoadRoundTripCompressed is the compact-repository promise: a
// .golden.json.gz fixture is real gzip on disk and an ordinary fixture in
// memory.
func TestSaveAndLoadRoundTripCompressed(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, 100)
	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json.gz")

	fixture, err := golden.Build(env, "TransactionEnvelope", "compressed", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
		t.Fatal("a .gz fixture was written as plain bytes")
	}

	loaded, err := golden.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.XDR != env || loaded.Type != "TransactionEnvelope" || loaded.Description != "compressed" {
		t.Errorf("compressed fixture did not round-trip: %+v", loaded)
	}
}

// TestLoadDetectsGzipByContent covers the renamed file: compression is
// detected by magic bytes, not by the suffix, so fixtures survive a rename.
func TestLoadDetectsGzipByContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	compressed := filepath.Join(dir, "env.golden.json.gz")
	renamed := filepath.Join(dir, "renamed.txt")

	fixture, err := golden.Build(buildEnvelope(t, 100), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(compressed, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	raw, err := os.ReadFile(compressed)
	if err != nil {
		t.Fatalf("reading %s: %v", compressed, err)
	}
	if err := os.WriteFile(renamed, raw, 0o600); err != nil {
		t.Fatalf("writing %s: %v", renamed, err)
	}

	loaded, err := golden.Load(renamed)
	if err != nil {
		t.Fatalf("Load() of a gzip payload without the .gz suffix: %v", err)
	}
	if loaded.XDR != fixture.XDR {
		t.Error("the stored XDR does not survive transparent decompression")
	}
}

// TestLoadReportsCorruptCompression keeps a truncated archive from being
// mistaken for a fixture that simply parses badly.
func TestLoadReportsCorruptCompression(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "broken.golden.json.gz")
	// A gzip magic prefix followed by garbage: detected as compressed, then
	// rejected by the decompressor.
	if err := os.WriteFile(path, append([]byte{0x1f, 0x8b, 0x08}, []byte("not really gzip")...), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	if _, err := golden.Load(path); err == nil {
		t.Fatal("Load() accepted a corrupt gzip payload")
	} else if !strings.Contains(err.Error(), "decompressing") || !strings.Contains(err.Error(), path) {
		t.Errorf("error does not explain what failed and where: %v", err)
	}
}

// TestWriteFileCompressesOnlyWhenAsked pins the storage rule: bytes are
// stored as-is unless the path asks for .gz.
func TestWriteFileCompressesOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := []byte("line one\nline two\n")

	plain := filepath.Join(dir, "plain.json")
	if err := golden.WriteFile(plain, content); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := os.ReadFile(plain)
	if err != nil {
		t.Fatalf("reading %s: %v", plain, err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("plain write altered the content: %q", got)
	}

	compressed := filepath.Join(dir, "packed.json.gz")
	if err := golden.WriteFile(compressed, content); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	read, err := golden.ReadFile(compressed)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(read, content) {
		t.Errorf("compressed round-trip altered the content: %q", read)
	}
}

// TestWriteFileReportsAnUnwritableDirectory keeps a silent write failure —
// a golden file that never landed — impossible.
func TestWriteFileReportsAnUnwritableDirectory(t *testing.T) {
	t.Parallel()

	// A regular file where the parent directory should be: MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", blocker, err)
	}

	err := golden.WriteFile(filepath.Join(blocker, "sub", "env.golden.json"), []byte("{}"))
	if err == nil {
		t.Fatal("WriteFile() succeeded with a file in place of its directory")
	}
	if !strings.Contains(err.Error(), blocker) {
		t.Errorf("error does not name the blocking path: %v", err)
	}
}

// TestCompressionIsDeterministic matters because a fixture that only
// decompresses differently would churn in review.
func TestCompressionIsDeterministic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := []byte(`{"format":"focus/v1"}` + "\n")

	first := filepath.Join(dir, "a.gz")
	second := filepath.Join(dir, "b.gz")
	if err := golden.WriteFile(first, content); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := golden.WriteFile(second, content); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("reading %s: %v", first, err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("reading %s: %v", second, err)
	}
	if !bytes.Equal(a, b) {
		t.Error("the same content compressed to different bytes")
	}
}

// TestAssertFindsCompressedTwin: a repository that gzipped its fixtures must
// not have to reconfigure every assertion.
func TestAssertFindsCompressedTwin(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, 100)
	dir := t.TempDir()

	fixture, err := golden.Build(env, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json.gz"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if !g.Assert("env", env) {
		t.Errorf("Assert() did not find the compressed fixture:\n%s", tb.Output())
	}
}

// TestAssertFailureOnCompressedFixtureShowsDiff keeps the failure output the
// contract it is: even against a .gz fixture, the message names the changed
// field and the remedy.
func TestAssertFailureOnCompressedFixtureShowsDiff(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fixture, err := golden.Build(buildEnvelope(t, 100), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json.gz"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if g.Assert("env", buildEnvelope(t, 200)) {
		t.Fatal("Assert() passed against a stale compressed golden file")
	}

	out := tb.Output()
	for _, want := range []string{"Body.PaymentOp.Amount", "is out of date", "env.golden.json.gz"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure does not contain %q:\n%s", want, out)
		}
	}
}
