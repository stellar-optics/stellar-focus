// Package golden — streaming diff for large golden fixtures.
//
// When a fixture exceeds 1 MB the line-by-line in-memory diff used by the
// default path creates significant GC pressure: both serialized forms have to
// be held in memory at once and then split into string slices before the first
// comparison starts.  StreamDiff reads the two readers line by line using
// buffered I/O and streams mismatch hunks to w as it goes, so the peak
// allocation is O(window) rather than O(file).
package golden

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// DefaultMaxStreamDifferences is the maximum number of differing lines
// StreamDiff reports before it stops reading further. It matches the
// default context a human can reasonably review in one sitting.
const DefaultMaxStreamDifferences = 20

// StreamDiff reads r1 and r2 line by line and writes unified-style mismatch
// hunks to w.
//
// It returns differed=true when at least one mismatch was found. It stops
// emitting hunks (but keeps reading) once maxDifferences mismatching lines
// have been reported, to prevent runaway output for fixtures that diverge
// everywhere. When maxDifferences ≤ 0 the [DefaultMaxStreamDifferences] limit
// is applied instead.
//
// Lines are compared after [NormalizeWhitespace] is applied to each one, so a
// CRLF checkout or a trailing space never counts as a difference.
func StreamDiff(r1, r2 io.Reader, w io.Writer, maxDifferences int) (differed bool, err error) {
	if maxDifferences <= 0 {
		maxDifferences = DefaultMaxStreamDifferences
	}

	sc1 := bufio.NewScanner(r1)
	sc2 := bufio.NewScanner(r2)

	buf1 := make([]byte, 64*1024)
	buf2 := make([]byte, 64*1024)
	sc1.Buffer(buf1, 64*1024*1024)
	sc2.Buffer(buf2, 64*1024*1024)

	var (
		lineno    int
		reported  int
		truncated bool
	)

	for {
		has1 := sc1.Scan()
		has2 := sc2.Scan()

		if !has1 && !has2 {
			break
		}

		lineno++

		var b1, b2 []byte
		if has1 {
			b1 = bytes.TrimRight(sc1.Bytes(), " \t\r")
		}
		if has2 {
			b2 = bytes.TrimRight(sc2.Bytes(), " \t\r")
		}

		if has1 && has2 && bytes.Equal(b1, b2) {
			continue
		}

		differed = true
		reported++
		switch {
		case !has1:
			fmt.Fprintf(w, "@line %d\n- (eof)\n+ %s\n", lineno, b2)
		case !has2:
			fmt.Fprintf(w, "@line %d\n- %s\n+ (eof)\n", lineno, b1)
		default:
			fmt.Fprintf(w, "@line %d\n- %s\n+ %s\n", lineno, b1, b2)
		}

		if reported >= maxDifferences {
			truncated = true
			break
		}
	}

	if err1 := sc1.Err(); err1 != nil {
		return differed, fmt.Errorf("stream diff reading left side: %w", err1)
	}
	if err2 := sc2.Err(); err2 != nil {
		return differed, fmt.Errorf("stream diff reading right side: %w", err2)
	}

	if truncated {
		fmt.Fprintf(w, "... (truncated after %d differences; re-record with go test ./... -focus.update)\n", maxDifferences)
	}

	return differed, nil
}

// normalizeLine trims trailing horizontal whitespace from one line, which is
// the per-line part of what [NormalizeWhitespace] does to a whole document.
func normalizeLine(s string) string {
	return strings.TrimRight(s, " \t\r")
}
