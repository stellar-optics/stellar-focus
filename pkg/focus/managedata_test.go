package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func TestAssertAccountDataEqual(t *testing.T) {
	t.Parallel()

	entry := dataLedgerEntryValue(t, testDest, "config", []byte("v2"))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)})

	tests := []struct {
		name  string
		value focus.Value
	}{
		{"a string is compared as its bytes", "v2"},
		{"a byte slice", []byte("v2")},
		{"an xdr data value", xdr.DataValue("v2")},
		{"a pointer to an xdr data value", func() focus.Value {
			v := xdr.DataValue("v2")
			return &v
		}()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertAccountDataEqual(tb, meta, "config", tc.value) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("a value written over an existing one", func(t *testing.T) {
		t.Parallel()

		before := dataLedgerEntryValue(t, testDest, "config", []byte("v1"))
		tb := faketb.New()
		if !focus.AssertAccountDataEqual(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(before), updatedChange(entry)}), "config", "v2") {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("the last write of the transaction wins", func(t *testing.T) {
		t.Parallel()

		final := dataLedgerEntryValue(t, testDest, "config", []byte("v3"))
		m := metaV2(t,
			[]xdr.LedgerEntryChange{createdChange(entry)},
			[]xdr.LedgerEntryChange{stateChange(entry), updatedChange(final)})

		tb := faketb.New()
		if !focus.AssertAccountDataEqual(tb, m, "config", "v3") {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		stale := faketb.New()
		if focus.AssertAccountDataEqual(stale, m, "config", "v2") {
			t.Error("assertion passed against a value the transaction overwrote")
		}
	})

	t.Run("binary and empty values", func(t *testing.T) {
		t.Parallel()

		binary := dataLedgerEntryValue(t, testDest, "blob", []byte{0x00, 0xff, 0x10})
		empty := dataLedgerEntryValue(t, testDest, "empty", []byte{})
		m := metaV2(t, []xdr.LedgerEntryChange{createdChange(binary), createdChange(empty)})

		tb := faketb.New()
		if !focus.AssertAccountDataEqual(tb, m, "blob", []byte{0x00, 0xff, 0x10}) {
			t.Errorf("binary value: assertion failed unexpectedly:\n%s", tb.Output())
		}
		tb = faketb.New()
		if !focus.AssertAccountDataEqual(tb, m, "empty", "") {
			t.Errorf("empty value: assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("any of two accounts with the same name satisfies it", func(t *testing.T) {
		t.Parallel()

		mine := dataLedgerEntryValue(t, testSource, "config", []byte("mine"))
		theirs := dataLedgerEntryValue(t, testDest, "config", []byte("theirs"))
		m := metaV2(t, []xdr.LedgerEntryChange{createdChange(mine), createdChange(theirs)})

		for _, want := range []string{"mine", "theirs"} {
			tb := faketb.New()
			if !focus.AssertAccountDataEqual(tb, m, "config", want) {
				t.Errorf("value %q: assertion failed unexpectedly:\n%s", want, tb.Output())
			}
		}
	})
}

func TestAssertAccountDataEqualFailureMessages(t *testing.T) {
	t.Parallel()

	entry := dataLedgerEntryValue(t, testDest, "config", []byte("v2"))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)})

	t.Run("a wrong value names the first differing byte", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb, meta, "config", "v3") {
			t.Fatal("assertion passed with the wrong value")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`AssertAccountDataEqual: account data "config" on account GAPB7…WYXZ`,
			`is "v2" (2 bytes), want "v3" (2 bytes)`,
			`first difference at byte 1: got 0x32 ('2'), want 0x33 ('3')`,
			`account data written (1):`,
			`created "config" on GAPB7…WYXZ = "v2" (2 bytes)`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a trailing byte is named rather than hidden", func(t *testing.T) {
		t.Parallel()

		padded := dataLedgerEntryValue(t, testDest, "config", []byte("v2\n"))
		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(padded)}), "config", "v2") {
			t.Fatal("assertion passed with a trailing newline")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`is "v2\n" (3 bytes), want "v2" (2 bytes)`,
			"the value found is 1 byte(s) longer, from byte 2",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a missing trailing byte is named too", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb, meta, "config", "v2\n") {
			t.Fatal("assertion passed with a short value")
		}
		if msg := tb.Output(); !strings.Contains(msg,
			"the value found is 1 byte(s) shorter, missing from byte 2") {
			t.Errorf("message does not name the missing byte:\n%s", msg)
		}
	})

	t.Run("a binary value is rendered as hex", func(t *testing.T) {
		t.Parallel()

		binary := dataLedgerEntryValue(t, testDest, "blob", []byte{0x00, 0xff})
		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(binary)}), "blob", []byte{0x00, 0xfe}) {
			t.Fatal("assertion passed with the wrong bytes")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"is 0x00ff (2 bytes), want 0x00fe (2 bytes)",
			"first difference at byte 1: got 0xff, want 0xfe",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a long value is truncated with its length kept", func(t *testing.T) {
		t.Parallel()

		long := strings.Repeat("a", 64)
		longEntry := dataLedgerEntryValue(t, testDest, "long", []byte(long))
		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(longEntry)}), "long", "short") {
			t.Fatal("assertion passed with the wrong value")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "… (+32 bytes)") || !strings.Contains(msg, "(64 bytes)") {
			t.Errorf("message does not truncate the long value with its length:\n%s", msg)
		}
	})

	t.Run("an absent name lists what was written", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb, meta, "missing", "v2") {
			t.Fatal("assertion passed for a name that was never written")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`the transaction left no account data named "missing"`,
			`created "config" on GAPB7…WYXZ`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a name that was deleted does not satisfy the value check", func(t *testing.T) {
		t.Parallel()

		key := ledgerKeyOf(t, entry)
		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(entry), removedChange(key)}),
			"config", "v2") {
			t.Fatal("assertion passed for a deleted entry")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`the transaction left no account data named "config"`,
			`deleted "config" on GAPB7…WYXZ`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a meta with no data entries at all", func(t *testing.T) {
		t.Parallel()

		account := accountEntryValue(t, testSource, 100, xdr.Thresholds{1, 1, 1, 1})
		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb,
			metaV2(t, []xdr.LedgerEntryChange{updatedChange(account)}), "config", "v2") {
			t.Fatal("assertion passed with no data entry in the meta")
		}
		if msg := tb.Output(); !strings.Contains(msg, "the transaction wrote no account data") {
			t.Errorf("message does not say the meta wrote no data:\n%s", msg)
		}
	})

	t.Run("several accounts with the name and none matching", func(t *testing.T) {
		t.Parallel()

		mine := dataLedgerEntryValue(t, testSource, "config", []byte("mine"))
		theirs := dataLedgerEntryValue(t, testDest, "config", []byte("theirs"))
		tb := faketb.New()
		if focus.AssertAccountDataEqual(tb, metaV2(t,
			[]xdr.LedgerEntryChange{createdChange(mine), createdChange(theirs)}),
			"config", "other") {
			t.Fatal("assertion passed with no matching value")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`2 accounts have data named "config", none holding "other" (5 bytes)`,
			`created "config" on GCJNH…ZST6 = "mine" (4 bytes)`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an unsupported value type is an input error", func(t *testing.T) {
		t.Parallel()

		for _, v := range []focus.Value{42, struct{}{}, (*xdr.DataValue)(nil), nil} {
			tb := faketb.New()
			if focus.AssertAccountDataEqual(tb, meta, "config", v) {
				t.Fatalf("assertion passed with value %#v", v)
			}
			if msg := tb.Output(); !strings.Contains(msg, "value could not be read") {
				t.Errorf("message does not name the bad input for %#v:\n%s", v, msg)
			}
		}
	})
}

func TestAssertAccountDataEqualFor(t *testing.T) {
	t.Parallel()

	mine := dataLedgerEntryValue(t, testSource, "config", []byte("mine"))
	theirs := dataLedgerEntryValue(t, testDest, "config", []byte("theirs"))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(mine), createdChange(theirs)})

	t.Run("each account is checked against its own entry", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			account focus.Value
			value   string
		}{
			{testSource, "mine"},
			{testDest, "theirs"},
			{mustAccountID(t, testSource), "mine"},
			{mustMuxed(t, testDest), "theirs"},
		}
		for _, tc := range cases {
			tb := faketb.New()
			if !focus.AssertAccountDataEqualFor(tb, meta, tc.account, "config", tc.value) {
				t.Errorf("assertion failed unexpectedly for %v:\n%s", tc.account, tb.Output())
			}
		}
	})

	t.Run("the other account's value does not satisfy it", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataEqualFor(tb, meta, testSource, "config", "theirs") {
			t.Fatal("assertion passed against another account's value")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`AssertAccountDataEqualFor: account data "config" on account GCJNH…ZST6`,
			`is "mine" (4 bytes), want "theirs" (6 bytes)`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an account with no entry of that name", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataEqualFor(tb, meta, testIssuer, "config", "mine") {
			t.Fatal("assertion passed for an account that wrote nothing")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`the transaction left no data named "config" on account GA2JR…HVAW`,
			"this transaction writes no account data for GA2JR…HVAW",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an account with other names lists only its own entries", func(t *testing.T) {
		t.Parallel()

		other := dataLedgerEntryValue(t, testSource, "other", []byte("x"))
		tb := faketb.New()
		if focus.AssertAccountDataEqualFor(tb, metaV2(t,
			[]xdr.LedgerEntryChange{createdChange(other), createdChange(theirs)}),
			testSource, "config", "mine") {
			t.Fatal("assertion passed for a name the account did not write")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, `created "other" on GCJNH…ZST6`) {
			t.Errorf("message does not list the account's own entries:\n%s", msg)
		}
		if strings.Contains(msg, "GAPB7…WYXZ") {
			t.Errorf("message lists another account's entries:\n%s", msg)
		}
	})

	t.Run("a bad account is an input error", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataEqualFor(tb, meta, "nope", "config", "mine") {
			t.Fatal("assertion passed with an unreadable account")
		}
		if msg := tb.Output(); !strings.Contains(msg, "account could not be read as an account") {
			t.Errorf("message does not name the bad input:\n%s", msg)
		}
	})
}

func TestAssertAccountDataDeleted(t *testing.T) {
	t.Parallel()

	entry := dataLedgerEntryValue(t, testDest, "config", []byte("v2"))
	key := ledgerKeyOf(t, entry)
	deleted := metaV2(t, []xdr.LedgerEntryChange{stateChange(entry), removedChange(key)})

	t.Run("a removed entry", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertAccountDataDeleted(tb, deleted, "config") {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("an entry still present", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataDeleted(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)}), "config") {
			t.Fatal("assertion passed for an entry that was created")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			`account data "config" still exists after this transaction`,
			`created "config" on GAPB7…WYXZ = "v2" (2 bytes)`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an entry deleted and written again is not deleted", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataDeleted(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(entry), removedChange(key)},
			[]xdr.LedgerEntryChange{createdChange(entry)}), "config") {
			t.Fatal("assertion passed for an entry that was written again")
		}
		if msg := tb.Output(); !strings.Contains(msg, "still exists after this transaction") {
			t.Errorf("message does not report the entry as present:\n%s", msg)
		}
	})

	t.Run("a name that was never touched", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataDeleted(tb, deleted, "missing") {
			t.Fatal("assertion passed for a name the transaction never touched")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, `no account data named "missing" was removed by this transaction`) {
			t.Errorf("message does not report the absent name:\n%s", msg)
		}
	})

	t.Run("a non-meta value is reported as such", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertAccountDataDeleted(tb, buildEnvelope(t, paymentOp(t, 1)), "config") {
			t.Fatal("assertion passed with an envelope")
		}
		if tb.Output() == "" {
			t.Error("no failure was reported for an envelope")
		}
	})
}

// TestAccountDataValueRendering covers the two forms a value is rendered in:
// quoted text, including whitespace, and hex for anything that is not text.
func TestAccountDataValueRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value []byte
		want  string
	}{
		{"printable text", []byte("hello"), `"hello" (5 bytes)`},
		{"text with a tab", []byte("a\tb"), `"a\tb" (3 bytes)`},
		{"a control byte is not text", []byte{0x01, 0x02}, "0x0102 (2 bytes)"},
		{"invalid utf-8 is not text", []byte{0xff, 0xfe}, "0xfffe (2 bytes)"},
		{"an empty value", []byte{}, "empty (0 bytes)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entry := dataLedgerEntryValue(t, testDest, "k", tc.value)
			tb := faketb.New()
			// A deliberately wrong expectation is the route to the listing.
			if focus.AssertAccountDataEqual(tb,
				metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)}), "k", "\x00mismatch") {
				t.Fatal("assertion passed with the wrong value")
			}
			if !strings.Contains(tb.Output(), tc.want) {
				t.Errorf("value is not rendered as %q:\n%s", tc.want, tb.Output())
			}
		})
	}
}
