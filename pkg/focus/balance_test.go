package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

const (
	usdcAsset = "USDC:" + testIssuer
	eurAsset  = "EUR:" + testIssuer
)

func TestAssertBalanceChangedNative(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	before := accountEntryValue(t, testSource, 1000, thresholds)
	after := accountEntryValue(t, testSource, 800, thresholds)

	tests := []struct {
		name    string
		meta    string
		account focus.Value
		delta   int64
	}{
		{
			name:    "update by strkey",
			meta:    metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			account: testSource,
			delta:   -200,
		},
		{
			name:    "update by xdr account id",
			meta:    metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			account: mustAccountID(t, testSource),
			delta:   -200,
		},
		{
			name:    "update by muxed account",
			meta:    metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			account: mustMuxed(t, testSource),
			delta:   -200,
		},
		{
			name:    "creation starts from zero",
			meta:    metaV2(t, []xdr.LedgerEntryChange{createdChange(accountEntryValue(t, testDest, 500, thresholds))}),
			account: testDest,
			delta:   500,
		},
		{
			name: "removal subtracts the state balance",
			meta: metaV2(t,
				[]xdr.LedgerEntryChange{
					stateChange(accountEntryValue(t, testDest, 500, thresholds)),
					removedChange(ledgerKeyOf(t, accountEntryValue(t, testDest, 500, thresholds))),
				}),
			account: testDest,
			delta:   -500,
		},
		{
			name: "two operations without a re-emitted state",
			meta: metaV2(t,
				[]xdr.LedgerEntryChange{stateChange(before), updatedChange(after)},
				[]xdr.LedgerEntryChange{updatedChange(accountEntryValue(t, testSource, 700, thresholds))},
			),
			account: testSource,
			delta:   -300,
		},
		{
			name: "transaction-level changes count",
			meta: metaV2WithTx(t,
				[]xdr.LedgerEntryChange{stateChange(before), updatedChange(after)},
				nil,
				[]xdr.LedgerEntryChange{
					stateChange(after),
					updatedChange(accountEntryValue(t, testSource, 700, thresholds)),
				}),
			account: testSource,
			delta:   -300,
		},
		{
			name:    "an untouched account has a zero delta",
			meta:    metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			account: testDest,
			delta:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertBalanceChanged(tb, tc.meta, tc.account, "native", tc.delta) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

func TestAssertBalanceChangedCredit(t *testing.T) {
	t.Parallel()

	before := trustlineEntryValue(t, 100, 1000)
	after := trustlineEntryValue(t, 150, 1000)
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})

	_, decodedAsset, err := focus.Encode(mustCreditAsset(t, "USDC"), "Asset")
	if err != nil {
		t.Fatalf("encoding the asset: %v", err)
	}

	tests := []struct {
		name  string
		asset focus.Value
	}{
		{"canonical string", usdcAsset},
		{"xdr asset", mustCreditAsset(t, "USDC")},
		{"xdr trustline asset", trustLineAsset(t)},
		{"decoded lens value", decodedAsset},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertBalanceChanged(tb, meta, testDest, tc.asset, 50) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("created trustline counts in full", func(t *testing.T) {
		t.Parallel()

		created := trustlineEntryValue(t, 5_0000000, 1000)
		tb := faketb.New()
		if !focus.AssertBalanceChanged(tb, metaV2(t, []xdr.LedgerEntryChange{createdChange(created)}),
			testDest, usdcAsset, 5_0000000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("removed trustline subtracts its balance", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		removed := metaV2(t, []xdr.LedgerEntryChange{
			stateChange(before),
			removedChange(ledgerKeyOf(t, before)),
		})
		if !focus.AssertBalanceChanged(tb, removed, testDest, usdcAsset, -100) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})
}

// TestAssertBalanceChangedFailureMessage checks that a wrong delta is reported
// with both values in stroops and XLM, and with the changes that were counted.
func TestAssertBalanceChangedFailureMessage(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	before := accountEntryValue(t, testSource, 100_0000000, thresholds)
	after := accountEntryValue(t, testSource, 95_0000000, thresholds)

	t.Run("wrong delta", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})
		tb := faketb.New()
		if focus.AssertBalanceChanged(tb, meta, testSource, "native", 0) {
			t.Fatal("assertion passed with the wrong delta")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertBalanceChanged: account GCJNH…ZST6 native balance changed by",
			"-50000000 (-5.0000000 XLM), want 0 (0.0000000 XLM)",
			"ledger changes present (2)",
			"1000000000 (100.0000000 XLM)",
			"950000000 (95.0000000 XLM)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("fee is part of the delta", func(t *testing.T) {
		t.Parallel()

		feePaid := accountEntryValue(t, testSource, 100_0000000, thresholds)
		afterFee := accountEntryValue(t, testSource, 99_9999900, thresholds)
		afterPayment := accountEntryValue(t, testSource, 94_9999900, thresholds)
		meta := metaV2WithTx(t,
			[]xdr.LedgerEntryChange{stateChange(feePaid), updatedChange(afterFee)},
			nil,
			[]xdr.LedgerEntryChange{stateChange(afterFee), updatedChange(afterPayment)},
		)

		tb := faketb.New()
		// Without the fee the delta would be -50000000; it is -50000100.
		if focus.AssertBalanceChanged(tb, meta, testSource, "native", -50_000000) {
			t.Fatal("assertion passed without accounting for the fee")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "-50000100 (-5.0000100 XLM)") {
			t.Errorf("message does not include the fee in the computed delta:\n%s", msg)
		}
	})

	t.Run("wrong asset shows the account's real changes", func(t *testing.T) {
		t.Parallel()

		trustlineBefore := trustlineEntryValue(t, 100, 1000)
		trustlineAfter := trustlineEntryValue(t, 150, 1000)
		meta := metaV2(t, []xdr.LedgerEntryChange{
			stateChange(trustlineBefore),
			updatedChange(trustlineAfter),
		})

		tb := faketb.New()
		if focus.AssertBalanceChanged(tb, meta, testDest, eurAsset, 50) {
			t.Fatal("assertion passed for an unrelated asset")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"account GAPB7…WYXZ (EUR:" + testIssuer + ")",
			"balance changed by 0 (0.0000000), want 50 (0.0000050)",
			"trustline " + usdcAsset + " on GAPB7…WYXZ",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})
}

func TestAssertBalanceChangedInputErrors(t *testing.T) {
	t.Parallel()

	contractAddress, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	if err != nil {
		t.Fatalf("encoding a contract address: %v", err)
	}
	meta := func() string {
		thresholds := xdr.NewThreshold(1, 0, 0, 0)
		entry := accountEntryValue(t, testSource, 100, thresholds)
		return metaV2(t, []xdr.LedgerEntryChange{updatedChange(entry)})
	}()

	tests := []struct {
		name     string
		account  focus.Value
		asset    focus.Value
		contains []string
	}{
		{
			name:     "account is not an address",
			account:  "not-an-account",
			asset:    "native",
			contains: []string{"account could not be read as an account", "not a strkey"},
		},
		{
			name:     "account is a contract address",
			account:  contractAddress,
			asset:    "native",
			contains: []string{"not an account address"},
		},
		{
			name:     "account is a number",
			account:  42,
			asset:    "native",
			contains: []string{"unsupported type int"},
		},
		{
			name:     "asset has no issuer",
			account:  testSource,
			asset:    "USDC",
			contains: []string{`asset could not be read as an asset`, `use "native" or "CODE:ISSUER"`},
		},
		{
			name:     "asset issuer is invalid",
			account:  testSource,
			asset:    "USDC:nope",
			contains: []string{"invalid issuer"},
		},
		{
			name:     "asset is a number",
			account:  testSource,
			asset:    42,
			contains: []string{"unsupported type int"},
		},
		{
			name:     "meta is an envelope",
			account:  testSource,
			asset:    "native",
			contains: []string{"is not a valid TransactionMeta"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			metaValue := focus.Value(meta)
			if tc.name == "meta is an envelope" {
				metaValue = buildEnvelope(t, paymentOp(t, 100))
			}

			tb := faketb.New()
			if focus.AssertBalanceChanged(tb, metaValue, tc.account, tc.asset, 0) {
				t.Fatal("assertion passed with invalid input")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

func mustCreditAsset(t *testing.T, code string) xdr.Asset {
	t.Helper()
	var asset xdr.Asset
	if err := asset.SetCredit(code, mustAccountID(t, testIssuer)); err != nil {
		t.Fatalf("building asset %s: %v", code, err)
	}
	return asset
}

func TestAssertBalanceChangeRange(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	before := accountEntryValue(t, testSource, 100_0000000, thresholds)
	after := accountEntryValue(t, testSource, 99_9999900, thresholds)
	afterPayment := accountEntryValue(t, testSource, 94_9999900, thresholds)

	// A 100 XLM payment plus a fee: between one and two hundred stroops of
	// fee makes the delta land anywhere in [-50000200, -49999800].
	meta := metaV2WithTx(t,
		[]xdr.LedgerEntryChange{stateChange(before), updatedChange(after)},
		nil,
		[]xdr.LedgerEntryChange{stateChange(after), updatedChange(afterPayment)},
	)

	tests := []struct {
		name     string
		meta     focus.Value
		account  focus.Value
		asset    focus.Value
		minDelta int64
		maxDelta int64
	}{
		{
			name:     "fee at the low end of the range",
			meta:     meta,
			account:  testSource,
			asset:    "native",
			minDelta: -50_000200,
			maxDelta: -49_999800,
		},
		{
			name:     "delta exactly on a bound passes",
			meta:     meta,
			account:  testSource,
			asset:    "native",
			minDelta: -50_000100,
			maxDelta: -50_000100,
		},
		{
			name:     "unpaid fee inside a wide range",
			meta:     meta,
			account:  testSource,
			asset:    "native",
			minDelta: -100_000000,
			maxDelta: 0,
		},
		{
			name:     "credit asset within tolerance",
			meta:     meta,
			account:  testDest,
			asset:    eurAsset,
			minDelta: -1000,
			maxDelta: 1000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertBalanceChangeRange(tb, tc.meta, tc.account, tc.asset, tc.minDelta, tc.maxDelta) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("no change is a delta of zero", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		untouched := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(before)})
		if !focus.AssertBalanceChangeRange(tb, untouched, testDest, "native", -100, 100) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})
}

func TestAssertBalanceChangeRangeRejectsOutOfBoundsDelta(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	before := accountEntryValue(t, testSource, 100_0000000, thresholds)
	after := accountEntryValue(t, testSource, 95_0000000, thresholds)
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})

	tests := []struct {
		name     string
		minDelta int64
		maxDelta int64
		contains []string
	}{
		{
			name:     "delta below the minimum",
			minDelta: -40_000000,
			maxDelta: -30_000000,
			contains: []string{
				"AssertBalanceChangeRange: account GCJNH…ZST6 native balance changed by",
				"-50000000 (-5.0000000 XLM), outside [-40000000 (-4.0000000 XLM), -30000000 (-3.0000000 XLM)]",
				"ledger changes present (2)",
			},
		},
		{
			name:     "delta above the maximum",
			minDelta: -60_000000,
			maxDelta: -55_000000,
			contains: []string{
				"-50000000 (-5.0000000 XLM), outside [-60000000 (-6.0000000 XLM), -55000000 (-5.5000000 XLM)]",
			},
		},
		{
			name:     "empty range passes nothing",
			minDelta: -50_000000,
			maxDelta: -50_000001,
			contains: []string{"outside [-50000000 (-5.0000000 XLM), -50000001 (-5.0000001 XLM)]"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertBalanceChangeRange(tb, meta, testSource, "native", tc.minDelta, tc.maxDelta) {
				t.Fatal("assertion passed with a delta outside the range")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

func TestAssertBalanceChangeRangeInputErrors(t *testing.T) {
	t.Parallel()

	meta := func() string {
		thresholds := xdr.NewThreshold(1, 0, 0, 0)
		entry := accountEntryValue(t, testSource, 100, thresholds)
		return metaV2(t, []xdr.LedgerEntryChange{updatedChange(entry)})
	}()

	tests := []struct {
		name     string
		account  focus.Value
		asset    focus.Value
		contains []string
	}{
		{
			name:     "account is not an address",
			account:  "not-an-account",
			asset:    "native",
			contains: []string{"account could not be read as an account", "not a strkey"},
		},
		{
			name:     "account is a contract address",
			account:  "C" + strings.Repeat("A", 55),
			asset:    "native",
			contains: []string{"not an account address"},
		},
		{
			name:     "asset has no issuer",
			account:  testSource,
			asset:    "USDC",
			contains: []string{`asset could not be read as an asset`, `use "native" or "CODE:ISSUER"`},
		},
		{
			name:     "meta is an envelope",
			account:  testSource,
			asset:    "native",
			contains: []string{"is not a valid TransactionMeta"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			metaValue := focus.Value(meta)
			if tc.name == "meta is an envelope" {
				metaValue = buildEnvelope(t, paymentOp(t, 100))
			}

			tb := faketb.New()
			if focus.AssertBalanceChangeRange(tb, metaValue, tc.account, tc.asset, 0, 0) {
				t.Fatal("assertion passed with invalid input")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}
