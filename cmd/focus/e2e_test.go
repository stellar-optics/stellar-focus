package main_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// focusBinary is the freshly built command, so these tests exercise the same
// binary a user runs rather than calling internal functions in-process.
var focusBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "focus-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "building the focus binary:", err)
		os.Exit(1)
	}
	name := "focus"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)

	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building the focus binary:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	focusBinary = bin
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// runFocus runs the built binary in dir with a clean FOCUS_* environment and
// returns exactly what a shell would see.
func runFocus(t *testing.T, dir string, env map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	// The developer running the suite may have FOCUS_* set in their shell.
	// These tests own the environment completely, so drop the ambient
	// values and add only what the test asked for.
	merged := make([]string, 0, len(os.Environ())+len(env))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FOCUS_") {
			continue
		}
		merged = append(merged, kv)
	}
	for key, value := range env {
		merged = append(merged, key+"="+value)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, focusBinary, args...)
	cmd.Dir = dir
	cmd.Env = merged

	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	err := cmd.Run()
	switch {
	case err == nil:
		code = 0
	case ctx.Err() != nil:
		t.Fatalf("focus %v timed out", args)
	default:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running focus %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errBuf.String(), code
}

func TestE2EVersion(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runFocus(t, t.TempDir(), nil, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("version printed nothing")
	}
	if stderr != "" {
		t.Errorf("version wrote to stderr: %s", stderr)
	}
}

func TestE2EHelpListsCommandsAndConfigurationFlags(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runFocus(t, t.TempDir(), nil, "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{
		"capture", "verify", "encode", "version",
		"--config", "--network", "--rpc-url", "--timeout", "--color",
		"FOCUS_",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not mention %q:\n%s", want, stdout)
		}
	}
}

func TestE2EUnknownCommandFails(t *testing.T) {
	t.Parallel()

	_, stderr, code := runFocus(t, t.TempDir(), nil, "definitely-not-a-command")
	if code == 0 {
		t.Fatal("an unknown command exited 0")
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr does not name the problem: %s", stderr)
	}
}

func TestE2ECaptureRequiresAHash(t *testing.T) {
	t.Parallel()

	_, stderr, code := runFocus(t, t.TempDir(), nil, "capture", "tx")
	if code == 0 {
		t.Fatal("capture tx without a hash exited 0")
	}
	if !strings.Contains(stderr, "--batch") {
		t.Errorf("stderr does not explain how to name the transactions: %s", stderr)
	}
}

func TestE2EUnknownNetworkFailsBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	_, stderr, code := runFocus(t, t.TempDir(), nil,
		"capture", "tx", "deadbeef", "--network", "wonderland", "--out", out)
	if code == 0 {
		t.Fatal("an unknown network exited 0")
	}
	if !strings.Contains(stderr, "unknown network") {
		t.Errorf("stderr does not name the network problem: %s", stderr)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("reading the output directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a request was made despite the invalid network: %v", entries)
	}
}

func TestE2EInvalidConfigurationFailsWithAnExplanation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"colour", []string{"--color", "rainbow", "version"}, "color must be"},
		{"timeout", []string{"--timeout", "0s", "version"}, "timeout must be positive"},
		{"network", []string{"--network", "", "version"}, "network must not be empty"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, stderr, code := runFocus(t, t.TempDir(), nil, tc.args...)
			if code == 0 {
				t.Fatalf("%v exited 0", tc.args)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not contain %q: %s", tc.want, stderr)
			}
		})
	}
}

func TestE2EVerifyEmptyDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, stderr, code := runFocus(t, t.TempDir(), nil, "verify", dir)
	if code == 0 {
		t.Fatal("verify of an empty directory exited 0")
	}
	if !strings.Contains(stderr, "no fixture files") {
		t.Errorf("stderr does not explain the empty directory: %s", stderr)
	}
}

// TestE2EVerifyHandWrittenFixtureIsSkipped runs the whole pipeline — file
// parsing, provenance detection, reporting — with no network and no fixture
// from the module's testdata beyond the XDR payload itself.
func TestE2EVerifyHandWrittenFixtureIsSkipped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeHandWrittenFixture(t, dir)

	stdout, stderr, code := runFocus(t, t.TempDir(), nil, "verify", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "SKIP") {
		t.Errorf("hand-written fixture was not reported as skipped:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 checked: 0 ok, 0 drifted, 1 skipped") {
		t.Errorf("summary line missing or wrong:\n%s", stdout)
	}
}

func TestE2EConfigFileIsDiscovered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".focus.yaml"), "network: wonderland\n")

	_, stderr, code := runFocus(t, dir, nil, "version")
	if code == 0 {
		t.Fatal(".focus.yaml was not read")
	}
	if !strings.Contains(stderr, "unknown network") {
		t.Errorf("config-file network was not validated: %s", stderr)
	}

	writeFile(t, filepath.Join(dir, ".focus.yaml"), "network: testnet\n")
	if _, stderr, code := runFocus(t, dir, nil, "version"); code != 0 {
		t.Fatalf("valid .focus.yaml exited %d: %s", code, stderr)
	}
}

func TestE2EFlagBeatsEnvironmentAndConfigFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	config := filepath.Join(dir, "focus.yaml")
	writeFile(t, config, "network: wonderland\n")

	env := map[string]string{"FOCUS_NETWORK": "venus"}
	if _, stderr, code := runFocus(t, t.TempDir(), env,
		"--config", config, "--network", "testnet", "version"); code != 0 {
		t.Fatalf("flag did not win over config and environment: %s", stderr)
	}
}

func TestE2EExplicitMissingConfigFails(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "nope.yaml")
	_, stderr, code := runFocus(t, t.TempDir(), nil, "--config", missing, "version")
	if code == 0 {
		t.Fatal("a missing --config file exited 0")
	}
	if !strings.Contains(stderr, "reading configuration") {
		t.Errorf("stderr does not explain the config problem: %s", stderr)
	}
}

// TestE2EEncodeWritesAFixture runs the real binary over a payload and checks
// that a fixture appears, so the command is exercised the way a user runs
// it rather than through its internals.
func TestE2EEncodeWritesAFixture(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading the envelope fixture: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "payment.golden.json")

	stdout, stderr, code := runFocus(t, dir, nil,
		"encode", strings.TrimSpace(string(payload)),
		"--type", "TransactionEnvelope", "--out", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "wrote") {
		t.Errorf("encode printed nothing useful:\n%s", stdout)
	}

	fixture, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if fixture.XDR != strings.TrimSpace(string(payload)) {
		t.Error("the stored XDR does not match the payload")
	}
}

// TestE2EEncodeRejectsAnEmptyPayload checks that the command explains itself
// rather than writing an empty fixture.
func TestE2EEncodeRejectsAnEmptyPayload(t *testing.T) {
	t.Parallel()

	_, stderr, code := runFocus(t, t.TempDir(), nil, "encode", "")
	if code == 0 {
		t.Fatal("an empty payload exited 0")
	}
	if !strings.Contains(stderr, "no XDR payload") {
		t.Errorf("stderr does not explain the problem: %s", stderr)
	}
}

// TestE2EVerifyFindsNestedFixtures checks that a suite whose fixtures live
// in subdirectories is checked in full by the real binary.
func TestE2EVerifyFindsNestedFixtures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testdata := filepath.Join(dir, "testdata")
	nested := filepath.Join(testdata, "soroban")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating %s: %v", nested, err)
	}
	writeHandWrittenFixture(t, testdata)
	writeHandWrittenFixture(t, nested)

	stdout, stderr, code := runFocus(t, dir, nil, "verify", "testdata")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "2 checked: 0 ok, 0 drifted, 2 skipped") {
		t.Errorf("the nested fixture was not checked:\n%s", stdout)
	}
}

// writeHandWrittenFixture writes a fixture with no capture provenance, the
// case verify must report as skipped rather than drift.
func writeHandWrittenFixture(t *testing.T, dir string) string {
	t.Helper()

	payload, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading the envelope fixture: %v", err)
	}
	f, err := golden.Build(strings.TrimSpace(string(payload)), "TransactionEnvelope", "hand written", nil)
	if err != nil {
		t.Fatalf("building the hand-written fixture: %v", err)
	}
	path := filepath.Join(dir, "hand.golden.json")
	if err := golden.Save(path, f); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
