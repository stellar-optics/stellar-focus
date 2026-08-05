package focus

import (
	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// AssertEqual reports whether two XDR values are structurally equal,
// detecting their type automatically.
//
// Prefer one of the typed helpers — AssertEnvelopeEqual and friends — when
// the type is known. Detection is ambiguous for short payloads, and naming
// the type both removes that ambiguity and produces a clearer message when
// the wrong sort of value is passed in.
func AssertEqual(t TB, want, got Value, opts ...Option) bool {
	t.Helper()
	return assertEqual(t, "AssertEqual", "", want, got, opts...)
}

// AssertEqualAs compares two values decoded as the named XDR type, as listed
// by `lens types`.
func AssertEqualAs(t TB, typeName string, want, got Value, opts ...Option) bool {
	t.Helper()
	return assertEqual(t, "AssertEqualAs", typeName, want, got, opts...)
}

// AssertEnvelopeEqual compares two transaction envelopes.
//
// Most tests want IgnoreVolatile, or some subset of it: a rebuilt transaction
// legitimately differs in its sequence number, signatures and time bounds.
func AssertEnvelopeEqual(t TB, want, got Value, opts ...Option) bool {
	t.Helper()
	return assertEqual(t, "AssertEnvelopeEqual", "TransactionEnvelope", want, got, opts...)
}

// AssertResultEqual compares two transaction results.
func AssertResultEqual(t TB, want, got Value, opts ...Option) bool {
	t.Helper()
	return assertEqual(t, "AssertResultEqual", "TransactionResult", want, got, opts...)
}

// AssertMetaEqual compares two transaction metas.
func AssertMetaEqual(t TB, want, got Value, opts ...Option) bool {
	t.Helper()
	return assertEqual(t, "AssertMetaEqual", "TransactionMeta", want, got, opts...)
}

// assertEqual is the shared implementation behind every equality assertion.
func assertEqual(t TB, assertion, typeName string, want, got Value, opts ...Option) bool {
	t.Helper()

	wantValue, ok := resolve(t, assertion, "want", want, typeName)
	if !ok {
		return false
	}
	gotValue, ok := resolve(t, assertion, "got", got, typeName)
	if !ok {
		return false
	}

	c := newConfig(opts...)

	var (
		diff    *lens.DiffResult
		diffErr error
	)
	if !guard(t, assertion, func() {
		diff, diffErr = lens.Diff(wantValue, gotValue)
	}) {
		return false
	}
	if diffErr != nil {
		t.Errorf("%s: comparing values: %v", assertion, diffErr)
		return false
	}

	changes := applyNormalizers(diff.Changes, c)
	kept, applied := c.filter(changes)

	if len(kept) == 0 {
		// Report ignore rules that matched nothing even on success: a stale
		// pattern is a test quietly weakening over time, and the only moment
		// anyone would notice is now.
		if unused := c.unusedIgnores(changes); len(unused) > 0 {
			t.Errorf("%s: values match, but these ignore patterns matched nothing "+
				"and are probably stale: %v", assertion, unused)
			return false
		}
		return true
	}

	t.Errorf("%s", diffMessage(assertion, wantValue, gotValue, kept, applied, c))
	return false
}

// applyNormalizers rewrites values at matching paths, dropping any change
// whose two sides become equal as a result.
func applyNormalizers(changes []lens.Change, c *config) []lens.Change {
	if len(c.normalize) == 0 {
		return changes
	}

	out := make([]lens.Change, 0, len(changes))
	for _, ch := range changes {
		for _, n := range c.normalize {
			if !matchPath(n.pattern, ch.Path) {
				continue
			}
			ch.Before = n.fn(ch.Before)
			ch.After = n.fn(ch.After)
		}
		if equalScalars(ch.Before, ch.After) {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// equalScalars compares two normalized values. Formatting both is enough:
// these are the scalar leaves of a decoded tree, never composites.
func equalScalars(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return render(a) == render(b)
}
