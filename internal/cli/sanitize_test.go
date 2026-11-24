package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

const leakedMemo = "cust-4471@example.com"

// sourceWithSecrets serves a transaction whose memo and data entry carry
// data that must never reach a fixture.
func sourceWithSecrets(t *testing.T) (*fakesource.Source, string) {
	t.Helper()

	kp, err := keypair.FromRawSeed([32]byte{9, 9, 9})
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	seed := xdr.DataValue(kp.Seed())
	memo := leakedMemo
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: xdr.MustMuxedAddress(kp.Address()),
			Fee:           100,
			SeqNum:        1,
			Memo:          xdr.Memo{Type: xdr.MemoTypeMemoText, Text: &memo},
			Operations: []xdr.Operation{{Body: xdr.OperationBody{
				Type:         xdr.OperationTypeManageData,
				ManageDataOp: &xdr.ManageDataOp{DataName: "backup", DataValue: &seed},
			}}},
		}},
	}
	encoded, err := xdr.MarshalBase64(env)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	src.Transactions[testHash] = []capture.Record{
		{Name: "envelope", Type: "TransactionEnvelope", XDR: encoded, Ledger: 1234, TxHash: testHash},
	}
	return src, kp.Seed()
}

func TestCaptureSanitizeMasksEverything(t *testing.T) {
	clearFocusEnv(t)
	src, seed := sourceWithSecrets(t)
	withFake(t, src)
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir, "--sanitize"); err != nil {
		t.Fatalf("capture error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "envelope.golden.json"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	file := string(raw)
	for _, leaked := range []string{leakedMemo, seed} {
		if strings.Contains(file, leaked) {
			t.Errorf("the fixture still contains %q", leaked)
		}
	}
	if !strings.Contains(file, capture.RedactedMemo) || !strings.Contains(file, `"sanitized": true`) {
		t.Errorf("the fixture is not visibly sanitized:\n%s", file)
	}

	// A sanitized fixture cannot match the chain, so verify skips it rather
	// than reporting drift or an error.
	stdout, _, err := run(t, "verify", dir)
	if err != nil {
		t.Fatalf("verify error = %v", err)
	}
	if !strings.Contains(stdout, "SKIP") || !strings.Contains(stdout, "1 skipped") {
		t.Errorf("verify did not skip the sanitized fixture:\n%s", stdout)
	}
}

func TestCaptureMaskMemoAloneLeavesDataAlone(t *testing.T) {
	clearFocusEnv(t)
	src, _ := sourceWithSecrets(t)
	withFake(t, src)
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir, "--mask-memo"); err != nil {
		t.Fatalf("capture error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "envelope.golden.json"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	if strings.Contains(string(raw), leakedMemo) {
		t.Error("--mask-memo left the memo in the fixture")
	}
	if strings.Contains(string(raw), capture.RedactedData) || strings.Contains(string(raw), capture.RedactedSecretKey) {
		t.Error("--mask-memo also masked the data entry")
	}
}

func TestCaptureWithoutSanitizeKeepsTheData(t *testing.T) {
	clearFocusEnv(t)
	src, _ := sourceWithSecrets(t)
	withFake(t, src)
	dir := t.TempDir()

	if _, _, err := run(t, "capture", "tx", testHash, "--out", dir); err != nil {
		t.Fatalf("capture error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "envelope.golden.json"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	if !strings.Contains(string(raw), leakedMemo) || strings.Contains(string(raw), `"sanitized"`) {
		t.Error("a capture without sanitize flags changed the data")
	}
}
