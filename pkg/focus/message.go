package focus

import (
	"bytes"
	"encoding"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"
)

// The failure message is this library's product. A developer reading a red
// test has three questions, and the layout answers them in order:
//
//	1. What differs?      the structural diff, field by field
//	2. What was skipped?  the ignore rules that fired
//	3. Where do I look?   truncated base64, to paste into `lens decode`
//
// Everything here exists to keep those three answers short and in that order.

// maxInlineXDR is how much base64 is shown before truncation. Enough to
// recognise a payload, short enough never to become a wall.
const maxInlineXDR = 24

// diffMessage renders the failure text for a structural mismatch.
func diffMessage(assertion string, want, got *lens.Value, changes []lens.Change, applied []string, c *config) string {
	var b strings.Builder

	// Headline: the count is the first thing that tells you how bad it is.
	fmt.Fprintf(&b, "%s: %s differ — %s\n",
		assertion, describeType(want, got), pluralChanges(len(changes)))

	if len(applied) > 0 {
		fmt.Fprintf(&b, "(ignored: %s)\n", strings.Join(applied, ", "))
	}

	b.WriteString("\n")
	b.WriteString(renderChanges(want, got, changes, c))
	b.WriteString("\n")

	// The raw payloads come last, truncated, so they are available for
	// copy-paste into lens without dominating the message.
	fmt.Fprintf(&b, "want  %s\n", describePayload(want))
	fmt.Fprintf(&b, "got   %s\n", describePayload(got))

	return b.String()
}

// renderChanges prints the diff using lens's own formatter, so a failing test
// shows exactly what `lens diff` shows for the same pair.
func renderChanges(want, got *lens.Value, changes []lens.Change, c *config) string {
	shown := changes
	var elided int
	if c.maxChanges > 0 && len(changes) > c.maxChanges {
		shown = changes[:c.maxChanges]
		elided = len(changes) - c.maxChanges
	}

	result := &lens.DiffResult{
		LeftType:  want.Type,
		RightType: got.Type,
		Changes:   shown,
	}

	var buf bytes.Buffer
	f := lensformat.NewDiffFormatter(lensformat.WithDiffPalette(c.palette()))
	if err := f.Format(&buf, result); err != nil {
		// Falling back keeps a formatter bug from hiding the actual failure.
		var fallback strings.Builder
		for _, ch := range shown {
			fmt.Fprintf(&fallback, "  %s\n", ch.String())
		}
		return fallback.String()
	}

	// lens appends its own one-line summary ("1 changed"), which duplicates
	// the count already in this message's headline.
	rendered := strings.TrimRight(buf.String(), "\n")
	if idx := strings.LastIndex(rendered, "\n\n"); idx > 0 {
		rendered = rendered[:idx]
	}

	out := indent(rendered, "  ")
	if elided > 0 {
		out += fmt.Sprintf("\n  … and %d more difference(s); raise the limit with focus.WithMaxChanges", elided)
	}
	return out + "\n"
}

// mismatchMessage renders a simple want/got failure for scalar assertions.
func mismatchMessage(assertion, field string, want, got any) string {
	return fmt.Sprintf("%s: %s is %s, want %s", assertion, field, render(got), render(want))
}

// render formats a scalar for a failure message, quoting strings so an empty
// value or stray whitespace is visible.
func render(v any) string {
	switch val := v.(type) {
	case nil:
		return "(none)"
	case string:
		if val == "" {
			return `"" (empty)`
		}
		return fmt.Sprintf("%q", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// describeType names what is being compared, preferring the shared type name
// and reporting a mismatch when the two sides are different types.
func describeType(want, got *lens.Value) string {
	if want.Type == got.Type {
		return humanType(want.Type)
	}
	return fmt.Sprintf("values (%s vs %s)", want.Type, got.Type)
}

// humanType turns an XDR type name into something that reads in a sentence.
func humanType(t string) string {
	switch t {
	case "TransactionEnvelope":
		return "envelopes"
	case "TransactionResult":
		return "results"
	case "TransactionMeta":
		return "transaction metas"
	default:
		return t + " values"
	}
}

func pluralChanges(n int) string {
	if n == 1 {
		return "1 difference"
	}
	return fmt.Sprintf("%d differences", n)
}

// describePayload renders a truncated base64 form plus its size, so the
// message stays short while remaining actionable.
func describePayload(v *lens.Value) string {
	b64, err := payloadBase64(v)
	if err != nil || b64 == "" {
		return fmt.Sprintf("(%s, raw XDR unavailable)", v.Type)
	}
	if len(b64) <= maxInlineXDR {
		return b64
	}
	return fmt.Sprintf("%s…  (%d bytes base64)", b64[:maxInlineXDR], len(b64))
}

// payloadBase64 re-encodes a decoded value so the failure message can show
// what to paste into lens.
func payloadBase64(v *lens.Value) (string, error) {
	m, ok := v.Raw.(encoding.BinaryMarshaler)
	if !ok {
		return "", fmt.Errorf("value of type %s is not binary-marshalable", v.Type)
	}
	raw, err := m.MarshalBinary()
	if err != nil {
		return "", err
	}
	return base64Encode(raw), nil
}

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// indent prefixes every line, so a multi-line diff nests cleanly under the
// go test file:line prefix instead of running back to column zero.
func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
