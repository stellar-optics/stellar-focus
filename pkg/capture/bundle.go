package capture

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// BundleFormat identifies the bundle manifest layout, so a future change is
// detected rather than silently misread.
const BundleFormat = "focus/bundle/v1"

// BundleSuffix is the manifest filename suffix a bundle directory carries.
const BundleSuffix = ".bundle.json"

// GitHubActionsWorkflowPath is where GenerateGitHubActionWorkflow's output is
// meant to be checked in, relative to the repository root.
const GitHubActionsWorkflowPath = ".github/workflows/focus-verify.yml"

// BundleMember is one captured transaction inside a bundle manifest.
//
// The fixture files stay ordinary fixtures — each one is a golden file an
// assertion can load directly — and the manifest is what makes them a set:
// the order, the provenance and the shared capture that tied them together.
type BundleMember struct {
	// Name is the member's fixture filename, relative to the bundle
	// directory.
	Name string `json:"name"`
	// Type is the XDR type of the record, e.g. "TransactionEnvelope".
	Type string `json:"type"`
	// TxHash and Ledger locate the member on the chain.
	TxHash string `json:"txHash"`
	Ledger uint32 `json:"ledger,omitempty"`
	// CapturedAt is the RFC3339 capture time shared by the bundle.
	CapturedAt string `json:"capturedAt"`
}

// Bundle is the manifest of a coordinated multi-transaction capture.
type Bundle struct {
	// Format identifies the layout version.
	Format string `json:"format"`
	// Description is a free-text note explaining what the set captures.
	Description string `json:"description,omitempty"`
	// Network and Endpoint record where the whole bundle came from.
	Network  string `json:"network"`
	Endpoint string `json:"endpoint"`
	// Members are the captured transactions, in capture order.
	Members []BundleMember `json:"members"`
}

// BundleRequest describes one coordinated capture: which transactions, in
// which order, under which filename prefix.
type BundleRequest struct {
	// Hashes are the transaction hashes to capture, in the order the
	// bundle should record them.
	Hashes []string
	// Description is recorded in the manifest.
	Description string
	// Prefix names the bundle; it becomes the manifest filename and the
	// fixture filename prefix. An empty prefix means "bundle".
	Prefix string
	// Force overwrites fixture files that already exist.
	Force bool
}

// ManifestName is the manifest filename for a request prefix.
func ManifestName(prefix string) string {
	if prefix == "" {
		prefix = "bundle"
	}
	return prefix + BundleSuffix
}

// bundleFetch is one fetched record paired with the manifest member it
// will become.
type bundleFetch struct {
	record Record
	member BundleMember
}

// GatherBundle fetches every requested transaction and writes the set as
// one coordinated fixture bundle: a manifest plus one fixture file per
// captured record.
//
// Every transaction is fetched before anything is written, so a mid-run
// failure — an endpoint that dies, a hash that is not on the network —
// leaves the directory exactly as it was rather than half-populated. The
// manifest is written last, so a bundle directory without a manifest is
// always an interrupted capture rather than a valid one.
//
// now is passed in rather than read from the clock so that callers — and
// tests — control the timestamp written into the fixtures and manifest.
func GatherBundle(
	ctx context.Context,
	src Source,
	req BundleRequest,
	outDir string,
	now time.Time,
) (*Bundle, []string, error) {
	if len(req.Hashes) == 0 {
		return nil, nil, fmt.Errorf("a bundle needs at least one transaction hash")
	}

	prefix := req.Prefix
	if prefix == "" {
		prefix = "bundle"
	}

	// Fetch phase: all or nothing. A failure here writes nothing.
	var all []bundleFetch
	seen := map[string]bool{}
	for txIndex, hash := range req.Hashes {
		if seen[hash] {
			return nil, nil, fmt.Errorf("transaction %s appears twice in the bundle", hash)
		}
		seen[hash] = true

		records, err := src.Transaction(ctx, hash)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range records {
			all = append(all, bundleFetch{
				record: r,
				member: BundleMember{
					// The transaction index keeps member filenames unique
					// when several transactions contribute the same record
					// name — every transaction has an "envelope".
					Name:       fmt.Sprintf("%s_tx%02d_%s.golden.json", prefix, txIndex+1, r.Name),
					Type:       r.Type,
					TxHash:     r.TxHash,
					Ledger:     r.Ledger,
					CapturedAt: now.UTC().Format(time.RFC3339),
				},
			})
		}
	}

	// Write phase.
	fixtures, err := Capture(ctx, src, recordsOf(all), now)
	if err != nil {
		return nil, nil, err
	}
	bundle := &Bundle{
		Format:      BundleFormat,
		Description: req.Description,
		Network:     src.Network(),
		Endpoint:    src.Endpoint(),
	}

	var written []string
	for i, f := range fixtures {
		member := all[i].member
		path := filepath.Join(outDir, member.Name)

		if !req.Force {
			if _, statErr := os.Stat(path); statErr == nil {
				return nil, written, fmt.Errorf(
					"%s already exists; pass --force to overwrite it, or a different --prefix to write alongside it",
					path)
			}
		}

		if err := golden.Save(path, f); err != nil {
			return nil, written, err
		}
		written = append(written, path)
		bundle.Members = append(bundle.Members, member)
	}

	manifestPath := filepath.Join(outDir, ManifestName(prefix))
	if err := SaveBundle(manifestPath, bundle); err != nil {
		return nil, written, err
	}
	written = append(written, manifestPath)
	return bundle, written, nil
}

// recordsOf projects the fetched view back into the records Capture needs.
func recordsOf(fetched []bundleFetch) []Record {
	out := make([]Record, len(fetched))
	for i, f := range fetched {
		out[i] = f.record
	}
	return out
}

// SaveBundle writes a manifest to disk.
func SaveBundle(path string, b *Bundle) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding bundle manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing bundle manifest %s: %w", path, err)
	}
	return nil
}

// LoadBundle reads a manifest from disk.
func LoadBundle(path string) (*Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading bundle manifest %s: %w", path, err)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parsing bundle manifest %s: %w", path, err)
	}
	if b.Format != BundleFormat {
		return nil, fmt.Errorf(
			"bundle manifest %s has format %q, want %q", path, b.Format, BundleFormat)
	}
	return &b, nil
}

// VerifyBundle re-checks every member of a bundle against the network,
// returning one result per member in manifest order.
//
// A member whose fixture file is missing produces a result with Err set —
// a missing member is a finding, not something to skip silently.
func VerifyBundle(ctx context.Context, src Source, dir string, b *Bundle) []VerifyResult {
	results := make([]VerifyResult, 0, len(b.Members))
	for _, member := range b.Members {
		path := filepath.Join(dir, member.Name)

		f, err := golden.Load(path)
		if err != nil {
			results = append(results, VerifyResult{
				Path: path,
				Err:  fmt.Errorf("loading bundle member: %w", err),
			})
			continue
		}
		results = append(results, Verify(ctx, src, path, f))
	}
	return results
}

// LedgerBatchFormat identifies the multi-ledger manifest layout.
const LedgerBatchFormat = "focus/ledger-batch/v1"

// LedgerBatchEntry is one ledger inside a multi-ledger bundle: its close
// metadata and the header XDR itself, which is what makes the entry
// re-checkable without a fixture file of its own.
type LedgerBatchEntry struct {
	// Sequence is the ledger number.
	Sequence uint32 `json:"sequence"`
	// Hash is the ledger's hash, as the header records it.
	Hash string `json:"hash"`
	// CloseTime is the ledger close time, in seconds since the Unix epoch.
	CloseTime int64 `json:"closeTime"`
	// ProtocolVersion is the protocol the ledger closed under.
	ProtocolVersion uint32 `json:"protocolVersion"`
	// Type is the XDR type of XDR, always LedgerHeaderHistoryEntry.
	Type string `json:"type"`
	// XDR is the base64 LedgerHeaderHistoryEntry.
	XDR string `json:"xdr"`
}

// LedgerBatchBundle is the manifest of a consecutive multi-ledger capture.
//
// Unlike Bundle, which indexes separate fixture files, a ledger batch is one
// self-contained document: a run of headers is small, and keeping them
// together preserves the thing being tested — that the ledgers are
// consecutive and in order.
type LedgerBatchBundle struct {
	// Format identifies the layout version.
	Format string `json:"format"`
	// Network and Endpoint record where the batch came from.
	Network  string `json:"network"`
	Endpoint string `json:"endpoint"`
	// Start and End are the requested range, inclusive.
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
	// CapturedAt is the RFC3339 time the batch was captured.
	CapturedAt string `json:"capturedAt"`
	// Ledgers are the captured headers, in ascending sequence order.
	Ledgers []LedgerBatchEntry `json:"ledgers"`
}

// Save writes the batch manifest to path.
func (b *LedgerBatchBundle) Save(path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding ledger batch manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing ledger batch manifest %s: %w", path, err)
	}
	return nil
}

// LoadLedgerBatchBundle reads a multi-ledger manifest from disk.
func LoadLedgerBatchBundle(path string) (*LedgerBatchBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading ledger batch manifest %s: %w", path, err)
	}
	var b LedgerBatchBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parsing ledger batch manifest %s: %w", path, err)
	}
	if b.Format != LedgerBatchFormat {
		return nil, fmt.Errorf(
			"ledger batch manifest %s has format %q, want %q", path, b.Format, LedgerBatchFormat)
	}
	return &b, nil
}

// VerifyLedgerBatch re-fetches every ledger in the batch and reports which
// ones still match. Results are returned in manifest order.
//
// A ledger the source no longer serves is an error rather than a mismatch:
// a pruned ledger cannot be compared, and saying so is more useful than a
// diff against nothing.
func VerifyLedgerBatch(ctx context.Context, src Source, b *LedgerBatchBundle) []VerifyResult {
	headerSource, ok := src.(LedgerHeaderSource)
	if !ok {
		return []VerifyResult{{Err: fmt.Errorf("%s cannot fetch ledger %d: it is not a LedgerHeaderSource",
			src.Name(), b.Start)}}
	}

	results := make([]VerifyResult, 0, len(b.Ledgers))
	for _, entry := range b.Ledgers {
		result := VerifyResult{Path: fmt.Sprintf("ledger %d", entry.Sequence)}
		record, err := headerSource.LedgerHeaderAt(ctx, entry.Sequence)
		if err != nil {
			result.Err = fmt.Errorf("re-fetching ledger %d: %w", entry.Sequence, err)
			results = append(results, result)
			continue
		}
		if record.XDR == entry.XDR {
			result.Match = true
			results = append(results, result)
			continue
		}

		stored, sErr := lens.DecodeAs(entry.XDR, entry.Type)
		fresh, fErr := lens.DecodeAs(record.XDR, record.Type)
		if sErr == nil && fErr == nil {
			if diff, dErr := lens.Diff(stored, fresh); dErr == nil {
				result.Diff = diff
			}
		}
		results = append(results, result)
	}
	return results
}

// BundleGone counts the results whose fixture is gone from the network —
// the condition worth distinguishing from drift when a bundle is re-checked
// in CI.
func BundleGone(results []VerifyResult) int {
	n := 0
	for _, r := range results {
		if errors.Is(r.Err, ErrNotFound) {
			n++
		}
	}
	return n
}

// ExportCIWorkflow writes the GitHub Actions workflow that verifies this
// bundle's fixtures into dir/.github/workflows/focus-verify.yml and returns
// the path it was written to. A missing directory is created; an existing
// workflow file is overwritten, since the manifest is the source of truth
// for what the workflow checks.
func ExportCIWorkflow(dir string, bundle *Bundle) (string, error) {
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, GitHubActionsWorkflowPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating workflow directory %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(GenerateGitHubActionWorkflow(bundle)), 0o644); err != nil {
		return "", fmt.Errorf("writing GitHub Actions workflow %s: %w", path, err)
	}
	return path, nil
}

// BatchHash computes a deterministic hex hash for batch file content or data.
func BatchHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

// BatchCheckpointPath returns the path to the checkpoint file for the given batch hash.
func BatchCheckpointPath(batchHash string) string {
	return filepath.Join(".focus", fmt.Sprintf("batch_%s.checkpoint", batchHash))
}

// ReadCheckpoint reads the list of already-completed transaction hashes from path.
func ReadCheckpoint(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return make(map[string]bool), nil
		}
		return nil, fmt.Errorf("reading checkpoint %s: %w", path, err)
	}

	completed := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		h := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if h != "" {
			completed[h] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parsing checkpoint %s: %w", path, err)
	}
	return completed, nil
}

// AppendCheckpoint records a completed transaction hash to the checkpoint file.
func AppendCheckpoint(path string, hash string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating checkpoint directory %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening checkpoint %s: %w", path, err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "%s\n", strings.ToLower(strings.TrimSpace(hash))); err != nil {
		return fmt.Errorf("writing checkpoint %s: %w", path, err)
	}
	return f.Sync()
}

// DeleteCheckpoint removes the checkpoint file upon successful batch completion.
func DeleteCheckpoint(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing checkpoint %s: %w", path, err)
	}
	return nil
}
