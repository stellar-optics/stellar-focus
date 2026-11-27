package focus

import (
	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertLiquidityPoolWithdraw checks a LiquidityPoolWithdrawOp operation
// directly: the pool it withdraws from, the pool shares it burns, and the
// minimum amounts of each reserve asset it will accept in return.
//
// This reads the operation as submitted, not the ledger's record of what the
// protocol actually paid out — for that, see AssertLiquidityPoolWithdrawal,
// which measures the movement in the meta.
func AssertLiquidityPoolWithdraw(
	t TB, op xdr.Operation, poolID xdr.PoolId, shares xdr.Int64, minA, minB xdr.Int64, opts ...Option,
) bool {
	t.Helper()
	const assertion = "AssertLiquidityPoolWithdraw"
	_ = newConfig(opts...)

	if op.Body.Type != xdr.OperationTypeLiquidityPoolWithdraw || op.Body.LiquidityPoolWithdrawOp == nil {
		t.Errorf("%s: operation is %s, want LiquidityPoolWithdraw",
			assertion, operationTypeName(op.Body.Type))
		return false
	}
	withdraw := op.Body.LiquidityPoolWithdrawOp

	if withdraw.LiquidityPoolId != poolID {
		t.Errorf("%s: operation withdraws from pool %s, want %s",
			assertion, shortenID(describePoolID(withdraw.LiquidityPoolId)), shortenID(describePoolID(poolID)))
		return false
	}

	var diffs []fieldDifference
	if withdraw.Amount != shares {
		diffs = append(diffs, fieldDifference{
			field: "shares",
			want:  amountStroops(shares, ""),
			got:   amountStroops(withdraw.Amount, ""),
		})
	}
	if withdraw.MinAmountA != minA {
		diffs = append(diffs, fieldDifference{
			field: "minAmountA",
			want:  amountStroops(minA, ""),
			got:   amountStroops(withdraw.MinAmountA, ""),
		})
	}
	if withdraw.MinAmountB != minB {
		diffs = append(diffs, fieldDifference{
			field: "minAmountB",
			want:  amountStroops(minB, ""),
			got:   amountStroops(withdraw.MinAmountB, ""),
		})
	}
	if len(diffs) == 0 {
		return true
	}

	t.Errorf("%s: withdrawal from pool %s does not match\n\n%s",
		assertion, shortenID(describePoolID(poolID)), indent(renderFieldDiffs(diffs)))
	return false
}
