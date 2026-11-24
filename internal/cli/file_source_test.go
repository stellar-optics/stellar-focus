package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFileSourceCapturesAndVerifiesOffline runs the real source factory, not
// the fake: --file-source must reach a source that reads the directory.
func TestFileSourceCapturesAndVerifiesOffline(t *testing.T) {
	clearFocusEnv(t)
	originalNow := now
	now = func() time.Time { return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = originalNow })

	dumps, out := t.TempDir(), t.TempDir()
	for name, file := range map[string]string{
		"envelope": "tx_failed.env.txt",
		"result":   "tx_failed.res.txt",
	} {
		path := filepath.Join(dumps, testHash+"."+name+".xdr")
		if err := os.WriteFile(path, []byte(fixtureXDR(t, file)), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	stdout, _, err := run(t, "capture", "tx", testHash, "--out", out, "--file-source", dumps)
	if err != nil {
		t.Fatalf("capture error = %v", err)
	}
	if !strings.Contains(stdout, "envelope.golden.json") || !strings.Contains(stdout, "result.golden.json") {
		t.Errorf("capture did not write both fixtures:\n%s", stdout)
	}

	stdout, stderr, err := run(t, "verify", out, "--file-source", dumps)
	if err != nil {
		t.Fatalf("verify error = %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "2 checked: 2 ok") {
		t.Errorf("verify did not match both fixtures:\n%s", stdout)
	}
}

func TestFileSourceMustBeADirectory(t *testing.T) {
	clearFocusEnv(t)

	file := filepath.Join(t.TempDir(), "dump.xdr")
	if err := os.WriteFile(file, []byte("AAAA"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(t.TempDir(), "missing"), file} {
		_, _, err := run(t, "verify", t.TempDir(), "--file-source", dir)
		if err == nil || !strings.Contains(err.Error(), "file source") {
			t.Errorf("--file-source %s: error = %v, want a file source usage error", dir, err)
		}
		if code, _ := ExitCode(err); code != ExitFailure {
			t.Errorf("--file-source %s: exit code = %d, want %d", dir, code, ExitFailure)
		}
	}
}
