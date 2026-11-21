package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func TestAssertClaimableBalanceCreated(t *testing.T) {
	t.Parallel()

	id := claimableBalanceIDValue(0xab)
	entry := claimableBalanceEntryWith(id, nativeAsset(), 5_000_000,
		unconditionalClaimant(t, testDest))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)})

	tests := []struct {
		name     string
		claimant focus.Value
	}{
		{"strkey claimant", testDest},
		{"account id", mustAccountID(t, testDest)},
		{"muxed account reduces to the account", mustMuxed(t, testDest)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertClaimableBalanceCreated(tb, meta, tc.claimant, 5_000_000) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("one of several claimants", func(t *testing.T) {
		t.Parallel()

		shared := claimableBalanceEntryWith(id, nativeAsset(), 100,
			unconditionalClaimant(t, testSource),
			beforeTimeClaimant(t, testDest, 1_700_000_000))

		for _, who := range []string{testSource, testDest} {
			tb := faketb.New()
			if !focus.AssertClaimableBalanceCreated(tb,
				metaV2(t, []xdr.LedgerEntryChange{createdChange(shared)}), who, 100) {
				t.Errorf("assertion failed for claimant %s:\n%s", who, tb.Output())
			}
		}
	})

	t.Run("a credit asset balance", func(t *testing.T) {
		t.Parallel()

		credit := claimableBalanceEntryWith(claimableBalanceIDValue(0x11),
			mustCreditAsset(t, "USDC"), 250, unconditionalClaimant(t, testDest))
		tb := faketb.New()
		if !focus.AssertClaimableBalanceCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(credit)}), testDest, 250) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a restored balance counts as created", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertClaimableBalanceCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{restoredChange(entry)}), testDest, 5_000_000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("balances in a v0, v1 and v3 meta are all found", func(t *testing.T) {
		t.Parallel()

		changes := []xdr.LedgerEntryChange{createdChange(entry)}
		for name, m := range map[string]string{
			"v0": metaV0(t, changes),
			"v1": metaV1(t, nil, changes),
			"v3": metaV3WithTx(t, nil, nil, changes),
		} {
			tb := faketb.New()
			if !focus.AssertClaimableBalanceCreated(tb, m, testDest, 5_000_000) {
				t.Errorf("%s meta: assertion failed unexpectedly:\n%s", name, tb.Output())
			}
		}
	})
}

func TestAssertClaimableBalanceCreatedFailureMessages(t *testing.T) {
	t.Parallel()

	id := claimableBalanceIDValue(0xab)
	entry := claimableBalanceEntryWith(id, nativeAsset(), 5_000_000,
		beforeTimeClaimant(t, testDest, 1_700_000_000))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)})

	t.Run("no balance created names what the transaction did", func(t *testing.T) {
		t.Parallel()

		account := accountEntryValue(t, testSource, 100, xdr.Thresholds{1, 1, 1, 1})
		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{updatedChange(account)}), testDest, 1) {
			t.Fatal("assertion passed with no claimable balance in the meta")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertClaimableBalanceCreated: the transaction created no claimable balance",
			"want 1 (0.0000001) claimable by account GAPB7…WYXZ",
			"ledger changes present (1):",
			"account GCJNH…ZST6",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("wrong claimant lists the balances and their predicates", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb, meta, testSource, 5_000_000) {
			t.Fatal("assertion passed for an account that cannot claim")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"created 1 claimable balance, none of them claimable by account GCJNH…ZST6",
			"claimable balances created (1):",
			"created abababab…abababab — native 5000000 (0.5000000 XLM)",
			"claimant GAPB7…WYXZ, before unix time 1700000000",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("wrong amount names the balance and both amounts", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb, meta, testDest, 4_000_000) {
			t.Fatal("assertion passed with the wrong amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"claimable balance abababab…abababab for account GAPB7…WYXZ",
			"holds 5000000 (0.5000000 XLM), want 4000000 (0.4000000 XLM)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("several balances for one claimant list the amounts", func(t *testing.T) {
		t.Parallel()

		second := claimableBalanceEntryWith(claimableBalanceIDValue(0xcd),
			nativeAsset(), 7_000_000, unconditionalClaimant(t, testDest))
		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb, metaV2(t,
			[]xdr.LedgerEntryChange{createdChange(entry), createdChange(second)}),
			testDest, 6_000_000) {
			t.Fatal("assertion passed with the wrong amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"account GAPB7…WYXZ can claim 2 claimable balances",
			"holding 5000000 (0.5000000 XLM), 7000000 (0.7000000 XLM)",
			"want one of 6000000 (0.6000000)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a bad claimant is an input error", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb, meta, "not-an-account", 1) {
			t.Fatal("assertion passed with an unreadable claimant")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "claimant could not be read as an account") {
			t.Errorf("message does not name the bad input:\n%s", msg)
		}
	})

	t.Run("a non-meta value is reported as such", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb, buildEnvelope(t, paymentOp(t, 1)), testDest, 1) {
			t.Fatal("assertion passed with an envelope")
		}
		if msg := tb.Output(); !strings.Contains(msg, "could not be read") &&
			!strings.Contains(msg, "want a TransactionMeta") {
			t.Errorf("message does not reject the envelope:\n%s", msg)
		}
	})
}

// TestClaimPredicateRendering covers the predicate forms a failure listing
// has to describe, including the composite ones.
func TestClaimPredicateRendering(t *testing.T) {
	t.Parallel()

	abs := xdr.Int64(1_700_000_000)
	rel := xdr.Int64(3600)
	absBefore := xdr.ClaimPredicate{
		Type:      xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime,
		AbsBefore: &abs,
	}
	relBefore := xdr.ClaimPredicate{
		Type:      xdr.ClaimPredicateTypeClaimPredicateBeforeRelativeTime,
		RelBefore: &rel,
	}
	notInner := &absBefore
	tests := []struct {
		name      string
		predicate xdr.ClaimPredicate
		want      string
	}{
		{"unconditional", xdr.ClaimPredicate{
			Type: xdr.ClaimPredicateTypeClaimPredicateUnconditional}, "unconditional"},
		{"absolute", absBefore, "before unix time 1700000000"},
		{"relative", relBefore, "within 3600s of creation"},
		{"not", xdr.ClaimPredicate{
			Type:         xdr.ClaimPredicateTypeClaimPredicateNot,
			NotPredicate: &notInner,
		}, "not (before unix time 1700000000)"},
		{"and", xdr.ClaimPredicate{
			Type:          xdr.ClaimPredicateTypeClaimPredicateAnd,
			AndPredicates: &[]xdr.ClaimPredicate{absBefore, relBefore},
		}, "(before unix time 1700000000 and within 3600s of creation)"},
		{"or", xdr.ClaimPredicate{
			Type:         xdr.ClaimPredicateTypeClaimPredicateOr,
			OrPredicates: &[]xdr.ClaimPredicate{absBefore, relBefore},
		}, "(before unix time 1700000000 or within 3600s of creation)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entry := claimableBalanceEntryWith(claimableBalanceIDValue(0x01),
				nativeAsset(), 100, xdr.Claimant{
					Type: xdr.ClaimantTypeClaimantTypeV0,
					V0: &xdr.ClaimantV0{
						Destination: mustAccountID(t, testDest),
						Predicate:   tc.predicate,
					},
				})

			// A wrong amount is the shortest route to the listing.
			tb := faketb.New()
			if focus.AssertClaimableBalanceCreated(tb,
				metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)}), testDest, 1) {
				t.Fatal("assertion passed with the wrong amount")
			}
			if !strings.Contains(tb.Output(), tc.want) {
				t.Errorf("listing does not render the predicate as %q:\n%s", tc.want, tb.Output())
			}
		})
	}
}

// TestClaimableBalanceEdgeCases covers the forms of a balance that the
// failure listing has to survive: a balance with no claimants, a claimant
// arm this build cannot read, and an unknown predicate.
func TestClaimableBalanceEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("a balance with no claimants", func(t *testing.T) {
		t.Parallel()

		entry := claimableBalanceEntryWith(claimableBalanceIDValue(0x01), nativeAsset(), 100)
		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)}), testDest, 100) {
			t.Fatal("assertion passed for a balance nobody can claim")
		}
		if msg := tb.Output(); !strings.Contains(msg, "claimants: none") {
			t.Errorf("listing does not report the absent claimants:\n%s", msg)
		}
	})

	t.Run("an unreadable claimant arm is kept in the listing", func(t *testing.T) {
		t.Parallel()

		entry := claimableBalanceEntryWith(claimableBalanceIDValue(0x02), nativeAsset(), 100,
			xdr.Claimant{Type: xdr.ClaimantType(99)})
		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb,
			decodedMeta([]xdr.LedgerEntryChange{createdChange(entry)}), testDest, 100) {
			t.Fatal("assertion passed for a claimant that cannot be read")
		}
		msg := tb.Output()
		for _, want := range []string{"claimant (unreadable)", "claimant type"} {
			if !strings.Contains(msg, want) {
				t.Errorf("listing does not keep the unreadable claimant (%q):\n%s", want, msg)
			}
		}
	})

	t.Run("an unknown predicate falls back to its type name", func(t *testing.T) {
		t.Parallel()

		entry := claimableBalanceEntryWith(claimableBalanceIDValue(0x03), nativeAsset(), 100,
			xdr.Claimant{
				Type: xdr.ClaimantTypeClaimantTypeV0,
				V0: &xdr.ClaimantV0{
					Destination: mustAccountID(t, testDest),
					Predicate:   xdr.ClaimPredicate{Type: xdr.ClaimPredicateType(99)},
				},
			})
		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb,
			decodedMeta([]xdr.LedgerEntryChange{createdChange(entry)}), testDest, 1) {
			t.Fatal("assertion passed with the wrong amount")
		}
		if msg := tb.Output(); !strings.Contains(msg, "claimant GAPB7…WYXZ, ") {
			t.Errorf("listing does not render the unknown predicate:\n%s", msg)
		}
	})

	t.Run("a restored balance is labelled restored in the listing", func(t *testing.T) {
		t.Parallel()

		entry := claimableBalanceEntryWith(claimableBalanceIDValue(0x04),
			mustCreditAsset(t, "USDC"), 100, unconditionalClaimant(t, testDest))
		tb := faketb.New()
		if focus.AssertClaimableBalanceCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{restoredChange(entry)}), testDest, 1) {
			t.Fatal("assertion passed with the wrong amount")
		}
		msg := tb.Output()
		for _, want := range []string{
			"restored 04040404…04040404 — " + usdcAsset,
			// A credit asset carries no XLM unit in the rendered amount.
			"holds 100 (0.0000100), want 1 (0.0000001)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})
}

func TestAssertClaimPredicateTree(t *testing.T) {
	t.Parallel()

	unconditional := xdr.ClaimPredicate{
		Type: xdr.ClaimPredicateTypeClaimPredicateUnconditional,
	}

	absTime := int64(1_700_000_000)
	absPred := xdr.ClaimPredicate{
		Type:      xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime,
		AbsBefore: (*xdr.Int64)(&absTime),
	}

	relTime := int64(300)
	relPred := xdr.ClaimPredicate{
		Type:      xdr.ClaimPredicateTypeClaimPredicateBeforeRelativeTime,
		RelBefore: (*xdr.Int64)(&relTime),
	}

	notAbsPred := xdr.ClaimPredicate{
		Type:         xdr.ClaimPredicateTypeClaimPredicateNot,
		NotPredicate: func() **xdr.ClaimPredicate { p := &absPred; return &p }(),
	}

	orPred := xdr.ClaimPredicate{
		Type:         xdr.ClaimPredicateTypeClaimPredicateOr,
		OrPredicates: &[]xdr.ClaimPredicate{unconditional, relPred},
	}

	andPred := xdr.ClaimPredicate{
		Type:          xdr.ClaimPredicateTypeClaimPredicateAnd,
		AndPredicates: &[]xdr.ClaimPredicate{notAbsPred, orPred},
	}

	claimant := xdr.Claimant{
		Type: xdr.ClaimantTypeClaimantTypeV0,
		V0: &xdr.ClaimantV0{
			Destination: mustAccountID(t, testDest),
			Predicate:   andPred,
		},
	}

	entry := claimableBalanceEntryWith(claimableBalanceIDValue(0x55), nativeAsset(), 1000, claimant)
	op := xdr.CreateClaimableBalanceOp{
		Asset:     nativeAsset(),
		Amount:    1000,
		Claimants: []xdr.Claimant{claimant},
	}

	t.Run("matches complex nested predicate on entry and operation", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if !focus.AssertClaimPredicateTree(tb, entry, 0, andPred) {
			t.Errorf("assertion failed for entry: %s", tb.Output())
		}
		tb2 := faketb.New()
		if !focus.AssertClaimPredicateTree(tb2, op, 0, andPred) {
			t.Errorf("assertion failed for op: %s", tb2.Output())
		}
		// Require mirror test
		require.ClaimPredicateTree(t, entry, 0, andPred)
	})

	t.Run("failure shows structural path diff and human-readable timestamps", func(t *testing.T) {
		t.Parallel()
		deviatingAbsTime := int64(1_600_000_000)
		deviatingAbsPred := xdr.ClaimPredicate{
			Type:      xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime,
			AbsBefore: (*xdr.Int64)(&deviatingAbsTime),
		}
		deviatingNot := xdr.ClaimPredicate{
			Type:         xdr.ClaimPredicateTypeClaimPredicateNot,
			NotPredicate: func() **xdr.ClaimPredicate { p := &deviatingAbsPred; return &p }(),
		}
		deviatingAnd := xdr.ClaimPredicate{
			Type:          xdr.ClaimPredicateTypeClaimPredicateAnd,
			AndPredicates: &[]xdr.ClaimPredicate{deviatingNot, orPred},
		}

		tb := faketb.New()
		if focus.AssertClaimPredicateTree(tb, entry, 0, deviatingAnd) {
			t.Fatal("assertion unexpectedly passed for deviating predicate")
		}
		msg := tb.Output()
		for _, want := range []string{
			"AssertClaimPredicateTree",
			`at path "$.and[0].not"`,
			"1600000000",
			"1700000000",
			"2023-11-14T22:13:20Z",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message missing %q:\n%s", want, msg)
			}
		}
	})

	t.Run("claimantIndex out of bounds reports informative error", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if focus.AssertClaimPredicateTree(tb, entry, 5, andPred) {
			t.Fatal("assertion unexpectedly passed for out of bounds index")
		}
		if !strings.Contains(tb.Output(), "claimantIndex 5 out of bounds") {
			t.Errorf("unexpected error message: %s", tb.Output())
		}
	})
}
