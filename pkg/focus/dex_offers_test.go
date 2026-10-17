package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func TestAssertOfferCreated(t *testing.T) {
	t.Parallel()

	usdc := mustCreditAsset(t, "USDC")
	offer := offerEntryValue(t, testSource, 4242, usdc, nativeAsset(), 1000, priceValue(1, 2))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(offer)})

	tests := []struct {
		name string
		want focus.Offer
	}{
		{"every field", focus.Offer{
			Seller:  testSource,
			OfferID: 4242,
			Selling: usdcAsset,
			Buying:  "native",
			Amount:  1000,
			Price:   "1/2",
		}},
		{"assets only", focus.Offer{Selling: usdcAsset, Buying: "native"}},
		{"an unreduced price is the same price", focus.Offer{
			Selling: usdcAsset, Buying: "native", Price: "2/4"}},
		{"an xdr price", focus.Offer{
			Selling: usdcAsset, Buying: "native", Price: priceValue(1, 2)}},
		{"xdr assets", focus.Offer{Selling: usdc, Buying: nativeAsset()}},
		{"a muxed seller reduces to the account", focus.Offer{
			Seller: mustMuxed(t, testSource), Selling: usdcAsset}},
		{"an empty query matches any offer", focus.Offer{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertOfferCreated(tb, meta, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("one of several offers", func(t *testing.T) {
		t.Parallel()

		eur := mustCreditAsset(t, "EUR")
		second := offerEntryValue(t, testDest, 77, eur, nativeAsset(), 500, priceValue(3, 1))
		m := metaV2(t, []xdr.LedgerEntryChange{createdChange(offer), createdChange(second)})

		tb := faketb.New()
		if !focus.AssertOfferCreated(tb, m, focus.Offer{
			Seller: testDest, Selling: eurAsset, Buying: "native", Amount: 500, Price: "3/1"}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a restored offer counts as created", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertOfferCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{restoredChange(offer)}),
			focus.Offer{OfferID: 4242}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})
}

func TestAssertOfferCreatedFailureMessages(t *testing.T) {
	t.Parallel()

	usdc := mustCreditAsset(t, "USDC")
	offer := offerEntryValue(t, testSource, 4242, usdc, nativeAsset(), 1000, priceValue(1, 2))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(offer)})

	t.Run("one offer created names every field that differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertOfferCreated(tb, meta, focus.Offer{
			Seller:  testDest,
			OfferID: 1,
			Selling: usdcAsset,
			Buying:  eurAsset,
			Amount:  2000,
			Price:   "1/3",
		}) {
			t.Fatal("assertion passed for a different offer")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertOfferCreated: the offer this transaction created is not an offer",
			"5 differences",
			"~ seller is GCJNH…ZST6, want GAPB7…WYXZ",
			"~ offer id is 4242, want 1",
			"~ buying native, want " + eurAsset,
			"~ amount is 1000 (0.0001000), want 2000 (0.0002000)",
			"~ price is 1/2, want 1/3",
			"offers created (1):",
			"created offer #4242 by GCJNH…ZST6: selling " + usdcAsset +
				" for native, amount 1000 (0.0001000), price 1/2",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
		if strings.Contains(msg, "~ selling") {
			t.Errorf("message names a field that matched:\n%s", msg)
		}
	})

	t.Run("no offer created points at the trade assertion", func(t *testing.T) {
		t.Parallel()

		account := accountEntryValue(t, testSource, 100, xdr.Thresholds{1, 1, 1, 1})
		tb := faketb.New()
		if focus.AssertOfferCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{updatedChange(account)}),
			focus.Offer{Selling: usdcAsset, Buying: "native", Amount: 1000}) {
			t.Fatal("assertion passed with no offer in the meta")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"the transaction created no offer",
			"want an offer selling " + usdcAsset + " for native, amount 1000 (0.0001000)",
			"An offer that was filled completely on submission never reaches the ledger",
			"assert the trade with AssertTradeExecuted instead",
			"no ledger entry change in this meta touches an offer",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("several offers created and none matching", func(t *testing.T) {
		t.Parallel()

		second := offerEntryValue(t, testDest, 77, mustCreditAsset(t, "EUR"),
			nativeAsset(), 500, priceValue(3, 1))
		tb := faketb.New()
		if focus.AssertOfferCreated(tb, metaV2(t,
			[]xdr.LedgerEntryChange{createdChange(offer), createdChange(second)}),
			focus.Offer{Selling: usdcAsset, Buying: "native", Amount: 999}) {
			t.Fatal("assertion passed with no matching offer")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"none of the 2 offers this transaction created is an offer selling " +
				usdcAsset + " for native, amount 999 (0.0000999)",
			"offers created (2):",
			"created offer #77 by GAPB7…WYXZ",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a modified offer is not a created one", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertOfferCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(offer), updatedChange(offer)}),
			focus.Offer{OfferID: 4242}) {
			t.Fatal("assertion passed for an offer that was only modified")
		}
		if msg := tb.Output(); !strings.Contains(msg, "the transaction created no offer") {
			t.Errorf("message does not report the absence of a creation:\n%s", msg)
		}
	})

	t.Run("unreadable fields are input errors", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name string
			want focus.Offer
			text string
		}{
			{"seller", focus.Offer{Seller: "nope"}, "Seller could not be read as an account"},
			{"selling", focus.Offer{Selling: "nope"}, "Selling could not be read as an asset"},
			{"buying", focus.Offer{Buying: 42}, "Buying could not be read as an asset"},
			{"price without a slash", focus.Offer{Price: "0.5"}, "a price is a ratio written \"N/D\""},
			{"price numerator", focus.Offer{Price: "x/2"}, "invalid numerator"},
			{"price denominator", focus.Offer{Price: "1/x"}, "invalid denominator"},
			{"price with a zero denominator", focus.Offer{Price: "1/0"},
				"a price cannot have a denominator of zero"},
			{"negative price", focus.Offer{Price: priceValue(-1, 2)}, "a price cannot be negative"},
			{"price of an unsupported type", focus.Offer{Price: 42},
				"pass a price as \"N/D\" or an xdr.Price"},
			{"a nil price pointer", focus.Offer{Price: (*xdr.Price)(nil)}, "nil *xdr.Price"},
		}
		for _, tc := range cases {
			tb := faketb.New()
			if focus.AssertOfferCreated(tb, meta, tc.want) {
				t.Fatalf("%s: assertion passed with an unreadable field", tc.name)
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !strings.Contains(msg, tc.text) {
				t.Errorf("%s: message does not contain %q:\n%s", tc.name, tc.text, msg)
			}
		}
	})
}

func TestAssertTradeExecutedFromResult(t *testing.T) {
	t.Parallel()

	usdc := mustCreditAsset(t, "USDC")
	atom := orderBookClaim(t, testDest, 99, usdc, 1000, nativeAsset(), 500)
	result := buildResult(t, xdr.TransactionResultCodeTxSuccess, manageSellOfferResult(atom))

	tests := []struct {
		name string
		want focus.Trade
	}{
		{"every field", focus.Trade{
			Seller:       testDest,
			OfferID:      99,
			Sold:         usdcAsset,
			Bought:       "native",
			AmountSold:   1000,
			AmountBought: 500,
		}},
		{"amounts only", focus.Trade{AmountSold: 1000, AmountBought: 500}},
		{"assets only", focus.Trade{Sold: usdcAsset, Bought: "native"}},
		{"an empty query matches any trade", focus.Trade{}},
		{"xdr assets", focus.Trade{Sold: usdc, Bought: nativeAsset()}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertTradeExecuted(tb, result, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("a trade against a liquidity pool", func(t *testing.T) {
		t.Parallel()

		id := poolIDValue(0x7a)
		pooled := buildResult(t, xdr.TransactionResultCodeTxSuccess,
			manageSellOfferResult(poolClaim(id, usdc, 300, nativeAsset(), 150)))

		tb := faketb.New()
		if !focus.AssertTradeExecuted(tb, pooled, focus.Trade{
			Pool: id, Sold: usdcAsset, Bought: "native", AmountSold: 300, AmountBought: 150}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a pre-protocol-11 claim atom", func(t *testing.T) {
		t.Parallel()

		aid := mustAccountID(t, testDest)
		if aid.Ed25519 == nil {
			t.Fatal("the test account has no ed25519 key")
		}
		v0 := buildResult(t, xdr.TransactionResultCodeTxSuccess,
			manageSellOfferResult(xdr.ClaimAtom{
				Type: xdr.ClaimAtomTypeClaimAtomTypeV0,
				V0: &xdr.ClaimOfferAtomV0{
					SellerEd25519: *aid.Ed25519,
					OfferId:       99,
					AssetSold:     usdc,
					AmountSold:    1000,
					AssetBought:   nativeAsset(),
					AmountBought:  500,
				},
			}))

		tb := faketb.New()
		if !focus.AssertTradeExecuted(tb, v0, focus.Trade{Seller: testDest, OfferID: 99}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("the xdr result value and a pointer to it", func(t *testing.T) {
		t.Parallel()

		res := xdr.TransactionResult{
			FeeCharged: 100,
			Result: xdr.TransactionResultResult{
				Code:    xdr.TransactionResultCodeTxSuccess,
				Results: &[]xdr.OperationResult{manageSellOfferResult(atom)},
			},
		}
		for _, v := range []focus.Value{res, &res} {
			tb := faketb.New()
			if !focus.AssertTradeExecuted(tb, v, focus.Trade{AmountSold: 1000}) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		}
	})

	t.Run("claims from every operation that can match", func(t *testing.T) {
		t.Parallel()

		claim := []xdr.ClaimAtom{orderBookClaim(t, testDest, 1, usdc, 10, nativeAsset(), 5)}
		ops := []xdr.OperationResult{
			{Code: xdr.OperationResultCodeOpInner, Tr: &xdr.OperationResultTr{
				Type: xdr.OperationTypeCreatePassiveSellOffer,
				CreatePassiveSellOfferResult: &xdr.ManageSellOfferResult{
					Code: xdr.ManageSellOfferResultCodeManageSellOfferSuccess,
					Success: &xdr.ManageOfferSuccessResult{
						OffersClaimed: claim,
						Offer: xdr.ManageOfferSuccessResultOffer{
							Effect: xdr.ManageOfferEffectManageOfferDeleted,
						},
					},
				}}},
			{Code: xdr.OperationResultCodeOpInner, Tr: &xdr.OperationResultTr{
				Type: xdr.OperationTypeManageBuyOffer,
				ManageBuyOfferResult: &xdr.ManageBuyOfferResult{
					Code: xdr.ManageBuyOfferResultCodeManageBuyOfferSuccess,
					Success: &xdr.ManageOfferSuccessResult{
						OffersClaimed: claim,
						Offer: xdr.ManageOfferSuccessResultOffer{
							Effect: xdr.ManageOfferEffectManageOfferDeleted,
						},
					},
				}}},
			{Code: xdr.OperationResultCodeOpInner, Tr: &xdr.OperationResultTr{
				Type: xdr.OperationTypePathPaymentStrictReceive,
				PathPaymentStrictReceiveResult: &xdr.PathPaymentStrictReceiveResult{
					Code: xdr.PathPaymentStrictReceiveResultCodePathPaymentStrictReceiveSuccess,
					Success: &xdr.PathPaymentStrictReceiveResultSuccess{
						Offers: claim,
						Last: xdr.SimplePaymentResult{
							Destination: mustAccountID(t, testDest),
							Asset:       nativeAsset(),
							Amount:      5,
						},
					}}}},
			{Code: xdr.OperationResultCodeOpInner, Tr: &xdr.OperationResultTr{
				Type: xdr.OperationTypePathPaymentStrictSend,
				PathPaymentStrictSendResult: &xdr.PathPaymentStrictSendResult{
					Code: xdr.PathPaymentStrictSendResultCodePathPaymentStrictSendSuccess,
					Success: &xdr.PathPaymentStrictSendResultSuccess{
						Offers: claim,
						Last: xdr.SimplePaymentResult{
							Destination: mustAccountID(t, testDest),
							Asset:       nativeAsset(),
							Amount:      5,
						},
					}}}},
		}

		for i, op := range ops {
			tb := faketb.New()
			if !focus.AssertTradeExecuted(tb,
				buildResult(t, xdr.TransactionResultCodeTxSuccess, op),
				focus.Trade{Seller: testDest, OfferID: 1, AmountSold: 10, AmountBought: 5}) {
				t.Errorf("operation %d: assertion failed unexpectedly:\n%s", i, tb.Output())
			}
		}
	})
}

func TestAssertTradeExecutedFromMeta(t *testing.T) {
	t.Parallel()

	usdc := mustCreditAsset(t, "USDC")
	before := offerEntryValue(t, testDest, 99, usdc, nativeAsset(), 1000, priceValue(1, 2))
	after := offerEntryValue(t, testDest, 99, usdc, nativeAsset(), 400, priceValue(1, 2))

	t.Run("a partial fill measures what the offer gave up", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertTradeExecuted(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			focus.Trade{Seller: testDest, OfferID: 99, Sold: usdcAsset,
				Bought: "native", AmountSold: 600}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a full fill removes the offer", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertTradeExecuted(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(before), removedChange(ledgerKeyOf(t, before))}),
			focus.Trade{Seller: testDest, AmountSold: 1000}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("an offer the transaction created gave nothing up", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(before)}), focus.Trade{}) {
			t.Fatal("assertion passed for a newly created offer")
		}
		if msg := tb.Output(); !strings.Contains(msg,
			"this transaction meta records no trade") {
			t.Errorf("message does not report the absence of a trade:\n%s", msg)
		}
	})

	t.Run("an offer that grew is not a trade", func(t *testing.T) {
		t.Parallel()

		bigger := offerEntryValue(t, testDest, 99, usdc, nativeAsset(), 2000, priceValue(1, 2))
		tb := faketb.New()
		if focus.AssertTradeExecuted(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(bigger)}),
			focus.Trade{}) {
			t.Fatal("assertion passed for an offer that was raised")
		}
		if tb.Output() == "" {
			t.Error("no failure was reported")
		}
	})

	t.Run("a meta cannot answer the bought amount", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			focus.Trade{AmountSold: 600, AmountBought: 300}) {
			t.Fatal("assertion passed on an amount a meta cannot establish")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"a transaction meta records the offers that changed, not what they were paid",
			"so AmountBought cannot be checked against one",
			"pass the transaction result, whose claim atoms carry both amounts",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("the xdr meta value and a pointer to it", func(t *testing.T) {
		t.Parallel()

		m := xdr.TransactionMeta{
			V: 2,
			V2: &xdr.TransactionMetaV2{
				Operations: []xdr.OperationMeta{{Changes: []xdr.LedgerEntryChange{
					stateChange(before), updatedChange(after),
				}}},
			},
		}
		for _, v := range []focus.Value{m, &m} {
			tb := faketb.New()
			if !focus.AssertTradeExecuted(tb, v, focus.Trade{AmountSold: 600}) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		}
	})
}

func TestAssertTradeExecutedFailureMessages(t *testing.T) {
	t.Parallel()

	usdc := mustCreditAsset(t, "USDC")
	atom := orderBookClaim(t, testDest, 99, usdc, 1000, nativeAsset(), 500)
	result := buildResult(t, xdr.TransactionResultCodeTxSuccess, manageSellOfferResult(atom))

	t.Run("no matching trade lists the trades recorded", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb, result, focus.Trade{AmountSold: 900}) {
			t.Fatal("assertion passed with the wrong amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertTradeExecuted: no trade in this transaction result is a trade " +
				"amount sold 900 (0.0000900)",
			"trades recorded (1):",
			"[0] account GAPB7…WYXZ offer #99 (operation 0) sold 1000 (0.0001000) " +
				usdcAsset + " for 500 (0.0000500) native",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a result with no claims at all", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb,
			buildResult(t, xdr.TransactionResultCodeTxSuccess,
				paymentResult(xdr.PaymentResultCodePaymentSuccess)),
			focus.Trade{Sold: usdcAsset}) {
			t.Fatal("assertion passed with no trade in the result")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"this transaction result records no trade",
			"want a trade selling " + usdcAsset + " for any asset",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a meta trade says the bought amount was not recorded", func(t *testing.T) {
		t.Parallel()

		before := offerEntryValue(t, testDest, 99, usdc, nativeAsset(), 1000, priceValue(1, 2))
		after := offerEntryValue(t, testDest, 99, usdc, nativeAsset(), 400, priceValue(1, 2))
		tb := faketb.New()
		if focus.AssertTradeExecuted(tb,
			metaV2(t, []xdr.LedgerEntryChange{stateChange(before), updatedChange(after)}),
			focus.Trade{AmountSold: 1}) {
			t.Fatal("assertion passed with the wrong amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"no trade in this transaction meta is a trade amount sold 1 (0.0000001)",
			"sold 600 (0.0000600) " + usdcAsset + " for native (amount not recorded in a meta)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a fully filled meta trade is named as such", func(t *testing.T) {
		t.Parallel()

		before := offerEntryValue(t, testDest, 99, usdc, nativeAsset(), 1000, priceValue(1, 2))
		tb := faketb.New()
		if focus.AssertTradeExecuted(tb, metaV2(t,
			[]xdr.LedgerEntryChange{stateChange(before), removedChange(ledgerKeyOf(t, before))}),
			focus.Trade{AmountSold: 1}) {
			t.Fatal("assertion passed with the wrong amount")
		}
		if msg := tb.Output(); !strings.Contains(msg, "offer fully filled") {
			t.Errorf("message does not report the offer as fully filled:\n%s", msg)
		}
	})

	t.Run("a pool trade is named by its pool", func(t *testing.T) {
		t.Parallel()

		id := poolIDValue(0x7a)
		pooled := buildResult(t, xdr.TransactionResultCodeTxSuccess,
			manageSellOfferResult(poolClaim(id, usdc, 300, nativeAsset(), 150)))
		tb := faketb.New()
		if focus.AssertTradeExecuted(tb, pooled, focus.Trade{Seller: testDest}) {
			t.Fatal("assertion passed for a pool trade matched against a seller")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"no trade in this transaction result is a trade against account GAPB7…WYXZ",
			"[0] pool 7a7a7a7a…7a7a7a7a (operation 0) sold 300 (0.0000300)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a value that is neither a result nor a meta", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb, buildEnvelope(t, paymentOp(t, 1)), focus.Trade{}) {
			t.Fatal("assertion passed with an envelope")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"want a TransactionResult or a TransactionMeta",
			"a trade is recorded in a result's claim atoms",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("nil xdr pointers are input errors", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name  string
			value focus.Value
			want  string
		}{
			{"result", (*xdr.TransactionResult)(nil), "nil *xdr.TransactionResult"},
			{"meta", (*xdr.TransactionMeta)(nil), "nil *xdr.TransactionMeta"},
		}
		for _, tc := range cases {
			tb := faketb.New()
			if focus.AssertTradeExecuted(tb, tc.value, focus.Trade{}) {
				t.Fatalf("%s: assertion passed with a nil pointer", tc.name)
			}
			if msg := tb.Output(); !strings.Contains(msg, tc.want) {
				t.Errorf("%s: message does not name the bad input:\n%s", tc.name, msg)
			}
		}
	})

	t.Run("unreadable fields are input errors", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name string
			want focus.Trade
			text string
		}{
			{"seller", focus.Trade{Seller: "nope"}, "Seller could not be read as an account"},
			{"pool", focus.Trade{Pool: "nope"}, "pool could not be read"},
			{"sold", focus.Trade{Sold: "nope"}, "Sold could not be read as an asset"},
			{"bought", focus.Trade{Bought: 42}, "Bought could not be read as an asset"},
		}
		for _, tc := range cases {
			tb := faketb.New()
			if focus.AssertTradeExecuted(tb, result, tc.want) {
				t.Fatalf("%s: assertion passed with an unreadable field", tc.name)
			}
			if msg := tb.Output(); !strings.Contains(msg, tc.text) {
				t.Errorf("%s: message does not contain %q:\n%s", tc.name, tc.text, msg)
			}
		}
	})
}

// TestDexEdgeCases covers the claim atom and price forms the main tests do
// not reach.
func TestDexEdgeCases(t *testing.T) {
	t.Parallel()

	usdc := mustCreditAsset(t, "USDC")
	offer := offerEntryValue(t, testSource, 4242, usdc, nativeAsset(), 1000, priceValue(1, 2))
	meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(offer)})

	t.Run("a decoded lens price value", func(t *testing.T) {
		t.Parallel()

		p := priceValue(1, 2)
		tb := faketb.New()
		if !focus.AssertOfferCreated(tb, meta, focus.Offer{
			Price: &lens.Value{Type: "Price", Raw: &p}}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("a decoded value that is not a price", func(t *testing.T) {
		t.Parallel()

		cases := []focus.Value{
			&lens.Value{Type: "Asset", Raw: &usdc},
			(*lens.Value)(nil),
		}
		for _, v := range cases {
			tb := faketb.New()
			if focus.AssertOfferCreated(tb, meta, focus.Offer{Price: v}) {
				t.Fatalf("assertion passed with %#v as a price", v)
			}
			if msg := tb.Output(); !strings.Contains(msg, "Price could not be read") {
				t.Errorf("message does not name the bad price:\n%s", msg)
			}
		}
	})

	t.Run("a wrong selling asset is named", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertOfferCreated(tb, meta, focus.Offer{Selling: eurAsset}) {
			t.Fatal("assertion passed for a different asset")
		}
		if msg := tb.Output(); !strings.Contains(msg,
			"~ selling "+usdcAsset+", want "+eurAsset) {
			t.Errorf("message does not name the selling asset:\n%s", msg)
		}
	})

	t.Run("a restored offer is labelled restored in the listing", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertOfferCreated(tb,
			metaV2(t, []xdr.LedgerEntryChange{restoredChange(offer)}),
			focus.Offer{OfferID: 1}) {
			t.Fatal("assertion passed for a different offer id")
		}
		if msg := tb.Output(); !strings.Contains(msg, "restored offer #4242") {
			t.Errorf("listing does not label the offer as restored:\n%s", msg)
		}
	})

	t.Run("a failed operation carries no claims", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb,
			buildResult(t, xdr.TransactionResultCodeTxFailed, xdr.OperationResult{
				Code: xdr.OperationResultCodeOpNoAccount,
			}), focus.Trade{}) {
			t.Fatal("assertion passed for a failed operation")
		}
		if msg := tb.Output(); !strings.Contains(msg, "records no trade") {
			t.Errorf("message does not report the absence of a trade:\n%s", msg)
		}
	})

	t.Run("an unknown claim atom arm is skipped", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb,
			decodedResult(manageSellOfferResult(xdr.ClaimAtom{Type: xdr.ClaimAtomType(99)})),
			focus.Trade{}) {
			t.Fatal("assertion passed for a claim atom that cannot be read")
		}
		if msg := tb.Output(); !strings.Contains(msg, "records no trade") {
			t.Errorf("message does not report the absence of a trade:\n%s", msg)
		}
	})

	t.Run("a pool trade matched by its pool and offer id", func(t *testing.T) {
		t.Parallel()

		id := poolIDValue(0x7a)
		other := poolIDValue(0x01)
		pooled := buildResult(t, xdr.TransactionResultCodeTxSuccess,
			manageSellOfferResult(poolClaim(id, usdc, 300, nativeAsset(), 150)))

		tb := faketb.New()
		if focus.AssertTradeExecuted(tb, pooled, focus.Trade{Pool: other}) {
			t.Fatal("assertion passed for a different pool")
		}
		tb = faketb.New()
		if focus.AssertTradeExecuted(tb, pooled, focus.Trade{OfferID: 7}) {
			t.Fatal("assertion passed for an offer id a pool trade has not got")
		}
		tb = faketb.New()
		if focus.AssertTradeExecuted(tb, pooled, focus.Trade{Sold: eurAsset}) {
			t.Fatal("assertion passed for a different sold asset")
		}
		tb = faketb.New()
		if focus.AssertTradeExecuted(tb, pooled, focus.Trade{Bought: eurAsset}) {
			t.Fatal("assertion passed for a different bought asset")
		}
		tb = faketb.New()
		if focus.AssertTradeExecuted(tb, pooled, focus.Trade{AmountBought: 1}) {
			t.Fatal("assertion passed for a different bought amount")
		}
	})
}
