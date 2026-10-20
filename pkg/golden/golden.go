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

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
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
	Network           string `json:"network"`
	NetworkPassphrase string `json:"networkPassphrase,omitempty"`
	Ledger            uint32 `json:"ledger,omitempty"`
	TxHash            string `json:"txHash,omitempty"`
	At                string `json:"at"`
	Endpoint          string `json:"endpoint,omitempty"`
	Anonymized        bool   `json:"anonymized,omitempty"`
}

// Golden compares values against golden files on disk.
type Golden struct {
	t                focus.TB
	dir              string
	suffix           string
	format           Format
	opts             []focus.Option
	noiseSuppression bool
	desc             string
	typeHnt          string
	vars             any
}

// Opt configures a Golden.
type Opt func(*Golden)

// WithDir sets the directory holding golden files. The default is "testdata".
func WithDir(dir string) Opt {
	return func(g *Golden) { g.dir = dir }
}

// WithSuffix sets the file suffix. The default follows the format, so it is
// ".golden.json" unless WithFormat says otherwise.
func WithSuffix(s string) Opt {
	return func(g *Golden) { g.suffix = s }
}

// WithFormat stores fixtures in the named format instead of JSON, and moves the
// default suffix with it — ".golden.yaml" for YAML and ".golden.txt" for text —
// so a name needs no extension. A WithSuffix given after it still wins.
func WithFormat(f Format) Opt {
	return func(g *Golden) {
		g.format = f
		g.suffix = f.DefaultSuffix()
	}
}

// WithOptions applies focus assertion options — ignore and normalize rules —
// to every comparison this Golden makes.
func WithOptions(opts ...focus.Option) Opt {
	return func(g *Golden) { g.opts = append(g.opts, opts...) }
}

// WithNoiseSuppression controls automatic normalization of volatile XDR fields.
// It is enabled by default; disable it when a test intentionally checks them.
func WithNoiseSuppression(enabled bool) Opt {
	return func(g *Golden) { g.noiseSuppression = enabled }
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

// WithVariables supplies data used to interpolate templates in loaded fixtures.
func WithVariables(variables any) Opt {
	return func(g *Golden) { g.vars = variables }
}

// New returns a Golden bound to a test.
func New(t focus.TB, opts ...Opt) *Golden {
	g := &Golden{
		t: t, dir: "testdata", suffix: JSON.DefaultSuffix(), format: JSON,
		noiseSuppression: true,
	}
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
	return g.assert(g.t, name, actual, "", nil)
}

// assert compares a value against the named golden file, reporting through
// tb.
//
// description and extra are supplied by AssertAll: a plain Assert writes the
// suite's description into a new fixture and uses the suite's options only.
func (g *Golden) assert(
	tb focus.TB,
	name string,
	actual focus.Value,
	description string,
	extra []focus.Option,
) bool {
	g.t.Helper()
	tb.Helper()

	path := g.Path(name)

	if Update() {
		if err := g.write(path, actual, description); err != nil {
			tb.Errorf("golden: updating %s: %v", path, err)
			return false
		}
		return true
	}

	want, err := LoadAs(path, g.format)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			tb.Errorf("golden: %s does not exist yet.\n"+
				"Create it by running:\n\n    go test ./... -focus.update\n", path)
			return false
		}
		tb.Errorf("golden: reading %s: %v", path, err)
		return false
	}
	if g.vars != nil {
		want, err = InterpolateFixture(want, g.vars)
		if err != nil {
			tb.Errorf("golden: interpolating %s: %v", path, err)
			return false
		}
	}

	typeName := g.typeHnt
	if typeName == "" {
		typeName = want.Type
	}

	// The comparison is against the stored XDR, never the decoded rendering:
	// the decoded form exists for humans reading diffs, and treating it as
	// authoritative would make the assertion depend on lens's formatting.
	ok := focus.AssertEqualAs(tb, typeName, want.XDR, actual, g.options(extra)...)
	if !ok {
		tb.Errorf("golden: %s is out of date.\n"+
			"If the change above is intended, re-record it with:\n\n    go test ./... -focus.update\n", path)
	}
	return ok
}

// options merges the suite's assertion options with a case's own.
//
// The case's options come last, so a case can add to what the suite already
// asks for. focus options accumulate rather than replace one another, so
// nothing is overridden by the order; the order is only about reading.
func (g *Golden) options(extra []focus.Option) []focus.Option {
	opts := make([]focus.Option, 0, len(g.opts)+len(extra))
	if g.noiseSuppression {
		opts = append(opts, noiseSuppressionOptions()...)
	}
	opts = append(opts, g.opts...)
	opts = append(opts, extra...)
	return opts
}

// write records a value as a golden file.
//
// A description given for one case overrides the suite's own, so a table can
// say what each of its fixtures is for.
func (g *Golden) write(path string, actual focus.Value, description string) error {
	if description == "" {
		description = g.desc
	}
	fixture, err := Build(actual, g.typeHnt, description, nil)
	if err != nil {
		return err
	}
	return SaveAs(path, fixture, g.format)
}

// Case is one entry of a table-driven golden test.
//
// The description and the options travel with the entry rather than with the
// suite, so a failure can say which entry failed and what it was for — the
// context a list of bare names cannot carry.
type Case struct {
	// Name is the golden file's base name, without a suffix.
	Name string
	// Value is the XDR compared against the golden file.
	Value focus.Value
	// Description is a note recorded in the fixture when it is written, and
	// shown beside the entry in the failure summary.
	Description string
	// Options are assertion options for this entry alone, merged over the
	// suite's own options.
	Options []focus.Option
}

// AssertAll compares every case of a table against its golden file.
//
// Every case runs even when an earlier one fails: a table that stops at the
// first failure hides how much of the table is broken. The result is false
// when any case failed.
//
// A failure reports the whole table, so the broken entry can be found without
// scanning filenames:
//
//	golden: table summary — 3 case(s), 1 failed:
//	  [1/3] payment_v1: ok
//	  [2/3] payment_v2: DRIFTED (a payment that moved)
//	  [3/3] payment_v3: ok
//
// and each failure is repeated with the entry's index, name and description
// attached, so the diff that follows says which entry it belongs to.
func (g *Golden) AssertAll(cases []Case) bool {
	g.t.Helper()

	results := make([]caseResult, 0, len(cases))
	failed := 0
	for i, c := range cases {
		result := g.runCase(i+1, c)
		if !result.ok {
			failed++
		}
		results = append(results, result)
	}

	summary := tableSummary(results, failed)
	if failed == 0 {
		g.logf("%s", summary)
		return true
	}

	g.t.Errorf("%s", summary)
	for _, r := range results {
		for _, msg := range r.messages {
			g.t.Errorf("golden: %s failed:\n%s", r.label(len(results)), msg)
		}
	}
	return false
}

// runCase runs one case of a table and collects what it reported.
func (g *Golden) runCase(index int, c Case) caseResult {
	g.t.Helper()

	rec := &recorder{tb: g.t}
	ok := g.assert(rec, c.Name, c.Value, c.Description, c.Options)

	result := caseResult{index: index, name: c.Name, description: c.Description, ok: ok}
	result.messages = append(result.messages, rec.errs...)
	result.messages = append(result.messages, rec.fatals...)
	return result
}

// logf writes to the test log when the TB has one.
//
// focus.TB is deliberately small — Helper, Errorf, Fatalf and Name — so there
// is no way to report a passing summary through it without failing the test.
// A TB that also offers Logf gets the summary for free; one that does not
// simply does not see it.
func (g *Golden) logf(format string, args ...any) {
	if l, ok := g.t.(interface{ Logf(string, ...any) }); ok {
		l.Logf(format, args...)
	}
}

// caseResult is what one case of a table did.
type caseResult struct {
	// index is the case's position in the table, counting from one.
	index int
	// name is the case's golden file name.
	name string
	// description is the case's note, when it has one.
	description string
	// ok reports whether the case matched its golden file.
	ok bool
	// messages are the failures the case reported.
	messages []string
}

// label names the case in a failure message.
func (r caseResult) label(total int) string {
	label := fmt.Sprintf("[%d/%d] %s", r.index, total, r.name)
	if r.description != "" {
		label += " (" + r.description + ")"
	}
	return label
}

// status is the word the summary table uses for the case.
func (r caseResult) status() string {
	if r.ok {
		return "ok"
	}
	return "DRIFTED"
}

// tableSummary renders the table of cases and how each of them went.
func tableSummary(results []caseResult, failed int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "golden: table summary — %d case(s), %d failed:", len(results), failed)
	for _, r := range results {
		fmt.Fprintf(&b, "\n  [%d/%d] %s: %s", r.index, len(results), r.name, r.status())
		if r.description != "" {
			fmt.Fprintf(&b, " (%s)", r.description)
		}
	}
	return b.String()
}

// recorder captures what an assertion reports, so a table-driven failure can
// be re-emitted with the entry's identity attached to it.
//
// Assertions report through TB.Errorf and have no other channel, so the
// context a table needs — the entry's index, name and description — can only
// be added by intercepting that channel.
type recorder struct {
	tb     focus.TB
	errs   []string
	fatals []string
}

// Helper implements focus.TB.
func (r *recorder) Helper() { r.tb.Helper() }

// Name implements focus.TB.
func (r *recorder) Name() string { return r.tb.Name() }

// Errorf implements focus.TB, recording the message rather than reporting it.
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// Fatalf implements focus.TB, recording the message rather than aborting.
//
// The assertions this package uses report through Errorf, so nothing here
// ends a test early; a fatal would be replayed as an ordinary failure.
func (r *recorder) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
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
	rendered, err := renderDecoded(decoded)
	if err != nil {
		return nil, err
	}
	fixture.Decoded = rendered

	return fixture, nil
}

// renderDecoded renders a decoded value as the JSON tree a fixture stores.
func renderDecoded(decoded *lens.Value) (json.RawMessage, error) {
	var buf bytes.Buffer
	f := lensformat.NewJSONFormatter(lensformat.WithIndent("  "))
	if err := f.Format(&buf, decoded); err != nil {
		return nil, fmt.Errorf("rendering decoded form: %w", err)
	}
	return json.RawMessage(bytes.TrimSpace(buf.Bytes())), nil
}

// Refresh re-renders the decoded section of a fixture from the XDR it stores.
//
// The XDR is the source of truth and the decoded tree beside it is what a
// human reads in a pull request. When the formatter improves, or the
// protocol gains a field, that tree goes stale — and re-recording the
// fixtures to fix it would re-record the data too. Refresh touches only the
// decoded section: the format, the type, the description, the XDR and the
// capture provenance keep their values.
//
// It reports whether the file on disk changed. A fixture whose decoded
// section is already current is left alone, so the command is safe to run
// over a whole testdata directory.
func Refresh(path string) (bool, error) {
	format := FormatForPath(path)

	fixture, err := LoadAs(path, format)
	if err != nil {
		return false, err
	}

	decoded, err := RefreshedDecoded(fixture)
	if err != nil {
		return false, err
	}

	refreshed := *fixture
	refreshed.Decoded = decoded

	serializer, err := SerializerFor(format)
	if err != nil {
		return false, err
	}
	data, err := serializer.Marshal(&refreshed)
	if err != nil {
		return false, fmt.Errorf("rendering %s: %w", path, err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(current, data) {
		return false, nil
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// RefreshedDecoded returns the decoded section a fixture would carry after a
// refresh, without writing anything.
//
// It is what a dry run shows: the tree the fixture would have, next to the
// one it has.
func RefreshedDecoded(f *Fixture) (json.RawMessage, error) {
	if f == nil {
		return nil, errors.New("fixture is nil")
	}
	if f.Type == "" {
		return nil, errors.New("fixture has no type, so there is nothing to decode")
	}
	if strings.TrimSpace(f.XDR) == "" {
		return nil, errors.New("fixture has no XDR, so there is nothing to decode")
	}

	decoded, err := lens.DecodeAs(f.XDR, f.Type)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", f.Type, err)
	}
	return renderDecoded(decoded)
}

// Load reads a golden file, choosing the format from its extension. An
// unrecognised extension is read as JSON, which is what every fixture written
// before the other formats existed is.
func Load(path string) (*Fixture, error) {
	return LoadAs(path, FormatForPath(path))
}

// LoadAs reads a golden file in an explicit format.
func LoadAs(path string, format Format) (*Fixture, error) {
	// The format is checked before the file is, so an unknown format is
	// reported as such even when the path does not exist.
	serializer, err := SerializerFor(format)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := serializer.Unmarshal(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return f, nil
}

// Save writes a golden file, choosing the format from its extension and
// creating the directory if needed.
func Save(path string, f *Fixture) error {
	return SaveAs(path, f, FormatForPath(path))
}

// SaveAs writes a golden file in an explicit format, creating the directory if
// needed.
//
// Output is indented and newline-terminated so that a diff is line-oriented
// and reviewable, which is the reason this format exists at all.
func SaveAs(path string, f *Fixture, format Format) error {
	serializer, err := SerializerFor(format)
	if err != nil {
		return err
	}
	data, err := serializer.Marshal(f)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
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
