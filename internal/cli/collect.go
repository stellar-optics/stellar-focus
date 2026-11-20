package cli

// Finding the fixtures to check.
//
// A suite rarely keeps its fixtures in one directory: envelopes, results and
// Soroban captures end up in subdirectories, and scratch fixtures end up in
// directories nobody wants checked. This file is how a path argument — a
// file, a directory or a pattern — turns into the list of fixtures to check.
//
// The rules are deliberately boring. A directory is searched all the way
// down, because a nested fixture that is silently skipped is worse than one
// that is reported; a pattern is matched the way a shell would match it, with
// "**" standing for any number of directories; and a .focusignore file lists
// the directories to leave out.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// fixtureSuffixes are the file names a fixture can carry.
//
// The library can store a fixture in three formats, so all three are
// recognised here: a verify run should not skip a fixture because it was
// written in YAML rather than JSON. The .yml spelling is accepted too,
// because the package reads it as YAML.
var fixtureSuffixes = []string{
	golden.JSON.DefaultSuffix(),
	golden.YAML.DefaultSuffix(),
	golden.Text.DefaultSuffix(),
	".golden.yml",
}

// collectFixtures expands files, directories and patterns into the list of
// fixture paths to check.
//
// A directory contributes the fixtures beneath it, at any depth; a pattern
// contributes the fixtures it matches; a file contributes itself.
func collectFixtures(args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		paths, err := collectArg(arg)
		if err != nil {
			return nil, err
		}
		out = append(out, paths...)
	}
	return out, nil
}

// collectArg expands one argument into fixture paths.
func collectArg(arg string) ([]string, error) {
	if hasGlobMeta(arg) {
		return globFixtures(arg)
	}

	info, err := os.Stat(arg)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", arg, err)
	}
	if !info.IsDir() {
		return []string{arg}, nil
	}
	return walkFixtures(arg)
}

// walkFixtures collects the fixtures beneath a directory.
//
// Hidden directories are skipped: they hold version-control data and editor
// scratch, never fixtures. The .focusignore rules are applied on top, so a
// suite can exclude a directory of benchmarks or throwaway captures.
func walkFixtures(dir string) ([]string, error) {
	ignores, err := golden.LoadIgnore(dir)
	if err != nil {
		return nil, err
	}

	var out []string
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == dir {
				return nil
			}
			if isHidden(entry.Name()) || ignores.Matches(path) {
				return fs.SkipDir
			}
			return nil
		}
		if !isFixtureName(entry.Name()) || ignores.Matches(path) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", dir, err)
	}
	return out, nil
}

// globFixtures collects the fixtures matching a pattern.
//
// A pattern holding "**" is matched against everything below its static
// prefix, so "testdata/**/*.golden.json" finds the fixtures in nested
// directories as well as the ones directly below. Any other pattern is
// matched one directory at a time, which is what filepath.Glob does.
func globFixtures(pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", pattern, err)
		}
		return fixtureFiles(matches), nil
	}

	base, rest := splitGlobPattern(pattern)
	matcher, err := globMatcher(rest)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", pattern, err)
	}
	ignores, err := golden.LoadIgnore(base)
	if err != nil {
		return nil, err
	}

	var out []string
	err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == base {
				return nil
			}
			if isHidden(entry.Name()) || ignores.Matches(path) {
				return fs.SkipDir
			}
			return nil
		}
		if !isFixtureName(entry.Name()) || ignores.Matches(path) {
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return relErr
		}
		if matcher(filepath.ToSlash(rel)) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", pattern, err)
	}
	return out, nil
}

// fixtureFiles keeps the fixtures among a list of matched paths.
func fixtureFiles(paths []string) []string {
	var out []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if isFixtureName(filepath.Base(path)) {
			out = append(out, path)
		}
	}
	return out
}

// isFixtureName reports whether a file name is one of the fixture names.
func isFixtureName(name string) bool {
	for _, suffix := range fixtureSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// isHidden reports whether a name is hidden, as the dot in ".git" makes it.
func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// hasGlobMeta reports whether a path holds glob metacharacters.
func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

// splitGlobPattern splits a pattern into the directory it is rooted at and
// the pattern relative to it.
//
// The rooted-at part is everything before the first segment holding a
// metacharacter, and it is the only part that has to exist on disk.
func splitGlobPattern(pattern string) (base, rest string) {
	parts := strings.Split(filepath.ToSlash(pattern), "/")

	keep := 0
	for _, part := range parts {
		if hasGlobMeta(part) {
			break
		}
		keep++
	}
	if keep == 0 {
		return ".", filepath.ToSlash(pattern)
	}
	if keep == len(parts) {
		return filepath.Dir(pattern), filepath.Base(pattern)
	}
	return strings.Join(parts[:keep], "/"), strings.Join(parts[keep:], "/")
}

// globMatcher compiles a pattern into a matcher over relative paths.
//
// It differs from filepath.Match in one way: "**" matches any number of path
// segments, so a pattern can reach into nested directories.
func globMatcher(pattern string) (func(string) bool, error) {
	re, err := globRegexp(pattern)
	if err != nil {
		return nil, err
	}
	return re.MatchString, nil
}

// globRegexp converts a path pattern into a regular expression.
//
// The pattern is a path pattern, so "*" and "?" stop at a separator and
// "**" does not.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")

	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:[^/]+/)*")
					continue
				}
				b.WriteString(".*")
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			class, width, ok := characterClass(pattern[i:])
			if !ok {
				b.WriteString(regexp.QuoteMeta(string(c)))
				continue
			}
			b.WriteString(class)
			i += width - 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}

	b.WriteString("$")
	return regexp.Compile(b.String())
}

// characterClass parses a "[...]" character class from the start of a
// pattern, returning its regular-expression form and how much it consumed.
//
// An unterminated class is not a class: it is a literal bracket, and a
// pattern that means something else must not silently mean this.
func characterClass(pattern string) (string, int, bool) {
	if len(pattern) < 2 || pattern[0] != '[' {
		return "", 0, false
	}

	i := 1
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		i++
	}
	for i < len(pattern) && pattern[i] != ']' {
		i++
	}
	if i >= len(pattern) {
		return "", 0, false
	}

	class := pattern[:i+1]
	if strings.HasPrefix(class, "[!") {
		class = "[^" + class[2:]
	}
	return class, i + 1, true
}
