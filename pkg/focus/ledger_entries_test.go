package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func TestAssertEntryCreated(t *testing.T) {
	t.Parallel()

	acct := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
	key := ledgerKeyOf(t, acct)
	_, lensKey, err := focus.Encode(key, "LedgerKey")
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}

	tests := []struct {
		name string
		meta string
		key  focus.Value
	}{
		{"xdr key", metaV2(t, []xdr.LedgerEntryChange{createdChange(acct)}), key},
		{"ledger entry as key", metaV2(t, []xdr.LedgerEntryChange{createdChange(acct)}), acct},
		{"base64 key", metaV2(t, []xdr.LedgerEntryChange{createdChange(acct)}), mustMarshal(t, key)},
		{"decoded key", metaV2(t, []xdr.LedgerEntryChange{createdChange(acct)}), lensKey},
		{"restored counts as created", metaV2(t, []xdr.LedgerEntryChange{restoredChange(acct)}), key},
		{"in a later operation", metaV2(t, nil, []xdr.LedgerEntryChange{createdChange(acct)}), key},
		{"in tx changes before", metaV2WithTx(t, []xdr.LedgerEntryChange{createdChange(acct)}, nil), key},
		{"in tx changes after", metaV2WithTx(t, nil, []xdr.LedgerEntryChange{createdChange(acct)}), key},
		{"version 0 meta", metaV0(t, []xdr.LedgerEntryChange{createdChange(acct)}), key},
		{"version 1 meta", metaV1(t, []xdr.LedgerEntryChange{createdChange(acct)}), key},
		{"version 3 meta", metaV3WithTx(t, nil, nil, []xdr.LedgerEntryChange{createdChange(acct)}), key},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertEntryCreated(tb, tc.meta, tc.key) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertEntryCreatedFailureMessage checks that a miss names the entry that
// was wanted and shows what the transaction did instead.
func TestAssertEntryCreatedFailureMessage(t *testing.T) {
	t.Parallel()

	before := accountEntryValue(t, testSource, 100_0000000, xdr.NewThreshold(1, 0, 0, 0))
	after := accountEntryValue(t, testSource, 90_0000000, xdr.NewThreshold(1, 0, 0, 0))
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})
	key := ledgerKeyOf(t, after)

	tb := faketb.New()
	if focus.AssertEntryCreated(tb, meta, key) {
		t.Fatal("assertion passed for an entry that was only modified")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{
		"AssertEntryCreated: no ledger entry was created for account GCJNH…ZST6",
		"ledger changes present (2)",
		"[0] before",
		"[1] modified",
		"1000000000 (100.0000000 XLM)",
		"900000000 (90.0000000 XLM)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertEntryCreatedEmptyMeta(t *testing.T) {
	t.Parallel()

	acct := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
	key := ledgerKeyOf(t, acct)

	tb := faketb.New()
	if focus.AssertEntryCreated(tb, metaV2(t), key) {
		t.Fatal("assertion passed with no ledger changes at all")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "carries no ledger entry changes") {
		t.Errorf("message does not say the meta has no changes:\n%s", msg)
	}
}

func TestAssertEntryModified(t *testing.T) {
	t.Parallel()

	before := trustlineEntryValue(t, 100, 1000)
	after := trustlineEntryValue(t, 500, 1000)
	key := ledgerKeyOf(t, after)

	t.Run("passes for an update", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})
		tb := faketb.New()
		if !focus.AssertEntryModified(tb, meta, key) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("fails for a creation", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(after)})
		tb := faketb.New()
		if focus.AssertEntryModified(tb, meta, key) {
			t.Fatal("assertion passed for a creation")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"no ledger entry was modified for trustline " + usdcAsset + " on GAPB7…WYXZ",
			"created",
			"balance 500 (0.0000500), limit 1000 (0.0001000)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})
}

func TestAssertEntryDeleted(t *testing.T) {
	t.Parallel()

	trustline := trustlineEntryValue(t, 100, 1000)
	key := ledgerKeyOf(t, trustline)

	t.Run("passes for a removal", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(trustline), removedChange(key)})
		tb := faketb.New()
		if !focus.AssertEntryDeleted(tb, meta, key) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("fails for an update", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(trustline), updatedChange(trustline)})
		tb := faketb.New()
		if focus.AssertEntryDeleted(tb, meta, key) {
			t.Fatal("assertion passed for an update")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "no ledger entry was deleted for trustline "+usdcAsset+" on GAPB7…WYXZ") {
			t.Errorf("message does not name the wanted entry:\n%s", msg)
		}
	})
}

func TestAssertLedgerEntryState(t *testing.T) {
	t.Parallel()

	acct := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
	key := ledgerKeyOf(t, acct)
	_, lensKey, err := focus.Encode(key, "LedgerKey")
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}

	t.Run("created accepts every key form", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(acct)})
		forms := []focus.Value{key, acct, mustMarshal(t, key), lensKey, testDest}
		for _, form := range forms {
			t.Run("", func(t *testing.T) {
				t.Parallel()
				tb := faketb.New()
				if !focus.AssertLedgerEntryState(tb, meta, form, focus.StateCreated) {
					t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
				}
			})
		}
	})

	t.Run("restored counts as created", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertLedgerEntryState(tb, metaV2(t, []xdr.LedgerEntryChange{restoredChange(acct)}), key, focus.StateCreated) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("updated", func(t *testing.T) {
		t.Parallel()

		before := trustlineEntryValue(t, 100, 1000)
		after := trustlineEntryValue(t, 500, 1000)
		key := ledgerKeyOf(t, after)
		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})
		tb := faketb.New()
		if !focus.AssertLedgerEntryState(tb, meta, key, focus.StateUpdated) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("deleted", func(t *testing.T) {
		t.Parallel()

		trustline := trustlineEntryValue(t, 100, 1000)
		key := ledgerKeyOf(t, trustline)
		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(trustline), removedChange(key)})
		tb := faketb.New()
		if !focus.AssertLedgerEntryState(tb, meta, key, focus.StateDeleted) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("each state rejects the others", func(t *testing.T) {
		t.Parallel()

		key := ledgerKeyOf(t, acct)

		tests := []struct {
			name string
			meta string
			want focus.EntryStateTransition
		}{
			{"created entry is not updated", metaV2(t, []xdr.LedgerEntryChange{createdChange(acct)}), focus.StateUpdated},
			{"updated entry is not deleted", metaV2(t, []xdr.LedgerEntryChange{stateChange(acct), updatedChange(acct)}), focus.StateDeleted},
			{"deleted entry is not created", metaV2(t, []xdr.LedgerEntryChange{stateChange(acct), removedChange(key)}), focus.StateCreated},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				tb := faketb.New()
				if focus.AssertLedgerEntryState(tb, tc.meta, key, tc.want) {
					t.Fatal("assertion passed for the wrong state")
				}
				msg, err := tb.Message()
				if err != nil {
					t.Fatalf("%v", err)
				}
				if !strings.Contains(msg, "no ledger entry was "+tc.want.String()) {
					t.Errorf("message does not name the wanted transition:\n%s", msg)
				}
			})
		}
	})
}

// TestAssertLedgerEntryStateFailureMessage checks that a miss names the entry
// that was wanted and shows what the transaction did instead.
func TestAssertLedgerEntryStateFailureMessage(t *testing.T) {
	t.Parallel()

	before := accountEntryValue(t, testSource, 100_0000000, xdr.NewThreshold(1, 0, 0, 0))
	after := accountEntryValue(t, testSource, 90_0000000, xdr.NewThreshold(1, 0, 0, 0))
	meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)})
	key := ledgerKeyOf(t, after)

	tb := faketb.New()
	if focus.AssertLedgerEntryState(tb, meta, key, focus.StateCreated) {
		t.Fatal("assertion passed for an entry that was only modified")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{
		"AssertLedgerEntryState: no ledger entry was created for account GCJNH…ZST6",
		"ledger changes present (2)",
		"[0] before",
		"[1] modified",
		"1000000000 (100.0000000 XLM)",
		"900000000 (90.0000000 XLM)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertLedgerEntryStateEmptyMeta(t *testing.T) {
	t.Parallel()

	acct := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
	key := ledgerKeyOf(t, acct)

	tb := faketb.New()
	if focus.AssertLedgerEntryState(tb, metaV2(t), key, focus.StateCreated) {
		t.Fatal("assertion passed with no ledger changes at all")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "carries no ledger entry changes") {
		t.Errorf("message does not say the meta has no changes:\n%s", msg)
	}
}

// TestLedgerKeyRendering covers the entry kinds a failure listing may have to
// name, including the less common ones.
func TestLedgerKeyRendering(t *testing.T) {
	t.Parallel()

	var (
		poolID  xdr.PoolId
		ttlHash xdr.Hash
		cbHash  xdr.Hash
	)
	copy(poolID[:], "pool-id")
	copy(ttlHash[:], "ttl-hash")
	copy(cbHash[:], "balance-hash")

	tests := []struct {
		name    string
		key     xdr.LedgerKey
		wantSub []string
	}{
		{"offer", offerKey(t, testSource, 42), []string{"offer GCJNH…ZST6 #42"}},
		{"data", dataKey(t, testSource, "profile"), []string{`data "profile" on GCJNH…ZST6`}},
		{"claimable balance", claimableBalanceKey(t, cbHash), []string{"claimable balance"}},
		{"liquidity pool", liquidityPoolKey(t, poolID), []string{"liquidity pool"}},
		{"contract code", contractCodeKey(t, ttlHash), []string{"contract code"}},
		{"ttl", ttlKey(t, ttlHash), []string{"ttl"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The key is used to look for an entry that is not there, so the
			// failure message must render it.
			tb := faketb.New()
			if focus.AssertEntryCreated(tb, metaV2(t), tc.key) {
				t.Fatal("assertion passed with an empty meta")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.wantSub {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

func TestEntryAssertionInputErrors(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 100))
	acct := accountEntryValue(t, testDest, 1, xdr.NewThreshold(1, 0, 0, 0))
	key := ledgerKeyOf(t, acct)

	tests := []struct {
		name     string
		fn       func(focus.TB) bool
		contains []string
	}{
		{
			name:     "meta is an envelope",
			fn:       func(tb focus.TB) bool { return focus.AssertEntryCreated(tb, env, key) },
			contains: []string{"AssertEntryCreated", "is not a valid TransactionMeta"},
		},
		{
			name:     "key is a number",
			fn:       func(tb focus.TB) bool { return focus.AssertEntryCreated(tb, metaV2(t), 42) },
			contains: []string{"key could not be read", "expected a base64 string"},
		},
		{
			name: "key is a decoded envelope",
			fn: func(tb focus.TB) bool {
				_, decoded, err := focus.Encode(env, "TransactionEnvelope")
				if err != nil {
					t.Fatalf("encoding the envelope: %v", err)
				}
				return focus.AssertEntryCreated(tb, metaV2(t), decoded)
			},
			contains: []string{"want a LedgerKey or a LedgerEntry"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.fn(tb) {
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

func offerKey(t *testing.T, addr string, id int64) xdr.LedgerKey {
	t.Helper()
	var key xdr.LedgerKey
	if err := key.SetOffer(mustAccountID(t, addr), uint64(id)); err != nil {
		t.Fatalf("building offer key: %v", err)
	}
	return key
}

func dataKey(t *testing.T, addr, name string) xdr.LedgerKey {
	t.Helper()
	var key xdr.LedgerKey
	if err := key.SetData(mustAccountID(t, addr), name); err != nil {
		t.Fatalf("building data key: %v", err)
	}
	return key
}

func claimableBalanceKey(t *testing.T, hash xdr.Hash) xdr.LedgerKey {
	t.Helper()
	var key xdr.LedgerKey
	id := xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &hash,
	}
	if err := key.SetClaimableBalance(id); err != nil {
		t.Fatalf("building claimable balance key: %v", err)
	}
	return key
}

func liquidityPoolKey(t *testing.T, id xdr.PoolId) xdr.LedgerKey {
	t.Helper()
	var key xdr.LedgerKey
	if err := key.SetLiquidityPool(id); err != nil {
		t.Fatalf("building liquidity pool key: %v", err)
	}
	return key
}

func contractCodeKey(t *testing.T, hash xdr.Hash) xdr.LedgerKey {
	t.Helper()
	var key xdr.LedgerKey
	if err := key.SetContractCode(hash); err != nil {
		t.Fatalf("building contract code key: %v", err)
	}
	return key
}

func ttlKey(t *testing.T, hash xdr.Hash) xdr.LedgerKey {
	t.Helper()
	var key xdr.LedgerKey
	if err := key.SetTtl(hash); err != nil {
		t.Fatalf("building ttl key: %v", err)
	}
	return key
}
