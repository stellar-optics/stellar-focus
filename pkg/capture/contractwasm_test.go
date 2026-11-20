package capture_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

const testContractID = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// wasmWithSpec builds a minimal WASM module carrying the given contract
// specification in its contractspec custom section.
func wasmWithSpec(t *testing.T, entries []xdr.ScSpecEntry) []byte {
	t.Helper()

	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(len(entries)))
	for _, entry := range entries {
		raw, err := entry.MarshalBinary()
		if err != nil {
			t.Fatalf("marshalling spec entry: %v", err)
		}
		payload = append(payload, raw...)
	}

	name := []byte("contractspec")
	content := append([]byte{byte(len(name))}, name...)
	content = append(content, payload...)

	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	wasm = append(wasm, 0x00, byte(len(content)))
	wasm = append(wasm, content...)
	return wasm
}

// contractCodeEntry encodes a contract code ledger entry whose stored hash is
// the hash of code, or of a different payload when tampered is true.
func contractCodeEntry(t *testing.T, code []byte, tampered bool) string {
	t.Helper()

	sum := sha256.Sum256(code)
	if tampered {
		sum[0] ^= 0xff
	}
	var hash xdr.Hash
	copy(hash[:], sum[:])

	data := xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeContractCode,
		ContractCode: &xdr.ContractCodeEntry{
			Ext:  xdr.ContractCodeEntryExt{V: 0},
			Hash: hash,
			Code: code,
		},
	}
	raw, err := data.MarshalBinary()
	if err != nil {
		t.Fatalf("marshalling contract code entry: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func contractSource(t *testing.T, entryXDR string) *fakesource.Source {
	t.Helper()

	key, err := capture.ContractCodeKey(testContractID)
	if err != nil {
		t.Fatalf("ContractCodeKey: %v", err)
	}
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	src.Entries[key] = []capture.Record{{
		Name:   "wasm",
		Type:   "ContractCode",
		XDR:    entryXDR,
		Ledger: 42,
	}}
	return src
}

func TestCaptureContractWASMExtractsSpecAndSavesBinary(t *testing.T) {
	fn := xdr.ScSpecFunctionV0{Doc: "", Name: "hello"}
	code := wasmWithSpec(t, []xdr.ScSpecEntry{{
		Kind:       xdr.ScSpecEntryKindScSpecEntryFunctionV0,
		FunctionV0: &fn,
	}})
	src := contractSource(t, contractCodeEntry(t, code, false))

	got, err := capture.CaptureContractWASM(context.Background(), src, testContractID)
	if err != nil {
		t.Fatalf("CaptureContractWASM: %v", err)
	}

	sum := sha256.Sum256(code)
	if got.CodeHash != hex.EncodeToString(sum[:]) {
		t.Errorf("code hash = %q, want %x", got.CodeHash, sum)
	}
	if string(got.Code) != string(code) {
		t.Error("captured WASM bytes do not match the entry")
	}
	if got.Ledger != 42 {
		t.Errorf("ledger = %d, want 42", got.Ledger)
	}
	if len(got.Spec) != 1 || got.Spec[0].FunctionV0 == nil || got.Spec[0].FunctionV0.Name != "hello" {
		t.Fatalf("contract spec was not extracted: %+v", got.Spec)
	}

	dir := t.TempDir()
	metadataPath, wasmPath, err := got.Save(dir)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	savedWASM, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("reading saved WASM: %v", err)
	}
	if string(savedWASM) != string(code) {
		t.Error("saved WASM differs from the captured bytecode")
	}

	metadataData, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("reading metadata: %v", err)
	}
	var metadata capture.WASMMetadata
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		t.Fatalf("parsing metadata: %v", err)
	}
	if metadata.Format != capture.WASMCaptureFormat {
		t.Errorf("metadata format = %q", metadata.Format)
	}
	if metadata.ContractID != testContractID || metadata.Size != len(code) {
		t.Errorf("metadata identity is wrong: %+v", metadata)
	}
	if metadata.WASMFile != filepath.Base(wasmPath) {
		t.Errorf("metadata points at %q, saved binary is %q", metadata.WASMFile, filepath.Base(wasmPath))
	}
	if len(metadata.Spec) != 1 || metadata.Spec[0].Kind == "" || metadata.Spec[0].XDR == "" {
		t.Errorf("metadata has no extracted spec: %+v", metadata.Spec)
	}
}

func TestCaptureContractWASMRejectsAHashMismatch(t *testing.T) {
	code := wasmWithSpec(t, nil)
	src := contractSource(t, contractCodeEntry(t, code, true))

	_, err := capture.CaptureContractWASM(context.Background(), src, testContractID)
	if !errors.Is(err, capture.ErrContractCodeHashMismatch) {
		t.Fatalf("want ErrContractCodeHashMismatch, got: %v", err)
	}
}

func TestVerifyContractExecutable(t *testing.T) {
	code := []byte("some wasm bytes")
	sum := sha256.Sum256(code)
	var hash xdr.Hash
	copy(hash[:], sum[:])

	executable := xdr.ContractExecutable{
		Type:     xdr.ContractExecutableTypeContractExecutableWasm,
		WasmHash: &hash,
	}
	if err := capture.VerifyContractExecutable(code, executable); err != nil {
		t.Fatalf("VerifyContractExecutable rejected matching bytes: %v", err)
	}

	if err := capture.VerifyContractExecutable([]byte("other bytes"), executable); !errors.Is(err, capture.ErrContractCodeHashMismatch) {
		t.Fatalf("want ErrContractCodeHashMismatch, got: %v", err)
	}

	if err := capture.VerifyContractExecutable(code, xdr.ContractExecutable{
		Type: xdr.ContractExecutableTypeContractExecutableStellarAsset,
	}); !errors.Is(err, capture.ErrNotContractCode) {
		t.Fatalf("want ErrNotContractCode for a non-WASM executable, got: %v", err)
	}
}

func TestCaptureWASMWritesBothFiles(t *testing.T) {
	code := wasmWithSpec(t, nil)
	src := contractSource(t, contractCodeEntry(t, code, false))
	dir := t.TempDir()

	_, metadataPath, wasmPath, err := capture.CaptureWASM(context.Background(), src, testContractID, dir)
	if err != nil {
		t.Fatalf("CaptureWASM: %v", err)
	}
	for _, path := range []string{metadataPath, wasmPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was not written: %v", path, err)
		}
	}
}
