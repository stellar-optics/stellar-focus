package golden_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	testSource = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	testDest   = "GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ"
)

// buildEnvelope returns a base64 envelope carrying one payment of the given
// stroop amount.
func buildEnvelope(t *testing.T, amount int64) string {
	t.Helper()
	return buildEnvelopeSeq(t, 12345, amount)
}

// buildEnvelopeSeq is buildEnvelope with an explicit sequence number.
func buildEnvelopeSeq(t *testing.T, seq, amount int64) string {
	t.Helper()

	var src, dst xdr.AccountId
	if err := src.SetAddress(testSource); err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	if err := dst.SetAddress(testDest); err != nil {
		t.Fatalf("parsing destination: %v", err)
	}

	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: src.ToMuxedAccount(),
				Fee:           100,
				SeqNum:        xdr.SequenceNumber(seq),
				Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations: []xdr.Operation{{Body: xdr.OperationBody{
					Type: xdr.OperationTypePayment,
					PaymentOp: &xdr.PaymentOp{
						Destination: dst.ToMuxedAccount(),
						Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
						Amount:      xdr.Int64(amount),
					},
				}}},
			},
		},
	}

	s, err := xdr.MarshalBase64(env)
	if err != nil {
		t.Fatalf("marshalling envelope: %v", err)
	}
	return s
}
