package focus

import "github.com/stellar/go-stellar-sdk/xdr"

// AssertSetTrustLineFlags checks the trustor, asset and flag masks of an operation.
func AssertSetTrustLineFlags(t TB, op xdr.Operation, wantTrustor string, wantAsset xdr.Asset, wantSet, wantClear uint32, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertSetTrustLineFlags"
	if op.Body.Type != xdr.OperationTypeSetTrustLineFlags || op.Body.SetTrustLineFlagsOp == nil {
		t.Errorf("%s: operation is %s, want SetTrustLineFlags", assertion, operationTypeName(op.Body.Type))
		return false
	}
	flags := op.Body.SetTrustLineFlagsOp
	gotTrustor, ok := accountAddressOf(t, assertion, flags.Trustor)
	if !ok {
		return false
	}
	want, err := addressFromString(wantTrustor)
	if err != nil {
		t.Errorf("%s: trustor could not be read: %v", assertion, err)
		return false
	}
	if gotTrustor != want {
		t.Errorf("%s: trustor is %s, want %s", assertion, gotTrustor, want)
		return false
	}
	if !flags.Asset.Equals(wantAsset) {
		t.Errorf("%s: asset differs: got %v, want %v", assertion, flags.Asset, wantAsset)
		return false
	}
	gotSet, gotClear := uint32(flags.SetFlags), uint32(flags.ClearFlags)
	if gotSet != wantSet || gotClear != wantClear {
		t.Errorf("%s: flag changes differ: got set %s, clear %s; want set %s, clear %s", assertion,
			trustLineFlagNames(gotSet), trustLineFlagNames(gotClear), trustLineFlagNames(wantSet), trustLineFlagNames(wantClear))
		return false
	}
	_ = opts
	return true
}
