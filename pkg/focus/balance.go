package focus

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// AssertBalanceChanged checks whether a transaction changed an account's
// balance in an asset by exactly delta stroops.
//
// The delta is the net change across every ledger change in the meta, so it
// includes the transaction-level changes that carry the fee and sequence
// number. For the transaction's source account a payment of 5 XLM with a
// 100-stroop fee therefore shows a delta of -50000100. Pass a delta of zero to
// assert that the account's balance was left alone.
//
// account accepts a strkey address, an xdr.AccountId or an xdr.MuxedAccount.
// asset accepts "native" (or "XLM"), a "CODE:ISSUER" string, an xdr.Asset, an
// xdr.TrustLineAsset, an xdr.ChangeTrustAsset, or the XDR of one of those as
// base64, raw bytes or a decoded *lens.Value.
//
// A balance change is observed in an account entry for the native asset and in
// a trustline entry for anything else; an asset the account has no trustline
// for has a delta of zero.
func AssertBalanceChanged(t TB, meta Value, account Value, asset Value, delta int64) bool {
	t.Helper()
	const assertion = "AssertBalanceChanged"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}

	got, ok := balanceDelta(t, assertion, changes, addr, spec)
	if !ok {
		return false
	}
	if got == delta {
		return true
	}

	t.Errorf("%s: %s balance changed by %s, want %s\n\n%s",
		assertion, describeBalanceSubject(addr, spec),
		amountStroops(xdr.Int64(got), spec.unit()),
		amountStroops(xdr.Int64(delta), spec.unit()),
		indent(describeAccountChanges(changes, addr)))
	return false
}

// AssertBalanceChangeRange checks that a transaction changed an account's
// balance in an asset by somewhere between minDelta and maxDelta stroops,
// inclusive.
//
// It accepts the same account, asset and meta forms as AssertBalanceChanged,
// and computes the delta the same way: the net change across every ledger
// change in the meta. Bounds are inclusive, so passing the same value for
// both is an exact-delta assertion.
//
// Use it where dynamic fees or DEX slippage make an exact delta flaky: a
// payment of 100 XLM with a fee that can land anywhere between 0 and 200
// stroops is bounded by [-100000200, -99999800] for the sender. A zero bound
// is an ordinary bound, not an unchecked side — a delta of zero passes only
// when it falls within [minDelta, maxDelta].
func AssertBalanceChangeRange(t TB, meta, account, asset Value, minDelta, maxDelta int64) bool {
	t.Helper()
	const assertion = "AssertBalanceChangeRange"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}

	got, ok := balanceDelta(t, assertion, changes, addr, spec)
	if !ok {
		return false
	}
	if got < minDelta || got > maxDelta {
		t.Errorf("%s: %s balance changed by %s, outside [%s, %s]\n\n%s",
			assertion, describeBalanceSubject(addr, spec),
			amountStroops(xdr.Int64(got), spec.unit()),
			amountStroops(xdr.Int64(minDelta), spec.unit()),
			amountStroops(xdr.Int64(maxDelta), spec.unit()),
			indent(describeAccountChanges(changes, addr)))
		return false
	}
	return true
}

// balanceDelta sums the balance movements for one account and asset across a
// meta's changes.
//
// The protocol emits a state change before the first update to an entry and
// then update changes as the transaction proceeds, so the before value of each
// update comes from the most recent state or update seen for that key. An
// entry created from nothing starts at zero; a removed entry's final balance
// comes from the state change that precedes its removal.
func balanceDelta(t TB, assertion string, changes []ledgerChange, account string, spec assetSpec) (int64, bool) {
	var (
		delta int64
		state = make(map[string]int64)
	)
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if !spec.matches(c, account) {
				continue
			}
			balance, has := entryBalance(c.entry, spec)

			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if has {
					state[c.keyID] = balance
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				if has {
					delta += balance
					state[c.keyID] = balance
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				if has {
					delta += balance - state[c.keyID]
					state[c.keyID] = balance
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				// A removed entry carries only a key, so its final balance is
				// the last state recorded for it.
				if before, seen := state[c.keyID]; seen {
					delta -= before
					delete(state, c.keyID)
				}
			}
		}
	})
	if !ok {
		return 0, false
	}
	return delta, true
}

// entryBalance reads the balance of an entry for the queried asset, reporting
// whether the entry is of the kind that carries one.
func entryBalance(entry *xdr.LedgerEntry, spec assetSpec) (int64, bool) {
	if entry == nil {
		return 0, false
	}
	if spec.native {
		if entry.Data.Type != xdr.LedgerEntryTypeAccount || entry.Data.Account == nil {
			return 0, false
		}
		return int64(entry.Data.Account.Balance), true
	}
	if entry.Data.Type != xdr.LedgerEntryTypeTrustline || entry.Data.TrustLine == nil {
		return 0, false
	}
	return int64(entry.Data.TrustLine.Balance), true
}

// assetSpec is a resolved asset query: either the native asset or a specific
// credit or pool-share asset, reduced to a canonical label so matching a
// ledger change is a string comparison.
type assetSpec struct {
	native bool
	// id is the canonical identity, matching the label captured from a
	// trustline change: "native", "USDC:G…" or "pool:<hex>".
	id string
}

// unit names the asset in a rendered amount, where that reads naturally.
func (s assetSpec) unit() string {
	if s.native {
		return " XLM"
	}
	return ""
}

// matches reports whether a change concerns this asset for this account.
func (s assetSpec) matches(c ledgerChange, account string) bool {
	if c.account != account {
		return false
	}
	if s.native {
		return c.entryType == xdr.LedgerEntryTypeAccount
	}
	return c.entryType == xdr.LedgerEntryTypeTrustline && c.asset == s.id
}

// describeBalanceSubject names the account and asset a balance assertion is
// about, for the failure headline.
func describeBalanceSubject(account string, spec assetSpec) string {
	if spec.native {
		return fmt.Sprintf("account %s native", shorten(account))
	}
	return fmt.Sprintf("account %s (%s)", shorten(account), spec.id)
}

// resolveAccount coerces an account argument and reports a test failure when
// it cannot.
func resolveAccount(t TB, assertion, label string, v Value) (string, bool) {
	t.Helper()

	var (
		addr string
		err  error
	)
	if !guard(t, assertion, func() { addr, err = accountAddress(v) }) {
		return "", false
	}
	if err != nil {
		t.Errorf("%s: %s could not be read as an account: %v", assertion, label, err)
		return "", false
	}
	return addr, true
}

// accountAddress accepts an account in the forms callers hold it in and
// returns its strkey address.
//
// A muxed account is reduced to the underlying account, because ledger entries
// are keyed by the G… address, never the muxed M… one.
func accountAddress(v Value) (string, error) {
	switch val := v.(type) {
	case string:
		return addressFromString(val)
	case xdr.AccountId:
		// GetAddress has a pointer receiver, so take the address of a copy.
		aid := val
		return aid.GetAddress()
	case *xdr.AccountId:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.AccountId")
		}
		return val.GetAddress()
	case xdr.MuxedAccount:
		aid := val.ToAccountId()
		return aid.GetAddress()
	case *xdr.MuxedAccount:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.MuxedAccount")
		}
		aid := val.ToAccountId()
		return aid.GetAddress()
	default:
		return "", fmt.Errorf(
			"unsupported type %T; expected a strkey address, an xdr.AccountId or an xdr.MuxedAccount", v)
	}
}

// addressFromString accepts a G… or M… strkey and returns the G… account the
// ledger uses.
func addressFromString(s string) (string, error) {
	trimmed := strings.TrimSpace(s)
	vb, err := strkey.Version(trimmed)
	if err != nil {
		return "", fmt.Errorf("%q is not a strkey account address: %w", s, err)
	}
	switch vb {
	case strkey.VersionByteAccountID:
		return trimmed, nil
	case strkey.VersionByteMuxedAccount:
		m, err := xdr.AddressToMuxedAccount(trimmed)
		if err != nil {
			return "", fmt.Errorf("%q is not a valid muxed address: %w", s, err)
		}
		aid := m.ToAccountId()
		return aid.GetAddress()
	default:
		return "", fmt.Errorf("%q is not an account address (strkey version %d)", s, vb)
	}
}

// resolveAsset coerces an asset argument and reports a test failure when it
// cannot.
func resolveAsset(t TB, assertion, label string, v Value) (assetSpec, bool) {
	t.Helper()

	var (
		spec assetSpec
		err  error
	)
	if !guard(t, assertion, func() { spec, err = assetSpecFrom(v) }) {
		return assetSpec{}, false
	}
	if err != nil {
		t.Errorf("%s: %s could not be read as an asset: %v", assertion, label, err)
		return assetSpec{}, false
	}
	return spec, true
}

// assetSpecFrom accepts an asset in the forms callers hold it in.
func assetSpecFrom(v Value) (assetSpec, error) {
	switch val := v.(type) {
	case string:
		return assetSpecFromString(val)
	case xdr.Asset:
		return assetSpecFromAsset(val)
	case *xdr.Asset:
		if val == nil {
			return assetSpec{}, fmt.Errorf("value is a nil *xdr.Asset")
		}
		return assetSpecFromAsset(*val)
	case xdr.TrustLineAsset:
		return assetSpecFromTrustLineAsset(val)
	case *xdr.TrustLineAsset:
		if val == nil {
			return assetSpec{}, fmt.Errorf("value is a nil *xdr.TrustLineAsset")
		}
		return assetSpecFromTrustLineAsset(*val)
	case xdr.ChangeTrustAsset:
		return assetSpecFromChangeTrustAsset(val)
	case *xdr.ChangeTrustAsset:
		if val == nil {
			return assetSpec{}, fmt.Errorf("value is a nil *xdr.ChangeTrustAsset")
		}
		return assetSpecFromChangeTrustAsset(*val)
	case *lens.Value:
		if val == nil {
			return assetSpec{}, fmt.Errorf("value is a nil *lens.Value")
		}
		if val.Type != "Asset" {
			return assetSpec{}, fmt.Errorf("value is a %s, want an Asset", val.Type)
		}
		a, ok := val.Raw.(*xdr.Asset)
		if !ok {
			return assetSpec{}, fmt.Errorf("decoded %s value does not hold an Asset", val.Type)
		}
		return assetSpecFromAsset(*a)
	default:
		decoded, err := coerce(v, "Asset")
		if err != nil {
			return assetSpec{}, fmt.Errorf(
				"%w; pass an xdr.Asset, an xdr.TrustLineAsset, \"native\", \"CODE:ISSUER\" or \"pool:<id>\"", err)
		}
		a, ok := decoded.Raw.(*xdr.Asset)
		if !ok {
			return assetSpec{}, fmt.Errorf("decoded %s value does not hold an Asset", decoded.Type)
		}
		return assetSpecFromAsset(*a)
	}
}

// assetSpecFromString accepts "native", "XLM", a canonical "CODE:ISSUER" or a
// pool share as "pool:<id>", the form failure messages render pool shares in.
func assetSpecFromString(s string) (assetSpec, error) {
	trimmed := strings.TrimSpace(s)
	switch strings.ToLower(trimmed) {
	case "native", "xlm":
		return nativeAssetSpec(), nil
	}
	if !strings.Contains(trimmed, ":") {
		return assetSpec{}, fmt.Errorf("%q is not an asset; use \"native\" or \"CODE:ISSUER\"", s)
	}

	parts := strings.SplitN(trimmed, ":", 2)
	issuer, err := xdr.AddressToAccountId(parts[1])
	if err == nil {
		var a xdr.Asset
		if err := a.SetCredit(parts[0], issuer); err != nil {
			return assetSpec{}, fmt.Errorf("invalid asset %q: %w", s, err)
		}
		return assetSpecFromAsset(a)
	}

	// An asset code may legitimately be "pool", so only the issuer failing to
	// parse makes this a pool id.
	if strings.EqualFold(parts[0], "pool") {
		raw, err := hex.DecodeString(parts[1])
		if err != nil || len(raw) != len(xdr.PoolId{}) {
			return assetSpec{}, fmt.Errorf(
				"invalid liquidity pool id in %q; want pool:<64 hex characters>", s)
		}
		var id xdr.PoolId
		copy(id[:], raw)
		return poolAssetSpec(id), nil
	}
	return assetSpec{}, fmt.Errorf("invalid issuer in %q: %w", s, err)
}

// assetSpecFromAsset converts a credit asset, or the native one.
func assetSpecFromAsset(a xdr.Asset) (assetSpec, error) {
	if a.Type == xdr.AssetTypeAssetTypeNative {
		return nativeAssetSpec(), nil
	}
	return assetSpec{id: a.StringCanonical()}, nil
}

// assetSpecFromTrustLineAsset converts a trustline asset, including pool
// shares, which a plain Asset cannot represent.
func assetSpecFromTrustLineAsset(a xdr.TrustLineAsset) (assetSpec, error) {
	switch a.Type {
	case xdr.AssetTypeAssetTypeNative:
		return nativeAssetSpec(), nil
	case xdr.AssetTypeAssetTypeCreditAlphanum4, xdr.AssetTypeAssetTypeCreditAlphanum12:
		return assetSpecFromAsset(a.ToAsset())
	case xdr.AssetTypeAssetTypePoolShare:
		if a.LiquidityPoolId == nil {
			return assetSpec{}, fmt.Errorf("pool-share asset has no liquidity pool id")
		}
		return poolAssetSpec(*a.LiquidityPoolId), nil
	default:
		return assetSpec{}, fmt.Errorf("unknown trustline asset type %d", a.Type)
	}
}

// assetSpecFromChangeTrustAsset converts the asset form used by change-trust
// operations.
func assetSpecFromChangeTrustAsset(a xdr.ChangeTrustAsset) (assetSpec, error) {
	switch a.Type {
	case xdr.AssetTypeAssetTypeNative:
		return nativeAssetSpec(), nil
	case xdr.AssetTypeAssetTypeCreditAlphanum4, xdr.AssetTypeAssetTypeCreditAlphanum12:
		return assetSpecFromAsset(a.ToAsset())
	case xdr.AssetTypeAssetTypePoolShare:
		return assetSpec{}, fmt.Errorf(
			"a pool-share change-trust asset cannot be resolved to a pool id; " +
				"pass the xdr.TrustLineAsset instead")
	default:
		return assetSpec{}, fmt.Errorf("unknown asset type %d", a.Type)
	}
}

func nativeAssetSpec() assetSpec {
	return assetSpec{native: true, id: "native"}
}

func poolAssetSpec(id xdr.PoolId) assetSpec {
	return assetSpec{id: "pool:" + describePoolID(id)}
}
