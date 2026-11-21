package focus

import (
	"fmt"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// AssertOperationResultCode checks the result code of one operation by short
// name, whether the operation succeeded or failed.
//
// The expected code is the short form Horizon returns — "payment_success",
// "payment_underfunded", "op_bad_auth" — not the generated XDR constant.
//
// AssertOperationFailed is the assertion for "this operation failed"; this one
// is for "this operation ended with exactly this code", which is what a test
// wants when it pairs an expected success code with an expected failure. The
// expected code is also validated: a typo or an XDR constant name in want is
// reported as such rather than as a puzzling mismatch.
func AssertOperationResultCode(t TB, result Value, index int, want string) bool {
	t.Helper()

	op, _, ok := operationResult(t, "AssertOperationResultCode", result, index)
	if !ok {
		return false
	}
	if op.Result == nil {
		t.Errorf("AssertOperationResultCode: operation %d has no result", index)
		return false
	}
	if op.Result.Code == want {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "AssertOperationResultCode: operation %d%s result code is %q, want %q\n\n",
		index, opLabel(op.Type), op.Result.Code, want)
	fmt.Fprintf(&b, "  got:  %s\n", describeReason(*op.Result))
	fmt.Fprintf(&b, "  want: %s\n", describeWantedCode(want))
	t.Errorf("%s", b.String())
	return false
}

// describeWantedCode renders an expected short code, naming the most likely
// mistake when lens does not know it.
//
// An unknown code is almost always a typo or a generated XDR constant. Both
// deserve a message better than a bare mismatch: without one, the failure
// reads as "the code this transaction produced is wrong" when in fact the
// expectation could never have matched anything.
func describeWantedCode(want string) string {
	if reason, ok := lens.ReasonByCode(want); ok {
		return describeReason(reason)
	}

	// A generated constant derives to its short code via lens. When that
	// derivation is a code lens knows, the caller passed the wrong form.
	if short := lens.ExplainCode(want).Code; short != "" && short != want {
		if reason, ok := lens.ReasonByCode(short); ok {
			return fmt.Sprintf(
				"%q is not a short result code — it looks like the XDR constant %q; use %q instead",
				want, reason.Constant, reason.Code)
		}
	}

	return fmt.Sprintf("%q (not a known result code; codes are the short forms Horizon returns, "+
		"such as \"tx_bad_seq\" or \"payment_underfunded\")", want)
}

// AssertFeeBumpInnerResultCode isolates and checks the inner failure code of a fee-bump transaction.
//
// When fee-bump transactions fail, the outer result is typically tx_fee_bump_inner_failed,
// while the actionable failure code resides inside the inner transaction result.
//
// Both outer and inner codes are cleanly reported, and the failure output includes
// lens plain-English explanations and remediations.
func AssertFeeBumpInnerResultCode(t TestingT, resultInput any, wantInnerCode string, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertFeeBumpInnerResultCode"
	_ = newConfig(opts...)

	s, ok := summarize(t, assertion, resultInput, "TransactionResult")
	if !ok {
		return false
	}
	if s.Outcome == nil {
		t.Errorf("%s: transaction result carries no outcome", assertion)
		return false
	}
	if s.Outcome.InnerReason == nil {
		t.Errorf("%s: transaction result (outer code %q) is not a fee-bump transaction with an inner result",
			assertion, s.Outcome.Reason.Code)
		return false
	}

	gotInnerCode := s.Outcome.InnerReason.Code
	if gotInnerCode == wantInnerCode {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: inner transaction result code is %q, want %q\n\n",
		assertion, gotInnerCode, wantInnerCode)
	fmt.Fprintf(&b, "  outer:      %s\n", describeReason(s.Outcome.Reason))
	fmt.Fprintf(&b, "  got inner:  %s\n", describeReason(*s.Outcome.InnerReason))
	if wantReason, ok := lens.ReasonByCode(wantInnerCode); ok {
		fmt.Fprintf(&b, "  want inner: %s\n", describeReason(wantReason))
	} else {
		fmt.Fprintf(&b, "  want inner: %s\n", describeWantedCode(wantInnerCode))
	}
	t.Errorf("%s", b.String())
	return false
}
