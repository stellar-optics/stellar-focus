package focus_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func TestAssertLiquidityPoolDepositOperation(t *testing.T) {
	var poolID xdr.PoolId
	poolID[0] = 1
	deposit := xdr.LiquidityPoolDepositOp{
		LiquidityPoolId: poolID,
		MaxAmountA:      12_345_678,
		MaxAmountB:      9_000_000,
		MinPrice:        xdr.Price{N: 1, D: 2},
		MaxPrice:        xdr.Price{N: 2, D: 3},
	}
	body, err := xdr.NewOperationBody(xdr.OperationTypeLiquidityPoolDeposit, deposit)
	if err != nil {
		t.Fatal(err)
	}
	op := xdr.Operation{Body: body}
	if !focus.AssertLiquidityPoolDeposit(faketb.New(), op, poolID, deposit.MaxAmountA, deposit.MaxAmountB, deposit.MinPrice, deposit.MaxPrice) {
		t.Fatal("matching deposit failed")
	}

	tb := faketb.New()
	if focus.AssertLiquidityPoolDeposit(tb, op, poolID, 1, deposit.MaxAmountB, xdr.Price{N: 3, D: 4}, deposit.MaxPrice) {
		t.Fatal("mismatched deposit passed")
	}
	for _, want := range []string{"maxAmountA", "stroops", "minPrice", "1/2", "3/4"} {
		if !strings.Contains(tb.Output(), want) {
			t.Errorf("failure %q does not mention %q", tb.Output(), want)
		}
	}
}

func TestAssertContractWasmHashUploadAndCreate(t *testing.T) {
	wasm := []byte("example wasm")
	want := sha256.Sum256(wasm)
	upload, err := xdr.NewHostFunction(xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm, wasm)
	if err != nil {
		t.Fatal(err)
	}
	if !focus.AssertContractWasmHash(faketb.New(), invokeOperation(t, upload), want) {
		t.Fatal("matching upload hash failed")
	}

	executableHash := xdr.Hash(want)
	executable := xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &executableHash}
	create, err := xdr.NewHostFunction(xdr.HostFunctionTypeHostFunctionTypeCreateContract, xdr.CreateContractArgs{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	if !focus.AssertContractWasmHash(faketb.New(), invokeOperation(t, create), want) {
		t.Fatal("matching create contract hash failed")
	}

	wrong := faketb.New()
	focus.AssertContractWasmHash(wrong, invokeOperation(t, create), [32]byte{})
	if !strings.Contains(wrong.Output(), hex.EncodeToString(want[:])) {
		t.Fatalf("hash mismatch should print expected hash in hex: %s", wrong.Output())
	}
}

func invokeOperation(t *testing.T, host xdr.HostFunction) xdr.Operation {
	t.Helper()
	body, err := xdr.NewOperationBody(xdr.OperationTypeInvokeHostFunction, xdr.InvokeHostFunctionOp{HostFunction: host})
	if err != nil {
		t.Fatal(err)
	}
	return xdr.Operation{Body: body}
}

func TestAssertClaimableBalanceIDInputs(t *testing.T) {
	var hash xdr.Hash
	hash[0] = 0xab
	id, err := xdr.NewClaimableBalanceId(xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0, hash)
	if err != nil {
		t.Fatal(err)
	}
	result := claimableBalanceOperationResult(t, id)
	for _, expected := range []any{id, [32]byte(hash), hash[:], hex.EncodeToString(hash[:]), base64.StdEncoding.EncodeToString(hash[:])} {
		if !focus.AssertClaimableBalanceID(faketb.New(), result, expected) {
			t.Errorf("matching ID input %T failed", expected)
		}
	}

	wrong := faketb.New()
	badHash := hash
	badHash[1] = 1
	focus.AssertClaimableBalanceID(wrong, result, badHash)
	if !strings.Contains(wrong.Output(), hex.EncodeToString(hash[:])) {
		t.Fatalf("mismatch should show actual ID in hex: %s", wrong.Output())
	}
}

func claimableBalanceOperationResult(t *testing.T, id xdr.ClaimableBalanceId) xdr.OperationResult {
	t.Helper()
	claimableResult, err := xdr.NewCreateClaimableBalanceResult(xdr.CreateClaimableBalanceResultCodeCreateClaimableBalanceSuccess, id)
	if err != nil {
		t.Fatal(err)
	}
	resultBody, err := xdr.NewOperationResultTr(xdr.OperationTypeCreateClaimableBalance, claimableResult)
	if err != nil {
		t.Fatal(err)
	}
	result, err := xdr.NewOperationResult(xdr.OperationResultCodeOpInner, resultBody)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
