package fuzz_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/fuzz"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// TestCorpusDecodes checks that every seed is real XDR of the type the
// corpus says it is. A seed that does not decode is worse than no seed: it
// teaches a fuzzer nothing.
func TestCorpusDecodes(t *testing.T) {
	t.Parallel()

	for _, seed := range fuzz.Seeds() {
		t.Run(seed.Name, func(t *testing.T) {
			t.Parallel()

			switch seed.Type {
			case "TransactionEnvelope":
				var env xdr.TransactionEnvelope
				if err := xdr.SafeUnmarshal(seed.Bytes, &env); err != nil {
					t.Fatalf("the seed does not decode as an envelope: %v", err)
				}
			case "TransactionResult":
				var res xdr.TransactionResult
				if err := xdr.SafeUnmarshal(seed.Bytes, &res); err != nil {
					t.Fatalf("the seed does not decode as a result: %v", err)
				}
			case "TransactionMeta":
				var meta xdr.TransactionMeta
				if err := xdr.SafeUnmarshal(seed.Bytes, &meta); err != nil {
					t.Fatalf("the seed does not decode as a meta: %v", err)
				}
			default:
				t.Fatalf("the corpus holds an unknown type %q", seed.Type)
			}
		})
	}
}

func TestCorpusForSelectsByType(t *testing.T) {
	t.Parallel()

	envelopes := fuzz.CorpusFor("TransactionEnvelope")
	if len(envelopes) == 0 {
		t.Fatal("the corpus holds no envelope seeds")
	}
	for _, seed := range envelopes {
		var env xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshal(seed, &env); err != nil {
			t.Errorf("an envelope seed does not decode: %v", err)
		}
	}

	// The type name is matched loosely, so a caller who writes it in
	// snake_case still finds the seeds.
	if len(fuzz.CorpusFor("transaction_envelope")) != len(envelopes) {
		t.Errorf("the snake_case spelling found %d seeds, want %d",
			len(fuzz.CorpusFor("transaction_envelope")), len(envelopes))
	}

	if got := fuzz.CorpusFor("NoSuchType"); len(got) != 0 {
		t.Errorf("an unknown type returned %d seeds, want none", len(got))
	}
}

func TestCorpusHoldsEverySeed(t *testing.T) {
	t.Parallel()

	if len(fuzz.Corpus()) != len(fuzz.Seeds()) {
		t.Errorf("the corpus holds %d seeds and %d entries", len(fuzz.Corpus()), len(fuzz.Seeds()))
	}
}

func TestTypesCoverTheMajorOnes(t *testing.T) {
	t.Parallel()

	types := fuzz.Types()
	for _, want := range []string{"TransactionEnvelope", "TransactionMeta", "TransactionResult"} {
		if !contains(types, want) {
			t.Errorf("the corpus does not cover %s: %v", want, types)
		}
	}
}

// TestCorpusMatchesTheFixtures checks that the embedded corpus and the
// fixture files are the same payloads, so a corpus cannot drift away from
// the captures it came from.
func TestCorpusMatchesTheFixtures(t *testing.T) {
	t.Parallel()

	files, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("reading the corpus directory: %v", err)
	}

	for _, file := range files {
		if file.IsDir() || file.Name() == "manifest.json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("testdata", file.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", file.Name(), err)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatalf("decoding %s: %v", file.Name(), err)
		}
		if !containsSeed(fuzz.Seeds(), decoded) {
			t.Errorf("%s is in the corpus directory but not in the corpus", file.Name())
		}
	}
}

// contains reports whether a list of strings holds a value.
func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// containsSeed reports whether the corpus holds a payload.
func containsSeed(seeds []fuzz.Seed, want []byte) bool {
	for _, seed := range seeds {
		if string(seed.Bytes) == string(want) {
			return true
		}
	}
	return false
}

// FuzzGoldenDeserializer checks that arbitrary bytes cannot make a fixture
// deserializer panic, loop, or allocate without bound.
//
// Malformed input is expected — returning an error is the pass — so the
// failure modes that matter are the ones the engine finds on its own: a
// panic, a deadlock, or an OOM. Every format is tried against every input
// rather than trusting a format hint, so a mutation that is still valid JSON
// but broken YAML exercises both readers.
func FuzzGoldenDeserializer(f *testing.F) {
	for _, seed := range fuzz.GoldenSeeds() {
		f.Add(seed.Data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, format := range []golden.Format{golden.JSON, golden.YAML, golden.Text} {
			serializer, err := golden.SerializerFor(format)
			if err != nil {
				t.Fatalf("SerializerFor(%q) error = %v", format, err)
			}
			fixture, err := serializer.Unmarshal(data)
			if err != nil {
				continue
			}
			// A document that reads has to write: if the encoder disagrees
			// with the decoder that accepted it, the package could store a
			// fixture it can never load back.
			encoded, err := serializer.Marshal(fixture)
			if err != nil {
				t.Fatalf("%s: Marshal() after a successful Unmarshal() error = %v", format, err)
			}
			if len(encoded) == 0 {
				t.Fatalf("%s: Marshal() returned no bytes for a fixture it read", format)
			}
		}
	})
}

// FuzzInterpolate checks that arbitrary template text either renders or comes
// back as an error: no panic, no runaway execution.
func FuzzInterpolate(f *testing.F) {
	for _, seed := range fuzz.TemplateSeeds() {
		f.Add(seed.Template)
	}

	f.Fuzz(func(t *testing.T, text string) {
		rendered, err := golden.Interpolate(text, templateFuzzData())
		if err != nil {
			// Rejecting a malformed template is the pass.
			return
		}
		// Text without an action is returned as it came in; anything else
		// means a template with no template in it was rewritten.
		if !strings.Contains(text, "{{") && rendered != text {
			t.Fatalf("Interpolate(%q) = %q, want the text unchanged", text, rendered)
		}
	})
}

// TestGoldenSeedsInitializeTheFuzzHarness checks the corpus the deserializer
// target starts from: every format has a valid fixture and a corrupt document,
// and each seed behaves the way its name says.
func TestGoldenSeedsInitializeTheFuzzHarness(t *testing.T) {
	t.Parallel()

	seeds := fuzz.GoldenSeeds()
	if len(seeds) == 0 {
		t.Fatal("the golden seed corpus is empty")
	}

	valid := map[golden.Format]int{}
	corrupt := map[golden.Format]int{}
	for _, seed := range seeds {
		serializer, err := golden.SerializerFor(seed.Format)
		if err != nil {
			t.Fatalf("seed %s: SerializerFor(%q) error = %v", seed.Name, seed.Format, err)
		}
		_, err = serializer.Unmarshal(seed.Data)
		switch {
		case strings.HasPrefix(seed.Name, "valid/"):
			if err != nil {
				t.Errorf("seed %s does not deserialize: %v", seed.Name, err)
				continue
			}
			valid[seed.Format]++
		case strings.HasPrefix(seed.Name, "corrupt/"):
			if err == nil {
				t.Errorf("seed %s is named corrupt but deserialized", seed.Name)
				continue
			}
			corrupt[seed.Format]++
		default:
			t.Errorf("seed %s has neither a valid/ nor a corrupt/ prefix", seed.Name)
		}
	}

	for _, format := range []golden.Format{golden.JSON, golden.YAML, golden.Text} {
		if valid[format] == 0 {
			t.Errorf("the corpus holds no valid %s fixture", format)
		}
		if corrupt[format] == 0 {
			t.Errorf("the corpus holds no corrupt %s document", format)
		}
	}
}

// TestTemplateSeedsInitializeTheFuzzHarness checks that the template corpus
// covers both sides of the parser: templates that render and templates that
// are rejected.
func TestTemplateSeedsInitializeTheFuzzHarness(t *testing.T) {
	t.Parallel()

	seeds := fuzz.TemplateSeeds()
	if len(seeds) == 0 {
		t.Fatal("the template seed corpus is empty")
	}

	rendered := 0
	for _, seed := range seeds {
		if _, err := golden.Interpolate(seed.Template, templateFuzzData()); err == nil {
			rendered++
		}
	}
	if rendered == 0 {
		t.Error("no template seed renders; the corpus covers only error paths")
	}
	if rendered == len(seeds) {
		t.Errorf("all %d template seeds render; the corpus covers no error path", len(seeds))
	}
}

// templateFuzzData is the context both the seeds and the fuzz target render
// against. It carries a scalar, a list and a nested map so a mutation has
// something of every shape to reach for.
func templateFuzzData() map[string]any {
	return map[string]any{
		"Name":     "world",
		"Sequence": 42,
		"Enabled":  true,
		"List":     []any{"a", "b"},
		"Nested":   map[string]any{"Value": "inner"},
	}
}

// ExampleCorpus shows the pattern a downstream project follows to seed a
// fuzzer with the corpus.
func ExampleCorpus() {
	for _, seed := range fuzz.CorpusFor("TransactionEnvelope") {
		var env xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshal(seed, &env); err != nil {
			continue
		}
	}
	// Output:
}
