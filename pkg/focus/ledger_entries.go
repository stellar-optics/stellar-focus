package focus

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/stellar/go-stellar-sdk/amount"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Ledger entry assertions read the ledger changes a transaction carries in its
// meta: the entries it created, modified and deleted. An envelope assertion
// checks what the transaction was asked to do; these check what it did to the
// ledger, which is usually the property a test actually wants to pin down.
//
// The raw changes are flattened and sanitised once, in ledgerChanges, before
// any assertion looks at them. The generated XDR accessors panic when a union
// arm is missing, so all touching of generated types happens behind guard, and
// the assertions themselves only read the sanitised form.

// ledgerChange is one sanitised LedgerEntryChange.
//
// Identity is reduced to strings — a canonical key id, the account address and
// a canonical asset label — so the assertions can match without calling
// generated accessors, and so a failure listing can be rendered from data that
// was captured safely.
type ledgerChange struct {
	// kind is the protocol change type: created, updated, removed, state or
	// restored.
	kind xdr.LedgerEntryChangeType
	// entryType is the kind of ledger entry that changed.
	entryType xdr.LedgerEntryType
	// keyID is the canonical identity of the changed entry, derived from the
	// XDR encoding of its LedgerKey.
	keyID string
	// label describes the key for a human, such as
	// "trustline USDC:GA2J… on GCJN…".
	label string
	// account is the strkey account the entry belongs to, when it has one.
	account string
	// asset is the canonical trustline asset label for trustline changes.
	asset string
	// id is the entry's own identifier, for the entry kinds that have one
	// besides an account: a claimable balance id or liquidity pool id in hex,
	// an offer id in decimal, or a data entry's name.
	//
	// It is read from the key rather than the entry, so a removed entry —
	// which carries only a key — is still identifiable.
	id string
	// entry is the entry value after (or before, for a state change) the
	// change. It is nil for a removed entry, which carries only a key.
	entry *xdr.LedgerEntry
	// detail renders entry compactly for a failure listing.
	detail string
}

// ledgerKey is a resolved key argument: its canonical identity and a human
// description of it.
type ledgerKey struct {
	keyID string
	label string
}

// AssertEntryCreated checks that the transaction created the ledger entry
// identified by key.
//
// key accepts an xdr.LedgerKey, an xdr.LedgerEntry (whose key is used), or
// either as base64 XDR, raw bytes or a decoded *lens.Value. An entry that was
// restored from the Soroban archival state counts as created, since it is new
// to the live ledger.
func AssertEntryCreated(t TB, meta Value, key Value) bool {
	t.Helper()
	return assertEntryChange(t, "AssertEntryCreated", meta, key,
		xdr.LedgerEntryChangeTypeLedgerEntryCreated)
}

// AssertEntryModified checks that the transaction modified the ledger entry
// identified by key.
//
// It accepts the same key forms as AssertEntryCreated. A modification that
// only changed which entry is sponsored still counts; use
// AssertBalanceChanged or AssertTrustlineModified to pin down the fields that
// changed.
func AssertEntryModified(t TB, meta Value, key Value) bool {
	t.Helper()
	return assertEntryChange(t, "AssertEntryModified", meta, key,
		xdr.LedgerEntryChangeTypeLedgerEntryUpdated)
}

// AssertEntryDeleted checks that the transaction removed the ledger entry
// identified by key.
//
// It accepts the same key forms as AssertEntryCreated.
func AssertEntryDeleted(t TB, meta Value, key Value) bool {
	t.Helper()
	return assertEntryChange(t, "AssertEntryDeleted", meta, key,
		xdr.LedgerEntryChangeTypeLedgerEntryRemoved)
}

// assertEntryChange is the shared implementation behind the three ledger
// entry assertions.
func assertEntryChange(t TB, assertion string, meta, key Value, want xdr.LedgerEntryChangeType) bool {
	t.Helper()

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	wantKey, ok := resolveLedgerKey(t, assertion, key)
	if !ok {
		return false
	}

	for _, c := range changes {
		if c.keyID == wantKey.keyID && changeKindSatisfies(c.kind, want) {
			return true
		}
	}

	t.Errorf("%s: no ledger entry was %s for %s\n\n%s",
		assertion, changeVerb(want), wantKey.label, indent(describeChanges(changes), "  "))
	return false
}

// changeKindSatisfies reports whether a change of kind got satisfies an
// assertion looking for want.
//
// The one asymmetry is restoration: a restored entry is new to the live
// ledger, so it answers a creation assertion.
func changeKindSatisfies(got, want xdr.LedgerEntryChangeType) bool {
	if got == want {
		return true
	}
	return want == xdr.LedgerEntryChangeTypeLedgerEntryCreated &&
		got == xdr.LedgerEntryChangeTypeLedgerEntryRestored
}

// changeVerb names the change an assertion looks for, for failure messages.
func changeVerb(kind xdr.LedgerEntryChangeType) string {
	switch kind {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return "created"
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return "modified"
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		return "deleted"
	default:
		return "changed"
	}
}

// ledgerChanges decodes a transaction meta and returns every ledger entry
// change it carries, in the order the protocol applied them.
func ledgerChanges(t TB, assertion string, meta Value) ([]ledgerChange, bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "", meta, "TransactionMeta")
	if !ok {
		return nil, false
	}
	m, ok := decoded.Raw.(*xdr.TransactionMeta)
	if !ok {
		t.Errorf("%s: value is a %s, want a TransactionMeta; ledger changes live in transaction meta",
			assertion, decoded.Type)
		return nil, false
	}

	var out []ledgerChange
	if !guard(t, assertion, func() {
		out = sanitiseChanges(metaChanges(m))
	}) {
		return nil, false
	}
	return out, true
}

// metaChanges flattens the ledger entry changes of every meta version, in
// protocol order.
func metaChanges(m *xdr.TransactionMeta) []xdr.LedgerEntryChange {
	var out []xdr.LedgerEntryChange
	switch m.V {
	case 0:
		if m.Operations != nil {
			for _, op := range *m.Operations {
				out = append(out, op.Changes...)
			}
		}
	case 1:
		if m.V1 != nil {
			out = append(out, m.V1.TxChanges...)
			for _, op := range m.V1.Operations {
				out = append(out, op.Changes...)
			}
		}
	case 2:
		if m.V2 != nil {
			out = append(out, m.V2.TxChangesBefore...)
			for _, op := range m.V2.Operations {
				out = append(out, op.Changes...)
			}
			out = append(out, m.V2.TxChangesAfter...)
		}
	case 3:
		if m.V3 != nil {
			out = append(out, m.V3.TxChangesBefore...)
			for _, op := range m.V3.Operations {
				out = append(out, op.Changes...)
			}
			out = append(out, m.V3.TxChangesAfter...)
		}
	case 4:
		if m.V4 != nil {
			out = append(out, m.V4.TxChangesBefore...)
			for _, op := range m.V4.Operations {
				out = append(out, op.Changes...)
			}
			out = append(out, m.V4.TxChangesAfter...)
		}
	}
	return out
}

// sanitiseChanges converts raw changes into the form the assertions read.
// Changes whose key cannot be established are skipped rather than failing the
// whole assertion, since a single unknown arm should not hide the changes that
// are understandable.
func sanitiseChanges(raw []xdr.LedgerEntryChange) []ledgerChange {
	out := make([]ledgerChange, 0, len(raw))
	for _, ch := range raw {
		lc, ok := sanitiseChange(ch)
		if !ok {
			continue
		}
		out = append(out, lc)
	}
	return out
}

// sanitiseChange converts one raw change. It calls generated accessors, so it
// must run behind guard.
func sanitiseChange(ch xdr.LedgerEntryChange) (ledgerChange, bool) {
	key, err := ch.LedgerKey()
	if err != nil {
		return ledgerChange{}, false
	}
	id, err := ledgerKeyID(key)
	if err != nil {
		return ledgerChange{}, false
	}

	out := ledgerChange{
		kind:      ch.Type,
		entryType: key.Type,
		keyID:     id,
		label:     describeLedgerKey(key),
	}
	switch key.Type {
	case xdr.LedgerEntryTypeAccount:
		if k, ok := key.GetAccount(); ok {
			out.account = k.AccountId.Address()
		}
	case xdr.LedgerEntryTypeTrustline:
		if k, ok := key.GetTrustLine(); ok {
			out.account = k.AccountId.Address()
			out.asset = describeTrustLineAsset(k.Asset)
		}
	case xdr.LedgerEntryTypeOffer:
		if k, ok := key.GetOffer(); ok {
			out.account = k.SellerId.Address()
			out.id = strconv.FormatInt(int64(k.OfferId), 10)
		}
	case xdr.LedgerEntryTypeData:
		if k, ok := key.GetData(); ok {
			out.account = k.AccountId.Address()
			out.id = string(k.DataName)
		}
	case xdr.LedgerEntryTypeClaimableBalance:
		if k, ok := key.GetClaimableBalance(); ok {
			out.id = describeClaimableBalanceID(k.BalanceId)
		}
	case xdr.LedgerEntryTypeLiquidityPool:
		if k, ok := key.GetLiquidityPool(); ok {
			out.id = describePoolID(k.LiquidityPoolId)
		}
	}

	switch ch.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		out.entry = ch.Created
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		out.entry = ch.Updated
	case xdr.LedgerEntryChangeTypeLedgerEntryState:
		out.entry = ch.State
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		out.entry = ch.Restored
	}
	if out.entry != nil {
		out.detail = describeLedgerEntry(out.entry)
	}
	return out, true
}

// resolveLedgerKey coerces a key argument and reports a test failure when it
// cannot.
func resolveLedgerKey(t TB, assertion string, v Value) (ledgerKey, bool) {
	t.Helper()

	var (
		out ledgerKey
		err error
	)
	if !guard(t, assertion, func() {
		var key xdr.LedgerKey
		key, err = coerceLedgerKey(v)
		if err != nil {
			return
		}
		out.keyID, err = ledgerKeyID(key)
		if err != nil {
			return
		}
		out.label = describeLedgerKey(key)
	}) {
		return ledgerKey{}, false
	}
	if err != nil {
		t.Errorf("%s: key could not be read: %v", assertion, err)
		return ledgerKey{}, false
	}
	return out, true
}

// coerceLedgerKey accepts a LedgerKey or a LedgerEntry in every form this
// package usually takes: the xdr type, base64 XDR, raw bytes or a decoded
// *lens.Value.
func coerceLedgerKey(v Value) (xdr.LedgerKey, error) {
	switch val := v.(type) {
	case xdr.LedgerKey:
		return val, nil
	case *xdr.LedgerKey:
		if val == nil {
			return xdr.LedgerKey{}, fmt.Errorf("value is a nil *xdr.LedgerKey")
		}
		return *val, nil
	case xdr.LedgerEntry:
		return val.LedgerKey()
	case *xdr.LedgerEntry:
		if val == nil {
			return xdr.LedgerKey{}, fmt.Errorf("value is a nil *xdr.LedgerEntry")
		}
		return val.LedgerKey()
	case *lens.Value:
		if val == nil {
			return xdr.LedgerKey{}, fmt.Errorf("value is a nil *lens.Value")
		}
		switch val.Type {
		case "LedgerKey":
			key, ok := val.Raw.(*xdr.LedgerKey)
			if !ok {
				return xdr.LedgerKey{}, fmt.Errorf("decoded %s value does not hold a LedgerKey", val.Type)
			}
			return *key, nil
		case "LedgerEntry":
			entry, ok := val.Raw.(*xdr.LedgerEntry)
			if !ok {
				return xdr.LedgerKey{}, fmt.Errorf("decoded %s value does not hold a LedgerEntry", val.Type)
			}
			return entry.LedgerKey()
		default:
			return xdr.LedgerKey{}, fmt.Errorf(
				"value is a %s, want a LedgerKey or a LedgerEntry", val.Type)
		}
	default:
		decoded, err := coerce(v, "LedgerKey")
		if err != nil {
			return xdr.LedgerKey{}, fmt.Errorf(
				"%w; pass an xdr.LedgerKey, an xdr.LedgerEntry, or their base64 XDR", err)
		}
		key, ok := decoded.Raw.(*xdr.LedgerKey)
		if !ok {
			return xdr.LedgerKey{}, fmt.Errorf(
				"decoded %s value does not hold a LedgerKey", decoded.Type)
		}
		return *key, nil
	}
}

// ledgerKeyID reduces a key to a string that is equal exactly when the keys
// are, avoiding a deep comparison with its panicky generated accessors.
func ledgerKeyID(key xdr.LedgerKey) (string, error) {
	raw, err := key.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encoding the ledger key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// describeChanges renders the changes present, so a failed lookup shows what
// the transaction did instead of only what was wanted.
func describeChanges(changes []ledgerChange) string {
	if len(changes) == 0 {
		return "the transaction meta carries no ledger entry changes"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ledger changes present (%d):\n", len(changes))
	for i, c := range changes {
		fmt.Fprintf(&b, "  [%d] %-8s %s", i, changeKindLabel(c.kind), c.label)
		if c.detail != "" {
			fmt.Fprintf(&b, " — %s", c.detail)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeAccountChanges lists only the changes touching one account, so a
// balance or trustline failure names what happened to that account rather than
// to the whole ledger.
func describeAccountChanges(changes []ledgerChange, account string) string {
	matching := make([]ledgerChange, 0, len(changes))
	for _, c := range changes {
		if c.account == account {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return "no ledger entry change in this meta touches account " + shorten(account)
	}
	return describeChanges(matching)
}

// changeKindLabel names a change type for a failure listing.
func changeKindLabel(kind xdr.LedgerEntryChangeType) string {
	switch kind {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return "created"
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return "modified"
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		return "deleted"
	case xdr.LedgerEntryChangeTypeLedgerEntryState:
		return "before"
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		return "restored"
	default:
		return "changed"
	}
}

// describeLedgerKey renders a key in the terms a developer thinks in:
// "account G…", "trustline USDC:G… on G…", "offer G… #42".
func describeLedgerKey(key xdr.LedgerKey) string {
	switch key.Type {
	case xdr.LedgerEntryTypeAccount:
		if k, ok := key.GetAccount(); ok {
			return "account " + shorten(k.AccountId.Address())
		}
	case xdr.LedgerEntryTypeTrustline:
		if k, ok := key.GetTrustLine(); ok {
			return fmt.Sprintf("trustline %s on %s",
				describeTrustLineAsset(k.Asset), shorten(k.AccountId.Address()))
		}
	case xdr.LedgerEntryTypeOffer:
		if k, ok := key.GetOffer(); ok {
			return fmt.Sprintf("offer %s #%d", shorten(k.SellerId.Address()), k.OfferId)
		}
	case xdr.LedgerEntryTypeData:
		if k, ok := key.GetData(); ok {
			return fmt.Sprintf("data %q on %s", string(k.DataName), shorten(k.AccountId.Address()))
		}
	case xdr.LedgerEntryTypeClaimableBalance:
		if k, ok := key.GetClaimableBalance(); ok {
			return "claimable balance " + describeClaimableBalanceID(k.BalanceId)
		}
	case xdr.LedgerEntryTypeLiquidityPool:
		if k, ok := key.GetLiquidityPool(); ok {
			return "liquidity pool " + describePoolID(k.LiquidityPoolId)
		}
	case xdr.LedgerEntryTypeContractData:
		if k, ok := key.GetContractData(); ok {
			if addr, err := k.Contract.String(); err == nil {
				return "contract data for " + shorten(addr)
			}
			return "contract data"
		}
	case xdr.LedgerEntryTypeContractCode:
		if k, ok := key.GetContractCode(); ok {
			return "contract code " + hex.EncodeToString(k.Hash[:])
		}
	case xdr.LedgerEntryTypeConfigSetting:
		if k, ok := key.GetConfigSetting(); ok {
			return "config setting " + k.ConfigSettingId.String()
		}
	case xdr.LedgerEntryTypeTtl:
		if k, ok := key.GetTtl(); ok {
			return "ttl " + hex.EncodeToString(k.KeyHash[:])
		}
	}
	return strings.TrimPrefix(key.Type.String(), "LedgerEntryType")
}

// describeClaimableBalanceID renders a claimable balance id, falling back to
// hex when the union holds an unexpected arm.
func describeClaimableBalanceID(id xdr.ClaimableBalanceId) string {
	if v0, ok := id.GetV0(); ok {
		return v0.HexString()
	}
	return id.Type.String()
}

// describeLedgerEntry summarises an entry's state for a failure listing. It is
// deliberately short: the key already says which entry this is.
func describeLedgerEntry(e *xdr.LedgerEntry) string {
	switch e.Data.Type {
	case xdr.LedgerEntryTypeAccount:
		if a := e.Data.Account; a != nil {
			return fmt.Sprintf("balance %s, %d signer(s)",
				amountStroops(a.Balance, " XLM"), len(a.Signers))
		}
	case xdr.LedgerEntryTypeTrustline:
		if tl := e.Data.TrustLine; tl != nil {
			return fmt.Sprintf("balance %s, limit %s",
				amountStroops(tl.Balance, ""), amountStroops(tl.Limit, ""))
		}
	case xdr.LedgerEntryTypeOffer:
		if o := e.Data.Offer; o != nil {
			return fmt.Sprintf("selling %s, buying %s, amount %s",
				o.Selling.StringCanonical(), o.Buying.StringCanonical(), amountStroops(o.Amount, ""))
		}
	case xdr.LedgerEntryTypeData:
		if d := e.Data.Data; d != nil {
			return fmt.Sprintf("value %d byte(s)", len(d.DataValue))
		}
	case xdr.LedgerEntryTypeClaimableBalance:
		if cb := e.Data.ClaimableBalance; cb != nil {
			return "amount " + amountStroops(cb.Amount, "")
		}
	case xdr.LedgerEntryTypeTtl:
		if ttl := e.Data.Ttl; ttl != nil {
			return fmt.Sprintf("live until ledger %d", ttl.LiveUntilLedgerSeq)
		}
	}
	return ""
}

// describeTrustLineAsset renders a trustline asset canonically, so it can be
// compared with a queried asset label as a string.
func describeTrustLineAsset(a xdr.TrustLineAsset) string {
	switch a.Type {
	case xdr.AssetTypeAssetTypeNative:
		return "native"
	case xdr.AssetTypeAssetTypeCreditAlphanum4, xdr.AssetTypeAssetTypeCreditAlphanum12:
		return a.ToAsset().StringCanonical()
	case xdr.AssetTypeAssetTypePoolShare:
		if a.LiquidityPoolId != nil {
			return "pool:" + describePoolID(*a.LiquidityPoolId)
		}
		return "pool share"
	default:
		return fmt.Sprintf("asset type %d", a.Type)
	}
}

// describePoolID renders a liquidity pool id, matching the hex form lens
// shows for the same value.
func describePoolID(id xdr.PoolId) string {
	return hex.EncodeToString(id[:])
}

// amountStroops renders a stroop amount as both the raw integer and the
// seven-decimal form developers read, matching the style used elsewhere in
// the failure messages.
func amountStroops(v xdr.Int64, unit string) string {
	return fmt.Sprintf("%d (%s%s)", int64(v), amount.String(v), unit)
}
