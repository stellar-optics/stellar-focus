// Package require mirrors the golden assertions with Fatalf semantics.
//
// golden.Golden.Assert reports a failure and lets the test continue, which
// is what you want in a table test that should report every problem in one
// run. This package stops the test at the first failure, which is what you
// want when everything after the comparison would be meaningless — a
// fixture that does not exist, a decode that failed, a transaction that was
// never built.
//
// The split, and the naming, follow the convention Go developers already
// know from testify's assert and require packages, and from the sibling
// package pkg/focus/require.
package require

import (
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// Assert is golden.Golden.Assert, stopping the test on failure.
//
// The diagnostic is the one Assert reports — the same diff, in the same
// layout, with the same -focus.update instructions — so a test that stops
// still shows the reader everything a test that continued would have shown.
//
//	g := require.New(t, golden.WithDir("testdata"))
//	g.Assert("payment", BuildPayment(dest, "100"))
func Assert(t focus.TB, name string, actual focus.Value, opts ...golden.Opt) {
	t.Helper()
	golden.New(t, opts...).Require(name, actual)
}

// AssertAs is Assert with an explicit XDR type, stopping the test on failure.
//
// Naming the type removes the ambiguity of detecting it from the payload, and
// matches focus.AssertEqualAs and the golden.WithType option.
//
//	require.AssertAs(t, "TransactionEnvelope", "payment", BuildPayment(dest, "100"))
func AssertAs(t focus.TB, typeName, name string, actual focus.Value, opts ...golden.Opt) {
	t.Helper()
	golden.New(t, append([]golden.Opt{golden.WithType(typeName)}, opts...)...).Require(name, actual)
}

// Golden is a golden file comparison whose failures stop the test.
//
// It holds the same Golden as pkg/golden does, with the same options and
// the same comparison; the difference is that a failure ends the test
// rather than being reported and stepped over.
type Golden struct {
	g *golden.Golden
}

// New returns a Golden whose failures stop the test.
//
//	g := require.New(t, golden.WithType("TransactionEnvelope"))
//	g.Assert("payment", envelope)
//
// The options are the ones from pkg/golden, and the diagnostic is the one
// golden.Golden.Assert reports.
func New(t focus.TB, opts ...golden.Opt) *Golden {
	return &Golden{g: golden.New(t, opts...)}
}

// Assert is golden.Golden.Assert, stopping the test on failure.
//
// It returns false when the comparison failed, so a caller that wants to
// know can ask; the test has already been told.
func (g *Golden) Assert(name string, actual focus.Value) bool {
	return g.g.Require(name, actual)
}

// Path returns the file a given name maps to, as golden.Golden.Path does.
func (g *Golden) Path(name string) string {
	return g.g.Path(name)
}
