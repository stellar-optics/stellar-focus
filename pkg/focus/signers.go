package focus

import (
	"encoding/hex"
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

// AssertHashSigner checks that opOrEntry carries a HashX (SHA-256 preimage)
// signer with the wanted hash and weight.
//
// HashX signers are how atomic swaps and payment channels name a signer by the
// hash of a preimage rather than by an ed25519 public key. The comparison is
// on the 32-byte hash itself: the strkey form ("X…") is derived from it, but
// a test that holds the hash should not have to encode a strkey to look for
// its own signer.
//
// opOrEntry accepts a set-options operation or an account entry:
//
//   - an xdr.SetOptionsOp or *xdr.SetOptionsOp (its optional signer)
//   - an xdr.Operation or *xdr.Operation holding a set-options operation
//   - an xdr.TransactionEnvelope or *xdr.TransactionEnvelope, whose set-options
//     operations are searched
//   - an xdr.AccountEntry or *xdr.AccountEntry
//   - an xdr.LedgerEntry or *xdr.LedgerEntry holding an account
//   - an xdr.TransactionMeta or *xdr.TransactionMeta, whose account entries
//     are searched
//   - an xdr.Signer, *xdr.Signer or []xdr.Signer
//   - any of the base64, []byte or *lens.Value forms those accept
//
// A weight of zero means the signer was removed, the protocol's own
// convention, so it is compared like any other weight. Hashes are printed in
// full hexadecimal in failure messages.
func AssertHashSigner(t TestingT, opOrEntry any, hash [32]byte, wantWeight uint32, opts ...Option) bool {
	t.Helper()
	return assertSignerKey(t, "AssertHashSigner", opOrEntry, xdr.SignerKeyTypeSignerKeyTypeHashX,
		"hash-x", hash, wantWeight, opts...)
}

// AssertPreAuthTxSigner checks that opOrEntry carries a PreAuthTx signer with
// the wanted transaction hash and weight.
//
// A PreAuthTx signer authorizes a specific transaction by its hash, which is
// what a payment channel uses to let the counterparty submit a pre-signed
// transaction later. The hash compared is the 32-byte transaction hash, not
// its "T…" strkey.
//
// opOrEntry accepts the same forms as AssertHashSigner. Hashes are printed in
// full hexadecimal in failure messages.
func AssertPreAuthTxSigner(t TestingT, opOrEntry any, txHash [32]byte, wantWeight uint32, opts ...Option) bool {
	t.Helper()
	return assertSignerKey(t, "AssertPreAuthTxSigner", opOrEntry, xdr.SignerKeyTypeSignerKeyTypePreAuthTx,
		"pre-auth-tx", txHash, wantWeight, opts...)
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

// signerKeyMatch is one signer of a HashX or PreAuthTx kind, reduced to the
// hash and weight the assertions compare.
type signerKeyMatch struct {
	hash   [32]byte
	weight uint32
}

// assertSignerKey is the shared implementation behind AssertHashSigner and
// AssertPreAuthTxSigner.
//
// noun is the human name of the signer kind, "hash-x" or "pre-auth-tx", and
// keyType is the union discriminant it corresponds to. Sharing the
// implementation keeps the two failure messages, and the accepted inputs,
// identical.
func assertSignerKey(
	t TB, assertion string, opOrEntry any, keyType xdr.SignerKeyType,
	noun string, wantHash [32]byte, wantWeight uint32, opts ...Option,
) bool {
	t.Helper()

	name := newConfig(opts...).assertionName(assertion)
	signers, ok := signersFromOpOrEntry(t, name, opOrEntry)
	if !ok {
		return false
	}

	wantHex := hex.EncodeToString(wantHash[:])
	matches, ok := signerKeysOfType(t, name, signers, keyType)
	if !ok {
		return false
	}

	var found *signerKeyMatch
	for i := range matches {
		if matches[i].hash == wantHash {
			found = &matches[i]
			break
		}
	}
	if found == nil {
		t.Errorf("%s: no %s signer with hash %s, want weight %d\n\n%s",
			name, noun, wantHex, wantWeight, indent(describeSignerKeys(signers)))
		return false
	}
	if found.weight != wantWeight {
		t.Errorf("%s: %s signer %s weight is %d, want %d\n\n%s",
			name, noun, wantHex, found.weight, wantWeight, indent(describeSignerKeys(signers)))
		return false
	}
	return true
}

// signerKeysOfType reduces the signers of one key kind to their hashes and
// weights.
//
// A signer of another kind is not an error: a set-options operation may also
// carry ed25519 signers, and the wanted one is simply absent. Malformed union
// arms are reported rather than skipped, because a signer that cannot be read
// is not a signer that was not there.
func signerKeysOfType(
	t TB, assertion string, signers []xdr.Signer, keyType xdr.SignerKeyType,
) ([]signerKeyMatch, bool) {
	t.Helper()

	var (
		matches []signerKeyMatch
		err     error
	)
	if !guard(t, assertion, func() {
		for i, s := range signers {
			if s.Key.Type != keyType {
				continue
			}
			hash, ok := signerKeyHash(s.Key)
			if !ok {
				err = fmt.Errorf("signer %d has no %s hash", i, signerKeyTypeName(keyType))
				return
			}
			matches = append(matches, signerKeyMatch{hash: hash, weight: uint32(s.Weight)})
		}
	}) {
		return nil, false
	}
	if err != nil {
		t.Errorf("%s: could not read the signers: %v", assertion, err)
		return nil, false
	}
	return matches, true
}

// signerKeyHash reads the 32-byte hash of a HashX or PreAuthTx signer key. It
// calls generated accessors, so it must run behind guard.
func signerKeyHash(key xdr.SignerKey) ([32]byte, bool) {
	var out [32]byte
	switch key.Type {
	case xdr.SignerKeyTypeSignerKeyTypePreAuthTx:
		hash, ok := key.GetPreAuthTx()
		if !ok {
			return out, false
		}
		copy(out[:], hash[:])
		return out, true
	case xdr.SignerKeyTypeSignerKeyTypeHashX:
		hash, ok := key.GetHashX()
		if !ok {
			return out, false
		}
		copy(out[:], hash[:])
		return out, true
	default:
		return out, false
	}
}

// signerKeyTypeName names a signer key kind the way the protocol's own tooling
// does, so a failure listing is readable.
func signerKeyTypeName(t xdr.SignerKeyType) string {
	switch t {
	case xdr.SignerKeyTypeSignerKeyTypeEd25519:
		return "ed25519"
	case xdr.SignerKeyTypeSignerKeyTypePreAuthTx:
		return "pre-auth-tx"
	case xdr.SignerKeyTypeSignerKeyTypeHashX:
		return "hash-x"
	case xdr.SignerKeyTypeSignerKeyTypeEd25519SignedPayload:
		return "signed-payload"
	default:
		return fmt.Sprintf("signer type %d", int32(t))
	}
}

// describeSignerKeys lists every signer found, with hashes in full hex. A
// failure that only says the wanted hash is absent leaves the reader unable to
// see which signer was actually there, which is usually the thing worth
// knowing.
func describeSignerKeys(signers []xdr.Signer) string {
	if len(signers) == 0 {
		return "signers present: none"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "signers present (%d):\n", len(signers))
	for i, s := range signers {
		fmt.Fprintf(&b, "  [%d] %s weight %d\n", i, describeSignerKey(s.Key), uint32(s.Weight))
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeSignerKey renders one signer key, spelling HashX and PreAuthTx keys
// as the full hexadecimal hash the assertions compare.
//
// The generated accessors panic on a missing union arm, and this runs while a
// failure is already being reported, so a malformed key is rendered as
// unreadable rather than taking down the test binary.
func describeSignerKey(key xdr.SignerKey) (out string) {
	out = signerKeyTypeName(key.Type) + " (unreadable)"
	defer func() {
		if recover() != nil {
			out = signerKeyTypeName(key.Type) + " (unreadable)"
		}
	}()

	name := signerKeyTypeName(key.Type)
	switch key.Type {
	case xdr.SignerKeyTypeSignerKeyTypeHashX, xdr.SignerKeyTypeSignerKeyTypePreAuthTx:
		if hash, ok := signerKeyHash(key); ok {
			return name + " " + hex.EncodeToString(hash[:])
		}
		return name + " (unreadable)"
	default:
		addr, err := key.GetAddress()
		if err != nil || addr == "" {
			return name + " (unreadable)"
		}
		return name + " " + shorten(addr)
	}
}

// signersFromOpOrEntry extracts the signers an assertion should search, from
// the forms a test holds a set-options operation or an account entry in.
func signersFromOpOrEntry(t TB, assertion string, opOrEntry any) ([]xdr.Signer, bool) {
	t.Helper()

	if opOrEntry == nil {
		t.Errorf("%s: opOrEntry is nil", assertion)
		return nil, false
	}

	switch v := opOrEntry.(type) {
	case xdr.Signer:
		return []xdr.Signer{v}, true
	case *xdr.Signer:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.Signer", assertion)
			return nil, false
		}
		return []xdr.Signer{*v}, true
	case []xdr.Signer:
		return v, true
	case xdr.SetOptionsOp:
		return signersOfSetOptions(&v), true
	case *xdr.SetOptionsOp:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.SetOptionsOp", assertion)
			return nil, false
		}
		return signersOfSetOptions(v), true
	case xdr.Operation:
		return signersOfOperation(t, assertion, v)
	case *xdr.Operation:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.Operation", assertion)
			return nil, false
		}
		return signersOfOperation(t, assertion, *v)
	case xdr.AccountEntry:
		return v.Signers, true
	case *xdr.AccountEntry:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.AccountEntry", assertion)
			return nil, false
		}
		return v.Signers, true
	case xdr.LedgerEntry:
		return signersOfLedgerEntry(t, assertion, v)
	case *xdr.LedgerEntry:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.LedgerEntry", assertion)
			return nil, false
		}
		return signersOfLedgerEntry(t, assertion, *v)
	case xdr.TransactionEnvelope:
		return signersOfEnvelope(t, assertion, &v)
	case *xdr.TransactionEnvelope:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.TransactionEnvelope", assertion)
			return nil, false
		}
		return signersOfEnvelope(t, assertion, v)
	case xdr.TransactionMeta:
		return signersOfMeta(t, assertion, &v)
	case *xdr.TransactionMeta:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.TransactionMeta", assertion)
			return nil, false
		}
		return signersOfMeta(t, assertion, v)
	default:
		decoded, err := coerce(opOrEntry, "")
		if err == nil && decoded != nil && decoded.Raw != nil {
			switch raw := decoded.Raw.(type) {
			case *xdr.SetOptionsOp:
				return signersOfSetOptions(raw), true
			case *xdr.Operation:
				return signersOfOperation(t, assertion, *raw)
			case *xdr.AccountEntry:
				if raw == nil {
					t.Errorf("%s: value holds a nil account entry", assertion)
					return nil, false
				}
				return raw.Signers, true
			case *xdr.LedgerEntry:
				return signersOfLedgerEntry(t, assertion, *raw)
			case *xdr.TransactionEnvelope:
				return signersOfEnvelope(t, assertion, raw)
			case *xdr.TransactionMeta:
				return signersOfMeta(t, assertion, raw)
			case *xdr.Signer:
				return []xdr.Signer{*raw}, true
			}
		}
		t.Errorf("%s: unsupported opOrEntry type %T; expected a SetOptionsOp, an Operation, "+
			"a TransactionEnvelope, an AccountEntry, a LedgerEntry, a TransactionMeta, "+
			"a Signer or a []Signer", assertion, opOrEntry)
		return nil, false
	}
}

// signersOfSetOptions reads the optional signer of a set-options operation.
func signersOfSetOptions(op *xdr.SetOptionsOp) []xdr.Signer {
	if op == nil || op.Signer == nil {
		return nil
	}
	return []xdr.Signer{*op.Signer}
}

// signersOfOperation reads the signer of a set-options operation.
func signersOfOperation(t TB, assertion string, op xdr.Operation) ([]xdr.Signer, bool) {
	t.Helper()
	if op.Body.Type != xdr.OperationTypeSetOptions {
		t.Errorf("%s: operation is type %s, want SetOptions", assertion, op.Body.Type.String())
		return nil, false
	}
	return signersOfSetOptions(op.Body.SetOptionsOp), true
}

// signersOfLedgerEntry reads the signers of an account ledger entry.
func signersOfLedgerEntry(t TB, assertion string, entry xdr.LedgerEntry) ([]xdr.Signer, bool) {
	t.Helper()
	if entry.Data.Type != xdr.LedgerEntryTypeAccount || entry.Data.Account == nil {
		t.Errorf("%s: ledger entry is type %s, want Account", assertion, entry.Data.Type.String())
		return nil, false
	}
	return entry.Data.Account.Signers, true
}

// signersOfEnvelope reads the signers of every set-options operation in an
// envelope, including the inner transaction of a fee bump.
func signersOfEnvelope(t TB, assertion string, env *xdr.TransactionEnvelope) ([]xdr.Signer, bool) {
	t.Helper()

	var (
		signers []xdr.Signer
		found   bool
	)
	if !guard(t, assertion, func() {
		for _, op := range signerEnvelopeOperations(env) {
			if op.Body.Type != xdr.OperationTypeSetOptions {
				continue
			}
			found = true
			signers = append(signers, signersOfSetOptions(op.Body.SetOptionsOp)...)
		}
	}) {
		return nil, false
	}
	if !found {
		t.Errorf("%s: envelope contains no SetOptions operation", assertion)
		return nil, false
	}
	return signers, true
}

// signerEnvelopeOperations returns the operations of an envelope, reading
// through a fee-bump wrapper. It calls generated accessors, so it must run
// behind guard.
func signerEnvelopeOperations(env *xdr.TransactionEnvelope) []xdr.Operation {
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		if env.V0 != nil {
			return env.V0.Tx.Operations
		}
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if env.V1 != nil {
			return env.V1.Tx.Operations
		}
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump != nil && env.FeeBump.Tx.InnerTx.V1 != nil {
			return env.FeeBump.Tx.InnerTx.V1.Tx.Operations
		}
	}
	return nil
}

// signersOfMeta reads the signers of every account entry a transaction meta
// created, updated or restored. It calls generated accessors, so it must run
// behind guard.
func signersOfMeta(t TB, assertion string, meta *xdr.TransactionMeta) ([]xdr.Signer, bool) {
	t.Helper()

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return nil, false
	}
	var signers []xdr.Signer
	if !guard(t, assertion, func() {
		for _, c := range changes {
			if c.entry == nil || c.entry.Data.Type != xdr.LedgerEntryTypeAccount ||
				c.entry.Data.Account == nil {
				continue
			}
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated,
				xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				signers = append(signers, c.entry.Data.Account.Signers...)
			}
		}
	}) {
		return nil, false
	}
	return signers, true
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
