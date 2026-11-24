package capture_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/fakearchive"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

const archiveLedger = 100

// archivedTx is one transaction stored in a fake archive.
type archivedTx struct {
	hash     string
	envelope string
	result   string
}

func decodeEnvelope(t *testing.T, b64 string) xdr.TransactionEnvelope {
	t.Helper()
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(b64, &env); err != nil {
		t.Fatalf("decoding envelope: %v", err)
	}
	return env
}

func decodeResult(t *testing.T, b64 string) xdr.TransactionResult {
	t.Helper()
	var res xdr.TransactionResult
	if err := xdr.SafeUnmarshalBase64(b64, &res); err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	return res
}

func txHash(t *testing.T, env xdr.TransactionEnvelope) xdr.Hash {
	t.Helper()
	h, err := network.HashTransactionInEnvelope(env, network.TestNetworkPassphrase)
	if err != nil {
		t.Fatalf("hashing envelope: %v", err)
	}
	return h
}

// archiveWithTx serves an archive holding the failed transaction in a
// legacy transaction set at archiveLedger, and the Soroban transaction in a
// generalized, parallel set at the next ledger.
func archiveWithTx(t *testing.T) (*fakearchive.Archive, string, archivedTx, archivedTx) {
	t.Helper()
	archive := fakearchive.New(network.TestNetworkPassphrase)

	legacyEnv := decodeEnvelope(t, envelopeXDR(t))
	legacyRes := decodeResult(t, resultXDR(t))
	legacyHash := txHash(t, legacyEnv)

	// An earlier ledger in the same checkpoint, which must be skipped.
	archive.AddLedger(
		xdr.TransactionHistoryEntry{LedgerSeq: archiveLedger - 1},
		xdr.TransactionHistoryResultEntry{LedgerSeq: archiveLedger - 1},
		xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{LedgerSeq: archiveLedger - 1}},
	)
	archive.AddLedger(
		xdr.TransactionHistoryEntry{
			LedgerSeq: archiveLedger,
			TxSet:     xdr.TransactionSet{Txs: []xdr.TransactionEnvelope{legacyEnv}},
		},
		xdr.TransactionHistoryResultEntry{
			LedgerSeq: archiveLedger,
			TxResultSet: xdr.TransactionResultSet{Results: []xdr.TransactionResultPair{
				{TransactionHash: legacyHash, Result: legacyRes},
			}},
		},
		xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{LedgerSeq: archiveLedger, LedgerVersion: 23}},
	)

	sorobanEnvXDR := fixtureFromFile(t, "tx_soroban.env.txt")
	sorobanEnv := decodeEnvelope(t, sorobanEnvXDR)
	sorobanRes := decodeResult(t, fixtureFromFile(t, "tx_soroban.res.txt"))
	sorobanHash := txHash(t, sorobanEnv)
	set := xdr.GeneralizedTransactionSet{V: 1, V1TxSet: &xdr.TransactionSetV1{Phases: []xdr.TransactionPhase{
		{V: 0, V0Components: &[]xdr.TxSetComponent{}},
		{V: 1, ParallelTxsComponent: &xdr.ParallelTxsComponent{
			ExecutionStages: []xdr.ParallelTxExecutionStage{{{sorobanEnv}}},
		}},
	}}}
	archive.AddLedger(
		xdr.TransactionHistoryEntry{
			LedgerSeq: archiveLedger + 1,
			Ext:       xdr.TransactionHistoryEntryExt{V: 1, GeneralizedTxSet: &set},
		},
		xdr.TransactionHistoryResultEntry{
			LedgerSeq: archiveLedger + 1,
			TxResultSet: xdr.TransactionResultSet{Results: []xdr.TransactionResultPair{
				{TransactionHash: sorobanHash, Result: sorobanRes},
			}},
		},
		xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{LedgerSeq: archiveLedger + 1}},
	)

	legacy := archivedTx{hash: hex.EncodeToString(legacyHash[:]), envelope: envelopeXDR(t), result: resultXDR(t)}
	soroban := archivedTx{
		hash:     hex.EncodeToString(sorobanHash[:]),
		envelope: sorobanEnvXDR,
		result:   fixtureFromFile(t, "tx_soroban.res.txt"),
	}
	return archive, archive.Serve(t), legacy, soroban
}

func TestArchivalSourceReadsATransactionFromItsCheckpoint(t *testing.T) {
	t.Parallel()

	archive, url, legacy, _ := archiveWithTx(t)
	src := capture.NewArchivalSource(capture.NetworkTestnet, url+"/", time.Second)

	records, err := src.TransactionInLedger(context.Background(), legacy.hash, archiveLedger)
	if err != nil {
		t.Fatalf("TransactionInLedger() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want the envelope and result", len(records))
	}
	if records[0].Type != "TransactionEnvelope" || records[0].XDR != legacy.envelope {
		t.Errorf("records[0] = %+v, want the archived envelope", records[0])
	}
	if records[1].Type != "TransactionResult" || records[1].XDR != legacy.result {
		t.Errorf("records[1] = %+v, want the archived result", records[1])
	}
	for _, r := range records {
		if r.Ledger != archiveLedger || r.TxHash != legacy.hash {
			t.Errorf("%s located at ledger %d / %s", r.Name, r.Ledger, r.TxHash)
		}
	}

	// Ledger 100 lives in checkpoint 127 (0x7f).
	want := "/transactions/00/00/00/transactions-0000007f.xdr.gz"
	if !contains(archive.Requests(), want) {
		t.Errorf("requests = %v, want %s", archive.Requests(), want)
	}
	if src.Name() != "history-archive" || src.Endpoint() != url || src.Network() != capture.NetworkTestnet {
		t.Errorf("source reports %s/%s/%s", src.Name(), src.Network(), src.Endpoint())
	}
}

func TestArchivalSourceReadsAGeneralizedParallelTransactionSet(t *testing.T) {
	t.Parallel()

	_, url, _, soroban := archiveWithTx(t)
	src := capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second)

	records, err := src.TransactionInLedger(context.Background(), soroban.hash, archiveLedger+1)
	if err != nil {
		t.Fatalf("TransactionInLedger() error = %v", err)
	}
	if records[0].XDR != soroban.envelope || records[1].XDR != soroban.result {
		t.Error("the Soroban transaction did not come back from the parallel phase")
	}
}

func TestArchivalSourceReportsWhatItCannotFind(t *testing.T) {
	t.Parallel()

	_, url, legacy, _ := archiveWithTx(t)
	src := capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second)
	ctx := context.Background()

	missing := strings.Repeat("ab", 32)
	if _, err := src.TransactionInLedger(ctx, missing, archiveLedger); !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("an unknown hash: error = %v, want ErrNotFound", err)
	}
	// The right hash in the wrong ledger is not found either.
	if _, err := src.TransactionInLedger(ctx, legacy.hash, archiveLedger+1); !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("the wrong ledger: error = %v, want ErrNotFound", err)
	}
	// A checkpoint the archive has not published.
	if _, err := src.TransactionInLedger(ctx, legacy.hash, 1_000_000); !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("an unpublished checkpoint: error = %v, want ErrNotFound", err)
	}
	if _, err := src.TransactionInLedger(ctx, "../etc", archiveLedger); err == nil || errors.Is(err, capture.ErrNotFound) {
		t.Errorf("a malformed hash: error = %v, want it rejected", err)
	}
}

func TestArchivalSourceNeedsALedger(t *testing.T) {
	t.Parallel()

	_, url, legacy, _ := archiveWithTx(t)
	src := capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second)

	for _, fetch := range []func() error{
		func() error { _, err := src.Transaction(context.Background(), legacy.hash); return err },
		func() error { _, err := src.TransactionInLedger(context.Background(), legacy.hash, 0); return err },
	} {
		if err := fetch(); !errors.Is(err, capture.ErrLedgerRequired) {
			t.Errorf("error = %v, want ErrLedgerRequired", err)
		}
	}
}

func TestArchivalSourceDoesNotReadLedgerEntries(t *testing.T) {
	t.Parallel()

	src := capture.NewArchivalSource(capture.NetworkTestnet, "http://127.0.0.1:1", time.Second)
	if _, err := src.LedgerEntry(context.Background(), "AAAA"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("LedgerEntry() error = %v, want ErrUnsupported", err)
	}
}

func TestArchivalSourceReadsALedgerHeader(t *testing.T) {
	t.Parallel()

	_, url, _, _ := archiveWithTx(t)
	src := capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second)

	record, err := src.LedgerHeader(context.Background(), archiveLedger)
	if err != nil {
		t.Fatalf("LedgerHeader() error = %v", err)
	}
	var header xdr.LedgerHeaderHistoryEntry
	if err := xdr.SafeUnmarshalBase64(record.XDR, &header); err != nil {
		t.Fatalf("decoding header: %v", err)
	}
	if record.Type != "LedgerHeaderHistoryEntry" || header.Header.LedgerSeq != archiveLedger || header.Header.LedgerVersion != 23 {
		t.Errorf("header = %+v, want ledger %d", header.Header, archiveLedger)
	}
	if _, err := src.LedgerHeader(context.Background(), archiveLedger+5); !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("a ledger missing from its checkpoint: error = %v, want ErrNotFound", err)
	}
}

func TestArchivalSourceReadsALocalArchiveDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	header := xdr.LedgerHeaderHistoryEntry{
		Hash: xdr.Hash{7, 7, 7},
		Header: xdr.LedgerHeader{
			LedgerSeq:     archiveLedger,
			LedgerVersion: 21,
			ScpValue:      xdr.StellarValue{CloseTime: 1700000000},
		},
	}
	path := filepath.Join(dir, strings.TrimPrefix(fakearchive.Path("ledger", archiveLedger), "/"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fakearchive.Gzip([]xdr.LedgerHeaderHistoryEntry{header}), 0o644); err != nil {
		t.Fatal(err)
	}

	src := capture.NewArchiveSource(capture.NetworkTestnet, dir, time.Second)
	record, err := src.LedgerHeaderAt(context.Background(), archiveLedger)
	if err != nil {
		t.Fatalf("LedgerHeaderAt() error = %v", err)
	}
	var decoded xdr.LedgerHeaderHistoryEntry
	if err := xdr.SafeUnmarshalBase64(record.XDR, &decoded); err != nil {
		t.Fatalf("decoding header: %v", err)
	}
	if decoded.Header.LedgerSeq != archiveLedger || decoded.Header.LedgerVersion != 21 {
		t.Errorf("header = %+v, want ledger %d", decoded.Header, archiveLedger)
	}

	if _, err := src.LedgerHeaderAt(context.Background(), archiveLedger+5); !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("a ledger missing from the directory: error = %v, want ErrNotFound", err)
	}
}

func TestArchivalSourceReadsAndCachesThePassphrase(t *testing.T) {
	t.Parallel()

	archive := fakearchive.New("Standalone Network ; February 2017")
	src := capture.NewArchivalSource("standalone", archive.Serve(t), time.Second)

	for range 2 {
		got, err := src.NetworkPassphrase(context.Background())
		if err != nil {
			t.Fatalf("NetworkPassphrase() error = %v", err)
		}
		if got != "Standalone Network ; February 2017" {
			t.Errorf("NetworkPassphrase() = %q, want the archive's", got)
		}
	}
	if n := len(archive.Requests()); n != 1 {
		t.Errorf("the archive state was fetched %d times, want once", n)
	}
}

func TestArchivalSourceFallsBackToAKnownPassphrase(t *testing.T) {
	t.Parallel()

	archive := fakearchive.New("")
	src := capture.NewArchivalSource(capture.NetworkMainnet, archive.Serve(t), time.Second)

	got, err := src.NetworkPassphrase(context.Background())
	if err != nil {
		t.Fatalf("NetworkPassphrase() error = %v", err)
	}
	if got != network.PublicNetworkPassphrase {
		t.Errorf("NetworkPassphrase() = %q, want the mainnet passphrase", got)
	}
}

func TestArchivalSourceRejectsACorruptStream(t *testing.T) {
	t.Parallel()

	archive, url, legacy, _ := archiveWithTx(t)
	// A record that claims to be 2 GiB long.
	archive.SetFile(fakearchive.Path("results", archiveLedger), gzipBytes(t, []byte{0xff, 0xff, 0xff, 0xff}))

	src := capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second)
	_, err := src.TransactionInLedger(context.Background(), legacy.hash, archiveLedger)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want an oversized record rejected", err)
	}
}

func TestFallbackSourceUsesTheArchiveForOldTransactions(t *testing.T) {
	t.Parallel()

	archive, url, legacy, _ := archiveWithTx(t)
	rpc := sourceWithTransaction(t, "recent")
	src := capture.NewFallbackSource(rpc, capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second))
	ctx := context.Background()

	// RPC still has it: the archive is not asked.
	if _, err := src.Transaction(ctx, "recent"); err != nil {
		t.Fatalf("Transaction(recent) error = %v", err)
	}
	if n := len(archive.Requests()); n != 0 {
		t.Errorf("the archive was asked %d times for a transaction RPC has", n)
	}

	// RPC has aged it out: the archive answers, given the ledger.
	records, err := capture.FetchTransaction(ctx, src, legacy.hash, archiveLedger)
	if err != nil {
		t.Fatalf("FetchTransaction() error = %v", err)
	}
	if records[0].XDR != legacy.envelope {
		t.Error("the fallback did not return the archived envelope")
	}

	// Without the ledger, the error says what is missing.
	if _, err := src.Transaction(ctx, legacy.hash); !errors.Is(err, capture.ErrLedgerRequired) ||
		!errors.Is(err, capture.ErrNotFound) {
		t.Errorf("Transaction() error = %v, want RPC's not-found and the archive's ledger requirement", err)
	}
	if src.Name() != "fake+history-archive" || src.Network() != rpc.Network() || src.Endpoint() != rpc.Endpoint() {
		t.Errorf("fallback reports %s/%s/%s", src.Name(), src.Network(), src.Endpoint())
	}
}

func TestVerifyUsesTheRecordedLedgerWithAnArchive(t *testing.T) {
	t.Parallel()

	_, url, legacy, _ := archiveWithTx(t)
	archival := capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second)
	records, err := archival.TransactionInLedger(context.Background(), legacy.hash, archiveLedger)
	if err != nil {
		t.Fatalf("TransactionInLedger() error = %v", err)
	}
	fixtures, err := capture.Capture(context.Background(), archival, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	src := capture.NewFallbackSource(sourceWithTransaction(t, "recent"), archival)
	for _, f := range fixtures {
		if got := capture.Verify(context.Background(), src, f.Type, f); got.Err != nil || !got.Match {
			t.Errorf("Verify(%s) = %+v, want a match from the archive", f.Type, got)
		}
	}
}

func TestWithPassphraseKeepsTheLedgerHint(t *testing.T) {
	t.Parallel()

	_, url, legacy, _ := archiveWithTx(t)
	fallback := capture.NewFallbackSource(sourceWithTransaction(t, "recent"),
		capture.NewArchivalSource(capture.NetworkTestnet, url, time.Second))
	src := capture.WithPassphrase(fallback, network.TestNetworkPassphrase)

	records, err := capture.FetchTransaction(context.Background(), src, legacy.hash, archiveLedger)
	if err != nil {
		t.Fatalf("FetchTransaction() error = %v", err)
	}
	if records[0].XDR != legacy.envelope {
		t.Error("the wrapped fallback did not reach the archive")
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
