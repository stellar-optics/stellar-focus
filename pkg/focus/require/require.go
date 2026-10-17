// Package require mirrors the assertions in pkg/focus with Fatalf semantics.
//
// focus assertions report through Errorf and let the test continue, which is
// what you want in a table test that should report every problem in one run.
// These stop the test at the first failure, which is what you want when
// everything after the assertion would be meaningless — a decode that failed,
// or a nil result there is no point inspecting further.
//
// The split, and the naming, follow the convention Go developers already know
// from testify's assert and require packages.
package require

import (
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// fatalTB adapts a focus.TB so reported failures become fatal.
//
// Errorf is routed to Fatalf, so the underlying testing.T stops the test.
// Everything else passes straight through.
type fatalTB struct {
	focus.TB
}

// Errorf routes to Fatalf on the embedded TB. This is not recursive: only
// Errorf is overridden here, so Helper and Fatalf resolve to the wrapped
// implementation.
func (f fatalTB) Errorf(format string, args ...any) {
	f.Helper()
	f.Fatalf(format, args...)
}

func fatal(t focus.TB) focus.TB {
	return fatalTB{TB: t}
}

// EnvelopeEqual is focus.AssertEnvelopeEqual, stopping the test on failure.
func EnvelopeEqual(t focus.TB, want, got focus.Value, opts ...focus.Option) {
	t.Helper()
	focus.AssertEnvelopeEqual(fatal(t), want, got, opts...)
}

// ResultEqual is focus.AssertResultEqual, stopping the test on failure.
func ResultEqual(t focus.TB, want, got focus.Value, opts ...focus.Option) {
	t.Helper()
	focus.AssertResultEqual(fatal(t), want, got, opts...)
}

// MetaEqual is focus.AssertMetaEqual, stopping the test on failure.
func MetaEqual(t focus.TB, want, got focus.Value, opts ...focus.Option) {
	t.Helper()
	focus.AssertMetaEqual(fatal(t), want, got, opts...)
}

// Equal is focus.AssertEqual, stopping the test on failure.
func Equal(t focus.TB, want, got focus.Value, opts ...focus.Option) {
	t.Helper()
	focus.AssertEqual(fatal(t), want, got, opts...)
}

// EqualAs is focus.AssertEqualAs, stopping the test on failure.
func EqualAs(t focus.TB, typeName string, want, got focus.Value, opts ...focus.Option) {
	t.Helper()
	focus.AssertEqualAs(fatal(t), typeName, want, got, opts...)
}

// SourceAccount is focus.AssertSourceAccount, stopping the test on failure.
func SourceAccount(t focus.TB, envelope focus.Value, want string) {
	t.Helper()
	focus.AssertSourceAccount(fatal(t), envelope, want)
}

// Fee is focus.AssertFee, stopping the test on failure.
func Fee(t focus.TB, envelope focus.Value, want int64) {
	t.Helper()
	focus.AssertFee(fatal(t), envelope, want)
}

// Sequence is focus.AssertSequence, stopping the test on failure.
func Sequence(t focus.TB, envelope focus.Value, want int64) {
	t.Helper()
	focus.AssertSequence(fatal(t), envelope, want)
}

// TimeboundsValid is focus.AssertTimeboundsValid, stopping the test on
// failure.
func TimeboundsValid(t focus.TB, envelope focus.Value, minTime, maxTime int64) {
	t.Helper()
	focus.AssertTimeboundsValid(fatal(t), envelope, minTime, maxTime)
}

// LedgerboundsValid is focus.AssertLedgerboundsValid, stopping the test on
// failure.
func LedgerboundsValid(t focus.TB, envelope focus.Value, minLedger, maxLedger uint32) {
	t.Helper()
	focus.AssertLedgerboundsValid(fatal(t), envelope, minLedger, maxLedger)
}

// Memo is focus.AssertMemo, stopping the test on failure.
func Memo(t focus.TB, envelope focus.Value, want string) {
	t.Helper()
	focus.AssertMemo(fatal(t), envelope, want)
}

// MemoTextEqual is focus.AssertMemoTextEqual, stopping the test on failure.
func MemoTextEqual(t focus.TB, envelope focus.Value, want string) {
	t.Helper()
	focus.AssertMemoTextEqual(fatal(t), envelope, want)
}

// MemoIDEqual is focus.AssertMemoIDEqual, stopping the test on failure.
func MemoIDEqual(t focus.TB, envelope focus.Value, want uint64) {
	t.Helper()
	focus.AssertMemoIDEqual(fatal(t), envelope, want)
}

// MemoHashEqual is focus.AssertMemoHashEqual, stopping the test on failure.
func MemoHashEqual(t focus.TB, envelope focus.Value, want focus.Value) {
	t.Helper()
	focus.AssertMemoHashEqual(fatal(t), envelope, want)
}

// MemoReturnEqual is focus.AssertMemoReturnEqual, stopping the test on
// failure.
func MemoReturnEqual(t focus.TB, envelope focus.Value, want focus.Value) {
	t.Helper()
	focus.AssertMemoReturnEqual(fatal(t), envelope, want)
}

// OperationCount is focus.AssertOperationCount, stopping the test on failure.
func OperationCount(t focus.TB, envelope focus.Value, want int) {
	t.Helper()
	focus.AssertOperationCount(fatal(t), envelope, want)
}

// OperationTypes is focus.AssertOperationTypes, stopping the test on failure.
func OperationTypes(t focus.TB, envelope focus.Value, want ...string) {
	t.Helper()
	focus.AssertOperationTypes(fatal(t), envelope, want...)
}

// OperationSequence is focus.AssertOperationSequence, stopping the test on
// failure.
func OperationSequence(t focus.TB, envelope focus.Value, want []xdr.OperationType) {
	t.Helper()
	focus.AssertOperationSequence(fatal(t), envelope, want)
}

// SignatureCount is focus.AssertSignatureCount, stopping the test on failure.
func SignatureCount(t focus.TB, envelope focus.Value, want int) {
	t.Helper()
	focus.AssertSignatureCount(fatal(t), envelope, want)
}

// SignaturesPresent is focus.AssertSignaturesPresent, stopping the test on
// failure.
func SignaturesPresent(t focus.TB, envelope focus.Value, expectedPubKeys ...focus.Value) {
	t.Helper()
	focus.AssertSignaturesPresent(fatal(t), envelope, expectedPubKeys...)
}

// SignatureThreshold is focus.AssertSignatureThreshold, stopping the test on
// failure.
func SignatureThreshold(t focus.TB, envelope focus.Value, threshold int, expectedPubKeys ...focus.Value) {
	t.Helper()
	focus.AssertSignatureThreshold(fatal(t), envelope, threshold, expectedPubKeys...)
}

// Succeeded is focus.AssertSucceeded, stopping the test on failure.
func Succeeded(t focus.TB, result focus.Value) {
	t.Helper()
	focus.AssertSucceeded(fatal(t), result)
}

// Failed is focus.AssertFailed, stopping the test on failure.
func Failed(t focus.TB, result focus.Value) {
	t.Helper()
	focus.AssertFailed(fatal(t), result)
}

// ResultCode is focus.AssertResultCode, stopping the test on failure.
func ResultCode(t focus.TB, result focus.Value, want string) {
	t.Helper()
	focus.AssertResultCode(fatal(t), result, want)
}

// OperationFailed is focus.AssertOperationFailed, stopping the test on failure.
func OperationFailed(t focus.TB, result focus.Value, index int, want string) {
	t.Helper()
	focus.AssertOperationFailed(fatal(t), result, index, want)
}

// OperationSucceeded is focus.AssertOperationSucceeded, stopping the test on
// failure.
func OperationSucceeded(t focus.TB, result focus.Value, index int) {
	t.Helper()
	focus.AssertOperationSucceeded(fatal(t), result, index)
}

// OperationResultCode is focus.AssertOperationResultCode, stopping the test on
// failure.
func OperationResultCode(t focus.TB, result focus.Value, index int, want string) {
	t.Helper()
	focus.AssertOperationResultCode(fatal(t), result, index, want)
}

// ConsumedFeeWithin is focus.AssertConsumedFeeWithin, stopping the test on
// failure.
func ConsumedFeeWithin(t focus.TB, result focus.Value, limit int64) {
	t.Helper()
	focus.AssertConsumedFeeWithin(fatal(t), result, limit)
}

// RefundFeeEqual is focus.AssertRefundFeeEqual, stopping the test on failure.
func RefundFeeEqual(t focus.TB, meta focus.Value, want int64) {
	t.Helper()
	focus.AssertRefundFeeEqual(fatal(t), meta, want)
}

// SimulationResourcesWithin is focus.AssertSimulationResourcesWithin, stopping
// the test on failure.
func SimulationResourcesWithin(t focus.TB, simResponse focus.Value, maxCPU, maxMem uint32) {
	t.Helper()
	focus.AssertSimulationResourcesWithin(fatal(t), simResponse, maxCPU, maxMem)
}

// EventEmitted is focus.AssertEventEmitted, stopping the test on failure.
func EventEmitted(t focus.TB, meta focus.Value, want focus.Event) {
	t.Helper()
	focus.AssertEventEmitted(fatal(t), meta, want)
}

// EventCount is focus.AssertEventCount, stopping the test on failure.
func EventCount(t focus.TB, meta focus.Value, want int) {
	t.Helper()
	focus.AssertEventCount(fatal(t), meta, want)
}

// ReturnValue is focus.AssertReturnValue, stopping the test on failure.
func ReturnValue(t focus.TB, meta focus.Value, want focus.Value) {
	t.Helper()
	focus.AssertReturnValue(fatal(t), meta, want)
}

// ContractTrapped is focus.AssertContractTrapped, stopping the test on failure.
func ContractTrapped(t focus.TB, result focus.Value) {
	t.Helper()
	focus.AssertContractTrapped(fatal(t), result)
}

// ConstructorState is focus.AssertConstructorState, stopping the test on
// failure.
func ConstructorState(t focus.TB, meta focus.Value, want focus.ConstructorState) {
	t.Helper()
	focus.AssertConstructorState(fatal(t), meta, want)
}

// ContractOwner is focus.AssertContractOwner, stopping the test on failure.
func ContractOwner(t focus.TB, meta focus.Value, contractID, wantOwner string) {
	t.Helper()
	focus.AssertContractOwner(fatal(t), meta, contractID, wantOwner)
}

// ContractAuthValid is focus.AssertContractAuthValid, stopping the test on
// failure.
func ContractAuthValid(t focus.TB, envelope focus.Value, expectedInvoker string, expectedCall focus.ContractCall) {
	t.Helper()
	focus.AssertContractAuthValid(fatal(t), envelope, expectedInvoker, expectedCall)
}

// DiagnosticEventOrder is focus.AssertDiagnosticEventOrder, stopping the test
// on failure.
func DiagnosticEventOrder(t focus.TB, meta focus.Value, want []focus.DiagnosticEventMatch) {
	t.Helper()
	focus.AssertDiagnosticEventOrder(fatal(t), meta, want)
}

// DiagnosticEventTimeline is focus.AssertDiagnosticEventTimeline, stopping the
// test on failure.
func DiagnosticEventTimeline(t focus.TB, meta focus.Value, want focus.DiagnosticTimeline) {
	t.Helper()
	focus.AssertDiagnosticEventTimeline(fatal(t), meta, want)
}

// EntryCreated is focus.AssertEntryCreated, stopping the test on failure.
func EntryCreated(t focus.TB, meta, key focus.Value) {
	t.Helper()
	focus.AssertEntryCreated(fatal(t), meta, key)
}

// EntryModified is focus.AssertEntryModified, stopping the test on failure.
func EntryModified(t focus.TB, meta, key focus.Value) {
	t.Helper()
	focus.AssertEntryModified(fatal(t), meta, key)
}

// EntryDeleted is focus.AssertEntryDeleted, stopping the test on failure.
func EntryDeleted(t focus.TB, meta, key focus.Value) {
	t.Helper()
	focus.AssertEntryDeleted(fatal(t), meta, key)
}

// BalanceChanged is focus.AssertBalanceChanged, stopping the test on failure.
func BalanceChanged(t focus.TB, meta, account, asset focus.Value, delta int64) {
	t.Helper()
	focus.AssertBalanceChanged(fatal(t), meta, account, asset, delta)
}

// TrustlineModified is focus.AssertTrustlineModified, stopping the test on
// failure.
func TrustlineModified(t focus.TB, meta, account, asset focus.Value, expectedLimit int64) {
	t.Helper()
	focus.AssertTrustlineModified(fatal(t), meta, account, asset, expectedLimit)
}

// SignersEqual is focus.AssertSignersEqual, stopping the test on failure.
func SignersEqual(t focus.TB, meta, account focus.Value, want map[string]uint32) {
	t.Helper()
	focus.AssertSignersEqual(fatal(t), meta, account, want)
}

// ThresholdsEqual is focus.AssertThresholdsEqual, stopping the test on
// failure.
func ThresholdsEqual(t focus.TB, meta, account, want focus.Value) {
	t.Helper()
	focus.AssertThresholdsEqual(fatal(t), meta, account, want)
}

// FailedOperations is focus.AssertFailedOperations, stopping the test on
// failure.
func FailedOperations(t focus.TB, result focus.Value, want ...int) {
	t.Helper()
	focus.AssertFailedOperations(fatal(t), result, want...)
}

// FeeBump is focus.AssertFeeBump, stopping the test on failure.
func FeeBump(t focus.TB, envelope focus.Value, want bool) {
	t.Helper()
	focus.AssertFeeBump(fatal(t), envelope, want)
}

// NoEvents is focus.AssertNoEvents, stopping the test on failure.
func NoEvents(t focus.TB, meta focus.Value) {
	t.Helper()
	focus.AssertNoEvents(fatal(t), meta)
}

// OperationType is focus.AssertOperationType, stopping the test on failure.
func OperationType(t focus.TB, envelope focus.Value, index int, want string) {
	t.Helper()
	focus.AssertOperationType(fatal(t), envelope, index, want)
}
