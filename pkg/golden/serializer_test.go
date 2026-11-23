package golden_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// goldenFormats is every format a fixture can be stored in. Tests that state a
// property of "the serializers" table over it, so a fourth format cannot be
// added without being held to the same promises.
var goldenFormats = []golden.Format{golden.JSON, golden.YAML, golden.Text, golden.TOML}

// capturedFixture is a fixture with everything a real one carries: provenance,
// a description, and the decoded tree lens renders for a reviewer.
func capturedFixture(t *testing.T) *golden.Fixture {
	t.Helper()

	provenance := golden.NewProvenance(
		"testnet", "https://soroban-testnet.stellar.org", 3966820, "2b2e276a8f99a292",
		time.Unix(1754395200, 0).UTC())

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "a failed Soroban call", provenance)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return fixture
}

// trickyFixture carries the values that a careless serializer loses: a string
// that looks like a number, an int64 past float64's exact range, empty
// containers, nested sequences, null, a key containing a path separator, and a
// string containing a colon.
func trickyFixture() *golden.Fixture {
	return &golden.Fixture{
		Format:      golden.FormatVersion,
		Type:        "TransactionEnvelope",
		Description: "values that must survive a round trip",
		XDR:         "AAAA",
		Decoded: json.RawMessage(`{
			"numericString": "58002",
			"number": 58002,
			"maxInt64": 9223372036854775807,
			"float": 1.5,
			"empty": {},
			"emptyList": [],
			"nested": [[1, 2], [3]],
			"list": [{"deep": {"key.with.dot": "v"}}, null, true],
			"reserved": "yes",
			"colon: key": "a: b",
			"leading": "- dash",
			"spaces": " padded ",
			"emptyString": "",
			"hash": "a # b",
			"url": "https://example.com/x?a=b"
		}`),
	}
}

func TestParseFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		want    golden.Format
		wantErr bool
	}{
		{name: "json", want: golden.JSON},
		{name: "JSON", want: golden.JSON},
		{name: "  json  ", want: golden.JSON},
		{name: "yaml", want: golden.YAML},
		{name: "YAML", want: golden.YAML},
		{name: "yml", want: golden.YAML},
		{name: "text", want: golden.Text},
		{name: "txt", want: golden.Text},
		{name: "toml", want: golden.TOML},
		{name: "TOML", want: golden.TOML},
		{name: "invalid", wantErr: true},
		{name: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := golden.ParseFormat(tc.name)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseFormat(%q) = %q, want an error", tc.name, got)
				}
				// The message must name the offending input, quoted; an empty
				// name is named as "" rather than left out.
				if !strings.Contains(err.Error(), `"`+tc.name+`"`) {
					t.Errorf("error does not name the format: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFormat(%q) error = %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("ParseFormat(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestFormatSuffixAndName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format golden.Format
		name   string
		suffix string
	}{
		{format: golden.JSON, name: "json", suffix: ".golden.json"},
		{format: golden.YAML, name: "yaml", suffix: ".golden.yaml"},
		{format: golden.Text, name: "text", suffix: ".golden.txt"},
		{format: golden.TOML, name: "toml", suffix: ".golden.toml"},
	}

	for _, tc := range tests {
		if got := tc.format.String(); got != tc.name {
			t.Errorf("%q.String() = %q, want %q", tc.format, got, tc.name)
		}
		if got := tc.format.DefaultSuffix(); got != tc.suffix {
			t.Errorf("%q.DefaultSuffix() = %q, want %q", tc.format, got, tc.suffix)
		}
	}
}

func TestFormatForPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want golden.Format
	}{
		{path: "testdata/env.golden.json", want: golden.JSON},
		{path: "testdata/env.json", want: golden.JSON},
		{path: "testdata/env.golden.yaml", want: golden.YAML},
		{path: "testdata/env.yml", want: golden.YAML},
		{path: "testdata/ENV.YAML", want: golden.YAML},
		{path: "testdata/env.golden.txt", want: golden.Text},
		{path: "testdata/env.golden.toml", want: golden.TOML},
		{path: "testdata/env.toml", want: golden.TOML},
		{path: "testdata/ENV.TOML", want: golden.TOML},
		// A path written before the other formats existed, or with no
		// extension at all, is still JSON.
		{path: "testdata/env", want: golden.JSON},
		{path: "testdata/env.gold", want: golden.JSON},
	}

	for _, tc := range tests {
		if got := golden.FormatForPath(tc.path); got != tc.want {
			t.Errorf("FormatForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSerializerFor(t *testing.T) {
	t.Parallel()

	for _, format := range goldenFormats {
		serializer, err := golden.SerializerFor(format)
		if err != nil {
			t.Fatalf("SerializerFor(%q) error = %v", format, err)
		}
		if got := serializer.Format(); got != format {
			t.Errorf("SerializerFor(%q).Format() = %q", format, got)
		}
	}

	// An empty format is JSON: that is the default everywhere else.
	serializer, err := golden.SerializerFor("")
	if err != nil {
		t.Fatalf("SerializerFor(\"\") error = %v", err)
	}
	if got := serializer.Format(); got != golden.JSON {
		t.Errorf("SerializerFor(\"\").Format() = %q, want %q", got, golden.JSON)
	}

	if _, err := golden.SerializerFor("protobuf"); err == nil {
		t.Error("SerializerFor accepted an unknown format")
	} else if !strings.Contains(err.Error(), "protobuf") {
		t.Errorf("error does not name the format: %v", err)
	}
}

func TestSerializerForPathFollowsTheExtension(t *testing.T) {
	t.Parallel()

	for _, format := range goldenFormats {
		path := "testdata/env" + format.DefaultSuffix()
		if got := golden.SerializerForPath(path).Format(); got != format {
			t.Errorf("SerializerForPath(%q).Format() = %q, want %q", path, got, format)
		}
	}
}

// TestEveryFormatRoundTrips covers the central promise: the three formats are
// three spellings of one fixture, and the XDR — the source of truth for every
// comparison — survives all of them.
func TestEveryFormatRoundTrips(t *testing.T) {
	t.Parallel()

	want := capturedFixture(t)

	for _, format := range goldenFormats {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			serializer, err := golden.SerializerFor(format)
			if err != nil {
				t.Fatalf("SerializerFor() error = %v", err)
			}

			data, err := serializer.Marshal(want)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			got, err := serializer.Unmarshal(data)
			if err != nil {
				t.Fatalf("Unmarshal() error = %v\n%s", err, data)
			}

			if got.Format != want.Format {
				t.Errorf("Format = %q, want %q", got.Format, want.Format)
			}
			if got.Type != want.Type {
				t.Errorf("Type = %q, want %q", got.Type, want.Type)
			}
			if got.Description != want.Description {
				t.Errorf("Description = %q, want %q", got.Description, want.Description)
			}
			// Comparison is always against the stored XDR, so a lost byte here
			// would make every assertion silently wrong.
			if got.XDR != want.XDR {
				t.Errorf("the stored XDR does not round-trip through %s", format)
			}
			if got.Captured == nil {
				t.Fatalf("provenance was lost through %s", format)
			}
			if *got.Captured != *want.Captured {
				t.Errorf("provenance = %+v, want %+v", *got.Captured, *want.Captured)
			}
			if !decodedEqual(t, want.Decoded, got.Decoded) {
				t.Errorf("the decoded section changed through %s:\n got  %s\n want %s",
					format, got.Decoded, want.Decoded)
			}
		})
	}
}

// TestEveryFormatRoundTripsAwkwardValues is the lossless half of the promise:
// a decoded tree that is only for humans must still not be corrupted, because
// a reviewer reads it to decide whether a change was intended.
func TestEveryFormatRoundTripsAwkwardValues(t *testing.T) {
	t.Parallel()

	want := trickyFixture()

	for _, format := range goldenFormats {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			serializer, err := golden.SerializerFor(format)
			if err != nil {
				t.Fatalf("SerializerFor() error = %v", err)
			}

			data, err := serializer.Marshal(want)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			got, err := serializer.Unmarshal(data)
			if err != nil {
				t.Fatalf("Unmarshal() error = %v\n%s", err, data)
			}

			if !decodedEqual(t, want.Decoded, got.Decoded) {
				t.Errorf("awkward values did not survive %s:\n got  %s\n want %s",
					format, got.Decoded, want.Decoded)
			}
			// A string that looks like a number must still be a string.
			var tree map[string]any
			if err := json.Unmarshal(got.Decoded, &tree); err != nil {
				t.Fatalf("decoded section is not JSON: %v", err)
			}
			if _, ok := tree["numericString"].(string); !ok {
				t.Errorf("\"58002\" came back as %T, not a string", tree["numericString"])
			}
		})
	}
}

// TestEveryFormatIsDeterministic checks the property a golden file exists for:
// the same fixture always produces the same bytes, so a diff means a change.
func TestEveryFormatIsDeterministic(t *testing.T) {
	t.Parallel()

	fixture := trickyFixture()

	for _, format := range goldenFormats {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			serializer, err := golden.SerializerFor(format)
			if err != nil {
				t.Fatalf("SerializerFor() error = %v", err)
			}

			first, err := serializer.Marshal(fixture)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			for i := 0; i < 5; i++ {
				again, err := serializer.Marshal(fixture)
				if err != nil {
					t.Fatalf("Marshal() error = %v", err)
				}
				if !bytes.Equal(first, again) {
					t.Fatalf("%s output changed between runs:\n%s\n---\n%s", format, first, again)
				}
			}
		})
	}
}

// TestOutputIsWhitespaceNormalized is the format's headline requirement: no
// CRLF, no trailing space, no blank-line run, and exactly one final newline.
func TestOutputIsWhitespaceNormalized(t *testing.T) {
	t.Parallel()

	for _, format := range goldenFormats {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			serializer, err := golden.SerializerFor(format)
			if err != nil {
				t.Fatalf("SerializerFor() error = %v", err)
			}
			data, err := serializer.Marshal(capturedFixture(t))
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			content := string(data)

			if strings.Contains(content, "\r") {
				t.Error("output contains a carriage return")
			}
			if !strings.HasSuffix(content, "\n") {
				t.Error("output does not end with a newline")
			}
			if strings.HasSuffix(content, "\n\n") {
				t.Error("output ends with more than one newline")
			}
			for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
				if strings.TrimRight(line, " \t") != line {
					t.Errorf("line has trailing whitespace: %q", line)
				}
			}
			if normalized := golden.NormalizeWhitespace(data); !bytes.Equal(normalized, data) {
				t.Error("output is not already normalized")
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "crlf", in: "a\r\nb\r\n", want: "a\nb\n"},
		{name: "bare cr", in: "a\rb\r", want: "a\nb\n"},
		{name: "trailing spaces", in: "a   \nb\t\n", want: "a\nb\n"},
		{name: "missing final newline", in: "a\nb", want: "a\nb\n"},
		{name: "extra final newlines", in: "a\n\n\n", want: "a\n"},
		{name: "blank line run", in: "a\n\n\n\nb\n", want: "a\n\nb\n"},
		{name: "leading blank lines", in: "\n\na\n", want: "\na\n"},
		{name: "already normal", in: "a\nb\n", want: "a\nb\n"},
		{name: "empty", in: "", want: ""},
		{name: "whitespace only", in: " \n\t\n", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := golden.NormalizeWhitespace([]byte(tc.in))
			if string(got) != tc.want {
				t.Errorf("NormalizeWhitespace(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := golden.NormalizeWhitespace(got); string(again) != string(got) {
				t.Errorf("NormalizeWhitespace is not idempotent: %q then %q", got, again)
			}
		})
	}
}

// TestFixturesAreReviewable checks each format is readable by a human reading
// a pull-request diff, which is the reason the decoded section is stored at
// all.
func TestFixturesAreReviewable(t *testing.T) {
	t.Parallel()

	for _, format := range goldenFormats {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			serializer, err := golden.SerializerFor(format)
			if err != nil {
				t.Fatalf("SerializerFor() error = %v", err)
			}
			data, err := serializer.Marshal(capturedFixture(t))
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			content := string(data)

			if lines := strings.Count(content, "\n"); lines < 20 {
				t.Errorf("%s fixture has only %d lines; a blob is not reviewable", format, lines)
			}
			// Field names a reviewer recognises, not just base64.
			for _, want := range []string{"SourceAccount", "Operations", "TransactionEnvelope"} {
				if !strings.Contains(content, want) {
					t.Errorf("%s fixture does not contain %q", format, want)
				}
			}
			// The decoded tree must be present in every format.
			if !strings.Contains(content, "decoded") {
				t.Errorf("%s fixture has no decoded section", format)
			}
		})
	}
}

// TestTextNamesLeavesByDottedPath documents the text format's point: a leaf is
// addressable the way a focus failure names it, so a fixture can be grepped.
func TestTextNamesLeavesByDottedPath(t *testing.T) {
	t.Parallel()

	serializer, err := golden.SerializerFor(golden.Text)
	if err != nil {
		t.Fatalf("SerializerFor() error = %v", err)
	}
	data, err := serializer.Marshal(capturedFixture(t))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"format: " + golden.FormatVersion,
		"captured.network: testnet",
		"decoded.type: TransactionEnvelope",
		"decoded.value.FeeBump.Tx.InnerTx.V1.Tx.Fee:",
		"decoded.value.FeeBump.Signatures[0].Hint:",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("text fixture does not contain %q", want)
		}
	}
}

// TestYAMLUsesBlockStyle checks the YAML is the shape a human hand-edits, and
// not JSON with the braces filed off.
func TestYAMLUsesBlockStyle(t *testing.T) {
	t.Parallel()

	serializer, err := golden.SerializerFor(golden.YAML)
	if err != nil {
		t.Fatalf("SerializerFor() error = %v", err)
	}
	data, err := serializer.Marshal(trickyFixture())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	content := string(data)

	if strings.Contains(content, "\t") {
		t.Error("YAML output contains a tab; tabs cannot indent YAML")
	}
	for _, want := range []string{
		"format: focus/v1\n",
		"empty: {}\n",
		"emptyList: []\n",
		// A sequence entry puts its block on the following lines, which is the
		// shape this reader implements.
		"nested:\n    -\n      - 1\n      - 2\n    -\n      - 3\n",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("YAML output does not contain %q:\n%s", want, content)
		}
	}
	// Reserved words and number-like strings must be quoted to stay strings.
	for _, want := range []string{`reserved: "yes"`, `numericString: "58002"`} {
		if !strings.Contains(content, want) {
			t.Errorf("YAML output does not quote %q:\n%s", want, content)
		}
	}
}

func TestSaveAndLoadChooseTheFormatByExtension(t *testing.T) {
	t.Parallel()

	fixture := capturedFixture(t)
	dir := t.TempDir()

	for _, format := range goldenFormats {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(dir, "env"+format.DefaultSuffix())
			if err := golden.Save(path, fixture); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			if len(raw) == 0 {
				t.Fatal("Save() wrote nothing")
			}

			got, err := golden.Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.XDR != fixture.XDR || got.Type != fixture.Type {
				t.Errorf("Load(%s) lost the fixture", path)
			}
		})
	}
}

func TestLoadAsAndSaveAsRejectAnUnknownFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	if err := golden.SaveAs(path, capturedFixture(t), "unsupported_fmt"); err == nil {
		t.Error("SaveAs accepted an unknown format")
	}
	if _, err := golden.LoadAs(path, "unsupported_fmt"); err == nil {
		t.Error("LoadAs accepted an unknown format")
	} else if !strings.Contains(err.Error(), "unsupported_fmt") {
		t.Errorf("error does not name the format: %v", err)
	}
}

// TestGoldenUsesTheConfiguredFormat is the integration point: a Golden told to
// store YAML or text must read that file back, and its failure message must
// still describe the changed field.
func TestGoldenUsesTheConfiguredFormat(t *testing.T) {
	t.Parallel()

	stored := buildEnvelope(t, 100)
	actual := buildEnvelope(t, 200)

	for _, format := range goldenFormats {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "env"+format.DefaultSuffix())

			fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if err := golden.SaveAs(path, fixture, format); err != nil {
				t.Fatalf("SaveAs() error = %v", err)
			}

			// A matching value passes.
			passing := faketb.New()
			g := golden.New(passing, golden.WithDir(dir), golden.WithFormat(format),
				golden.WithType("TransactionEnvelope"))
			if !g.Assert("env", stored) {
				t.Fatalf("Assert() failed against a matching %s fixture:\n%s", format, passing.Output())
			}

			// A changed amount must fail with the diff and the remedy.
			failing := faketb.New()
			g = golden.New(failing, golden.WithDir(dir), golden.WithFormat(format),
				golden.WithType("TransactionEnvelope"))
			if g.Assert("env", actual) {
				t.Fatalf("Assert() passed against a stale %s fixture", format)
			}
			out := failing.Output()
			if !strings.Contains(out, "Body.PaymentOp.Amount") {
				t.Errorf("%s failure does not name the changed field:\n%s", format, out)
			}
			if !strings.Contains(out, "is out of date") || !strings.Contains(out, "-focus.update") {
				t.Errorf("%s failure does not say how to re-record the fixture:\n%s", format, out)
			}
		})
	}
}

// TestGoldenWithFormatMovesTheSuffix checks the convenience the option
// promises, and that an explicit suffix still wins when it is set after it.
func TestGoldenWithFormatMovesTheSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []golden.Opt
		want string
	}{
		{name: "default", want: filepath.Join("testdata", "env.golden.json")},
		{name: "yaml", opts: []golden.Opt{golden.WithFormat(golden.YAML)}, want: filepath.Join("testdata", "env.golden.yaml")},
		{name: "text", opts: []golden.Opt{golden.WithFormat(golden.Text)}, want: filepath.Join("testdata", "env.golden.txt")},
		{
			name: "explicit suffix after the format",
			opts: []golden.Opt{golden.WithFormat(golden.YAML), golden.WithSuffix(".fixture")},
			want: filepath.Join("testdata", "env.fixture"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := golden.New(faketb.New(), tc.opts...).Path("env"); got != tc.want {
				t.Errorf("Path(env) = %q, want %q", got, tc.want)
			}
		})
	}
}

// updateDirEnv tells the child process of TestGoldenUpdateWritesEveryFormat
// where to write, since a test binary cannot pass arguments to itself any
// other way.
const updateDirEnv = "FOCUS_UPDATE_TEST_DIR"

// TestGoldenUpdateWritesEveryFormat runs this test binary again with
// -focus.update — the flag is a package-level global, so re-executing is the
// only way to exercise the recording path a user actually runs.
func TestGoldenUpdateWritesEveryFormat(t *testing.T) {
	if golden.Update() {
		recordEveryFormat(t)
		return
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoldenUpdateWritesEveryFormat$", "-focus.update")
	cmd.Env = append(os.Environ(), updateDirEnv+"="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("-focus.update run failed: %v\n%s", err, out)
	}

	want := envelope(t)
	for _, format := range goldenFormats {
		path := filepath.Join(dir, "envelope"+format.DefaultSuffix())

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("-focus.update wrote no %s fixture: %v", format, err)
		}
		if lines := strings.Count(string(raw), "\n"); lines < 20 {
			t.Errorf("the recorded %s fixture is a blob, not reviewable", format)
		}

		got, err := golden.LoadAs(path, format)
		if err != nil {
			t.Fatalf("the recorded %s fixture does not load: %v", format, err)
		}
		if got.XDR != want {
			t.Errorf("the recorded %s fixture has the wrong XDR", format)
		}
		if got.Description != "recorded under -focus.update" {
			t.Errorf("the recorded %s fixture lost its description: %q", format, got.Description)
		}
	}
}

// recordEveryFormat is the child half of TestGoldenUpdateWritesEveryFormat.
func recordEveryFormat(t *testing.T) {
	t.Helper()

	dir := os.Getenv(updateDirEnv)
	if dir == "" {
		t.Skip("not the -focus.update child run")
	}

	for _, format := range goldenFormats {
		g := golden.New(t, golden.WithDir(dir), golden.WithFormat(format),
			golden.WithType("TransactionEnvelope"),
			golden.WithDescription("recorded under -focus.update"))
		if !g.Assert("envelope", envelope(t)) {
			t.Errorf("%s: Assert() failed while recording", format)
		}
	}
}

// TestUnmarshalRejectsBadFixtures checks each reader fails loudly on the input
// that would otherwise be misread: a fixture from a future version, one that
// lost its payload, and one whose fields have the wrong shape.
func TestUnmarshalRejectsBadFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		format  golden.Format
		data    string
		wantErr string
	}{
		{
			name: "json unknown version", format: golden.JSON,
			data:    `{"format":"focus/v99","type":"TransactionEnvelope","xdr":"AAAA"}`,
			wantErr: "focus/v99",
		},
		{
			name: "json no xdr", format: golden.JSON,
			data:    `{"format":"focus/v1","type":"TransactionEnvelope"}`,
			wantErr: "no xdr field",
		},
		{
			name: "json malformed", format: golden.JSON,
			data:    `{"format":`,
			wantErr: "invalid JSON",
		},
		{
			name: "yaml unknown version", format: golden.YAML,
			data:    "format: focus/v99\ntype: TransactionEnvelope\nxdr: AAAA\n",
			wantErr: "focus/v99",
		},
		{
			name: "yaml no xdr", format: golden.YAML,
			data:    "format: focus/v1\ntype: TransactionEnvelope\n",
			wantErr: "no xdr field",
		},
		{
			name: "yaml empty", format: golden.YAML,
			data:    "\n\n# only a comment\n",
			wantErr: "empty",
		},
		{
			name: "yaml not a mapping", format: golden.YAML,
			data:    "- a\n- b\n",
			wantErr: "top level must be a mapping",
		},
		{
			name: "yaml wrong field type", format: golden.YAML,
			data:    "format: focus/v1\ntype: 5\nxdr: AAAA\n",
			wantErr: "type must be a string",
		},
		{
			name: "yaml bad provenance", format: golden.YAML,
			data:    "format: focus/v1\nxdr: AAAA\ncaptured: nonsense\n",
			wantErr: "captured must be a mapping",
		},
		{
			name: "yaml bad ledger", format: golden.YAML,
			data:    "format: focus/v1\nxdr: AAAA\ncaptured:\n  ledger: 99999999999999\n",
			wantErr: "captured.ledger",
		},
		{
			name: "yaml ledger not a number", format: golden.YAML,
			data:    "format: focus/v1\nxdr: AAAA\ncaptured:\n  ledger: mainnet\n",
			wantErr: "captured.ledger must be a number",
		},
		{
			name: "yaml tab indent", format: golden.YAML,
			data:    "format: focus/v1\n\ttype: TransactionEnvelope\n",
			wantErr: "tab",
		},
		{
			name: "text unknown version", format: golden.Text,
			data:    "format: focus/v99\ntype: TransactionEnvelope\nxdr: AAAA\n",
			wantErr: "focus/v99",
		},
		{
			name: "text no xdr", format: golden.Text,
			data:    "format: focus/v1\ntype: TransactionEnvelope\n",
			wantErr: "no xdr field",
		},
		{
			name: "text empty", format: golden.Text,
			data:    "\n# nothing here\n",
			wantErr: "empty",
		},
		{
			name: "text line without a separator", format: golden.Text,
			data:    "format: focus/v1\nnot a field\n",
			wantErr: `expected "path: value"`,
		},
		{
			name: "text mixed container", format: golden.Text,
			data:    "format: focus/v1\nxdr: AAAA\ndecoded.a: 1\ndecoded.a.b: 2\n",
			wantErr: "both a value and a container",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			serializer, err := golden.SerializerFor(tc.format)
			if err != nil {
				t.Fatalf("SerializerFor() error = %v", err)
			}
			if _, err := serializer.Unmarshal([]byte(tc.data)); err == nil {
				t.Fatalf("Unmarshal() accepted bad input:\n%s", tc.data)
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestUnmarshalAcceptsHandWrittenAndNoisyFiles covers the leniency the formats
// are allowed: blank lines, comments, document markers, a compact sequence
// entry, quoted keys, and CRLF line endings all describe the same fixture.
func TestUnmarshalAcceptsHandWrittenAndNoisyFiles(t *testing.T) {
	t.Parallel()

	const header = "format: focus/v1\ntype: TransactionEnvelope\nxdr: AAAA\n"

	tests := []struct {
		format    golden.Format
		canonical string
		noisy     string
	}{
		{
			format:    golden.YAML,
			canonical: header + "captured:\n  network: testnet\n  ledger: 3966820\n",
			noisy: "---\r\n" +
				"\r\n" +
				"# a hand-written fixture\r\n" +
				"format: focus/v1\r\n" +
				"type: TransactionEnvelope\r\n" +
				"xdr: AAAA\r\n" +
				"captured:\r\n" +
				"  network: testnet   \r\n" +
				"  ledger: 3966820\r\n" +
				"\r\n" +
				"...\r\n",
		},
		{
			format:    golden.Text,
			canonical: header + "captured.network: testnet\ncaptured.ledger: 3966820\n",
			noisy: "\r\n" +
				"# a hand-written fixture\r\n" +
				"format: focus/v1\r\n" +
				"type: TransactionEnvelope\r\n" +
				"xdr: AAAA\r\n" +
				"\r\n" +
				"captured.network: testnet   \r\n" +
				"captured.ledger: 3966820\r\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.format.String(), func(t *testing.T) {
			t.Parallel()

			serializer, err := golden.SerializerFor(tc.format)
			if err != nil {
				t.Fatalf("SerializerFor() error = %v", err)
			}

			want, err := serializer.Unmarshal([]byte(tc.canonical))
			if err != nil {
				t.Fatalf("Unmarshal(canonical) error = %v", err)
			}
			got, err := serializer.Unmarshal([]byte(tc.noisy))
			if err != nil {
				t.Fatalf("Unmarshal(noisy) error = %v", err)
			}
			if got.XDR != want.XDR || got.Captured == nil ||
				got.Captured.Ledger != want.Captured.Ledger {
				t.Errorf("the noisy %s fixture is not the same fixture: %+v vs %+v", tc.format, got, want)
			}
		})
	}
}

// TestYAMLReadsCompactSequenceEntries covers the one shape most hand-written
// YAML uses that the writer spells out: "- key: value".
func TestYAMLReadsCompactSequenceEntries(t *testing.T) {
	t.Parallel()

	const doc = "format: focus/v1\n" +
		"type: TransactionEnvelope\n" +
		"xdr: AAAA\n" +
		"decoded:\n" +
		"  operations:\n" +
		"    - type: payment\n" +
		"      amount: 100\n" +
		"    - type: change_trust\n" +
		"      amount: 200\n"

	serializer, err := golden.SerializerFor(golden.YAML)
	if err != nil {
		t.Fatalf("SerializerFor() error = %v", err)
	}
	fixture, err := serializer.Unmarshal([]byte(doc))
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	var tree struct {
		Operations []struct {
			Type   string `json:"type"`
			Amount int    `json:"amount"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(fixture.Decoded, &tree); err != nil {
		t.Fatalf("decoded section: %v", err)
	}
	if len(tree.Operations) != 2 || tree.Operations[0].Type != "payment" ||
		tree.Operations[1].Amount != 200 {
		t.Errorf("compact sequence entries were misread: %+v", tree.Operations)
	}
}

// decodedEqual compares two decoded sections as data rather than as bytes.
// Each format renders the tree with its own whitespace and key order, and the
// comparison path never reads it — the XDR does that.
func decodedEqual(t *testing.T, want, got json.RawMessage) bool {
	t.Helper()
	return reflect.DeepEqual(canonicalJSON(t, want), canonicalJSON(t, got))
}

// canonicalJSON decodes JSON into a generic tree, keeping numbers as
// json.Number so an int64 amount is not rounded through a float64.
func canonicalJSON(t *testing.T, raw []byte) any {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return v
}

func TestTOMLSerializerDeterministicTableKeyOrdering(t *testing.T) {
	t.Parallel()

	serializer, err := golden.SerializerFor(golden.TOML)
	if err != nil {
		t.Fatalf("SerializerFor(TOML) error = %v", err)
	}

	fixture := &golden.Fixture{
		Format:      golden.FormatVersion,
		Type:        "TransactionEnvelope",
		Description: "deterministic key ordering test",
		XDR:         "AAAA",
		Decoded: json.RawMessage(`{
			"zeta": 1,
			"alpha": 2,
			"nested": {
				"z": 10,
				"a": 20
			},
			"beta": "test"
		}`),
	}

	first, err := serializer.Marshal(fixture)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	// Verify key order: alpha before beta, beta before zeta.
	firstStr := string(first)
	alphaIdx := strings.Index(firstStr, "alpha = ")
	betaIdx := strings.Index(firstStr, "beta = ")
	zetaIdx := strings.Index(firstStr, "zeta = ")
	if alphaIdx == -1 || betaIdx == -1 || zetaIdx == -1 {
		t.Fatalf("keys missing from output:\n%s", firstStr)
	}
	if !(alphaIdx < betaIdx && betaIdx < zetaIdx) {
		t.Errorf("keys not sorted alphabetically in TOML:\n%s", firstStr)
	}

	for i := 0; i < 5; i++ {
		again, err := serializer.Marshal(fixture)
		if err != nil {
			t.Fatalf("Marshal() iteration %d error = %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("TOML serializer is not deterministic:\n%s\nvs\n%s", first, again)
		}
	}
}

func TestTOMLSerializerMultilineStrings(t *testing.T) {
	t.Parallel()

	serializer, err := golden.SerializerFor(golden.TOML)
	if err != nil {
		t.Fatalf("SerializerFor(TOML) error = %v", err)
	}

	tomlDoc := `format = "focus/v1"
type = "TransactionEnvelope"
description = """
Line one of description
Line two of description
"""
xdr = "AAAA"
`
	fixture, err := serializer.Unmarshal([]byte(tomlDoc))
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	wantDesc := "Line one of description\nLine two of description\n"
	if fixture.Description != wantDesc {
		t.Errorf("Description = %q, want %q", fixture.Description, wantDesc)
	}
}

func TestTOMLSerializerArrayOfTables(t *testing.T) {
	t.Parallel()

	serializer, err := golden.SerializerFor(golden.TOML)
	if err != nil {
		t.Fatalf("SerializerFor(TOML) error = %v", err)
	}

	tomlDoc := `format = "focus/v1"
type = "TransactionEnvelope"
xdr = "AAAA"

[decoded]
name = "batch"

[[decoded.operations]]
type = "payment"
amount = 100

[[decoded.operations]]
type = "bump_sequence"
amount = 200
`
	fixture, err := serializer.Unmarshal([]byte(tomlDoc))
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if fixture.XDR != "AAAA" || fixture.Type != "TransactionEnvelope" {
		t.Fatalf("fixture header lost: %+v", fixture)
	}

	var tree struct {
		Name       string `json:"name"`
		Operations []struct {
			Type   string `json:"type"`
			Amount int    `json:"amount"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(fixture.Decoded, &tree); err != nil {
		t.Fatalf("decoding json tree: %v", err)
	}
	if tree.Name != "batch" || len(tree.Operations) != 2 || tree.Operations[0].Type != "payment" || tree.Operations[1].Amount != 200 {
		t.Errorf("operations table array misread: %+v", tree)
	}
}
