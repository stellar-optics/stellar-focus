package capture

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileSource replays transactions and ledger entries from a directory of
// XDR dumps, so fixtures can be captured and verified with no network at
// all — on an air-gapped CI runner, say, or offline.
//
// A transaction is read from up to three files named by its hash:
//
//	{hash}.envelope.xdr
//	{hash}.result.xdr
//	{hash}.meta.xdr
//
// and a ledger entry from one file named by its key:
//
//	{encoded_key}.entry.xdr
//
// where encoded_key is the base64 LedgerKey in URL-safe base64, so the name
// holds no path separator (see LedgerEntryFileName).
//
// Each file holds either the base64 XDR, as RPC returns it, or the raw XDR
// bytes. Files that are missing are skipped, so a dump with only an envelope
// serves only an envelope.
type FileSource struct {
	dir     string
	network string
}

// NewFileSource returns a Source that reads XDR dumps from dir.
//
// The source reports NetworkCustom until ForNetwork names the network the
// dumps were taken from.
func NewFileSource(dir string) *FileSource {
	return &FileSource{dir: filepath.Clean(dir), network: NetworkCustom}
}

// ForNetwork returns a copy of the source that reports the given network,
// which is recorded in the provenance of anything captured from it.
func (s *FileSource) ForNetwork(network string) *FileSource {
	out := *s
	out.network = network
	return &out
}

// Name implements Source.
func (s *FileSource) Name() string { return "file" }

// Network implements Source.
func (s *FileSource) Network() string { return s.network }

// Endpoint implements Source, reporting the directory the dumps are read
// from.
func (s *FileSource) Endpoint() string { return s.dir }

// fileSourceParts are the transaction parts a dump may hold, in the order
// RPCSource returns them.
var fileSourceParts = []struct {
	name, xdrType string
}{
	{"envelope", "TransactionEnvelope"},
	{"result", "TransactionResult"},
	{"meta", "TransactionMeta"},
}

// Transaction implements Source, reading the envelope, result and meta
// dumped for hash.
func (s *FileSource) Transaction(ctx context.Context, hash string) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if decoded, err := hex.DecodeString(hash); err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("transaction hash %q is not 64 hex characters", hash)
	}
	hash = strings.ToLower(hash)

	var out []Record
	for _, part := range fileSourceParts {
		path := filepath.Join(s.dir, hash+"."+part.name+".xdr")
		payload, err := readXDRFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Record{
			Name:        part.name,
			Type:        part.xdrType,
			XDR:         payload,
			TxHash:      hash,
			Description: fmt.Sprintf("%s of transaction %s", part.name, hash),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("transaction %s: %w (no %s.{envelope,result,meta}.xdr in %s)",
			hash, ErrNotFound, hash, s.dir)
	}
	return out, nil
}

// LedgerEntry implements Source, reading the entry dumped for key.
func (s *FileSource) LedgerEntry(ctx context.Context, key string) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := LedgerEntryFileName(key)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, name)
	payload, err := readXDRFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("ledger key %s: %w (no %s in %s)", key, ErrNotFound, name, s.dir)
	}
	if err != nil {
		return nil, err
	}
	return []Record{{
		Name:        "entry",
		Type:        "LedgerEntryData",
		XDR:         payload,
		Description: "ledger entry " + key,
	}}, nil
}

// LedgerEntryFileName returns the name FileSource reads a ledger entry from:
// the base64 key in URL-safe base64, followed by ".entry.xdr".
func LedgerEntryFileName(key string) (string, error) {
	if _, err := base64.StdEncoding.DecodeString(key); err != nil {
		return "", fmt.Errorf("ledger key %q is not base64: %w", key, err)
	}
	return base64URLSafe.Replace(key) + ".entry.xdr", nil
}

// base64URLSafe turns standard base64 into its URL- and filename-safe form.
var base64URLSafe = strings.NewReplacer("+", "-", "/", "_")

// readXDRFile reads one dump, returning its payload as base64 XDR whether
// the file held base64 text or raw bytes.
func readXDRFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	if _, err := base64.StdEncoding.DecodeString(text); err == nil {
		return text, nil
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
