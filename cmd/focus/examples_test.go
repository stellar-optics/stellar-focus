package main_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// TestAssertEnvelopeEqualExampleCases checks the behavior documented by the
// package example with an in-memory TB; the assertion never needs an RPC call.
func TestAssertEnvelopeEqualExampleCases(t *testing.T) {
	t.Parallel()
	const source = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	account := xdr.MustAddress(source)
	base := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: account.ToMuxedAccount(),
			SeqNum:        123,
			Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
			Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
		}},
	}

	identical := faketb.New()
	if !focus.AssertEnvelopeEqual(identical, base, base) || identical.Failed() {
		t.Errorf("identical envelopes failed: %s", identical.Output())
	}

	changed := base
	changedV1 := *base.V1
	changed.V1 = &changedV1
	changed.V1.Tx.SeqNum++
	different := faketb.New()
	if focus.AssertEnvelopeEqual(different, base, changed) {
		t.Fatal("different envelopes compared equal")
	}
	message, err := different.Message()
	if err != nil {
		t.Fatalf("reading failure message: %v", err)
	}
	for _, part := range []string{"AssertEnvelopeEqual", "SeqNum", "want", "got", "123", "124"} {
		if !strings.Contains(message, part) {
			t.Errorf("failure does not contain %q:\n%s", part, message)
		}
	}

	invalid := faketb.New()
	if focus.AssertEnvelopeEqual(invalid, "not base64 XDR", base) {
		t.Fatal("invalid XDR compared equal")
	}
	message, err = invalid.Message()
	if err != nil {
		t.Fatalf("reading invalid-input failure: %v", err)
	}
	if !strings.Contains(message, "want") {
		t.Errorf("invalid-input failure does not identify the input: %s", message)
	}
}
