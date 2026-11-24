package golden

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
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
