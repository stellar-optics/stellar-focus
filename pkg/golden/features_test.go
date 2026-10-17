package golden_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

func TestValidateFixtureSchema(t *testing.T) {
	t.Parallel()

	valid := &golden.Fixture{Format: golden.FormatVersion, Type: "TransactionEnvelope", XDR: "AAAA"}
	if err := golden.Validate(valid); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown property", data: `{"type":"X","xdr":"AAAA","other":true}`, want: "not allowed"},
		{name: "wrong type", data: `{"type":5,"xdr":"AAAA"}`, want: "fixture.type must be string"},
		{name: "empty XDR", data: `{"type":"X","xdr":""}`, want: "no xdr field"},
		{name: "missing type", data: `{"xdr":"AAAA"}`, want: "fixture.type is required"},
		{name: "invalid provenance", data: `{"type":"X","xdr":"AAAA","captured":{"network":"testnet","ledger":-1}}`, want: "captured.ledger"},
	}
	serializer, err := golden.SerializerFor(golden.JSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := serializer.Unmarshal([]byte(tc.data)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Unmarshal() error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestInterpolateFixture(t *testing.T) {
	t.Parallel()

	fixture := &golden.Fixture{
		Format:      golden.FormatVersion,
		Type:        "{{ .Type }}",
		Description: "for {{ .Network }}",
		XDR:         "{{ .XDR }}",
		Captured:    &golden.Provenance{Network: "{{ .Network }}"},
		Decoded:     json.RawMessage(`{"account":"{{ .Account }}","amount":12}`),
	}
	data := map[string]string{
		"Type": "TransactionEnvelope", "Network": "testnet", "XDR": "AAAA", "Account": "GABC",
	}
	got, err := golden.InterpolateFixture(fixture, data)
	if err != nil {
		t.Fatalf("InterpolateFixture() error = %v", err)
	}
	if got.Type != "TransactionEnvelope" || got.Description != "for testnet" || got.XDR != "AAAA" {
		t.Errorf("top-level fields were not rendered: %+v", got)
	}
	if got.Captured == nil || got.Captured.Network != "testnet" {
		t.Errorf("provenance was not rendered: %+v", got.Captured)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.Decoded, &decoded); err != nil {
		t.Fatalf("decoding rendered tree: %v", err)
	}
	if decoded["account"] != "GABC" || decoded["amount"] != float64(12) {
		t.Errorf("decoded values = %#v", decoded)
	}
	if fixture.XDR != "{{ .XDR }}" || fixture.Captured.Network != "{{ .Network }}" {
		t.Fatal("InterpolateFixture mutated its input")
	}
}

func TestInterpolateRejectsMalformedAndMissingValues(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"{{ .Missing }}", "{{ .Unclosed"} {
		if _, err := golden.Interpolate(input, map[string]string{}); err == nil {
			t.Errorf("Interpolate(%q) succeeded without all template data", input)
		}
	}
}

func TestInterpolateFixtureRejectsCollidingDecodedKeys(t *testing.T) {
	t.Parallel()

	fixture := &golden.Fixture{
		Format: golden.FormatVersion,
		Type:   "X",
		XDR:    "AAAA",
		Decoded: json.RawMessage(`{
			"{{ .First }}": 1,
			"{{ .Second }}": 2
		}`),
	}
	_, err := golden.InterpolateFixture(fixture, map[string]string{"First": "same", "Second": "same"})
	if err == nil || !strings.Contains(err.Error(), "keys collide") {
		t.Fatalf("InterpolateFixture() error = %v, want collision error", err)
	}
}

func TestGoldenAssertInterpolatesStoredXDR(t *testing.T) {
	t.Parallel()

	actual := buildEnvelope(t, 100)
	fixture, err := golden.Build(actual, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture.XDR = "{{ .Envelope }}"
	dir := t.TempDir()
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatal(err)
	}
	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir), golden.WithVariables(map[string]string{"Envelope": actual}))
	if !g.Assert("env", actual) {
		t.Fatalf("Assert() rejected the rendered golden XDR: %s", tb.Output())
	}
}

func TestGoldenAssertReportsMissingInterpolationVariable(t *testing.T) {
	t.Parallel()

	fixture, err := golden.Build(buildEnvelope(t, 100), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture.XDR = "{{ .Envelope }}"
	dir := t.TempDir()
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatal(err)
	}
	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir), golden.WithVariables(map[string]string{}))
	if g.Assert("env", buildEnvelope(t, 100)) {
		t.Fatal("Assert() passed with a missing template variable")
	}
	if !strings.Contains(tb.Output(), "interpolating") {
		t.Errorf("failure omitted interpolation context: %s", tb.Output())
	}
}

func TestDiscoverFindsNestedJSONFixturesInStableOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"z.golden.json", filepath.Join("nested", "b.golden.json"), "a.golden.json", "skip.golden.yaml"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := golden.Discover(dir)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	want := []string{"a", "nested/b", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover() = %v, want %v", got, want)
	}
}

func TestRunExecutesOneAssertionPerFixture(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	actual := buildEnvelope(t, 100)
	fixture, err := golden.Build(actual, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", filepath.Join("nested", "second")} {
		if err := golden.Save(filepath.Join(dir, name+".golden.json"), fixture); err != nil {
			t.Fatal(err)
		}
	}
	var ran []string
	golden.Run(t, dir, func(_ *testing.T, name string) focus.Value {
		ran = append(ran, name)
		return actual
	})
	if want := []string{"first", "nested/second"}; !reflect.DeepEqual(ran, want) {
		t.Errorf("runner names = %v, want %v", ran, want)
	}
}

func TestExportFuzzSeedsWritesGoCorpusFiles(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	corpusDir := filepath.Join(t.TempDir(), "testdata", "fuzz", "FuzzEnvelope")
	for _, name := range []string{"z", filepath.Join("nested", "a")} {
		fixture, err := golden.Build(buildEnvelope(t, int64(len(name))), "TransactionEnvelope", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := golden.Save(filepath.Join(fixtureDir, name+".golden.json"), fixture); err != nil {
			t.Fatal(err)
		}
	}
	if err := golden.ExportFuzzSeeds(fixtureDir, corpusDir); err != nil {
		t.Fatalf("ExportFuzzSeeds() error = %v", err)
	}
	for index, name := range []string{"nested/a", "z"} {
		fixture, err := golden.Load(filepath.Join(fixtureDir, name+".golden.json"))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(corpusDir, fmt.Sprintf("seed_%06d", index))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "go test fuzz v1\nstring(" + fmt.Sprintf("%q", fixture.XDR) + ")\n"
		if string(data) != want {
			t.Errorf("seed %s = %q, want %q", path, data, want)
		}
	}
}
