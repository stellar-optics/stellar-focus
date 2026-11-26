package focus

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// assertLiquidityPoolDepositOperation checks the pool and all deposit limits
// on an operation.
func assertLiquidityPoolDepositOperation(t TB, op xdr.Operation, poolID xdr.PoolId, maxA, maxB xdr.Int64, minPrice, maxPrice xdr.Price, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertLiquidityPoolDeposit"
	if op.Body.Type != xdr.OperationTypeLiquidityPoolDeposit {
		t.Errorf("%s: operation is %s, want LiquidityPoolDeposit", assertion, op.Body.Type)
		return false
	}
	deposit, ok := op.Body.GetLiquidityPoolDepositOp()
	if !ok {
		t.Errorf("%s: liquidity pool deposit operation has no parameters", assertion)
		return false
	}
	var differences []string
	if deposit.LiquidityPoolId != poolID {
		differences = append(differences, fmt.Sprintf("poolID: %x != %x", poolID, deposit.LiquidityPoolId))
	}
	if deposit.MaxAmountA != maxA {
		differences = append(differences, fmt.Sprintf("maxAmountA: want %s, got %s", formatPoolAmount(int64(maxA)), formatPoolAmount(int64(deposit.MaxAmountA))))
	}
	if deposit.MaxAmountB != maxB {
		differences = append(differences, fmt.Sprintf("maxAmountB: want %s, got %s", formatPoolAmount(int64(maxB)), formatPoolAmount(int64(deposit.MaxAmountB))))
	}
	if deposit.MinPrice != minPrice {
		differences = append(differences, fmt.Sprintf("minPrice: want %s, got %s", formatPoolPrice(minPrice), formatPoolPrice(deposit.MinPrice)))
	}
	if deposit.MaxPrice != maxPrice {
		differences = append(differences, fmt.Sprintf("maxPrice: want %s, got %s", formatPoolPrice(maxPrice), formatPoolPrice(deposit.MaxPrice)))
	}
	_ = opts
	if len(differences) != 0 {
		t.Errorf("%s: %s", assertion, strings.Join(differences, "; "))
		return false
	}
	return true
}

func formatPoolAmount(stroops int64) string {
	units := new(big.Rat).SetFrac(big.NewInt(stroops), big.NewInt(10_000_000)).FloatString(7)
	return fmt.Sprintf("%d stroops (%s units)", stroops, units)
}

func formatPoolPrice(price xdr.Price) string {
	return fmt.Sprintf("%d/%d", price.N, price.D)
}
