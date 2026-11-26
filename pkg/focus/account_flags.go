package focus

import "github.com/stellar/go-stellar-sdk/xdr"

// AssertAccountFlags checks the set and clear flag masks on a SetOptions
// operation.
func AssertAccountFlags(
	t TestingT, op xdr.Operation, wantSet xdr.AccountFlags, wantClear xdr.AccountFlags, opts ...Option,
) bool {
	t.Helper()
	const assertion = "AssertAccountFlags"
	_ = newConfig(opts...)

	if op.Body.Type != xdr.OperationTypeSetOptions || op.Body.SetOptionsOp == nil {
		t.Errorf("%s: operation is %s, want SetOptions",
			assertion, operationTypeName(op.Body.Type))
		return false
	}

	var gotSet, gotClear uint32
	if op.Body.SetOptionsOp.SetFlags != nil {
		gotSet = uint32(*op.Body.SetOptionsOp.SetFlags)
	}
	if op.Body.SetOptionsOp.ClearFlags != nil {
		gotClear = uint32(*op.Body.SetOptionsOp.ClearFlags)
	}
	if gotSet == uint32(wantSet) && gotClear == uint32(wantClear) {
		return true
	}

	t.Errorf("%s: want set: [%s], got set: [%s]; want clear: [%s], got clear: [%s]",
		assertion,
		DescribeAccountFlags(uint32(wantSet)), DescribeAccountFlags(gotSet),
		DescribeAccountFlags(uint32(wantClear)), DescribeAccountFlags(gotClear))
	return false
}
