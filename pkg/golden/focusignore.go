package golden

// Honouring .focusignore.
//
// A fixture that is not ready to be verified — a draft captured from a
// network a CI job cannot reach, an experiment whose shape still changes
// every day — should not fail the suite, and should not have to be deleted
// either. A .focusignore file lists such fixtures, and both AssertAll and
// `focus verify` leave them out.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IgnoreFileName is the file that lists the fixtures a run leaves out.
const IgnoreFileName = ".focusignore"

// Ignore is the set of rules a .focusignore file carries.
//
// Rules match slash-separated paths relative to the directory holding the
// file. A rule with no slash matches a name at any depth, so "*.draft.json"
// covers a draft anywhere in the tree; a rule with a slash is matched from
// that directory, with "**" reaching across directories; a trailing "/"
// marks a directory, whose contents are excluded with it; and a line whose
// first character is "#" is a comment.
type Ignore struct {
	rules []ignoreRule
}

// ignoreRule is one line of a .focusignore file, compiled.
type ignoreRule struct {
	// base is the directory the rule is relative to, as an absolute path.
	base string
	// matcher matches a slash-separated path relative to base.
	matcher *regexp.Regexp
}

// LoadIgnore reads the .focusignore files that apply to a fixture directory.
//
// The file in dir applies to the fixtures beneath it, and so does the file in
// the working directory, because a project usually keeps one at its root and
// runs both its tests and its commands from there. A missing file is not an
// error: most projects have no ignore file at all.
func LoadIgnore(dir string) (*Ignore, error) {
	working, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("reading the working directory: %w", err)
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	out := &Ignore{}
	if err := out.add(working); err != nil {
		return nil, err
	}
	if absolute != working {
		if err := out.add(absolute); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// add reads the .focusignore file in a directory, if it has one.
func (i *Ignore) add(dir string) error {
	path := filepath.Join(dir, IgnoreFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		pattern := strings.TrimSpace(line)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		matcher, err := compileIgnorePattern(pattern)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		i.rules = append(i.rules, ignoreRule{base: dir, matcher: matcher})
	}
	return nil
}

// Matches reports whether a fixture path is excluded.
//
// The path may be relative or absolute: both sides are resolved before a rule
// is applied, because a scan is often given a relative path while the
// directory holding the rules is absolute.
func (i *Ignore) Matches(path string) bool {
	if i == nil {
		return false
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, rule := range i.rules {
		rel, err := filepath.Rel(rule.base, absolute)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if matchesRuleOrAncestor(rule.matcher, filepath.ToSlash(rel)) {
			return true
		}
	}
	return false
}

// matchesRuleOrAncestor reports whether a path, or a directory holding it,
// matches a rule.
//
// A rule that names a directory excludes everything beneath it — the way an
// ignore file is read, not the way a shell glob is — so every ancestor is
// tried as well as the path itself.
func matchesRuleOrAncestor(matcher *regexp.Regexp, rel string) bool {
	for {
		if matcher.MatchString(rel) {
			return true
		}
		cut := strings.LastIndex(rel, "/")
		if cut < 0 {
			return false
		}
		rel = rel[:cut]
	}
}

// compileIgnorePattern compiles one .focusignore line into a matcher over a
// slash-separated path relative to the file that carried it.
//
// A rule written without a slash is matched at any depth, which is what
// "*.draft.json" is meant to say. A rule with a slash, or one anchored with a
// leading "/", is matched from the directory the file sits in.
func compileIgnorePattern(pattern string) (*regexp.Regexp, error) {
	rule := strings.TrimSuffix(strings.TrimSpace(pattern), "/")
	anchored := strings.HasPrefix(rule, "/")
	rule = strings.TrimPrefix(rule, "/")

	body := globBody(rule)
	if !anchored && !strings.Contains(rule, "/") {
		body = "(?:[^/]+/)*" + body
	}

	re, err := regexp.Compile("^" + body + "$")
	if err != nil {
		return nil, fmt.Errorf("pattern %q: %w", pattern, err)
	}
	return re, nil
}

// globBody translates a path pattern into the body of a regular expression.
//
// The pattern is a path pattern, so "*" and "?" stop at a separator and "**"
// does not.
func globBody(pattern string) string {
	var b strings.Builder

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

	return b.String()
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
