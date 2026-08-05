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

// Memo is focus.AssertMemo, stopping the test on failure.
func Memo(t focus.TB, envelope focus.Value, want string) {
	t.Helper()
	focus.AssertMemo(fatal(t), envelope, want)
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

// SignatureCount is focus.AssertSignatureCount, stopping the test on failure.
func SignatureCount(t focus.TB, envelope focus.Value, want int) {
	t.Helper()
	focus.AssertSignatureCount(fatal(t), envelope, want)
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
