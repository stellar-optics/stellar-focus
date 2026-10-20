package golden

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// ChecksumFile is the manifest name produced by Checksums.Write. A file with
// this name is never hashed, so the manifest can live inside the directory it
// describes — where a CI job naturally looks for it.
const ChecksumFile = "focus.sha256"

// Checksums maps a golden directory's files, as slash-separated paths
// relative to it, to the SHA-256 hex digest of their content.
//
// Digests are taken over what ReadFile returns — decompressed and
// LF-normalized — so a digest is a property of the fixture itself, not of how
// the working copy happens to be stored or checked out. The same fixture
// hashed on Windows and on Linux, plain or gzipped, agrees.
type Checksums map[string]string

// ComputeChecksums hashes every regular file under dir, skipping the
// manifest itself. It is the function CI runs to regenerate expectations and
// the one Verify runs to re-check them; both go through it so they can never
// disagree about what was hashed.
func ComputeChecksums(dir string) (Checksums, error) {
	sums := Checksums{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || d.Name() == ChecksumFile {
			return nil
		}
		data, readErr := ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return fmt.Errorf("relating %s to %s: %w", path, dir, relErr)
		}
		sum := sha256.Sum256(data)
		sums[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("checksumming %s: %w", dir, err)
	}
	return sums, nil
}

// Write stores the manifest at path in sha256sum's text format —
// "<digest>  <path>" per line, sorted — so a human can diff it in review and
// `sha256sum` can read it without conversion.
func (c Checksums) Write(path string) error {
	var body strings.Builder
	for _, name := range slices.Sorted(maps.Keys(c)) {
		fmt.Fprintf(&body, "%s  %s\n", c[name], name)
	}
	return WriteFile(path, []byte(body.String()))
}

// ReadChecksums parses a manifest written by Write.
func ReadChecksums(path string) (Checksums, error) {
	raw, err := ReadFile(path)
	if err != nil {
		return nil, err
	}

	sums := Checksums{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		digest, name, found := strings.Cut(line, "  ")
		if !found {
			return nil, fmt.Errorf("%s line %d is not a checksum entry: %q", path, i+1, line)
		}
		if _, err := hex.DecodeString(digest); err != nil || len(digest) != sha256.Size*2 {
			return nil, fmt.Errorf("%s line %d has a malformed digest: %q", path, i+1, digest)
		}
		if name == "" {
			return nil, fmt.Errorf("%s line %d names no file", path, i+1)
		}
		sums[name] = digest
	}
	return sums, nil
}

// Verify re-hashes dir and compares the result against the manifest. It
// returns nil when every file matches, and a *MismatchError naming exactly
// what changed otherwise — a tampered file must be reported as tampered, not
// merely as "verification failed".
func (c Checksums) Verify(dir string) error {
	got, err := ComputeChecksums(dir)
	if err != nil {
		return err
	}

	mismatch := &MismatchError{}
	for name, want := range c {
		switch have, ok := got[name]; {
		case !ok:
			mismatch.Missing = append(mismatch.Missing, name)
		case have != want:
			mismatch.Modified = append(mismatch.Modified, name)
		}
	}
	for name := range got {
		if _, ok := c[name]; !ok {
			mismatch.Added = append(mismatch.Added, name)
		}
	}
	if mismatch.empty() {
		return nil
	}
	mismatch.sort()
	return mismatch
}

// VerifyChecksums reads the manifest at manifestPath and verifies dir
// against it — the entry point a CI step calls.
func VerifyChecksums(dir, manifestPath string) error {
	sums, err := ReadChecksums(manifestPath)
	if err != nil {
		return err
	}
	return sums.Verify(dir)
}

// MismatchError reports every way a golden directory disagrees with its
// manifest: files whose content changed, files the manifest has but the disk
// does not, and files the disk has but the manifest does not.
type MismatchError struct {
	// Modified lists files present in both whose digest differs.
	Modified []string
	// Missing lists files the manifest records that are absent from disk.
	Missing []string
	// Added lists files on disk that the manifest never recorded.
	Added []string
}

// Error names each class with its files, so one line of CI output is enough
// to tell manual tampering — a modification — from an added or forgotten
// fixture.
func (e *MismatchError) Error() string {
	var parts []string
	for class, names := range map[string][]string{
		"modified": e.Modified,
		"missing":  e.Missing,
		"unlisted": e.Added,
	} {
		if len(names) > 0 {
			parts = append(parts, fmt.Sprintf("%s: %s", class, strings.Join(names, ", ")))
		}
	}
	slices.Sort(parts)
	return fmt.Sprintf("golden checksums do not match (%s)", strings.Join(parts, "; "))
}

func (e *MismatchError) empty() bool {
	return len(e.Modified) == 0 && len(e.Missing) == 0 && len(e.Added) == 0
}

func (e *MismatchError) sort() {
	slices.Sort(e.Modified)
	slices.Sort(e.Missing)
	slices.Sort(e.Added)
}
