package focus

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Liquidity pool assertions read what a deposit or a withdrawal did to a
// LiquidityPoolEntry: how far each reserve moved, and how many pool shares
// were minted or burned against it.
//
// None of that is in the envelope. A deposit operation names the maximum
// amounts it is willing to put in and the price range it will accept, and the
// protocol then decides the actual amounts from the pool's reserves at
// execution; a withdrawal names the shares to burn and gets back whatever
// share of the reserves they represent. So the amounts a test wants to pin
// down exist only in the meta, as the difference between the entry before and
// after.
//
// Shares are the direction signal. Reserves move for a trade as well, so what
// separates a deposit from a swap is that a deposit mints pool shares and a
// swap does not — which is also why a reserve movement with no share movement
// is reported as a trade rather than as a wrong amount.

// PoolChange describes the movement a liquidity pool assertion expects, in
// stroops.
//
// Every field is a magnitude, not a signed delta: the direction comes from the
// assertion. A deposit adds ReserveA and ReserveB to the pool and mints
// Shares; a withdrawal takes them out and burns Shares. A negative field is
// reported as an input error rather than inverted.
//
// All three fields are checked. A deposit that leaves one of them out is
// almost always a test that has not noticed the protocol choosing different
// amounts than the operation asked for, which is the thing most worth
// catching here.
type PoolChange struct {
	// ReserveA is the movement in the pool's first reserve.
	ReserveA int64
	// ReserveB is the movement in the pool's second reserve.
	ReserveB int64
	// Shares is the pool shares minted by a deposit or burned by a
	// withdrawal.
	Shares int64
}

// poolReserves is the measurable state of a constant-product pool.
type poolReserves struct {
	a, b, shares int64
}

// poolMovement is the outcome of walking a meta's changes for one pool: its
// state before and after, and the shape of the change.
type poolMovement struct {
	// id is the pool id in hex.
	id string
	// assetA and assetB are canonical labels for the pool's two assets, taken
	// from the entry. They are empty when the meta never shows the entry's
	// body, as is the case for a pool that was only removed.
	assetA, assetB string
	// fee is the pool's fee in basis points.
	fee int32
	// touched reports that some change in the meta concerns this pool.
	touched bool
	// before is the pool's state ahead of the transaction, and hasBefore
	// reports whether the meta establishes it.
	before    poolReserves
	hasBefore bool
	// after is the pool's state once the transaction committed.
	after poolReserves
	// created and removed report that the transaction added or deleted the
	// entry, which is what the first deposit into a pool and the last
	// withdrawal out of it do.
	created, removed bool
}

// delta is the signed movement the transaction made.
func (m poolMovement) delta() poolReserves {
	return poolReserves{
		a:      m.after.a - m.before.a,
		b:      m.after.b - m.before.b,
		shares: m.after.shares - m.before.shares,
	}
}

// AssertLiquidityPoolDeposit checks that the transaction deposited into a
// liquidity pool: that both reserves rose by the wanted amounts and that the
// wanted number of pool shares was minted.
//
// pool accepts the pool id as 64 hex characters — with or without the "pool:"
// prefix the failure messages use — an xdr.PoolId, an xdr.TrustLineAsset for
// the pool's shares, an xdr.LedgerKey or an xdr.LedgerEntry for the pool, or
// the XDR of one of those as base64, raw bytes or a decoded *lens.Value.
//
// The movement is measured across the whole transaction, from the pool's state
// before it to the state it committed, so a transaction that deposits twice is
// checked against the total. Creating the pool counts as a deposit into it,
// since its reserves and shares are new to the ledger.
func AssertLiquidityPoolDeposit(t TB, meta Value, pool Value, want PoolChange) bool {
	t.Helper()
	return assertPoolChange(t, "AssertLiquidityPoolDeposit", meta, pool, want, true)
}

// AssertLiquidityPoolWithdrawal checks that the transaction withdrew from a
// liquidity pool: that both reserves fell by the wanted amounts and that the
// wanted number of pool shares was burned.
//
// It accepts the same pool forms as AssertLiquidityPoolDeposit, and the wanted
// amounts are the magnitudes taken out rather than negative deltas. A
// withdrawal that empties the pool deletes its entry, which counts: the
// reserves and shares go to zero.
func AssertLiquidityPoolWithdrawal(t TB, meta Value, pool Value, want PoolChange) bool {
	t.Helper()
	return assertPoolChange(t, "AssertLiquidityPoolWithdrawal", meta, pool, want, false)
}

// assertPoolChange is the shared implementation behind the two pool
// assertions. deposit selects the direction the magnitudes in want are
// applied in.
func assertPoolChange(t TB, assertion string, meta, pool Value, want PoolChange, deposit bool) bool {
	t.Helper()

	if !validPoolChange(t, assertion, want, deposit) {
		return false
	}
	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	id, ok := resolvePoolID(t, assertion, pool)
	if !ok {
		return false
	}
	mov, ok := poolMovementOf(t, assertion, changes, id)
	if !ok {
		return false
	}

	if !mov.touched {
		t.Errorf("%s: the transaction did not change liquidity pool %s\n\n%s",
			assertion, shortenID(id), indent(describePoolChanges(changes), "  "))
		return false
	}
	if !mov.hasBefore {
		t.Errorf("%s: this meta does not record liquidity pool %s before the change, "+
			"so the movement cannot be measured\n\n%s",
			assertion, shortenID(id), indent(describePoolChanges(changes), "  "))
		return false
	}

	got := mov.delta()
	if !correctDirection(t, assertion, mov, got, deposit) {
		return false
	}

	wanted := signedPoolChange(want, deposit)
	if got == wanted {
		return true
	}

	t.Errorf("%s", poolMismatchMessage(assertion, mov, got, wanted, deposit))
	return false
}

// validPoolChange rejects negative magnitudes, which are a sign the caller
// has mixed up the two assertions rather than a movement to look for.
func validPoolChange(t TB, assertion string, want PoolChange, deposit bool) bool {
	t.Helper()

	noun := "a withdrawal"
	if deposit {
		noun = "a deposit"
	}
	for _, f := range []struct {
		name string
		v    int64
	}{{"ReserveA", want.ReserveA}, {"ReserveB", want.ReserveB}, {"Shares", want.Shares}} {
		if f.v < 0 {
			t.Errorf("%s: want.%s is %d; %s is described by the amounts it moves, "+
				"which are never negative", assertion, f.name, f.v, noun)
			return false
		}
	}
	return true
}

// signedPoolChange turns the wanted magnitudes into the signed delta the
// direction implies.
func signedPoolChange(want PoolChange, deposit bool) poolReserves {
	if deposit {
		return poolReserves{a: want.ReserveA, b: want.ReserveB, shares: want.Shares}
	}
	return poolReserves{a: -want.ReserveA, b: -want.ReserveB, shares: -want.Shares}
}

// correctDirection reports whether the movement found is of the kind the
// assertion is about, naming the other assertion when it is not.
//
// The share movement decides: minting is a deposit and burning is a
// withdrawal, while reserves that move with no share movement are a trade
// against the pool.
func correctDirection(t TB, assertion string, mov poolMovement, got poolReserves, deposit bool) bool {
	t.Helper()

	switch {
	case got.shares == 0 && (got.a != 0 || got.b != 0):
		t.Errorf("%s: liquidity pool %s moved its reserves without minting or burning "+
			"pool shares, which is a trade against the pool rather than a deposit or "+
			"a withdrawal\n\n%s",
			assertion, shortenID(mov.id), indent(describePoolMovement(mov), "  "))
		return false
	case deposit && got.shares < 0:
		t.Errorf("%s: liquidity pool %s burned %s pool shares; "+
			"a withdrawal is asserted with AssertLiquidityPoolWithdrawal\n\n%s",
			assertion, shortenID(mov.id), amountStroops(xdr.Int64(-got.shares), ""),
			indent(describePoolMovement(mov), "  "))
		return false
	case !deposit && got.shares > 0:
		t.Errorf("%s: liquidity pool %s minted %s pool shares; "+
			"a deposit is asserted with AssertLiquidityPoolDeposit\n\n%s",
			assertion, shortenID(mov.id), amountStroops(xdr.Int64(got.shares), ""),
			indent(describePoolMovement(mov), "  "))
		return false
	}
	return true
}

// poolMovementOf walks a meta's changes for one pool and reduces them to the
// movement the transaction made.
func poolMovementOf(t TB, assertion string, changes []ledgerChange, id string) (poolMovement, bool) {
	mov := poolMovement{id: id}
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeLiquidityPool || c.id != id {
				continue
			}
			mov.touched = true

			body, has := poolBodyOf(c.entry)
			if has {
				mov.assetA, mov.assetB, mov.fee = body.assetA, body.assetB, body.fee
			}

			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if has && !mov.hasBefore {
					mov.before, mov.hasBefore = body.reserves, true
				}
				if has {
					mov.after = body.reserves
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				// A pool that did not exist held nothing, so its state
				// before the transaction is zero.
				if !mov.hasBefore {
					mov.hasBefore, mov.created = true, true
				}
				if has {
					mov.after = body.reserves
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				if has {
					mov.after = body.reserves
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				// A removed entry carries only a key: the pool is gone, so
				// everything it held is zero.
				mov.after, mov.removed = poolReserves{}, true
			}
		}
	})
	if !ok {
		return poolMovement{}, false
	}
	return mov, true
}

// poolBody is the part of a pool entry the assertions read.
type poolBody struct {
	assetA, assetB string
	fee            int32
	reserves       poolReserves
}

// poolBodyOf reads a pool entry's constant-product body. It calls generated
// accessors, so it must run behind guard.
func poolBodyOf(entry *xdr.LedgerEntry) (poolBody, bool) {
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeLiquidityPool ||
		entry.Data.LiquidityPool == nil {
		return poolBody{}, false
	}
	cp, ok := entry.Data.LiquidityPool.Body.GetConstantProduct()
	if !ok {
		return poolBody{}, false
	}
	return poolBody{
		assetA: cp.Params.AssetA.StringCanonical(),
		assetB: cp.Params.AssetB.StringCanonical(),
		fee:    int32(cp.Params.Fee),
		reserves: poolReserves{
			a:      int64(cp.ReserveA),
			b:      int64(cp.ReserveB),
			shares: int64(cp.TotalPoolShares),
		},
	}, true
}

// resolvePoolID coerces a pool argument to its hex id and reports a test
// failure when it cannot.
func resolvePoolID(t TB, assertion string, v Value) (string, bool) {
	t.Helper()

	var (
		id  string
		err error
	)
	if !guard(t, assertion, func() { id, err = poolIDOf(v) }) {
		return "", false
	}
	if err != nil {
		t.Errorf("%s: pool could not be read: %v", assertion, err)
		return "", false
	}
	return id, true
}

// poolIDOf accepts a pool in the forms callers hold it in.
func poolIDOf(v Value) (string, error) {
	switch val := v.(type) {
	case string:
		return poolIDFromHex(val)
	case xdr.PoolId:
		return describePoolID(val), nil
	case *xdr.PoolId:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.PoolId")
		}
		return describePoolID(*val), nil
	case xdr.TrustLineAsset:
		return poolIDFromTrustLineAsset(val)
	case *xdr.TrustLineAsset:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.TrustLineAsset")
		}
		return poolIDFromTrustLineAsset(*val)
	default:
		key, err := coerceLedgerKey(v)
		if err != nil {
			return "", fmt.Errorf(
				"%w; pass the pool id as hex, an xdr.PoolId, or the LedgerKey of the pool", err)
		}
		k, ok := key.GetLiquidityPool()
		if !ok {
			return "", fmt.Errorf("value identifies a %s entry, not a liquidity pool",
				strings.TrimPrefix(key.Type.String(), "LedgerEntryType"))
		}
		return describePoolID(k.LiquidityPoolId), nil
	}
}

// poolIDFromTrustLineAsset reads the pool a pool-share trustline is for.
func poolIDFromTrustLineAsset(a xdr.TrustLineAsset) (string, error) {
	if a.Type != xdr.AssetTypeAssetTypePoolShare {
		return "", fmt.Errorf("the asset is %s, not a pool share", describeTrustLineAsset(a))
	}
	if a.LiquidityPoolId == nil {
		return "", fmt.Errorf("pool-share asset has no liquidity pool id")
	}
	return describePoolID(*a.LiquidityPoolId), nil
}

// poolIDFromHex accepts the pool id as hex, with or without the "pool:"
// prefix the package renders pool shares with.
func poolIDFromHex(s string) (string, error) {
	trimmed := strings.TrimSpace(s)
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "pool:"), "0x")
	trimmed = strings.ToLower(trimmed)

	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return "", fmt.Errorf("%q is not hex: %w", s, err)
	}
	if len(raw) != len(xdr.PoolId{}) {
		return "", fmt.Errorf("%q is %d bytes of hex; a liquidity pool id is %d bytes "+
			"(%d characters)", s, len(raw), len(xdr.PoolId{}), 2*len(xdr.PoolId{}))
	}
	return trimmed, nil
}

// poolMismatchMessage renders an amount mismatch: the fields that differ
// first, then the pool's movement in full.
func poolMismatchMessage(
	assertion string, mov poolMovement, got, want poolReserves, deposit bool,
) string {
	noun := "withdrawal"
	if deposit {
		noun = "deposit"
	}

	diffs := make([]string, 0, 3)
	for _, f := range []struct {
		label     string
		got, want int64
	}{
		{"reserve A" + poolAssetSuffix(mov.assetA), got.a, want.a},
		{"reserve B" + poolAssetSuffix(mov.assetB), got.b, want.b},
		{"shares", got.shares, want.shares},
	} {
		if f.got == f.want {
			continue
		}
		diffs = append(diffs, fmt.Sprintf("~ %s moved by %s, want %s",
			f.label, signedStroops(f.got), signedStroops(f.want)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: liquidity pool %s %s does not match — %s\n\n",
		assertion, shortenID(mov.id), noun, pluralChanges(len(diffs)))
	for _, d := range diffs {
		fmt.Fprintf(&b, "  %s\n", d)
	}
	b.WriteString("\n")
	b.WriteString(indent(describePoolMovement(mov), "  "))
	return b.String()
}

// describePoolMovement renders a pool's before and after state, so a mismatch
// shows where the reserves moved from as well as to.
func describePoolMovement(mov poolMovement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "liquidity pool %s", shortenID(mov.id))
	switch {
	case mov.created:
		b.WriteString(" (created by this transaction)")
	case mov.removed:
		b.WriteString(" (removed by this transaction)")
	}
	if mov.assetA != "" || mov.assetB != "" {
		fmt.Fprintf(&b, ", %s/%s, fee %d bps", mov.assetA, mov.assetB, mov.fee)
	}
	b.WriteString(":\n")

	for _, f := range []struct {
		label         string
		before, after int64
	}{
		{"reserve A" + poolAssetSuffix(mov.assetA), mov.before.a, mov.after.a},
		{"reserve B" + poolAssetSuffix(mov.assetB), mov.before.b, mov.after.b},
		{"shares", mov.before.shares, mov.after.shares},
	} {
		// Without a before state there is nothing to show an arrow from, so
		// the line states what the entry holds now.
		//
		// The label is not padded to a column: a credit asset carries its
		// issuer, which is long enough that any column wide enough for it
		// would push the numbers off the line.
		if !mov.hasBefore {
			fmt.Fprintf(&b, "  %s: %s\n", f.label, amountStroops(xdr.Int64(f.after), ""))
			continue
		}
		fmt.Fprintf(&b, "  %s: %s → %s\n", f.label,
			amountStroops(xdr.Int64(f.before), ""), amountStroops(xdr.Int64(f.after), ""))
	}
	return strings.TrimRight(b.String(), "\n")
}

// describePoolChanges lists only the changes touching liquidity pools, so a
// failed lookup names the pools the transaction did touch.
func describePoolChanges(changes []ledgerChange) string {
	matching := make([]ledgerChange, 0, len(changes))
	for _, c := range changes {
		if c.entryType == xdr.LedgerEntryTypeLiquidityPool {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return "no ledger entry change in this meta touches a liquidity pool"
	}
	return describeChanges(matching)
}

// poolAssetSuffix names a pool's asset next to its reserve, where the entry
// said what it is.
func poolAssetSuffix(asset string) string {
	if asset == "" {
		return ""
	}
	return " (" + asset + ")"
}

// signedStroops renders a delta with an explicit sign, so a movement reads as
// a direction rather than as a number.
func signedStroops(v int64) string {
	if v > 0 {
		return "+" + amountStroops(xdr.Int64(v), "")
	}
	return amountStroops(xdr.Int64(v), "")
}
