// Package golden — diff utilities for comparing golden fixtures.
package golden

import (
	"fmt"
	"io"
	"strings"
)

// Diff computes a full line-by-line diff between two strings in memory.
//
// Unlike StreamDiff, which processes streams with O(1) buffer overhead and
// halts early, Diff splits both full strings into memory. For small fixtures
// this is fast and straightforward; for multi-megabyte fixtures, use StreamDiff
// instead to avoid high GC pressure.
func Diff(s1, s2 string) (differed bool, out string) {
	if s1 == s2 {
		return false, ""
	}

	lines1 := strings.Split(s1, "\n")
	lines2 := strings.Split(s2, "\n")

	var b strings.Builder
	maxLen := len(lines1)
	if len(lines2) > maxLen {
		maxLen = len(lines2)
	}

	reported := 0
	for i := 0; i < maxLen; i++ {
		var l1, l2 string
		has1 := i < len(lines1)
		has2 := i < len(lines2)
		if has1 {
			l1 = normalizeLine(lines1[i])
		}
		if has2 {
			l2 = normalizeLine(lines2[i])
		}

		if l1 == l2 {
			continue
		}

		differed = true
		reported++
		if reported <= DefaultMaxStreamDifferences {
			switch {
			case !has1:
				fmt.Fprintf(&b, "@line %d\n- (eof)\n+ %s\n", i+1, l2)
			case !has2:
				fmt.Fprintf(&b, "@line %d\n- %s\n+ (eof)\n", i+1, l1)
			default:
				fmt.Fprintf(&b, "@line %d\n- %s\n+ %s\n", i+1, l1, l2)
			}
		}
	}

	if reported > DefaultMaxStreamDifferences {
		fmt.Fprintf(&b, "... (truncated after %d differences)\n", DefaultMaxStreamDifferences)
	}

	return differed, b.String()
}

// DiffReaders buffers both readers entirely into memory strings and calls Diff.
func DiffReaders(r1, r2 io.Reader) (differed bool, out string, err error) {
	b1, err := io.ReadAll(r1)
	if err != nil {
		return false, "", fmt.Errorf("reading first source: %w", err)
	}
	b2, err := io.ReadAll(r2)
	if err != nil {
		return false, "", fmt.Errorf("reading second source: %w", err)
	}
	d, s := Diff(string(b1), string(b2))
	return d, s, nil
}
