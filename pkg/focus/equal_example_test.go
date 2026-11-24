package focus_test

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// ExampleAssertEnvelopeEqual shows an offline comparison and how to inspect
// the path reported when two envelopes differ.
func ExampleAssertEnvelopeEqual() {
	const source = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	account := xdr.MustAddress(source)
	want := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: account.ToMuxedAccount(),
			SeqNum:        123,
			Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
			Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
		}},
	}
	got := want
	gotV1 := *want.V1
	got.V1 = &gotV1
	got.V1.Tx.SeqNum = 124

	check := func(actual xdr.TransactionEnvelope) error {
		return focus.Check(func(tb focus.TB) bool {
			return focus.AssertEnvelopeEqual(tb, want, actual)
		})
	}
	fmt.Println(check(want) == nil)
	err := check(got)
	fmt.Println(err != nil && strings.Contains(err.Error(), "SeqNum"))
	// Output:
	// true
	// true
}
