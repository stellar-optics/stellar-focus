package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func withdrawOp(id xdr.PoolId, shares, minA, minB int64) xdr.Operation {
	return xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeLiquidityPoolWithdraw,
			LiquidityPoolWithdrawOp: &xdr.LiquidityPoolWithdrawOp{
				LiquidityPoolId: id,
				Amount:          xdr.Int64(shares),
				MinAmountA:      xdr.Int64(minA),
				MinAmountB:      xdr.Int64(minB),
			},
		},
	}
}

func TestAssertLiquidityPoolWithdraw(t *testing.T) {
	t.Parallel()

	id := poolIDValue(0x7a)
	op := withdrawOp(id, 70, 100, 200)

	tb := faketb.New()
	if !focus.AssertLiquidityPoolWithdraw(tb, op, id, 70, 100, 200) {
		t.Errorf("assertion failed against a matching operation:\n%s", tb.Output())
	}
}

func TestAssertLiquidityPoolWithdrawWrongOperationType(t *testing.T) {
	t.Parallel()

	id := poolIDValue(0x7a)
	op := xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypePayment}}

	tb := faketb.New()
	if focus.AssertLiquidityPoolWithdraw(tb, op, id, 70, 100, 200) {
		t.Fatal("expected a non-withdraw operation to fail")
	}
	if !strings.Contains(tb.Output(), "want LiquidityPoolWithdraw") {
		t.Errorf("failure message does not name the expected operation type:\n%s", tb.Output())
	}
}

func TestAssertLiquidityPoolWithdrawWrongPool(t *testing.T) {
	t.Parallel()

	op := withdrawOp(poolIDValue(0x7a), 70, 100, 200)

	tb := faketb.New()
	if focus.AssertLiquidityPoolWithdraw(tb, op, poolIDValue(0x7b), 70, 100, 200) {
		t.Fatal("expected a mismatched pool id to fail")
	}
	if !strings.Contains(tb.Output(), "withdraws from pool") {
		t.Errorf("failure message does not name the pool mismatch:\n%s", tb.Output())
	}
}

func TestAssertLiquidityPoolWithdrawMismatchedAmounts(t *testing.T) {
	t.Parallel()

	id := poolIDValue(0x7a)
	op := withdrawOp(id, 70, 100, 200)

	tests := []struct {
		name               string
		shares, minA, minB int64
		wantInMessage      string
	}{
		{"shares", 71, 100, 200, "shares"},
		{"minAmountA", 70, 101, 200, "minAmountA"},
		{"minAmountB", 70, 100, 201, "minAmountB"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertLiquidityPoolWithdraw(tb, op, id, xdr.Int64(tc.shares), xdr.Int64(tc.minA), xdr.Int64(tc.minB)) {
				t.Fatal("expected a mismatched amount to fail")
			}
			if !strings.Contains(tb.Output(), tc.wantInMessage) {
				t.Errorf("failure message does not name %q:\n%s", tc.wantInMessage, tb.Output())
			}
		})
	}
}

func TestRequireLiquidityPoolWithdraw(t *testing.T) {
	t.Parallel()

	id := poolIDValue(0x7a)
	op := withdrawOp(id, 70, 100, 200)

	tb := faketb.New()
	require.LiquidityPoolWithdraw(tb, op, id, 70, 100, 200)
	if tb.Failed() {
		t.Errorf("require.LiquidityPoolWithdraw failed against a matching operation:\n%s", tb.Output())
	}
}
