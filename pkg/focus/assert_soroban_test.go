package focus

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestAssertInvokeHostFunctionArgs(t *testing.T) {
	var id xdr.ContractId
	copy(id[:], []byte("focus-test-contract-id-32bytes!!"))
	contractID, err := strkey.Encode(strkey.VersionByteContract, id[:])
	if err != nil {
		t.Fatal(err)
	}
	name := xdr.ScSymbol("test")
	arg, err := constructorValue(uint64(7))
	if err != nil {
		t.Fatal(err)
	}
	symbol := xdr.ScSymbol("hello")
	bytes := xdr.ScBytes{1, 2, 3}
	truth := true
	op := xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeInvokeHostFunction, InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
		HostFunction: xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract, InvokeContract: &xdr.InvokeContractArgs{
			ContractAddress: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}, FunctionName: name, Args: []xdr.ScVal{
				arg,
				{Type: xdr.ScValTypeScvSymbol, Sym: &symbol},
				{Type: xdr.ScValTypeScvBytes, Bytes: &bytes},
				{Type: xdr.ScValTypeScvBool, B: &truth},
			},
		}},
	}}}
	wantArgs := []any{uint64(7), "hello", []byte{1, 2, 3}, true}
	if !AssertInvokeHostFunctionArgs(t, op, contractID, "test", wantArgs) {
		t.Fatal("expected arguments to match")
	}
	recorder := &assertionRecorder{}
	if AssertInvokeHostFunctionArgs(recorder, op, contractID, "test", []any{uint64(8), "hello", []byte{1, 2, 3}, true}) {
		t.Fatal("expected mismatched argument to fail")
	}
	if len(recorder.messages) != 1 || !strings.Contains(recorder.messages[0], "argument 0") || !strings.Contains(recorder.messages[0], "want: u64(8)") || !strings.Contains(recorder.messages[0], "got: u64(7)") {
		t.Fatalf("expected indexed argument diff, got %v", recorder.messages)
	}
	for _, mismatch := range []struct {
		contract, function string
		want               []any
		field              string
	}{
		{contractID + "x", "test", wantArgs, "contract ID"},
		{contractID, "other", wantArgs, "function"},
		{contractID, "test", append(wantArgs, true), "argument count"},
	} {
		recorder := &assertionRecorder{}
		if AssertInvokeHostFunctionArgs(recorder, op, mismatch.contract, mismatch.function, mismatch.want) {
			t.Fatalf("expected %s mismatch to fail", mismatch.field)
		}
		if len(recorder.messages) != 1 || !strings.Contains(recorder.messages[0], mismatch.field) {
			t.Fatalf("expected %s diagnostic, got %v", mismatch.field, recorder.messages)
		}
	}
}
