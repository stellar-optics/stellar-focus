package focus

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// The typed memo assertions take the memo's value in the form the caller
// already holds it — a string for a text memo, a uint64 for an id, bytes for a
// hash — instead of the "text: …" display form AssertMemo compares.
//
// They exist alongside AssertMemo because the display form makes a wrong memo
// type read like a wrong value: "id: 42" against "43" hides that the
// transaction carries the wrong kind of memo entirely. These name the type the
// transaction actually has, which is the part that is usually wrong.
//
// A memo is a union: at most one of text, id, hash and return is set. Asking
// for a text memo on a transaction with an id memo is a normal failure, not an
// input error, and the failure says which memo it found.

// AssertMemoText checks that the transaction carries a text memo with
// exactly this text.
//
// The protocol caps a text memo at 28 bytes, so a longer want can never match
// and the failure says so rather than leaving the reader to discover the cap.
func AssertMemoText(t TB, envelope Value, want string, opts ...Option) bool {
	t.Helper()
	return assertMemoText(t, newConfig(opts...), "AssertMemoText", envelope, want)
}

// AssertMemoTextEqual checks that the transaction carries a text memo with
// exactly this text.
//
// It is AssertMemoText under the name the other typed memo assertions use,
// and behaves identically.
func AssertMemoTextEqual(t TB, envelope Value, want string, opts ...Option) bool {
	t.Helper()
	return assertMemoText(t, newConfig(opts...), "AssertMemoTextEqual", envelope, want)
}

// assertMemoText is the shared implementation behind the text memo
// assertions.
func assertMemoText(t TB, c *config, assertion string, envelope Value, want string) bool {
	t.Helper()
	display := c.assertionName(assertion)

	memo, decoded, ok := resolveMemo(t, assertion, envelope)
	if !ok {
		return false
	}
	if memo.Type != xdr.MemoTypeMemoText {
		t.Errorf("%s", memoMismatch(display, memo, decoded, describeWantedText(want)))
		return false
	}

	var (
		got  string
		read bool
	)
	if !guard(t, assertion, func() { got, read = memo.GetText() }) {
		return false
	}
	if !read {
		t.Errorf("%s: the text memo could not be read; its XDR is malformed", display)
		return false
	}
	if got == want {
		return true
	}

	msg := mismatchMessage(assertion, "text memo", want, got, c)
	if len(want) > maxTextMemoBytes {
		msg += fmt.Sprintf("\n(a text memo holds at most %d bytes)", maxTextMemoBytes)
	}
	t.Errorf("%s", msg)
	return false
}

// AssertMemoID checks that the transaction carries an id memo with exactly
// this id. Id memos hold a uint64.
func AssertMemoID(t TB, envelope Value, want uint64, opts ...Option) bool {
	t.Helper()
	return assertMemoID(t, newConfig(opts...), "AssertMemoID", envelope, want)
}

// AssertMemoIDEqual checks that the transaction carries an id memo with
// exactly this id. Id memos hold a uint64.
//
// It is AssertMemoID under the name the other typed memo assertions use, and
// behaves identically.
func AssertMemoIDEqual(t TB, envelope Value, want uint64, opts ...Option) bool {
	t.Helper()
	return assertMemoID(t, newConfig(opts...), "AssertMemoIDEqual", envelope, want)
}

// assertMemoID is the shared implementation behind the id memo assertions.
func assertMemoID(t TB, c *config, assertion string, envelope Value, want uint64) bool {
	t.Helper()
	display := c.assertionName(assertion)

	memo, decoded, ok := resolveMemo(t, assertion, envelope)
	if !ok {
		return false
	}
	if memo.Type != xdr.MemoTypeMemoId {
		t.Errorf("%s", memoMismatch(display, memo, decoded, fmt.Sprintf("an id memo %d", want)))
		return false
	}

	var (
		got  xdr.Uint64
		read bool
	)
	if !guard(t, assertion, func() { got, read = memo.GetId() }) {
		return false
	}
	if !read {
		t.Errorf("%s: the id memo could not be read; its XDR is malformed", display)
		return false
	}
	if uint64(got) == want {
		return true
	}
	t.Errorf("%s", mismatchMessage(assertion, "id memo", want, uint64(got), c))
	return false
}

// AssertMemoHash checks that the transaction carries a hash memo with
// exactly these 32 bytes.
//
// A hash memo and a return memo hold the same thing — a 32-byte hash — and
// differ only in what they are for, so either satisfies this assertion. Use
// AssertMemoHashEqual or AssertMemoReturnEqual to insist on one of the two.
func AssertMemoHash(t TB, envelope Value, want [32]byte) bool {
	t.Helper()
	return assertMemoHash(t, "AssertMemoHash", envelope, xdr.Hash(want),
		xdr.MemoTypeMemoHash, xdr.MemoTypeMemoReturn)
}

// AssertMemoHashEqual checks that the transaction carries a hash memo with
// exactly these 32 bytes.
//
// want accepts an xdr.Hash, a [32]byte, 32 raw bytes, or the hash as 64 hex
// characters, in either case.
func AssertMemoHashEqual(t TB, envelope Value, want Value) bool {
	t.Helper()
	const assertion = "AssertMemoHashEqual"

	wantHash, err := hashFrom(want)
	if err != nil {
		t.Errorf("%s: want could not be read as a %s: %v", assertion, memoKind(xdr.MemoTypeMemoHash), err)
		return false
	}
	return assertMemoHash(t, assertion, envelope, wantHash, xdr.MemoTypeMemoHash)
}

// AssertMemoReturnEqual checks that the transaction carries a return memo with
// exactly these 32 bytes, the memo form a refund or rejection receipt uses.
//
// It accepts the same forms as AssertMemoHashEqual.
func AssertMemoReturnEqual(t TB, envelope Value, want Value) bool {
	t.Helper()
	const assertion = "AssertMemoReturnEqual"

	wantHash, err := hashFrom(want)
	if err != nil {
		t.Errorf("%s: want could not be read as a %s: %v", assertion, memoKind(xdr.MemoTypeMemoReturn), err)
		return false
	}
	return assertMemoHash(t, assertion, envelope, wantHash, xdr.MemoTypeMemoReturn)
}

// assertMemoHash is the shared implementation behind the hash assertions,
// whose values are all 32-byte hashes.
//
// The kinds are the memo types that satisfy the assertion: one for the
// assertions that insist on a single type, two for the one that accepts
// either.
func assertMemoHash(t TB, assertion string, envelope Value, want xdr.Hash, kinds ...xdr.MemoType) bool {
	t.Helper()

	memo, decoded, ok := resolveMemo(t, assertion, envelope)
	if !ok {
		return false
	}
	if !memoTypeIn(memo.Type, kinds) {
		t.Errorf("%s", memoMismatch(assertion, memo, decoded, describeWantedHash(want, kinds)))
		return false
	}

	var (
		got  xdr.Hash
		read bool
	)
	if !guard(t, assertion, func() { got, read = memoHash(memo, memo.Type) }) {
		return false
	}
	if !read {
		t.Errorf("%s: the %s could not be read; its XDR is malformed", assertion, memoKind(memo.Type))
		return false
	}
	if got == want {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s memo differs\n\n", assertion, memoKind(memo.Type))
	fmt.Fprintf(&b, "  got   %s\n", got.HexString())
	fmt.Fprintf(&b, "  want  %s", want.HexString())
	t.Errorf("%s", b.String())
	return false
}

// AssertNoMemo checks that the transaction carries no memo.
//
// A transaction with a memo and one without it are different transactions —
// the memo is what a caller reads to recognise their own transaction — so
// "no memo" is worth stating rather than leaving implicit.
func AssertNoMemo(t TB, envelope Value) bool {
	t.Helper()
	const assertion = "AssertNoMemo"

	memo, decoded, ok := resolveMemo(t, assertion, envelope)
	if !ok {
		return false
	}
	if memo.Type == xdr.MemoTypeMemoNone {
		return true
	}
	t.Errorf("%s: %s, want no memo", assertion, describeMemoFor(memo, decoded))
	return false
}

// memoTypeIn reports whether a memo type is one of the wanted kinds.
func memoTypeIn(t xdr.MemoType, kinds []xdr.MemoType) bool {
	for _, k := range kinds {
		if t == k {
			return true
		}
	}
	return false
}

// describeWantedHash names the hash memo an assertion expected, and the memo
// types that satisfy it.
func describeWantedHash(want xdr.Hash, kinds []xdr.MemoType) string {
	switch len(kinds) {
	case 0:
		return "a memo " + want.HexString()
	case 1:
		return fmt.Sprintf("a %s %s", memoKind(kinds[0]), want.HexString())
	default:
		names := make([]string, 0, len(kinds))
		for _, k := range kinds {
			names = append(names, memoKind(k))
		}
		return fmt.Sprintf("a %s memo %s", strings.Join(names, " or "), want.HexString())
	}
}

// memoHash reads the hash arm a hash or return memo carries. It calls
// generated accessors, so it must run behind guard.
func memoHash(memo xdr.Memo, kind xdr.MemoType) (xdr.Hash, bool) {
	if kind == xdr.MemoTypeMemoReturn {
		return memo.GetRetHash()
	}
	return memo.GetHash()
}

// resolveMemo reads an envelope's memo together with its decoded form, which
// the failure messages explain so the actual memo is rendered exactly as
// `lens explain` renders it.
func resolveMemo(t TB, assertion string, envelope Value) (xdr.Memo, *lens.Value, bool) {
	t.Helper()

	env, decoded, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return xdr.Memo{}, nil, false
	}

	var memo xdr.Memo
	if !guard(t, assertion, func() { memo = env.Memo() }) {
		return xdr.Memo{}, nil, false
	}
	return memo, decoded, true
}

// memoMismatch renders the failure for a memo of the wrong type, naming both
// what the transaction carries and what the assertion wanted. A memo that is
// not set is described as unset rather than as an empty memo.
func memoMismatch(assertion string, memo xdr.Memo, decoded *lens.Value, want string) string {
	return fmt.Sprintf("%s: %s, want %s", assertion, describeMemoFor(memo, decoded), want)
}

// describeMemoFor renders the memo a transaction carries for a failure
// message, such as "the memo is an id memo (id: 42)". The parenthesised value
// comes from lens, so it matches what `lens explain` prints.
func describeMemoFor(memo xdr.Memo, decoded *lens.Value) string {
	if memo.Type == xdr.MemoTypeMemoNone {
		return "the memo is unset"
	}

	out := fmt.Sprintf("the memo is %s memo", memoArticle(memoKind(memo.Type)))
	if display := explainedMemo(decoded); display != "" {
		out += " (" + display + ")"
	}
	return out
}

// memoArticle gives a memo kind its indefinite article, which is "an" for the
// id memo and "a" for the rest.
func memoArticle(kind string) string {
	if kind == "id" {
		return "an " + kind
	}
	return "a " + kind
}

// explainedMemo returns lens's rendering of an envelope's memo, e.g.
// "text: hello" or "id: 42". It returns "" when the value cannot be
// summarised.
//
// A panic is swallowed rather than reported: the rendering is only used to
// make an already-failing assertion friendlier, and reporting a second failure
// for it would fail the same test twice for one problem.
func explainedMemo(decoded *lens.Value) (out string) {
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()

	summary, err := lens.Explain(decoded)
	if err != nil || summary == nil {
		return ""
	}
	return summary.Memo
}

// memoKind names a memo type as a noun phrase, so it reads in a sentence.
func memoKind(t xdr.MemoType) string {
	switch t {
	case xdr.MemoTypeMemoNone:
		return "unset"
	case xdr.MemoTypeMemoText:
		return "text"
	case xdr.MemoTypeMemoId:
		return "id"
	case xdr.MemoTypeMemoHash:
		return "hash"
	case xdr.MemoTypeMemoReturn:
		return "return"
	default:
		return fmt.Sprintf("type %d", t)
	}
}

// hashFrom accepts the forms a caller may hold a 32-byte memo hash in.
func hashFrom(v Value) (xdr.Hash, error) {
	switch val := v.(type) {
	case xdr.Hash:
		return val, nil
	case *xdr.Hash:
		if val == nil {
			return xdr.Hash{}, fmt.Errorf("value is a nil *xdr.Hash")
		}
		return *val, nil
	case [32]byte:
		return xdr.Hash(val), nil
	case []byte:
		if len(val) != len(xdr.Hash{}) {
			return xdr.Hash{}, fmt.Errorf("hash is %d byte(s), want 32", len(val))
		}
		var h xdr.Hash
		copy(h[:], val)
		return h, nil
	case string:
		raw, err := hex.DecodeString(strings.TrimSpace(val))
		if err != nil {
			return xdr.Hash{}, fmt.Errorf("%q is not hex: %w", v, err)
		}
		if len(raw) != len(xdr.Hash{}) {
			return xdr.Hash{}, fmt.Errorf("hash is %d byte(s), want 32 (64 hex characters)", len(raw))
		}
		var h xdr.Hash
		copy(h[:], raw)
		return h, nil
	default:
		return xdr.Hash{}, fmt.Errorf(
			"unsupported type %T; expected an xdr.Hash, a [32]byte, 32 raw bytes or 64 hex characters", v)
	}
}

// describeWantedText names the text memo an assertion expected. A want longer
// than the protocol's cap can never match, so the description says that rather
// than leaving the reader to wonder why a text that is "in" the transaction
// failed.
func describeWantedText(want string) string {
	out := "a text memo " + render(want)
	if len(want) > maxTextMemoBytes {
		out += fmt.Sprintf(" (a text memo holds at most %d bytes)", maxTextMemoBytes)
	}
	return out
}

// maxTextMemoBytes is the protocol's cap on a text memo.
const maxTextMemoBytes = 28
