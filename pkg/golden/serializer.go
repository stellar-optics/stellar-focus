// Fixture serialization for golden files.
//
// A fixture is the same data however it is stored: the base64 XDR that is the
// source of truth, the provenance of a capture, and a decoded rendering a
// reviewer reads in a pull request. This file is the seam that keeps the
// encodings of that data — reviewable JSON, hand-editable YAML, and a flat
// line-oriented text form — behind one interface, so nothing above it has to
// know which file a fixture came from.
//
// Two properties are load-bearing and are tested as such:
//
//   - Deterministic. No clock, no network, and no map iteration order reaches
//     the output, so the same fixture always serializes to the same bytes.
//   - Normalized. Marshal runs its output through NormalizeWhitespace, so a
//     CRLF checkout or a stray trailing space cannot turn into diff noise.
//
// YAML is implemented here rather than taken from a dependency on purpose.
// pkg/golden is imported into other people's test suites, and the permitted
// dependency set is deliberately the standard library, the Stellar SDK and
// lens — enforced by TestLibraryStaysDependencyLight. A YAML library would be
// inherited by every consumer, so the supported YAML is a documented subset:
// what this file writes.
//
// The subset is block mappings, block sequences and single-line scalars, plus
// the flow forms "{}" and "[]". Anchors, aliases, tags, block scalars,
// multi-document streams and compact mappings on a dash line are not read;
// the reader rejects what it does not understand rather than guessing.
package golden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Format identifies how a fixture is encoded on disk.
type Format string

// The formats a fixture can be stored in.
//
// JSON is the default: it is what the README documents and what -focus.update
// writes unless told otherwise. YAML is the same data in a form that is
// pleasant to hand-edit. Text flattens the fixture to dotted paths — the
// vocabulary lens already uses to name a changed field — so a fixture reads
// like the failure message that produced it and can be grepped without a
// parser in the loop.
const (
	// JSON is encoding/json's indented rendering of a Fixture.
	JSON Format = "json"
	// YAML is block-style YAML, restricted to the subset documented above.
	YAML Format = "yaml"
	// Text is a flat "dotted.path: value" listing, one leaf per line.
	Text Format = "text"
)

// ParseFormat converts a format name into a Format, accepting "json", "yaml"
// (or "yml") and "text" (or "txt"), case-insensitively.
func ParseFormat(name string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "json":
		return JSON, nil
	case "yaml", "yml":
		return YAML, nil
	case "text", "txt":
		return Text, nil
	default:
		return "", fmt.Errorf("unknown fixture format %q; supported formats are %q, %q and %q",
			name, JSON, YAML, Text)
	}
}

// String returns the format's canonical name.
func (f Format) String() string { return string(f) }

// DefaultSuffix is the file suffix fixtures of this format are written with.
//
// The suffix starts with ".golden." so a fixture is recognisable in a
// testdata directory next to the captured XDR it was built from.
func (f Format) DefaultSuffix() string {
	switch f {
	case YAML:
		return ".golden.yaml"
	case Text:
		return ".golden.txt"
	default:
		return ".golden.json"
	}
}

// FormatForPath reports the format a file name implies, so Load and Save keep
// working from a path alone. An unrecognised extension is read as JSON, which
// is what every fixture written before these formats existed is.
func FormatForPath(path string) Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return YAML
	case ".txt":
		return Text
	default:
		return JSON
	}
}

// Serializer encodes and decodes fixtures in one format.
//
// It is the seam a project can substitute if it needs a fixture encoding this
// package does not ship, without the comparison path knowing.
type Serializer interface {
	// Format reports which format this serializer handles.
	Format() Format
	// Marshal encodes a fixture, with whitespace normalized.
	Marshal(f *Fixture) ([]byte, error)
	// Unmarshal decodes a fixture and validates it.
	Unmarshal(data []byte) (*Fixture, error)
}

// SerializerFor returns the serializer for a format. An empty format means
// JSON, which is the default everywhere else in the package.
func SerializerFor(format Format) (Serializer, error) {
	switch format {
	case "", JSON:
		return jsonSerializer{}, nil
	case YAML:
		return yamlSerializer{}, nil
	case Text:
		return textSerializer{}, nil
	default:
		return nil, fmt.Errorf("unknown fixture format %q; supported formats are %q, %q and %q",
			format, JSON, YAML, Text)
	}
}

// SerializerForPath returns the serializer for the format a file name implies.
func SerializerForPath(path string) Serializer {
	switch FormatForPath(path) {
	case YAML:
		return yamlSerializer{}
	case Text:
		return textSerializer{}
	default:
		return jsonSerializer{}
	}
}

// NormalizeWhitespace rewrites serialized output so the same fixture always
// produces the same bytes: line endings become "\n", trailing horizontal
// whitespace is dropped from every line, runs of blank lines collapse to one,
// and the result ends with exactly one newline.
//
// Every serializer's output goes through this, which is what makes a golden
// file stable in review — no churn from a CRLF checkout, a trailing space, or
// "no newline at end of file".
func NormalizeWhitespace(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, line)
	}
	// A run of blank lines at the end is all of it; the single newline added
	// below is the one that returns.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// validate rejects a fixture this version cannot read. Both checks exist so a
// file written by a future version, or one that lost its payload, fails loudly
// instead of comparing equal to nothing.
func validate(f *Fixture) error {
	return Validate(f)
}

// number renders an unsigned integer as a JSON number, so a value that is
// numeric in the fixture stays numeric in YAML and in the text form.
func number(v uint32) json.Number {
	return json.Number(strconv.FormatUint(uint64(v), 10))
}

// JSON.

type jsonSerializer struct{}

// Format implements Serializer.
func (jsonSerializer) Format() Format { return JSON }

// Marshal implements Serializer, writing the indented, line-oriented JSON the
// package has always written. Field order is the struct order, so identity
// comes first and the large decoded tree last.
func (jsonSerializer) Marshal(f *Fixture) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("encoding fixture as JSON: %w", err)
	}
	return NormalizeWhitespace(buf.Bytes()), nil
}

// Unmarshal implements Serializer. The decoded section is kept as raw JSON
// rather than being re-encoded, so a fixture round-trips byte for byte.
func (jsonSerializer) Unmarshal(data []byte) (*Fixture, error) {
	if err := validateFixtureJSON(data); err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := validate(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

// YAML.

type yamlSerializer struct{}

// Format implements Serializer.
func (yamlSerializer) Format() Format { return YAML }

// Marshal implements Serializer, writing block-style YAML.
//
// Keys are emitted in a fixed order — the fixture's fields first, then sorted
// keys for the decoded tree — because two runs must produce identical bytes.
// Unmarshal reads exactly what this writes, so a fixture written here always
// reads back.
func (yamlSerializer) Marshal(f *Fixture) ([]byte, error) {
	var buf bytes.Buffer

	writeYAMLPair(&buf, 0, "format", f.Format)
	writeYAMLPair(&buf, 0, "type", f.Type)
	if f.Description != "" {
		writeYAMLPair(&buf, 0, "description", f.Description)
	}
	writeYAMLPair(&buf, 0, "xdr", f.XDR)

	if p := f.Captured; p != nil {
		writeYAMLIndent(&buf, 0)
		buf.WriteString("captured:\n")
		writeYAMLPair(&buf, 1, "network", p.Network)
		if p.Ledger != 0 {
			writeYAMLPair(&buf, 1, "ledger", number(p.Ledger))
		}
		if p.TxHash != "" {
			writeYAMLPair(&buf, 1, "txHash", p.TxHash)
		}
		writeYAMLPair(&buf, 1, "at", p.At)
		if p.Endpoint != "" {
			writeYAMLPair(&buf, 1, "endpoint", p.Endpoint)
		}
	}

	if len(f.Decoded) > 0 {
		tree, err := decodeTree(f.Decoded)
		if err != nil {
			return nil, fmt.Errorf("reading the decoded section: %w", err)
		}
		writeYAMLPair(&buf, 0, "decoded", tree)
	}

	return NormalizeWhitespace(buf.Bytes()), nil
}

// Unmarshal implements Serializer, reading the subset Marshal writes.
func (yamlSerializer) Unmarshal(data []byte) (*Fixture, error) {
	// Normalizing first means a checkout that rewrote the line endings, or a
	// hand-edit that left a trailing space, still reads as the same fixture.
	lines, err := splitYAMLLines(NormalizeWhitespace(data))
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("fixture is empty")
	}

	p := &yamlParser{lines: lines}
	tree, err := p.parseBlock(0)
	if err != nil {
		return nil, err
	}
	if !p.done() {
		return nil, p.errorf(p.cur(), "unexpected trailing content")
	}
	return fixtureFromTree(tree)
}

// writeYAMLIndent writes the indentation for a block level.
func writeYAMLIndent(buf *bytes.Buffer, level int) {
	for i := 0; i < level; i++ {
		buf.WriteString("  ")
	}
}

// writeYAMLPair writes one "key: value" line, or a key followed by the nested
// block that is its value.
func writeYAMLPair(buf *bytes.Buffer, level int, key string, v any) {
	writeYAMLIndent(buf, level)
	buf.WriteString(scalarText(key))
	buf.WriteByte(':')

	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			buf.WriteString(" {}\n")
			return
		}
		buf.WriteByte('\n')
		writeYAMLMap(buf, level+1, val)
	case []any:
		if len(val) == 0 {
			buf.WriteString(" []\n")
			return
		}
		buf.WriteByte('\n')
		writeYAMLSeq(buf, level+1, val)
	default:
		buf.WriteByte(' ')
		buf.WriteString(scalarText(v))
		buf.WriteByte('\n')
	}
}

// writeYAMLMap writes a mapping's entries in sorted key order.
func writeYAMLMap(buf *bytes.Buffer, level int, m map[string]any) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeYAMLPair(buf, level, k, m[k])
	}
}

// writeYAMLSeq writes a sequence. Non-scalar entries put their block on the
// lines after a bare dash, which is one shape to parse instead of two.
func writeYAMLSeq(buf *bytes.Buffer, level int, items []any) {
	for _, item := range items {
		writeYAMLIndent(buf, level)
		buf.WriteByte('-')

		switch val := item.(type) {
		case map[string]any:
			if len(val) == 0 {
				buf.WriteString(" {}\n")
				continue
			}
			buf.WriteByte('\n')
			writeYAMLMap(buf, level+1, val)
		case []any:
			if len(val) == 0 {
				buf.WriteString(" []\n")
				continue
			}
			buf.WriteByte('\n')
			writeYAMLSeq(buf, level+1, val)
		default:
			buf.WriteByte(' ')
			buf.WriteString(scalarText(item))
			buf.WriteByte('\n')
		}
	}
}

// yamlLine is one significant line of a YAML document: blank lines, comments
// and document markers are dropped before parsing starts.
type yamlLine struct {
	indent int
	text   string
	num    int
}

// splitYAMLLines drops the lines that carry no data and rewrites a compact
// sequence entry ("- key: value") into the expanded form the parser reads, so
// hand-written YAML in the common style is understood without a second parse
// path.
func splitYAMLLines(data []byte) ([]yamlLine, error) {
	var out []yamlLine
	for i, raw := range strings.Split(string(data), "\n") {
		num := i + 1

		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		if indent < len(raw) && raw[indent] == '\t' {
			return nil, fmt.Errorf("line %d: a tab cannot be used for indentation", num)
		}

		text := strings.TrimRight(raw[indent:], " \t")
		if text == "" || text == "---" || text == "..." || strings.HasPrefix(text, "#") {
			continue
		}

		if rest, ok := compactSeqBody(text); ok {
			out = append(out,
				yamlLine{indent: indent, text: "-", num: num},
				yamlLine{indent: indent + 2, text: rest, num: num})
			continue
		}
		out = append(out, yamlLine{indent: indent, text: text, num: num})
	}
	return out, nil
}

// compactSeqBody reports whether a sequence entry line carries the start of a
// nested block after its dash, returning that block's first line.
func compactSeqBody(text string) (string, bool) {
	rest, ok := strings.CutPrefix(text, "- ")
	if !ok {
		return "", false
	}
	rest = strings.TrimRight(rest, " \t")
	if rest == "" {
		return "", false
	}
	if _, _, isKey := splitYAMLKey(rest); isKey {
		return rest, true
	}
	if isSeqEntry(rest) {
		return rest, true
	}
	return "", false
}

// isSeqEntry reports whether a line is a sequence entry.
func isSeqEntry(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ")
}

// yamlParser walks the significant lines of a document.
type yamlParser struct {
	lines []yamlLine
	pos   int
}

func (p *yamlParser) done() bool    { return p.pos >= len(p.lines) }
func (p *yamlParser) cur() yamlLine { return p.lines[p.pos] }
func (p *yamlParser) errorf(line yamlLine, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", line.num, fmt.Sprintf(format, args...))
}

// parseBlock parses the mapping or sequence starting at the current line,
// taking that line's indentation as the block's. minIndent is the smallest
// indentation the block may use, which is what keeps nesting honest.
func (p *yamlParser) parseBlock(minIndent int) (any, error) {
	line := p.cur()
	if line.indent < minIndent {
		return nil, p.errorf(line, "unexpected indentation: %d spaces, want at least %d",
			line.indent, minIndent)
	}
	if isSeqEntry(line.text) {
		return p.parseSeq(line.indent)
	}
	return p.parseMap(line.indent)
}

// parseMap parses the mapping whose keys are indented by indent.
func (p *yamlParser) parseMap(indent int) (any, error) {
	out := make(map[string]any)

	for !p.done() {
		line := p.cur()
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, p.errorf(line, "unexpected indentation: %d spaces, want %d", line.indent, indent)
		}
		if isSeqEntry(line.text) {
			return nil, p.errorf(line, "a sequence entry cannot appear inside a mapping")
		}
		if strings.HasPrefix(line.text, "? ") {
			return nil, p.errorf(line, "explicit keys are not supported")
		}

		key, rest, ok := splitYAMLKey(line.text)
		if !ok {
			return nil, p.errorf(line, "expected \"key: value\", got %q", line.text)
		}
		if _, dup := out[key]; dup {
			return nil, p.errorf(line, "duplicate key %q", key)
		}
		p.pos++

		if rest != "" {
			v, err := parseYAMLValue(rest)
			if err != nil {
				return nil, p.errorf(line, "%s", err)
			}
			out[key] = v
			continue
		}
		if p.done() || p.cur().indent <= indent {
			out[key] = nil
			continue
		}
		v, err := p.parseBlock(indent + 1)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// parseSeq parses the sequence entries indented by indent.
func (p *yamlParser) parseSeq(indent int) (any, error) {
	out := []any{}

	for !p.done() {
		line := p.cur()
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, p.errorf(line, "unexpected indentation: %d spaces, want %d", line.indent, indent)
		}
		if !isSeqEntry(line.text) {
			return nil, p.errorf(line, "expected a \"-\" sequence entry, got %q", line.text)
		}

		rest := strings.TrimSpace(strings.TrimPrefix(line.text, "-"))
		p.pos++

		if rest != "" {
			v, err := parseYAMLValue(rest)
			if err != nil {
				return nil, p.errorf(line, "%s", err)
			}
			out = append(out, v)
			continue
		}
		if p.done() || p.cur().indent <= indent {
			out = append(out, nil)
			continue
		}
		v, err := p.parseBlock(indent + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// parseYAMLValue reads the value that follows a key or a dash on the same
// line. A nested block has to be on the following lines, which is the shape
// Marshal writes.
func parseYAMLValue(text string) (any, error) {
	if isSeqEntry(text) {
		return nil, fmt.Errorf("a nested block must be indented on the following lines")
	}
	return parseScalarToken(text)
}

// splitYAMLKey splits a "key: value" line into its key and the remainder. The
// separator is a colon that ends the line or is followed by a space, so a
// colon inside a value does not split — and the writer quotes a key that
// contains one, so a quoted key is read here too.
func splitYAMLKey(text string) (key, rest string, ok bool) {
	if text == "" {
		return "", "", false
	}

	if q := text[0]; q == '"' || q == '\'' {
		end, err := scanQuoted(text, q)
		if err != nil {
			return "", "", false
		}
		key, err = unquoteYAML(text[:end])
		if err != nil {
			return "", "", false
		}
		after := strings.TrimSpace(text[end:])
		if !strings.HasPrefix(after, ":") {
			return "", "", false
		}
		return key, strings.TrimSpace(after[1:]), true
	}

	i := strings.IndexByte(text, ':')
	if i < 0 {
		return "", "", false
	}
	if i+1 < len(text) && text[i+1] != ' ' {
		return "", "", false
	}
	return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:]), true
}

// scanQuoted returns the index just past the closing quote of the scalar that
// starts text, honouring backslash escapes in double quotes and the doubled
// quote in single quotes.
func scanQuoted(text string, quote byte) (int, error) {
	for i := 1; i < len(text); i++ {
		switch {
		case text[i] == '\\' && quote == '"':
			i++
		case text[i] == quote:
			if quote == '\'' && i+1 < len(text) && text[i+1] == '\'' {
				i++
				continue
			}
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated %c-quoted scalar", quote)
}

// unquoteYAML decodes one quoted scalar.
func unquoteYAML(raw string) (string, error) {
	if strings.HasPrefix(raw, "'") {
		return parseSingleQuoted(raw)
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return "", fmt.Errorf("malformed quoted scalar %q: %w", raw, err)
	}
	return s, nil
}

// parseSingleQuoted reads a YAML single-quoted scalar, in which the only
// escape is a doubled quote.
func parseSingleQuoted(text string) (string, error) {
	if len(text) < 2 || !strings.HasSuffix(text, "'") {
		return "", fmt.Errorf("malformed single-quoted scalar %q", text)
	}
	body := text[1 : len(text)-1]
	// A quote that is not doubled would have ended the scalar earlier, so it
	// means the scalar is malformed rather than that it contains a quote.
	if strings.Contains(strings.ReplaceAll(body, "''", ""), "'") {
		return "", fmt.Errorf("malformed single-quoted scalar %q", text)
	}
	return strings.ReplaceAll(body, "''", "'"), nil
}

// Text.

type textSerializer struct{}

// Format implements Serializer.
func (textSerializer) Format() Format { return Text }

// Marshal implements Serializer, writing one leaf per line named by its dotted
// path — "decoded.V1.Tx.Fee", "decoded.V1.Tx.Operations[0].Body.Type" — the
// same vocabulary a focus failure names a changed field with.
//
// Scalars are written the way YAML writes them, so a quoted value means the
// same thing in both formats and one reader handles both.
func (textSerializer) Marshal(f *Fixture) ([]byte, error) {
	var lines []string
	add := func(path string, v any) {
		lines = append(lines, path+": "+scalarText(v))
	}

	add("format", f.Format)
	add("type", f.Type)
	if f.Description != "" {
		add("description", f.Description)
	}
	add("xdr", f.XDR)

	if p := f.Captured; p != nil {
		add("captured.network", p.Network)
		if p.Ledger != 0 {
			add("captured.ledger", number(p.Ledger))
		}
		if p.TxHash != "" {
			add("captured.txHash", p.TxHash)
		}
		add("captured.at", p.At)
		if p.Endpoint != "" {
			add("captured.endpoint", p.Endpoint)
		}
	}

	if len(f.Decoded) > 0 {
		tree, err := decodeTree(f.Decoded)
		if err != nil {
			return nil, fmt.Errorf("reading the decoded section: %w", err)
		}
		lines = append(lines, flattenTree("decoded", tree)...)
	}

	return NormalizeWhitespace([]byte(strings.Join(lines, "\n") + "\n")), nil
}

// Unmarshal implements Serializer.
func (textSerializer) Unmarshal(data []byte) (*Fixture, error) {
	var entries []flatEntry

	for i, raw := range strings.Split(string(NormalizeWhitespace(data)), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		path, token, ok := splitTextLine(line)
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"path: value\", got %q", i+1, line)
		}
		segs, err := parsePath(path)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		val, err := parseScalarToken(token)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		entries = append(entries, flatEntry{segs: segs, val: val})
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("fixture is empty")
	}
	tree, err := buildTree(entries)
	if err != nil {
		return nil, err
	}
	return fixtureFromTree(tree)
}

// splitTextLine splits "dotted.path: value" at the first unescaped colon that
// ends a key. The writer escapes a colon inside a key, so the first one that
// is followed by a space is always the separator.
func splitTextLine(line string) (path, value string, ok bool) {
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' {
			i++ // skip the escaped character
			continue
		}
		if line[i] == ':' && i+1 < len(line) && line[i+1] == ' ' {
			return line[:i], line[i+2:], true
		}
	}
	return "", "", false
}

// pathSegment is one step of a dotted path: a mapping key or an array index.
type pathSegment struct {
	key   string
	index int
	isIdx bool
}

// flatEntry is one "path: value" line with its path already split.
type flatEntry struct {
	segs []pathSegment
	val  any
}

// flattenTree renders a decoded value as dotted paths, one leaf per line.
func flattenTree(prefix string, v any) []string {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			return []string{prefix + ": {}"}
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, flattenTree(prefix+"."+escapePathKey(k), val[k])...)
		}
		return out
	case []any:
		if len(val) == 0 {
			return []string{prefix + ": []"}
		}
		var out []string
		for i, item := range val {
			out = append(out, flattenTree(fmt.Sprintf("%s[%d]", prefix, i), item)...)
		}
		return out
	default:
		return []string{prefix + ": " + scalarText(v)}
	}
}

// escapePathKey escapes the characters that separate segments in a path, plus
// the control characters that would break a line-oriented format.
func escapePathKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch r {
		case '\\', '.', '[', ']', ':':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unescapePathChar decodes one escaped character, returning it and how many
// bytes of the input it used.
func unescapePathChar(s string) (rune, int) {
	switch s[0] {
	case 'n':
		return '\n', 1
	case 'r':
		return '\r', 1
	case 't':
		return '\t', 1
	}
	return utf8.DecodeRuneInString(s)
}

// parsePath splits a dotted path into segments, undoing escapePathKey. A path
// is a key, then any number of ".key" and "[index]" steps.
func parsePath(path string) ([]pathSegment, error) {
	var segs []pathSegment

	for i := 0; i < len(path); {
		key, next, err := readPathKey(path, i)
		if err != nil {
			return nil, err
		}
		segs = append(segs, pathSegment{key: key})
		i = next

		for i < len(path) && path[i] == '[' {
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q has an unterminated index", path)
			}
			index, err := strconv.Atoi(path[i+1 : i+end])
			if err != nil || index < 0 {
				return nil, fmt.Errorf("path %q has an invalid index %q", path, path[i+1:i+end])
			}
			segs = append(segs, pathSegment{index: index, isIdx: true})
			i += end + 1
		}

		if i >= len(path) {
			break
		}
		if path[i] != '.' {
			return nil, fmt.Errorf("path %q has an unexpected %q", path, string(path[i]))
		}
		i++
	}

	if len(segs) == 0 {
		return nil, fmt.Errorf("path %q is empty", path)
	}
	return segs, nil
}

// readPathKey reads one key starting at i, stopping at the next unescaped
// separator, and returns the index of that separator.
func readPathKey(path string, i int) (string, int, error) {
	var b strings.Builder

	for i < len(path) {
		switch path[i] {
		case '\\':
			if i+1 >= len(path) {
				return "", 0, fmt.Errorf("path %q ends with an escape character", path)
			}
			r, width := unescapePathChar(path[i+1:])
			b.WriteRune(r)
			i += 1 + width

		case '.', '[':
			if b.Len() == 0 {
				return "", 0, fmt.Errorf("path %q has an empty segment", path)
			}
			return b.String(), i, nil

		case ']':
			return "", 0, fmt.Errorf("path %q has an unmatched %q", path, "]")

		default:
			b.WriteByte(path[i])
			i++
		}
	}

	if b.Len() == 0 {
		return "", 0, fmt.Errorf("path %q has an empty segment", path)
	}
	return b.String(), i, nil
}

// formatPath renders segments the way the fixture wrote them, so an error
// message points at the offending line.
func formatPath(segs []pathSegment) string {
	var b strings.Builder
	for i, seg := range segs {
		switch {
		case seg.isIdx:
			fmt.Fprintf(&b, "[%d]", seg.index)
		case i == 0:
			b.WriteString(escapePathKey(seg.key))
		default:
			b.WriteByte('.')
			b.WriteString(escapePathKey(seg.key))
		}
	}
	return b.String()
}

// buildTree turns flat entries back into nested mappings and sequences, which
// is what lets a text fixture produce the same tree every reader shares.
//
// Malformed input is reported rather than silently dropped: a path that is
// both a value and a container, a duplicate key, or a gap in an array is a
// fixture that lost data, and a lost field in a fixture is a test that stopped
// checking something.
func buildTree(entries []flatEntry) (any, error) {
	return buildNode(entries, 0)
}

// buildNode builds the value at depth, where every entry has at least one
// segment left.
func buildNode(entries []flatEntry, depth int) (any, error) {
	if entries[0].segs[depth].isIdx {
		return buildArray(entries, depth)
	}
	return buildMap(entries, depth)
}

// buildMap assembles a mapping, keeping the file's key order for error
// messages and rejecting anything ambiguous.
func buildMap(entries []flatEntry, depth int) (any, error) {
	groups := make(map[string][]flatEntry)
	leaves := make(map[string]any)
	var order []string

	for _, e := range entries {
		seg := e.segs[depth]
		if seg.isIdx {
			return nil, fmt.Errorf("path %s mixes an array index into a mapping", formatPath(e.segs))
		}
		if len(e.segs) == depth+1 {
			if _, both := groups[seg.key]; both {
				return nil, fmt.Errorf("path %s is both a value and a container", formatPath(e.segs))
			}
			if _, dup := leaves[seg.key]; dup {
				return nil, fmt.Errorf("duplicate path %s", formatPath(e.segs))
			}
			leaves[seg.key] = e.val
			order = append(order, seg.key)
			continue
		}
		if _, both := leaves[seg.key]; both {
			return nil, fmt.Errorf("path %s is both a value and a container", formatPath(e.segs))
		}
		if _, seen := groups[seg.key]; !seen {
			order = append(order, seg.key)
		}
		groups[seg.key] = append(groups[seg.key], e)
	}

	out := make(map[string]any, len(order))
	for _, k := range order {
		if v, ok := leaves[k]; ok {
			out[k] = v
			continue
		}
		v, err := buildNode(groups[k], depth+1)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// buildArray assembles a sequence, requiring the indices to be complete: a
// missing index would silently shift every later element.
func buildArray(entries []flatEntry, depth int) (any, error) {
	groups := make(map[int][]flatEntry)
	leaves := make(map[int]any)
	highest := -1

	for _, e := range entries {
		seg := e.segs[depth]
		if !seg.isIdx {
			return nil, fmt.Errorf("path %s mixes a mapping key into an array", formatPath(e.segs))
		}
		if seg.index > highest {
			highest = seg.index
		}
		if len(e.segs) == depth+1 {
			if _, both := groups[seg.index]; both {
				return nil, fmt.Errorf("path %s is both a value and a container", formatPath(e.segs))
			}
			if _, dup := leaves[seg.index]; dup {
				return nil, fmt.Errorf("duplicate path %s", formatPath(e.segs))
			}
			leaves[seg.index] = e.val
			continue
		}
		if _, both := leaves[seg.index]; both {
			return nil, fmt.Errorf("path %s is both a value and a container", formatPath(e.segs))
		}
		groups[seg.index] = append(groups[seg.index], e)
	}

	out := make([]any, 0, highest+1)
	for i := 0; i <= highest; i++ {
		if v, ok := leaves[i]; ok {
			out = append(out, v)
			continue
		}
		group, ok := groups[i]
		if !ok {
			return nil, fmt.Errorf("array index %d is missing", i)
		}
		v, err := buildNode(group, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Shared decoding.

// decodeTree parses JSON into generic values, keeping numbers as json.Number
// so an int64 amount or a ledger sequence never loses precision on the way
// through a fixture. The same numbers are what the YAML and text writers emit
// for numbers, so the three formats agree on what a number is.
func decodeTree(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// fixtureFromTree converts the generic tree a YAML or text reader produced
// into a Fixture. All three formats share it, so they reject bad input with
// the same wording.
func fixtureFromTree(tree any) (*Fixture, error) {
	obj, ok := tree.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("top level must be a mapping, got %s", describeTree(tree))
	}

	f := &Fixture{}
	var err error
	if f.Format, err = treeString(obj, "format"); err != nil {
		return nil, err
	}
	if f.Type, err = treeString(obj, "type"); err != nil {
		return nil, err
	}
	if f.Description, err = treeString(obj, "description"); err != nil {
		return nil, err
	}
	if f.XDR, err = treeString(obj, "xdr"); err != nil {
		return nil, err
	}
	if f.Captured, err = provenanceFromTree(obj["captured"]); err != nil {
		return nil, err
	}

	if decoded, ok := obj["decoded"]; ok && decoded != nil {
		raw, err := json.Marshal(decoded)
		if err != nil {
			return nil, fmt.Errorf("decoded section cannot be re-encoded: %w", err)
		}
		f.Decoded = raw
	}

	if err := validate(f); err != nil {
		return nil, err
	}
	return f, nil
}

// treeString reads an optional string field.
func treeString(obj map[string]any, key string) (string, error) {
	v, ok := obj[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %s", key, describeTree(v))
	}
	return s, nil
}

// provenanceFromTree reads the captured section, which is absent in a
// hand-written fixture and recorded on everything -focus.update writes.
func provenanceFromTree(v any) (*Provenance, error) {
	if v == nil {
		return nil, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("captured must be a mapping, got %s", describeTree(v))
	}

	p := &Provenance{}
	var err error
	if p.Network, err = treeString(obj, "network"); err != nil {
		return nil, err
	}
	if p.TxHash, err = treeString(obj, "txHash"); err != nil {
		return nil, err
	}
	if p.At, err = treeString(obj, "at"); err != nil {
		return nil, err
	}
	if p.Endpoint, err = treeString(obj, "endpoint"); err != nil {
		return nil, err
	}
	if ledger, ok := obj["ledger"]; ok && ledger != nil {
		if p.Ledger, err = treeUint32(ledger); err != nil {
			return nil, fmt.Errorf("captured.ledger %w", err)
		}
	}
	return p, nil
}

// treeUint32 reads an unsigned integer field.
func treeUint32(v any) (uint32, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("must be a number, got %s", describeTree(v))
	}
	u, err := strconv.ParseUint(string(n), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("must be a 32-bit unsigned integer, got %s", n)
	}
	return uint32(u), nil
}

// describeTree names a generic value for an error message.
func describeTree(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "a mapping"
	case []any:
		return "a sequence"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case json.Number:
		return "a number"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// Scalars.

// scalarText renders a scalar the way both the YAML and text writers write it:
// bare when that is unambiguous, and as a JSON string literal otherwise.
//
// Quoting is what makes a round trip lossless. "testnet" is written bare and
// read back as the string it is; "12345" would be read back as a number, so a
// string that looks like one is quoted. The literal is JSON's, which is also a
// valid YAML double-quoted scalar, so one reader decodes both.
func scalarText(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(val)
	case string:
		if plainSafe(val) {
			return val
		}
		return quoteText(val)
	case json.Number:
		if isNumber(string(val)) {
			return string(val)
		}
		return quoteText(string(val))
	default:
		// Values come from JSON, so a scalar is one of the cases above; the
		// fallback keeps a hand-built value from producing an empty line.
		return quoteText(fmt.Sprintf("%v", val))
	}
}

// yamlUnsafeChars are the characters that make a bare scalar ambiguous in
// YAML: indicators, the comment marker, and the quoting characters.
const yamlUnsafeChars = ":#,{}[]&*!|>'\"%@`\\"

// yamlReservedWords are the bare scalars a YAML reader resolves to a boolean
// or a null. A string that equals one has to be quoted to stay a string.
var yamlReservedWords = map[string]bool{
	"true": true, "false": true, "null": true, "~": true,
	"yes": true, "no": true, "on": true, "off": true,
	"y": true, "n": true, ".inf": true, ".nan": true,
}

// plainSafe reports whether a string can be written bare, in either format.
func plainSafe(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	if yamlReservedWords[strings.ToLower(s)] {
		return false
	}
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|',
		'>', '\'', '"', '%', '@', '`', '\\':
		return false
	}
	if strings.ContainsAny(s, yamlUnsafeChars) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	// A number would be read back as a number and a JSON document as its JSON
	// value, so anything that parses as either is quoted to survive as a
	// string.
	return !json.Valid([]byte(s)) && !isNumber(s)
}

// isNumber reports whether s is a number in a notation a YAML reader would
// resolve to one, including the leading-zero, hex and float forms JSON
// rejects but YAML accepts.
func isNumber(s string) bool {
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseInt(s, 0, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseUint(s, 0, 64); err == nil {
		return true
	}
	return false
}

// quoteText renders s as a JSON string literal.
func quoteText(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// Marshalling a string cannot fail; this keeps the function honest
		// rather than silently dropping the value.
		return strconv.Quote(s)
	}
	return string(b)
}

// parseScalarToken reads one scalar as the writers emit it: a JSON literal
// when it is quoted or a number or a boolean or null, and a bare string
// otherwise.
func parseScalarToken(text string) (any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}

	if strings.HasPrefix(text, "'") {
		return parseSingleQuoted(text)
	}
	if json.Valid([]byte(text)) {
		return decodeTree([]byte(text))
	}
	if strings.HasPrefix(text, "\"") {
		return nil, fmt.Errorf("malformed double-quoted scalar %q", text)
	}

	switch strings.ToLower(text) {
	case "null", "~":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}

	for _, r := range text {
		if r < 0x20 {
			return nil, fmt.Errorf("control character in value %q", text)
		}
	}
	return text, nil
}
