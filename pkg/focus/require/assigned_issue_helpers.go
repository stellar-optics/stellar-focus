package require

import (
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ContractWasmHash is focus.AssertContractWasmHash, stopping on failure.
func ContractWasmHash(t focus.TB, op xdr.Operation, wantHash [32]byte, opts ...focus.Option) {
	t.Helper()
	focus.AssertContractWasmHash(fatal(t), op, wantHash, opts...)
}

// ClaimableBalanceID is focus.AssertClaimableBalanceID, stopping on failure.
func ClaimableBalanceID(t focus.TB, result xdr.OperationResult, wantID any, opts ...focus.Option) {
	t.Helper()
	focus.AssertClaimableBalanceID(fatal(t), result, wantID, opts...)
}
