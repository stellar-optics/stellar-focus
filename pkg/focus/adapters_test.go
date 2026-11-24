package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// gomockMatcher mirrors the interface github.com/golang/mock and
// go.uber.org/mock both declare for matchers. focus must satisfy it
// structurally; importing either module to check it directly would fail this
// package's dependency tests, which is the whole point of the adapters being
// structural.
type gomockMatcher interface {
	Matches(x any) error
	String() string
}

// adaptersResultFixtures returns one successful and one failed transaction
// result, the pair the result assertions are written against elsewhere in
// this suite.
func adaptersResultFixtures(t *testing.T) (success, failed string) {
	t.Helper()
	success = buildResult(t, xdr.TransactionResultCodeTxSuccess,
		paymentResult(xdr.PaymentResultCodePaymentSuccess))
	failed = buildResult(t, xdr.TransactionResultCodeTxFailed,
		paymentResult(xdr.PaymentResultCodePaymentSuccess),
		paymentResult(xdr.PaymentResultCodePaymentUnderfunded))
	return success, failed
}

// otherEnvelope is a real envelope differing from buildEnvelope's in source
// account, fee and sequence number, so adapters have a second value to fail
// against.
func otherEnvelope(t *testing.T) string {
	t.Helper()
	env := envelopeValue(t, paymentOp(t, 5))
	env.V1.Tx.SourceAccount = mustMuxed(t, testDest)
	env.V1.Tx.Fee = 999
	env.V1.Tx.SeqNum = 99
	return mustMarshal(t, env)
}

// TestCheckReturnsNilOnPass pins the passing half of the contract: no error,
// no message, nothing for an assertion library to print.
func TestCheckReturnsNilOnPass(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 1_000_000_000))
	if err := focus.Check(func(t focus.TB) bool {
		return focus.AssertFee(t, env, 100)
	}); err != nil {
		t.Fatalf("Check returned %v for a passing assertion, want nil", err)
	}
}

// TestCheckReportsTheSameTextAsTheAssertion is the equivalence that makes
// the adapter worth using: the error handed to testify is byte-for-byte the
// failure focus itself would report, verified with the recording TB, and it
// carries the descriptive diff rather than a bare pass/fail.
func TestCheckReportsTheSameTextAsTheAssertion(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 1_000_000_000))

	tb := faketb.New()
	focus.AssertFee(tb, env, 999)
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("direct assertion reported %d messages: %v", len(tb.Errors), err)
	}

	got := focus.Check(func(t focus.TB) bool {
		return focus.AssertFee(t, env, 999)
	})
	if got == nil {
		t.Fatal("Check returned nil for a failing assertion")
	}
	if got.Error() != msg {
		t.Errorf("adapter error differs from the assertion's own failure\n--- adapter ---\n%s\n--- assertion ---\n%s",
			got, msg)
	}
	for _, want := range []string{"AssertFee", "want 999"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("adapter error does not carry the diff %q:\n%s", want, got)
		}
	}
}

// TestCheckRejectsNilAssertion covers the programming mistake that would
// otherwise panic inside someone else's test run.
func TestCheckRejectsNilAssertion(t *testing.T) {
	t.Parallel()

	err := focus.Check(nil)
	if err == nil {
		t.Fatal("Check(nil) returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "non-nil") {
		t.Errorf("Check(nil) = %q, want it to name the missing assertion", err)
	}
}

// TestCheckConvertsPanicIntoError keeps the never-panic rule true for the
// adapter path too: a panicking assertion becomes an ordinary error carrying
// the bug-report prompt.
func TestCheckConvertsPanicIntoError(t *testing.T) {
	t.Parallel()

	err := focus.Check(func(focus.TB) bool { panic("kaboom") })
	if err == nil {
		t.Fatal("Check returned nil for a panicking assertion")
	}
	for _, want := range []string{
		"internal error while inspecting the value",
		"kaboom",
		"bug in stellar-focus",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("panic error does not contain %q:\n%s", want, err)
		}
	}
}

// TestCheckReportsSilentFailure guards the false-but-unexplained case: an
// assertion that fails without saying why must not be treated as passing.
func TestCheckReportsSilentFailure(t *testing.T) {
	t.Parallel()

	err := focus.Check(func(focus.TB) bool { return false })
	if err == nil {
		t.Fatal("Check returned nil for a failed assertion that reported nothing")
	}
	if !strings.Contains(err.Error(), "without reporting") {
		t.Errorf("Check = %q, want it to say the assertion reported nothing", err)
	}
}

// TestCheckCapturesFatalfSemantics covers the other half of the TB
// contract: an assertion reported with Fatalf (as require-style helpers do)
// surfaces its message as an error instead of aborting the caller's
// goroutine.
func TestCheckCapturesFatalfSemantics(t *testing.T) {
	t.Parallel()

	err := focus.Check(func(t focus.TB) bool {
		t.Fatalf("fatal from a custom assertion: %d", 7)
		return true
	})
	if err == nil {
		t.Fatal("Check returned nil though the assertion reported a fatal")
	}
	if !strings.Contains(err.Error(), "fatal from a custom assertion: 7") {
		t.Errorf("Check = %q, want the fatal message", err)
	}
}

// TestErrAdaptersAgreeWithTheDirectAssertions checks every Err* wrapper in
// both directions: nil exactly when the direct assertion passes, and — when
// it fails — an error byte-identical to what internal/faketb captured from
// the direct call, so no diff detail is lost in the conversion.
func TestErrAdaptersAgreeWithTheDirectAssertions(t *testing.T) {
	t.Parallel()

	success, failed := adaptersResultFixtures(t)

	tests := []struct {
		name    string
		err     error
		run     func(focus.TB)
		wantErr bool
	}{
		{"succeeded on a success", focus.ErrSucceeded(success),
			func(tb focus.TB) { focus.AssertSucceeded(tb, success) }, false},
		{"succeeded on a failure", focus.ErrSucceeded(failed),
			func(tb focus.TB) { focus.AssertSucceeded(tb, failed) }, true},
		{"failed on a failure", focus.ErrFailed(failed),
			func(tb focus.TB) { focus.AssertFailed(tb, failed) }, false},
		{"failed on a success", focus.ErrFailed(success),
			func(tb focus.TB) { focus.AssertFailed(tb, success) }, true},
		{"result code matches", focus.ErrResultCode(failed, "tx_failed"),
			func(tb focus.TB) { focus.AssertResultCode(tb, failed, "tx_failed") }, false},
		{"result code differs", focus.ErrResultCode(failed, "tx_bad_seq"),
			func(tb focus.TB) { focus.AssertResultCode(tb, failed, "tx_bad_seq") }, true},
		{"operation succeeded", focus.ErrOperationSucceeded(failed, 0),
			func(tb focus.TB) { focus.AssertOperationSucceeded(tb, failed, 0) }, false},
		{"operation succeeded on a failed operation", focus.ErrOperationSucceeded(failed, 1),
			func(tb focus.TB) { focus.AssertOperationSucceeded(tb, failed, 1) }, true},
		{"operation failed with the right code", focus.ErrOperationFailed(failed, 1, "payment_underfunded"),
			func(tb focus.TB) { focus.AssertOperationFailed(tb, failed, 1, "payment_underfunded") }, false},
		{"operation failed with the wrong code", focus.ErrOperationFailed(failed, 1, "payment_success"),
			func(tb focus.TB) { focus.AssertOperationFailed(tb, failed, 1, "payment_success") }, true},
		{"failed operation indexes match", focus.ErrFailedOperations(failed, 1),
			func(tb focus.TB) { focus.AssertFailedOperations(tb, failed, 1) }, false},
		{"failed operation indexes differ", focus.ErrFailedOperations(failed, 0),
			func(tb focus.TB) { focus.AssertFailedOperations(tb, failed, 0) }, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			tc.run(tb)

			if !tc.wantErr {
				if tc.err != nil {
					t.Errorf("adapter returned %v, want nil for a passing assertion", tc.err)
				}
				if tb.Failed() {
					t.Errorf("the direct assertion was supposed to pass but reported:\n%s", tb.Output())
				}
				return
			}

			if tc.err == nil {
				t.Fatal("adapter returned nil, want the assertion's failure")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("the direct assertion should have reported exactly once: %v", err)
			}
			if tc.err.Error() != msg {
				t.Errorf("adapter error differs from the assertion's own failure\n--- adapter ---\n%s\n--- assertion ---\n%s",
					tc.err, msg)
			}
		})
	}
}

// TestErrAdaptersRejectHostileInput feeds the same deliberate garbage the
// panic tests use through every Err* wrapper: each must fail with a message
// naming the assertion, and none may panic.
func TestErrAdaptersRejectHostileInput(t *testing.T) {
	t.Parallel()

	hostile := []focus.Value{
		nil, "", "!!!", "AA==", []byte{0x00}, 42, struct{}{},
	}

	for _, v := range hostile {
		cases := []struct {
			name string
			err  error
		}{
			{"ErrSucceeded", focus.ErrSucceeded(v)},
			{"ErrFailed", focus.ErrFailed(v)},
			{"ErrResultCode", focus.ErrResultCode(v, "tx_failed")},
			{"ErrOperationSucceeded", focus.ErrOperationSucceeded(v, 0)},
			{"ErrOperationFailed", focus.ErrOperationFailed(v, 0, "")},
			{"ErrFailedOperations", focus.ErrFailedOperations(v, 0)},
		}
		for _, tc := range cases {
			if tc.err == nil {
				t.Errorf("%s accepted hostile input %T without a failure", tc.name, v)
				continue
			}
			if !strings.Contains(tc.err.Error(), "Assert") {
				t.Errorf("%s on %T reported %q, want the assertion name in the message",
					tc.name, v, tc.err)
			}
		}
	}
}

// TestMatchersSatisfyGomocksInterface is the compile-time half of the
// gomock contract: every matcher focus hands out must satisfy the method set
// gomock's generated mocks require, and must describe itself.
func TestMatchersSatisfyGomocksInterface(t *testing.T) {
	t.Parallel()

	matchers := []gomockMatcher{
		focus.MatchSucceeded(),
		focus.MatchFailed(),
		focus.MatchResultCode("tx_failed"),
		focus.MatchOperationResultCode(0, "payment_success"),
		focus.MatchSourceAccount("GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"),
		focus.MatchFee(100),
		focus.MatchSequence(42),
		focus.MatchMemo("text: hello"),
		focus.MatchOperationCount(1),
		focus.MatchOperationTypes("payment"),
		focus.MatchSignatureCount(1),
		focus.MatchFeeBump(true),
		focus.MatchFeeBump(false),
		focus.Match("a custom expectation", func(focus.TB, focus.Value) bool { return true }),
	}

	for _, m := range matchers {
		if m.String() == "" {
			t.Errorf("%T describes itself as an empty string; gomock would print nothing", m)
		}
	}
}

// TestMatchersMatchTheRightValues checks each built-in matcher against a
// value it must accept and the same value it must reject, plus the exact
// description gomock prints on a miss.
func TestMatchersMatchTheRightValues(t *testing.T) {
	t.Parallel()

	success, failed := adaptersResultFixtures(t)
	env := buildEnvelope(t, paymentOp(t, 1_000_000_000))
	feeBump := mustMarshal(t, feeBumpEnvelopeValue(t, envelopeValue(t, paymentOp(t, 100)), nil, nil))

	tests := []struct {
		name         string
		good         focus.Matcher
		bad          focus.Matcher
		value        focus.Value
		wantString   string
		wantBadError string
	}{
		{
			"succeeded", focus.MatchSucceeded(), focus.MatchFailed(), success,
			"a successful transaction", "AssertFailed",
		},
		{
			"failed", focus.MatchFailed(), focus.MatchSucceeded(), failed,
			"a failed transaction", "AssertSucceeded",
		},
		{
			"result code", focus.MatchResultCode("tx_failed"), focus.MatchResultCode("tx_bad_seq"), failed,
			`a transaction result coded "tx_failed"`, `want "tx_bad_seq"`,
		},
		{
			"operation result code",
			focus.MatchOperationResultCode(1, "payment_underfunded"),
			focus.MatchOperationResultCode(1, "payment_success"), failed,
			`operation 1 result coded "payment_underfunded"`, "AssertOperationResultCode",
		},
		{
			"source account",
			focus.MatchSourceAccount(testSource), focus.MatchSourceAccount(testDest), env,
			"a transaction with source account " + testSource, "AssertSourceAccount",
		},
		{
			"fee", focus.MatchFee(100), focus.MatchFee(999), env,
			"a transaction bidding a fee of 100 stroops", "AssertFee",
		},
		{
			"sequence", focus.MatchSequence(12345), focus.MatchSequence(12346), env,
			"a transaction with sequence number 12345", "AssertSequence",
		},
		{
			"memo", focus.MatchMemo(""), focus.MatchMemo("text: hi"), env,
			`a transaction with memo ""`, "AssertMemo",
		},
		{
			"operation count", focus.MatchOperationCount(1), focus.MatchOperationCount(2), env,
			"a transaction with 1 operation(s)", "AssertOperationCount",
		},
		{
			"operation types",
			focus.MatchOperationTypes("payment"), focus.MatchOperationTypes("manage_data"), env,
			`operations ["payment"]`, "AssertOperationTypes",
		},
		{
			"signature count", focus.MatchSignatureCount(0), focus.MatchSignatureCount(1), env,
			"a transaction with 0 signature(s)", "AssertSignatureCount",
		},
		{
			"not fee-bumped", focus.MatchFeeBump(false), focus.MatchFeeBump(true), env,
			"a transaction that is not fee-bumped", "AssertFeeBump",
		},
		{
			"fee-bumped", focus.MatchFeeBump(true), focus.MatchFeeBump(false), feeBump,
			"a fee-bump transaction", "AssertFeeBump",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.good.String(); got != tc.wantString {
				t.Errorf("String() = %q, want %q", got, tc.wantString)
			}
			if err := tc.good.Matches(tc.value); err != nil {
				t.Errorf("matching value rejected: %v", err)
			}

			err := tc.bad.Matches(tc.value)
			if err == nil {
				t.Fatal("non-matching value accepted")
			}
			if !strings.Contains(err.Error(), tc.wantBadError) {
				t.Errorf("miss error does not contain %q:\n%s", tc.wantBadError, err)
			}
		})
	}
}

// TestMatcherFailuresCarryTheSameDiff checks the matcher path against
// faketb: a miss returns byte-for-byte the failure the direct focus
// assertion reports, so mock failures print the same descriptive diff.
func TestMatcherFailuresCarryTheSameDiff(t *testing.T) {
	t.Parallel()

	_, failed := adaptersResultFixtures(t)
	env := buildEnvelope(t, paymentOp(t, 1_000_000_000))

	tests := []struct {
		name  string
		m     focus.Matcher
		value focus.Value
		run   func(focus.TB)
	}{
		{
			"result code", focus.MatchResultCode("tx_bad_seq"), failed,
			func(tb focus.TB) { focus.AssertResultCode(tb, failed, "tx_bad_seq") },
		},
		{
			"sequence", focus.MatchSequence(12346), env,
			func(tb focus.TB) { focus.AssertSequence(tb, env, 12346) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			tc.run(tb)
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("the direct assertion should have reported exactly once: %v", err)
			}

			got := tc.m.Matches(tc.value)
			if got == nil {
				t.Fatal("Matches returned nil for a non-matching value")
			}
			if got.Error() != msg {
				t.Errorf("matcher error differs from the assertion's own failure\n--- matcher ---\n%s\n--- assertion ---\n%s",
					got, msg)
			}
		})
	}
}

// TestMatchAdaptsAnyAssertion covers the escape hatch: a matcher built by
// hand around an arbitrary focus assertion, which is how a project pins
// fields the built-ins do not cover.
func TestMatchAdaptsAnyAssertion(t *testing.T) {
	t.Parallel()

	match := buildEnvelopeSeq(t, 42, paymentOp(t, 1_000_000_000))
	other := otherEnvelope(t)

	m := focus.Match("a transaction with sequence 42", func(t focus.TB, v focus.Value) bool {
		return focus.AssertSequence(t, v, 42)
	})

	if want := "a transaction with sequence 42"; m.String() != want {
		t.Errorf("String() = %q, want %q", m.String(), want)
	}
	if err := m.Matches(match); err != nil {
		t.Errorf("matching envelope rejected: %v", err)
	}

	err := m.Matches(other)
	if err == nil {
		t.Fatal("non-matching envelope accepted")
	}
	for _, want := range []string{"AssertSequence", "42"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("miss error does not contain %q:\n%s", want, err)
		}
	}
}

// TestMatchWithoutAssertion covers a hand-built matcher with no assertion
// behind it: a miss rather than a panic, named as such.
func TestMatchWithoutAssertion(t *testing.T) {
	t.Parallel()

	m := focus.Match("an expectation with no assertion", nil)
	err := m.Matches(nil)
	if err == nil {
		t.Fatal("Matches returned nil for a matcher with no assertion")
	}
	if !strings.Contains(err.Error(), "carries no assertion") {
		t.Errorf("Matches = %q, want it to name the missing assertion", err)
	}
}

// TestMatchersNeverPanic feeds hostile input through every built-in matcher
// and one hand-built matcher that panics: none may panic, all must report.
func TestMatchersNeverPanic(t *testing.T) {
	t.Parallel()

	hostile := []focus.Value{
		nil, "", "!!!", []byte{}, 42, struct{}{},
	}

	matchers := []struct {
		name string
		m    focus.Matcher
	}{
		{"MatchSucceeded", focus.MatchSucceeded()},
		{"MatchFailed", focus.MatchFailed()},
		{"MatchResultCode", focus.MatchResultCode("tx_failed")},
		{"MatchOperationResultCode", focus.MatchOperationResultCode(0, "payment_success")},
		{"MatchSourceAccount", focus.MatchSourceAccount(testSource)},
		{"MatchFee", focus.MatchFee(100)},
		{"MatchSequence", focus.MatchSequence(1)},
		{"MatchMemo", focus.MatchMemo("")},
		{"MatchOperationCount", focus.MatchOperationCount(1)},
		{"MatchOperationTypes", focus.MatchOperationTypes("payment")},
		{"MatchSignatureCount", focus.MatchSignatureCount(1)},
		{"MatchFeeBump", focus.MatchFeeBump(false)},
	}

	for _, tc := range matchers {
		for _, v := range hostile {
			err := tc.m.Matches(v)
			if err == nil {
				t.Errorf("%s accepted hostile input %T without a failure", tc.name, v)
				continue
			}
			if !strings.Contains(err.Error(), "Assert") {
				t.Errorf("%s on %T reported %q, want the assertion name in the message",
					tc.name, v, err)
			}
		}
	}

	panicking := focus.Match("an assertion that panics", func(focus.TB, focus.Value) bool {
		panic("kaboom")
	})
	err := panicking.Matches(nil)
	if err == nil {
		t.Fatal("Matches returned nil for a panicking assertion")
	}
	for _, want := range []string{
		"internal error while inspecting the value",
		"kaboom",
		"bug in stellar-focus",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("panic error does not contain %q:\n%s", want, err)
		}
	}
}
