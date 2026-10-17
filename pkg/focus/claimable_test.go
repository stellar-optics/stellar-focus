package focus_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func TestAssertClaimableBalanceCreated(t *testing.T) {
	t.Parallel()

	id := claimableBalanceIDValue(0xab)
	entry := claimableBalanceEntryValue(id, nativeAsset(), 5_000_000,
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

		shared := claimableBalanceEntryValue(id, nativeAsset(), 100,
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

		credit := claimableBalanceEntryValue(claimableBalanceIDValue(0x11),
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
	hexID := hex.EncodeToString([]byte{
		0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab,
		0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab, 0xab,
	})
	entry := claimableBalanceEntryValue(id, nativeAsset(), 5_000_000,
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

		second := claimableBalanceEntryValue(claimableBalanceIDValue(0xcd),
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

	// The hex id is used by the claimed assertion's tests; assert here that
	// the id the failure message shortens is the one the protocol prints.
	t.Run("the rendered id is the balance hash", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		focus.AssertClaimableBalanceCreated(tb, meta, testSource, 1)
		if !strings.Contains(tb.Output(), hexID[:8]) {
			t.Errorf("message does not show the balance id %s:\n%s", hexID, tb.Output())
		}
	})
}

func TestAssertClaimableBalanceClaimed(t *testing.T) {
	t.Parallel()

	id := claimableBalanceIDValue(0xab)
	entry := claimableBalanceEntryValue(id, nativeAsset(), 5_000_000,
		unconditionalClaimant(t, testDest))
	key := ledgerKeyOf(t, entry)
	claimed := metaV2(t, []xdr.LedgerEntryChange{stateChange(entry), removedChange(key)})
	hexID := strings.Repeat("ab", 32)

	tests := []struct {
		name string
		id   focus.Value
	}{
		{"bare hex", hexID},
		{"hex with the 0x prefix", "0x" + hexID},
		{"hex in upper case", strings.ToUpper(hexID)},
		{"xdr balance id", id},
		{"pointer to an xdr balance id", &id},
		{"the ledger key", key},
		{"the ledger entry", entry},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertClaimableBalanceClaimed(tb, claimed, tc.id) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("the 36-byte xdr spelling of the id", func(t *testing.T) {
		t.Parallel()

		raw, err := id.MarshalBinary()
		if err != nil {
			t.Fatalf("marshalling the balance id: %v", err)
		}
		tb := faketb.New()
		if !focus.AssertClaimableBalanceClaimed(tb, claimed, hex.EncodeToString(raw)) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})
}

func TestAssertClaimableBalanceClaimedFailureMessages(t *testing.T) {
	t.Parallel()

	id := claimableBalanceIDValue(0xab)
	entry := claimableBalanceEntryValue(id, nativeAsset(), 5_000_000,
		unconditionalClaimant(t, testDest))
	hexID := strings.Repeat("ab", 32)

	t.Run("a balance that was only created is not claimed", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClaimableBalanceClaimed(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)}), hexID) {
			t.Fatal("assertion passed for a balance that was created, not claimed")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"claimable balance abababab…abababab was touched but not removed",
			"claiming a balance deletes its entry",
			"created  claimable balance abababababab",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an untouched balance names the balances that did change", func(t *testing.T) {
		t.Parallel()

		other := claimableBalanceEntryValue(claimableBalanceIDValue(0xcd),
			nativeAsset(), 1, unconditionalClaimant(t, testDest))
		otherKey := ledgerKeyOf(t, other)
		tb := faketb.New()
		if focus.AssertClaimableBalanceClaimed(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(other), removedChange(otherKey)}), hexID) {
			t.Fatal("assertion passed for an untouched balance")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"no claimable balance abababab…abababab was removed by this transaction",
			"deleted  claimable balance cdcdcdcdcd",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a meta with no claimable balance at all", func(t *testing.T) {
		t.Parallel()

		account := accountEntryValue(t, testSource, 100, xdr.Thresholds{1, 1, 1, 1})
		tb := faketb.New()
		if focus.AssertClaimableBalanceClaimed(tb,
			metaV2(t, []xdr.LedgerEntryChange{updatedChange(account)}), hexID) {
			t.Fatal("assertion passed with no claimable balance in the meta")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "no ledger entry change in this meta touches a claimable balance") {
			t.Errorf("message does not say the meta has no claimable balance:\n%s", msg)
		}
	})

	t.Run("unreadable balance ids are input errors", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name string
			id   focus.Value
			want string
		}{
			{"not hex", "zzzz", "is not hex"},
			{"wrong length", "abcd", "a claimable balance id is 32 bytes"},
			{"a nil pointer", (*xdr.ClaimableBalanceId)(nil), "nil *xdr.ClaimableBalanceId"},
			{"another entry's key", ledgerKeyOf(t, accountEntryValue(t, testSource, 1,
				xdr.Thresholds{1, 1, 1, 1})), "not a claimable balance"},
			{"an unsupported type", 42, "pass the balance id as hex"},
		}
		for _, tc := range cases {
			tb := faketb.New()
			if focus.AssertClaimableBalanceClaimed(tb,
				metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)}), tc.id) {
				t.Fatalf("%s: assertion passed with an unreadable id", tc.name)
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !strings.Contains(msg, "balance id could not be read") {
				t.Errorf("%s: message does not name the bad input:\n%s", tc.name, msg)
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("%s: message does not contain %q:\n%s", tc.name, tc.want, msg)
			}
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

			entry := claimableBalanceEntryValue(claimableBalanceIDValue(0x01),
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

	t.Run("the balance id as an xdr hash", func(t *testing.T) {
		t.Parallel()

		id := claimableBalanceIDValue(0xab)
		entry := claimableBalanceEntryValue(id, nativeAsset(), 1,
			unconditionalClaimant(t, testDest))
		tb := faketb.New()
		if !focus.AssertClaimableBalanceClaimed(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(entry), removedChange(ledgerKeyOf(t, entry))}),
			*id.V0) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a 36-byte id with an unknown discriminant", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClaimableBalanceClaimed(tb,
			metaV2(t, []xdr.LedgerEntryChange{}), "ffffffff"+strings.Repeat("ab", 32)) {
			t.Fatal("assertion passed with an unreadable id")
		}
		if msg := tb.Output(); !strings.Contains(msg, "is not a claimable balance id") {
			t.Errorf("message does not reject the id:\n%s", msg)
		}
	})

	t.Run("a balance with no claimants", func(t *testing.T) {
		t.Parallel()

		entry := claimableBalanceEntryValue(claimableBalanceIDValue(0x01), nativeAsset(), 100)
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

		entry := claimableBalanceEntryValue(claimableBalanceIDValue(0x02), nativeAsset(), 100,
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

		entry := claimableBalanceEntryValue(claimableBalanceIDValue(0x03), nativeAsset(), 100,
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

		entry := claimableBalanceEntryValue(claimableBalanceIDValue(0x04),
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
