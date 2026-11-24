package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func TestAssertAccountFlags(t *testing.T) {
	t.Parallel()

	setFlags := xdr.Uint32(xdr.AccountFlagsAuthRequiredFlag)
	clearFlags := xdr.Uint32(xdr.AccountFlagsAuthClawbackEnabledFlag)
	op := xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeSetOptions,
		SetOptionsOp: &xdr.SetOptionsOp{
			SetFlags:   &setFlags,
			ClearFlags: &clearFlags,
		},
	}}

	t.Run("exact match", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if !focus.AssertAccountFlags(tb, op,
			xdr.AccountFlagsAuthRequiredFlag, xdr.AccountFlagsAuthClawbackEnabledFlag) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.AccountFlags(t, op,
			xdr.AccountFlagsAuthRequiredFlag, xdr.AccountFlagsAuthClawbackEnabledFlag)
	})

	t.Run("partial mismatch names flags", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		wantSet := xdr.AccountFlagsAuthRevocableFlag | xdr.AccountFlagsAuthImmutableFlag
		if focus.AssertAccountFlags(tb, op, wantSet, xdr.AccountFlagsAuthClawbackEnabledFlag) {
			t.Fatal("assertion passed with different flags")
		}
		for _, want := range []string{
			"want set: [AUTH_REVOCABLE, AUTH_IMMUTABLE]",
			"got set: [AUTH_REQUIRED]",
		} {
			if !strings.Contains(tb.Output(), want) {
				t.Errorf("message does not contain %q:\n%s", want, tb.Output())
			}
		}
	})

	t.Run("rejects a non-SetOptions operation", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if focus.AssertAccountFlags(tb, xdr.Operation{
			Body: xdr.OperationBody{Type: xdr.OperationTypePayment},
		}, 0, 0) {
			t.Fatal("assertion passed for a payment operation")
		}
		if !strings.Contains(tb.Output(), "want SetOptions") {
			t.Errorf("message does not identify the expected operation:\n%s", tb.Output())
		}
	})
}
