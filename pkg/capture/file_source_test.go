package capture_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

const dumpHash = "2b2e276a8f99a2927580eca2c8d665c0adaef9526476f5549269798380a66648"

func writeDump(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestFileSourceReadsATransactionDump(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env, res := envelopeXDR(t), resultXDR(t)
	writeDump(t, dir, dumpHash+".envelope.xdr", []byte(env+"\n"))
	writeDump(t, dir, dumpHash+".result.xdr", []byte(res))

	src := capture.NewFileSource(dir).ForNetwork(capture.NetworkMainnet)
	records, err := src.Transaction(context.Background(), dumpHash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want the envelope and result (no meta was dumped)", len(records))
	}
	if records[0].Type != "TransactionEnvelope" || records[0].XDR != env || records[0].Name != "envelope" {
		t.Errorf("records[0] = %+v, want the envelope", records[0])
	}
	if records[1].Type != "TransactionResult" || records[1].XDR != res || records[1].Name != "result" {
		t.Errorf("records[1] = %+v, want the result", records[1])
	}
	for _, r := range records {
		if r.TxHash != dumpHash {
			t.Errorf("%s TxHash = %q, want %q", r.Name, r.TxHash, dumpHash)
		}
	}
	if src.Network() != capture.NetworkMainnet || src.Endpoint() != dir || src.Name() != "file" {
		t.Errorf("source reports %s/%s/%s", src.Name(), src.Network(), src.Endpoint())
	}
}

func TestFileSourceReadsRawXDRBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env := envelopeXDR(t)
	raw, err := base64.StdEncoding.DecodeString(env)
	if err != nil {
		t.Fatalf("decoding the envelope: %v", err)
	}
	writeDump(t, dir, dumpHash+".envelope.xdr", raw)

	// An upper-case hash names the same dump.
	records, err := capture.NewFileSource(dir).Transaction(context.Background(), strings.ToUpper(dumpHash))
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	if len(records) != 1 || records[0].XDR != env {
		t.Errorf("records = %+v, want the envelope re-encoded as base64", records)
	}
}

func TestFileSourceReportsAMissingTransactionAsNotFound(t *testing.T) {
	t.Parallel()

	_, err := capture.NewFileSource(t.TempDir()).Transaction(context.Background(), dumpHash)
	if !errors.Is(err, capture.ErrNotFound) {
		t.Fatalf("Transaction() error = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), dumpHash+".{envelope,result,meta}.xdr") {
		t.Errorf("error %q does not say which files it looked for", err)
	}
}

func TestFileSourceRejectsAHashThatIsNotAHash(t *testing.T) {
	t.Parallel()

	for _, hash := range []string{"../../etc/passwd", "abc123", ""} {
		_, err := capture.NewFileSource(t.TempDir()).Transaction(context.Background(), hash)
		if err == nil || errors.Is(err, capture.ErrNotFound) {
			t.Errorf("Transaction(%q) error = %v, want a rejected hash", hash, err)
		}
	}
}

func TestFileSourceRejectsAnEmptyDump(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDump(t, dir, dumpHash+".envelope.xdr", []byte("  \n"))
	_, err := capture.NewFileSource(dir).Transaction(context.Background(), dumpHash)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("Transaction() error = %v, want an empty-file error", err)
	}
}

func TestFileSourceReadsALedgerEntryDump(t *testing.T) {
	t.Parallel()

	key, err := capture.NewAccountLedgerKey("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7")
	if err != nil {
		t.Fatalf("NewAccountLedgerKey() error = %v", err)
	}
	encoded, err := capture.EncodeLedgerKey(key)
	if err != nil {
		t.Fatalf("EncodeLedgerKey() error = %v", err)
	}
	name, err := capture.LedgerEntryFileName(encoded)
	if err != nil {
		t.Fatalf("LedgerEntryFileName() error = %v", err)
	}

	dir := t.TempDir()
	writeDump(t, dir, name, []byte("AAAAAA=="))

	records, err := capture.NewFileSource(dir).LedgerEntry(context.Background(), encoded)
	if err != nil {
		t.Fatalf("LedgerEntry() error = %v", err)
	}
	if len(records) != 1 || records[0].Type != "LedgerEntryData" || records[0].XDR != "AAAAAA==" {
		t.Errorf("records = %+v, want the dumped entry", records)
	}

	if _, err := capture.NewFileSource(t.TempDir()).LedgerEntry(context.Background(), encoded); !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("LedgerEntry() on an empty directory error = %v, want ErrNotFound", err)
	}
}

func TestLedgerEntryFileNameHasNoPathSeparator(t *testing.T) {
	t.Parallel()

	got, err := capture.LedgerEntryFileName("ab+/cd==")
	if err != nil {
		t.Fatalf("LedgerEntryFileName() error = %v", err)
	}
	if got != "ab-_cd==.entry.xdr" {
		t.Errorf("LedgerEntryFileName() = %q, want the URL-safe key", got)
	}
	if _, err := capture.LedgerEntryFileName("../not base64"); err == nil {
		t.Error("LedgerEntryFileName() accepted a key that is not base64")
	}
}

func TestFileSourceHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := capture.NewFileSource(t.TempDir()).Transaction(ctx, dumpHash); !errors.Is(err, context.Canceled) {
		t.Errorf("Transaction() error = %v, want context.Canceled", err)
	}
}

// TestFileSourceVerifiesOffline is the point of the source: a fixture
// captured from the network is re-checked against a dump, with no network.
func TestFileSourceVerifiesOffline(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	live := sourceWithTransaction(t, hash)
	records, err := live.Transaction(context.Background(), hash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	fixtures, err := capture.Capture(context.Background(), live, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	f := fixtures[0]
	f.Captured.TxHash = dumpHash

	dir := t.TempDir()
	writeDump(t, dir, dumpHash+".envelope.xdr", []byte(f.XDR))

	// The dumps are named for the network they came from, so the source can
	// vouch for the passphrase the fixture is bound to.
	src := capture.NewFileSource(dir).ForNetwork(capture.NetworkTestnet)
	got := capture.Verify(context.Background(), src, "envelope.golden.json", f)
	if got.Err != nil || !got.Match {
		t.Errorf("Verify() = %+v, want a match against the dump", got)
	}
}
