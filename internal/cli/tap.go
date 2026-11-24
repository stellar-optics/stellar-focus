package cli

// TAP output for focus verify.
//
// TAP version 13 is the other format CI test reporters understand: a plan, one
// ok/not ok line per fixture, and a YAML diagnostic block for a failure. A
// skipped fixture is reported as ok with a SKIP directive, which is how TAP
// says "not a failure, not a pass".

import (
	"fmt"
	"io"
	"strings"
)

// writeTAP renders a verification report as TAP version 13.
func writeTAP(w io.Writer, report *VerifyReport) error {
	if _, err := fmt.Fprintln(w, "TAP version 13"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "1..%d\n", len(report.Results)); err != nil {
		return err
	}

	var passed, failed, skipped int
	for i, item := range report.Results {
		number := i + 1
		switch item.Status {
		case statusMatched:
			passed++
			if _, err := fmt.Fprintf(w, "ok %d - %s\n", number, item.Path); err != nil {
				return err
			}
		case statusDrifted:
			failed++
			if _, err := fmt.Fprintf(w, "not ok %d - %s\n", number, item.Path); err != nil {
				return err
			}
			if err := writeTAPDiagnostic(w, item); err != nil {
				return err
			}
		case statusErrored:
			failed++
			if _, err := fmt.Fprintf(w, "not ok %d - %s\n", number, item.Path); err != nil {
				return err
			}
			if err := writeTAPDiagnostic(w, item); err != nil {
				return err
			}
		case statusSkipped, statusFiltered:
			skipped++
			if _, err := fmt.Fprintf(w, "ok %d - %s # SKIP %s\n",
				number, item.Path, tapText(item.Error)); err != nil {
				return err
			}
		}
	}

	_, err := fmt.Fprintf(w, "# tests: %d\n# passed: %d\n# failed: %d\n# skipped: %d\n",
		len(report.Results), passed, failed, skipped)
	return err
}

// writeTAPDiagnostic writes the YAML block that explains a failing fixture.
//
// Every line is indented and quoted where needed, so a path or an error
// containing a colon cannot turn the block into something else.
func writeTAPDiagnostic(w io.Writer, item VerifyItemResult) error {
	message := item.Error
	if message == "" {
		message = "the fixture no longer matches the chain"
	}

	if _, err := fmt.Fprintln(w, "  ---"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  message: %s\n", tapQuote(message)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  status: %s\n", item.Status); err != nil {
		return err
	}
	if len(item.DiffPaths) > 0 {
		if _, err := fmt.Fprintln(w, "  diff_paths:"); err != nil {
			return err
		}
		for _, path := range item.DiffPaths {
			if _, err := fmt.Fprintf(w, "    - %s\n", tapQuote(path)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w, "  ...")
	return err
}

// tapQuote quotes a string for a YAML scalar.
func tapQuote(text string) string {
	return fmt.Sprintf("%q", tapText(text))
}

// tapText flattens a message onto one line, so a multi-line error cannot
// break a TAP line or a YAML scalar.
func tapText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.TrimSpace(text)
}
