package focus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Snapshot testing records a piece of human-facing text — most often a
// failure message — in a file and compares later runs against it. It exists
// because the failure message is this library's product: a refactor that
// silently changes the layout is a regression even when every assertion still
// returns the right boolean.
//
// Snapshots are plain text, deliberately. A reviewed snapshot in a pull
// request shows exactly what a developer will see in a red test, and a diff
// of the snapshot is the review. The base64 in a failure message is already
// truncated, so the files stay small enough to read.
//
// The default directory is testdata/snapshots. Files end in .snap unless the
// snapshot name already does. A missing snapshot is a failure, not an empty
// expectation: an absent file is how a new snapshot gets created, and it must
// not pass as though the output matched nothing.
const (
	// DefaultSnapshotDir is where snapshots are written when no directory is
	// configured.
	DefaultSnapshotDir = "testdata/snapshots"

	// SnapshotSuffix is appended to a snapshot name that does not already
	// carry it.
	SnapshotSuffix = ".snap"

	// SnapshotUpdateEnv is the environment variable that turns on snapshot
	// updating. Set it to 1 (or true/yes/on) and run the tests as usual:
	//
	//	FOCUS_UPDATE_SNAPSHOTS=1 go test ./...
	//
	// Every snapshot that differs is rewritten, and the resulting files are
	// meant to be reviewed and committed.
	SnapshotUpdateEnv = "FOCUS_UPDATE_SNAPSHOTS"
)

// SnapshotOption configures a snapshot assertion.
type SnapshotOption func(*snapshotConfig)

// snapshotConfig holds resolved snapshot settings.
type snapshotConfig struct {
	dir string
	// update is nil when the environment decides; a non-nil value is an
	// explicit choice that wins over the environment.
	update *bool
}

// WithSnapshotDir sets the directory snapshots are read from and written to.
// An empty directory is rejected when the snapshot is used.
func WithSnapshotDir(dir string) SnapshotOption {
	return func(c *snapshotConfig) { c.dir = dir }
}

// WithSnapshotUpdate forces update mode on or off for one snapshot, taking
// precedence over SnapshotUpdateEnv. Most tests should leave this unset and
// use the environment variable, so a normal test run never rewrites a file.
func WithSnapshotUpdate(update bool) SnapshotOption {
	return func(c *snapshotConfig) { c.update = &update }
}

// AssertSnapshot compares got with the snapshot named name under the
// configured directory, writing the snapshot instead when updating is
// enabled.
//
// The name is a slash-separated path relative to the snapshot directory.
// Absolute paths and names containing "." or ".." segments are rejected, so a
// test cannot read or write outside the directory by accident or through
// copied input.
//
// The text is compared after normalising line endings and the final newline,
// so a CRLF checkout does not produce a spurious failure. Nothing else is
// normalised: a change in indentation or in the amount of trailing whitespace
// inside a line is exactly the sort of regression a visual snapshot exists to
// catch.
func AssertSnapshot(t TB, name, got string, opts ...SnapshotOption) bool {
	t.Helper()

	cfg := snapshotConfig{dir: DefaultSnapshotDir}
	for _, opt := range opts {
		opt(&cfg)
	}

	filePath, err := snapshotPath(cfg.dir, name)
	if err != nil {
		t.Errorf("AssertSnapshot: %v", err)
		return false
	}
	want := normalizeSnapshot(got)

	// Updating does not need to read the old value first: writing the new
	// snapshot is the whole job, and doing it first means a corrupt or
	// unreadable snapshot file cannot block its own replacement.
	if cfg.updating() {
		return writeSnapshot(t, name, filePath, want)
	}

	raw, readErr := os.ReadFile(filePath)
	switch {
	case readErr == nil:
		if normalizeSnapshot(string(raw)) == want {
			return true
		}
		t.Errorf("%s", snapshotMismatchMessage(name, filePath, normalizeSnapshot(string(raw)), want))
		return false

	case errors.Is(readErr, fs.ErrNotExist):
		t.Errorf("AssertSnapshot: no snapshot %q at %s\n\n"+
			"Create it by running:\n\n"+
			"    %s=1 go test ./...\n",
			name, filePath, SnapshotUpdateEnv)
		return false

	default:
		t.Errorf("AssertSnapshot: reading snapshot %q at %s: %v", name, filePath, readErr)
		return false
	}
}

// AssertFailureSnapshot runs fn with a recording TB and compares the failure
// text it produced with the snapshot named name.
//
// A function that reports nothing is itself a failure: a snapshot of no
// output would pass forever while checking nothing. The FATAL variant of an
// assertion is recorded rather than stopping the goroutine, so fn continues
// after a Fatalf; tests that snapshot require's output should call one
// assertion and return.
func AssertFailureSnapshot(t TB, name string, fn func(TB), opts ...SnapshotOption) bool {
	t.Helper()

	if fn == nil {
		t.Errorf("AssertFailureSnapshot: %q has no function to run", name)
		return false
	}

	recorder := &recordingTB{name: t.Name()}
	fn(recorder)
	if len(recorder.messages) == 0 {
		t.Errorf("AssertFailureSnapshot: %q reported no failure", name)
		return false
	}
	return AssertSnapshot(t, name, recorder.Output(), opts...)
}

// snapshotPath validates name and returns the file it denotes.
func snapshotPath(dir, name string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("snapshot directory is empty")
	}
	if name == "" {
		return "", fmt.Errorf("snapshot name is empty")
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("snapshot name %q contains a NUL byte", name)
	}
	if strings.Contains(name, `\`) {
		return "", fmt.Errorf("snapshot name %q must use '/' separators", name)
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("snapshot name %q must be relative", name)
	}
	for _, segment := range strings.Split(name, "/") {
		switch segment {
		case "", ".", "..":
			return "", fmt.Errorf("snapshot name %q contains an invalid path segment", name)
		}
	}
	if !strings.HasSuffix(name, SnapshotSuffix) {
		name += SnapshotSuffix
	}
	return filepath.Join(dir, filepath.FromSlash(path.Clean(name))), nil
}

// writeSnapshot creates the snapshot file, including its directory.
func writeSnapshot(t TB, name, filePath, content string) bool {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Errorf("AssertSnapshot: creating the snapshot directory for %q: %v", name, err)
		return false
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Errorf("AssertSnapshot: writing snapshot %q at %s: %v", name, filePath, err)
		return false
	}
	return true
}

// snapshotMismatchMessage renders a changed snapshot. The first differing
// line is quoted with %q so indentation and trailing whitespace are visible;
// the line counts tell the reader whether text was added or removed beyond
// that first difference.
func snapshotMismatchMessage(name, filePath, want, got string) string {
	wantLines := snapshotLines(want)
	gotLines := snapshotLines(got)

	first := 0
	for first < len(wantLines) && first < len(gotLines) && wantLines[first] == gotLines[first] {
		first++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "AssertSnapshot: snapshot %q changed at %s\n", name, filePath)
	fmt.Fprintf(&b, "\n  line %d:\n", first+1)
	fmt.Fprintf(&b, "    want: %q\n", snapshotLine(wantLines, first))
	fmt.Fprintf(&b, "    got:  %q\n", snapshotLine(gotLines, first))
	if len(wantLines) != len(gotLines) {
		fmt.Fprintf(&b, "    (want %d line(s), got %d)\n", len(wantLines), len(gotLines))
	}
	fmt.Fprintf(&b, "\nUpdate it by running:\n\n    %s=1 go test ./...\n", SnapshotUpdateEnv)
	return b.String()
}

// snapshotLines splits normalised snapshot text into lines without a
// trailing empty line.
func snapshotLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// snapshotLine returns a line or a marker when the snapshot ended.
func snapshotLine(lines []string, i int) string {
	if i >= len(lines) {
		return "(end of snapshot)"
	}
	return lines[i]
}

// normalizeSnapshot makes a snapshot platform-independent at the line-ending
// and final-newline level. It deliberately changes nothing else.
func normalizeSnapshot(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return ""
	}
	return strings.TrimRight(s, "\n") + "\n"
}

// updating reports whether snapshots should be rewritten. An explicit option
// wins over the environment.
func (c snapshotConfig) updating() bool {
	if c.update != nil {
		return *c.update
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(SnapshotUpdateEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// recordingTB captures what an assertion reports so it can be snapshotted.
// Messages keep the order they were reported in, whichever method was used.
type recordingTB struct {
	name     string
	messages []string
}

// Helper implements TB.
func (r *recordingTB) Helper() {}

// Errorf implements TB.
func (r *recordingTB) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// Fatalf implements TB. It records rather than aborting, matching
// internal/faketb: a snapshot test wants the text, not a stopped goroutine.
func (r *recordingTB) Fatalf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// Name implements TB.
func (r *recordingTB) Name() string { return r.name }

// Output joins the recorded messages in report order.
func (r *recordingTB) Output() string {
	return strings.Join(r.messages, "\n")
}
