package focus

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestAssertSetTrustLineFlags(t *testing.T) {
	const account = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	id, err := xdr.AddressToAccountId(account)
	if err != nil {
		t.Fatal(err)
	}
	asset := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	op := xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeSetTrustLineFlags, SetTrustLineFlagsOp: &xdr.SetTrustLineFlagsOp{
		Trustor: id, Asset: asset, SetFlags: xdr.Uint32(xdr.TrustLineFlagsAuthorizedFlag),
	}}}
	if !AssertSetTrustLineFlags(t, op, account, asset, uint32(xdr.TrustLineFlagsAuthorizedFlag), 0) {
		t.Fatal("expected flag masks to match")
	}
	recorder := &assertionRecorder{}
	if AssertSetTrustLineFlags(recorder, op, account, asset, 0, 0) {
		t.Fatal("expected mismatched flags to fail")
	}
	if len(recorder.messages) != 1 || !strings.Contains(recorder.messages[0], "AUTHORIZED") {
		t.Fatalf("expected named flag diagnostics, got %v", recorder.messages)
	}
}
