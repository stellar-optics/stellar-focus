package focus

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Data entry assertions read the arbitrary key-value storage a manage-data
// operation keeps on an account: one DataEntry per name, holding up to 64
// bytes of opaque value.
//
// What the envelope carries is the operation that was submitted; what these
// read is the entry the ledger ended up with. The difference matters because
// manage-data is last-write-wins and value-free deletion shares one operation
// with assignment: a transaction can set a name twice, or set and then delete
// it, and only the meta says which value survived.
//
// A data value is opaque bytes, so it is compared as bytes and rendered as
// text only where the bytes are printable. A failure names the first byte that
// differs, because the common case — a trailing newline, a padded field, the
// wrong one of two similar values — is invisible in a side-by-side of two
// quoted strings.

// dataEntry is the state of one account data entry after a transaction.
type dataEntry struct {
	// account is the strkey address the entry belongs to.
	account string
	// name is the entry's key, as stored.
	name string
	// value is the entry's value after the transaction. It is nil when the
	// entry was removed.
	value []byte
	// present reports whether the entry exists after the transaction. A
	// manage-data operation with no value deletes the entry.
	present bool
	// created reports that the transaction added the entry rather than
	// changing one that already existed.
	created bool
}

// AssertAccountDataEqual checks that the transaction left an account data
// entry named key holding value.
//
// value accepts a string, compared as its UTF-8 bytes, a []byte compared as
// itself, or an xdr.DataValue. A data value is opaque to the protocol, so
// there is no XDR coercion here: a string argument is the value, never base64
// to decode.
//
// The key is matched exactly, as the protocol stores it. Only entries that
// exist after the transaction are considered, so a name that was written and
// then deleted does not satisfy this; use AssertAccountDataDeleted for that.
//
// This assertion does not name an account, mirroring the manage-data
// vocabulary where a test usually knows there is only one account writing
// data. Where more than one account has an entry with that name, any of them
// holding the value satisfies the assertion; AssertAccountDataEqualFor pins
// down which account is meant.
func AssertAccountDataEqual(t TB, meta Value, key string, value Value) bool {
	t.Helper()
	const assertion = "AssertAccountDataEqual"

	entries, want, ok := dataEntriesAndValue(t, assertion, meta, value)
	if !ok {
		return false
	}

	candidates := presentEntriesNamed(entries, key)
	if len(candidates) == 0 {
		t.Errorf("%s: the transaction left no account data named %q\n\n%s",
			assertion, key, indent(describeDataEntries(entries)))
		return false
	}
	for _, e := range candidates {
		if bytes.Equal(e.value, want) {
			return true
		}
	}

	if len(candidates) == 1 {
		t.Errorf("%s", dataMismatchMessage(assertion, candidates[0], want, entries))
		return false
	}
	t.Errorf("%s: %d accounts have data named %q, none holding %s\n\n%s",
		assertion, len(candidates), key, renderDataValue(want),
		indent(describeDataEntries(candidates)))
	return false
}

// AssertAccountDataEqualFor is AssertAccountDataEqual scoped to one account.
//
// account accepts a strkey address, an xdr.AccountId or an xdr.MuxedAccount;
// a muxed account is reduced to the underlying G… account, since data entries
// are keyed by that.
func AssertAccountDataEqualFor(t TB, meta Value, account Value, key string, value Value) bool {
	t.Helper()
	const assertion = "AssertAccountDataEqualFor"

	entries, want, ok := dataEntriesAndValue(t, assertion, meta, value)
	if !ok {
		return false
	}
	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}

	for _, e := range entries {
		if e.account != addr || e.name != key || !e.present {
			continue
		}
		if bytes.Equal(e.value, want) {
			return true
		}
		t.Errorf("%s", dataMismatchMessage(assertion, e, want, entries))
		return false
	}

	t.Errorf("%s: the transaction left no data named %q on account %s\n\n%s",
		assertion, key, shorten(addr), indent(describeAccountDataEntries(entries, addr)))
	return false
}

// AssertAccountDataDeleted checks that the transaction removed the account
// data entry named key, which is what a manage-data operation with no value
// does.
//
// An entry that was deleted and then written again in the same transaction is
// not deleted: the assertion reads the state the transaction committed, not
// every state it passed through.
func AssertAccountDataDeleted(t TB, meta Value, key string) bool {
	t.Helper()
	const assertion = "AssertAccountDataDeleted"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	entries, ok := accountDataEntries(t, assertion, changes)
	if !ok {
		return false
	}

	var touched bool
	for _, e := range entries {
		if e.name != key {
			continue
		}
		if !e.present {
			return true
		}
		touched = true
	}

	if touched {
		t.Errorf("%s: account data %q still exists after this transaction\n\n%s",
			assertion, key, indent(describeDataEntries(entries)))
		return false
	}
	t.Errorf("%s: no account data named %q was removed by this transaction\n\n%s",
		assertion, key, indent(describeDataEntries(entries)))
	return false
}

// dataEntriesAndValue resolves the two inputs the value assertions share.
func dataEntriesAndValue(t TB, assertion string, meta, value Value) ([]dataEntry, []byte, bool) {
	t.Helper()

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return nil, nil, false
	}
	entries, ok := accountDataEntries(t, assertion, changes)
	if !ok {
		return nil, nil, false
	}
	want, ok := resolveDataValue(t, assertion, value)
	if !ok {
		return nil, nil, false
	}
	return entries, want, true
}

// accountDataEntries reduces a meta's changes to the state of every data
// entry the transaction touched, in the order the entries were first touched.
//
// Later changes to a key replace earlier ones, so what comes back is the state
// the transaction committed. A state change — the value before an update — is
// skipped rather than mistaken for something the transaction wrote.
func accountDataEntries(t TB, assertion string, changes []ledgerChange) ([]dataEntry, bool) {
	var (
		order []string
		byKey = make(map[string]*dataEntry)
	)
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeData {
				continue
			}
			e, seen := byKey[c.keyID]
			if !seen {
				e = &dataEntry{account: c.account, name: c.id}
				byKey[c.keyID] = e
				order = append(order, c.keyID)
			}

			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				// The entry existed before this transaction, so a later
				// write to it is a change rather than a creation.
				e.created = false
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				if v, has := dataEntryValue(c.entry); has {
					e.value, e.present = v, true
					e.created = !seen
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				if v, has := dataEntryValue(c.entry); has {
					e.value, e.present = v, true
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				e.value, e.present = nil, false
			}
		}
	})
	if !ok {
		return nil, false
	}

	out := make([]dataEntry, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out, true
}

// dataEntryValue reads a data entry's value, reporting whether the entry is
// of the kind that carries one.
func dataEntryValue(entry *xdr.LedgerEntry) ([]byte, bool) {
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeData || entry.Data.Data == nil {
		return nil, false
	}
	// Copy, so the returned state does not alias the decoded XDR.
	return append([]byte(nil), entry.Data.Data.DataValue...), true
}

// presentEntriesNamed returns the entries with this name that exist after the
// transaction.
func presentEntriesNamed(entries []dataEntry, name string) []dataEntry {
	out := make([]dataEntry, 0, len(entries))
	for _, e := range entries {
		if e.name == name && e.present {
			out = append(out, e)
		}
	}
	return out
}

// resolveDataValue coerces a data value argument and reports a test failure
// when it cannot.
func resolveDataValue(t TB, assertion string, v Value) ([]byte, bool) {
	t.Helper()

	switch val := v.(type) {
	case string:
		return []byte(val), true
	case []byte:
		return val, true
	case xdr.DataValue:
		return val, true
	case *xdr.DataValue:
		if val == nil {
			t.Errorf("%s: value could not be read: value is a nil *xdr.DataValue", assertion)
			return nil, false
		}
		return *val, true
	default:
		t.Errorf("%s: value could not be read: unsupported type %T; "+
			"a data value is opaque bytes — pass a string, a []byte or an xdr.DataValue",
			assertion, v)
		return nil, false
	}
}

// dataMismatchMessage renders a value mismatch for one entry: the headline,
// the first byte that differs, and the data the transaction wrote.
func dataMismatchMessage(assertion string, got dataEntry, want []byte, all []dataEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: account data %q on account %s is %s, want %s\n\n",
		assertion, got.name, shorten(got.account),
		renderDataValue(got.value), renderDataValue(want))
	fmt.Fprintf(&b, "  %s\n", describeDataDifference(got.value, want))
	b.WriteString(indent(describeDataEntries(all)))
	return b.String()
}

// describeDataDifference names the first byte that differs, or the difference
// in length when one value is a prefix of the other.
func describeDataDifference(got, want []byte) string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			return fmt.Sprintf("first difference at byte %d: got %s, want %s",
				i, renderByte(got[i]), renderByte(want[i]))
		}
	}
	switch {
	case len(got) > len(want):
		return fmt.Sprintf("the value found is %d byte(s) longer, from byte %d: %s",
			len(got)-len(want), n, renderDataValue(got[n:]))
	case len(want) > len(got):
		return fmt.Sprintf("the value found is %d byte(s) shorter, missing from byte %d: %s",
			len(want)-len(got), n, renderDataValue(want[n:]))
	default:
		return "the values are equal as bytes"
	}
}

// renderByte shows a byte as hex, with its character where it is printable.
func renderByte(b byte) string {
	if b < utf8.RuneSelf && unicode.IsPrint(rune(b)) {
		return fmt.Sprintf("%#02x (%q)", b, rune(b))
	}
	return fmt.Sprintf("%#02x", b)
}

// maxInlineDataValue is how many bytes of a data value are shown before
// truncation. A data entry holds at most 64, so this shows most values whole
// while keeping a listing of several entries readable.
const maxInlineDataValue = 32

// renderDataValue renders a data value as quoted text where its bytes are
// printable and as hex where they are not, always with its length, since an
// invisible trailing byte is exactly the difference worth naming.
func renderDataValue(v []byte) string {
	if len(v) == 0 {
		return "empty (0 bytes)"
	}

	shown, elided := v, 0
	if len(shown) > maxInlineDataValue {
		shown, elided = v[:maxInlineDataValue], len(v)-maxInlineDataValue
	}

	var rendered string
	if printableText(shown) {
		rendered = fmt.Sprintf("%q", string(shown))
	} else {
		rendered = "0x" + hex.EncodeToString(shown)
	}
	if elided > 0 {
		rendered += fmt.Sprintf("… (+%d bytes)", elided)
	}
	return fmt.Sprintf("%s (%d bytes)", rendered, len(v))
}

// printableText reports whether bytes are text, the case where quoting reads
// better than hex.
//
// Tab, newline and carriage return count as text although they are not
// printable runes: a quoted string escapes them visibly, and a value whose
// only oddity is a trailing newline is exactly the one that must not be
// turned into a wall of hex.
func printableText(v []byte) bool {
	if !utf8.Valid(v) {
		return false
	}
	for _, r := range string(v) {
		if unicode.IsPrint(r) || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		return false
	}
	return true
}

// describeDataEntries lists the data entries a transaction wrote, so a failed
// lookup shows what it did write.
func describeDataEntries(entries []dataEntry) string {
	if len(entries) == 0 {
		return "the transaction wrote no account data"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "account data written (%d):\n", len(entries))
	for i, e := range entries {
		switch {
		case !e.present:
			fmt.Fprintf(&b, "  [%d] deleted %q on %s\n", i, e.name, shorten(e.account))
		case e.created:
			fmt.Fprintf(&b, "  [%d] created %q on %s = %s\n",
				i, e.name, shorten(e.account), renderDataValue(e.value))
		default:
			fmt.Fprintf(&b, "  [%d] set     %q on %s = %s\n",
				i, e.name, shorten(e.account), renderDataValue(e.value))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeAccountDataEntries lists only the data entries of one account, so a
// scoped failure names what that account wrote rather than what every account
// did.
func describeAccountDataEntries(entries []dataEntry, account string) string {
	matching := make([]dataEntry, 0, len(entries))
	for _, e := range entries {
		if e.account == account {
			matching = append(matching, e)
		}
	}
	if len(matching) == 0 {
		return "this transaction writes no account data for " + shorten(account)
	}
	return describeDataEntries(matching)
}
