package golden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// tomlSerializer implements Serializer for TOML fixtures (.golden.toml).
type tomlSerializer struct{}

// Format implements Serializer.
func (tomlSerializer) Format() Format { return TOML }

// Marshal encodes a fixture into deterministic, human-readable TOML.
func (tomlSerializer) Marshal(f *Fixture) ([]byte, error) {
	var buf bytes.Buffer

	// Top-level fixture identity keys.
	writeTOMLKeyValue(&buf, "format", f.Format)
	writeTOMLKeyValue(&buf, "type", f.Type)
	if f.Description != "" {
		writeTOMLKeyValue(&buf, "description", f.Description)
	}
	writeTOMLKeyValue(&buf, "xdr", f.XDR)

	// Captured provenance table.
	if p := f.Captured; p != nil {
		buf.WriteString("\n[captured]\n")
		var keys []string
		m := make(map[string]any)
		m["network"] = p.Network
		keys = append(keys, "network")
		if p.NetworkPassphrase != "" {
			m["networkPassphrase"] = p.NetworkPassphrase
			keys = append(keys, "networkPassphrase")
		}
		if p.Ledger != 0 {
			m["ledger"] = number(p.Ledger)
			keys = append(keys, "ledger")
		}
		if p.TxHash != "" {
			m["txHash"] = p.TxHash
			keys = append(keys, "txHash")
		}
		m["at"] = p.At
		keys = append(keys, "at")
		if p.Endpoint != "" {
			m["endpoint"] = p.Endpoint
			keys = append(keys, "endpoint")
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeTOMLKeyValue(&buf, k, m[k])
		}
	}

	// Decoded fixture content.
	if len(f.Decoded) > 0 {
		tree, err := decodeTree(f.Decoded)
		if err != nil {
			return nil, fmt.Errorf("reading the decoded section: %w", err)
		}
		if m, ok := tree.(map[string]any); ok {
			buf.WriteString("\n[decoded]\n")
			writeTOMLTableContent(&buf, "decoded", m)
		} else {
			writeTOMLKeyValue(&buf, "decoded", tree)
		}
	}

	return NormalizeWhitespace(buf.Bytes()), nil
}

// writeTOMLTableContent recursively serializes a map as TOML tables.
// Direct key-values are emitted first, followed by sub-tables and arrays-of-tables in sorted key order.
func writeTOMLTableContent(buf *bytes.Buffer, prefix string, m map[string]any) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var subTables []string
	var arrayTables []string

	// Pass 1: Simple leaf keys and inline values.
	for _, k := range keys {
		v := m[k]
		if subMap, ok := v.(map[string]any); ok && len(subMap) > 0 {
			subTables = append(subTables, k)
			continue
		}
		if arr, ok := v.([]any); ok && isArrayOfTables(arr) {
			arrayTables = append(arrayTables, k)
			continue
		}
		writeTOMLKeyValue(buf, k, v)
	}

	// Pass 2: Nested sub-tables.
	for _, k := range subTables {
		subMap := m[k].(map[string]any)
		subPrefix := formatTOMLTablePath(prefix, k)
		buf.WriteString("\n[" + subPrefix + "]\n")
		writeTOMLTableContent(buf, subPrefix, subMap)
	}

	// Pass 3: Arrays of tables.
	for _, k := range arrayTables {
		arr := m[k].([]any)
		tablePath := formatTOMLTablePath(prefix, k)
		for _, item := range arr {
			subMap := item.(map[string]any)
			buf.WriteString("\n[[" + tablePath + "]]\n")
			writeTOMLTableContent(buf, tablePath, subMap)
		}
	}
}

// isArrayOfTables reports whether arr contains only non-empty tables.
func isArrayOfTables(arr []any) bool {
	if len(arr) == 0 {
		return false
	}
	for _, item := range arr {
		subMap, ok := item.(map[string]any)
		if !ok || len(subMap) == 0 {
			return false
		}
	}
	return true
}

// formatTOMLTablePath joins table prefix and key, quoting the key if necessary.
func formatTOMLTablePath(prefix, key string) string {
	quoted := tomlKey(key)
	if prefix == "" {
		return quoted
	}
	return prefix + "." + quoted
}

// tomlKey quotes a table key if it contains characters outside [A-Za-z0-9_-].
func tomlKey(key string) string {
	if isBareTOMLKey(key) {
		return key
	}
	return quoteText(key)
}

// isBareTOMLKey reports whether key contains only alphanumeric, hyphen, or underscore characters.
func isBareTOMLKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// writeTOMLKeyValue writes "key = value\n" to buf.
func writeTOMLKeyValue(buf *bytes.Buffer, key string, v any) {
	buf.WriteString(tomlKey(key))
	buf.WriteString(" = ")
	buf.WriteString(tomlValueText(v))
	buf.WriteByte('\n')
}

// tomlValueText renders a generic value as TOML value syntax.
func tomlValueText(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(val)
	case string:
		return quoteText(val)
	case json.Number:
		return string(val)
	case map[string]any:
		if len(val) == 0 {
			return "{}"
		}
		var b strings.Builder
		b.WriteString("{ ")
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tomlKey(k))
			b.WriteString(" = ")
			b.WriteString(tomlValueText(val[k]))
		}
		b.WriteString(" }")
		return b.String()
	case []any:
		if len(val) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[")
		for i, item := range val {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tomlValueText(item))
		}
		b.WriteString("]")
		return b.String()
	default:
		return quoteText(fmt.Sprintf("%v", val))
	}
}

// Unmarshal decodes TOML fixture bytes into a Fixture struct.
func (tomlSerializer) Unmarshal(data []byte) (*Fixture, error) {
	normalized := NormalizeWhitespace(data)
	if len(bytes.TrimSpace(normalized)) == 0 {
		return nil, fmt.Errorf("fixture is empty")
	}

	p := &tomlParser{input: string(normalized)}
	tree, err := p.parse()
	if err != nil {
		return nil, err
	}
	return fixtureFromTree(tree)
}

// tomlParser parses a subset of TOML into a map[string]any tree.
type tomlParser struct {
	input string
	pos   int
	line  int
}

func (p *tomlParser) parse() (map[string]any, error) {
	root := make(map[string]any)
	currentMap := root

	p.line = 1
	for p.pos < len(p.input) {
		p.skipWhitespaceAndComments()
		if p.pos >= len(p.input) {
			break
		}

		ch := p.input[p.pos]
		if ch == '\n' {
			p.pos++
			p.line++
			continue
		}
		if ch == '\r' {
			p.pos++
			continue
		}

		// Table header: [table.name] or [[array.table]]
		if ch == '[' {
			if p.pos+1 < len(p.input) && p.input[p.pos+1] == '[' {
				// Array of tables: [[path]]
				p.pos += 2
				path, err := p.parseTablePath(']')
				if err != nil {
					return nil, err
				}
				if p.pos >= len(p.input) || p.input[p.pos] != ']' {
					return nil, fmt.Errorf("line %d: expected closing ']]'", p.line)
				}
				p.pos++ // consume second ']'
				currentMap, err = p.navigateArrayOfTables(root, path)
				if err != nil {
					return nil, err
				}
				continue
			}

			// Standard table: [path]
			p.pos++
			path, err := p.parseTablePath(']')
			if err != nil {
				return nil, err
			}
			currentMap, err = p.navigateTable(root, path)
			if err != nil {
				return nil, err
			}
			continue
		}

		// Key-value pair: key = value
		key, err := p.parseKey()
		if err != nil {
			return nil, err
		}
		p.skipWhitespaceInline()
		if p.pos >= len(p.input) || p.input[p.pos] != '=' {
			return nil, fmt.Errorf("line %d: expected '=' after key %q", p.line, key)
		}
		p.pos++ // consume '='
		p.skipWhitespaceInline()

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		currentMap[key] = val
	}

	return root, nil
}

func (p *tomlParser) skipWhitespaceAndComments() {
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == ' ' || ch == '\t' {
			p.pos++
			continue
		}
		if ch == '#' {
			// Skip comment to end of line
			for p.pos < len(p.input) && p.input[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		break
	}
}

func (p *tomlParser) skipWhitespaceInline() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

func (p *tomlParser) parseTablePath(terminator byte) ([]string, error) {
	var parts []string
	for {
		p.skipWhitespaceInline()
		key, err := p.parseKey()
		if err != nil {
			return nil, err
		}
		parts = append(parts, key)
		p.skipWhitespaceInline()

		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("line %d: unterminated table header", p.line)
		}
		if p.input[p.pos] == '.' {
			p.pos++
			continue
		}
		if p.input[p.pos] == terminator {
			p.pos++
			return parts, nil
		}
		return nil, fmt.Errorf("line %d: unexpected character %q in table header", p.line, p.input[p.pos])
	}
}

func (p *tomlParser) navigateTable(root map[string]any, path []string) (map[string]any, error) {
	curr := root
	for _, seg := range path {
		if existing, ok := curr[seg]; ok {
			sub, ok := existing.(map[string]any)
			if !ok {
				// Might be inside an array of tables
				if arr, ok := existing.([]any); ok && len(arr) > 0 {
					if lastTable, ok := arr[len(arr)-1].(map[string]any); ok {
						curr = lastTable
						continue
					}
				}
				return nil, fmt.Errorf("path %s conflicts with non-table", seg)
			}
			curr = sub
		} else {
			sub := make(map[string]any)
			curr[seg] = sub
			curr = sub
		}
	}
	return curr, nil
}

func (p *tomlParser) navigateArrayOfTables(root map[string]any, path []string) (map[string]any, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty array table path")
	}
	curr := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		if existing, ok := curr[seg]; ok {
			sub, ok := existing.(map[string]any)
			if !ok {
				if arr, ok := existing.([]any); ok && len(arr) > 0 {
					if lastTable, ok := arr[len(arr)-1].(map[string]any); ok {
						curr = lastTable
						continue
					}
				}
				return nil, fmt.Errorf("path %s conflicts with non-table", seg)
			}
			curr = sub
		} else {
			sub := make(map[string]any)
			curr[seg] = sub
			curr = sub
		}
	}

	target := path[len(path)-1]
	newTable := make(map[string]any)
	if existing, ok := curr[target]; ok {
		arr, ok := existing.([]any)
		if !ok {
			return nil, fmt.Errorf("path %s is not an array of tables", target)
		}
		curr[target] = append(arr, newTable)
	} else {
		curr[target] = []any{newTable}
	}
	return newTable, nil
}

func (p *tomlParser) parseKey() (string, error) {
	p.skipWhitespaceInline()
	if p.pos >= len(p.input) {
		return "", fmt.Errorf("line %d: unexpected EOF reading key", p.line)
	}

	ch := p.input[p.pos]
	if ch == '"' || ch == '\'' {
		return p.parseQuotedString(ch)
	}

	// Bare key: [A-Za-z0-9_-]+
	start := p.pos
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			p.pos++
		} else {
			break
		}
	}
	if start == p.pos {
		return "", fmt.Errorf("line %d: expected key, got %q", p.line, p.input[p.pos])
	}
	return p.input[start:p.pos], nil
}

func (p *tomlParser) parseValue() (any, error) {
	p.skipWhitespaceInline()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("line %d: unexpected EOF reading value", p.line)
	}

	// Multiline basic string """ or literal '''
	if strings.HasPrefix(p.input[p.pos:], `"""`) {
		return p.parseMultilineString(`"""`)
	}
	if strings.HasPrefix(p.input[p.pos:], `'''`) {
		return p.parseMultilineLiteralString(`'''`)
	}

	ch := p.input[p.pos]
	switch ch {
	case '"', '\'':
		return p.parseQuotedString(ch)
	case '[':
		return p.parseInlineArray()
	case '{':
		return p.parseInlineTable()
	}

	// Literal / identifier / number
	start := p.pos
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		if c == '\n' || c == '\r' || c == ',' || c == ']' || c == '}' || c == '#' {
			break
		}
		p.pos++
	}
	raw := strings.TrimSpace(p.input[start:p.pos])
	if raw == "" {
		return nil, fmt.Errorf("line %d: expected value", p.line)
	}

	switch strings.ToLower(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}

	// Number validation
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return json.Number(raw), nil
	}
	if _, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return json.Number(raw), nil
	}
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		return json.Number(raw), nil
	}

	return raw, nil
}

func (p *tomlParser) parseQuotedString(quote byte) (string, error) {
	p.pos++ // consume opening quote
	var b strings.Builder
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == quote {
			p.pos++ // consume closing quote
			return b.String(), nil
		}
		if quote == '"' && ch == '\\' {
			p.pos++
			if p.pos >= len(p.input) {
				return "", fmt.Errorf("line %d: unterminated escape in string", p.line)
			}
			esc := p.input[p.pos]
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'u':
				if p.pos+4 >= len(p.input) {
					return "", fmt.Errorf("line %d: short \\u escape", p.line)
				}
				r, err := strconv.ParseUint(p.input[p.pos+1:p.pos+5], 16, 32)
				if err != nil {
					return "", fmt.Errorf("line %d: invalid \\u escape", p.line)
				}
				b.WriteRune(rune(r))
				p.pos += 4
			default:
				b.WriteByte(esc)
			}
			p.pos++
			continue
		}
		if ch == '\n' {
			return "", fmt.Errorf("line %d: newline in single-line string", p.line)
		}
		b.WriteByte(ch)
		p.pos++
	}
	return "", fmt.Errorf("line %d: unterminated string literal", p.line)
}

func (p *tomlParser) parseMultilineString(delim string) (string, error) {
	p.pos += len(delim)
	// If a newline immediately follows the opening delimiter, it is trimmed according to TOML spec.
	if p.pos < len(p.input) && p.input[p.pos] == '\r' {
		p.pos++
	}
	if p.pos < len(p.input) && p.input[p.pos] == '\n' {
		p.pos++
		p.line++
	}

	var b strings.Builder
	for p.pos < len(p.input) {
		if strings.HasPrefix(p.input[p.pos:], delim) {
			p.pos += len(delim)
			return b.String(), nil
		}
		ch := p.input[p.pos]
		if ch == '\n' {
			p.line++
		}
		if ch == '\\' && p.pos+1 < len(p.input) {
			p.pos++
			esc := p.input[p.pos]
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			default:
				b.WriteByte(esc)
			}
			p.pos++
			continue
		}
		b.WriteByte(ch)
		p.pos++
	}
	return "", fmt.Errorf("line %d: unterminated multiline string", p.line)
}

func (p *tomlParser) parseMultilineLiteralString(delim string) (string, error) {
	p.pos += len(delim)
	if p.pos < len(p.input) && p.input[p.pos] == '\r' {
		p.pos++
	}
	if p.pos < len(p.input) && p.input[p.pos] == '\n' {
		p.pos++
		p.line++
	}

	idx := strings.Index(p.input[p.pos:], delim)
	if idx < 0 {
		return "", fmt.Errorf("line %d: unterminated multiline literal string", p.line)
	}
	content := p.input[p.pos : p.pos+idx]
	p.line += strings.Count(content, "\n")
	p.pos += idx + len(delim)
	return content, nil
}

func (p *tomlParser) parseInlineArray() ([]any, error) {
	p.pos++ // consume '['
	out := []any{}
	for {
		p.skipWhitespaceAndNewlines()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("line %d: unterminated inline array", p.line)
		}
		if p.input[p.pos] == ']' {
			p.pos++
			return out, nil
		}

		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out = append(out, v)

		p.skipWhitespaceAndNewlines()
		if p.pos < len(p.input) && p.input[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.pos < len(p.input) && p.input[p.pos] == ']' {
			p.pos++
			return out, nil
		}
		return nil, fmt.Errorf("line %d: expected ',' or ']' in array", p.line)
	}
}

func (p *tomlParser) parseInlineTable() (map[string]any, error) {
	p.pos++ // consume '{'
	out := make(map[string]any)
	for {
		p.skipWhitespaceInline()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("line %d: unterminated inline table", p.line)
		}
		if p.input[p.pos] == '}' {
			p.pos++
			return out, nil
		}

		key, err := p.parseKey()
		if err != nil {
			return nil, err
		}
		p.skipWhitespaceInline()
		if p.pos >= len(p.input) || p.input[p.pos] != '=' {
			return nil, fmt.Errorf("line %d: expected '=' in inline table", p.line)
		}
		p.pos++ // consume '='
		p.skipWhitespaceInline()

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out[key] = val

		p.skipWhitespaceInline()
		if p.pos < len(p.input) && p.input[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.pos < len(p.input) && p.input[p.pos] == '}' {
			p.pos++
			return out, nil
		}
		return nil, fmt.Errorf("line %d: expected ',' or '}' in inline table", p.line)
	}
}

func (p *tomlParser) skipWhitespaceAndNewlines() {
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == ' ' || ch == '\t' || ch == '\r' {
			p.pos++
			continue
		}
		if ch == '\n' {
			p.pos++
			p.line++
			continue
		}
		if ch == '#' {
			for p.pos < len(p.input) && p.input[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		break
	}
}
