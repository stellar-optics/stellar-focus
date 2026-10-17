package focus_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Real mainnet addresses, so the strkey enrichers in lens actually fire and
// the failure messages under test are the ones users will see.
const (
	testSource = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	testDest   = "GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ"
	testIssuer = "GA2JRQOF6EA3HQWDCEDBPPMLYPJCFLDDGYZLEQGMS5SOBQIB3BAFHVAW"
)

func mustAccountID(t *testing.T, addr string) xdr.AccountId {
	t.Helper()
	var aid xdr.AccountId
	if err := aid.SetAddress(addr); err != nil {
		t.Fatalf("parsing address %s: %v", addr, err)
	}
	return aid
}

func mustMuxed(t *testing.T, addr string) xdr.MuxedAccount {
	t.Helper()
	aid := mustAccountID(t, addr)
	return aid.ToMuxedAccount()
}

// paymentOp builds a native payment of the given stroop amount.
func paymentOp(t *testing.T, amount int64) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypePayment,
		PaymentOp: &xdr.PaymentOp{
			Destination: mustMuxed(t, testDest),
			Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			Amount:      xdr.Int64(amount),
		},
	}}
}

// creditPaymentOp builds a payment of a credit asset, for tests that need a
// non-native asset in the diff.
func creditPaymentOp(t *testing.T, code string, amount int64) xdr.Operation {
	t.Helper()
	var asset xdr.Asset
	if err := asset.SetCredit(code, mustAccountID(t, testIssuer)); err != nil {
		t.Fatalf("building asset %s: %v", code, err)
	}
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypePayment,
		PaymentOp: &xdr.PaymentOp{
			Destination: mustMuxed(t, testDest),
			Asset:       asset,
			Amount:      xdr.Int64(amount),
		},
	}}
}

// envelopeValue builds a minimal valid v1 envelope around the given
// operations.
func envelopeValue(t *testing.T, ops ...xdr.Operation) xdr.TransactionEnvelope {
	t.Helper()
	return envelopeValueSeq(t, 12345, ops...)
}

func envelopeValueSeq(t *testing.T, seq int64, ops ...xdr.Operation) xdr.TransactionEnvelope {
	t.Helper()
	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: mustMuxed(t, testSource),
				Fee:           xdr.Uint32(100 * len(ops)),
				SeqNum:        xdr.SequenceNumber(seq),
				Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations:    ops,
			},
		},
	}
}

// buildEnvelope returns a base64 envelope wrapping the given operations.
func buildEnvelope(t *testing.T, ops ...xdr.Operation) string {
	t.Helper()
	return mustMarshal(t, envelopeValue(t, ops...))
}

// buildEnvelopeSeq is buildEnvelope with an explicit sequence number, for
// tests exercising the ignore and normalize options.
func buildEnvelopeSeq(t *testing.T, seq int64, ops ...xdr.Operation) string {
	t.Helper()
	return mustMarshal(t, envelopeValueSeq(t, seq, ops...))
}

// buildResult returns a base64 TransactionResult with the given code and
// per-operation results.
func buildResult(t *testing.T, code xdr.TransactionResultCode, ops ...xdr.OperationResult) string {
	t.Helper()

	res := xdr.TransactionResult{
		FeeCharged: 100,
		Result:     xdr.TransactionResultResult{Code: code},
	}
	if code == xdr.TransactionResultCodeTxFailed || code == xdr.TransactionResultCodeTxSuccess {
		results := ops
		res.Result.Results = &results
	}
	return mustMarshal(t, res)
}

// paymentResult builds one operation result for a payment.
func paymentResult(code xdr.PaymentResultCode) xdr.OperationResult {
	return xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type:          xdr.OperationTypePayment,
			PaymentResult: &xdr.PaymentResult{Code: code},
		},
	}
}

// contractEvent builds a contract event with a symbol topic and an i128-free
// u32 payload, which is enough to exercise matching without noise.
func contractEvent(t *testing.T, topic string, data uint32) xdr.ContractEvent {
	t.Helper()

	sym := xdr.ScSymbol(topic)
	val := xdr.Uint32(data)
	var contractID xdr.ContractId
	copy(contractID[:], []byte("focus-test-contract-id-32bytes!!"))

	return xdr.ContractEvent{
		Type:       xdr.ContractEventTypeContract,
		ContractId: &contractID,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: []xdr.ScVal{{Type: xdr.ScValTypeScvSymbol, Sym: &sym}},
				Data:   xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &val},
			},
		},
	}
}

// buildSorobanMeta wraps events in a V3 transaction meta with a u32 return
// value of 7.
func buildSorobanMeta(t *testing.T, events ...xdr.ContractEvent) string {
	t.Helper()

	ret := xdr.Uint32(7)
	return mustMarshal(t, xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				Events:      events,
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &ret},
			},
		},
	})
}

// accountEntryValue builds a ledger entry holding an account with the given
// balance, thresholds and signers.
func accountEntryValue(t *testing.T, addr string, balance int64, th xdr.Thresholds, signers ...xdr.Signer) xdr.LedgerEntry {
	t.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeAccount,
			Account: &xdr.AccountEntry{
				AccountId:  mustAccountID(t, addr),
				Balance:    xdr.Int64(balance),
				Thresholds: th,
				Signers:    signers,
			},
		},
	}
}

// trustLineAsset builds a credit trustline asset with the test issuer.
func trustLineAsset(t *testing.T, code string) xdr.TrustLineAsset {
	t.Helper()
	var asset xdr.Asset
	if err := asset.SetCredit(code, mustAccountID(t, testIssuer)); err != nil {
		t.Fatalf("building asset %s: %v", code, err)
	}
	return asset.ToTrustLineAsset()
}

// trustlineEntryValue builds a ledger entry holding a trustline.
func trustlineEntryValue(t *testing.T, addr, code string, balance, limit int64) xdr.LedgerEntry {
	t.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeTrustline,
			TrustLine: &xdr.TrustLineEntry{
				AccountId: mustAccountID(t, addr),
				Asset:     trustLineAsset(t, code),
				Balance:   xdr.Int64(balance),
				Limit:     xdr.Int64(limit),
			},
		},
	}
}

func ledgerKeyOf(t *testing.T, entry xdr.LedgerEntry) xdr.LedgerKey {
	t.Helper()
	key, err := entry.LedgerKey()
	if err != nil {
		t.Fatalf("deriving a ledger key: %v", err)
	}
	return key
}

// signerValue builds one account signer.
func signerValue(t *testing.T, addr string, weight uint32) xdr.Signer {
	t.Helper()
	return xdr.Signer{Key: xdr.MustSigner(addr), Weight: xdr.Uint32(weight)}
}

func stateChange(entry xdr.LedgerEntry) xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryState, State: &entry}
}

func createdChange(entry xdr.LedgerEntry) xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated, Created: &entry}
}

func updatedChange(entry xdr.LedgerEntry) xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated, Updated: &entry}
}

func restoredChange(entry xdr.LedgerEntry) xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryRestored, Restored: &entry}
}

func removedChange(key xdr.LedgerKey) xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved, Removed: &key}
}

// metaV2 wraps per-operation changes in a version 2 transaction meta.
func metaV2(t *testing.T, ops ...[]xdr.LedgerEntryChange) string {
	t.Helper()
	return metaV2WithTx(t, nil, nil, ops...)
}

// metaV2WithTx wraps transaction-level and per-operation changes in a version
// 2 transaction meta, the shape a fee and a sequence bump arrive in.
func metaV2WithTx(t *testing.T, before, after []xdr.LedgerEntryChange, ops ...[]xdr.LedgerEntryChange) string {
	t.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(t, xdr.TransactionMeta{
		V: 2,
		V2: &xdr.TransactionMetaV2{
			TxChangesBefore: before,
			Operations:      operations,
			TxChangesAfter:  after,
		},
	})
}

// metaV0 wraps per-operation changes in a version 0 transaction meta.
func metaV0(t *testing.T, ops ...[]xdr.LedgerEntryChange) string {
	t.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(t, xdr.TransactionMeta{V: 0, Operations: &operations})
}

// metaV1 wraps transaction-level and per-operation changes in a version 1
// transaction meta.
func metaV1(t *testing.T, txChanges []xdr.LedgerEntryChange, ops ...[]xdr.LedgerEntryChange) string {
	t.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(t, xdr.TransactionMeta{
		V:  1,
		V1: &xdr.TransactionMetaV1{TxChanges: txChanges, Operations: operations},
	})
}

// precondTime builds a time-only precondition.
func precondTime(tb *xdr.TimeBounds) xdr.Preconditions {
	return xdr.Preconditions{Type: xdr.PreconditionTypePrecondTime, TimeBounds: tb}
}

// precondV2 builds a V2 precondition from the optional bounds, which is where
// ledger bounds and the minimum sequence number live.
func precondV2(tb *xdr.TimeBounds, lb *xdr.LedgerBounds) xdr.Preconditions {
	return xdr.Preconditions{
		Type: xdr.PreconditionTypePrecondV2,
		V2:   &xdr.PreconditionsV2{TimeBounds: tb, LedgerBounds: lb},
	}
}

// v0EnvelopeValue builds a preconditions-era envelope carrying time bounds in
// the older TransactionV0 field.
func v0EnvelopeValue(t *testing.T, tb *xdr.TimeBounds, ops ...xdr.Operation) xdr.TransactionEnvelope {
	t.Helper()

	aid := mustAccountID(t, testSource)
	var key xdr.Uint256
	if aid.Ed25519 == nil {
		t.Fatal("test source account has no ed25519 key")
	}
	key = *aid.Ed25519

	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTxV0,
		V0: &xdr.TransactionV0Envelope{
			Tx: xdr.TransactionV0{
				SourceAccountEd25519: key,
				Fee:                  xdr.Uint32(100 * len(ops)),
				SeqNum:               12345,
				TimeBounds:           tb,
				Memo:                 xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations:           ops,
			},
		},
	}
}

// feeBumpEnvelopeValue wraps an envelope's transaction in a fee bump with the
// given outer and inner signatures.
func feeBumpEnvelopeValue(
	t *testing.T, inner xdr.TransactionEnvelope,
	outerSigs, innerSigs []xdr.DecoratedSignature,
) xdr.TransactionEnvelope {
	t.Helper()

	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTxFeeBump,
		FeeBump: &xdr.FeeBumpTransactionEnvelope{
			Tx: xdr.FeeBumpTransaction{
				FeeSource: mustMuxed(t, testDest),
				Fee:       200,
				InnerTx: xdr.FeeBumpTransactionInnerTx{
					Type: xdr.EnvelopeTypeEnvelopeTypeTx,
					V1: &xdr.TransactionV1Envelope{
						Tx:         inner.V1.Tx,
						Signatures: innerSigs,
					},
				},
			},
			Signatures: outerSigs,
		},
	}
}

// metaV3WithTx wraps transaction-level and per-operation changes in a version
// 3 (Soroban) transaction meta.
func metaV3WithTx(t *testing.T, before, after []xdr.LedgerEntryChange, ops ...[]xdr.LedgerEntryChange) string {
	t.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(t, xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			TxChangesBefore: before,
			Operations:      operations,
			TxChangesAfter:  after,
		},
	})
}

// claimableBalanceIDValue builds a deterministic claimable balance id, so a
// test can name the balance it expects without a real transaction hash.
func claimableBalanceIDValue(seed byte) xdr.ClaimableBalanceId {
	var hash xdr.Hash
	for i := range hash {
		hash[i] = seed
	}
	return xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &hash,
	}
}

// unconditionalClaimant builds a claimant that can claim at any time.
func unconditionalClaimant(t *testing.T, addr string) xdr.Claimant {
	t.Helper()
	return xdr.Claimant{
		Type: xdr.ClaimantTypeClaimantTypeV0,
		V0: &xdr.ClaimantV0{
			Destination: mustAccountID(t, addr),
			Predicate: xdr.ClaimPredicate{
				Type: xdr.ClaimPredicateTypeClaimPredicateUnconditional,
			},
		},
	}
}

// beforeTimeClaimant builds a claimant whose claim expires at an absolute
// time, the predicate a timed escrow uses.
func beforeTimeClaimant(t *testing.T, addr string, absBefore int64) xdr.Claimant {
	t.Helper()
	before := xdr.Int64(absBefore)
	return xdr.Claimant{
		Type: xdr.ClaimantTypeClaimantTypeV0,
		V0: &xdr.ClaimantV0{
			Destination: mustAccountID(t, addr),
			Predicate: xdr.ClaimPredicate{
				Type:      xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime,
				AbsBefore: &before,
			},
		},
	}
}

// claimableBalanceEntryValue builds a ledger entry holding a claimable
// balance.
func claimableBalanceEntryValue(
	id xdr.ClaimableBalanceId, asset xdr.Asset, amount int64, claimants ...xdr.Claimant,
) xdr.LedgerEntry {
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeClaimableBalance,
			ClaimableBalance: &xdr.ClaimableBalanceEntry{
				BalanceId: id,
				Claimants: claimants,
				Asset:     asset,
				Amount:    xdr.Int64(amount),
			},
		},
	}
}

// dataLedgerEntryValue builds a ledger entry holding an account data entry.
func dataLedgerEntryValue(t *testing.T, addr, name string, value []byte) xdr.LedgerEntry {
	t.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeData,
			Data: &xdr.DataEntry{
				AccountId: mustAccountID(t, addr),
				DataName:  xdr.String64(name),
				DataValue: xdr.DataValue(value),
			},
		},
	}
}

// poolIDValue builds a deterministic liquidity pool id.
func poolIDValue(seed byte) xdr.PoolId {
	var id xdr.PoolId
	for i := range id {
		id[i] = seed
	}
	return id
}

// poolEntryValue builds a ledger entry holding a constant-product liquidity
// pool with the given reserves and shares.
func poolEntryValue(
	id xdr.PoolId, assetA, assetB xdr.Asset, reserveA, reserveB, shares int64,
) xdr.LedgerEntry {
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeLiquidityPool,
			LiquidityPool: &xdr.LiquidityPoolEntry{
				LiquidityPoolId: id,
				Body: xdr.LiquidityPoolEntryBody{
					Type: xdr.LiquidityPoolTypeLiquidityPoolConstantProduct,
					ConstantProduct: &xdr.LiquidityPoolEntryConstantProduct{
						Params: xdr.LiquidityPoolConstantProductParameters{
							AssetA: assetA,
							AssetB: assetB,
							Fee:    30,
						},
						ReserveA:                 xdr.Int64(reserveA),
						ReserveB:                 xdr.Int64(reserveB),
						TotalPoolShares:          xdr.Int64(shares),
						PoolSharesTrustLineCount: 1,
					},
				},
			},
		},
	}
}

// offerEntryValue builds a ledger entry holding an order book offer.
func offerEntryValue(
	t *testing.T, addr string, offerID int64, selling, buying xdr.Asset, amount int64, price xdr.Price,
) xdr.LedgerEntry {
	t.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeOffer,
			Offer: &xdr.OfferEntry{
				SellerId: mustAccountID(t, addr),
				OfferId:  xdr.Int64(offerID),
				Selling:  selling,
				Buying:   buying,
				Amount:   xdr.Int64(amount),
				Price:    price,
			},
		},
	}
}

// nativeAsset is the native asset, which several of the new assertions need
// as an xdr.Asset rather than as a label.
func nativeAsset() xdr.Asset {
	return xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
}

// orderBookClaim builds a claim atom for a matched order book offer.
func orderBookClaim(
	t *testing.T, seller string, offerID int64,
	sold xdr.Asset, amountSold int64, bought xdr.Asset, amountBought int64,
) xdr.ClaimAtom {
	t.Helper()
	return xdr.ClaimAtom{
		Type: xdr.ClaimAtomTypeClaimAtomTypeOrderBook,
		OrderBook: &xdr.ClaimOfferAtom{
			SellerId:     mustAccountID(t, seller),
			OfferId:      xdr.Int64(offerID),
			AssetSold:    sold,
			AmountSold:   xdr.Int64(amountSold),
			AssetBought:  bought,
			AmountBought: xdr.Int64(amountBought),
		},
	}
}

// poolClaim builds a claim atom for a trade against a liquidity pool.
func poolClaim(
	id xdr.PoolId, sold xdr.Asset, amountSold int64, bought xdr.Asset, amountBought int64,
) xdr.ClaimAtom {
	return xdr.ClaimAtom{
		Type: xdr.ClaimAtomTypeClaimAtomTypeLiquidityPool,
		LiquidityPool: &xdr.ClaimLiquidityAtom{
			LiquidityPoolId: id,
			AssetSold:       sold,
			AmountSold:      xdr.Int64(amountSold),
			AssetBought:     bought,
			AmountBought:    xdr.Int64(amountBought),
		},
	}
}

// manageSellOfferResult builds one operation result for a manage-sell-offer
// that claimed the given offers.
func manageSellOfferResult(atoms ...xdr.ClaimAtom) xdr.OperationResult {
	return xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypeManageSellOffer,
			ManageSellOfferResult: &xdr.ManageSellOfferResult{
				Code: xdr.ManageSellOfferResultCodeManageSellOfferSuccess,
				Success: &xdr.ManageOfferSuccessResult{
					OffersClaimed: atoms,
					Offer: xdr.ManageOfferSuccessResultOffer{
						Effect: xdr.ManageOfferEffectManageOfferDeleted,
					},
				},
			},
		},
	}
}

// priceValue builds an offer price as the ratio the protocol stores.
func priceValue(n, d int32) xdr.Price {
	return xdr.Price{N: xdr.Int32(n), D: xdr.Int32(d)}
}

// decodedMeta wraps a meta as an already-decoded lens value.
//
// This is the one accepted input form that does not go through the XDR
// encoder, so it is how a test can hand an assertion a union arm the encoder
// would reject — which is exactly what the guards against malformed values
// are there for.
func decodedMeta(ops ...[]xdr.LedgerEntryChange) *lens.Value {
	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return &lens.Value{
		Type: "TransactionMeta",
		Raw: &xdr.TransactionMeta{
			V:  2,
			V2: &xdr.TransactionMetaV2{Operations: operations},
		},
	}
}

// decodedResult wraps a transaction result as an already-decoded lens value,
// for the same reason as decodedMeta.
func decodedResult(ops ...xdr.OperationResult) *lens.Value {
	results := ops
	return &lens.Value{
		Type: "TransactionResult",
		Raw: &xdr.TransactionResult{
			FeeCharged: 100,
			Result: xdr.TransactionResultResult{
				Code:    xdr.TransactionResultCodeTxSuccess,
				Results: &results,
			},
		},
	}
}
