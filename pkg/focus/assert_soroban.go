package focus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertContractWasmHash checks that an InvokeHostFunction operation
// references the wanted WASM hash, whether the operation uploads the
// bytecode directly or creates a contract instance from an
// already-uploaded hash.
//
// For HostFunctionTypeUploadContractWasm, wantHash is compared against the
// SHA-256 of the uploaded bytecode — the hash the protocol assigns the code
// once the upload lands. For HostFunctionTypeCreateContract with a WASM
// executable, wantHash is compared against the hash the operation names
// directly.
func AssertContractWasmHash(t TB, op xdr.Operation, wantHash [32]byte, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertContractWasmHash"
	_ = newConfig(opts...)

	got, ok := wasmHashOf(t, assertion, op)
	if !ok {
		return false
	}

	if got == wantHash {
		return true
	}

	t.Errorf("%s: WASM hash does not match\n%s",
		assertion, indent(highlightHexDiff(hex.EncodeToString(wantHash[:]), hex.EncodeToString(got[:]))))
	return false
}

// wasmHashOf reads the WASM hash an InvokeHostFunction operation carries,
// computing it from the uploaded bytecode for an upload and reading it
// directly for a contract creation.
func wasmHashOf(t TB, assertion string, op xdr.Operation) ([32]byte, bool) {
	t.Helper()

	if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
		t.Errorf("%s: operation is %s, want InvokeHostFunction",
			assertion, operationTypeName(op.Body.Type))
		return [32]byte{}, false
	}

	var (
		hash  [32]byte
		found bool
		fail  string
	)
	ok := guard(t, assertion, func() {
		hf := op.Body.InvokeHostFunctionOp.HostFunction
		switch hf.Type {
		case xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm:
			if hf.Wasm == nil {
				fail = "the operation uploads no WASM bytecode"
				return
			}
			hash = sha256.Sum256(*hf.Wasm)
			found = true
		case xdr.HostFunctionTypeHostFunctionTypeCreateContract:
			if hf.CreateContract == nil {
				fail = "the operation carries no create-contract arguments"
				return
			}
			exec := hf.CreateContract.Executable
			if exec.Type != xdr.ContractExecutableTypeContractExecutableWasm || exec.WasmHash == nil {
				fail = fmt.Sprintf("the contract executable is %s, not a WASM hash",
					strings.TrimPrefix(exec.Type.String(), "ContractExecutableTypeContractExecutable"))
				return
			}
			hash = [32]byte(*exec.WasmHash)
			found = true
		default:
			fail = fmt.Sprintf("host function is %s, want UploadContractWasm or CreateContract",
				describeHostFunction(&hf))
		}
	})
	if !ok {
		return [32]byte{}, false
	}
	if !found {
		t.Errorf("%s: %s", assertion, fail)
		return [32]byte{}, false
	}
	return hash, true
}
