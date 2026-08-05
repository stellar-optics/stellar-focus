package focus

import (
	"fmt"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Result assertions take the short codes lens produces — "tx_bad_seq",
// "payment_underfunded" — rather than the generated XDR constants. The short
// form is what Horizon returns and what a developer recognises, and lens
// already maps every one of the protocol's 208 result codes to it along with
// a plain-English explanation, which the failure messages here reuse.

// AssertSucceeded checks that a transaction result reports success.
func AssertSucceeded(t TB, result Value) bool {
	t.Helper()
	s, ok := summarize(t, "AssertSucceeded", result, "TransactionResult")
	if !ok {
		return false
	}
	if s.Outcome == nil {
		t.Errorf("AssertSucceeded: value carries no outcome")
		return false
	}
	if !s.Outcome.Success {
		t.Errorf("AssertSucceeded: transaction failed\n%s", indent(explainOutcome(s), "  "))
		return false
	}
	return true
}

// AssertFailed checks that a transaction result reports failure.
func AssertFailed(t TB, result Value) bool {
	t.Helper()
	s, ok := summarize(t, "AssertFailed", result, "TransactionResult")
	if !ok {
		return false
	}
	if s.Outcome == nil {
		t.Errorf("AssertFailed: value carries no outcome")
		return false
	}
	if s.Outcome.Success {
		t.Errorf("AssertFailed: transaction succeeded, want it to have failed")
		return false
	}
	return true
}

// AssertResultCode checks the transaction-level result code, such as
// "tx_bad_seq" or "tx_failed".
//
// For a fee-bump transaction the inner code is also accepted, since the inner
// result is usually the one a test means.
func AssertResultCode(t TB, result Value, want string) bool {
	t.Helper()
	s, ok := summarize(t, "AssertResultCode", result, "TransactionResult")
	if !ok {
		return false
	}
	if s.Outcome == nil {
		t.Errorf("AssertResultCode: value carries no outcome")
		return false
	}

	got := s.Outcome.Reason.Code
	if got == want {
		return true
	}
	if s.Outcome.InnerReason != nil && s.Outcome.InnerReason.Code == want {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "AssertResultCode: result code is %q, want %q\n\n", got, want)
	fmt.Fprintf(&b, "  got:  %s\n", describeReason(s.Outcome.Reason))
	if s.Outcome.InnerReason != nil {
		fmt.Fprintf(&b, "  inner: %s\n", describeReason(*s.Outcome.InnerReason))
	}
	fmt.Fprintf(&b, "  want: %s\n", want)
	t.Errorf("%s", b.String())
	return false
}

// AssertOperationSucceeded checks that one operation succeeded.
func AssertOperationSucceeded(t TB, result Value, index int) bool {
	t.Helper()
	op, s, ok := operationResult(t, "AssertOperationSucceeded", result, index)
	if !ok {
		return false
	}
	if op.Result == nil {
		t.Errorf("AssertOperationSucceeded: operation %d has no result", index)
		return false
	}
	if !op.Result.Success {
		t.Errorf("AssertOperationSucceeded: operation %d%s failed with %q\n\n  %s\n%s",
			index, opLabel(op.Type), op.Result.Code, describeReason(*op.Result), indent(explainOutcome(s), "  "))
		return false
	}
	return true
}

// AssertOperationFailed checks that one operation failed with the expected
// code, such as "payment_underfunded".
//
// Passing an empty want asserts only that the operation failed.
func AssertOperationFailed(t TB, result Value, index int, want string) bool {
	t.Helper()
	op, _, ok := operationResult(t, "AssertOperationFailed", result, index)
	if !ok {
		return false
	}
	if op.Result == nil {
		t.Errorf("AssertOperationFailed: operation %d has no result", index)
		return false
	}
	if op.Result.Success {
		t.Errorf("AssertOperationFailed: operation %d%s succeeded, want it to have failed",
			index, opLabel(op.Type))
		return false
	}
	if want == "" || op.Result.Code == want {
		return true
	}

	t.Errorf("AssertOperationFailed: operation %d%s failed with %q, want %q\n\n"+
		"  got:  %s\n  want: %s",
		index, opLabel(op.Type), op.Result.Code, want,
		describeReason(*op.Result), want)
	return false
}

// AssertFailedOperations checks exactly which operation indexes failed, which
// is the assertion to reach for when a transaction has several operations and
// only some are expected to fail.
func AssertFailedOperations(t TB, result Value, want ...int) bool {
	t.Helper()
	s, ok := summarize(t, "AssertFailedOperations", result, "TransactionResult")
	if !ok {
		return false
	}
	if s.Outcome == nil {
		t.Errorf("AssertFailedOperations: value carries no outcome")
		return false
	}

	got := s.Outcome.FailedOps
	if len(got) == len(want) && equalInts(got, want) {
		return true
	}
	t.Errorf("AssertFailedOperations: operations %v failed, want %v\n%s",
		got, want, indent(explainOutcome(s), "  "))
	return false
}

// operationResult locates one operation's summary within a result.
func operationResult(t TB, assertion string, result Value, index int) (lens.OpSummary, *lens.Summary, bool) {
	t.Helper()

	s, ok := summarize(t, assertion, result, "TransactionResult")
	if !ok {
		return lens.OpSummary{}, nil, false
	}
	if index < 0 || index >= len(s.Operations) {
		t.Errorf("%s: no operation at index %d; the result covers %d operation(s)",
			assertion, index, len(s.Operations))
		return lens.OpSummary{}, nil, false
	}
	return s.Operations[index], s, true
}

// opLabel names an operation's type in parentheses, or says nothing when the
// type is unknown.
//
// A TransactionResult on its own does not carry operation types — only an
// envelope does — so lens reports "unknown". Printing that adds noise rather
// than information; pair the result with its envelope to see real names.
func opLabel(opType string) string {
	if opType == "" || opType == "unknown" {
		return ""
	}
	return " (" + opType + ")"
}

// describeReason renders a result code with its plain-English explanation and
// any actionable hint, which is the whole reason to use short codes here.
func describeReason(r lens.Reason) string {
	out := fmt.Sprintf("%s — %s", r.Code, r.Summary)
	if r.Hint != "" {
		out += "\n        → " + r.Hint
	}
	return out
}

// explainOutcome renders every operation's outcome, so a failure shows the
// whole picture rather than only the operation that was asserted on.
func explainOutcome(s *lens.Summary) string {
	if len(s.Operations) == 0 {
		if s.Outcome != nil {
			return describeReason(s.Outcome.Reason)
		}
		return ""
	}

	var b strings.Builder
	for _, op := range s.Operations {
		mark := "?"
		detail := ""
		if op.Result != nil {
			if op.Result.Success {
				mark = "ok"
			} else {
				mark = "FAILED"
				detail = " " + op.Result.Code
			}
		}
		opType := op.Type
		if opType == "unknown" {
			opType = "operation"
		}
		fmt.Fprintf(&b, "[%d] %-6s %s%s\n", op.Index, mark, opType, detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

// equalInts compares two index slices.
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
