// Package fuzz provides seed corpora for fuzzing XDR decoders and the
// library's own parsers.
//
// The corpus is the XDR stellar-focus has been reading all along: real
// mainnet transactions, captured and kept as fixtures. A project that wants
// to fuzz its own XDR decoder can seed it with the same payloads, rather
// than capturing the same transactions again or writing a corpus by hand.
//
// The corpus is embedded, so it is available wherever the module is
// imported, with no filesystem path to get wrong:
//
//	func FuzzDecodeEnvelope(f *testing.F) {
//	    for _, seed := range fuzz.CorpusFor("TransactionEnvelope") {
//	        f.Add(seed)
//	    }
//	    f.Fuzz(func(t *testing.T, data []byte) {
//	        var env xdr.TransactionEnvelope
//	        _ = xdr.SafeUnmarshal(data, &env)
//	    })
//	}
//
// The seeds are raw XDR bytes. A fuzzer that wants the base64 form — the
// one the Stellar APIs speak — can encode them itself, or read the fixture
// files from the package's testdata directory.
//
// A corpus is only useful if it covers the types a decoder accepts, so
// Types lists what is here:
//
//	fuzz.Types() // TransactionEnvelope, TransactionMeta, TransactionResult
//
// The golden fixture deserializer and the template interpolation engine read
// arbitrary files and arbitrary text, so they have seed corpora here too:
// valid fixtures in every format the package writes, corrupted documents, and
// malformed templates. Targets live in a _test.go file because that is where
// the go tool looks for them:
//
//	func FuzzGoldenDeserializer(f *testing.F) {
//	    for _, seed := range fuzz.GoldenSeeds() {
//	        f.Add(seed.Data)
//	    }
//	    f.Fuzz(func(t *testing.T, data []byte) {
//	        // data is arbitrary JSON, YAML or text: an error is fine, a panic
//	        // or an unbounded allocation is not.
//	    })
//	}
package fuzz

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

//go:embed testdata
var files embed.FS

// manifestName is the file that describes the embedded corpus.
const manifestName = "testdata/manifest.json"

// Seed is one payload, ready to be handed to a fuzzer.
type Seed struct {
	// Name identifies the seed, e.g. "tx_failed.env".
	Name string
	// Type is the XDR type the payload holds, e.g. "TransactionEnvelope".
	Type string
	// Bytes is the raw XDR.
	Bytes []byte
	// Note says where the payload came from, for a reader wondering why
	// this particular one is in the corpus.
	Note string
}

// manifest is the description of the embedded corpus.
type manifest struct {
	Seeds []struct {
		File string `json:"file"`
		Name string `json:"name"`
		Type string `json:"type"`
		Note string `json:"note"`
	} `json:"seeds"`
}

// seeds is the corpus, read once.
var seeds = loadSeeds()

// loadSeeds reads the embedded corpus.
//
// A corpus that cannot be read is a broken package rather than a broken
// test, so a malformed manifest panics here: the failure happens at import
// time, where it is obvious, rather than in whichever test happened to ask
// for a seed.
func loadSeeds() []Seed {
	raw, err := files.ReadFile(manifestName)
	if err != nil {
		panic(fmt.Sprintf("fuzz: reading the corpus manifest: %v", err))
	}

	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(fmt.Sprintf("fuzz: parsing the corpus manifest: %v", err))
	}

	out := make([]Seed, 0, len(m.Seeds)+1)
	for _, entry := range m.Seeds {
		payload, err := files.ReadFile("testdata/" + entry.File)
		if err != nil {
			panic(fmt.Sprintf("fuzz: reading the corpus file %s: %v", entry.File, err))
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
		if err != nil {
			panic(fmt.Sprintf("fuzz: decoding the corpus file %s: %v", entry.File, err))
		}
		out = append(out, Seed{
			Name:  entry.Name,
			Type:  entry.Type,
			Bytes: decoded,
			Note:  entry.Note,
		})
	}

	// The captured transactions have no captured meta, so one is built here
	// to give a meta decoder a seed that carries the structures only a
	// Soroban meta carries.
	if meta, err := sorobanMetaSeed(); err == nil {
		out = append(out, meta)
	}
	return out
}

// Seeds returns every seed in the corpus, with the names and types that
// describe them.
func Seeds() []Seed {
	out := make([]Seed, len(seeds))
	copy(out, seeds)
	return out
}

// Corpus returns the raw XDR bytes of every seed, suitable for use as a
// fuzz seed corpus.
func Corpus() [][]byte {
	out := make([][]byte, 0, len(seeds))
	for _, seed := range seeds {
		out = append(out, seed.Bytes)
	}
	return out
}

// CorpusFor returns the raw XDR bytes of the seeds of one XDR type.
//
// The type name is matched loosely, so "TransactionEnvelope",
// "transaction_envelope" and "Transaction Envelope" all find the same
// seeds. A type the corpus does not hold yields an empty slice rather than
// an error: a fuzzer that asks for seeds it does not need should not fail.
func CorpusFor(typeName string) [][]byte {
	wanted := normalizeType(typeName)
	out := make([][]byte, 0, len(seeds))
	for _, seed := range seeds {
		if normalizeType(seed.Type) == wanted {
			out = append(out, seed.Bytes)
		}
	}
	return out
}

// Types lists the XDR types the corpus covers, in alphabetical order.
func Types() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if seen[seed.Type] {
			continue
		}
		seen[seed.Type] = true
		out = append(out, seed.Type)
	}
	sort.Strings(out)
	return out
}

// normalizeType folds a type name to the form used for comparison.
func normalizeType(name string) string {
	return strings.ToLower(strings.NewReplacer("_", "", " ", "", "-", "").Replace(strings.TrimSpace(name)))
}

// GoldenSeed is one input for fuzzing the golden fixture deserializers.
//
// Format names the encoding the bytes are written in, so a target can check
// that a valid seed really is the document the corpus claims it is.
type GoldenSeed struct {
	// Name identifies the seed, e.g. "valid/json" or "corrupt/yaml-tab".
	Name string
	// Format is the fixture format the bytes are written in.
	Format golden.Format
	// Data is the document to deserialize.
	Data []byte
}

// TemplateSeed is one input for fuzzing the template interpolation engine.
type TemplateSeed struct {
	// Name identifies the seed, e.g. "unclosed-action".
	Name string
	// Template is the template text to render.
	Template string
}

// goldenSeeds is the golden deserializer corpus, built once.
var goldenSeeds = loadGoldenSeeds()

// templateSeeds is the interpolation corpus.
var templateSeeds = []TemplateSeed{
	{Name: "valid", Template: "hello {{ .Name }} / {{ .Sequence }}"},
	{Name: "conditional", Template: "{{ if .Enabled }}{{ .Name }}{{ else }}off{{ end }}"},
	{Name: "range", Template: "{{ range .List }}{{ . }},{{ end }}"},
	{Name: "nested-field", Template: "{{ .Nested.Value }}"},
	// Malformed templates: every one of these must come back as an error,
	// not as a panic and not as a partial render.
	{Name: "unclosed-action", Template: "hello {{ .Name"},
	{Name: "unclosed-quote", Template: "{{ .Name | printf \"%s }}"},
	{Name: "unknown-function", Template: "{{ nope .Name }}"},
	{Name: "dangling-pipeline", Template: "{{ .Name | }}"},
	{Name: "unclosed-block", Template: "{{ if .Enabled }}yes"},
	{Name: "stray-end", Template: "{{ end }}"},
	{Name: "missing-key", Template: "{{ .Missing }}"},
	{Name: "range-over-scalar", Template: "{{ range .Name }}x{{ end }}"},
	{Name: "bad-printf", Template: "{{ printf \"%d\" \"not a number\" }}"},
}

// GoldenSeeds returns the seed corpus for the golden fixture deserializer: a
// valid fixture in each format the package writes, plus documents that are
// corrupted in the ways a hand-edit or a bad merge corrupts one.
func GoldenSeeds() []GoldenSeed {
	out := make([]GoldenSeed, len(goldenSeeds))
	copy(out, goldenSeeds)
	return out
}

// TemplateSeeds returns the seed corpus for template interpolation: templates
// that render and templates that must be rejected.
func TemplateSeeds() []TemplateSeed {
	out := make([]TemplateSeed, len(templateSeeds))
	copy(out, templateSeeds)
	return out
}

// loadGoldenSeeds builds the golden deserializer corpus.
//
// The valid seeds are written by the serializers themselves rather than
// copied out of a fixture file, so they cannot drift away from what the
// package writes today: a fixture is seeded, then serialized in each format,
// then mutated into the corrupt variants.
func loadGoldenSeeds() []GoldenSeed {
	fixture := goldenSeedFixture()

	seeds := make([]GoldenSeed, 0, 12)
	for _, format := range []golden.Format{golden.JSON, golden.YAML, golden.Text} {
		serializer, err := golden.SerializerFor(format)
		if err != nil {
			panic(fmt.Sprintf("fuzz: serializer for %s: %v", format, err))
		}
		data, err := serializer.Marshal(fixture)
		if err != nil {
			panic(fmt.Sprintf("fuzz: building the %s golden seed: %v", format, err))
		}
		seeds = append(seeds, GoldenSeed{Name: "valid/" + format.String(), Format: format, Data: data})
	}

	jsonSeed := seeds[0].Data
	seeds = append(seeds,
		GoldenSeed{Name: "corrupt/json-truncated", Format: golden.JSON,
			Data: append([]byte(nil), jsonSeed[:len(jsonSeed)/2]...)},
		GoldenSeed{Name: "corrupt/json-unknown-format", Format: golden.JSON,
			Data: []byte(`{"format":"focus/v99","type":"TransactionEnvelope","xdr":"AAAA"}`)},
		GoldenSeed{Name: "corrupt/json-wrong-types", Format: golden.JSON,
			Data: []byte(`{"type":7,"xdr":[]}`)},
		GoldenSeed{Name: "corrupt/json-trailing-value", Format: golden.JSON,
			Data: []byte(`{"type":"X","xdr":"AAAA"}{}`)},
		GoldenSeed{Name: "corrupt/yaml-tab-indent", Format: golden.YAML,
			Data: []byte("format: focus/v1\ntype: X\n\txdr: AAAA\n")},
		GoldenSeed{Name: "corrupt/yaml-unterminated-quote", Format: golden.YAML,
			Data: []byte("type: \"X\nxdr: AAAA\n")},
		GoldenSeed{Name: "corrupt/yaml-duplicate-key", Format: golden.YAML,
			Data: []byte("type: X\ntype: Y\nxdr: AAAA\n")},
		GoldenSeed{Name: "corrupt/text-missing-separator", Format: golden.Text,
			Data: []byte("type TransactionEnvelope\n")},
		GoldenSeed{Name: "corrupt/text-unterminated-index", Format: golden.Text,
			Data: []byte("xdr: AAAA\ndecoded.a[1: 2\n")},
		// A path that asks for an absurd array index must be rejected rather
		// than allocating the array it names.
		GoldenSeed{Name: "corrupt/text-huge-index", Format: golden.Text,
			Data: []byte("type: X\nxdr: AAAA\ndecoded.a[4294967295]: 1\n")},
		GoldenSeed{Name: "corrupt/empty", Format: golden.JSON, Data: nil},
	)

	seeds = append(seeds, GoldenSeed{Name: "valid/yaml-deep", Format: golden.YAML, Data: deepYAMLSeed()})
	return seeds
}

// goldenSeedFixture is the fixture the valid golden seeds are built from: a
// real captured envelope, so the corpus starts with the data the package is
// meant to read.
func goldenSeedFixture() *golden.Fixture {
	envelope, err := files.ReadFile("testdata/tx_failed.env.txt")
	if err != nil {
		panic(fmt.Sprintf("fuzz: reading the golden seed envelope: %v", err))
	}
	return &golden.Fixture{
		Format:      golden.FormatVersion,
		Type:        "TransactionEnvelope",
		Description: "a failed Soroban call",
		XDR:         strings.TrimSpace(string(envelope)),
		Decoded:     json.RawMessage(`{"V1":{"Tx":{"Fee":"100","Operations":[]}}}`),
	}
}

// deepYAMLSeed renders a fixture nested deepYAMLDepth levels deep, which is
// valid input and exercises the reader's recursion rather than its error
// paths.
func deepYAMLSeed() []byte {
	const depth = 200

	var b strings.Builder
	b.WriteString("type: TransactionEnvelope\nxdr: AAAA\ndecoded:\n")
	for i := 0; i < depth; i++ {
		b.WriteString(strings.Repeat("  ", i+1))
		b.WriteString("a:\n")
	}
	return []byte(b.String())
}

// sorobanMetaSeed builds a Soroban transaction meta.
//
// The captured transactions in the corpus have no captured meta, and a meta
// is where the interesting structures live: contract events, ScVal
// payloads and the ledger changes a contract invocation makes. The seed is
// built rather than captured, and it is deterministic, so the corpus does
// not change from run to run.
func sorobanMetaSeed() (Seed, error) {
	symbol := xdr.ScSymbol("transfer")
	fromKey := xdr.ScSymbol("from")
	amountKey := xdr.ScSymbol("amount")

	var contractID xdr.ContractId
	copy(contractID[:], []byte("stellar-focus-fuzz-corpus-id!!"))

	accountID, err := xdr.AddressToAccountId("GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6")
	if err != nil {
		return Seed{}, err
	}
	address := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &accountID}

	amount := xdr.UInt128Parts{Hi: 0, Lo: 1_000_000}
	returned := xdr.Uint32(7)

	entries := xdr.ScMap{
		{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &fromKey},
			Val: xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address},
		},
		{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &amountKey},
			Val: xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &amount},
		},
	}
	mapPtr := &entries

	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				Events: []xdr.ContractEvent{
					{
						Type:       xdr.ContractEventTypeContract,
						ContractId: &contractID,
						Body: xdr.ContractEventBody{
							V: 0,
							V0: &xdr.ContractEventV0{
								Topics: []xdr.ScVal{{Type: xdr.ScValTypeScvSymbol, Sym: &symbol}},
								Data:   xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr},
							},
						},
					},
				},
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &returned},
			},
		},
	}

	encoded, err := xdr.MarshalBase64(meta)
	if err != nil {
		return Seed{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Seed{}, err
	}
	return Seed{
		Name:  "soroban.meta",
		Type:  "TransactionMeta",
		Bytes: raw,
		Note:  "a Soroban meta, built from the captured transaction rather than captured with it",
	}, nil
}
