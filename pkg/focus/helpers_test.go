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

func mustAccountID(tb testing.TB, addr string) xdr.AccountId {
	tb.Helper()
	var aid xdr.AccountId
	if err := aid.SetAddress(addr); err != nil {
		tb.Fatalf("parsing address %s: %v", addr, err)
	}
	return aid
}

func mustMuxed(tb testing.TB, addr string) xdr.MuxedAccount {
	tb.Helper()
	aid := mustAccountID(tb, addr)
	return aid.ToMuxedAccount()
}

// paymentOp builds a native payment of the given stroop amount.
func paymentOp(tb testing.TB, amount int64) xdr.Operation {
	tb.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypePayment,
		PaymentOp: &xdr.PaymentOp{
			Destination: mustMuxed(tb, testDest),
			Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			Amount:      xdr.Int64(amount),
		},
	}}
}

// creditPaymentOp builds a payment of a credit asset, for tests that need a
// non-native asset in the diff.
func creditPaymentOp(tb testing.TB, amount int64) xdr.Operation {
	tb.Helper()
	var asset xdr.Asset
	if err := asset.SetCredit("USDC", mustAccountID(tb, testIssuer)); err != nil {
		tb.Fatalf("building asset: %v", err)
	}
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypePayment,
		PaymentOp: &xdr.PaymentOp{
			Destination: mustMuxed(tb, testDest),
			Asset:       asset,
			Amount:      xdr.Int64(amount),
		},
	}}
}

func endSponsoringFutureReservesOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeEndSponsoringFutureReserves,
	}}
}

func revokeSponsorshipOp(t *testing.T) xdr.Operation {
	t.Helper()
	key := ledgerKeyOf(t, accountEntryValue(t, testSource, 1000, xdr.Thresholds{}))
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeRevokeSponsorship,
		RevokeSponsorshipOp: &xdr.RevokeSponsorshipOp{
			Type:      xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry,
			LedgerKey: &key,
		},
	}}
}

func clawbackOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeClawback,
		ClawbackOp: &xdr.ClawbackOp{
			Asset:  creditAsset(t, "USDC"),
			From:   mustMuxed(t, testDest),
			Amount: xdr.Int64(50_0000000),
		},
	}}
}

func clawbackClaimableBalanceOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeClawbackClaimableBalance,
		ClawbackClaimableBalanceOp: &xdr.ClawbackClaimableBalanceOp{
			BalanceId: claimableBalanceIDValue(1),
		},
	}}
}

func setTrustLineFlagsOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeSetTrustLineFlags,
		SetTrustLineFlagsOp: &xdr.SetTrustLineFlagsOp{
			Trustor:    mustAccountID(t, testSource),
			Asset:      creditAsset(t, "USDC"),
			SetFlags:   xdr.Uint32(xdr.TrustLineFlagsAuthorizedFlag),
			ClearFlags: 0,
		},
	}}
}

func liquidityPoolDepositOp(t *testing.T) xdr.Operation {
	t.Helper()
	id := poolIDValue(1)
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeLiquidityPoolDeposit,
		LiquidityPoolDepositOp: &xdr.LiquidityPoolDepositOp{
			LiquidityPoolId: id,
			MaxAmountA:      xdr.Int64(100_0000000),
			MaxAmountB:      xdr.Int64(50_0000000),
			MinPrice:        xdr.Price{N: xdr.Int32(1), D: xdr.Int32(1)},
			MaxPrice:        xdr.Price{N: xdr.Int32(2), D: xdr.Int32(1)},
		},
	}}
}

func liquidityPoolWithdrawOp(t *testing.T) xdr.Operation {
	t.Helper()
	id := poolIDValue(1)
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeLiquidityPoolWithdraw,
		LiquidityPoolWithdrawOp: &xdr.LiquidityPoolWithdrawOp{
			LiquidityPoolId: id,
			Amount:          xdr.Int64(1000),
			MinAmountA:      xdr.Int64(100_000000),
			MinAmountB:      xdr.Int64(50_000000),
		},
	}}
}

func invokeHostFunctionOp(t *testing.T) xdr.Operation {
	t.Helper()
	var cid xdr.ContractId
	copy(cid[:], []byte("focus-test-contract-id-32bytes!!"))
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeInvokeHostFunction,
		InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
			HostFunction: xdr.HostFunction{
				Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
				InvokeContract: &xdr.InvokeContractArgs{
					ContractAddress: xdr.ScAddress{
						Type:       xdr.ScAddressTypeScAddressTypeContract,
						ContractId: &cid,
					},
					FunctionName: xdr.ScSymbol("test"),
					Args:         []xdr.ScVal{},
				},
			},
			Auth: []xdr.SorobanAuthorizationEntry{},
		},
	}}
}

func extendFootprintTtlOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeExtendFootprintTtl,
		ExtendFootprintTtlOp: &xdr.ExtendFootprintTtlOp{
			ExtendTo: xdr.Uint32(1000),
		},
	}}
}

// restoreFootprintOp builds a restoreFootprint operation.
func restoreFootprintOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type:               xdr.OperationTypeRestoreFootprint,
		RestoreFootprintOp: &xdr.RestoreFootprintOp{},
	}}
}

func createAccountOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeCreateAccount,
		CreateAccountOp: &xdr.CreateAccountOp{
			Destination:     mustAccountID(t, testDest),
			StartingBalance: xdr.Int64(100_0000000),
		},
	}}
}

func pathPaymentStrictReceiveOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypePathPaymentStrictReceive,
		PathPaymentStrictReceiveOp: &xdr.PathPaymentStrictReceiveOp{
			SendAsset:   xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			SendMax:     xdr.Int64(100_0000000),
			Destination: mustMuxed(t, testDest),
			DestAsset:   creditAsset(t, "USDC"),
			DestAmount:  xdr.Int64(50_0000000),
			Path:        []xdr.Asset{},
		},
	}}
}

func manageSellOfferOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeManageSellOffer,
		ManageSellOfferOp: &xdr.ManageSellOfferOp{
			Selling: creditAsset(t, "EURC"),
			Buying:  xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			Amount:  xdr.Int64(100_0000000),
			Price:   xdr.Price{N: xdr.Int32(1), D: xdr.Int32(1)},
			OfferId: 0,
		},
	}}
}

func changeTrustOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeChangeTrust,
		ChangeTrustOp: &xdr.ChangeTrustOp{
			Line:  creditAsset(t, "USDC").ToChangeTrustAsset(),
			Limit: xdr.Int64(100_0000000),
		},
	}}
}

func allowTrustOp(t *testing.T) xdr.Operation {
	t.Helper()
	asset := creditAsset(t, "USDC")
	var assetCode xdr.AssetCode
	assetCode.Type = asset.Type
	switch asset.Type {
	case xdr.AssetTypeAssetTypeCreditAlphanum4:
		assetCode.AssetCode4 = &asset.AlphaNum4.AssetCode
	case xdr.AssetTypeAssetTypeCreditAlphanum12:
		assetCode.AssetCode12 = &asset.AlphaNum12.AssetCode
	}
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeAllowTrust,
		AllowTrustOp: &xdr.AllowTrustOp{
			Trustor:   mustAccountID(t, testDest),
			Asset:     assetCode,
			Authorize: xdr.Uint32(1),
		},
	}}
}

func accountMergeOp(t *testing.T) xdr.Operation {
	t.Helper()
	dest := mustMuxed(t, testDest)
	return xdr.Operation{Body: xdr.OperationBody{
		Type:        xdr.OperationTypeAccountMerge,
		Destination: &dest,
	}}
}

func inflationOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeInflation,
	}}
}

func manageDataOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeManageData,
		ManageDataOp: &xdr.ManageDataOp{
			DataName:  xdr.String64("test"),
			DataValue: ptr(xdr.DataValue([]byte("value"))),
		},
	}}
}

func bumpSequenceOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeBumpSequence,
		BumpSequenceOp: &xdr.BumpSequenceOp{
			BumpTo: xdr.SequenceNumber(20000),
		},
	}}
}

func manageBuyOfferOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeManageBuyOffer,
		ManageBuyOfferOp: &xdr.ManageBuyOfferOp{
			Selling:   xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			Buying:    creditAsset(t, "USDC"),
			BuyAmount: xdr.Int64(100_0000000),
			Price:     xdr.Price{N: xdr.Int32(1), D: xdr.Int32(1)},
			OfferId:   0,
		},
	}}
}

func createPassiveSellOfferOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeCreatePassiveSellOffer,
		CreatePassiveSellOfferOp: &xdr.CreatePassiveSellOfferOp{
			Selling: creditAsset(t, "USDC"),
			Buying:  xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			Amount:  xdr.Int64(100_0000000),
			Price:   xdr.Price{N: xdr.Int32(1), D: xdr.Int32(1)},
		},
	}}
}

func pathPaymentStrictSendOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypePathPaymentStrictSend,
		PathPaymentStrictSendOp: &xdr.PathPaymentStrictSendOp{
			SendAsset:   creditAsset(t, "USDC"),
			SendAmount:  xdr.Int64(100_0000000),
			DestMin:     xdr.Int64(50_0000000),
			Destination: mustMuxed(t, testDest),
			DestAsset:   xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			Path:        []xdr.Asset{},
		},
	}}
}

func createClaimableBalanceOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeCreateClaimableBalance,
		CreateClaimableBalanceOp: &xdr.CreateClaimableBalanceOp{
			Asset:     creditAsset(t, "USDC"),
			Amount:    xdr.Int64(100_0000000),
			Claimants: []xdr.Claimant{unconditionalClaimant(t, testDest)},
		},
	}}
}

func claimClaimableBalanceOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeClaimClaimableBalance,
		ClaimClaimableBalanceOp: &xdr.ClaimClaimableBalanceOp{
			BalanceId: claimableBalanceIDValue(1),
		},
	}}
}

func beginSponsoringFutureReservesOp(t *testing.T) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeBeginSponsoringFutureReserves,
		BeginSponsoringFutureReservesOp: &xdr.BeginSponsoringFutureReservesOp{
			SponsoredId: mustAccountID(t, testDest),
		},
	}}
}

// envelopeValue builds a minimal valid v1 envelope around the given
// operations.
func envelopeValue(tb testing.TB, ops ...xdr.Operation) xdr.TransactionEnvelope {
	tb.Helper()
	return envelopeValueSeq(tb, 12345, ops...)
}

func envelopeValueSeq(tb testing.TB, seq int64, ops ...xdr.Operation) xdr.TransactionEnvelope {
	tb.Helper()
	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: mustMuxed(tb, testSource),
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
func buildEnvelope(tb testing.TB, ops ...xdr.Operation) string {
	tb.Helper()
	return mustMarshal(tb, envelopeValue(tb, ops...))
}

// buildEnvelopeSeq is buildEnvelope with an explicit sequence number, for
// tests exercising the ignore and normalize options.
func buildEnvelopeSeq(tb testing.TB, seq int64, ops ...xdr.Operation) string {
	tb.Helper()
	return mustMarshal(tb, envelopeValueSeq(tb, seq, ops...))
}

// envelopeWithSignatures builds a v1 envelope carrying the given signatures,
// in the order they are given.
//
// The signatures stand in for real ones: what matters to a comparison is that
// they are distinct and that their order can be changed. Each signature
// carries its own hint, so reordering the signatures produces the same
// elements in a different order rather than different elements.
func envelopeWithSignatures(tb testing.TB, signatures ...[]byte) string {
	tb.Helper()

	env := envelopeValue(tb, paymentOp(tb, 100))
	env.V1.Signatures = make([]xdr.DecoratedSignature, 0, len(signatures))
	for _, sig := range signatures {
		env.V1.Signatures = append(env.V1.Signatures, xdr.DecoratedSignature{
			Hint:      xdr.SignatureHint{sig[0], sig[1], sig[2], sig[3]},
			Signature: xdr.Signature(sig),
		})
	}
	return mustMarshal(tb, env)
}

// buildResult returns a base64 TransactionResult with the given code and
// per-operation results.
func buildResult(tb testing.TB, code xdr.TransactionResultCode, ops ...xdr.OperationResult) string {
	tb.Helper()

	res := xdr.TransactionResult{
		FeeCharged: 100,
		Result:     xdr.TransactionResultResult{Code: code},
	}
	if code == xdr.TransactionResultCodeTxFailed || code == xdr.TransactionResultCodeTxSuccess {
		results := ops
		res.Result.Results = &results
	}
	return mustMarshal(tb, res)
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
func contractEvent(tb testing.TB, topic string, data uint32) xdr.ContractEvent {
	tb.Helper()

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
func buildSorobanMeta(tb testing.TB, events ...xdr.ContractEvent) string {
	tb.Helper()

	ret := xdr.Uint32(7)
	return mustMarshal(tb, xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				Events:      events,
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &ret},
			},
		},
	})
}

// transferSpec builds the specification of a contract with a transfer event
// whose payload is a struct of named fields.
//
// It is the shape the issue describes: the contract's author writes
// {from: Address, amount: u128}, and the wire carries a map keyed by those
// names.
func transferSpec() []xdr.ScSpecEntry {
	address := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}
	amount := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU128}

	return []xdr.ScSpecEntry{
		{
			Kind: xdr.ScSpecEntryKindScSpecEntryUdtStructV0,
			UdtStructV0: &xdr.ScSpecUdtStructV0{
				Name: "TransferData",
				Fields: []xdr.ScSpecUdtStructFieldV0{
					{Name: "from", Type: address},
					{Name: "amount", Type: amount},
				},
			},
		},
		{
			Kind: xdr.ScSpecEntryKindScSpecEntryEventV0,
			EventV0: &xdr.ScSpecEventV0{
				Name:       "transfer",
				DataFormat: xdr.ScSpecEventDataFormatScSpecEventDataFormatSingleValue,
				Params: []xdr.ScSpecEventParamV0{
					{
						Name:     "data",
						Type:     xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeUdt, Udt: &xdr.ScSpecTypeUdt{Name: "TransferData"}},
						Location: xdr.ScSpecEventParamLocationV0ScSpecEventParamLocationData,
					},
				},
			},
		},
	}
}

// transferEvent builds a contract event whose payload is the map the
// transfer specification describes.
func transferEvent(tb testing.TB, from string, amount uint64) xdr.ContractEvent {
	tb.Helper()

	id, err := xdr.AddressToAccountId(from)
	if err != nil {
		tb.Fatalf("parsing address %s: %v", from, err)
	}
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &id}

	sym := xdr.ScSymbol("transfer")
	fromKey := xdr.ScSymbol("from")
	amountKey := xdr.ScSymbol("amount")
	amountVal := xdr.UInt128Parts{Hi: 0, Lo: xdr.Uint64(amount)}

	entries := xdr.ScMap{
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &fromKey}, Val: xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}},
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &amountKey}, Val: xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &amountVal}},
	}
	mapPtr := &entries

	var contractID xdr.ContractId
	copy(contractID[:], []byte("focus-test-contract-id-32bytes!!"))

	return xdr.ContractEvent{
		Type:       xdr.ContractEventTypeContract,
		ContractId: &contractID,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: []xdr.ScVal{{Type: xdr.ScValTypeScvSymbol, Sym: &sym}},
				Data:   xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr},
			},
		},
	}
}

// accountEntryValue builds a ledger entry holding an account with the given
// balance, thresholds and signers.
func accountEntryValue(tb testing.TB, addr string, balance int64, th xdr.Thresholds, signers ...xdr.Signer) xdr.LedgerEntry {
	tb.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeAccount,
			Account: &xdr.AccountEntry{
				AccountId:  mustAccountID(tb, addr),
				Balance:    xdr.Int64(balance),
				Thresholds: th,
				Signers:    signers,
			},
		},
	}
}

// trustLineAsset builds a credit trustline asset with the test issuer.
func trustLineAsset(tb testing.TB) xdr.TrustLineAsset {
	tb.Helper()
	var asset xdr.Asset
	if err := asset.SetCredit("USDC", mustAccountID(tb, testIssuer)); err != nil {
		tb.Fatalf("building asset: %v", err)
	}
	return asset.ToTrustLineAsset()
}

// trustlineEntryValue builds a ledger entry holding a trustline.
func trustlineEntryValue(tb testing.TB, balance, limit int64) xdr.LedgerEntry {
	tb.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeTrustline,
			TrustLine: &xdr.TrustLineEntry{
				AccountId: mustAccountID(tb, testDest),
				Asset:     trustLineAsset(tb),
				Balance:   xdr.Int64(balance),
				Limit:     xdr.Int64(limit),
			},
		},
	}
}

func ledgerKeyOf(tb testing.TB, entry xdr.LedgerEntry) xdr.LedgerKey {
	tb.Helper()
	key, err := entry.LedgerKey()
	if err != nil {
		tb.Fatalf("deriving a ledger key: %v", err)
	}
	return key
}

// signerValue builds one account signer.
func signerValue(tb testing.TB, addr string, weight uint32) xdr.Signer {
	tb.Helper()
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
func metaV2(tb testing.TB, ops ...[]xdr.LedgerEntryChange) string {
	tb.Helper()
	return metaV2WithTx(tb, nil, nil, ops...)
}

// metaV2WithTx wraps transaction-level and per-operation changes in a version
// 2 transaction meta, the shape a fee and a sequence bump arrive in.
func metaV2WithTx(tb testing.TB, before, after []xdr.LedgerEntryChange, ops ...[]xdr.LedgerEntryChange) string {
	tb.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(tb, xdr.TransactionMeta{
		V: 2,
		V2: &xdr.TransactionMetaV2{
			TxChangesBefore: before,
			Operations:      operations,
			TxChangesAfter:  after,
		},
	})
}

// metaV0 wraps per-operation changes in a version 0 transaction meta.
func metaV0(tb testing.TB, ops ...[]xdr.LedgerEntryChange) string {
	tb.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(tb, xdr.TransactionMeta{V: 0, Operations: &operations})
}

// metaV1 wraps transaction-level and per-operation changes in a version 1
// transaction meta.
func metaV1(tb testing.TB, txChanges []xdr.LedgerEntryChange, ops ...[]xdr.LedgerEntryChange) string {
	tb.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(tb, xdr.TransactionMeta{
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
func v0EnvelopeValue(tb testing.TB, timeBounds *xdr.TimeBounds, ops ...xdr.Operation) xdr.TransactionEnvelope {
	tb.Helper()

	aid := mustAccountID(tb, testSource)
	var key xdr.Uint256
	if aid.Ed25519 == nil {
		tb.Fatal("test source account has no ed25519 key")
	}
	key = *aid.Ed25519

	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTxV0,
		V0: &xdr.TransactionV0Envelope{
			Tx: xdr.TransactionV0{
				SourceAccountEd25519: key,
				Fee:                  xdr.Uint32(100 * len(ops)),
				SeqNum:               12345,
				TimeBounds:           timeBounds,
				Memo:                 xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations:           ops,
			},
		},
	}
}

// feeBumpEnvelopeValue wraps an envelope's transaction in a fee bump with the
// given outer and inner signatures.
func feeBumpEnvelopeValue(
	tb testing.TB, inner xdr.TransactionEnvelope,
	outerSigs, innerSigs []xdr.DecoratedSignature,
) xdr.TransactionEnvelope {
	tb.Helper()

	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTxFeeBump,
		FeeBump: &xdr.FeeBumpTransactionEnvelope{
			Tx: xdr.FeeBumpTransaction{
				FeeSource: mustMuxed(tb, testDest),
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
func metaV3WithTx(tb testing.TB, before, after []xdr.LedgerEntryChange, ops ...[]xdr.LedgerEntryChange) string {
	tb.Helper()

	operations := make([]xdr.OperationMeta, 0, len(ops))
	for _, changes := range ops {
		operations = append(operations, xdr.OperationMeta{Changes: changes})
	}
	return mustMarshal(tb, xdr.TransactionMeta{
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
func unconditionalClaimant(tb testing.TB, addr string) xdr.Claimant {
	tb.Helper()
	return xdr.Claimant{
		Type: xdr.ClaimantTypeClaimantTypeV0,
		V0: &xdr.ClaimantV0{
			Destination: mustAccountID(tb, addr),
			Predicate: xdr.ClaimPredicate{
				Type: xdr.ClaimPredicateTypeClaimPredicateUnconditional,
			},
		},
	}
}

// beforeTimeClaimant builds a claimant whose claim expires at an absolute
// time, the predicate a timed escrow uses.
func beforeTimeClaimant(tb testing.TB, addr string, absBefore int64) xdr.Claimant {
	tb.Helper()
	before := xdr.Int64(absBefore)
	return xdr.Claimant{
		Type: xdr.ClaimantTypeClaimantTypeV0,
		V0: &xdr.ClaimantV0{
			Destination: mustAccountID(tb, addr),
			Predicate: xdr.ClaimPredicate{
				Type:      xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime,
				AbsBefore: &before,
			},
		},
	}
}

// claimableBalanceEntryWith builds a ledger entry holding a claimable balance
// with a given asset and claimants.
//
// clawback_test.go has a fixed-shape builder of its own for the revocation
// tests, which only need one balance of one asset; these tests vary the asset,
// the amount and the claim predicates, so they take them as arguments.
func claimableBalanceEntryWith(
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
func dataLedgerEntryValue(tb testing.TB, addr, name string, value []byte) xdr.LedgerEntry {
	tb.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeData,
			Data: &xdr.DataEntry{
				AccountId: mustAccountID(tb, addr),
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
	tb testing.TB, addr string, offerID int64, selling, buying xdr.Asset, amount int64, price xdr.Price,
) xdr.LedgerEntry {
	tb.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeOffer,
			Offer: &xdr.OfferEntry{
				SellerId: mustAccountID(tb, addr),
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
	tb testing.TB, seller string, offerID int64,
	sold xdr.Asset, amountSold int64, bought xdr.Asset, amountBought int64,
) xdr.ClaimAtom {
	tb.Helper()
	return xdr.ClaimAtom{
		Type: xdr.ClaimAtomTypeClaimAtomTypeOrderBook,
		OrderBook: &xdr.ClaimOfferAtom{
			SellerId:     mustAccountID(tb, seller),
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

// creditAsset builds a credit asset with the test issuer.
func creditAsset(t *testing.T, code string) xdr.Asset {
	t.Helper()
	var asset xdr.Asset
	if err := asset.SetCredit(code, mustAccountID(t, testIssuer)); err != nil {
		t.Fatalf("building asset %s: %v", code, err)
	}
	return asset
}
