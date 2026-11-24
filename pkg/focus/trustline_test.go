package focus_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func TestAssertTrustlineModified(t *testing.T) {
	t.Parallel()

	before := trustlineEntryValue(t, 100, 1000)
	after := trustlineEntryValue(t, 100, 2000)
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})

	tests := []struct {
		name    string
		account focus.Value
		asset   focus.Value
	}{
		{"strkey and canonical asset", testDest, usdcAsset},
		{"muxed account", mustMuxed(t, testDest), usdcAsset},
		{"xdr trustline asset", testDest, trustLineAsset(t)},
		{"xdr asset", testDest, mustCreditAsset(t, "USDC")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertTrustlineModified(tb, meta, tc.account, tc.asset, 2000) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("a balance-only change keeps the limit", func(t *testing.T) {
		t.Parallel()

		drained := trustlineEntryValue(t, 0, 1000)
		paid := trustlineEntryValue(t, 100, 1000)
		tb := faketb.New()
		if !focus.AssertTrustlineModified(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(drained), updatedChange(paid)}),
			testDest, usdcAsset, 1000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("creation counts as a modification", func(t *testing.T) {
		t.Parallel()

		created := trustlineEntryValue(t, 0, 5000)
		tb := faketb.New()
		if !focus.AssertTrustlineModified(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(created)}),
			testDest, usdcAsset, 5000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("two changes take the last limit", func(t *testing.T) {
		t.Parallel()

		raised := trustlineEntryValue(t, 100, 2000)
		final := trustlineEntryValue(t, 100, 3000)
		tb := faketb.New()
		if !focus.AssertTrustlineModified(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(before), updatedChange(raised)},
			[]xdr.LedgerEntryChange{updatedChange(final)}),
			testDest, usdcAsset, 3000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("wrong limit", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTrustlineModified(tb, meta, testDest, usdcAsset, 3000) {
			t.Fatal("assertion passed with the wrong limit")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertTrustlineModified: trustline " + usdcAsset + " on account GAPB7…WYXZ",
			"has limit 2000 (0.0002000), want 3000 (0.0003000)",
			"limit 1000 (0.0001000) → 2000 (0.0002000)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})
}

func TestAssertTrustlineModifiedFailures(t *testing.T) {
	t.Parallel()

	before := trustlineEntryValue(t, 100, 1000)
	after := trustlineEntryValue(t, 150, 1000)

	t.Run("untouched trustline names what did happen", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTrustlineModified(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			testDest, eurAsset, 1000) {
			t.Fatal("assertion passed for an untouched trustline")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"did not modify trustline " + eurAsset + " on account GAPB7…WYXZ",
			"trustline " + usdcAsset + " on GAPB7…WYXZ",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("native asset is rejected with an alternative", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTrustlineModified(tb,
			metaV2(t, []xdr.LedgerEntryChange{updatedChange(after)}),
			testDest, "native", 1000) {
			t.Fatal("assertion passed for the native asset")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "the native asset has no trustline") ||
			!strings.Contains(msg, "use AssertBalanceChanged") {
			t.Errorf("message does not explain the mistake:\n%s", msg)
		}
	})

	t.Run("invalid account", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTrustlineModified(tb, metaV2(t), "nope", usdcAsset, 1000) {
			t.Fatal("assertion passed with an invalid account")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "account could not be read as an account") {
			t.Errorf("message does not name the invalid input:\n%s", msg)
		}
	})
}

// TestAssertTrustlineModifiedPoolShare covers the pool share form, which has
// no string canonical form in the protocol and so is addressed by its id.
func TestAssertTrustlineModifiedPoolShare(t *testing.T) {
	t.Parallel()

	var poolID xdr.PoolId
	copy(poolID[:], "pool-id")
	address := "pool:" + hex.EncodeToString(poolID[:])

	poolEntry := func(balance, limit int64) xdr.LedgerEntry {
		return xdr.LedgerEntry{
			LastModifiedLedgerSeq: 42,
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeTrustline,
				TrustLine: &xdr.TrustLineEntry{
					AccountId: mustAccountID(t, testDest),
					Asset: xdr.TrustLineAsset{
						Type:            xdr.AssetTypeAssetTypePoolShare,
						LiquidityPoolId: &poolID,
					},
					Balance: xdr.Int64(balance),
					Limit:   xdr.Int64(limit),
				},
			},
		}
	}

	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(poolEntry(0, 1000)), updatedChange(poolEntry(0, 2000))})

	t.Run("by pool id string", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertTrustlineModified(tb, meta, testDest, address, 2000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("by xdr trustline asset", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		asset := xdr.TrustLineAsset{Type: xdr.AssetTypeAssetTypePoolShare, LiquidityPoolId: &poolID}
		if !focus.AssertTrustlineModified(tb, meta, testDest, asset, 2000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("another pool is not matched", func(t *testing.T) {
		t.Parallel()

		var other xdr.PoolId
		copy(other[:], "other-pool")
		tb := faketb.New()
		if focus.AssertTrustlineModified(tb, meta, testDest, "pool:"+hex.EncodeToString(other[:]), 2000) {
			t.Fatal("assertion matched the wrong pool")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, address) {
			t.Errorf("message does not name the wanted pool:\n%s", msg)
		}
	})
}

func TestAssertTrustlineFlags(t *testing.T) {
	t.Parallel()

	entryAuth := trustlineEntryValue(t, 100, 1000)
	entryAuth.Data.TrustLine.Flags = xdr.Uint32(xdr.TrustLineFlagsAuthorizedFlag)

	entryAuthClawback := trustlineEntryValue(t, 100, 1000)
	entryAuthClawback.Data.TrustLine.Flags = xdr.Uint32(uint32(xdr.TrustLineFlagsAuthorizedFlag) | uint32(xdr.TrustLineFlagsTrustlineClawbackEnabledFlag))

	entryLiabilities := trustlineEntryValue(t, 100, 1000)
	entryLiabilities.Data.TrustLine.Flags = xdr.Uint32(xdr.TrustLineFlagsAuthorizedToMaintainLiabilitiesFlag)

	metaAuth := metaV2(t, []xdr.LedgerEntryChange{createdChange(entryAuth)})
	metaAuthClawback := metaV2(t, []xdr.LedgerEntryChange{stateChange(entryAuth), updatedChange(entryAuthClawback)})

	t.Run("asserts AUTHORIZED flag on created trustline", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if !focus.AssertTrustlineFlags(tb, metaAuth, testDest, usdcAsset, uint32(xdr.TrustLineFlagsAuthorizedFlag)) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.TrustlineFlags(t, metaAuth, testDest, usdcAsset, uint32(xdr.TrustLineFlagsAuthorizedFlag))
	})

	t.Run("asserts AUTHORIZED and CLAWBACK_ENABLED flags on updated trustline", func(t *testing.T) {
		t.Parallel()
		wantFlags := uint32(xdr.TrustLineFlagsAuthorizedFlag) | uint32(xdr.TrustLineFlagsTrustlineClawbackEnabledFlag)
		tb := faketb.New()
		if !focus.AssertTrustlineFlags(tb, metaAuthClawback, testDest, usdcAsset, wantFlags) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("asserts direct TrustLineEntry and LedgerEntry", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if !focus.AssertTrustlineFlags(tb, entryLiabilities.Data.TrustLine, testDest, usdcAsset, uint32(xdr.TrustLineFlagsAuthorizedToMaintainLiabilitiesFlag)) {
			t.Errorf("assertion failed on direct TrustLineEntry: %s", tb.Output())
		}
		tb2 := faketb.New()
		if !focus.AssertTrustlineFlags(tb2, &entryLiabilities, testDest, usdcAsset, uint32(xdr.TrustLineFlagsAuthorizedToMaintainLiabilitiesFlag)) {
			t.Errorf("assertion failed on direct LedgerEntry: %s", tb2.Output())
		}
	})

	t.Run("failure message prints human-readable active flags vs wanted flags", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		wantFlags := uint32(xdr.TrustLineFlagsAuthorizedFlag) | uint32(xdr.TrustLineFlagsTrustlineClawbackEnabledFlag)
		if focus.AssertTrustlineFlags(tb, metaAuth, testDest, usdcAsset, wantFlags) {
			t.Fatal("assertion unexpectedly passed when flags differed")
		}
		msg := tb.Output()
		for _, want := range []string{
			"AssertTrustlineFlags",
			"has flags AUTHORIZED",
			"want AUTHORIZED|CLAWBACK_ENABLED",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("failure message missing %q:\n%s", want, msg)
			}
		}
	})

	t.Run("untouched trustline fails", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if focus.AssertTrustlineFlags(tb, metaAuth, testDest, eurAsset, uint32(xdr.TrustLineFlagsAuthorizedFlag)) {
			t.Fatal("assertion unexpectedly passed for untouched trustline")
		}
		if !strings.Contains(tb.Output(), "did not modify trustline") {
			t.Errorf("unexpected error message: %s", tb.Output())
		}
	})

	t.Run("native asset is rejected", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if focus.AssertTrustlineFlags(tb, metaAuth, testDest, "native", uint32(xdr.TrustLineFlagsAuthorizedFlag)) {
			t.Fatal("assertion unexpectedly passed for native asset")
		}
		if !strings.Contains(tb.Output(), "the native asset has no trustline") {
			t.Errorf("unexpected error message: %s", tb.Output())
		}
	})
}

func TestAssertSetTrustLineFlags(t *testing.T) {
	t.Parallel()

	asset := mustCreditAsset(t, "USDC")
	set := uint32(xdr.TrustLineFlagsAuthorizedFlag) | uint32(xdr.TrustLineFlagsTrustlineClawbackEnabledFlag)
	clear := uint32(xdr.TrustLineFlagsAuthorizedToMaintainLiabilitiesFlag)
	op := xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeSetTrustLineFlags,
		SetTrustLineFlagsOp: &xdr.SetTrustLineFlagsOp{
			Trustor:    mustAccountID(t, testDest),
			Asset:      asset,
			SetFlags:   xdr.Uint32(set),
			ClearFlags: xdr.Uint32(clear),
		},
	}}

	t.Run("matches trustor, asset, and masks", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if !focus.AssertSetTrustLineFlags(tb, op, testDest, asset, set, clear) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.SetTrustLineFlags(t, op, testDest, asset, set, clear)
	})

	t.Run("reports human-readable set and clear differences", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		wantSet := uint32(xdr.TrustLineFlagsAuthorizedFlag)
		wantClear := uint32(xdr.TrustLineFlagsAuthorizedFlag)
		if focus.AssertSetTrustLineFlags(tb, op, testDest, asset, wantSet, wantClear) {
			t.Fatal("assertion passed with different flag masks")
		}
		for _, want := range []string{"set flags are", "AUTHORIZED|CLAWBACK_ENABLED", "clear flags are", "MAINTAIN_LIABILITIES"} {
			if !strings.Contains(tb.Output(), want) {
				t.Errorf("message does not contain %q:\n%s", want, tb.Output())
			}
		}
	})
}
