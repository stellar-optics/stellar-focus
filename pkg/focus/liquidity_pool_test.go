package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// poolFixture builds a pool before and after a deposit of 100/200 reserves
// and 70 shares, the shape most of these tests need.
func poolFixture(t *testing.T) (xdr.PoolId, xdr.LedgerEntry, xdr.LedgerEntry) {
	t.Helper()

	id := poolIDValue(0x7a)
	usdc := mustCreditAsset(t, "USDC")
	before := poolEntryValue(id, nativeAsset(), usdc, 1000, 2000, 700)
	after := poolEntryValue(id, nativeAsset(), usdc, 1100, 2200, 770)
	return id, before, after
}

func TestAssertLiquidityPoolDeposit(t *testing.T) {
	t.Parallel()

	id, before, after := poolFixture(t)
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})
	want := focus.PoolChange{ReserveA: 100, ReserveB: 200, Shares: 70}

	tests := []struct {
		name string
		pool focus.Value
	}{
		{"hex pool id", strings.Repeat("7a", 32)},
		{"hex with the pool prefix", "pool:" + strings.Repeat("7a", 32)},
		{"hex in upper case", strings.ToUpper(strings.Repeat("7a", 32))},
		{"xdr pool id", id},
		{"pointer to an xdr pool id", &id},
		{"the pool share trustline asset", xdr.TrustLineAsset{
			Type:            xdr.AssetTypeAssetTypePoolShare,
			LiquidityPoolId: &id,
		}},
		{"the ledger key", ledgerKeyOf(t, after)},
		{"the ledger entry", after},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertLiquidityPoolDeposit(tb, meta, tc.pool, want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("creating the pool is a deposit into it", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(after)}), id,
			focus.PoolChange{ReserveA: 1100, ReserveB: 2200, Shares: 770}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("two deposits are measured as their total", func(t *testing.T) {
		t.Parallel()

		second := poolEntryValue(id, nativeAsset(), mustCreditAsset(t, "USDC"), 1300, 2600, 910)
		tb := faketb.New()
		if !focus.AssertLiquidityPoolDeposit(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(before), updatedChange(after)},
			[]xdr.LedgerEntryChange{stateChange(after), updatedChange(second)}),
			id, focus.PoolChange{ReserveA: 300, ReserveB: 600, Shares: 210}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a restored entry is read like an update", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), restoredChange(after)}),
			id, want) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})
}

func TestAssertLiquidityPoolWithdrawal(t *testing.T) {
	t.Parallel()

	id, small, large := poolFixture(t)
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(large), updatedChange(small)})

	t.Run("reserves and shares leaving the pool", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertLiquidityPoolWithdrawal(tb, meta, id,
			focus.PoolChange{ReserveA: 100, ReserveB: 200, Shares: 70}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("emptying the pool removes its entry", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertLiquidityPoolWithdrawal(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(large), removedChange(ledgerKeyOf(t, large))}),
			id, focus.PoolChange{ReserveA: 1100, ReserveB: 2200, Shares: 770}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})
}

func TestAssertLiquidityPoolFailureMessages(t *testing.T) {
	t.Parallel()

	id, before, after := poolFixture(t)
	deposit := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})

	t.Run("wrong amounts name every field that differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb, deposit, id,
			focus.PoolChange{ReserveA: 150, ReserveB: 200, Shares: 75}) {
			t.Fatal("assertion passed with the wrong amounts")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertLiquidityPoolDeposit: liquidity pool 7a7a7a7a…7a7a7a7a deposit does not match — 2 differences",
			"~ reserve A (native) moved by +100 (0.0000100), want +150 (0.0000150)",
			"~ shares moved by +70 (0.0000070), want +75 (0.0000075)",
			"liquidity pool 7a7a7a7a…7a7a7a7a, native/USDC:" + testIssuer + ", fee 30 bps",
			"reserve A (native)",
			"1000 (0.0001000) → 1100 (0.0001100)",
			"shares",
			"700 (0.0000700) → 770 (0.0000770)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
		if strings.Contains(msg, "~ reserve B") {
			t.Errorf("message names a field that matched:\n%s", msg)
		}
	})

	t.Run("an untouched pool names the pools that changed", func(t *testing.T) {
		t.Parallel()

		other := poolIDValue(0x01)
		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb, deposit, other,
			focus.PoolChange{ReserveA: 1, ReserveB: 1, Shares: 1}) {
			t.Fatal("assertion passed for an untouched pool")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"the transaction did not change liquidity pool 01010101…01010101",
			"liquidity pool 7a7a7a7a7a",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a meta with no pool at all", func(t *testing.T) {
		t.Parallel()

		account := accountEntryValue(t, testSource, 100, xdr.Thresholds{1, 1, 1, 1})
		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{updatedChange(account)}), id,
			focus.PoolChange{ReserveA: 1, ReserveB: 1, Shares: 1}) {
			t.Fatal("assertion passed with no pool in the meta")
		}
		if msg := tb.Output(); !strings.Contains(msg,
			"no ledger entry change in this meta touches a liquidity pool") {
			t.Errorf("message does not say the meta touches no pool:\n%s", msg)
		}
	})

	t.Run("a withdrawal asserted as a deposit names the other assertion", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(after), updatedChange(before)}),
			id, focus.PoolChange{ReserveA: 100, ReserveB: 200, Shares: 70}) {
			t.Fatal("assertion passed for a withdrawal")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"burned 70 (0.0000070) pool shares",
			"a withdrawal is asserted with AssertLiquidityPoolWithdrawal",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a deposit asserted as a withdrawal names the other assertion", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolWithdrawal(tb, deposit, id,
			focus.PoolChange{ReserveA: 100, ReserveB: 200, Shares: 70}) {
			t.Fatal("assertion passed for a deposit")
		}
		if msg := tb.Output(); !strings.Contains(msg,
			"a deposit is asserted with AssertLiquidityPoolDeposit") {
			t.Errorf("message does not name the other assertion:\n%s", msg)
		}
	})

	t.Run("a trade against the pool is called a trade", func(t *testing.T) {
		t.Parallel()

		usdc := mustCreditAsset(t, "USDC")
		traded := poolEntryValue(id, nativeAsset(), usdc, 1100, 1818, 700)
		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(traded)}),
			id, focus.PoolChange{ReserveA: 100, ReserveB: 0, Shares: 0}) {
			t.Fatal("assertion passed for a trade against the pool")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"moved its reserves without minting or burning pool shares",
			"a trade against the pool rather than a deposit or a withdrawal",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an update with no prior state cannot be measured", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{updatedChange(after)}), id,
			focus.PoolChange{ReserveA: 100, ReserveB: 200, Shares: 70}) {
			t.Fatal("assertion passed with no state to measure from")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"does not record liquidity pool 7a7a7a7a…7a7a7a7a before the change",
			"so the movement cannot be measured",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("negative wanted amounts are input errors", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name  string
			want  focus.PoolChange
			field string
		}{
			{"reserve A", focus.PoolChange{ReserveA: -1}, "want.ReserveA is -1"},
			{"reserve B", focus.PoolChange{ReserveB: -2}, "want.ReserveB is -2"},
			{"shares", focus.PoolChange{Shares: -3}, "want.Shares is -3"},
		}
		for _, tc := range cases {
			tb := faketb.New()
			if focus.AssertLiquidityPoolDeposit(tb, deposit, id, tc.want) {
				t.Fatalf("%s: assertion passed with a negative amount", tc.name)
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			for _, want := range []string{tc.field,
				"a deposit is described by the amounts it moves, which are never negative"} {
				if !strings.Contains(msg, want) {
					t.Errorf("%s: message does not contain %q:\n%s", tc.name, want, msg)
				}
			}
		}

		tb := faketb.New()
		if focus.AssertLiquidityPoolWithdrawal(tb, deposit, id, focus.PoolChange{Shares: -1}) {
			t.Fatal("assertion passed with a negative amount")
		}
		if msg := tb.Output(); !strings.Contains(msg, "a withdrawal is described by the amounts") {
			t.Errorf("message does not name the withdrawal direction:\n%s", msg)
		}
	})

	t.Run("unreadable pools are input errors", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name string
			pool focus.Value
			want string
		}{
			{"not hex", "zzzz", "is not hex"},
			{"wrong length", "abcd", "a liquidity pool id is 32 bytes"},
			{"a nil pointer", (*xdr.PoolId)(nil), "nil *xdr.PoolId"},
			{"a credit trustline asset", trustLineAsset(t, "USDC"), "not a pool share"},
			{"another entry's key", ledgerKeyOf(t, accountEntryValue(t, testSource, 1,
				xdr.Thresholds{1, 1, 1, 1})), "not a liquidity pool"},
			{"an unsupported type", 42, "pass the pool id as hex"},
		}
		for _, tc := range cases {
			tb := faketb.New()
			if focus.AssertLiquidityPoolDeposit(tb, deposit, tc.pool,
				focus.PoolChange{ReserveA: 1, ReserveB: 1, Shares: 1}) {
				t.Fatalf("%s: assertion passed with an unreadable pool", tc.name)
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !strings.Contains(msg, "pool could not be read") {
				t.Errorf("%s: message does not name the bad input:\n%s", tc.name, msg)
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("%s: message does not contain %q:\n%s", tc.name, tc.want, msg)
			}
		}
	})

	t.Run("a non-meta value is reported as such", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb, buildEnvelope(t, paymentOp(t, 1)), id,
			focus.PoolChange{ReserveA: 1, ReserveB: 1, Shares: 1}) {
			t.Fatal("assertion passed with an envelope")
		}
		if tb.Output() == "" {
			t.Error("no failure was reported for an envelope")
		}
	})
}

// TestLiquidityPoolEdgeCases covers the pool forms and listing labels the
// main tests do not reach.
func TestLiquidityPoolEdgeCases(t *testing.T) {
	t.Parallel()

	id, before, after := poolFixture(t)
	want := focus.PoolChange{ReserveA: 1, ReserveB: 1, Shares: 1}

	t.Run("a pointer to a pool share trustline asset", func(t *testing.T) {
		t.Parallel()

		asset := xdr.TrustLineAsset{
			Type:            xdr.AssetTypeAssetTypePoolShare,
			LiquidityPoolId: &id,
		}
		tb := faketb.New()
		if !focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			&asset, focus.PoolChange{ReserveA: 100, ReserveB: 200, Shares: 70}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a pool share asset with no pool id", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			xdr.TrustLineAsset{Type: xdr.AssetTypeAssetTypePoolShare}, want) {
			t.Fatal("assertion passed with an asset carrying no pool id")
		}
		if msg := tb.Output(); !strings.Contains(msg, "pool-share asset has no liquidity pool id") {
			t.Errorf("message does not name the missing pool id:\n%s", msg)
		}
	})

	t.Run("a created pool is labelled as created", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolDeposit(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(after)}), id, want) {
			t.Fatal("assertion passed with the wrong amounts")
		}
		if msg := tb.Output(); !strings.Contains(msg, "(created by this transaction)") {
			t.Errorf("listing does not label the pool as created:\n%s", msg)
		}
	})

	t.Run("a removed pool is labelled as removed", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertLiquidityPoolWithdrawal(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(after), removedChange(ledgerKeyOf(t, after))}),
			id, want) {
			t.Fatal("assertion passed with the wrong amounts")
		}
		msg := tb.Output()
		for _, label := range []string{"(removed by this transaction)", "→ 0 (0.0000000)"} {
			if !strings.Contains(msg, label) {
				t.Errorf("listing does not contain %q:\n%s", label, msg)
			}
		}
	})
}
