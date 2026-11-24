package focus

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// AssertOperationSequence checks that the transaction's operations are exactly
// the wanted types, in order.
//
// Order is the point: a multi-operation transaction executes its operations in
// XDR order, and a batch that is otherwise identical can behave completely
// differently when two operations are swapped — a payment before a change of
// trust can fail where the reverse succeeds. The assertion is therefore
// sensitive to sequence as well as membership, unlike AssertOperationCount and
// AssertOperationTypes, which compare sets of names.
//
// The wanted types are xdr.OperationType values, so the compiler checks them:
//
//	focus.AssertOperationSequence(t, env,
//		[]xdr.OperationType{xdr.OperationTypeChangeTrust, xdr.OperationTypePayment})
//
// Fee-bump envelopes are read through to the inner transaction, whose
// operations are the ones that execute. Failure messages name the first
// position that diverges and list the operations present, using the same
// snake_case names `lens explain` prints.
func AssertOperationSequence(t TB, envelope Value, want []xdr.OperationType) bool {
	t.Helper()
	const assertion = "AssertOperationSequence"

	s, ok := summarize(t, assertion, envelope, "TransactionEnvelope")
	if !ok {
		return false
	}
	got := operationTypes(s)
	wantNames := operationTypeNames(want)

	if len(got) == len(wantNames) && equalStrings(got, wantNames) {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n\n", assertion, describeSequenceDifference(got, wantNames))
	fmt.Fprintf(&b, "  got   %s\n", describeOperationSequence(got))
	fmt.Fprintf(&b, "  want  %s\n", describeOperationSequence(wantNames))
	if listing := operationList(s); listing != "" {
		fmt.Fprintf(&b, "\n%s\n", indent(listing))
	}
	t.Errorf("%s", strings.TrimRight(b.String(), "\n"))
	return false
}

// operationTypes reads the snake_case operation names lens reports, in order.
func operationTypes(s *lens.Summary) []string {
	got := make([]string, 0, len(s.Operations))
	for _, op := range s.Operations {
		got = append(got, op.Type)
	}
	return got
}

// operationTypeNames renders the wanted types the way lens names them.
func operationTypeNames(want []xdr.OperationType) []string {
	out := make([]string, len(want))
	for i, t := range want {
		out[i] = operationTypeName(t)
	}
	return out
}

// describeSequenceDifference names the first position at which the sequences
// diverge, or reports that one ran out, so the reader knows where to look
// without comparing the two lines by eye.
func describeSequenceDifference(got, want []string) string {
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			return fmt.Sprintf("operation sequence differs at index %d: it is %q, want %q",
				i, got[i], want[i])
		}
	}
	if len(got) < len(want) {
		return fmt.Sprintf("the transaction has %d operation(s), want %d: index %d should be %q",
			len(got), len(want), len(got), want[len(got)])
	}
	return fmt.Sprintf("the transaction has %d operation(s), want %d: index %d should not exist",
		len(got), len(want), len(want))
}

// describeOperationSequence renders a sequence on one line with each index
// visible, so a difference in length or position is easy to spot.
func describeOperationSequence(names []string) string {
	if len(names) == 0 {
		return "(no operations)"
	}
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("[%d] %s", i, name)
	}
	return strings.Join(parts, "  ")
}

// operationTypeName renders an operation type the way lens does, stripping the
// XDR prefix and snake_casing the rest, so a failure message uses the same
// words as `lens explain` for the same transaction.
func operationTypeName(t xdr.OperationType) string {
	name := strings.TrimPrefix(t.String(), "OperationType")
	if name == "" {
		return fmt.Sprintf("operation_type_%d", t)
	}

	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
