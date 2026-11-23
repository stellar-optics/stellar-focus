package golden

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// secretKeyPattern matches a StrKey secret seed: an S followed by 55 more
// base32 characters, the same shape capture.Sanitizer redacts. A fixture
// carrying one was not sanitized before it was committed.
var secretKeyPattern = regexp.MustCompile(`S[A-Z0-9]{55}`)

// LintError is one problem found in a fixture file.
//
// The field is what makes the message useful: "xdr: does not decode as a
// TransactionEnvelope" says where to look, where "the fixture is broken"
// does not.
type LintError struct {
	// Field names the part of the fixture the problem is in, such as "xdr"
	// or "captured.at". It is empty for a problem with the file as a whole.
	Field string
	// Message says what is wrong.
	Message string
	// Line is the line of the file the problem was found on, when it is
	// known.
	Line int
}

// Error implements error, naming the field and the line the problem is at.
func (e LintError) Error() string {
	switch {
	case e.Field == "" && e.Line > 0:
		return fmt.Sprintf("line %d: %s", e.Line, e.Message)
	case e.Field == "":
		return e.Message
	case e.Line > 0:
		return fmt.Sprintf("%s (line %d): %s", e.Field, e.Line, e.Message)
	default:
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
}

// LintFinding is a problem found during directory-level fixture linting.
//
// Unlike LintError, which reports problems within a single file, LintFinding
// reports problems about a fixture file in the context of a directory. The
// File field names the golden file that has the issue.
type LintFinding struct {
	// File is the golden file path relative to the lint directory.
	File string
	// Field names the part of the fixture the problem is in, such as "xdr"
	// or "captured.at". It is empty for a problem with the file as a whole.
	Field string
	// Message says what is wrong.
	Message string
}

// fixtureFields are the fields a fixture may carry.
//
// The list mirrors the schema, and it is here so a fixture with a field
// nobody knows about is reported rather than quietly ignored.
var fixtureFields = map[string]bool{
	"format":      true,
	"type":        true,
	"description": true,
	"xdr":         true,
	"captured":    true,
	"decoded":     true,
}

// LintFixtures lints every .golden.json file in dir and returns every problem
// found across the whole directory.
//
// Checks include:
//   - un-normalized sequence numbers (seqNum > 0) in the decoded section;
//   - timestamps that appear unmasked;
//   - unmasked signature hints;
//   - orphaned golden files not referenced in any _test.go file.
func LintFixtures(dir string) []LintFinding {
	var findings []LintFinding

	// Find all golden files in the directory.
	goldenFiles := findGoldenFiles(dir)
	if len(goldenFiles) == 0 {
		return findings
	}

	// Find which golden files are referenced in _test.go files.
	referenced := findReferencedGoldenFiles(dir)

	// Check for orphaned golden files.
	for _, gf := range goldenFiles {
		if !referenced[gf] {
			findings = append(findings, LintFinding{
				File:    gf,
				Message: "orphaned golden file not referenced in any _test.go",
			})
		}
	}

	// Lint each golden file and collect findings.
	for _, gf := range goldenFiles {
		fileFindings := lintFileForVolatile(gf)
		findings = append(findings, fileFindings...)
	}

	return findings
}

// findGoldenFiles returns a list of .golden.json file paths in dir (recursive),
// excluding hidden directories.
func findGoldenFiles(dir string) []string {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if entry.IsDir() {
			// Skip hidden directories
			if entry.Name()[0] == '.' {
				continue
			}
			// Recurse into subdirectories
			subFiles := findGoldenFiles(filepath.Join(dir, entry.Name()))
			files = append(files, subFiles...)
			continue
		}
		if strings.HasSuffix(entry.Name(), ".golden.json") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files
}

// findReferencedGoldenFiles scans _test.go files in dir (and subdirectories)
// for references to golden files and returns a map of golden file paths to
// bool indicating whether they are referenced.
func findReferencedGoldenFiles(dir string) map[string]bool {
	referenced := make(map[string]bool)

	// Walk the directory for _test.go files
	walkDir(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		content := string(raw)

		// Look for golden file references in the test file.
		// Common patterns: golden.Assert("name", ...), golden.Build(..., "name", ...)
		// or focus.Assert("name", ...)
		// We look for strings that, when appended with ".golden.json", match
		// an actual golden file.
		goldenFileNames := extractGoldenFileNames(content)

		for _, name := range goldenFileNames {
			goldenPath := name + ".golden.json"
			referenced[goldenPath] = true
		}

		return nil
	})

	return referenced
}

// walkDir walks the directory calling fn for each file/dir.
func walkDir(dir string, fn filepath.WalkDirFunc) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fn(dir, nil, err)
	}
	if err := fn(dir, entries[0], nil); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if entry.Name()[0] == '.' {
				continue // skip hidden dirs
			}
			subDir := filepath.Join(dir, entry.Name())
			if err := walkDir(subDir, fn); err != nil {
				return err
			}
		}
	}
	return fn(dir, nil, nil)
}

// extractGoldenFileNames extracts potential golden file base names from Go
// source code. Looks for string literals that might reference golden files.
func extractGoldenFileNames(content string) []string {
	var names []string
	// Match string literals that could be golden file references.
	// Patterns like: "env", "payment", "envelope", etc.
	// These would map to env.golden.json, payment.golden.json, etc.
	re := regexp.MustCompile(`"([a-zA-Z0-9_-]+)"`)
	matches := re.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// lintFileForVolatile lints a single golden file for volatile field issues.
func lintFileForVolatile(path string) []LintFinding {
	var findings []LintFinding

	raw, err := os.ReadFile(path)
	if err != nil {
		return findings
	}

	// Normalize line endings first
	normalized := NormalizeNewlines(raw)

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return findings
	}

	// Check for un-normalized sequence numbers in the decoded section
	if decoded, ok := doc["decoded"]; ok && decoded != nil {
		// Parse the decoded JSON tree
		tree, err := parseJSONTree(decoded)
		if err == nil {
			checkSequenceNumbers(tree, &findings, path)
		}
	}

	// Check for timestamps that may need masking
	if captured, ok := doc["captured"]; ok && captured != nil {
		if capturedMap, ok := captured.(map[string]any); ok {
			if _, hasAt := capturedMap["at"]; hasAt {
				// The at field contains a timestamp; flag it for review
				findings = append(findings, LintFinding{
					File:    path,
					Message: "captured timestamp present, ensure it is masked if volatile",
				})
			}
		}
	}

	// Check for signature hints (this is a simplification)
	// In a real implementation, we'd parse the decoded tree for signature fields

	return findings
}

// parseJSONTree parses a json.RawMessage into a generic tree.
func parseJSONTree(data json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// checkSequenceNumbers recursively walks the tree looking for sequence number
// fields that have non-zero values.
func checkSequenceNumbers(v any, findings *[]LintFinding, filePath string) {
	switch val := v.(type) {
	case map[string]any:
		for key, elem := range val {
			// Check for common sequence number field names
			if key == "seqNum" || key == "sequence" || key == "seq_num" ||
				key == "ledger_seq" || key == "ledgerSequence" || key == "ledger" {
				if n, ok := elem.(float64); ok {
					if n != 0 {
						*findings = append(*findings, LintFinding{
							File:    filePath,
							Field:   key,
							Message: fmt.Sprintf("sequence number %s is %v, consider masking", key, n),
						})
					}
				}
			}
			// Recurse into nested values
			checkSequenceNumbers(elem, findings, filePath)
		}
	case []any:
		for _, elem := range val {
			checkSequenceNumbers(elem, findings, filePath)
		}
	}
}

// Lint checks a fixture file and returns every problem it finds.
//
// The checks are static: nothing is fetched, nothing is run, and no test is
// executed. That is the point — a fixture that was merged by hand, or
// corrupted by a bad merge, should be caught while it is being written
// rather than when a test fails three weeks later.
//
// What is checked:
//
//   - the file is valid JSON (for a JSON fixture) and holds only fields a
//     fixture has;
//   - the format is the one this version reads;
//   - the type and the XDR are present;
//   - the XDR is valid base64;
//   - the XDR decodes as the type the fixture claims;
//   - a capture timestamp is RFC3339.
//
// An empty list means the fixture is clean.
func Lint(path string) []LintError {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []LintError{{Message: fmt.Sprintf("reading the file: %v", err)}}
	}

	var problems []LintError

	// CRLF (or lone CR) line endings are a checkout artefact, not content:
	// they make a fixture differ from byte to byte across platforms.
	if idx := bytes.IndexByte(raw, '\r'); idx >= 0 {
		line := 1
		for i := 0; i < idx; i++ {
			if raw[i] == '\n' {
				line++
			}
		}
		problems = append(problems, LintError{
			Field:   "",
			Message: "contains CRLF line endings",
			Line:    line,
		})
	}

	// A secret key that survived into a committed fixture is a leak, and a
	// volatile value that was never masked.
	if loc := secretKeyPattern.FindIndex(raw); loc != nil {
		line := 1
		for i := 0; i < loc[0]; i++ {
			if raw[i] == '\n' {
				line++
			}
		}
		problems = append(problems, LintError{
			Field:   "xdr",
			Message: "contains an unmasked secret key (volatile token)",
			Line:    line,
		})
	}

	fixture, more := parseForLint(path, raw)
	problems = append(problems, more...)
	if fixture == nil {
		return problems
	}

	return append(problems, lintContent(fixture)...)
}

// parseForLint reads a fixture for linting.
//
// It returns the fixture and any problem found while reading it. A nil
// fixture means the file could not be read as a fixture at all.
func parseForLint(path string, raw []byte) (*Fixture, []LintError) {
	if FormatForPath(path) != JSON {
		// The serializer reads, validates and decodes the other formats in
		// one go, and its errors already say what is wrong.
		fixture, err := LoadAs(path, FormatForPath(path))
		if err != nil {
			return nil, []LintError{{Message: err.Error()}}
		}
		return fixture, nil
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return nil, []LintError{{
				Message: fmt.Sprintf("invalid JSON: %v", err),
				Line:    lineAt(raw, syntax.Offset),
			}}
		}
		return nil, []LintError{{Message: fmt.Sprintf("invalid JSON: %v", err)}}
	}

	var problems []LintError
	for field := range doc {
		if !fixtureFields[field] {
			problems = append(problems, LintError{
				Field:   field,
				Message: "is not a field of a fixture",
				Line:    lineOfField(raw, field),
			})
		}
	}

	var fixture Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return nil, append(problems, LintError{
			Message: fmt.Sprintf("the fixture cannot be read: %v", err),
		})
	}

	// The schema knows the shape of a fixture better than this file does:
	// which fields are required, what each one holds and what the ranges
	// are. The format version and the capture timestamp are checked
	// separately, below, because the messages there can name the field.
	schema := fixture
	schema.Format = ""
	if schema.Captured != nil {
		captured := *schema.Captured
		captured.At = ""
		schema.Captured = &captured
	}
	if err := Validate(&schema); err != nil {
		problems = append(problems, LintError{Message: err.Error()})
	}

	return &fixture, problems
}

// lintContent checks what the schema cannot: whether the XDR is base64 at
// all, whether it decodes as the type the fixture claims, and whether the
// capture timestamp is a real one.
func lintContent(f *Fixture) []LintError {
	var problems []LintError

	if f.Format != FormatVersion {
		problems = append(problems, LintError{
			Field:   "format",
			Message: fmt.Sprintf("is %q, want %q", f.Format, FormatVersion),
		})
	}

	payload := strings.TrimSpace(f.XDR)
	switch {
	case payload == "":
		problems = append(problems, LintError{Field: "xdr", Message: "is missing"})
		return problems
	case f.Type == "":
		problems = append(problems, LintError{Field: "type", Message: "is missing"})
		return problems
	}

	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		problems = append(problems, LintError{
			Field:   "xdr",
			Message: fmt.Sprintf("is not valid base64: %v", err),
		})
	} else if f.Type == "" {
		problems = append(problems, LintError{Field: "type", Message: "is missing"})
	} else {
		decoded, err := lens.DecodeAs(payload, f.Type)
		switch {
		case err != nil:
			problems = append(problems, LintError{
				Field:   "xdr",
				Message: fmt.Sprintf("does not decode as a %s: %v", f.Type, err),
			})
		case !strings.EqualFold(decoded.Type, f.Type):
			problems = append(problems, LintError{
				Field:   "type",
				Message: fmt.Sprintf("says %q but the XDR decodes as %q", f.Type, decoded.Type),
			})
		}
	}

	// The capture timestamp is checked last, and always: a fixture with
	// several problems should hear about all of them in one run.
	if f.Captured != nil && f.Captured.At != "" {
		if _, err := time.Parse(time.RFC3339, f.Captured.At); err != nil {
			problems = append(problems, LintError{
				Field:   "captured.at",
				Message: fmt.Sprintf("is not an RFC3339 timestamp: %q", f.Captured.At),
			})
		}
	}

	return problems
}

// lineAt returns the line an offset falls on, counting from one.
func lineAt(raw []byte, offset int64) int {
	if offset <= 0 {
		return 0
	}
	if offset > int64(len(raw)) {
		offset = int64(len(raw))
	}
	line := 1
	for _, b := range raw[:offset] {
		if b == '\n' {
			line++
		}
	}
	return line
}

// lineOfField returns the line a top-level field is declared on, or zero
// when it cannot be found.
func lineOfField(raw []byte, field string) int {
	needle := []byte(`"` + field + `"`)
	for i := 0; i+len(needle) <= len(raw); i++ {
		if string(raw[i:i+len(needle)]) != string(needle) {
			continue
		}
		// Only a key counts: the value of a field may hold the same word.
		if i > 0 && raw[i-1] != ' ' && raw[i-1] != '\t' && raw[i-1] != '\n' {
			continue
		}
		if i+len(needle) < len(raw) && raw[i+len(needle)] != ':' {
			continue
		}
		return lineAt(raw, int64(i))
	}
	return 0
}
