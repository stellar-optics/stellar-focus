// Package focus provides XDR-aware assertions for Go tests.
//
// This file adapts those assertions to the two shapes other test ecosystems
// already know how to report: an ordinary error, which testify's assert and
// require (and gocheck's Assert) take directly, and a matcher whose method
// set matches gomock.Matcher, which generated mocks accept as an argument
// expectation.
//
// Neither library is imported. A test helper that drags an assertion or
// mocking framework into a project's dependency graph is a helper people
// resent, and this package's dependency tests — the compiled-graph check and
// the AST import check alike — ban exactly those imports. The adapters are
// structural instead: an error carries the same descriptive diff the direct
// assertion would have printed, and Matcher declares the two methods gomock
// requires, so the frameworks pick focus up without focus picking them up.
//
// Error adapters, for testify (or gocheck):
//
//	assert.NoError(t, focus.ErrResultCode(result, "tx_bad_seq"))
//	require.NoError(t, focus.Check(func(t focus.TB) bool {
//		return focus.AssertSequence(t, envelope, 42)
//	}))
//
// Matcher adapters, for gomock (github.com/golang/mock or go.uber.org/mock):
//
//	mock.EXPECT().Submit(focus.MatchSequence(42), focus.MatchSucceeded())
package focus

import (
	"errors"
	"fmt"
	"strings"
)

// Check runs a focus assertion and hands its failure back as an error.
//
// The result is nil when the assertion passed. When it failed, the error
// carries the exact text the assertion reported — the field path, the
// enriched values, the whole diff — so an assertion library that prints the
// error shows the message a direct focus failure would have shown:
//
//	assert.NoError(t, focus.Check(func(t focus.TB) bool {
//		return focus.AssertSequence(t, envelope, 42)
//	}))
//
// Any focus assertion fits this shape; the Err* functions below are
// shorthand for the result assertions, which are the ones most often checked
// from mock-heavy testify code.
//
// Check never panics. A panic inside the assertion is recovered by SafeCall
// and returned as an error carrying the usual bug-report prompt, upholding
// this package's rule that an assertion may not destroy a test run.
func Check(assertion func(TB) bool) error {
	if assertion == nil {
		return errors.New("focus: Check requires a non-nil assertion function")
	}
	rec := &adapterTB{}
	passed := false
	SafeCall(rec, "Check", func() { passed = assertion(rec) })
	return rec.toError(passed)
}

// ErrSucceeded adapts AssertSucceeded into an error, so a testify suite can
// write assert.NoError(t, focus.ErrSucceeded(result)) and still get the
// descriptive diff when the transaction failed.
func ErrSucceeded(result Value) error {
	return Check(func(t TB) bool { return AssertSucceeded(t, result) })
}

// ErrFailed adapts AssertFailed into an error.
func ErrFailed(result Value) error {
	return Check(func(t TB) bool { return AssertFailed(t, result) })
}

// ErrResultCode adapts AssertResultCode into an error, so a testify suite
// can write assert.NoError(t, focus.ErrResultCode(result, "tx_bad_seq")).
func ErrResultCode(result Value, want string) error {
	return Check(func(t TB) bool { return AssertResultCode(t, result, want) })
}

// ErrOperationSucceeded adapts AssertOperationSucceeded into an error.
func ErrOperationSucceeded(result Value, index int) error {
	return Check(func(t TB) bool { return AssertOperationSucceeded(t, result, index) })
}

// ErrOperationFailed adapts AssertOperationFailed into an error.
func ErrOperationFailed(result Value, index int, want string) error {
	return Check(func(t TB) bool { return AssertOperationFailed(t, result, index, want) })
}

// ErrFailedOperations adapts AssertFailedOperations into an error.
func ErrFailedOperations(result Value, want ...int) error {
	return Check(func(t TB) bool { return AssertFailedOperations(t, result, want...) })
}

// Matcher is the interface gomock's matchers implement: both
// github.com/golang/mock and go.uber.org/mock declare Matches plus String,
// and a generated mock accepts any value with that method set.
//
// focus declares the shape rather than importing either module, so projects
// generated with either one can use the matchers below — or their own, built
// with Match — without focus adding a mocking framework to anyone's
// dependency graph. The interface is structurally identical to
// gomock.Matcher; a compile-time check against the real interface belongs in
// the consuming project, since importing gomock here is exactly what the
// dependency tests forbid.
type Matcher interface {
	// Matches returns nil when x satisfies the matcher, and otherwise an
	// error carrying the same descriptive diff the underlying focus
	// assertion would have reported.
	Matches(x any) error
	// String describes the matcher. gomock prints it when a received call
	// does not match an expectation, so it names the expectation rather
	// than the failure.
	String() string
}

// match implements Matcher over an ordinary focus assertion.
type match struct {
	description string
	assertion   func(TB, Value) bool
}

// String implements Matcher.
func (m *match) String() string { return m.description }

// Matches implements Matcher.
func (m *match) Matches(x any) error {
	if m.assertion == nil {
		return fmt.Errorf("focus: matcher %q carries no assertion", m.description)
	}
	rec := &adapterTB{}
	passed := false
	SafeCall(rec, fmt.Sprintf("matcher %q", m.description), func() {
		passed = m.assertion(rec, x)
	})
	return rec.toError(passed)
}

// Match adapts any focus assertion into a gomock-style Matcher.
//
// The assertion receives the value the mock was called with, so a generated
// mock can pin down the XDR an argument actually carried:
//
//	mock.EXPECT().Submit(gomock.Any(), focus.Match(
//	    "a transaction with sequence 42",
//	    func(t focus.TB, v focus.Value) bool {
//	        return focus.AssertSequence(t, v, 42)
//	    }))
//
// description is what gomock prints for an unmatched expectation; it should
// read as a noun phrase describing the wanted value.
func Match(description string, assertion func(TB, Value) bool) Matcher {
	return &match{description: description, assertion: assertion}
}

// MatchSucceeded is a matcher for a transaction result that reports success.
func MatchSucceeded() Matcher {
	return Match("a successful transaction", AssertSucceeded)
}

// MatchFailed is a matcher for a transaction result that reports failure.
func MatchFailed() Matcher {
	return Match("a failed transaction", AssertFailed)
}

// MatchResultCode is a matcher for a transaction-level result code such as
// "tx_bad_seq".
func MatchResultCode(want string) Matcher {
	return Match(fmt.Sprintf("a transaction result coded %q", want), func(t TB, v Value) bool {
		return AssertResultCode(t, v, want)
	})
}

// MatchOperationResultCode is a matcher for one operation's result code,
// such as "payment_underfunded".
func MatchOperationResultCode(index int, want string) Matcher {
	return Match(fmt.Sprintf("operation %d result coded %q", index, want),
		func(t TB, v Value) bool {
			return AssertOperationResultCode(t, v, index, want)
		})
}

// MatchSourceAccount is a matcher for a transaction's source account.
func MatchSourceAccount(want string) Matcher {
	return Match(fmt.Sprintf("a transaction with source account %s", want), func(t TB, v Value) bool {
		return AssertSourceAccount(t, v, want)
	})
}

// MatchFee is a matcher for a transaction's fee bid, in stroops.
func MatchFee(want int64) Matcher {
	return Match(fmt.Sprintf("a transaction bidding a fee of %d stroops", want), func(t TB, v Value) bool {
		return AssertFee(t, v, want)
	})
}

// MatchSequence is a matcher for a transaction's sequence number.
func MatchSequence(want int64) Matcher {
	return Match(fmt.Sprintf("a transaction with sequence number %d", want), func(t TB, v Value) bool {
		return AssertSequence(t, v, want)
	})
}

// MatchMemo is a matcher for a transaction's memo, written in the same form
// lens prints: "text: hello", "id: 42", "hash: <hex>", or "" for no memo.
func MatchMemo(want string) Matcher {
	return Match(fmt.Sprintf("a transaction with memo %q", want), func(t TB, v Value) bool {
		return AssertMemo(t, v, want)
	})
}

// MatchOperationCount is a matcher for a transaction's operation count.
func MatchOperationCount(want int) Matcher {
	return Match(fmt.Sprintf("a transaction with %d operation(s)", want), func(t TB, v Value) bool {
		return AssertOperationCount(t, v, want)
	})
}

// MatchOperationTypes is a matcher for the exact ordered operation types,
// named as Horizon names them ("payment", "change_trust", ...).
func MatchOperationTypes(want ...string) Matcher {
	quoted := make([]string, 0, len(want))
	for _, w := range want {
		quoted = append(quoted, fmt.Sprintf("%q", w))
	}
	return Match(fmt.Sprintf("operations [%s]", strings.Join(quoted, ", ")),
		func(t TB, v Value) bool {
			return AssertOperationTypes(t, v, want...)
		})
}

// MatchSignatureCount is a matcher for the number of signatures an envelope
// carries.
func MatchSignatureCount(want int) Matcher {
	return Match(fmt.Sprintf("a transaction with %d signature(s)", want), func(t TB, v Value) bool {
		return AssertSignatureCount(t, v, want)
	})
}

// MatchFeeBump is a matcher for whether the envelope is fee-bump wrapped.
func MatchFeeBump(want bool) Matcher {
	if want {
		return Match("a fee-bump transaction", func(t TB, v Value) bool {
			return AssertFeeBump(t, v, true)
		})
	}
	return Match("a transaction that is not fee-bumped", func(t TB, v Value) bool {
		return AssertFeeBump(t, v, false)
	})
}

// adapterTB records what an assertion reported instead of failing a test, so
// an adapter can hand the message to another assertion library.
//
// It is the same seam internal/faketb provides to this package's tests,
// kept private and standard-library-only here so the library's error
// adapters add nothing to a downstream module's dependency graph.
type adapterTB struct {
	messages []string
}

// Helper implements TB. An adapter has no test line of its own to point at;
// this exists so the TB contract is complete.
func (a *adapterTB) Helper() {}

// Errorf implements TB, recording the formatted message.
func (a *adapterTB) Errorf(format string, args ...any) {
	a.messages = append(a.messages, fmt.Sprintf(format, args...))
}

// Fatalf implements TB, recording exactly like Errorf: an adapter runs
// inside the calling test's goroutine and must never abort it.
func (a *adapterTB) Fatalf(format string, args ...any) {
	a.messages = append(a.messages, fmt.Sprintf(format, args...))
}

// Name implements TB, identifying the recorder in any message that names
// the test.
func (a *adapterTB) Name() string { return "focus adapter" }

// toError renders what the recorder captured as an error. It is nil only
// when the assertion passed and reported nothing, so a false result that
// produced no message is surfaced rather than silently treated as success,
// and a passing assertion that still reported something is not swallowed.
func (a *adapterTB) toError(passed bool) error {
	switch {
	case len(a.messages) > 0:
		return errors.New(strings.Join(a.messages, "\n"))
	case passed:
		return nil
	default:
		return errors.New("focus: the assertion failed without reporting why")
	}
}
