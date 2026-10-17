package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// BundleFormat identifies the bundle manifest layout, so a future change is
// detected rather than silently misread.
const BundleFormat = "focus/bundle/v1"

// BundleSuffix is the manifest filename suffix a bundle directory carries.
const BundleSuffix = ".bundle.json"

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
