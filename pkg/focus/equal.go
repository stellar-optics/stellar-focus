package focus

import (
	"encoding"
	"fmt"
	"strings"

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

// AssertEnvelopeEqualUnordered compares two transaction envelopes, treating
// their signatures as an unordered set.
//
// A multi-signed transaction carries its signatures in signing order, and that
// order is not part of what the transaction means. This wrapper compares the
// envelope as if it did not, so a test that only cares that both signatures
// are present is not failed by the order they arrived in.
//
// Everything else is still compared the usual way: a missing signature is
// still a difference.
func AssertEnvelopeEqualUnordered(t TB, want, got Value, opts ...Option) bool {
	t.Helper()
	return AssertEnvelopeEqual(t, want, got, append([]Option{SortPath("**.Signatures")}, opts...)...)
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

// AssertXDRRoundTrip checks that value survives a lossless encode/decode
// round-trip.
//
// It decodes value, re-encodes the decoded XDR with MarshalBinary, decodes the
// bytes again, and compares the two decoded trees structurally. That is the
// property a transaction builder, signer or serialization pipeline needs: the
// payload is not merely accepted by the decoder, it comes back unchanged,
// without a dropped union arm, a mutated field or a reordered array.
//
//	typeName names the XDR type, as listed by `lens types`. Naming it is
//	preferred because detection is ambiguous for short payloads, and because
//	the round-tripped bytes are decoded as the same type the original was.
//
// On failure the field path that did not survive the round-trip is printed, so
// the difference is the same structural diff the other equality assertions
// produce rather than two base64 strings to compare by eye.
func AssertXDRRoundTrip(t TB, value Value, typeName string) bool {
	t.Helper()
	const assertion = "AssertXDRRoundTrip"

	original, ok := resolve(t, assertion, "value", value, typeName)
	if !ok {
		return false
	}

	var (
		roundTripped *lens.Value
		err          error
	)
	if !guard(t, assertion, func() {
		m, ok := original.Raw.(encoding.BinaryMarshaler)
		if !ok {
			err = fmt.Errorf("a %s value cannot be re-encoded to XDR", original.Type)
			return
		}
		raw, marshalErr := m.MarshalBinary()
		if marshalErr != nil {
			err = fmt.Errorf("re-encoding %s: %w", original.Type, marshalErr)
			return
		}
		roundTripped, err = coerce(raw, original.Type)
	}) {
		return false
	}
	if err != nil {
		t.Errorf("%s: %v", assertion, err)
		return false
	}

	var (
		diff    *lens.DiffResult
		diffErr error
	)
	if !guard(t, assertion, func() {
		diff, diffErr = lens.Diff(original, roundTripped)
	}) {
		return false
	}
	if diffErr != nil {
		t.Errorf("%s: comparing the round-tripped value: %v", assertion, diffErr)
		return false
	}
	if len(diff.Changes) == 0 {
		return true
	}

	t.Errorf("%s", roundTripMessage(original, roundTripped, diff.Changes, newConfig()))
	return false
}

// roundTripMessage renders a round-trip failure with the same field-by-field
// diff as the equality assertions, headed by what actually failed.
func roundTripMessage(original, roundTripped *lens.Value, changes []lens.Change, c *config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AssertXDRRoundTrip: %s did not survive a MarshalBinary/UnmarshalBinary "+
		"round-trip — %s\n\n", humanType(original.Type), pluralChanges(len(changes)))
	b.WriteString(renderChanges(changes, c, original, roundTripped))
	b.WriteString("\n")
	fmt.Fprintf(&b, "original   %s\n", describePayload(original))
	fmt.Fprintf(&b, "round-trip %s\n", describePayload(roundTripped))
	return b.String()
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

	// Sorting happens before the diff, because the diff is positional: the
	// order has to be taken out of the two trees rather than patched
	// afterwards.
	wantValue, gotValue, sorts := c.sortValues(wantValue, gotValue)

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
		t.Errorf("%s: comparing values: %v", c.assertionName(assertion), diffErr)
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
				"and are probably stale: %v", c.assertionName(assertion), unused)
			return false
		}
		// The same goes for a sort rule that matched no array: it compared
		// nothing as an unordered set, which is not what it says it does.
		if len(sorts.stale) > 0 {
			t.Errorf("%s: values match, but these sort patterns matched no array "+
				"and are probably stale: %v", c.assertionName(assertion), sorts.stale)
			return false
		}
		return true
	}

	t.Errorf("%s", diffMessage(assertion, wantValue, gotValue, kept, applied, c, sorts))
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
