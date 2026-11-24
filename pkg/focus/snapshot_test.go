package focus_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// message returns the single failure a recording TB captured.
func message(t *testing.T, tb *faketb.TB) string {
	t.Helper()
	msg, err := tb.Message()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestAssertSnapshotMissingExplainsHowToCreateIt(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	ok := focus.AssertSnapshot(tb, "missing", "hello", focus.WithSnapshotDir(t.TempDir()))
	if ok {
		t.Fatal("a missing snapshot passed")
	}
	msg := message(t, tb)
	for _, want := range []string{"no snapshot", "missing.snap", "FOCUS_UPDATE_SNAPSHOTS=1", "go test"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertSnapshotUpdateCreatesThenMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tb := faketb.New()
	if !focus.AssertSnapshot(tb, "payment", "first line\nsecond line\n",
		focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true)) {
		t.Fatalf("update failed:\n%s", tb.Output())
	}

	path := filepath.Join(dir, "payment.snap")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the written snapshot: %v", err)
	}
	if got, want := string(data), "first line\nsecond line\n"; got != want {
		t.Errorf("snapshot = %q, want %q", got, want)
	}

	// A normal run now matches without rewriting anything.
	tb = faketb.New()
	if !focus.AssertSnapshot(tb, "payment", "first line\nsecond line\n", focus.WithSnapshotDir(dir)) {
		t.Fatalf("matching snapshot failed:\n%s", tb.Output())
	}
}

func TestAssertSnapshotReportsTheFirstChangedLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	focus.AssertSnapshot(faketb.New(), "layout", "one\ntwo\nthree\n",
		focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true))

	tb := faketb.New()
	if focus.AssertSnapshot(tb, "layout", "one\nTWO\nthree\n", focus.WithSnapshotDir(dir)) {
		t.Fatal("a changed snapshot passed")
	}
	msg := message(t, tb)
	for _, want := range []string{"changed", "line 2", `want: "two"`, `got:  "TWO"`, "FOCUS_UPDATE_SNAPSHOTS=1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertSnapshotReportsAddedLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	focus.AssertSnapshot(faketb.New(), "added", "one\n", focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true))

	tb := faketb.New()
	if focus.AssertSnapshot(tb, "added", "one\ntwo\n", focus.WithSnapshotDir(dir)) {
		t.Fatal("an added line passed")
	}
	msg := message(t, tb)
	for _, want := range []string{"line 2", `want: "(end of snapshot)"`, `got:  "two"`, "want 1 line(s), got 2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertSnapshotNormalizesLineEndings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tb := faketb.New()
	if !focus.AssertSnapshot(tb, "crlf", "a\r\nb\r\n", focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true)) {
		t.Fatalf("update failed:\n%s", tb.Output())
	}

	data, err := os.ReadFile(filepath.Join(dir, "crlf.snap"))
	if err != nil {
		t.Fatalf("reading snapshot: %v", err)
	}
	if strings.Contains(string(data), "\r") {
		t.Errorf("snapshot kept a carriage return: %q", data)
	}

	// Different final-newline and line-ending spelling is the same text.
	tb = faketb.New()
	if !focus.AssertSnapshot(tb, "crlf", "a\nb\n\n", focus.WithSnapshotDir(dir)) {
		t.Fatalf("normalised snapshot did not match:\n%s", tb.Output())
	}
}

func TestAssertSnapshotEmptyContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tb := faketb.New()
	if !focus.AssertSnapshot(tb, "empty", "", focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true)) {
		t.Fatalf("empty update failed:\n%s", tb.Output())
	}
	data, err := os.ReadFile(filepath.Join(dir, "empty.snap"))
	if err != nil {
		t.Fatalf("reading snapshot: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("empty snapshot = %q, want an empty file", data)
	}
	if !focus.AssertSnapshot(faketb.New(), "empty", "", focus.WithSnapshotDir(dir)) {
		t.Error("an empty snapshot did not match")
	}
}

func TestAssertSnapshotAcceptsNestedNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tb := faketb.New()
	if !focus.AssertSnapshot(tb, "messages/payment", "text", focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true)) {
		t.Fatalf("nested update failed:\n%s", tb.Output())
	}
	if _, err := os.Stat(filepath.Join(dir, "messages", "payment.snap")); err != nil {
		t.Errorf("nested snapshot was not written: %v", err)
	}
}

func TestAssertSnapshotAcceptsAnExplicitSuffix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	focus.AssertSnapshot(faketb.New(), "already.snap", "text",
		focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true))
	if _, err := os.Stat(filepath.Join(dir, "already.snap")); err != nil {
		t.Errorf("explicitly suffixed snapshot was not written: %v", err)
	}
}

func TestAssertSnapshotRejectsUnsafeNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		snap string
		dir  string
		want string
	}{
		{"empty name", "", "testdata", "snapshot name is empty"},
		{"dot", ".", "testdata", "invalid path segment"},
		{"dot dot", "..", "testdata", "invalid path segment"},
		{"parent escape", "../escape", "testdata", "invalid path segment"},
		{"nested escape", "a/../b", "testdata", "invalid path segment"},
		{"leading slash", "/absolute", "testdata", "must be relative"},
		{"backslash", `a\b`, "testdata", "must use '/' separators"},
		{"double slash", "a//b", "testdata", "invalid path segment"},
		{"dot segment", "a/./b", "testdata", "invalid path segment"},
		{"trailing slash", "a/", "testdata", "invalid path segment"},
		{"NUL byte", "a\x00b", "testdata", "NUL byte"},
		{"empty directory", "valid", "", "snapshot directory is empty"},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertSnapshot(tb, tc.snap, "text", focus.WithSnapshotDir(tc.dir)) {
				t.Fatal("an unsafe snapshot name was accepted")
			}
			msg := message(t, tb)
			if !strings.Contains(msg, tc.want) {
				t.Errorf("message does not contain %q:\n%s", tc.want, msg)
			}
		})
	}
}

func TestAssertSnapshotReportsReadAndWriteErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "blocked.snap"), 0o755); err != nil {
		t.Fatalf("creating blocking directory: %v", err)
	}

	tb := faketb.New()
	if focus.AssertSnapshot(tb, "blocked", "text", focus.WithSnapshotDir(dir)) {
		t.Fatal("a snapshot path that is a directory passed")
	}
	if msg := message(t, tb); !strings.Contains(msg, "reading snapshot") {
		t.Errorf("read error is not explained:\n%s", msg)
	}

	// A file where the directory should be makes the write fail.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("creating blocker: %v", err)
	}
	tb = faketb.New()
	if focus.AssertSnapshot(tb, "written", "text",
		focus.WithSnapshotDir(blocker), focus.WithSnapshotUpdate(true)) {
		t.Fatal("a snapshot into a non-directory passed")
	}
	if msg := message(t, tb); !strings.Contains(msg, "creating the snapshot directory") {
		t.Errorf("write error is not explained:\n%s", msg)
	}
}

func TestAssertSnapshotDoesNotRewriteOnMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	focus.AssertSnapshot(faketb.New(), "stable", "original\n",
		focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true))

	focus.AssertSnapshot(faketb.New(), "stable", "different\n", focus.WithSnapshotDir(dir))

	data, err := os.ReadFile(filepath.Join(dir, "stable.snap"))
	if err != nil {
		t.Fatalf("reading snapshot: %v", err)
	}
	if got := string(data); got != "original\n" {
		t.Errorf("a failed comparison rewrote the snapshot: %q", got)
	}
}

func TestSnapshotUpdateEnvironment(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"off", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{" on ", true},
	}

	for _, tc := range tests {
		t.Run("value "+tc.value, func(t *testing.T) {
			t.Setenv(focus.SnapshotUpdateEnv, tc.value)
			dir := t.TempDir()

			focus.AssertSnapshot(faketb.New(), "value", "text", focus.WithSnapshotDir(dir))
			_, err := os.Stat(filepath.Join(dir, "value.snap"))
			got := err == nil
			if got != tc.want {
				t.Errorf("with %s=%q, snapshot written = %v, want %v",
					focus.SnapshotUpdateEnv, tc.value, got, tc.want)
			}
		})
	}
}

func TestWithSnapshotUpdateOverridesTheEnvironment(t *testing.T) {
	t.Setenv(focus.SnapshotUpdateEnv, "1")

	dir := t.TempDir()
	tb := faketb.New()
	if focus.AssertSnapshot(tb, "forced-off", "text",
		focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(false)) {
		t.Fatal("WithSnapshotUpdate(false) did not override the environment")
	}
	if _, err := os.Stat(filepath.Join(dir, "forced-off.snap")); err == nil {
		t.Error("WithSnapshotUpdate(false) still wrote the snapshot")
	}

	t.Setenv(focus.SnapshotUpdateEnv, "")
	dir = t.TempDir()
	if !focus.AssertSnapshot(faketb.New(), "forced-on", "text",
		focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true)) {
		t.Fatal("WithSnapshotUpdate(true) did not turn updating on")
	}
}

func TestAssertFailureSnapshotCapturesReportedText(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tb := faketb.New()
	ok := focus.AssertFailureSnapshot(tb, "failure", func(tb focus.TB) {
		tb.Helper()
		tb.Errorf("first problem: %d", 1)
		tb.Fatalf("second problem")
	}, focus.WithSnapshotDir(dir), focus.WithSnapshotUpdate(true))
	if !ok {
		t.Fatalf("failure snapshot failed:\n%s", tb.Output())
	}

	data, err := os.ReadFile(filepath.Join(dir, "failure.snap"))
	if err != nil {
		t.Fatalf("reading snapshot: %v", err)
	}
	want := "first problem: 1\nsecond problem\n"
	if got := string(data); got != want {
		t.Errorf("snapshot = %q, want %q", got, want)
	}
}

func TestAssertFailureSnapshotRequiresAFailure(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if focus.AssertFailureSnapshot(tb, "quiet", func(focus.TB) {},
		focus.WithSnapshotDir(t.TempDir())) {
		t.Fatal("a function that reported nothing passed")
	}
	if msg := message(t, tb); !strings.Contains(msg, "reported no failure") {
		t.Errorf("message does not explain the problem:\n%s", msg)
	}
}

func TestAssertFailureSnapshotRequiresAFunction(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if focus.AssertFailureSnapshot(tb, "nil", nil, focus.WithSnapshotDir(t.TempDir())) {
		t.Fatal("a nil function passed")
	}
	if msg := message(t, tb); !strings.Contains(msg, "no function to run") {
		t.Errorf("message does not explain the problem:\n%s", msg)
	}
}

// TestFailureMessageSnapshot is the integration point this feature exists
// for: the text a developer sees when an assertion fails is pinned, so a
// formatting regression is a red test with a reviewable snapshot diff.
func TestFailureMessageSnapshot(t *testing.T) {
	t.Parallel()

	focus.AssertFailureSnapshot(t, "envelope_amount_mismatch", func(tb focus.TB) {
		want := buildEnvelope(t, paymentOp(t, 100_0000000))
		got := buildEnvelope(t, paymentOp(t, 200_0000000))
		focus.AssertEnvelopeEqual(tb, want, got)
	})
}

// TestSnapshotHelperReportsErrorsAsFailures checks the feature's own failure
// path through the public API: a snapshot mismatch must not panic.
func TestSnapshotHelperReportsErrorsAsFailures(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("AssertSnapshot panicked: %v", r)
			}
		}()
		focus.AssertSnapshot(tb, "missing", "text", focus.WithSnapshotDir(t.TempDir()))
	}()
	if !tb.Failed() {
		t.Error("a missing snapshot did not report a failure")
	}
}
