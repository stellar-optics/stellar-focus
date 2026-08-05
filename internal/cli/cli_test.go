package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

const testHash = "2b2e276a8f99a2927580eca2c8d665c0adaef9526476f5549269798380a66648"

// withFake swaps the capture source and the clock so the commands run offline
// and produce byte-stable output.
func withFake(t *testing.T, src *fakesource.Source) {
	t.Helper()

	originalSource, originalNow := newSource, now
	newSource = func(string, string, time.Duration) capture.Source { return src }
	now = func() time.Time { return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { newSource, now = originalSource, originalNow })
}

func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err = Execute(args, &out, &errBuf)
	return out.String(), errBuf.String(), err
}

func fixtureXDR(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}

func sourceWithTx(t *testing.T) *fakesource.Source {
	t.Helper()
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	src.Transactions[testHash] = []capture.Record{
		{Name: "envelope", Type: "TransactionEnvelope", XDR: fixtureXDR(t, "tx_failed.env.txt"),
			Ledger: 1234, TxHash: testHash},
		{Name: "result", Type: "TransactionResult", XDR: fixtureXDR(t, "tx_failed.res.txt"),
			Ledger: 1234, TxHash: testHash},
	}
	return src
}

func TestCaptureTxWritesFixtures(t *testing.T) {
	withFake(t, sourceWithTx(t))
	dir := t.TempDir()

	stdout, _, err := run(t, "capture", "tx", testHash, "--out", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, name := range []string{"envelope.golden.json", "result.golden.json"} {
		path := filepath.Join(dir, name)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("%s was not written: %v", path, statErr)
		}
		f, loadErr := golden.Load(path)
		if loadErr != nil {
			t.Fatalf("loading %s: %v", path, loadErr)
		}
		if f.Captured == nil || f.Captured.TxHash != testHash {
			t.Errorf("%s has no usable provenance", name)
		}
	}
	if !strings.Contains(stdout, "wrote") {
		t.Errorf("capture printed nothing useful:\n%s", stdout)
	}
}

func TestCaptureRespectsPrefix(t *testing.T) {
	withFake(t, sourceWithTx(t))
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir, "--prefix", "failed_swap"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "failed_swap_envelope.golden.json")); err != nil {
		t.Errorf("prefixed fixture was not written: %v", err)
	}
}

// TestCaptureRefusesToClobber protects a hand-curated fixture from being
// silently replaced.
func TestCaptureRefusesToClobber(t *testing.T) {
	withFake(t, sourceWithTx(t))
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir); err != nil {
		t.Fatalf("first capture failed: %v", err)
	}

	_, _, err := run(t, "capture", "tx", testHash, "--out", dir)
	if err == nil {
		t.Fatal("second capture overwrote the fixture without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error does not mention how to proceed: %v", err)
	}

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir, "--force"); err != nil {
		t.Errorf("--force did not permit the overwrite: %v", err)
	}
}

func TestCaptureReportsAMissingTransaction(t *testing.T) {
	withFake(t, fakesource.New(capture.NetworkTestnet, "https://fake.example"))

	_, _, err := run(t, "capture", "tx", "nope", "--out", t.TempDir())
	if err == nil {
		t.Fatal("Execute() error = nil for a transaction that does not exist")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error does not say the transaction was not found: %v", err)
	}
}

func TestVerifyReportsMatchingFixtures(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir); err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	stdout, _, err := run(t, "verify", dir)
	if err != nil {
		t.Fatalf("verify reported drift for unchanged fixtures: %v", err)
	}
	if !strings.Contains(stdout, "ok") || !strings.Contains(stdout, "2 checked") {
		t.Errorf("verify output is not a usable summary:\n%s", stdout)
	}
}

// TestVerifyDetectsDrift is the reason the command exists.
func TestVerifyDetectsDrift(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir); err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	// The chain now returns something else for the same hash.
	src.Transactions[testHash] = []capture.Record{
		{Name: "envelope", Type: "TransactionEnvelope", XDR: fixtureXDR(t, "tx_success.env.txt"),
			Ledger: 1234, TxHash: testHash},
		{Name: "result", Type: "TransactionResult", XDR: fixtureXDR(t, "tx_failed.res.txt"),
			Ledger: 1234, TxHash: testHash},
	}

	_, stderr, err := run(t, "verify", dir)
	if err == nil {
		t.Fatal("verify succeeded despite drift")
	}
	if code, _ := ExitCode(err); code == 0 {
		t.Error("verify exited 0 despite drift")
	}
	if !strings.Contains(stderr, "DRIFTED") {
		t.Errorf("verify did not flag the drifted fixture:\n%s", stderr)
	}
	// The drift must be explained, not merely announced.
	if !strings.Contains(stderr, "~") && !strings.Contains(stderr, "SeqNum") {
		t.Errorf("verify did not show what changed:\n%s", stderr)
	}
}

// TestVerifySkipsHandWrittenFixtures checks that an unverifiable fixture is
// reported as skipped rather than quietly passing.
func TestVerifySkipsHandWrittenFixtures(t *testing.T) {
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	withFake(t, src)

	dir := t.TempDir()
	f, err := golden.Build(fixtureXDR(t, "tx_failed.env.txt"), "TransactionEnvelope", "hand written", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "hand.golden.json"), f); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	stdout, _, err := run(t, "verify", dir)
	if err != nil {
		t.Fatalf("verify failed on a hand-written fixture: %v", err)
	}
	if !strings.Contains(stdout, "SKIP") {
		t.Errorf("hand-written fixture was not reported as skipped:\n%s", stdout)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for an unverifiable fixture", src.Calls)
	}
}

func TestVerifyReportsAnEmptyDirectory(t *testing.T) {
	withFake(t, fakesource.New(capture.NetworkTestnet, "https://fake.example"))

	_, _, err := run(t, "verify", t.TempDir())
	if err == nil {
		t.Fatal("verify succeeded with no fixtures to check")
	}
	if !strings.Contains(err.Error(), "no fixture files") {
		t.Errorf("error is not specific: %v", err)
	}
}

func TestUnknownNetworkIsRejectedBeforeAnyRequest(t *testing.T) {
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	withFake(t, src)

	_, _, err := run(t, "capture", "tx", testHash, "--network", "wonderland", "--out", t.TempDir())
	if err == nil {
		t.Fatal("an unknown network was accepted")
	}
	if src.Calls != 0 {
		t.Error("a request was made despite the unknown network")
	}
}

func TestVersionCommand(t *testing.T) {
	stdout, _, err := run(t, "version")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("version printed nothing")
	}
}

func TestBareInvocationPrintsHelp(t *testing.T) {
	stdout, _, err := run(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"capture", "verify", "Usage"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}
