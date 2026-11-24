package focus

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Signer and threshold assertions inspect the account entry a transaction left
// behind: the signers it added or removed, the weight each signer carries, and
// the four signing thresholds. They are the meta-side counterpart of
// inspecting set-options operations in the envelope.
//
// All of them require the transaction to have changed the account entry. A
// transaction that does not touch an account carries no state for it, so
// there is nothing to assert on — the failure says so rather than guessing.

// Thresholds are an account's four signing thresholds.
//
// The zero value means every threshold is zero, which is what a freshly
// created account has.
type Thresholds struct {
	// Master is the weight of the account's master key.
	Master byte
	// Low is the threshold for low-security operations.
	Low byte
	// Medium is the threshold for medium-security operations.
	Medium byte
	// High is the threshold for high-security operations.
	High byte
}

// AssertSignersEqual checks that the transaction left an account with exactly
// the wanted signers and weights.
//
// want maps each signer's strkey address to its weight; a nil map asserts that
// the account has no signers. Order does not matter — signers are a set — but
// the count, keys and weights all do. The master key is not part of the signer
// list; its weight is a threshold, checked by AssertThresholdsEqual.
//
// account accepts a strkey address, an xdr.AccountId or an xdr.MuxedAccount.
func AssertSignersEqual(t TB, meta Value, account Value, want map[string]uint32) bool {
	t.Helper()
	const assertion = "AssertSignersEqual"

	entry, addr, ok := accountEntryFrom(t, assertion, meta, account, "signers")
	if !ok {
		return false
	}
	got, ok := accountSigners(t, assertion, entry)
	if !ok {
		return false
	}
	if signersEqual(got, want) {
		return true
	}

	lines := signerDiffLines(got, want)
	t.Errorf("%s: account %s signers differ — %s\n\n  %s\n\n%s",
		assertion, shorten(addr), pluralChanges(len(lines)),
		strings.Join(lines, "\n  "), indent(describeSigners(got)))
	return false
}

// AssertThresholdsEqual checks that the transaction left an account with the
// wanted signing thresholds.
//
// want accepts a Thresholds, an xdr.Thresholds or a [4]byte, in the protocol's
// order: master weight first, then low, medium and high. A one-signer test
// usually cares only about the master weight and passes zero for the rest.
//
// account accepts a strkey address, an xdr.AccountId or an xdr.MuxedAccount.
func AssertThresholdsEqual(t TB, meta Value, account Value, want Value) bool {
	t.Helper()
	const assertion = "AssertThresholdsEqual"

	wantThresholds, err := thresholdsFrom(want)
	if err != nil {
		t.Errorf("%s: want could not be read as thresholds: %v", assertion, err)
		return false
	}

	entry, addr, ok := accountEntryFrom(t, assertion, meta, account, "thresholds")
	if !ok {
		return false
	}
	got, ok := accountThresholds(t, assertion, entry)
	if !ok {
		return false
	}
	if got == wantThresholds {
		return true
	}

	t.Errorf("%s: account %s thresholds differ\n\n  %s\n\n  got  %s\n  want %s",
		assertion, shorten(addr),
		strings.Join(thresholdDiffLines(got, wantThresholds), "\n  "),
		describeThresholds(got), describeThresholds(wantThresholds))
	return false
}

// accountEntryFrom resolves a meta and account and returns the account entry
// the transaction left behind, which is the last created, modified or restored
// account entry for that account.
//
// noun names what could not be read, so the failure explains the missing
// entry in terms of the assertion's subject.
func accountEntryFrom(
	t TB, assertion string, meta, account Value, noun string,
) (*xdr.LedgerEntry, string, bool) {
	t.Helper()

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return nil, "", false
	}
	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return nil, "", false
	}

	var entry *xdr.LedgerEntry
	if !guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeAccount || c.account != addr {
				continue
			}
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated,
				xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				entry = c.entry
			}
		}
	}) {
		return nil, "", false
	}
	if entry == nil {
		t.Errorf("%s: this meta carries no account entry for %s, so its %s cannot be read from it\n\n%s",
			assertion, shorten(addr), noun, indent(describeAccountChanges(changes, addr)))
		return nil, "", false
	}
	return entry, addr, true
}

// accountSigner is a signer reduced to its strkey and weight, so comparisons
// and failure messages never touch the generated signer-key union.
type accountSigner struct {
	key    string
	weight uint32
}

// accountSigners reads an account entry's signers.
func accountSigners(t TB, assertion string, entry *xdr.LedgerEntry) ([]accountSigner, bool) {
	var (
		signers []accountSigner
		err     error
	)
	if !guard(t, assertion, func() { signers, err = signersFrom(entry) }) {
		return nil, false
	}
	if err != nil {
		t.Errorf("%s: could not read the account's signers: %v", assertion, err)
		return nil, false
	}
	return signers, true
}

// signersFrom extracts signers from an account entry. It calls generated
// accessors, so it must run behind guard.
func signersFrom(entry *xdr.LedgerEntry) ([]accountSigner, error) {
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeAccount || entry.Data.Account == nil {
		return nil, fmt.Errorf("ledger entry is not an account")
	}

	signers := entry.Data.Account.Signers
	out := make([]accountSigner, 0, len(signers))
	for i := range signers {
		key, err := signers[i].Key.GetAddress()
		if err != nil {
			return nil, fmt.Errorf("signer %d: %w", i, err)
		}
		out = append(out, accountSigner{key: key, weight: uint32(signers[i].Weight)})
	}
	return out, nil
}

// signersEqual reports whether the signers found are exactly the wanted set.
func signersEqual(got []accountSigner, want map[string]uint32) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, s := range got {
		if seen[s.key] {
			// A duplicate cannot satisfy a set with the same size and no
			// duplicate, so one wanted key must be missing.
			return false
		}
		seen[s.key] = true
		weight, ok := want[s.key]
		if !ok || weight != s.weight {
			return false
		}
	}
	return true
}

// signerDiffLines renders the difference between the signers found and the
// wanted ones, one line per signer, in a stable order.
func signerDiffLines(got []accountSigner, want map[string]uint32) []string {
	gotWeights := make(map[string]uint32, len(got))
	for _, s := range got {
		gotWeights[s.key] = s.weight
	}

	keys := make([]string, 0, len(gotWeights)+len(want))
	for k := range gotWeights {
		keys = append(keys, k)
	}
	for k := range want {
		if _, ok := gotWeights[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var lines []string
	for _, k := range keys {
		g, hasGot := gotWeights[k]
		w, hasWant := want[k]
		switch {
		case hasGot && hasWant && g != w:
			lines = append(lines, fmt.Sprintf("~ %s weight is %d, want %d", shorten(k), g, w))
		case hasGot && !hasWant:
			lines = append(lines, fmt.Sprintf("- %s weight %d", shorten(k), g))
		case !hasGot && hasWant:
			lines = append(lines, fmt.Sprintf("+ %s weight %d", shorten(k), w))
		}
	}
	return lines
}

// describeSigners renders the signers the account ended with, so a failure
// shows the whole set rather than only the entries that moved.
func describeSigners(signers []accountSigner) string {
	if len(signers) == 0 {
		return "signers after the transaction: none"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "signers after the transaction (%d):\n", len(signers))
	for i, s := range signers {
		fmt.Fprintf(&b, "  [%d] %s weight %d\n", i, shorten(s.key), s.weight)
	}
	return strings.TrimRight(b.String(), "\n")
}

// accountThresholds reads an account entry's thresholds.
func accountThresholds(t TB, assertion string, entry *xdr.LedgerEntry) (Thresholds, bool) {
	var out Thresholds
	ok := guard(t, assertion, func() {
		if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeAccount || entry.Data.Account == nil {
			return
		}
		out = thresholdValues(entry.Data.Account.Thresholds)
	})
	if !ok {
		return Thresholds{}, false
	}
	return out, true
}

// thresholdsFrom accepts the forms a caller may hold thresholds in.
func thresholdsFrom(v Value) (Thresholds, error) {
	switch val := v.(type) {
	case Thresholds:
		return val, nil
	case xdr.Thresholds:
		return thresholdValues(val), nil
	case [4]byte:
		return thresholdValues(xdr.Thresholds(val)), nil
	default:
		return Thresholds{}, fmt.Errorf(
			"unsupported type %T; expected a focus.Thresholds, an xdr.Thresholds or a [4]byte", v)
	}
}

func thresholdValues(t xdr.Thresholds) Thresholds {
	return Thresholds{Master: t[0], Low: t[1], Medium: t[2], High: t[3]}
}

// thresholdDiffLines names each threshold that differs, so a failure does not
// make the reader subtract four pairs of numbers.
func thresholdDiffLines(got, want Thresholds) []string {
	var lines []string
	if got.Master != want.Master {
		lines = append(lines, fmt.Sprintf("master weight is %d, want %d", got.Master, want.Master))
	}
	if got.Low != want.Low {
		lines = append(lines, fmt.Sprintf("low threshold is %d, want %d", got.Low, want.Low))
	}
	if got.Medium != want.Medium {
		lines = append(lines, fmt.Sprintf("medium threshold is %d, want %d", got.Medium, want.Medium))
	}
	if got.High != want.High {
		lines = append(lines, fmt.Sprintf("high threshold is %d, want %d", got.High, want.High))
	}
	return lines
}

// describeThresholds renders all four thresholds on one line.
func describeThresholds(t Thresholds) string {
	return fmt.Sprintf("master %d  low %d  medium %d  high %d", t.Master, t.Low, t.Medium, t.High)
}
