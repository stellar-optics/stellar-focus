package focus

import "github.com/stellar/go-stellar-sdk/xdr"

// AssertAccountFlags checks SetOptions flag masks and reports flag names.
func AssertAccountFlags(t TB, op xdr.Operation, wantSet, wantClear xdr.AccountFlags, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertAccountFlags"
	if op.Body.Type != xdr.OperationTypeSetOptions || op.Body.SetOptionsOp == nil {
		t.Errorf("%s: operation is %s, want SetOptions", assertion, operationTypeName(op.Body.Type))
		return false
	}
	var gotSet, gotClear xdr.AccountFlags
	if op.Body.SetOptionsOp.SetFlags != nil {
		gotSet = xdr.AccountFlags(*op.Body.SetOptionsOp.SetFlags)
	}
	if op.Body.SetOptionsOp.ClearFlags != nil {
		gotClear = xdr.AccountFlags(*op.Body.SetOptionsOp.ClearFlags)
	}
	if gotSet != wantSet || gotClear != wantClear {
		t.Errorf("%s: want set: %s, got set: %s; want clear: %s, got clear: %s", assertion,
			accountFlagNames(wantSet), accountFlagNames(gotSet), accountFlagNames(wantClear), accountFlagNames(gotClear))
		return false
	}
	_ = opts
	return true
}
