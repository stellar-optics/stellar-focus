package focus

import (
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
func diffMessage(
	assertion string,
	want, got *lens.Value,
	changes []lens.Change,
	applied []string,
	c *config,
	sorts sortResult,
) string {
	var b strings.Builder

	// Headline: the count is the first thing that tells you how bad it is.
	// The label goes beside the assertion name so a failure in a loop or a
	// multi-step flow names the step it came from.
	fmt.Fprintf(&b, "%s: %s differ — %s\n",
		c.assertionName(assertion), describeType(want, got), pluralChanges(len(changes)))

	if len(applied) > 0 {
		fmt.Fprintf(&b, "(ignored: %s)\n", strings.Join(applied, ", "))
	}

	// An array that was compared as a set is named, so a reader can tell
	// the difference between "these differ" and "these differ, and the order
	// was not part of the comparison".
	if len(sorts.unordered) > 0 {
		fmt.Fprintf(&b, "(compared as unordered sets: %s)\n", strings.Join(sorts.unordered, ", "))
	}

	// A sort rule that matched nothing is reported here too: on a failing
	// comparison it would otherwise be invisible.
	if len(sorts.stale) > 0 {
		fmt.Fprintf(&b, "(stale sort patterns, matched no array: %s)\n",
			strings.Join(sorts.stale, ", "))
	}

	b.WriteString("\n")
	b.WriteString(renderChanges(changes, c, want, got))
	b.WriteString("\n")

	// The raw payloads come last, truncated, so they are available for
	// copy-paste into lens without dominating the message.
	fmt.Fprintf(&b, "want  %s\n", describePayload(want))
	fmt.Fprintf(&b, "got   %s\n", describePayload(got))

	return b.String()
}

// renderChanges prints each difference as a labelled, side-by-side pair: the
// field path on its own line, then the value wanted and the value found, each
// labelled in its own column. It replaces the old "- before / + after" form
// because "which of these was I expecting?" is the question a failing diff
// has to answer, and a sign does not answer it.
//
// The layout stays aligned whether the value is present or not, and the
// colouring uses lens's palette — want in the removal colour, got in the
// addition colour — so a focus failure and `lens diff` agree. With colour off
// the palette is empty and the plain text is the coloured text minus the
// escapes.
func renderChanges(changes []lens.Change, c *config, want, got *lens.Value) string {
	shown := changes
	var elided int
	if c.maxChanges > 0 && len(changes) > c.maxChanges {
		shown = changes[:c.maxChanges]
		elided = len(changes) - c.maxChanges
	}

	p := c.palette()
	var b strings.Builder
	for _, ch := range shown {
		renderDiffContext(&b, ch, want, got, c.diffContext, true, p)
		renderChange(&b, ch, p)
		renderDiffContext(&b, ch, want, got, c.diffContext, false, p)
	}
	if elided > 0 {
		fmt.Fprintf(&b, "%s… and %d more difference(s); raise the limit with focus.WithMaxChanges%s\n",
			p.Dim, elided, p.Reset)
	}
	return b.String()
}

func renderDiffContext(b *strings.Builder, ch lens.Change, want, got *lens.Value, limit int, before bool, p lensformat.Palette) {
	if limit == 0 || want == nil || got == nil || want.Node == nil || got.Node == nil {
		return
	}

	parentPath := parentChangePath(ch.Path)
	wantParent := want.Node.Find(parentPath)
	gotParent := got.Node.Find(parentPath)
	if wantParent == nil || gotParent == nil {
		return
	}

	changedIndex := -1
	for index, child := range wantParent.Children {
		if nodeChildPath(parentPath, wantParent, child, index) == ch.Path {
			changedIndex = index
			break
		}
	}
	if changedIndex < 0 {
		return
	}

	if before {
		start := max(0, changedIndex-limit)
		for index := start; index < changedIndex; index++ {
			renderContextChild(b, parentPath, wantParent, gotParent, index, p)
		}
		return
	}
	end := min(len(wantParent.Children), changedIndex+limit+1)
	for index := changedIndex + 1; index < end; index++ {
		renderContextChild(b, parentPath, wantParent, gotParent, index, p)
	}
}

func renderContextChild(b *strings.Builder, parentPath string, wantParent, gotParent *lens.Node, index int, p lensformat.Palette) {
	if index >= len(gotParent.Children) {
		return
	}
	wantChild, gotChild := wantParent.Children[index], gotParent.Children[index]
	path := nodeChildPath(parentPath, wantParent, wantChild, index)
	if path != nodeChildPath(parentPath, gotParent, gotChild, index) ||
		wantChild.Kind != lens.KindScalar || gotChild.Kind != lens.KindScalar ||
		!equalScalars(wantChild.Value, gotChild.Value) || wantChild.Note != gotChild.Note {
		return
	}
	fmt.Fprintf(b, "  %s%s = %s%s\n", p.Dim, path, wantChild.String(), p.Reset)
}

func parentChangePath(path string) string {
	cut := max(strings.LastIndex(path, "."), strings.LastIndex(path, "["))
	if cut < 0 {
		return ""
	}
	return path[:cut]
}

func nodeChildPath(parentPath string, parent, child *lens.Node, index int) string {
	if parent.Kind == lens.KindList && child.Name == "" {
		return fmt.Sprintf("%s[%d]", parentPath, index)
	}
	if parentPath == "" {
		return child.Name
	}
	return parentPath + "." + child.Name
}

// renderChange renders one difference: the path with the marker for the kind
// of change, then one labelled line per side.
func renderChange(b *strings.Builder, ch lens.Change, p lensformat.Palette) {
	marker, color := "~", p.Change
	switch ch.Op {
	case lens.ChangeAdded:
		marker, color = "+", p.Add
	case lens.ChangeRemoved:
		marker, color = "-", p.Remove
	}
	fmt.Fprintf(b, "  %s%s %s%s\n", color, marker, changePath(ch), p.Reset)

	switch ch.Op {
	case lens.ChangeAdded:
		writeChangeSide(b, p, "got", p.Add, ch.After, ch.AfterNote)
	case lens.ChangeRemoved:
		writeChangeSide(b, p, "want", p.Remove, ch.Before, ch.BeforeNote)
	default:
		writeChangeSide(b, p, "want", p.Remove, ch.Before, ch.BeforeNote)
		writeChangeSide(b, p, "got", p.Add, ch.After, ch.AfterNote)
	}
}

// writeChangeSide renders one side of a difference with its label in a column,
// so the want and got values of every change line up under each other.
func writeChangeSide(b *strings.Builder, p lensformat.Palette, label, color string, v any, note string) {
	value, note := renderDiffValue(v, note)
	fmt.Fprintf(b, "      %s%-4s%s %s│%s %s%s%s",
		p.Dim, label, p.Reset, p.Dim, p.Reset, color, value, p.Reset)
	if note != "" {
		fmt.Fprintf(b, " %s%s%s", p.Dim, note, p.Reset)
	}
	b.WriteString("\n")
}

// renderDiffValue splits a change value into the part shown as the value and
// the enriched note beside it. A value that is only a note is shown as the
// note, so a diff never renders an empty cell.
func renderDiffValue(v any, note string) (string, string) {
	if v == nil {
		if note == "" {
			return "(none)", ""
		}
		return note, ""
	}
	return fmt.Sprintf("%v", v), note
}

// changePath names the field a change sits at, standing in for the empty path
// the walker reports when the two sides are different kinds of value.
func changePath(ch lens.Change) string {
	if ch.Path == "" {
		return "(value)"
	}
	return ch.Path
}

// mismatchMessage renders a simple want/got failure for scalar assertions.
//
// A config may be passed to render the assertion's label. The variadic form
// keeps the helper usable from the scalar assertions that do not take
// options themselves.
func mismatchMessage(assertion, field string, want, got any, c *config) string {
	return fmt.Sprintf("%s: %s is %s, want %s", c.assertionName(assertion), field, render(got), render(want))
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
func indent(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
