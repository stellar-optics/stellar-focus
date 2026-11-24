package focus_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// testHash is a fixed 32-byte memo hash, so tests can compare hex, bytes and
// xdr.Hash forms of the same value.
var testHash = xdr.Hash{
	0x7c, 0x3a, 0x1f, 0x00, 0x9b, 0x2d, 0x44, 0x11,
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
}

// otherHash differs from testHash in its first byte, so a failing assertion
// has something to show.
var otherHash = func() xdr.Hash {
	h := testHash
	h[0] = 0x0f
	return h
}()

func textMemo(s string) xdr.Memo {
	return xdr.Memo{Type: xdr.MemoTypeMemoText, Text: &s}
}

func idMemo(id uint64) xdr.Memo {
	v := xdr.Uint64(id)
	return xdr.Memo{Type: xdr.MemoTypeMemoId, Id: &v}
}

func hashMemo(h xdr.Hash) xdr.Memo {
	return xdr.Memo{Type: xdr.MemoTypeMemoHash, Hash: &h}
}

func returnMemo(h xdr.Hash) xdr.Memo {
	return xdr.Memo{Type: xdr.MemoTypeMemoReturn, RetHash: &h}
}

func envelopeWithMemo(t *testing.T, memo xdr.Memo) xdr.TransactionEnvelope {
	t.Helper()
	env := envelopeValue(t, paymentOp(t, 100))
	env.V1.Tx.Memo = memo
	return env
}

func TestAssertMemoTypedPasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		envelope xdr.TransactionEnvelope
		call     func(focus.TB) bool
	}{
		{
			"text memo",
			envelopeWithMemo(t, textMemo("invoice-42")),
			func(tb focus.TB) bool {
				return focus.AssertMemoTextEqual(tb, envelopeWithMemo(t, textMemo("invoice-42")), "invoice-42")
			},
		},
		{
			"empty text memo",
			envelopeWithMemo(t, textMemo("")),
			func(tb focus.TB) bool { return focus.AssertMemoTextEqual(tb, envelopeWithMemo(t, textMemo("")), "") },
		},
		{
			"id memo",
			envelopeWithMemo(t, idMemo(42)),
			func(tb focus.TB) bool { return focus.AssertMemoIDEqual(tb, envelopeWithMemo(t, idMemo(42)), 42) },
		},
		{
			"id memo above the int64 range",
			envelopeWithMemo(t, idMemo(1<<63+1)),
			func(tb focus.TB) bool {
				return focus.AssertMemoIDEqual(tb, envelopeWithMemo(t, idMemo(1<<63+1)), 1<<63+1)
			},
		},
		{
			"hash memo as xdr.Hash",
			envelopeWithMemo(t, hashMemo(testHash)),
			func(tb focus.TB) bool {
				return focus.AssertMemoHashEqual(tb, envelopeWithMemo(t, hashMemo(testHash)), testHash)
			},
		},
		{
			"hash memo as uppercase hex",
			envelopeWithMemo(t, hashMemo(testHash)),
			func(tb focus.TB) bool {
				return focus.AssertMemoHashEqual(tb, envelopeWithMemo(t, hashMemo(testHash)),
					strings.ToUpper(testHash.HexString()))
			},
		},
		{
			"hash memo as [32]byte",
			envelopeWithMemo(t, hashMemo(testHash)),
			func(tb focus.TB) bool {
				return focus.AssertMemoHashEqual(tb, envelopeWithMemo(t, hashMemo(testHash)), [32]byte(testHash))
			},
		},
		{
			"hash memo as raw bytes",
			envelopeWithMemo(t, hashMemo(testHash)),
			func(tb focus.TB) bool {
				return focus.AssertMemoHashEqual(tb, envelopeWithMemo(t, hashMemo(testHash)), testHash[:])
			},
		},
		{
			"return memo",
			envelopeWithMemo(t, returnMemo(testHash)),
			func(tb focus.TB) bool {
				return focus.AssertMemoReturnEqual(tb, envelopeWithMemo(t, returnMemo(testHash)), testHash.HexString())
			},
		},
		{
			"fee bump reads the inner transaction's memo",
			feeBumpEnvelopeValue(t, envelopeWithMemo(t, textMemo("inner")), nil, nil),
			func(tb focus.TB) bool {
				return focus.AssertMemoTextEqual(tb,
					feeBumpEnvelopeValue(t, envelopeWithMemo(t, textMemo("inner")), nil, nil), "inner")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !tc.call(tb) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertMemoTypedMessages checks that a wrong memo type is reported as
// such, rather than only comparing rendered values, and that a wrong value
// names both sides.
func TestAssertMemoTypedMessages(t *testing.T) {
	t.Parallel()

	text := func(s string) xdr.TransactionEnvelope { return envelopeWithMemo(t, textMemo(s)) }
	id := func(v uint64) xdr.TransactionEnvelope { return envelopeWithMemo(t, idMemo(v)) }
	hash := func(h xdr.Hash) xdr.TransactionEnvelope { return envelopeWithMemo(t, hashMemo(h)) }
	unset := func() xdr.TransactionEnvelope {
		return envelopeWithMemo(t, xdr.Memo{Type: xdr.MemoTypeMemoNone})
	}

	longText := strings.Repeat("x", 29)

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "wrong text value",
			call: func(tb focus.TB) bool { return focus.AssertMemoTextEqual(tb, text("goodbye"), "hello") },
			contains: []string{
				"AssertMemoTextEqual: text memo is \"goodbye\", want \"hello\"",
			},
		},
		{
			name: "text memo longer than the protocol cap",
			call: func(tb focus.TB) bool { return focus.AssertMemoTextEqual(tb, unset(), longText) },
			contains: []string{
				"AssertMemoTextEqual",
				"holds at most 28 bytes",
			},
		},
		{
			name: "id memo where text was wanted",
			call: func(tb focus.TB) bool { return focus.AssertMemoTextEqual(tb, id(42), "hello") },
			contains: []string{
				`the memo is an id memo (id: 42), want a text memo "hello"`,
			},
		},
		{
			name: "unset memo where text was wanted",
			call: func(tb focus.TB) bool { return focus.AssertMemoTextEqual(tb, unset(), "hello") },
			contains: []string{
				`the memo is unset, want a text memo "hello"`,
			},
		},
		{
			name: "wrong id value",
			call: func(tb focus.TB) bool { return focus.AssertMemoIDEqual(tb, id(41), 42) },
			contains: []string{
				"AssertMemoIDEqual: id memo is 41, want 42",
			},
		},
		{
			name: "text memo where id was wanted",
			call: func(tb focus.TB) bool { return focus.AssertMemoIDEqual(tb, text("hello"), 42) },
			contains: []string{
				"the memo is a text memo (text: hello), want an id memo 42",
			},
		},
		{
			name: "wrong hash value",
			call: func(tb focus.TB) bool { return focus.AssertMemoHashEqual(tb, hash(testHash), otherHash.HexString()) },
			contains: []string{
				"AssertMemoHashEqual: hash memo differs",
				"got   " + testHash.HexString(),
				"want  " + otherHash.HexString(),
			},
		},
		{
			name: "id memo where hash was wanted",
			call: func(tb focus.TB) bool { return focus.AssertMemoHashEqual(tb, id(7), testHash) },
			contains: []string{
				"the memo is an id memo (id: 7), want a hash " + testHash.HexString(),
			},
		},
		{
			name: "wrong return value",
			call: func(tb focus.TB) bool {
				return focus.AssertMemoReturnEqual(tb, envelopeWithMemo(t, returnMemo(otherHash)), testHash)
			},
			contains: []string{
				"AssertMemoReturnEqual: return memo differs",
				"got   " + otherHash.HexString(),
				"want  " + testHash.HexString(),
			},
		},
		{
			name: "hash memo where return was wanted",
			call: func(tb focus.TB) bool { return focus.AssertMemoReturnEqual(tb, hash(testHash), testHash) },
			contains: []string{
				"the memo is a hash memo (hash: " + testHash.HexString() + "), want a return " + testHash.HexString(),
			},
		},
		{
			name: "hash want that is not hex",
			call: func(tb focus.TB) bool { return focus.AssertMemoHashEqual(tb, hash(testHash), "zzzz") },
			contains: []string{
				`want could not be read as a hash: "zzzz" is not hex`,
			},
		},
		{
			name: "hash want that is the wrong length",
			call: func(tb focus.TB) bool {
				return focus.AssertMemoHashEqual(tb, hash(testHash), hex.EncodeToString([]byte{1, 2, 3}))
			},
			contains: []string{
				"want could not be read as a hash: hash is 3 byte(s), want 32 (64 hex characters)",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
				t.Fatal("assertion passed when it should have failed")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestAssertMemoVariantsPass covers the typed memo assertions: the memo the
// transaction carries is read in the form the caller already holds it.
func TestAssertMemoVariantsPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(focus.TB) bool
	}{
		{
			"text memo",
			func(tb focus.TB) bool {
				return focus.AssertMemoText(tb, envelopeWithMemo(t, textMemo("invoice-42")), "invoice-42")
			},
		},
		{
			"empty text memo",
			func(tb focus.TB) bool {
				return focus.AssertMemoText(tb, envelopeWithMemo(t, textMemo("")), "")
			},
		},
		{
			"id memo",
			func(tb focus.TB) bool {
				return focus.AssertMemoID(tb, envelopeWithMemo(t, idMemo(42)), 42)
			},
		},
		{
			"id memo above the int64 range",
			func(tb focus.TB) bool {
				return focus.AssertMemoID(tb, envelopeWithMemo(t, idMemo(1<<63+1)), 1<<63+1)
			},
		},
		{
			"hash memo",
			func(tb focus.TB) bool {
				return focus.AssertMemoHash(tb, envelopeWithMemo(t, hashMemo(testHash)), testHash)
			},
		},
		{
			"return memo satisfies the hash assertion",
			func(tb focus.TB) bool {
				return focus.AssertMemoHash(tb, envelopeWithMemo(t, returnMemo(testHash)), testHash)
			},
		},
		{
			"no memo",
			func(tb focus.TB) bool {
				return focus.AssertNoMemo(tb, envelopeWithMemo(t, xdr.Memo{Type: xdr.MemoTypeMemoNone}))
			},
		},
		{
			"no memo on an envelope that never had one",
			func(tb focus.TB) bool {
				return focus.AssertNoMemo(tb, envelopeValue(t, paymentOp(t, 100)))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !tc.call(tb) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertMemoVariantsFail checks what the typed memo assertions say when
// they fail: the memo the transaction actually carries, and the memo the test
// wanted.
func TestAssertMemoVariantsFail(t *testing.T) {
	t.Parallel()

	text := func(s string) xdr.TransactionEnvelope { return envelopeWithMemo(t, textMemo(s)) }
	id := func(v uint64) xdr.TransactionEnvelope { return envelopeWithMemo(t, idMemo(v)) }
	hash := func(h xdr.Hash) xdr.TransactionEnvelope { return envelopeWithMemo(t, hashMemo(h)) }

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "wrong text value",
			call: func(tb focus.TB) bool { return focus.AssertMemoText(tb, text("goodbye"), "hello") },
			contains: []string{
				"AssertMemoText: text memo is \"goodbye\", want \"hello\"",
			},
		},
		{
			name: "id memo where text was wanted",
			call: func(tb focus.TB) bool { return focus.AssertMemoText(tb, id(42), "hello") },
			contains: []string{
				`the memo is an id memo (id: 42), want a text memo "hello"`,
			},
		},
		{
			name: "wrong id value",
			call: func(tb focus.TB) bool { return focus.AssertMemoID(tb, id(41), 42) },
			contains: []string{
				"AssertMemoID: id memo is 41, want 42",
			},
		},
		{
			name: "text memo where id was wanted",
			call: func(tb focus.TB) bool { return focus.AssertMemoID(tb, text("hello"), 42) },
			contains: []string{
				"the memo is a text memo (text: hello), want an id memo 42",
			},
		},
		{
			name: "wrong hash value",
			call: func(tb focus.TB) bool {
				return focus.AssertMemoHash(tb, hash(testHash), otherHash)
			},
			contains: []string{
				"AssertMemoHash: hash memo differs",
				"got   " + testHash.HexString(),
				"want  " + otherHash.HexString(),
			},
		},
		{
			name: "id memo where hash was wanted",
			call: func(tb focus.TB) bool {
				return focus.AssertMemoHash(tb, id(7), testHash)
			},
			contains: []string{
				"the memo is an id memo (id: 7), want a hash or return memo " + testHash.HexString(),
			},
		},
		{
			name: "wrong hash in a return memo",
			call: func(tb focus.TB) bool {
				return focus.AssertMemoHash(tb, envelopeWithMemo(t, returnMemo(otherHash)), testHash)
			},
			contains: []string{
				"AssertMemoHash: return memo differs",
				"got   " + otherHash.HexString(),
				"want  " + testHash.HexString(),
			},
		},
		{
			name: "text memo where the memo should be absent",
			call: func(tb focus.TB) bool { return focus.AssertNoMemo(tb, text("hello")) },
			contains: []string{
				"AssertNoMemo: the memo is a text memo (text: hello), want no memo",
			},
		},
		{
			name: "id memo where the memo should be absent",
			call: func(tb focus.TB) bool { return focus.AssertNoMemo(tb, id(42)) },
			contains: []string{
				"AssertNoMemo: the memo is an id memo (id: 42), want no memo",
			},
		},
		{
			name: "value that is not an envelope",
			call: func(tb focus.TB) bool { return focus.AssertMemoText(tb, 42, "hello") },
			contains: []string{
				"envelope value could not be read",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
				t.Fatal("assertion passed, want a failure")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestMemoAssertionsOnBadInput checks the standard input error for a
// non-envelope value and for a hash want of an unsupported type.
func TestMemoAssertionsOnBadInput(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if focus.AssertMemoTextEqual(tb, 42, "hello") {
		t.Fatal("assertion passed on a non-envelope value")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "envelope value could not be read") {
		t.Errorf("message does not name the bad input:\n%s", msg)
	}

	tb = faketb.New()
	if focus.AssertMemoHashEqual(tb, envelopeWithMemo(t, hashMemo(testHash)), 42) {
		t.Fatal("assertion passed on an unsupported hash want")
	}
	msg, err = tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "want could not be read as a hash") {
		t.Errorf("message does not name the bad want:\n%s", msg)
	}
}
