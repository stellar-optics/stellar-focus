// Package golden — snapshot diff summary table for batch golden test failures.
//
// When golden.AssertAll or golden.AssertAllParallel runs over a large table-driven
// suite, a long run of failures can flood the terminal with consecutive multi-line
// diffs. The summary table printed at the conclusion of batch assertions gives a
// reviewer a concise overview: how many fixtures broke, their names, DRIFTED or
// MISSING status, and differing field paths.
package golden

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// colorEnabled reports whether ANSI color codes should be emitted on w.
//
// It is true when w is a real terminal (os.Stdout or os.Stderr) and the
// NO_COLOR / TERM=dumb convention is not set. Any other writer (a bytes.Buffer
// in a test, a file) gets plain text.
func colorEnabled(w io.Writer) bool {
	switch w {
	case os.Stdout, os.Stderr:
	default:
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}

const (
	ansiRed   = "\033[31m"
	ansiReset = "\033[0m"
)

// diffSummaryStatus is the word the summary table shows for a failed case.
//
// DRIFTED means the fixture exists on disk but its content no longer matches.
// MISSING means no fixture file was found on disk.
func diffSummaryStatus(messages []string) string {
	for _, m := range messages {
		if strings.Contains(m, "does not exist") {
			return "MISSING"
		}
	}
	return "DRIFTED"
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEscape = false
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// diffSummaryPaths extracts differing field paths from assertion error messages.
func diffSummaryPaths(messages []string) string {
	var paths []string
	seen := make(map[string]bool)
	for _, m := range messages {
		for _, line := range strings.Split(m, "\n") {
			line = strings.TrimSpace(stripANSI(line))
			var field string
			if strings.HasPrefix(line, "~ ") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "+ ") {
				field = strings.TrimSpace(line[2:])
				if idx := strings.Index(field, ":"); idx > 0 {
					field = strings.TrimSpace(field[:idx])
				}
			}
			if field != "" && !seen[field] {
				seen[field] = true
				paths = append(paths, field)
				if len(paths) >= 2 {
					break
				}
			}
		}
		if len(paths) >= 2 {
			break
		}
	}
	return strings.Join(paths, ", ")
}

// SummaryRow represents one row in the failure summary table.
type SummaryRow struct {
	Name   string
	Status string
	Paths  string
}

// RenderSummaryTable writes the FAILED GOLDEN FIXTURES table to w.
//
// It is called by AssertAll and AssertAllParallel after all cases have run.
// When failed is 0, nothing is written.
func RenderSummaryTable(w io.Writer, results []caseResult, failed int) {
	RenderSummaryTableWithColor(w, results, failed, colorEnabled(w))
}

// RenderSummaryTableWithColor writes the summary table with explicit ANSI color control.
func RenderSummaryTableWithColor(w io.Writer, results []caseResult, failed int, useColor bool) {
	if failed == 0 {
		return
	}

	rows := make([]SummaryRow, 0, failed)
	for _, r := range results {
		if r.ok {
			continue
		}
		status := diffSummaryStatus(r.messages)
		paths := diffSummaryPaths(r.messages)
		if paths == "" && status == "MISSING" {
			paths = "<file not found on disk>"
		}
		rows = append(rows, SummaryRow{
			Name:   r.name,
			Status: status,
			Paths:  paths,
		})
	}

	RenderSummaryRows(w, rows, len(results), failed, useColor)
}

// RenderSummaryRows formats and writes the summary table for rows to w.
func RenderSummaryRows(w io.Writer, rows []SummaryRow, total, failed int, useColor bool) {
	if len(rows) == 0 {
		return
	}

	red := func(s string) string {
		if useColor {
			return ansiRed + s + ansiReset
		}
		return s
	}

	const (
		hName   = "Fixture Name"
		hStatus = "Status"
		hPaths  = "Differing Field Paths"
	)
	wName, wStatus, wPaths := len(hName), len(hStatus), len(hPaths)
	for _, row := range rows {
		if len(row.Name) > wName {
			wName = len(row.Name)
		}
		if len(row.Status) > wStatus {
			wStatus = len(row.Status)
		}
		if len(row.Paths) > wPaths {
			wPaths = len(row.Paths)
		}
	}

	pad := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}

	sep := fmt.Sprintf("┌%s┬%s┬%s┐",
		strings.Repeat("─", wName+2),
		strings.Repeat("─", wStatus+2),
		strings.Repeat("─", wPaths+2),
	)
	mid := fmt.Sprintf("├%s┼%s┼%s┤",
		strings.Repeat("─", wName+2),
		strings.Repeat("─", wStatus+2),
		strings.Repeat("─", wPaths+2),
	)
	bot := fmt.Sprintf("└%s┴%s┴%s┘",
		strings.Repeat("─", wName+2),
		strings.Repeat("─", wStatus+2),
		strings.Repeat("─", wPaths+2),
	)

	fmt.Fprintf(w, "\nFAILED GOLDEN FIXTURES (%d / %d):\n", failed, total)
	fmt.Fprintln(w, sep)
	fmt.Fprintf(w, "│ %s │ %s │ %s │\n",
		pad(hName, wName), pad(hStatus, wStatus), pad(hPaths, wPaths))
	fmt.Fprintln(w, mid)
	for _, row := range rows {
		fmt.Fprintf(w, "│ %s │ %s │ %s │\n",
			pad(row.Name, wName),
			red(pad(row.Status, wStatus)),
			pad(row.Paths, wPaths),
		)
	}
	fmt.Fprintln(w, bot)
	fmt.Fprintln(w, "Run 'go test -focus.update' to accept new values.")
}
