package focus_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func uploadWasmOperation(wasm []byte) xdr.Operation {
	return xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm,
					Wasm: &wasm,
				},
			},
		},
	}
}

func createContractOperation(hash xdr.Hash) xdr.Operation {
	return xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeCreateContract,
					CreateContract: &xdr.CreateContractArgs{
						Executable: xdr.ContractExecutable{
							Type:     xdr.ContractExecutableTypeContractExecutableWasm,
							WasmHash: &hash,
						},
					},
				},
			},
		},
	}
}

func TestAssertContractWasmHashUpload(t *testing.T) {
	t.Parallel()

	wasm := []byte("\x00asm fake wasm bytecode for a test fixture")
	want := sha256.Sum256(wasm)

	tb := faketb.New()
	if !focus.AssertContractWasmHash(tb, uploadWasmOperation(wasm), want) {
		t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
	}
}

func TestAssertContractWasmHashCreateContract(t *testing.T) {
	t.Parallel()

	var hash xdr.Hash
	for i := range hash {
		hash[i] = byte(i + 1)
	}

	tb := faketb.New()
	if !focus.AssertContractWasmHash(tb, createContractOperation(hash), [32]byte(hash)) {
		t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
	}
}

func TestAssertContractWasmHashMismatch(t *testing.T) {
	t.Parallel()

	var hash, want xdr.Hash
	for i := range hash {
		hash[i] = 0x11
		want[i] = 0x11
	}
	want[5] = 0x99

	tb := faketb.New()
	if focus.AssertContractWasmHash(tb, createContractOperation(hash), [32]byte(want)) {
		t.Fatal("assertion unexpectedly passed")
	}
	msg := tb.Output()
	if !strings.Contains(msg, "^") {
		t.Errorf("failure message does not highlight the differing character:\n%s", msg)
	}

	tb2 := faketb.New()
	require.ContractWasmHash(tb2, createContractOperation(hash), [32]byte(want))
	if !tb2.FatalfCalled() {
		t.Errorf("require.ContractWasmHash did not stop the test on a mismatch:\n%s", tb2.Output())
	}
}

func TestAssertContractWasmHashWrongOperation(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	op := xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypePayment}}
	if focus.AssertContractWasmHash(tb, op, [32]byte{}) {
		t.Fatal("assertion unexpectedly passed for a non-InvokeHostFunction operation")
	}
}

func TestAssertContractWasmHashCreateContractWithoutWasm(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	op := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeCreateContract,
					CreateContract: &xdr.CreateContractArgs{
						Executable: xdr.ContractExecutable{
							Type: xdr.ContractExecutableTypeContractExecutableStellarAsset,
						},
					},
				},
			},
		},
	}
	if focus.AssertContractWasmHash(tb, op, [32]byte{}) {
		t.Fatal("assertion unexpectedly passed for a non-WASM executable")
	}
}
