package focus

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"
)

// Terminal rendering for assertion failures.
//
// The failure message is this library's product, and on a terminal the
// fastest way to see what changed is colour: the mismatched field path is
// highlighted while everything else stays plain. Test output is usually
// captured rather than read on a terminal, so colour defaults to off (see
// WithColor); these helpers render colour explicitly when the caller asked
// for it and keep the plain text identical when stripped.
//
// All helpers are offline-first and deterministic: they perform no I/O
// beyond inspecting the given writer for terminal detection, make no
// network calls, and produce the same output for the same input.

// ANSI escape sequences used for terminal output. They match the lens
// ColorPalette so a focus failure and `lens diff` highlight the same way.
const (
	// ANSIReset clears all attributes.
	ANSIReset = "\x1b[0m"
	// ANSIBold renders bold.
	ANSIBold = "\x1b[1m"
	// ANSIRed renders red, for removals.
	ANSIRed = "\x1b[31m"
	// ANSIGreen renders green, for additions.
	ANSIGreen = "\x1b[32m"
	// ANSIYellow renders yellow, for changed field paths.
	ANSIYellow = "\x1b[33m"
	// ANSICyan renders cyan, for keys.
	ANSICyan = "\x1b[36m"
	// ANSIDim renders dim, for notes.
	ANSIDim = "\x1b[90m"
)

// ansiSequence matches a single ANSI escape sequence (SGR subset).
var ansiSequence = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Colorize wraps s in the given ANSI code and a reset.
//
// It always wraps, even when s is empty, so callers can distinguish "no
// colour requested" (empty code, s returned unchanged) from "coloured empty
// string". Pass an empty code to disable colour for a single value.
func Colorize(s, code string) string {
	if code == "" {
		return s
	}
	return code + s + ANSIReset
}

// Bold renders s in bold.
func Bold(s string) string {
	return Colorize(s, ANSIBold)
}

// HighlightPath highlights a dotted XDR field path (for example
// "V1.Tx.Operations[0].Body.PaymentOp.Amount") exactly as the diff
// formatter does: yellow, so the eye lands on what changed.
func HighlightPath(path string) string {
	return Colorize(path, ANSIYellow)
}

// StripANSI removes ANSI escape sequences from s, returning the plain text.
func StripANSI(s string) string {
	return ansiSequence.ReplaceAllString(s, "")
}

// ContainsANSI reports whether s contains any ANSI escape sequence.
func ContainsANSI(s string) bool {
	return ansiSequence.MatchString(s)
}

// ColorEnabled reports whether a WithColor mode requests colour for string
// rendering.
//
// "always" enables colour, "never" (and any unknown mode) disables it, and
// "auto" honours the NO_COLOR convention and TERM=dumb without requiring a
// writer. Writer-aware detection belongs to SupportsColorWriter; this is for
// failure strings reported through TB, where there is no writer to inspect.
func ColorEnabled(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "always":
		return true
	case "auto":
		if _, set := os.LookupEnv("NO_COLOR"); set {
			return false
		}
		if os.Getenv("TERM") == "dumb" {
			return false
		}
		return true
	default:
		return false
	}
}

// SupportsColorWriter reports whether colourised output is appropriate for
// w, honouring NO_COLOR and TERM=dumb and requiring w to be a character
// device. It delegates to lens so focus and `lens diff` agree.
func SupportsColorWriter(w io.Writer) bool {
	return lensformat.ShouldColor(w)
}

// PaletteForWriter returns the palette appropriate for w under mode, one of
// "auto", "always" or "never". It delegates to lens so focus and lens stay
// consistent; "auto" inspects w while explicit modes override detection.
func PaletteForWriter(w io.Writer, mode string) lensformat.Palette {
	return lensformat.PaletteFor(w, mode)
}

// FormatFieldMismatch renders a scalar want/got failure, highlighting the
// field name when color is true.
//
// The plain text is identical to mismatchMessage; colour only wraps the
// field name and the two values, so StripANSI(colored) == plain. Values use
// the same quoting as every other failure via render, keeping empty strings
// and whitespace visible.
func FormatFieldMismatch(assertion, field string, want, got any, color bool) string {
	plain := mismatchMessage(assertion, field, want, got, nil)
	if !color {
		return plain
	}
	coloredField := HighlightPath(field)
	coloredWant := Colorize(render(want), ANSIGreen)
	coloredGot := Colorize(render(got), ANSIRed)
	return fmt.Sprintf("%s: %s is %s, want %s", assertion, coloredField, coloredGot, coloredWant)
}

// HighlightMismatchField returns msg with every occurrence of field wrapped
// in the change colour, so a pre-rendered failure can emphasise exactly
// which field mismatched. An empty field returns msg unchanged.
func HighlightMismatchField(msg, field string) string {
	if field == "" || msg == "" {
		return msg
	}
	if !strings.Contains(msg, field) {
		return msg
	}
	return strings.ReplaceAll(msg, field, HighlightPath(field))
}
