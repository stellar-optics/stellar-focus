package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func TestAssertLiquidityPoolWithdraw(t *testing.T) {
	var poolID xdr.PoolId
	poolID[0] = 1
	body, err := xdr.NewOperationBody(xdr.OperationTypeLiquidityPoolWithdraw, xdr.LiquidityPoolWithdrawOp{
		LiquidityPoolId: poolID, Amount: 25_000_000, MinAmountA: 3_000_000, MinAmountB: 4_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	op := xdr.Operation{Body: body}
	tb := faketb.New()
	if !focus.AssertLiquidityPoolWithdraw(tb, op, poolID, 25_000_000, 3_000_000, 4_000_000) {
		t.Fatalf("matching operation failed: %s", tb.Output())
	}
	tb = faketb.New()
	if focus.AssertLiquidityPoolWithdraw(tb, op, poolID, 20_000_000, 5_000_000, 4_000_000) {
		t.Fatal("mismatched operation passed")
	}
	for _, want := range []string{"shares", "minimum asset A", "stroops", "2.0000000"} {
		if !strings.Contains(tb.Output(), want) {
			t.Errorf("failure missing %q: %s", want, tb.Output())
		}
	}
}

func TestAssertLiquidityPoolWithdrawRejectsOtherOperation(t *testing.T) {
	var poolID xdr.PoolId
	body, err := xdr.NewOperationBody(xdr.OperationTypePayment, xdr.PaymentOp{})
	if err != nil {
		t.Fatal(err)
	}
	tb := faketb.New()
	if focus.AssertLiquidityPoolWithdraw(tb, xdr.Operation{Body: body}, poolID, 0, 0, 0) || !strings.Contains(tb.Output(), "want LiquidityPoolWithdraw") {
		t.Fatalf("wrong operation was not reported: %s", tb.Output())
	}
}
