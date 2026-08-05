package focus_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
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
