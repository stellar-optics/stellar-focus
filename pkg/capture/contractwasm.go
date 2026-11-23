package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// ContractWASMSource serves contract code ledger entries.
//
// It is Source narrowed to what a WASM capture needs, so a test can drive the
// capture from a canned entry and nothing here opens a connection. Any Source
// satisfies it.
type ContractWASMSource interface {
	// Name identifies the source in messages, e.g. "soroban-rpc".
	Name() string
	// Network reports which network the source is pointed at.
	Network() string
	// Endpoint reports the URL, for provenance.
	Endpoint() string
	// LedgerEntry fetches one entry by its base64 LedgerKey.
	LedgerEntry(ctx context.Context, key string) ([]Record, error)
}

// ErrContractCodeHashMismatch is returned when a deployed contract's stored
// WASM does not hash to the code hash the ledger records for it.
//
// The two are meant to agree: the hash in the entry is what every reference
// to the contract is keyed on, so a mismatch means the bytes are not the
// bytes the ledger is talking about — a truncated capture, or a source that
// re-encoded them. Reporting it rather than returning the bytes keeps a
// corrupt archive from looking like a valid one.
var ErrContractCodeHashMismatch = errors.New("contract code does not hash to its recorded code hash")

// ErrNotContractCode is returned when a ledger entry turns out not to hold
// contract code at all.
//
// Asking for a contract and getting back an account or a config setting is a
// wrong answer, not an empty one: reporting an empty WASM would make the
// capture look like a deployed contract with no body.
var ErrNotContractCode = errors.New("ledger entry does not hold contract code")

// ContractWASM is one captured deployed contract.
type ContractWASM struct {
	// CodeHash is the code hash the ledger stores for this entry, hex
	// encoded. It is what a contract ID derived from deploying this WASM
	// equals.
	CodeHash string
	// Code is the raw WASM bytes, unmodified.
	Code []byte
	// Ledger is the ledger the entry was read at, when the source reported
	// one.
	Ledger uint32
	// Spec is the contract's embedded interface specification. A module
	// without one has no entries, and that is not an error.
	Spec []xdr.ScSpecEntry
}

// ContractCodeKey encodes the ledger key that names a deployed contract's
// WASM, base64 encoded as getLedgerEntries expects it.
//
// The contract ID is the 32-byte hex hash the contract is addressed by. An ID
// that is not 32 hex bytes is an error rather than a best-effort key: sending
// a malformed key to an endpoint gets back "not found", which would read as
// "this contract is not deployed" and send a capture looking for the wrong
// thing.
func ContractCodeKey(contractID string) (string, error) {
	raw, err := hex.DecodeString(contractID)
	if err != nil {
		return "", fmt.Errorf("decoding contract id %q: %w", contractID, err)
	}
	if len(raw) != len(xdr.Hash{}) {
		return "", fmt.Errorf("contract id %q is %d bytes, want %d", contractID, len(raw), len(xdr.Hash{}))
	}

	var hash xdr.Hash
	copy(hash[:], raw)

	var key xdr.LedgerKey
	if err := key.SetContractCode(hash); err != nil {
		return "", fmt.Errorf("building contract code ledger key: %w", err)
	}

	encoded, err := key.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encoding contract code ledger key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}

// FetchContractWASM fetches the WASM a deployed contract runs and turns it
// into a capture record.
//
// The record carries the whole LedgerEntryData rather than only the code
// bytes, so re-fetching and re-parsing it later checks the entry the network
// returns rather than a projection of it. The code itself stays reachable
// through ParseContractWASM for assertions written against the bytes.
func FetchContractWASM(ctx context.Context, src ContractWASMSource, contractID string) (Record, error) {
	key, err := ContractCodeKey(contractID)
	if err != nil {
		return Record{}, err
	}

	records, err := src.LedgerEntry(ctx, key)
	if err != nil {
		return Record{}, fmt.Errorf("ledger entry for contract %s: %w", contractID, err)
	}
	if len(records) == 0 {
		return Record{}, fmt.Errorf("ledger entry for contract %s: %w", contractID, ErrNotFound)
	}

	record := records[0]
	contract, err := ParseContractWASM(record.XDR)
	if err != nil {
		return Record{}, fmt.Errorf("contract %s: %w", contractID, err)
	}

	// The ledger addresses contract code by the hash of its bytes, so the
	// stored hash naming the same bytes is the cheapest integrity check
	// available before anything is written down.
	if got := sha256.Sum256(contract.Code); hex.EncodeToString(got[:]) != contract.CodeHash {
		return Record{}, fmt.Errorf("contract %s: %w: code is %d bytes, hashed to %x, ledger says %s",
			contractID, ErrContractCodeHashMismatch, len(contract.Code), got, contract.CodeHash)
	}

	return Record{
		Name:        "wasm",
		Type:        "ContractCode",
		XDR:         record.XDR,
		Ledger:      record.Ledger,
		TxHash:      contract.CodeHash,
		Description: fmt.Sprintf("deployed contract %s (%s of WASM)", contractID, wasmSize(len(contract.Code))),
	}, nil
}

// wasmSize describes a code size the way a fixture reader can check it
// without counting bytes by eye.
func wasmSize(size int) string {
	return fmt.Sprintf("%d bytes", size)
}

// ParseContractWASM decodes a base64 LedgerEntryData — the shape
// getLedgerEntries returns — into the code hash and WASM bytes a capture
// asserts on.
//
// An entry that is not contract code is an error, not an empty ContractWASM:
// asking for a contract and getting back an account entry would otherwise
// report an empty WASM as if the contract had been deployed without one.
func ParseContractWASM(b64 string) (ContractWASM, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ContractWASM{}, fmt.Errorf("decoding contract code entry: %w", err)
	}

	var data xdr.LedgerEntryData
	if _, err := xdr.Unmarshal(bytes.NewReader(raw), &data); err != nil {
		return ContractWASM{}, fmt.Errorf("parsing contract code entry: %w", err)
	}

	code, ok := data.GetContractCode()
	if !ok {
		return ContractWASM{}, fmt.Errorf("ledger entry is %s, not contract code: %w",
			data.Type, ErrNotContractCode)
	}

	spec, err := focus.ContractSpec(code.Code)
	if err != nil {
		return ContractWASM{}, fmt.Errorf("extracting contract spec: %w", err)
	}

	return ContractWASM{
		CodeHash: hex.EncodeToString(code.Hash[:]),
		Code:     code.Code,
		Spec:     spec,
	}, nil
}

// VerifyContractExecutable checks that code hashes to the WASM hash recorded in
// a contract's on-chain executable.
//
// The executable in a contract instance is the ledger's own statement of which
// code the contract runs. Hashing the fetched bytes and comparing them with
// that statement is the check the capture must make before writing anything: a
// truncated response or a re-encoding source would otherwise produce a fixture
// that describes a contract the ledger is not talking about.
func VerifyContractExecutable(code []byte, executable xdr.ContractExecutable) error {
	if executable.Type != xdr.ContractExecutableTypeContractExecutableWasm {
		return fmt.Errorf("contract executable is %s, not wasm: %w", executable.Type, ErrNotContractCode)
	}
	want := executable.MustWasmHash()
	got := sha256.Sum256(code)
	if !bytes.Equal(got[:], want[:]) {
		return fmt.Errorf("code is %d bytes and hashes to %x, executable says %x: %w",
			len(code), got, want, ErrContractCodeHashMismatch)
	}
	return nil
}

// WASMSpecEntry is one contract specification entry as stored in capture
// metadata: its kind for a human and its XDR for a machine.
type WASMSpecEntry struct {
	Kind string `json:"kind"`
	XDR  string `json:"xdr"`
}

// WASMCapture is a verified contract WASM capture with its metadata rendered
// and ready to write.
type WASMCapture struct {
	// ContractID is the contract the WASM was captured for.
	ContractID string
	// CodeHash is the hash recorded by the ledger, hex encoded.
	CodeHash string
	// ExecutableHash is the hash from the contract instance's executable,
	// when one was supplied and verified.
	ExecutableHash string
	// Code is the raw WASM bytecode.
	Code []byte
	// Ledger is the ledger the code entry was read at.
	Ledger uint32
	// Spec is the extracted contract interface specification.
	Spec []xdr.ScSpecEntry
}

// CaptureContractWASM fetches a deployed contract's WASM, verifies its code
// hash, extracts its specification and returns a capture ready to save.
//
//nolint:revive // The public name is fixed by issue #239.
func CaptureContractWASM(ctx context.Context, src ContractWASMSource, contractID string) (*WASMCapture, error) {
	return captureContractWASM(ctx, src, contractID, nil)
}

// CaptureContractWASMWithExecutable is CaptureContractWASM with an explicit
// on-chain executable to verify against.
//
// It is for callers that have already fetched the contract instance. The code
// hash stored on the code entry is always checked; this adds the second check
// that the bytes are the ones the instance points at.
//
//nolint:revive // The public name is fixed by issue #239.
func CaptureContractWASMWithExecutable(
	ctx context.Context,
	src ContractWASMSource,
	contractID string,
	executable xdr.ContractExecutable,
) (*WASMCapture, error) {
	return captureContractWASM(ctx, src, contractID, &executable)
}

func captureContractWASM(
	ctx context.Context,
	src ContractWASMSource,
	contractID string,
	executable *xdr.ContractExecutable,
) (*WASMCapture, error) {
	record, err := FetchContractWASM(ctx, src, contractID)
	if err != nil {
		return nil, err
	}

	contract, err := ParseContractWASM(record.XDR)
	if err != nil {
		return nil, fmt.Errorf("contract %s: %w", contractID, err)
	}

	capture := &WASMCapture{
		ContractID: contractID,
		CodeHash:   contract.CodeHash,
		Code:       contract.Code,
		Ledger:     record.Ledger,
		Spec:       contract.Spec,
	}
	if executable != nil {
		if err := VerifyContractExecutable(contract.Code, *executable); err != nil {
			return nil, fmt.Errorf("contract %s: %w", contractID, err)
		}
		executableHash := executable.MustWasmHash()
		capture.ExecutableHash = hex.EncodeToString(executableHash[:])
	}
	return capture, nil
}

// WASMMetadata is the JSON written beside a captured WASM binary.
type WASMMetadata struct {
	Format         string          `json:"format"`
	ContractID     string          `json:"contractId"`
	CodeHash       string          `json:"codeHash"`
	ExecutableHash string          `json:"executableHash,omitempty"`
	Ledger         uint32          `json:"ledger,omitempty"`
	Size           int             `json:"size"`
	WASMFile       string          `json:"wasmFile"`
	Spec           []WASMSpecEntry `json:"spec,omitempty"`
}

// WASMCaptureFormat identifies the metadata layout written by Save.
const WASMCaptureFormat = "focus/wasm/v1"

// Save writes the WASM binary and its JSON metadata side by side in dir, and
// returns the two paths.
//
// The binary is the fixture; the JSON makes it reviewable and carries the code
// hash and extracted specification. Both names derive from the contract ID, so
// re-capturing an unchanged contract rewrites the same two files rather than
// accumulating versions.
func (c *WASMCapture) Save(dir string) (metadataPath, wasmPath string, err error) {
	if c == nil {
		return "", "", fmt.Errorf("saving wasm capture: capture is nil")
	}
	if c.ContractID == "" {
		return "", "", fmt.Errorf("saving wasm capture: contract ID is empty")
	}
	if len(c.Code) == 0 {
		return "", "", fmt.Errorf("saving wasm capture for %s: WASM is empty", c.ContractID)
	}

	metadataPath, wasmPath = c.Paths(dir)

	metadata := WASMMetadata{
		Format:         WASMCaptureFormat,
		ContractID:     c.ContractID,
		CodeHash:       c.CodeHash,
		ExecutableHash: c.ExecutableHash,
		Ledger:         c.Ledger,
		Size:           len(c.Code),
		WASMFile:       filepath.Base(wasmPath),
		Spec:           specMetadata(c.Spec),
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("rendering metadata for %s: %w", c.ContractID, err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating %s: %w", dir, err)
	}
	// The binary is written first: a metadata file that points at a missing
	// WASM is worse than a WASM with no metadata, which a later save repairs.
	if err := os.WriteFile(wasmPath, c.Code, 0o644); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", wasmPath, err)
	}
	if err := os.WriteFile(metadataPath, data, 0o644); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", metadataPath, err)
	}
	return metadataPath, wasmPath, nil
}

// Paths reports the two files Save would write for this capture in dir.
func (c *WASMCapture) Paths(dir string) (metadataPath, wasmPath string) {
	base := safeFileName(c.ContractID)
	return filepath.Join(dir, base+".wasm.json"), filepath.Join(dir, base+".wasm")
}

// specMetadata renders specification entries for the metadata file.
func specMetadata(entries []xdr.ScSpecEntry) []WASMSpecEntry {
	out := make([]WASMSpecEntry, 0, len(entries))
	for _, entry := range entries {
		raw, err := entry.MarshalBinary()
		if err != nil {
			continue
		}
		out = append(out, WASMSpecEntry{
			Kind: entry.Kind.String(),
			XDR:  base64.StdEncoding.EncodeToString(raw),
		})
	}
	return out
}

// safeFileName turns a contract ID into a filename without path separators.
func safeFileName(contractID string) string {
	var b strings.Builder
	for _, r := range contractID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "contract"
	}
	return b.String()
}

// ContractSpecDocument is the JSON structure saved as <tx_hash>.spec.json.
type ContractSpecDocument struct {
	TxHash     string          `json:"txHash,omitempty"`
	ContractID string          `json:"contractId,omitempty"`
	Entries    []WASMSpecEntry `json:"spec"`
}

// ExtractTransactionContractSpecs inspects a transaction's operations and footprint for contract IDs,
// fetches their WASM bytecode using GetContractCode on src, parses the contractspecv0 section,
// and saves the specification alongside the transaction fixture as <tx_hash>.spec.json.
func ExtractTransactionContractSpecs(
	ctx context.Context,
	src ContractWASMSource,
	txHash string,
	env xdr.TransactionEnvelope,
	outDir string,
) ([]string, error) {
	contractIDs := FindTargetContractIDs(env)
	if len(contractIDs) == 0 {
		return nil, nil
	}

	var savedPaths []string
	for _, cid := range contractIDs {
		specEntries, err := FetchAndParseContractSpec(ctx, src, cid)
		if err != nil {
			// Contract might not have code or source couldn't find it; continue
			continue
		}
		if len(specEntries) == 0 {
			continue
		}

		specPath := filepath.Join(outDir, fmt.Sprintf("%s.spec.json", txHash))
		doc := ContractSpecDocument{
			TxHash:     txHash,
			ContractID: cid,
			Entries:    specMetadata(specEntries),
		}

		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("rendering spec json: %w", err)
		}
		data = append(data, '\n')

		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(specPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("writing spec file %s: %w", specPath, err)
		}
		savedPaths = append(savedPaths, specPath)
	}
	return savedPaths, nil
}

// FindTargetContractIDs returns all contract IDs targeted by InvokeHostFunction operations or footprints in env.
func FindTargetContractIDs(env xdr.TransactionEnvelope) []string {
	seen := make(map[string]bool)
	var out []string

	addID := func(raw [32]byte) {
		h := hex.EncodeToString(raw[:])
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}

	var ops []xdr.Operation
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if env.V1 != nil {
			ops = env.V1.Tx.Operations
			if env.V1.Tx.Ext.V == 1 && env.V1.Tx.Ext.SorobanData != nil {
				for _, k := range env.V1.Tx.Ext.SorobanData.Resources.Footprint.ReadOnly {
					if k.Type == xdr.LedgerEntryTypeContractCode && k.ContractCode != nil {
						addID(k.ContractCode.Hash)
					}
					if k.Type == xdr.LedgerEntryTypeContractData && k.ContractData != nil && k.ContractData.Contract.ContractId != nil {
						addID(*k.ContractData.Contract.ContractId)
					}
				}
				for _, k := range env.V1.Tx.Ext.SorobanData.Resources.Footprint.ReadWrite {
					if k.Type == xdr.LedgerEntryTypeContractCode && k.ContractCode != nil {
						addID(k.ContractCode.Hash)
					}
					if k.Type == xdr.LedgerEntryTypeContractData && k.ContractData != nil && k.ContractData.Contract.ContractId != nil {
						addID(*k.ContractData.Contract.ContractId)
					}
				}
			}
		}
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump != nil && env.FeeBump.Tx.InnerTx.V1 != nil {
			ops = env.FeeBump.Tx.InnerTx.V1.Tx.Operations
		}
	}

	for _, op := range ops {
		if op.Body.Type == xdr.OperationTypeInvokeHostFunction && op.Body.InvokeHostFunctionOp != nil {
			hostFn := op.Body.InvokeHostFunctionOp.HostFunction
			if hostFn.Type == xdr.HostFunctionTypeHostFunctionTypeInvokeContract && hostFn.InvokeContract != nil {
				if hostFn.InvokeContract.ContractAddress.ContractId != nil {
					addID(*hostFn.InvokeContract.ContractAddress.ContractId)
				}
			}
		}
	}

	return out
}

// FetchAndParseContractSpec fetches contract WASM code via src and parses its ScSpecEntry entries.
func FetchAndParseContractSpec(ctx context.Context, src ContractWASMSource, contractID string) ([]xdr.ScSpecEntry, error) {
	record, err := FetchContractWASM(ctx, src, contractID)
	if err != nil {
		return nil, err
	}
	contract, err := ParseContractWASM(record.XDR)
	if err != nil {
		return nil, err
	}
	return contract.Spec, nil
}

// LoadContractSpec loads a <tx_hash>.spec.json file into []xdr.ScSpecEntry.
func LoadContractSpec(path string) ([]xdr.ScSpecEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading contract spec: %w", err)
	}
	var doc ContractSpecDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing contract spec JSON: %w", err)
	}

	out := make([]xdr.ScSpecEntry, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		var entry xdr.ScSpecEntry
		if err := xdr.SafeUnmarshalBase64(e.XDR, &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}
