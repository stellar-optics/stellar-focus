package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// batchHashes are the hashes of the transactions the fake endpoint knows.
var batchHashes = []string{
	"2b2e276a8f99a2927580eca2c8d665c0adaef9526476f5549269798380a66648",
	"3c3f387b9f0ab3a38690fdb3d9d776d1dbebf063758a66583a6799491b77759",
}

// sourceWithBatch returns a fake endpoint holding one transaction per hash.
func sourceWithBatch(t *testing.T) *fakesource.Source {
	t.Helper()

	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	for _, hash := range batchHashes {
		src.Transactions[hash] = []capture.Record{
			{Name: "envelope", Type: "TransactionEnvelope", XDR: fixtureXDR(t, "tx_failed.env.txt"),
				Ledger: 1234, TxHash: hash},
			{Name: "result", Type: "TransactionResult", XDR: fixtureXDR(t, "tx_failed.res.txt"),
				Ledger: 1234, TxHash: hash},
		}
	}
	return src
}

// writeBatchFile writes a batch file and returns its path.
func writeBatchFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hashes.txt")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing the batch file: %v", err)
	}
	return path
}

// TestCaptureBatchWritesEveryFixture is the ordinary case: every hash in the
// file is captured.
func TestCaptureBatchWritesEveryFixture(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	dir := t.TempDir()
	batch := writeBatchFile(t, strings.Join(batchHashes, "\n")+"\n")

	stdout, stderr, err := run(t, "capture", "tx", "--batch", batch, "--out", dir)
	if err != nil {
		t.Fatalf("capture --batch: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	// Each transaction is named after itself, so two of them in one batch do
	// not overwrite each other.
	for _, name := range []string{
		"2b2e276a_envelope.golden.json",
		"2b2e276a_result.golden.json",
		"3c3f387b_envelope.golden.json",
		"3c3f387b_result.golden.json",
	} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr != nil {
			t.Errorf("%s was not written: %v", name, statErr)
		}
	}
	if !strings.Contains(stdout, "[1/2] captured tx") {
		t.Errorf("no progress was reported:\n%s", stdout)
	}
	if !strings.Contains(stdout, "2 transaction(s): 2 captured") {
		t.Errorf("the summary is missing or wrong:\n%s", stdout)
	}
}

// TestCaptureBatchReportsAMissingTransaction checks that a hash the endpoint
// does not know is reported and does not stop the rest of the batch.
func TestCaptureBatchReportsAMissingTransaction(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	dir := t.TempDir()
	missing := "0000000000000000000000000000000000000000000000000000000000000000"
	batch := writeBatchFile(t, batchHashes[0]+"\n"+missing+"\n")

	stdout, stderr, err := run(t, "capture", "tx", "--batch", batch, "--out", dir)
	if err == nil {
		t.Fatal("the batch reported success although a transaction was missing")
	}
	if code, _ := ExitCode(err); code != ExitFailure {
		t.Errorf("exit code %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "not found tx") {
		t.Errorf("the missing transaction was not reported:\n%s", stderr)
	}
	if !strings.Contains(stdout, "[1/2] captured tx") {
		t.Errorf("the transaction before the missing one was not captured:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 captured, 1 not found") {
		t.Errorf("the summary does not count the missing transaction:\n%s", stdout)
	}
}

// TestCaptureBatchKeepsGoingAfterAFailure checks that one broken entry does
// not abandon the rest of the batch.
func TestCaptureBatchKeepsGoingAfterAFailure(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	dir := t.TempDir()
	batch := writeBatchFile(t, strings.Join(batchHashes, "\n")+"\n")

	// The first transaction cannot be written, because a directory is in the
	// way of its fixture; the second must still be captured.
	if err := os.Mkdir(filepath.Join(dir, "2b2e276a_envelope.golden.json"), 0o755); err != nil {
		t.Fatalf("blocking the first fixture: %v", err)
	}

	stdout, stderr, err := run(t, "capture", "tx", "--batch", batch, "--out", dir)
	if err == nil {
		t.Fatal("the batch reported success although a fixture could not be written")
	}
	if !strings.Contains(stderr, "failed tx") {
		t.Errorf("the failed transaction was not reported:\n%s", stderr)
	}
	if !strings.Contains(stdout, "[2/2] captured tx") {
		t.Errorf("the second transaction was not captured after the first failed:\n%s", stdout)
	}
}

// TestCaptureBatchUsesPerLinePrefixes checks that a line can name the prefix
// of the fixtures it produces.
func TestCaptureBatchUsesPerLinePrefixes(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	dir := t.TempDir()
	batch := writeBatchFile(t, "failed_payment "+batchHashes[0]+"\n"+
		"other:"+batchHashes[1]+"\n")

	if _, stderr, err := run(t, "capture", "tx", "--batch", batch, "--out", dir); err != nil {
		t.Fatalf("capture --batch: %v\nstderr: %s", err, stderr)
	}

	for _, name := range []string{
		"failed_payment_2b2e276a_envelope.golden.json",
		"other_3c3f387b_envelope.golden.json",
	} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr != nil {
			t.Errorf("%s was not written: %v", name, statErr)
		}
	}
}

// TestCaptureBatchUsesTheGlobalPrefix checks that --prefix applies to every
// fixture of a batch.
func TestCaptureBatchUsesTheGlobalPrefix(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	dir := t.TempDir()
	batch := writeBatchFile(t, batchHashes[0]+"\n")

	if _, stderr, err := run(t, "capture", "tx", "--batch", batch, "--out", dir,
		"--prefix", "nightly"); err != nil {
		t.Fatalf("capture --batch: %v\nstderr: %s", err, stderr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "nightly_2b2e276a_envelope.golden.json")); statErr != nil {
		t.Errorf("the prefixed fixture was not written: %v", statErr)
	}
}

// TestCaptureBatchIgnoresCommentsAndBlankLines checks that a batch file can
// be commented.
func TestCaptureBatchIgnoresCommentsAndBlankLines(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	dir := t.TempDir()
	batch := writeBatchFile(t, "# the transactions of the suite\n\n"+
		"# a note about the first one\n"+batchHashes[0]+"\n\n")

	stdout, stderr, err := run(t, "capture", "tx", "--batch", batch, "--out", dir)
	if err != nil {
		t.Fatalf("capture --batch: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "1 transaction(s): 1 captured") {
		t.Errorf("the summary does not count one transaction:\n%s", stdout)
	}
}

// TestCaptureBatchReadsStandardInput checks that a batch can be piped in.
func TestCaptureBatchReadsStandardInput(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	dir := t.TempDir()
	original := stdin
	stdin = strings.NewReader(batchHashes[0] + "\n")
	t.Cleanup(func() { stdin = original })

	stdout, stderr, err := run(t, "capture", "tx", "--batch", "-", "--out", dir)
	if err != nil {
		t.Fatalf("capture --batch -: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "1 transaction(s): 1 captured") {
		t.Errorf("the summary does not count one transaction:\n%s", stdout)
	}
}

// TestCaptureBatchRejectsAnEmptyFile checks that a batch file with nothing
// in it is an error rather than a silent success.
func TestCaptureBatchRejectsAnEmptyFile(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	batch := writeBatchFile(t, "# nothing here\n")

	_, _, err := run(t, "capture", "tx", "--batch", batch, "--out", t.TempDir())
	if err == nil {
		t.Fatal("an empty batch file was accepted")
	}
	if !strings.Contains(err.Error(), "no transaction hashes") {
		t.Errorf("error does not explain the problem: %v", err)
	}
}

// TestCaptureBatchRejectsAMalformedLine checks that a line that is not a
// hash is reported with the line it came from.
func TestCaptureBatchRejectsAMalformedLine(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	batch := writeBatchFile(t, batchHashes[0]+"\ntoo many fields here\n")

	_, _, err := run(t, "capture", "tx", "--batch", batch, "--out", t.TempDir())
	if err == nil {
		t.Fatal("a malformed line was accepted")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error does not name the line: %v", err)
	}
}

// TestCaptureBatchRejectsAHashAndABatch checks that the two ways of naming
// the transactions are not mixed.
func TestCaptureBatchRejectsAHashAndABatch(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	batch := writeBatchFile(t, batchHashes[0]+"\n")

	_, _, err := run(t, "capture", "tx", batchHashes[0], "--batch", batch, "--out", t.TempDir())
	if err == nil {
		t.Fatal("a hash and a batch file were accepted together")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Errorf("error does not explain the problem: %v", err)
	}
}

// TestCaptureBatchNeedsAHashOrABatch checks that the command says what to
// do when given neither.
func TestCaptureBatchNeedsAHashOrABatch(t *testing.T) {
	withFake(t, sourceWithBatch(t))

	_, _, err := run(t, "capture", "tx", "--out", t.TempDir())
	if err == nil {
		t.Fatal("capture tx with no hash and no batch was accepted")
	}
	if !strings.Contains(err.Error(), "--batch") {
		t.Errorf("error does not say how to proceed: %v", err)
	}
}

// TestCaptureBatchRunsConcurrently checks that a batch with several workers
// still captures every transaction.
func TestCaptureBatchRunsConcurrently(t *testing.T) {
	src := sourceWithBatch(t)
	withFake(t, src)

	dir := t.TempDir()
	batch := writeBatchFile(t, strings.Join(batchHashes, "\n")+"\n")

	stdout, stderr, err := run(t, "capture", "tx", "--batch", batch, "--out", dir,
		"--concurrency", "4")
	if err != nil {
		t.Fatalf("capture --batch: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "2 transaction(s): 2 captured") {
		t.Errorf("the summary does not count both transactions:\n%s", stdout)
	}
	if src.Calls != 2 {
		t.Errorf("the endpoint was called %d time(s), want 2", src.Calls)
	}
}

// TestCaptureBatchRejectsABadConcurrency checks that the worker count is
// validated before anything is captured.
func TestCaptureBatchRejectsABadConcurrency(t *testing.T) {
	src := sourceWithBatch(t)
	withFake(t, src)

	batch := writeBatchFile(t, batchHashes[0]+"\n")

	_, _, err := run(t, "capture", "tx", "--batch", batch, "--out", t.TempDir(),
		"--concurrency", "0")
	if err == nil {
		t.Fatal("a concurrency of zero was accepted")
	}
	if !strings.Contains(err.Error(), "concurrency") {
		t.Errorf("error does not explain the problem: %v", err)
	}
	if src.Calls != 0 {
		t.Error("a request was made despite the invalid concurrency")
	}
}
