package focus

import (
	"reflect"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertInvokeHostFunctionArgs checks the contract, function and arguments of an invocation.
func AssertInvokeHostFunctionArgs(t TB, op xdr.Operation, wantContractID, wantFunction string, wantArgs []any, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertInvokeHostFunctionArgs"
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
		t.Errorf("%s: operation is %s, want InvokeHostFunction", assertion, operationTypeName(op.Body.Type))
		return false
	}
	host := op.Body.InvokeHostFunctionOp.HostFunction
	if host.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract || host.InvokeContract == nil {
		t.Errorf("%s: host function is %s, want InvokeContract", assertion, host.Type)
		return false
	}
	call := host.InvokeContract
	actualID := ""
	if call.ContractAddress.Type == xdr.ScAddressTypeScAddressTypeContract && call.ContractAddress.ContractId != nil {
		actualID, _ = strkey.Encode(strkey.VersionByteContract, call.ContractAddress.ContractId[:])
	}
	if actualID != wantContractID {
		t.Errorf("%s: contract ID is %s, want %s", assertion, actualID, wantContractID)
		return false
	}
	if string(call.FunctionName) != wantFunction {
		t.Errorf("%s: function is %q, want %q", assertion, call.FunctionName, wantFunction)
		return false
	}
	if len(call.Args) != len(wantArgs) {
		t.Errorf("%s: argument count is %d, want %d", assertion, len(call.Args), len(wantArgs))
		return false
	}
	for i, value := range wantArgs {
		var want xdr.ScVal
		var err error
		if b, ok := value.([]byte); ok {
			copyBytes := append(xdr.ScBytes(nil), b...)
			want = xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &copyBytes}
		} else {
			want, err = constructorValue(value)
		}
		if err != nil {
			t.Errorf("%s: argument %d could not be coerced: %v", assertion, i, err)
			return false
		}
		wantRaw, wantErr := want.MarshalBinary()
		gotRaw, gotErr := call.Args[i].MarshalBinary()
		if wantErr != nil || gotErr != nil || !reflect.DeepEqual(wantRaw, gotRaw) {
			wantText, gotText := describeScVal(want), describeScVal(call.Args[i])
			t.Errorf("%s: argument %d differs:\n  want: %s\n   got: %s", assertion, i, wantText, gotText)
			return false
		}
	}
	_ = opts
	return true
}
