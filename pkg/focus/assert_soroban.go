package focus

import (
	"crypto/sha256"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertContractWasmHash checks the WASM hash used by an upload or contract
// creation operation.
func AssertContractWasmHash(t TB, op xdr.Operation, wantHash [32]byte, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertContractWasmHash"
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
		t.Errorf("%s: operation is %s, want InvokeHostFunction", assertion, op.Body.Type)
		return false
	}
	invoke, ok := op.Body.GetInvokeHostFunctionOp()
	if !ok {
		t.Errorf("%s: invoke host function operation has no parameters", assertion)
		return false
	}
	var gotHash [32]byte
	switch invoke.HostFunction.Type {
	case xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm:
		wasm, ok := invoke.HostFunction.GetWasm()
		if !ok {
			t.Errorf("%s: upload operation has no WASM bytecode", assertion)
			return false
		}
		gotHash = sha256.Sum256(wasm)
	case xdr.HostFunctionTypeHostFunctionTypeCreateContract:
		create, ok := invoke.HostFunction.GetCreateContract()
		if !ok {
			t.Errorf("%s: create contract operation has no parameters", assertion)
			return false
		}
		hash, ok := create.Executable.GetWasmHash()
		if !ok {
			t.Errorf("%s: executable is %s, want WASM", assertion, create.Executable.Type)
			return false
		}
		gotHash = [32]byte(hash)
	default:
		t.Errorf("%s: host function is %s, want upload WASM or create contract", assertion, invoke.HostFunction.Type)
		return false
	}
	_ = opts
	if gotHash != wantHash {
		t.Errorf("%s: WASM hash is %x, want %x", assertion, gotHash, wantHash)
		return false
	}
	return true
}
