package focus

import "strings"

// Path matching for ignore and normalize rules.
//
// Patterns are matched against the dotted paths lens reports for a change,
// such as "V1.Tx.Operations[0].Body.PaymentOp.Amount" or
// "V1.Tx.Signatures[0].Signature". The language is deliberately small — a test
// author should not have to learn a query language to say "ignore the
// sequence number" — but it covers the shapes real diffs produce:
//
//	V1.Tx.SeqNum          one exact path
//	**.SeqNum             that field at any depth
//	V1.Tx.*               exactly one segment
//	Operations[*]         any index at this position
//	Operations[*].Sourc*  a glob within a segment: "*" any run of characters,
//	                      "?" one character, "[abc]" a set, "[!abc]" or
//	                      "[^abc]" its complement
//
// Matching is case-insensitive, so a pattern can be written the way the field
// reads out loud — "operations[*].sourceaccount" — without having to mirror
// the generated struct's capitalisation. A pattern is anchored to the tail of
// a path as well as its whole, so it does not need a "V1.Tx." prefix or a
// leading "**" to reach the part the test author was thinking about. A bare
// name also matches an indexed segment ("Signatures" covers "Signatures[0]"),
// because remembering to write the index for the only entry a field has is
// exactly the kind of detail that should not be load-bearing in a test.

// MatchPath reports whether a path matches an ignore pattern.
func MatchPath(pattern, path string) bool {
	return matchPath(pattern, path)
}

// matchPath reports whether a path matches a pattern.
func matchPath(pattern, path string) bool {
	pat := strings.Split(pattern, ".")
	seg := strings.Split(path, ".")

	// A pattern is anchored to the whole path or any tail of it. Trying each
	// start is what makes "operations[*].sourceaccount" mean what it looks
	// like without every caller writing a leading "**".
	for start := 0; start <= len(seg); start++ {
		if matchSegments(pat, seg[start:]) {
			return true
		}
	}
	return false
}

// matchSegments matches dot-separated pattern segments against path segments.
//
// "**" consumes any number of path segments, so it may appear anywhere rather
// than only at the end; every other pattern segment consumes exactly one.
func matchSegments(pat, seg []string) bool {
	switch {
	case len(pat) == 0:
		return len(seg) == 0
	case pat[0] == "**":
		// Match zero or more segments, trying the shortest first.
		for i := 0; i <= len(seg); i++ {
			if matchSegments(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	case len(seg) == 0:
		return false
	case segmentMatches(pat[0], seg[0]):
		return matchSegments(pat[1:], seg[1:])
	default:
		return false
	}
}

// segmentMatches compares one pattern segment with one path segment,
// understanding globs and index wildcards.
func segmentMatches(pattern, segment string) bool {
	pName, pIdx, pHasIdx := splitIndex(pattern)
	sName, sIdx, sHasIdx := splitIndex(segment)

	if !globMatch(pName, sName) {
		return false
	}
	// A bare name in the pattern matches an indexed segment, so
	// "**.Signatures" also covers "Signatures[0]".
	if !pHasIdx {
		return true
	}
	if !sHasIdx {
		return false
	}
	// Allow "Operations[!0]" as well as the fully globbed
	// "Operations[[!0]]", because the index is the common place to want a
	// negation and the extra brackets read as a mistake.
	if rest, ok := negateIndex(pIdx); ok {
		return !globMatch(rest, sIdx)
	}
	return globMatch(pIdx, sIdx)
}

// negateIndex recognises the "[!...]" and "[^...]" shorthand for an index,
// which is not a character class by itself.
func negateIndex(index string) (rest string, ok bool) {
	if len(index) < 2 || (index[0] != '!' && index[0] != '^') {
		return "", false
	}
	return index[1:], true
}

// splitIndex separates "Operations[0]" into "Operations" and "0".
func splitIndex(s string) (name, index string, hasIndex bool) {
	open := strings.IndexByte(s, '[')
	if open < 0 || !strings.HasSuffix(s, "]") {
		return s, "", false
	}
	return s[:open], s[open+1 : len(s)-1], true
}

// globMatch reports whether value matches a glob pattern, case-insensitively.
//
// The wildcards are the ones every shell and .gitignore understands: "*"
// matches any run of characters (including none), "?" matches exactly one, and
// a "[...]" class matches one character, with "!" or "^" negating it and "-"
// denoting a range. A "[" with no closing "]" is a literal rather than an
// error, matching path.Match's forgiving behaviour: a malformed pattern
// matching nothing would be a very confusing way to learn about a typo.
func globMatch(pattern, value string) bool {
	return globFold([]rune(strings.ToLower(pattern)), []rune(strings.ToLower(value)))
}

func globFold(pat, value []rune) bool {
	switch {
	case len(pat) == 0:
		return len(value) == 0
	case pat[0] == '*':
		// Collapse runs of stars; they mean the same as one.
		for len(pat) > 1 && pat[1] == '*' {
			pat = pat[1:]
		}
		if len(pat) == 1 {
			return true
		}
		for i := 0; i <= len(value); i++ {
			if globFold(pat[1:], value[i:]) {
				return true
			}
		}
		return false
	case pat[0] == '?':
		return len(value) > 0 && globFold(pat[1:], value[1:])
	case pat[0] == '[':
		if len(value) == 0 {
			return false
		}
		matched, rest, ok := matchClass(pat, value[0])
		if !ok {
			// No closing bracket: treat "[" as the literal it looks like.
			return value[0] == '[' && globFold(pat[1:], value[1:])
		}
		return matched && globFold(rest, value[1:])
	default:
		return len(value) > 0 && pat[0] == value[0] && globFold(pat[1:], value[1:])
	}
}

// matchClass matches one character against a "[...]" class starting at pat[0].
//
// rest is what follows the closing bracket, and ok is false when there is no
// closing bracket. A "]" immediately after the opening bracket (or after a
// negation) is a member of the class, not its terminator, so "[!]]" matches
// every character except "]".
func matchClass(pat []rune, value rune) (matched bool, rest []rune, ok bool) {
	i := 1
	negate := false
	if i < len(pat) && (pat[i] == '!' || pat[i] == '^') {
		negate = true
		i++
	}

	first := true
	found := false
	for i < len(pat) {
		if pat[i] == ']' && !first {
			return found != negate, pat[i+1:], true
		}
		first = false

		lo := pat[i]
		i++
		if i+1 < len(pat) && pat[i] == '-' && pat[i+1] != ']' {
			hi := pat[i+1]
			i += 2
			if value >= lo && value <= hi {
				found = true
			}
			continue
		}
		if lo == value {
			found = true
		}
	}
	return false, nil, false
}
