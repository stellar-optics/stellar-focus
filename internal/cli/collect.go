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
	"errors"
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

// ignoreFileName is the file listing the paths a scan should skip.
const ignoreFileName = ".focusignore"

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
	ignores, err := loadIgnores(dir)
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
			if isHidden(entry.Name()) || ignores.matches(path) {
				return fs.SkipDir
			}
			return nil
		}
		if !isFixtureName(entry.Name()) || ignores.matches(path) {
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
	ignores, err := loadIgnores(base)
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
			if isHidden(entry.Name()) || ignores.matches(path) {
				return fs.SkipDir
			}
			return nil
		}
		if !isFixtureName(entry.Name()) || ignores.matches(path) {
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

// ignores are the paths a scan skips.
type ignores struct {
	rules []ignoreRule
}

// ignoreRule is one line of a .focusignore file.
type ignoreRule struct {
	// base is the directory the rule is relative to.
	base string
	// pattern is the rule as written.
	pattern string
	// matcher, when set, matches a path relative to base.
	matcher func(string) bool
}

// loadIgnores reads the .focusignore files that apply to a scan.
//
// The file in the directory being scanned applies to that directory; the
// file in the working directory applies to every scan, because a project
// usually keeps one at its root.
func loadIgnores(dir string) (*ignores, error) {
	out := &ignores{}

	working, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("reading the working directory: %w", err)
	}
	if err := out.add(working); err != nil {
		return nil, err
	}
	if dir != working {
		if err := out.add(dir); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// add reads the .focusignore file in a directory, if it has one.
//
// A missing file is not an error: most projects have no ignore file at all.
func (i *ignores) add(dir string) error {
	path := filepath.Join(dir, ignoreFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		rule := strings.TrimSpace(line)
		if rule == "" || strings.HasPrefix(rule, "#") {
			continue
		}
		rule = strings.TrimSuffix(rule, "/")

		r := ignoreRule{base: dir, pattern: rule}
		if hasGlobMeta(rule) {
			matcher, err := globMatcher(rule)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			r.matcher = matcher
		}
		i.rules = append(i.rules, r)
	}
	return nil
}

// matches reports whether a path is excluded by a rule.
//
// A rule without wildcards names a path, and excludes everything beneath it;
// a rule with wildcards is matched against the path relative to the
// .focusignore file that carried it.
func (i *ignores) matches(path string) bool {
	for _, rule := range i.rules {
		rel, err := relativeTo(rule.base, path)
		if err != nil {
			continue
		}

		if rule.matcher != nil {
			if rule.matcher(rel) {
				return true
			}
			continue
		}
		if rel == rule.pattern || strings.HasPrefix(rel, rule.pattern+"/") {
			return true
		}
	}
	return false
}

// relativeTo returns a path relative to a base, both resolved to absolute
// paths first.
//
// A scan is often given a relative path while the directory holding the
// .focusignore rules is absolute, and the two have to be brought onto the
// same footing before either can be subtracted from the other.
func relativeTo(base, path string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
