package cli

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/fakearchive"
	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// archivedTransaction serves an archive holding the failed transaction at
// ledger 100, and returns its URL and the transaction's hash.
func archivedTransaction(t *testing.T) (string, string) {
	t.Helper()

	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(fixtureXDR(t, "tx_failed.env.txt"), &env); err != nil {
		t.Fatal(err)
	}
	var res xdr.TransactionResult
	if err := xdr.SafeUnmarshalBase64(fixtureXDR(t, "tx_failed.res.txt"), &res); err != nil {
		t.Fatal(err)
	}
	hash, err := network.HashTransactionInEnvelope(env, network.TestNetworkPassphrase)
	if err != nil {
		t.Fatal(err)
	}

	archive := fakearchive.New(network.TestNetworkPassphrase)
	archive.AddLedger(
		xdr.TransactionHistoryEntry{LedgerSeq: 100, TxSet: xdr.TransactionSet{Txs: []xdr.TransactionEnvelope{env}}},
		xdr.TransactionHistoryResultEntry{LedgerSeq: 100, TxResultSet: xdr.TransactionResultSet{
			Results: []xdr.TransactionResultPair{{TransactionHash: hash, Result: res}},
		}},
		xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{LedgerSeq: 100}},
	)
	return archive.Serve(t), hex.EncodeToString(hash[:])
}

func TestCaptureFallsBackToTheArchive(t *testing.T) {
	clearFocusEnv(t)
	// RPC has aged the transaction out.
	withFake(t, fakesource.New(capture.NetworkTestnet, "https://fake.example"))
	url, hash := archivedTransaction(t)
	dir := t.TempDir()

	stdout, _, err := run(t, "capture", "tx", hash, "--out", dir, "--archive-url", url, "--ledger", "100")
	if err != nil {
		t.Fatalf("capture error = %v", err)
	}
	if !strings.Contains(stdout, "envelope.golden.json") || !strings.Contains(stdout, "result.golden.json") {
		t.Errorf("capture did not write the archived envelope and result:\n%s", stdout)
	}
	f, err := golden.Load(filepath.Join(dir, "envelope.golden.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if f.Captured.Ledger != 100 || f.Captured.TxHash != hash {
		t.Errorf("provenance = %+v, want ledger 100 and the hash", f.Captured)
	}

	// Verify finds it again through the ledger the fixture recorded.
	stdout, stderr, err := run(t, "verify", dir, "--archive-url", url)
	if err != nil {
		t.Fatalf("verify error = %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "2 checked: 2 ok") {
		t.Errorf("verify did not match the archived fixtures:\n%s", stdout)
	}
}

func TestCaptureFromTheArchiveAsksForTheLedger(t *testing.T) {
	clearFocusEnv(t)
	withFake(t, fakesource.New(capture.NetworkTestnet, "https://fake.example"))
	url, hash := archivedTransaction(t)

	_, _, err := run(t, "capture", "tx", hash, "--out", t.TempDir(), "--archive-url", url)
	if err == nil || !strings.Contains(err.Error(), "--ledger") {
		t.Errorf("error = %v, want a hint to pass --ledger", err)
	}
}
