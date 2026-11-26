package focus

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertLiquidityPoolWithdraw checks the pool id, shares and minimum reserve
// amounts on a liquidity pool withdrawal operation.
func AssertLiquidityPoolWithdraw(t TB, op xdr.Operation, poolID xdr.PoolId, shares xdr.Int64, minA, minB xdr.Int64, opts ...Option) bool {
	t.Helper()
	name := newConfig(opts...).assertionName("AssertLiquidityPoolWithdraw")
	withdraw, ok := op.Body.GetLiquidityPoolWithdrawOp()
	if !ok {
		t.Errorf("%s: operation type is %s, want LiquidityPoolWithdraw", name, op.Body.Type)
		return false
	}
	var diffs []string
	if withdraw.LiquidityPoolId != poolID {
		diffs = append(diffs, fmt.Sprintf("pool id: got %x, want %x", withdraw.LiquidityPoolId, poolID))
	}
	if withdraw.Amount != shares {
		diffs = append(diffs, fmt.Sprintf("shares: got %d stroops (%s), want %d stroops (%s)", withdraw.Amount, formatPoolUnits(int64(withdraw.Amount)), shares, formatPoolUnits(int64(shares))))
	}
	if withdraw.MinAmountA != minA {
		diffs = append(diffs, fmt.Sprintf("minimum asset A: got %d stroops (%s), want %d stroops (%s)", withdraw.MinAmountA, formatPoolUnits(int64(withdraw.MinAmountA)), minA, formatPoolUnits(int64(minA))))
	}
	if withdraw.MinAmountB != minB {
		diffs = append(diffs, fmt.Sprintf("minimum asset B: got %d stroops (%s), want %d stroops (%s)", withdraw.MinAmountB, formatPoolUnits(int64(withdraw.MinAmountB)), minB, formatPoolUnits(int64(minB))))
	}
	if len(diffs) == 0 {
		return true
	}
	for i, diff := range diffs {
		if i == 0 {
			t.Errorf("%s: liquidity pool withdrawal does not match", name)
		}
		t.Errorf("  %s", diff)
	}
	return false
}

func formatPoolUnits(stroops int64) string {
	negative := stroops < 0
	magnitude := uint64(stroops)
	if negative {
		magnitude = uint64(-(stroops + 1)) + 1
	}
	whole, fraction := magnitude/10_000_000, magnitude%10_000_000
	if negative {
		return fmt.Sprintf("-%d.%07d", whole, fraction)
	}
	return fmt.Sprintf("%d.%07d", whole, fraction)
}
