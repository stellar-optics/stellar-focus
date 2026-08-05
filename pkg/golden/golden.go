// Package golden provides golden-file testing for Stellar XDR.
//
// Golden files here are readable JSON rather than opaque blobs. A fixture
// stores the base64 XDR as the source of truth alongside its decoded form, so
// when a golden file changes, the pull-request diff shows which field changed
// rather than one unreadable string being replaced by another. That is the
// whole point: a golden test nobody can review is a golden test nobody
// updates carefully.
//
// Files are rewritten by running the test suite with -focus.update:
//
//	go test ./... -focus.update
//
// The flag is namespaced so it never collides with a project's own -update.
package golden

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// updateFlag is registered on the default command line so any test binary
// importing this package accepts -focus.update.
var updateFlag = flag.Bool("focus.update", false,
	"rewrite stellar-focus golden files instead of comparing against them")

// Update reports whether the suite was run with -focus.update.
//
// Tests rarely need this directly — Assert already honours the flag — but it
// is exported for suites that generate fixtures in some other way and want to
// follow the same convention.
func Update() bool { return *updateFlag }

// FormatVersion identifies the golden file layout, so a future change can be
// detected rather than silently misread.
const FormatVersion = "focus/v1"

// Fixture is the on-disk form of a golden file.
//
// Field order in the struct is the order in the file, chosen so a diff shows
// the identifying information first and the large decoded tree last.
type Fixture struct {
	// Format identifies the layout version.
	Format string `json:"format"`
	// Type is the XDR type, e.g. "TransactionEnvelope".
	Type string `json:"type"`
	// Description is a free-text note explaining what this fixture is for.
	Description string `json:"description,omitempty"`
	// XDR is the base64 payload and the source of truth for comparisons.
	XDR string `json:"xdr"`
	// Captured records where the fixture came from, when it was captured
	// from a live network rather than written by hand.
	Captured *Provenance `json:"captured,omitempty"`
	// Decoded is the human-readable form, present so a reviewer can see in a
	// pull request what actually changed. It is never used for comparison.
	Decoded json.RawMessage `json:"decoded,omitempty"`
}

// Provenance records where a captured fixture came from, so a stale fixture
// can be traced and re-verified.
type Provenance struct {
	Network  string `json:"network"`
	Ledger   uint32 `json:"ledger,omitempty"`
	TxHash   string `json:"txHash,omitempty"`
	At       string `json:"at"`
	Endpoint string `json:"endpoint,omitempty"`
}

// Golden compares values against golden files on disk.
type Golden struct {
	t       focus.TB
	dir     string
	suffix  string
	opts    []focus.Option
	desc    string
	typeHnt string
}

// Opt configures a Golden.
type Opt func(*Golden)

// WithDir sets the directory holding golden files. The default is "testdata".
func WithDir(dir string) Opt {
	return func(g *Golden) { g.dir = dir }
}

// WithSuffix sets the file suffix. The default is ".golden.json".
func WithSuffix(s string) Opt {
	return func(g *Golden) { g.suffix = s }
}

// WithOptions applies focus assertion options — ignore and normalize rules —
// to every comparison this Golden makes.
func WithOptions(opts ...focus.Option) Opt {
	return func(g *Golden) { g.opts = append(g.opts, opts...) }
}

// WithDescription sets the description written into new golden files.
func WithDescription(d string) Opt {
	return func(g *Golden) { g.desc = d }
}

// WithType forces the XDR type rather than detecting it, which is worth doing
// for short payloads where detection is ambiguous.
func WithType(typeName string) Opt {
	return func(g *Golden) { g.typeHnt = typeName }
}

// New returns a Golden bound to a test.
func New(t focus.TB, opts ...Opt) *Golden {
	g := &Golden{t: t, dir: "testdata", suffix: ".golden.json"}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Path returns the file a given name maps to.
func (g *Golden) Path(name string) string {
	if !strings.HasSuffix(name, g.suffix) {
		name += g.suffix
	}
	return filepath.Join(g.dir, name)
}

// Assert compares a value against the named golden file.
//
// With -focus.update the file is written instead, and the assertion passes.
// A missing file is reported with the command to create it rather than as a
// bare "no such file" — the first run of a new golden test should tell you
// what to do next.
func (g *Golden) Assert(name string, actual focus.Value) bool {
	g.t.Helper()

	path := g.Path(name)

	if Update() {
		if err := g.write(path, actual); err != nil {
			g.t.Errorf("golden: updating %s: %v", path, err)
			return false
		}
		return true
	}

	want, err := Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			g.t.Errorf("golden: %s does not exist yet.\n"+
				"Create it by running:\n\n    go test ./... -focus.update\n", path)
			return false
		}
		g.t.Errorf("golden: reading %s: %v", path, err)
		return false
	}

	typeName := g.typeHnt
	if typeName == "" {
		typeName = want.Type
	}

	// The comparison is against the stored XDR, never the decoded rendering:
	// the decoded form exists for humans reading diffs, and treating it as
	// authoritative would make the assertion depend on lens's formatting.
	ok := focus.AssertEqualAs(g.t, typeName, want.XDR, actual, g.opts...)
	if !ok {
		g.t.Errorf("golden: %s is out of date.\n"+
			"If the change above is intended, re-record it with:\n\n    go test ./... -focus.update\n", path)
	}
	return ok
}

// write records a value as a golden file.
func (g *Golden) write(path string, actual focus.Value) error {
	fixture, err := Build(actual, g.typeHnt, g.desc, nil)
	if err != nil {
		return err
	}
	return Save(path, fixture)
}

// Build turns a value into a Fixture, decoding it for the human-readable
// section.
func Build(v focus.Value, typeName, description string, provenance *Provenance) (*Fixture, error) {
	b64, decoded, err := focus.Encode(v, typeName)
	if err != nil {
		return nil, err
	}

	fixture := &Fixture{
		Format:      FormatVersion,
		Type:        decoded.Type,
		Description: description,
		XDR:         b64,
		Captured:    provenance,
	}

	// The decoded section is lens's own JSON, so it matches what
	// `lens decode --json` prints for the same payload.
	var buf bytes.Buffer
	f := lensformat.NewJSONFormatter(lensformat.WithIndent("  "))
	if err := f.Format(&buf, decoded); err != nil {
		return nil, fmt.Errorf("rendering decoded form: %w", err)
	}
	fixture.Decoded = json.RawMessage(bytes.TrimSpace(buf.Bytes()))

	return fixture, nil
}

// Load reads a golden file.
func Load(path string) (*Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if f.Format != "" && f.Format != FormatVersion {
		return nil, fmt.Errorf(
			"%s was written in format %q, but this version of stellar-focus reads %q",
			path, f.Format, FormatVersion)
	}
	if f.XDR == "" {
		return nil, fmt.Errorf("%s has no xdr field", path)
	}
	return &f, nil
}

// Save writes a golden file, creating the directory if needed.
//
// Output is indented and newline-terminated so that a diff is line-oriented
// and reviewable, which is the reason this format exists at all.
func Save(path string, f *Fixture) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		return fmt.Errorf("encoding fixture: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// NewProvenance records a capture at the given time.
func NewProvenance(network, endpoint string, ledger uint32, txHash string, at time.Time) *Provenance {
	return &Provenance{
		Network:  network,
		Ledger:   ledger,
		TxHash:   txHash,
		At:       at.UTC().Format(time.RFC3339),
		Endpoint: endpoint,
	}
}
