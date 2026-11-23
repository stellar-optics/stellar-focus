package focus

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	"github.com/stellar/go-stellar-sdk/strkey"
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

// AssertSignerWeight checks that opOrEntry (a SetOptionsOp, Operation,
// TransactionEnvelope, AccountEntry, LedgerEntry, or TransactionMeta) sets,
// updates, or removes signerKey with weight wantWeight.
//
// It matches signer keys across Ed25519 (G...), HashX (X...), PreAuthTx (T...),
// and signed payload (P...) signer types. If wantWeight == 0, it asserts that the
// signer was removed (weight 0 in SetOptionsOp, or absence from AccountEntry).
func AssertSignerWeight(t TestingT, opOrEntry any, signerKey string, wantWeight uint32, opts ...Option) bool {
	t.Helper()
	cfg := newConfig(opts...)
	assertion := cfg.assertionName("AssertSignerWeight")

	return SafeExec(t, assertion, func() bool {
		return checkSignerWeight(t, assertion, opOrEntry, signerKey, wantWeight)
	})
}

func checkSignerWeight(t TB, assertion string, opOrEntry any, signerKey string, wantWeight uint32) bool {
	t.Helper()
	if opOrEntry == nil {
		t.Errorf("%s: opOrEntry is nil", assertion)
		return false
	}
	switch v := opOrEntry.(type) {
	case xdr.SetOptionsOp:
		return extractSignerFromSetOptions(t, assertion, &v, signerKey, wantWeight)
	case *xdr.SetOptionsOp:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.SetOptionsOp", assertion)
			return false
		}
		return extractSignerFromSetOptions(t, assertion, v, signerKey, wantWeight)
	case xdr.Operation:
		if v.Body.Type == xdr.OperationTypeSetOptions && v.Body.SetOptionsOp != nil {
			return extractSignerFromSetOptions(t, assertion, v.Body.SetOptionsOp, signerKey, wantWeight)
		}
		t.Errorf("%s: operation is type %s, want SetOptions", assertion, operationTypeName(v.Body.Type))
		return false
	case *xdr.Operation:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.Operation", assertion)
			return false
		}
		return checkSignerWeight(t, assertion, *v, signerKey, wantWeight)
	case xdr.AccountEntry:
		return extractSignerFromAccountEntry(t, assertion, &v, signerKey, wantWeight)
	case *xdr.AccountEntry:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.AccountEntry", assertion)
			return false
		}
		return extractSignerFromAccountEntry(t, assertion, v, signerKey, wantWeight)
	case xdr.LedgerEntry:
		if v.Data.Type == xdr.LedgerEntryTypeAccount && v.Data.Account != nil {
			return extractSignerFromAccountEntry(t, assertion, v.Data.Account, signerKey, wantWeight)
		}
		t.Errorf("%s: ledger entry is type %s, want Account", assertion, v.Data.Type.String())
		return false
	case *xdr.LedgerEntry:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.LedgerEntry", assertion)
			return false
		}
		return checkSignerWeight(t, assertion, *v, signerKey, wantWeight)
	case xdr.TransactionEnvelope:
		return signersFromEnvelope(t, assertion, &v, signerKey, wantWeight)
	case *xdr.TransactionEnvelope:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.TransactionEnvelope", assertion)
			return false
		}
		return signersFromEnvelope(t, assertion, v, signerKey, wantWeight)
	case xdr.TransactionMeta:
		return signersFromMeta(t, assertion, v, signerKey, wantWeight)
	case *xdr.TransactionMeta:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.TransactionMeta", assertion)
			return false
		}
		return signersFromMeta(t, assertion, *v, signerKey, wantWeight)
	case *lens.Value:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *lens.Value", assertion)
			return false
		}
		return checkSignerWeight(t, assertion, v.Raw, signerKey, wantWeight)
	default:
		decoded, err := coerce(opOrEntry, "")
		if err != nil {
			t.Errorf("%s: opOrEntry could not be read: %v", assertion, err)
			return false
		}
		return checkSignerWeight(t, assertion, decoded.Raw, signerKey, wantWeight)
	}
}

func extractSignerFromSetOptions(t TB, assertion string, op *xdr.SetOptionsOp, signerKey string, wantWeight uint32) bool {
	t.Helper()
	if op.Signer == nil {
		t.Errorf("%s: signer %s is missing from SetOptionsOp (Signer is nil)",
			assertion, shorten(signerKey))
		return false
	}

	if !matchSignerKey(op.Signer.Key, signerKey) {
		gotKey, _ := op.Signer.Key.GetAddress()
		if gotKey == "" {
			gotKey = "unknown"
		}
		t.Errorf("%s: SetOptionsOp signer key is %s, want %s",
			assertion, shorten(gotKey), shorten(signerKey))
		return false
	}

	gotWeight := uint32(op.Signer.Weight)
	if gotWeight == wantWeight {
		return true
	}

	if wantWeight == 0 {
		t.Errorf("%s: signer %s was not removed: weight is %d, want 0",
			assertion, shorten(signerKey), gotWeight)
	} else if gotWeight == 0 {
		t.Errorf("%s: signer %s was removed (weight 0), want weight %d",
			assertion, shorten(signerKey), wantWeight)
	} else {
		t.Errorf("%s: signer %s weight is %d, want %d",
			assertion, shorten(signerKey), gotWeight, wantWeight)
	}
	return false
}

func extractSignerFromAccountEntry(t TB, assertion string, entry *xdr.AccountEntry, signerKey string, wantWeight uint32) bool {
	t.Helper()
	var (
		found     bool
		gotWeight uint32
	)
	for _, s := range entry.Signers {
		if matchSignerKey(s.Key, signerKey) {
			found = true
			gotWeight = uint32(s.Weight)
			break
		}
	}

	if !found {
		if wantWeight == 0 {
			return true
		}
		t.Errorf("%s: signer %s is missing from account entry (%d signers present)",
			assertion, shorten(signerKey), len(entry.Signers))
		return false
	}

	if gotWeight == wantWeight {
		return true
	}

	if wantWeight == 0 {
		t.Errorf("%s: signer %s was not removed from account entry: weight is %d, want 0",
			assertion, shorten(signerKey), gotWeight)
	} else {
		t.Errorf("%s: signer %s weight is %d, want %d",
			assertion, shorten(signerKey), gotWeight, wantWeight)
	}
	return false
}

func signersFromEnvelope(t TB, assertion string, env *xdr.TransactionEnvelope, signerKey string, wantWeight uint32) bool {
	t.Helper()
	ops, err := transactionOperations(env)
	if err != nil {
		t.Errorf("%s: could not read operations from envelope: %v", assertion, err)
		return false
	}
	var setOptionOps []*xdr.SetOptionsOp
	for i := range ops {
		if ops[i].Body.Type == xdr.OperationTypeSetOptions && ops[i].Body.SetOptionsOp != nil {
			setOptionOps = append(setOptionOps, ops[i].Body.SetOptionsOp)
		}
	}
	if len(setOptionOps) == 0 {
		t.Errorf("%s: transaction envelope carries no SetOptions operation", assertion)
		return false
	}
	for _, op := range setOptionOps {
		if op.Signer != nil && matchSignerKey(op.Signer.Key, signerKey) {
			return extractSignerFromSetOptions(t, assertion, op, signerKey, wantWeight)
		}
	}
	return extractSignerFromSetOptions(t, assertion, setOptionOps[0], signerKey, wantWeight)
}

func signersFromMeta(t TB, assertion string, meta xdr.TransactionMeta, signerKey string, wantWeight uint32) bool {
	t.Helper()
	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	for _, c := range changes {
		if c.entryType == xdr.LedgerEntryTypeAccount && c.entry != nil && c.entry.Data.Account != nil {
			return extractSignerFromAccountEntry(t, assertion, c.entry.Data.Account, signerKey, wantWeight)
		}
	}
	t.Errorf("%s: meta carries no account entry changes", assertion)
	return false
}

func matchSignerKey(key xdr.SignerKey, targetKey string) bool {
	targetKey = strings.TrimSpace(targetKey)
	if targetKey == "" {
		return false
	}

	// 1. Check address string from key
	addr, err := key.GetAddress()
	if err == nil {
		if addr == targetKey {
			return true
		}
		// If targetKey is a muxed account, decode to underlying G... address
		if strings.HasPrefix(targetKey, "M") {
			if vb, payload, err := strkey.DecodeAny(targetKey); err == nil && vb == strkey.VersionByteMuxedAccount && len(payload) >= 32 {
				if g, err := strkey.Encode(strkey.VersionByteAccountID, payload[:32]); err == nil && addr == g {
					return true
				}
			}
		}
	}

	// 2. Parse targetKey into SignerKey and compare with Equals
	var parsed xdr.SignerKey
	if err := parsed.SetAddress(targetKey); err == nil {
		if key.Equals(parsed) {
			return true
		}
	}

	// 3. Raw 64-char hex comparison for hash/keys
	if len(targetKey) == 64 {
		lowerHex := strings.ToLower(targetKey)
		switch key.Type {
		case xdr.SignerKeyTypeSignerKeyTypeEd25519:
			k := key.MustEd25519()
			if hex.EncodeToString(k[:]) == lowerHex {
				return true
			}
		case xdr.SignerKeyTypeSignerKeyTypeHashX:
			k := key.MustHashX()
			if hex.EncodeToString(k[:]) == lowerHex {
				return true
			}
		case xdr.SignerKeyTypeSignerKeyTypePreAuthTx:
			k := key.MustPreAuthTx()
			if hex.EncodeToString(k[:]) == lowerHex {
				return true
			}
		}
	}

	return false
}
