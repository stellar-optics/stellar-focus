package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func claimableBalanceSuccessResult(hash xdr.Hash) xdr.OperationResult {
	id := xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &hash,
	}
	return xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypeCreateClaimableBalance,
			CreateClaimableBalanceResult: &xdr.CreateClaimableBalanceResult{
				Code:      xdr.CreateClaimableBalanceResultCodeCreateClaimableBalanceSuccess,
				BalanceId: &id,
			},
		},
	}
}

func TestAssertClaimableBalanceID(t *testing.T) {
	t.Parallel()

	var hash xdr.Hash
	for i := range hash {
		hash[i] = byte(i)
	}
	result := claimableBalanceSuccessResult(hash)
	hexID := hash.HexString()

	tests := []struct {
		name   string
		wantID focus.Value
	}{
		{"xdr.ClaimableBalanceId", xdr.ClaimableBalanceId{
			Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
			V0:   &hash,
		}},
		{"raw 32-byte hash", [32]byte(hash)},
		{"raw byte slice", hash[:]},
		{"hex string", hexID},
		{"hex string with 0x prefix", "0x" + hexID},
		{"hex string in upper case", strings.ToUpper(hexID)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertClaimableBalanceID(tb, result, tc.wantID) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("require mirror stops the test on failure", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		require.ClaimableBalanceID(tb, result, strings.Repeat("00", 32))
		if !tb.FatalfCalled() {
			t.Errorf("require.ClaimableBalanceID did not stop the test on a mismatch:\n%s", tb.Output())
		}
	})
}

func TestAssertClaimableBalanceIDMismatch(t *testing.T) {
	t.Parallel()

	var want, got xdr.Hash
	for i := range want {
		want[i] = 0xaa
		got[i] = 0xaa
	}
	got[3] = 0xff

	tb := faketb.New()
	if focus.AssertClaimableBalanceID(tb, claimableBalanceSuccessResult(got), want) {
		t.Fatal("assertion unexpectedly passed")
	}
	msg := tb.Output()
	if !strings.Contains(msg, strings.ToLower(want.HexString())) {
		t.Errorf("failure message does not show the wanted hex id:\n%s", msg)
	}
	if !strings.Contains(msg, strings.ToLower(got.HexString())) {
		t.Errorf("failure message does not show the actual hex id:\n%s", msg)
	}
	if !strings.Contains(msg, "^") {
		t.Errorf("failure message does not highlight the differing character:\n%s", msg)
	}
}

func TestAssertClaimableBalanceIDWrongOperation(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type:          xdr.OperationTypePayment,
			PaymentResult: &xdr.PaymentResult{Code: xdr.PaymentResultCodePaymentSuccess},
		},
	}
	if focus.AssertClaimableBalanceID(tb, result, strings.Repeat("ab", 32)) {
		t.Fatal("assertion unexpectedly passed for a non-CreateClaimableBalance result")
	}
}
