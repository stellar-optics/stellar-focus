package capture_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

func TestNewAccountLedgerKey(t *testing.T) {
	t.Parallel()

	const address = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	key, err := capture.NewAccountLedgerKey(address)
	if err != nil {
		t.Fatalf("NewAccountLedgerKey() error = %v", err)
	}
	if key.Type != 0 {
		t.Fatalf("key type = %v, want account", key.Type)
	}
	encoded, err := capture.EncodeLedgerKey(key)
	if err != nil {
		t.Fatalf("EncodeLedgerKey() error = %v", err)
	}
	if encoded == "" {
		t.Fatal("EncodeLedgerKey() returned an empty string")
	}
}

func TestNewAccountLedgerKeyRejectsInvalidAddress(t *testing.T) {
	t.Parallel()
	if _, err := capture.NewAccountLedgerKey("not-an-account"); err == nil {
		t.Fatal("NewAccountLedgerKey() accepted an invalid StrKey")
	}
}

// capturedAt is fixed so fixtures produced by these tests are byte-stable.
var capturedAt = time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

func envelopeXDR(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func resultXDR(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", "tx_failed.res.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func sourceWithTransaction(t *testing.T, hash string) *fakesource.Source {
	t.Helper()
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	src.Transactions[hash] = []capture.Record{
		{
			Name: "envelope", Type: "TransactionEnvelope", XDR: envelopeXDR(t),
			Ledger: 1234, TxHash: hash, Description: "envelope",
		},
		{
			Name: "result", Type: "TransactionResult", XDR: resultXDR(t),
			Ledger: 1234, TxHash: hash, Description: "result",
		},
	}
	return src
}

func TestCaptureStampsProvenance(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)

	records, err := src.Transaction(context.Background(), hash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}

	fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("captured %d fixtures, want 2", len(fixtures))
	}

	for _, f := range fixtures {
		if f.Captured == nil {
			t.Fatal("fixture has no provenance; a captured fixture must be traceable")
		}
		if f.Captured.Network != capture.NetworkTestnet {
			t.Errorf("network = %q, want testnet", f.Captured.Network)
		}
		if f.Captured.TxHash != hash {
			t.Errorf("txHash = %q, want %q", f.Captured.TxHash, hash)
		}
		if f.Captured.Ledger != 1234 {
			t.Errorf("ledger = %d, want 1234", f.Captured.Ledger)
		}
		if f.Captured.At != "2026-08-05T12:00:00Z" {
			t.Errorf("at = %q, want the supplied timestamp", f.Captured.At)
		}
		if f.Captured.Endpoint != "https://fake.example" {
			t.Errorf("endpoint = %q", f.Captured.Endpoint)
		}
		if f.XDR == "" || f.Decoded == nil {
			t.Error("fixture is missing its XDR or decoded form")
		}
	}
}

// TestCaptureIsDeterministic matters because a capture that produced a
// different file each run would make every refresh look like a change.
func TestCaptureIsDeterministic(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	records, err := src.Transaction(context.Background(), hash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}

	dir := t.TempDir()
	render := func(name string) string {
		fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
		if err != nil {
			t.Fatalf("Capture() error = %v", err)
		}
		path := filepath.Join(dir, name)
		if err := golden.Save(path, fixtures[0]); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		return string(b)
	}

	if render("a.json") != render("b.json") {
		t.Error("two captures of the same data produced different files")
	}
}

func TestVerifyMatches(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	records, _ := src.Transaction(context.Background(), hash)
	fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0])
	if got.Err != nil {
		t.Fatalf("Verify() error = %v", got.Err)
	}
	if !got.Match {
		t.Errorf("Verify() reported a mismatch for unchanged data; diff = %v", got.Diff)
	}
}

func TestCaptureBindsNetworkPassphraseAndVerifyChecksIt(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	src.Passphrase = "Test SDF Network ; September 2015"
	records, err := src.Transaction(context.Background(), hash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if got := fixtures[0].Captured.NetworkPassphrase; got != src.Passphrase {
		t.Fatalf("captured passphrase = %q, want %q", got, src.Passphrase)
	}

	src.Passphrase = "Public Global Stellar Network ; September 2015"
	got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0])
	if got.Err == nil || !strings.Contains(got.Err.Error(), "passphrase mismatch") {
		t.Fatalf("Verify() error = %v, want passphrase mismatch", got.Err)
	}
}

func TestVerifyRefusesAnonymizedFixturesWithoutFetching(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	records, err := src.Transaction(context.Background(), hash)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	records[0].Anonymized = true
	fixtures, err := capture.Capture(context.Background(), src, records[:1], capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	src.Calls = 0

	got := capture.Verify(context.Background(), src, "masked.golden.json", fixtures[0])
	if got.Err == nil || !strings.Contains(got.Err.Error(), "anonymized") {
		t.Fatalf("Verify() error = %v, want anonymized fixture explanation", got.Err)
	}
	if src.Calls != 0 {
		t.Errorf("Verify() fetched %d times for an anonymized fixture", src.Calls)
	}
}

// TestVerifyDetectsDrift covers the point of the command: a fixture that no
// longer matches the chain must be reported, with the change named.
func TestVerifyDetectsDrift(t *testing.T) {
	t.Parallel()

	const hash = "abc123"
	src := sourceWithTransaction(t, hash)
	records, _ := src.Transaction(context.Background(), hash)
	fixtures, err := capture.Capture(context.Background(), src, records, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	// The chain now returns a different envelope for the same hash.
	src.Transactions[hash] = []capture.Record{{
		Name: "envelope", Type: "TransactionEnvelope",
		XDR:    fixtureFromFile(t, "tx_success.env.txt"),
		Ledger: 1234, TxHash: hash,
	}}

	got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0])
	if got.Err != nil {
		t.Fatalf("Verify() error = %v", got.Err)
	}
	if got.Match {
		t.Fatal("Verify() reported a match for changed data")
	}
	if got.Diff == nil || len(got.Diff.Changes) == 0 {
		t.Fatal("Verify() reported no diff explaining the mismatch")
	}
}

// TestVerifyRefusesFixturesWithNoProvenance keeps a hand-written fixture from
// silently passing a check it was never eligible for.
func TestVerifyRefusesFixturesWithNoProvenance(t *testing.T) {
	t.Parallel()

	f, err := golden.Build(envelopeXDR(t), "TransactionEnvelope", "hand written", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	got := capture.Verify(context.Background(), src, "hand.golden.json", f)

	if got.Err == nil {
		t.Fatal("Verify() accepted a fixture with no provenance")
	}
	if got.Match {
		t.Error("Verify() reported a match it could not have checked")
	}
	if !strings.Contains(got.Err.Error(), "provenance") {
		t.Errorf("error does not explain why: %v", got.Err)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s) for an unverifiable fixture", src.Calls)
	}
}

func TestVerifyReportsAMissingTransaction(t *testing.T) {
	t.Parallel()

	src := sourceWithTransaction(t, "abc123")
	records, _ := src.Transaction(context.Background(), "abc123")
	fixtures, _ := capture.Capture(context.Background(), src, records, capturedAt)

	// The transaction has aged out of the endpoint's retention window.
	delete(src.Transactions, "abc123")

	got := capture.Verify(context.Background(), src, "env.golden.json", fixtures[0])
	if got.Err == nil {
		t.Fatal("Verify() succeeded against a transaction that is gone")
	}
	if !errors.Is(got.Err, capture.ErrNotFound) {
		t.Errorf("error does not wrap ErrNotFound: %v", got.Err)
	}
}

func TestResolveEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		network  string
		override string
		want     string
		wantErr  bool
	}{
		{"testnet default", capture.NetworkTestnet, "", capture.DefaultEndpoints[capture.NetworkTestnet], false},
		{"mainnet default", capture.NetworkMainnet, "", capture.DefaultEndpoints[capture.NetworkMainnet], false},
		{"override wins", capture.NetworkTestnet, "http://localhost:8000", "http://localhost:8000", false},
		{"unknown network", "wonderland", "", "", true},
		{"unknown network with override", "wonderland", "http://x", "http://x", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := capture.ResolveEndpoint(tc.network, tc.override)
			if tc.wantErr {
				if err == nil {
					t.Fatal("ResolveEndpoint() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveEndpoint() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveHorizonEndpoint checks that the Horizon defaults are their
// own, since a Horizon endpoint and an RPC endpoint are different
// services.
func TestResolveHorizonEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		network  string
		override string
		want     string
		wantErr  bool
	}{
		{"testnet default", capture.NetworkTestnet, "", capture.DefaultHorizonEndpoints[capture.NetworkTestnet], false},
		{"mainnet default", capture.NetworkMainnet, "", capture.DefaultHorizonEndpoints[capture.NetworkMainnet], false},
		{"override wins", capture.NetworkTestnet, "http://localhost:8000", "http://localhost:8000", false},
		{"unknown network", "wonderland", "", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := capture.ResolveHorizonEndpoint(tc.network, tc.override)
			if tc.wantErr {
				if err == nil {
					t.Fatal("ResolveHorizonEndpoint() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveHorizonEndpoint() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveHorizonEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDefaultIsTestnet guards against a tool that reaches for mainnet unless
// told otherwise.
func TestDefaultIsTestnet(t *testing.T) {
	t.Parallel()

	if !strings.Contains(capture.DefaultEndpoints[capture.NetworkTestnet], "testnet") {
		t.Error("the testnet endpoint does not look like testnet")
	}
}

func fixtureFromFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}

// filterAccount is a valid, empty account used by the envelopes the filter
// tests build.
const filterAccount = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"

// paymentEnvelopeXDR builds a classic V1 envelope carrying ops payments.
func paymentEnvelopeXDR(t *testing.T, ops int) string {
	t.Helper()

	operations := make([]xdr.Operation, 0, ops)
	for i := 0; i < ops; i++ {
		operations = append(operations, xdr.Operation{
			Body: xdr.OperationBody{
				Type: xdr.OperationTypePayment,
				PaymentOp: &xdr.PaymentOp{
					Destination: xdr.MustMuxedAddress(filterAccount),
					Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
					Amount:      1,
				},
			},
		})
	}

	encoded, err := xdr.MarshalBase64(xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: xdr.MustMuxedAddress(filterAccount),
				Fee:           xdr.Uint32(100 * len(operations)),
				SeqNum:        1,
				Operations:    operations,
			},
		},
	})
	if err != nil {
		t.Fatalf("encoding the payment envelope: %v", err)
	}
	return encoded
}

// successResultXDR builds the result of a transaction that succeeded.
func successResultXDR(t *testing.T) string {
	t.Helper()

	results := []xdr.OperationResult{}
	encoded, err := xdr.MarshalBase64(xdr.TransactionResult{
		FeeCharged: 100,
		Result: xdr.TransactionResultResult{
			Code:    xdr.TransactionResultCodeTxSuccess,
			Results: &results,
		},
	})
	if err != nil {
		t.Fatalf("encoding the success result: %v", err)
	}
	return encoded
}

// sorobanEnvelopeXDR returns the envelope of a Soroban transaction.
func sorobanEnvelopeXDR(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", "tx_soroban.env.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// recordsOf returns the transaction's records in the order a source returns
// them.
func recordsOf(hash string, envelope, result string) []capture.Record {
	records := []capture.Record{
		{Name: "envelope", Type: "TransactionEnvelope", XDR: envelope, Ledger: 1234, TxHash: hash},
	}
	if result != "" {
		records = append(records, capture.Record{
			Name: "result", Type: "TransactionResult", XDR: result, Ledger: 1234, TxHash: hash,
		})
	}
	return records
}

// hashesOf lists the transactions the kept records belong to.
func hashesOf(records []capture.Record) []string {
	var out []string
	for _, r := range records {
		if len(out) == 0 || out[len(out)-1] != r.TxHash {
			out = append(out, r.TxHash)
		}
	}
	return out
}

func TestFilterRecordsWithoutFiltersKeepsEverything(t *testing.T) {
	t.Parallel()

	records := recordsOf("one", paymentEnvelopeXDR(t, 1), "")
	kept, err := capture.FilterRecords(records, capture.FilterOptions{})
	if err != nil {
		t.Fatalf("FilterRecords() error = %v", err)
	}
	if len(kept) != len(records) {
		t.Fatalf("kept %d records, want all %d", len(kept), len(records))
	}
}

func TestFilterRecordsMinOps(t *testing.T) {
	t.Parallel()

	records := append(
		recordsOf("small", paymentEnvelopeXDR(t, 1), resultXDR(t)),
		recordsOf("multi", paymentEnvelopeXDR(t, 5), resultXDR(t))...,
	)

	kept, err := capture.FilterRecords(records, capture.FilterOptions{MinOps: 3})
	if err != nil {
		t.Fatalf("FilterRecords() error = %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d records, want the 2 records of one transaction", len(kept))
	}
	for _, r := range kept {
		if r.TxHash != "multi" {
			t.Errorf("kept a %s record of %q, want only the transaction with 5 operations",
				r.Type, r.TxHash)
		}
	}
}

func TestFilterRecordsOnlySoroban(t *testing.T) {
	t.Parallel()

	records := append(
		recordsOf("classic", paymentEnvelopeXDR(t, 1), ""),
		recordsOf("soroban", sorobanEnvelopeXDR(t), "")...,
	)

	kept, err := capture.FilterRecords(records, capture.FilterOptions{OnlySoroban: true})
	if err != nil {
		t.Fatalf("FilterRecords() error = %v", err)
	}
	if got := hashesOf(kept); len(got) != 1 || got[0] != "soroban" {
		t.Errorf("kept the transactions %v, want only %q", got, "soroban")
	}
}

func TestFilterRecordsFailedOnly(t *testing.T) {
	t.Parallel()

	records := append(
		recordsOf("ok", paymentEnvelopeXDR(t, 1), successResultXDR(t)),
		recordsOf("failed", paymentEnvelopeXDR(t, 1), resultXDR(t))...,
	)

	kept, err := capture.FilterRecords(records, capture.FilterOptions{FailedOnly: true})
	if err != nil {
		t.Fatalf("FilterRecords() error = %v", err)
	}
	if got := hashesOf(kept); len(got) != 1 || got[0] != "failed" {
		t.Errorf("kept the transactions %v, want only %q", got, "failed")
	}
}

// TestFilterRecordsCompound covers the acceptance case: --only-soroban
// --failed-only keeps only the Soroban transactions that failed.
func TestFilterRecordsCompound(t *testing.T) {
	t.Parallel()

	records := append(append(append(
		recordsOf("classic-ok", paymentEnvelopeXDR(t, 4), successResultXDR(t)),
		recordsOf("classic-failed", paymentEnvelopeXDR(t, 4), resultXDR(t))...),
		recordsOf("soroban-ok", sorobanEnvelopeXDR(t), successResultXDR(t))...),
		recordsOf("soroban-failed", sorobanEnvelopeXDR(t), resultXDR(t))...,
	)

	kept, err := capture.FilterRecords(records, capture.FilterOptions{
		MinOps:      1,
		OnlySoroban: true,
		FailedOnly:  true,
	})
	if err != nil {
		t.Fatalf("FilterRecords() error = %v", err)
	}
	if got := hashesOf(kept); len(got) != 1 || got[0] != "soroban-failed" {
		t.Errorf("kept the transactions %v, want only %q", got, "soroban-failed")
	}
	if len(kept) != 2 {
		t.Errorf("kept %d records, want both records of the failing Soroban transaction", len(kept))
	}
}

// TestFilterRecordsDropsTransactionsMissingTheRecordAFilterNeeds checks that
// a transaction that cannot be judged is skipped rather than kept.
func TestFilterRecordsDropsTransactionsMissingTheRecordAFilterNeeds(t *testing.T) {
	t.Parallel()

	records := recordsOf("no-result", sorobanEnvelopeXDR(t), "")

	kept, err := capture.FilterRecords(records, capture.FilterOptions{FailedOnly: true})
	if err != nil {
		t.Fatalf("FilterRecords() error = %v", err)
	}
	if len(kept) != 0 {
		t.Errorf("kept %d records of a transaction with no result, want none", len(kept))
	}
}

// TestFilterRecordsKeepsRecordsThatAreNotTransactions checks that a ledger
// entry captured alongside transactions is not dropped by a transaction
// filter.
func TestFilterRecordsKeepsRecordsThatAreNotTransactions(t *testing.T) {
	t.Parallel()

	entry := capture.Record{Name: "entry", Type: "LedgerEntry", XDR: "AAAA", Ledger: 1234}
	records := append([]capture.Record{entry}, recordsOf("classic", paymentEnvelopeXDR(t, 1), "")...)

	kept, err := capture.FilterRecords(records, capture.FilterOptions{OnlySoroban: true})
	if err != nil {
		t.Fatalf("FilterRecords() error = %v", err)
	}
	if len(kept) != 1 || kept[0].Type != "LedgerEntry" {
		t.Errorf("kept %d records, want only the ledger entry", len(kept))
	}
}
