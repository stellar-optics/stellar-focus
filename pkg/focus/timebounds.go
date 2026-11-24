package focus

import (
	"fmt"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Precondition assertions read the time and ledger bounds a transaction
// declares in its preconditions. A field assertion checks a value the
// transaction carries; these check the validity window it promised before it
// could execute, which is what a wallet or exchange test usually wants to pin
// down.
//
// Both assertions compare the declared bounds exactly, with zero meaning
// "unbounded" — the protocol's own convention. A minimum time or ledger of
// zero is no lower bound, and a maximum of zero is no upper bound. An
// envelope that declares no bounds at all is read as 0..0, so asserting 0, 0
// is how a test says "this transaction does not expire".
//
// For a fee-bump envelope the bounds read here are the inner transaction's,
// since that is the transaction whose validity the protocol checks.

// timebounds are the effective time bounds of an envelope.
//
// A zero MinTime is no lower bound and a zero MaxTime no upper bound; the
// protocol treats a bound of 0, 0 as no bound at all. Set records whether the
// envelope declared any bounds, which only changes the failure message.
type timebounds struct {
	MinTime int64
	MaxTime int64
	Set     bool
}

// ledgerbounds are the effective ledger bounds of an envelope.
//
// A zero MinLedger is no lower bound and a zero MaxLedger no upper bound.
// Set records whether the envelope declared any bounds.
type ledgerbounds struct {
	MinLedger uint32
	MaxLedger uint32
	Set       bool
}

// extraSigners are the extra signers from V2 preconditions.
type extraSigners struct {
	// Signers are the strkey addresses of the extra signers.
	Signers []string
	Set     bool
}

// sequenceAge is the minimum sequence age from V2 preconditions.
type sequenceAge struct {
	// MinSeqAge is the minimum sequence age in seconds.
	MinSeqAge int64
	Set       bool
}

// AssertTimeboundsValid checks that the transaction declares exactly the
// wanted time bounds, in Unix seconds.
//
// A bound of zero means "unbounded": pass maxTime 0 to assert that the
// transaction does not expire, and 0, 0 to assert that it declares no bounds
// at all. The transaction's bounds are compared as a whole, so a min time that
// differs fails even when every other field matches.
//
// Transactions built before preconditions (V0 envelopes) carry their time
// bounds in the older field; both forms are read. Fee-bump envelopes are read
// through to the inner transaction.
func AssertTimeboundsValid(t TB, envelope Value, minTime, maxTime int64) bool {
	t.Helper()
	return assertTimeBounds(t, "AssertTimeboundsValid", envelope, minTime, maxTime)
}

// AssertTimeBounds checks that the envelope declares exactly the wanted time
// bounds, in Unix seconds.
//
// It asks the same question as AssertTimeboundsValid, under the name the
// precondition assertions use elsewhere, and answers it the same way: a
// bound of zero means "unbounded", so 0, 0 asserts that the transaction
// declares no time bounds at all, and a max time of zero asserts that it
// never expires.
//
// A V0 envelope, which carries its time bounds in the older field, is read
// the same way, and a fee-bump envelope is read through to the inner
// transaction — the one whose validity the protocol checks.
func AssertTimeBounds(t TB, envelope Value, wantMinTime, wantMaxTime int64) bool {
	t.Helper()
	return assertTimeBounds(t, "AssertTimeBounds", envelope, wantMinTime, wantMaxTime)
}

// AssertTimeBoundsWindow checks that the transaction's time bounds span
// exactly the wanted duration.
//
// The window is the time between the earliest and the latest moment the
// transaction is valid for — the length of time a sender gives the network
// to include it. A transaction that declares no bounds, or only one side of
// them, has no window, and the assertion fails saying so.
//
// The window is measured in whole seconds, the unit the protocol stores time
// bounds in, so a duration with a sub-second part never matches.
func AssertTimeBoundsWindow(t TB, envelope Value, wantDuration time.Duration) bool {
	t.Helper()
	const assertion = "AssertTimeBoundsWindow"

	got, ok := timeboundsFrom(t, assertion, envelope)
	if !ok {
		return false
	}

	// A window needs both ends: without them there is no length to measure.
	if !got.Set || got.MinTime == 0 || got.MaxTime == 0 {
		t.Errorf("%s: the transaction declares no time window\n\n  got   %s\n"+
			"  want  a window of %s\n\n"+
			"A window is the time between the earliest and the latest moment the "+
			"transaction is valid for, so it needs both of its bounds.",
			assertion, describeTimebounds(got), describeWindow(wantDuration))
		return false
	}

	gotWindow := time.Duration(got.MaxTime-got.MinTime) * time.Second
	if gotWindow == wantDuration {
		return true
	}

	t.Errorf("%s: the time window is %s, want %s\n\n  got   %s\n  want  a window of %s",
		assertion, describeWindow(gotWindow), describeWindow(wantDuration),
		describeTimebounds(got), describeWindow(wantDuration))
	return false
}

// AssertNoTimeBounds checks that the transaction declares no time bounds.
//
// A transaction with no time bounds is valid indefinitely: it never expires.
// The protocol's own way of saying the same thing — a bound of zero on both
// sides — counts as no bounds, because that is what the protocol means by
// it.
func AssertNoTimeBounds(t TB, envelope Value) bool {
	t.Helper()
	const assertion = "AssertNoTimeBounds"

	got, ok := timeboundsFrom(t, assertion, envelope)
	if !ok {
		return false
	}
	if !got.Set || (got.MinTime == 0 && got.MaxTime == 0) {
		return true
	}

	t.Errorf("%s: the transaction declares time bounds\n\n  got   %s\n  want  no time bounds (valid indefinitely)",
		assertion, describeTimebounds(got))
	return false
}

// assertTimeBounds is the shared body of the time-bound assertions: it reads
// the bounds, compares them as a whole and reports the difference.
func assertTimeBounds(t TB, assertion string, envelope Value, minTime, maxTime int64) bool {
	t.Helper()

	got, ok := timeboundsFrom(t, assertion, envelope)
	if !ok {
		return false
	}
	if got.MinTime == minTime && got.MaxTime == maxTime {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: time bounds differ\n\n", assertion)
	fmt.Fprintf(&b, "  got   %s\n", describeTimebounds(got))
	fmt.Fprintf(&b, "  want  %s\n\n", describeTimebounds(timebounds{
		MinTime: minTime, MaxTime: maxTime, Set: minTime != 0 || maxTime != 0,
	}))
	b.WriteString(indent(strings.Join(timeboundDiffLines(got, minTime, maxTime), "\n")))
	t.Errorf("%s", b.String())
	return false
}

// AssertLedgerboundsValid checks that the transaction declares exactly the
// wanted ledger bounds.
//
// A bound of zero means "unbounded", exactly as it does for time bounds: pass
// maxLedger 0 to assert that the transaction accepts any later ledger, and
// 0, 0 to assert that it declares no ledger bounds at all. Ledger bounds exist
// only in V2 preconditions, so a transaction that declares none reads as
// 0..0.
func AssertLedgerboundsValid(t TB, envelope Value, minLedger, maxLedger uint32) bool {
	t.Helper()
	return assertLedgerBounds(t, "AssertLedgerboundsValid", envelope, minLedger, maxLedger)
}

// AssertLedgerBounds checks that the envelope declares exactly the wanted
// ledger bounds.
//
// It asks the same question as AssertLedgerboundsValid, under the name the
// precondition assertions use elsewhere, and answers it the same way: a
// bound of zero means "unbounded", so 0, 0 asserts that the transaction
// declares no ledger bounds at all.
//
// Ledger bounds exist only in V2 preconditions, so a transaction that
// declares none reads as 0..0.
func AssertLedgerBounds(t TB, envelope Value, wantMinLedger, wantMaxLedger uint32) bool {
	t.Helper()
	return assertLedgerBounds(t, "AssertLedgerBounds", envelope, wantMinLedger, wantMaxLedger)
}

// assertLedgerBounds is the shared body of the ledger-bound assertions.
func assertLedgerBounds(t TB, assertion string, envelope Value, minLedger, maxLedger uint32) bool {
	t.Helper()

	got, ok := ledgerboundsFrom(t, assertion, envelope)
	if !ok {
		return false
	}
	if got.MinLedger == minLedger && got.MaxLedger == maxLedger {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: ledger bounds differ\n\n", assertion)
	fmt.Fprintf(&b, "  got   %s\n", describeLedgerbounds(got))
	fmt.Fprintf(&b, "  want  %s\n\n", describeLedgerbounds(ledgerbounds{
		MinLedger: minLedger, MaxLedger: maxLedger, Set: minLedger != 0 || maxLedger != 0,
	}))
	b.WriteString(indent(strings.Join(ledgerboundDiffLines(got, minLedger, maxLedger), "\n")))
	t.Errorf("%s", b.String())
	return false
}

// timeboundsFrom reads an envelope's effective time bounds. It calls the
// generated accessors, so it must run behind guard.
func timeboundsFrom(t TB, assertion string, envelope Value) (timebounds, bool) {
	t.Helper()

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return timebounds{}, false
	}

	var out timebounds
	if !guard(t, assertion, func() {
		if tb := env.TimeBounds(); tb != nil {
			out = timebounds{
				MinTime: int64(tb.MinTime),
				MaxTime: int64(tb.MaxTime),
				Set:     true,
			}
		}
	}) {
		return timebounds{}, false
	}
	return out, true
}

// ledgerboundsFrom reads an envelope's effective ledger bounds. It calls the
// generated accessors, so it must run behind guard.
func ledgerboundsFrom(t TB, assertion string, envelope Value) (ledgerbounds, bool) {
	t.Helper()

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return ledgerbounds{}, false
	}

	var out ledgerbounds
	if !guard(t, assertion, func() {
		if lb := env.LedgerBounds(); lb != nil {
			out = ledgerbounds{
				MinLedger: uint32(lb.MinLedger),
				MaxLedger: uint32(lb.MaxLedger),
				Set:       true,
			}
		}
	}) {
		return ledgerbounds{}, false
	}
	return out, true
}

// AssertExtraSigners checks that the envelope's V2 preconditions carry the
// expected extra signer keys. Each want signer is a strkey address ("G…") that
// must appear in the envelope's ExtraSigners list.
//
// If the envelope does not carry V2 preconditions, the assertion fails with a
// clear message noting what precondition form is present (or absent).
//
// Extra signers are a Protocol 19 (CAP-0021) feature: they are additional
// signers beyond the transaction's source account that must validate the
// transaction. This assertion verifies their presence.
func AssertExtraSigners(t TB, envelope Value, wantSigners ...string) bool {
	t.Helper()
	const assertion = "AssertExtraSigners"

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return false
	}

	got, ok := extraSignersFrom(t, assertion, env)
	if !ok {
		return false
	}

	if len(got.Signers) == 0 && len(wantSigners) > 0 {
		t.Errorf("%s: the envelope does not carry V2 preconditions (no extra signers available)\n\n  got   no extra signers\n  want %s", assertion, wantSigners)
		return false
	}

	if len(wantSigners) == 0 {
		// Want no signers: pass if extra signers exist (they are simply ignored),
		// or fail if they don't exist and the test expected none but the envelope
		// has none - that's fine too. Actually, if wantSigners is empty, we just
		// check that the envelope's extra signers (if any) don't cause a mismatch.
		// Since there's nothing to check, this always passes.
		return true
	}

	// Check that every wanted signer is present in the envelope's extra signers.
	missing := make([]string, 0, len(wantSigners))
	wantSet := make(map[string]bool, len(wantSigners))
	for _, w := range wantSigners {
		wantSet[w] = true
	}
	for _, g := range got.Signers {
		wantSet[g] = false
	}
	for w, found := range wantSet {
		if found {
			missing = append(missing, w)
		}
	}

	if len(missing) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s: extra signers differ\n\n", assertion)
		fmt.Fprintf(&b, "  got   extra signers: %s\n", describeExtraSigners(got))
		fmt.Fprintf(&b, "  want  %s\n\n", describeWantSigners(wantSigners, missing))
		t.Errorf("%s", b.String())
		return false
	}

	return true
}

// extraSignersFrom reads an envelope's V2 preconditions and extracts the
// extra signers.
func extraSignersFrom(t TB, assertion string, env *xdr.TransactionEnvelope) (extraSigners, bool) {
	t.Helper()

	var out extraSigners
	if env.V1 == nil {
		return out, false
	}

	// Access the V2 preconditions directly via the Cond struct field.
	// env.V1.Tx.Cond is of type xdr.Preconditions which has a V2 field.
	// We do not compare Cond to nil since Preconditions is a named struct type.
	if cond := env.V1.Tx.Cond.V2; cond != nil {
		// V2 preconditions — extract extra signers.
		if cond.ExtraSigners == nil {
			return out, true // V2 present but no extra signers
		}

		out.Set = true
		out.Signers = make([]string, 0, len(cond.ExtraSigners))
		for _, sk := range cond.ExtraSigners {
			// SignerKey.Address is an AccountId; use GetAddress to get the strkey.
			addr, err := sk.GetAddress()
			if err != nil {
				t.Errorf("%s: could not read extra signer address: %v", assertion, err)
				continue
			}
			// Use the string representation for comparison; both forms should
			// resolve to the same address.
			out.Signers = append(out.Signers, addr)
		}
		return out, true
	}

	// No V2 preconditions (either V1 or none).
	t.Errorf("%s: this envelope does not carry V2 preconditions; "+
		"AssertExtraSigners requires V2 preconditions", assertion)
	return out, false
}

// AssertSequenceAge checks that the envelope's V2 preconditions carry the
// minimum sequence age the test wants. The duration is in seconds, matching
// the protocol's own convention: a bound of zero means "unbounded".
//
// If the envelope does not carry V2 preconditions, the assertion fails with a
// clear message noting what precondition form is present (or absent).
//
// Sequence age is a Protocol 19 (CAP-0021) feature: it sets a minimum amount
// of time (measured in sequence numbers) that must elapse before a transaction
// can be valid. This assertion verifies that the declared minimum matches.
func AssertSequenceAge(t TB, envelope Value, wantMinAge time.Duration, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertSequenceAge"

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return false
	}

	got, ok := sequenceAgeFrom(t, assertion, env)
	if !ok {
		return false
	}

	if !got.Set {
		t.Errorf("%s: the envelope does not carry V2 preconditions (no sequence age available)\n\n  got   no sequence age\n  want %s", assertion, describeDuration(wantMinAge))
		return false
	}

	if got.MinSeqAge == 0 && wantMinAge == 0 {
		// Both zero means unbounded / no constraint — treat as match.
		return true
	}

	if got.MinSeqAge == int64(wantMinAge.Seconds()) {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: sequence age differs\n\n", assertion)
	fmt.Fprintf(&b, "  got   min seq age is %s (%d seconds)\n", renderSeqAge(got.MinSeqAge), got.MinSeqAge)
	fmt.Fprintf(&b, "  want  min seq age is %s (%d seconds)\n\n", describeDuration(wantMinAge), int64(wantMinAge.Seconds()))
	t.Errorf("%s", b.String())
	return false
}

// sequenceAgeFrom reads an envelope's V2 preconditions and extracts the
// minimum sequence age.
func sequenceAgeFrom(t TB, assertion string, env *xdr.TransactionEnvelope) (sequenceAge, bool) {
	t.Helper()

	var out sequenceAge
	if env.V1 == nil {
		return out, false
	}

	// Access the V2 preconditions directly via the Cond struct field.
	// env.V1.Tx.Cond is of type xdr.Preconditions which has a V2 field.
	// We do not compare Cond to nil since Preconditions is a named struct type.
	if cond := env.V1.Tx.Cond.V2; cond != nil {
		// V2 preconditions — extract minimum sequence age.
		if cond.MinSeqAge == 0 {
			// Zero means unbounded / no constraint.
			out.Set = true
			return out, true
		}

		out.Set = true
		out.MinSeqAge = int64(cond.MinSeqAge)
		return out, true
	}

	// No V2 preconditions (either V1 or none).
	t.Errorf("%s: this envelope does not carry V2 preconditions; "+
		"AssertSequenceAge requires V2 preconditions", assertion)
	return out, false
}

// describeExtraSigners renders the extra signers found in the envelope.
func describeExtraSigners(got extraSigners) string {
	if !got.Set || len(got.Signers) == 0 {
		return "none"
	}
	if len(got.Signers) == 1 {
		return got.Signers[0]
	}
	// Show first few, then count.
	shown := make([]string, 0, len(got.Signers))
	for i, s := range got.Signers {
		if i >= 3 {
			shown = append(shown, fmt.Sprintf("… +%d", len(got.Signers)-3))
			break
		}
		shown = append(shown, s)
	}
	return strings.Join(shown, ", ")
}

// describeWantSigners renders the wanted signers, marking the missing ones.
func describeWantSigners(wantSigners []string, missing []string) string {
	var b strings.Builder
	for i, w := range wantSigners {
		if i > 0 {
			fmt.Fprint(&b, ", ")
		}
		if contains(missing, w) {
			fmt.Fprintf(&b, "%s (MISSING)", w)
		} else {
			fmt.Fprint(&b, w)
		}
	}
	return b.String()
}

// renderSeqAge renders a minimum sequence age value, treating zero as unbounded.
func renderSeqAge(age int64) string {
	if age == 0 {
		return "0 (unbounded)"
	}
	return fmt.Sprintf("%d", age)
}

// describeDuration renders a duration the way a developer reads it.
func describeDuration(d time.Duration) string {
	return fmt.Sprintf("%ds (%s)", int64(d/time.Second), d)
}

// contains reports whether slice s contains string x.
func contains(s []string, x string) bool {
	for _, f := range s {
		if f == x {
			return true
		}
	}
	return false
}

func describeTimebounds(b timebounds) string {
	if !b.Set || (b.MinTime == 0 && b.MaxTime == 0) {
		return "no time bounds (valid indefinitely)"
	}
	switch {
	case b.MinTime == 0:
		return "valid until " + formatTime(b.MaxTime)
	case b.MaxTime == 0:
		return "valid from " + formatTime(b.MinTime)
	default:
		return fmt.Sprintf("valid from %s until %s", formatTime(b.MinTime), formatTime(b.MaxTime))
	}
}

// describeLedgerbounds renders a ledger window. Zero is rendered as the
// unbounded side it is, so a failure does not suggest the transaction wants
// ledger 0.
func describeLedgerbounds(b ledgerbounds) string {
	if !b.Set || (b.MinLedger == 0 && b.MaxLedger == 0) {
		return "no ledger bounds"
	}
	switch {
	case b.MinLedger == 0:
		return fmt.Sprintf("valid until ledger %d (no lower bound)", b.MaxLedger)
	case b.MaxLedger == 0:
		return fmt.Sprintf("valid from ledger %d (no upper bound)", b.MinLedger)
	default:
		return fmt.Sprintf("ledger bounds %d..%d", b.MinLedger, b.MaxLedger)
	}
}

// describeWindow renders a duration both as the whole seconds the protocol
// counts in and as the length a person reads it as.
func describeWindow(d time.Duration) string {
	return fmt.Sprintf("%ds (%s)", int64(d/time.Second), d)
}

// timeboundDiffLines names each time bound that differs, both as a timestamp
// and as the raw seconds the protocol stores, so the reader does not have to
// convert between them.
func timeboundDiffLines(got timebounds, minTime, maxTime int64) []string {
	var lines []string
	if got.MinTime != minTime {
		lines = append(lines, fmt.Sprintf("min time is %s, want %s",
			renderTimebound(got.MinTime), renderTimebound(minTime)))
	}
	if got.MaxTime != maxTime {
		lines = append(lines, fmt.Sprintf("max time is %s, want %s",
			renderTimebound(got.MaxTime), renderTimebound(maxTime)))
	}
	return lines
}

// ledgerboundDiffLines names each ledger bound that differs.
func ledgerboundDiffLines(got ledgerbounds, minLedger, maxLedger uint32) []string {
	var lines []string
	if got.MinLedger != minLedger {
		lines = append(lines, fmt.Sprintf("min ledger is %s, want %s",
			renderLedgerbound(got.MinLedger), renderLedgerbound(minLedger)))
	}
	if got.MaxLedger != maxLedger {
		lines = append(lines, fmt.Sprintf("max ledger is %s, want %s",
			renderLedgerbound(got.MaxLedger), renderLedgerbound(maxLedger)))
	}
	return lines
}

// renderTimebound names one time bound, treating zero as the protocol's
// "no bound" rather than as the Unix epoch.
func renderTimebound(sec int64) string {
	if sec == 0 {
		return "0 (unbounded)"
	}
	return fmt.Sprintf("%d (%s)", sec, formatTime(sec))
}

// renderLedgerbound names one ledger bound, treating zero as unbounded.
func renderLedgerbound(ledger uint32) string {
	if ledger == 0 {
		return "0 (unbounded)"
	}
	return fmt.Sprintf("%d", ledger)
}

// formatTime renders a Unix timestamp in UTC, the unit the protocol stores
// time bounds in and the only one that is unambiguous across time zones.
func formatTime(sec int64) string {
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}
