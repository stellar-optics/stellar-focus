package golden_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// writeManifestFixture saves a fixture in dir under name and returns its path.
func writeManifestFixture(t *testing.T, dir, name, amount string) string {
	t.Helper()

	fixture, err := golden.Build(buildEnvelope(t, 100), "TransactionEnvelope", amount, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	path := filepath.Join(dir, name)
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return path
}

// TestChecksumsRoundTripThroughAManifest covers the CI workflow: hash a
// directory, write the manifest, read it back, and get the same expectations.
func TestChecksumsRoundTripThroughAManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeManifestFixture(t, dir, "env.golden.json", "one")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hand-written\n"), 0o600); err != nil {
		t.Fatalf("writing notes: %v", err)
	}

	sums, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}
	if len(sums) != 2 {
		t.Fatalf("ComputeChecksums() hashed %d files, want 2: %v", len(sums), sums)
	}
	for name, digest := range sums {
		if len(digest) != 64 {
			t.Errorf("digest for %q is %q, want 64 hex characters", name, digest)
		}
	}

	manifest := filepath.Join(dir, golden.ChecksumFile)
	if err := sums.Write(manifest); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	read, err := golden.ReadChecksums(manifest)
	if err != nil {
		t.Fatalf("ReadChecksums() error = %v", err)
	}
	if len(read) != len(sums) {
		t.Errorf("manifest holds %d entries, want %d", len(read), len(sums))
	}
	for name, digest := range sums {
		if read[name] != digest {
			t.Errorf("manifest entry %q = %q, want %q", name, read[name], digest)
		}
	}

	// The manifest must not hash itself: re-computing over the directory it
	// now sits in must reproduce the same set.
	again, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("second ComputeChecksums() error = %v", err)
	}
	if len(again) != len(sums) {
		t.Errorf("re-computing included the manifest: %v", again)
	}
}

// TestVerifyAcceptsAnUntouchedDirectory is the green-CI path.
func TestVerifyAcceptsAnUntouchedDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeManifestFixture(t, dir, "env.golden.json", "one")

	sums, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}
	manifest := filepath.Join(dir, golden.ChecksumFile)
	if err := sums.Write(manifest); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := golden.VerifyChecksums(dir, manifest); err != nil {
		t.Errorf("VerifyChecksums() on an untouched directory: %v", err)
	}
}

// TestVerifyDetectsEveryKindOfTampering checks that the error is the
// descriptive kind — errors.As to a MismatchError naming the files — rather
// than a bare "verification failed".
func TestVerifyDetectsEveryKindOfTampering(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeManifestFixture(t, dir, "env.golden.json", "one")
	writeManifestFixture(t, dir, "other.golden.json", "two")

	sums, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}
	manifest := filepath.Join(dir, golden.ChecksumFile)
	if err := sums.Write(manifest); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// One file edited behind the manifest's back, one deleted, one added.
	if err := os.WriteFile(filepath.Join(dir, "env.golden.json"), []byte(`{"format":"focus/v1","type":"X","xdr":"AAAA"}`), 0o600); err != nil {
		t.Fatalf("tampering: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "other.golden.json")); err != nil {
		t.Fatalf("removing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sneaky.golden.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("adding: %v", err)
	}

	err = golden.VerifyChecksums(dir, manifest)
	if err == nil {
		t.Fatal("VerifyChecksums() passed on a tampered directory")
	}

	var mismatch *golden.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error is %T, want *golden.MismatchError: %v", err, err)
	}
	if len(mismatch.Modified) != 1 || mismatch.Modified[0] != "env.golden.json" {
		t.Errorf("Modified = %v, want [env.golden.json]", mismatch.Modified)
	}
	if len(mismatch.Missing) != 1 || mismatch.Missing[0] != "other.golden.json" {
		t.Errorf("Missing = %v, want [other.golden.json]", mismatch.Missing)
	}
	if len(mismatch.Added) != 1 || mismatch.Added[0] != "sneaky.golden.json" {
		t.Errorf("Added = %v, want [sneaky.golden.json]", mismatch.Added)
	}
	for _, want := range []string{"modified: env.golden.json", "missing: other.golden.json", "unlisted: sneaky.golden.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q: %v", want, err)
		}
	}
}

// TestChecksumsIgnoreLineEndings is the Windows/Linux agreement: the same
// fixture checked out with CRLF must hash to the same digest.
func TestChecksumsIgnoreLineEndings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeManifestFixture(t, dir, "env.golden.json", "one")

	lf, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), "\n", "\r\n")), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	crlf, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}
	if crlf["env.golden.json"] != lf["env.golden.json"] {
		t.Errorf("CRLF checkout changed the digest: %s vs %s",
			crlf["env.golden.json"], lf["env.golden.json"])
	}
}

// TestChecksumsSeeThroughCompression: a fixture recompressed in place is the
// same fixture, so its digest must not move.
func TestChecksumsSeeThroughCompression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeManifestFixture(t, dir, "env.golden.json", "one")

	before, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}

	// Store the same content gzipped under the same name.
	packed := filepath.Join(dir, "packed.gz")
	if err := golden.WriteFile(packed, mustRead(t, path)); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	raw, err := os.ReadFile(packed)
	if err != nil {
		t.Fatalf("reading %s: %v", packed, err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.Remove(packed); err != nil {
		t.Fatalf("removing %s: %v", packed, err)
	}

	after, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}
	if after["env.golden.json"] != before["env.golden.json"] {
		t.Errorf("compression changed the digest: %s vs %s",
			after["env.golden.json"], before["env.golden.json"])
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return raw
}

// TestTamperingFailsChecksumAndAssertionTogether is the CI story end to end:
// a hand-edited fixture is caught by the manifest, and the assertion that
// runs without the manifest still fails with a descriptive diff.
func TestTamperingFailsChecksumAndAssertionTogether(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	original := buildEnvelope(t, 100)
	fixture, err := golden.Build(original, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	sums, err := golden.ComputeChecksums(dir)
	if err != nil {
		t.Fatalf("ComputeChecksums() error = %v", err)
	}
	manifest := filepath.Join(dir, golden.ChecksumFile)
	if err := sums.Write(manifest); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Someone edits the fixture by hand to a different envelope.
	edited, err := golden.Build(buildEnvelope(t, 200), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), edited); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	var mismatch *golden.MismatchError
	if err := golden.VerifyChecksums(dir, manifest); !errors.As(err, &mismatch) {
		t.Errorf("VerifyChecksums() did not report a mismatch: %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if g.Assert("env", original) {
		t.Fatal("Assert() passed against the hand-edited fixture")
	}
	out := tb.Output()
	for _, want := range []string{"Body.PaymentOp.Amount", "is out of date"} {
		if !strings.Contains(out, want) {
			t.Errorf("assertion failure does not contain %q:\n%s", want, out)
		}
	}
}

// TestReadChecksumsRejectsMalformedManifests keeps a corrupted manifest from
// silently verifying nothing.
func TestReadChecksumsRejectsMalformedManifests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body string
	}{
		{"no separator", "deadbeef env.golden.json\n"},
		{"short digest", "abc  env.golden.json\n"},
		{"non-hex digest", strings.Repeat("z", 64) + "  env.golden.json\n"},
		{"no file name", strings.Repeat("a", 64) + "  \n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), golden.ChecksumFile)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("writing manifest: %v", err)
			}
			if _, err := golden.ReadChecksums(path); err == nil {
				t.Fatal("ReadChecksums() accepted a malformed manifest")
			}
		})
	}
}

// TestComputeChecksumsReportsAMissingDirectory keeps CI from hashing nothing
// and passing.
func TestComputeChecksumsReportsAMissingDirectory(t *testing.T) {
	t.Parallel()

	_, err := golden.ComputeChecksums(filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("ComputeChecksums() accepted a missing directory")
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("error does not name the directory: %v", err)
	}
}
