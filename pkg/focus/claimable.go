package focus

import (
	"fmt"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Claimable balance assertions read the entry a create-claimable-balance
// operation puts in the ledger.
//
// It is not visible in the envelope. The balance id is derived by the protocol
// from the operation and the transaction's source account and sequence number,
// so a test that only inspects what it submitted cannot name the balance it
// created — the meta is the first place the id exists. The amount is worth
// checking there for the same reason: the operation says what was asked for,
// the entry says what the ledger now holds.
//
// The other end of a balance's life — its removal, by a claim or a clawback —
// is one ledger change and one assertion, AssertClaimableBalanceRevoked in
// clawback.go. There is deliberately no second name for it here.
//
// Reading goes through the flattened changes the entry assertions use
// (ledgerChanges), so every assertion in the package sees a transaction the
// same way.

// claimableBalance is a sanitised claimable balance entry: the fields an
// assertion matches on, captured while the value is known to be well-formed.
type claimableBalance struct {
	// id is the balance id in hex, the form the protocol's own tooling shows.
	id string
	// asset is the canonical asset label, "native" or "CODE:ISSUER".
	asset string
	// amount is the stroop amount the entry holds.
	amount xdr.Int64
	// claimants lists each claimant's destination address, in entry order.
	claimants []string
	// predicates describes each claimant's predicate, positionally matching
	// claimants.
	predicates []string
	// restored reports that the entry came back from the Soroban archival
	// state rather than being created from nothing.
	restored bool
}

// AssertClaimableBalanceCreated checks that the transaction created a
// claimable balance of amount stroops that claimant can claim.
//
// claimant accepts a strkey address, an xdr.AccountId or an xdr.MuxedAccount;
// a muxed account is reduced to the underlying G… account, since a claimant
// is recorded as one. The amount is in stroops, the unit the entry stores.
//
// The claimant has to appear among the balance's claimants, not be its only
// one: a balance claimable by either of two accounts is claimable by each of
// them. The predicate guarding the claim is not checked — whether it is
// satisfiable depends on a clock, which an offline assertion has no business
// reading — but it is shown in the failure listing, since a balance with the
// right claimant and an unexpected predicate is a common reason for a test to
// be looking here at all.
//
// An entry restored from the Soroban archival state counts as created, as it
// does for AssertEntryCreated: it is new to the live ledger.
//
// For the other end of the balance's life, that its entry is gone, use
// AssertClaimableBalanceRevoked.
func AssertClaimableBalanceCreated(t TB, meta Value, claimant Value, amount int64) bool {
	t.Helper()
	const assertion = "AssertClaimableBalanceCreated"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	addr, ok := resolveAccount(t, assertion, "claimant", claimant)
	if !ok {
		return false
	}
	created, ok := createdClaimableBalances(t, assertion, changes)
	if !ok {
		return false
	}

	if len(created) == 0 {
		t.Errorf("%s: the transaction created no claimable balance; "+
			"want %s claimable by account %s\n\n%s",
			assertion, amountStroops(xdr.Int64(amount), ""), shorten(addr),
			indent(describeChanges(changes)))
		return false
	}

	forClaimant := make([]claimableBalance, 0, len(created))
	for _, cb := range created {
		for _, c := range cb.claimants {
			if c == addr {
				forClaimant = append(forClaimant, cb)
				break
			}
		}
	}

	if len(forClaimant) == 0 {
		t.Errorf("%s: the transaction created %s, none of them claimable by account %s\n\n%s",
			assertion, pluralBalances(len(created)), shorten(addr),
			indent(describeClaimableBalances(created)))
		return false
	}
	for _, cb := range forClaimant {
		if int64(cb.amount) == amount {
			return true
		}
	}

	// The claimant matched, so the amount is the difference worth naming.
	// With one candidate the message can be exact; with several it names the
	// amounts found rather than picking one arbitrarily.
	if len(forClaimant) == 1 {
		cb := forClaimant[0]
		t.Errorf("%s: claimable balance %s for account %s holds %s, want %s\n\n%s",
			assertion, shortenID(cb.id), shorten(addr),
			amountStroops(cb.amount, assetUnit(cb.asset)),
			amountStroops(xdr.Int64(amount), assetUnit(cb.asset)),
			indent(describeClaimableBalances(created)))
		return false
	}
	t.Errorf("%s: account %s can claim %s, holding %s; want one of %s\n\n%s",
		assertion, shorten(addr), pluralBalances(len(forClaimant)),
		strings.Join(balanceAmounts(forClaimant), ", "),
		amountStroops(xdr.Int64(amount), ""),
		indent(describeClaimableBalances(created)))
	return false
}

// createdClaimableBalances collects the claimable balances a transaction put
// in the ledger, in the order the protocol applied them.
func createdClaimableBalances(t TB, assertion string, changes []ledgerChange) ([]claimableBalance, bool) {
	var out []claimableBalance
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeClaimableBalance {
				continue
			}
			if c.kind != xdr.LedgerEntryChangeTypeLedgerEntryCreated &&
				c.kind != xdr.LedgerEntryChangeTypeLedgerEntryRestored {
				continue
			}
			cb, has := claimableBalanceOf(c)
			if !has {
				continue
			}
			out = append(out, cb)
		}
	})
	if !ok {
		return nil, false
	}
	return out, true
}

// claimableBalanceOf reads a change's claimable balance entry. It calls
// generated accessors, so it must run behind guard.
func claimableBalanceOf(c ledgerChange) (claimableBalance, bool) {
	entry := c.entry
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeClaimableBalance ||
		entry.Data.ClaimableBalance == nil {
		return claimableBalance{}, false
	}
	cb := entry.Data.ClaimableBalance

	out := claimableBalance{
		id:       describeClaimableBalanceID(cb.BalanceId),
		asset:    cb.Asset.StringCanonical(),
		amount:   cb.Amount,
		restored: c.kind == xdr.LedgerEntryChangeTypeLedgerEntryRestored,
	}
	for _, claimant := range cb.Claimants {
		v0, ok := claimant.GetV0()
		if !ok {
			// An unknown claimant arm is recorded as such rather than
			// dropped: a balance with a claimant this build cannot read is
			// not a balance with one fewer claimant.
			out.claimants = append(out.claimants, "")
			out.predicates = append(out.predicates, "claimant type "+claimant.Type.String())
			continue
		}
		out.claimants = append(out.claimants, v0.Destination.Address())
		out.predicates = append(out.predicates, describeClaimPredicate(v0.Predicate))
	}
	return out, true
}

// describeClaimPredicate renders a claim predicate compactly, so a failure
// listing shows the condition a claimant is subject to without printing a
// tree.
func describeClaimPredicate(p xdr.ClaimPredicate) string {
	switch p.Type {
	case xdr.ClaimPredicateTypeClaimPredicateUnconditional:
		return "unconditional"
	case xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime:
		if p.AbsBefore != nil {
			return fmt.Sprintf("before unix time %d", int64(*p.AbsBefore))
		}
	case xdr.ClaimPredicateTypeClaimPredicateBeforeRelativeTime:
		if p.RelBefore != nil {
			return fmt.Sprintf("within %ds of creation", int64(*p.RelBefore))
		}
	case xdr.ClaimPredicateTypeClaimPredicateNot:
		if p.NotPredicate != nil && *p.NotPredicate != nil {
			return "not (" + describeClaimPredicate(**p.NotPredicate) + ")"
		}
	case xdr.ClaimPredicateTypeClaimPredicateAnd:
		if p.AndPredicates != nil {
			return "(" + joinPredicates(*p.AndPredicates, " and ") + ")"
		}
	case xdr.ClaimPredicateTypeClaimPredicateOr:
		if p.OrPredicates != nil {
			return "(" + joinPredicates(*p.OrPredicates, " or ") + ")"
		}
	}
	return strings.TrimPrefix(p.Type.String(), "ClaimPredicateTypeClaimPredicate")
}

// joinPredicates renders a conjunction or disjunction of predicates.
func joinPredicates(ps []xdr.ClaimPredicate, sep string) string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		parts = append(parts, describeClaimPredicate(p))
	}
	return strings.Join(parts, sep)
}

// describeClaimableBalances lists the balances created, with the claimants and
// predicates that decide who can take them.
func describeClaimableBalances(balances []claimableBalance) string {
	if len(balances) == 0 {
		return "the transaction created no claimable balance"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "claimable balances created (%d):\n", len(balances))
	for i, cb := range balances {
		verb := "created"
		if cb.restored {
			verb = "restored"
		}
		fmt.Fprintf(&b, "  [%d] %s %s — %s %s\n",
			i, verb, shortenID(cb.id), cb.asset, amountStroops(cb.amount, assetUnit(cb.asset)))
		if len(cb.claimants) == 0 {
			b.WriteString("      claimants: none\n")
			continue
		}
		for j, c := range cb.claimants {
			who := shorten(c)
			if c == "" {
				who = "(unreadable)"
			}
			fmt.Fprintf(&b, "      claimant %s, %s\n", who, cb.predicates[j])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// balanceAmounts renders the amounts of several balances, for a message that
// cannot single one out.
func balanceAmounts(balances []claimableBalance) []string {
	out := make([]string, 0, len(balances))
	for _, cb := range balances {
		out = append(out, amountStroops(cb.amount, assetUnit(cb.asset)))
	}
	return out
}

func pluralBalances(n int) string {
	if n == 1 {
		return "1 claimable balance"
	}
	return fmt.Sprintf("%d claimable balances", n)
}

// assetUnit names the asset in a rendered amount, where that reads naturally.
// It mirrors assetSpec.unit for a canonical asset label.
func assetUnit(asset string) string {
	if asset == "native" {
		return " XLM"
	}
	return ""
}

// shortenID abbreviates a hex identifier the way shorten abbreviates a
// strkey, so a 64-character balance or pool id does not dominate a message.
func shortenID(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "…" + id[len(id)-8:]
}

// AssertClaimPredicateTree asserts that claimant at claimantIndex in opOrEntry has a
// claim predicate tree recursively matching wantPredicate.
//
// opOrEntry accepts an xdr.ClaimableBalanceEntry, an xdr.CreateClaimableBalanceOp,
// an xdr.LedgerEntry, an xdr.Operation, an xdr.TransactionEnvelope, an xdr.TransactionMeta,
// an xdr.Claimant, a slice []xdr.Claimant, or base64/bytes/*lens.Value of those.
//
// In failure messages, the structural path of any deviating branch is displayed alongside
// pretty-printed predicate trees with human-readable timestamps.
func AssertClaimPredicateTree(t TestingT, opOrEntry any, claimantIndex int, wantPredicate xdr.ClaimPredicate, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertClaimPredicateTree"
	_ = newConfig(opts...)

	claimants, ok := extractClaimants(t, opOrEntry)
	if !ok {
		return false
	}
	if claimantIndex < 0 || claimantIndex >= len(claimants) {
		t.Errorf("%s: claimantIndex %d out of bounds (found %d claimant(s))",
			assertion, claimantIndex, len(claimants))
		return false
	}

	c := claimants[claimantIndex]
	v0, ok := c.GetV0()
	if !ok {
		t.Errorf("%s: claimant at index %d is not a V0 claimant (type %s)",
			assertion, claimantIndex, c.Type.String())
		return false
	}

	gotPredicate := v0.Predicate
	diff, matched := diffClaimPredicateTree("$", wantPredicate, gotPredicate)
	if matched {
		return true
	}

	t.Errorf("%s: claim predicate mismatch at claimant index %d:\n  %s\n\nwant predicate:\n%s\n\ngot predicate:\n%s",
		assertion, claimantIndex, diff,
		indent(prettyClaimPredicateTree(wantPredicate, 0)),
		indent(prettyClaimPredicateTree(gotPredicate, 0)))
	return false
}

// extractClaimants retrieves the claimant slice from various accepted input types.
func extractClaimants(t TB, opOrEntry any) ([]xdr.Claimant, bool) {
	t.Helper()
	const assertion = "AssertClaimPredicateTree"
	if opOrEntry == nil {
		t.Errorf("%s: opOrEntry is nil", assertion)
		return nil, false
	}
	switch v := opOrEntry.(type) {
	case xdr.ClaimableBalanceEntry:
		return v.Claimants, true
	case *xdr.ClaimableBalanceEntry:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.ClaimableBalanceEntry", assertion)
			return nil, false
		}
		return v.Claimants, true
	case xdr.CreateClaimableBalanceOp:
		return v.Claimants, true
	case *xdr.CreateClaimableBalanceOp:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.CreateClaimableBalanceOp", assertion)
			return nil, false
		}
		return v.Claimants, true
	case xdr.Operation:
		if v.Body.Type == xdr.OperationTypeCreateClaimableBalance && v.Body.CreateClaimableBalanceOp != nil {
			return v.Body.CreateClaimableBalanceOp.Claimants, true
		}
		t.Errorf("%s: operation is type %s, want CreateClaimableBalance", assertion, v.Body.Type.String())
		return nil, false
	case *xdr.Operation:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.Operation", assertion)
			return nil, false
		}
		return extractClaimants(t, *v)
	case xdr.LedgerEntry:
		if v.Data.Type == xdr.LedgerEntryTypeClaimableBalance && v.Data.ClaimableBalance != nil {
			return v.Data.ClaimableBalance.Claimants, true
		}
		t.Errorf("%s: ledger entry is type %s, want ClaimableBalance", assertion, v.Data.Type.String())
		return nil, false
	case *xdr.LedgerEntry:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.LedgerEntry", assertion)
			return nil, false
		}
		return extractClaimants(t, *v)
	case xdr.Claimant:
		return []xdr.Claimant{v}, true
	case *xdr.Claimant:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.Claimant", assertion)
			return nil, false
		}
		return []xdr.Claimant{*v}, true
	case []xdr.Claimant:
		return v, true
	case xdr.ClaimPredicate:
		return []xdr.Claimant{{
			Type: xdr.ClaimantTypeClaimantTypeV0,
			V0:   &xdr.ClaimantV0{Predicate: v},
		}}, true
	case *xdr.ClaimPredicate:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.ClaimPredicate", assertion)
			return nil, false
		}
		return extractClaimants(t, *v)
	case xdr.TransactionEnvelope:
		return claimantsFromEnvelope(t, &v)
	case *xdr.TransactionEnvelope:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.TransactionEnvelope", assertion)
			return nil, false
		}
		return claimantsFromEnvelope(t, v)
	case xdr.TransactionMeta:
		return claimantsFromMeta(t, v)
	case *xdr.TransactionMeta:
		if v == nil {
			t.Errorf("%s: opOrEntry is a nil *xdr.TransactionMeta", assertion)
			return nil, false
		}
		return claimantsFromMeta(t, *v)
	default:
		decoded, err := coerce(opOrEntry, "")
		if err == nil && decoded != nil && decoded.Raw != nil {
			switch raw := decoded.Raw.(type) {
			case *xdr.TransactionEnvelope:
				return claimantsFromEnvelope(t, raw)
			case *xdr.TransactionMeta:
				return claimantsFromMeta(t, raw)
			case *xdr.LedgerEntry:
				return extractClaimants(t, raw)
			case *xdr.ClaimableBalanceEntry:
				return extractClaimants(t, raw)
			case *xdr.CreateClaimableBalanceOp:
				return extractClaimants(t, raw)
			case *xdr.Operation:
				return extractClaimants(t, raw)
			}
		}
		changes, ok := ledgerChanges(t, assertion, opOrEntry)
		if ok {
			for _, c := range changes {
				if c.entry != nil && c.entry.Data.Type == xdr.LedgerEntryTypeClaimableBalance && c.entry.Data.ClaimableBalance != nil {
					return c.entry.Data.ClaimableBalance.Claimants, true
				}
			}
		}
		t.Errorf("%s: unsupported opOrEntry type %T; expected ClaimableBalanceEntry, CreateClaimableBalanceOp, LedgerEntry, TransactionEnvelope, or TransactionMeta", assertion, opOrEntry)
		return nil, false
	}
}

func claimantsFromEnvelope(t TB, env *xdr.TransactionEnvelope) ([]xdr.Claimant, bool) {
	t.Helper()
	const assertion = "AssertClaimPredicateTree"
	var ops []xdr.Operation
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		if env.V0 != nil {
			ops = env.V0.Tx.Operations
		}
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if env.V1 != nil {
			ops = env.V1.Tx.Operations
		}
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump != nil && env.FeeBump.Tx.InnerTx.V1 != nil {
			ops = env.FeeBump.Tx.InnerTx.V1.Tx.Operations
		}
	}
	for _, op := range ops {
		if op.Body.Type == xdr.OperationTypeCreateClaimableBalance && op.Body.CreateClaimableBalanceOp != nil {
			return op.Body.CreateClaimableBalanceOp.Claimants, true
		}
	}
	t.Errorf("%s: envelope contains no CreateClaimableBalance operation", assertion)
	return nil, false
}

func claimantsFromMeta(t TB, meta any) ([]xdr.Claimant, bool) {
	t.Helper()
	const assertion = "AssertClaimPredicateTree"
	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return nil, false
	}
	for _, c := range changes {
		if c.entry != nil && c.entry.Data.Type == xdr.LedgerEntryTypeClaimableBalance && c.entry.Data.ClaimableBalance != nil {
			return c.entry.Data.ClaimableBalance.Claimants, true
		}
	}
	t.Errorf("%s: meta contains no claimable balance entry changes", assertion)
	return nil, false
}

// diffClaimPredicateTree recursively compares two ClaimPredicates, returning a diff message and false if they differ.
func diffClaimPredicateTree(path string, want, got xdr.ClaimPredicate) (string, bool) {
	if want.Type != got.Type {
		return fmt.Sprintf("at path %q: predicate type mismatch: want %s, got %s",
			path, formatClaimPredicateType(want.Type), formatClaimPredicateType(got.Type)), false
	}
	switch want.Type {
	case xdr.ClaimPredicateTypeClaimPredicateUnconditional:
		return "", true

	case xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime:
		if want.AbsBefore == nil || got.AbsBefore == nil {
			if want.AbsBefore != got.AbsBefore {
				return fmt.Sprintf("at path %q: absolute time mismatch (nil pointer)", path), false
			}
			return "", true
		}
		w := int64(*want.AbsBefore)
		g := int64(*got.AbsBefore)
		if w != g {
			return fmt.Sprintf("at path %q: absolute time mismatch: want %d (%s), got %d (%s)",
				path, w, time.Unix(w, 0).UTC().Format(time.RFC3339),
				g, time.Unix(g, 0).UTC().Format(time.RFC3339)), false
		}
		return "", true

	case xdr.ClaimPredicateTypeClaimPredicateBeforeRelativeTime:
		if want.RelBefore == nil || got.RelBefore == nil {
			if want.RelBefore != got.RelBefore {
				return fmt.Sprintf("at path %q: relative time mismatch (nil pointer)", path), false
			}
			return "", true
		}
		w := int64(*want.RelBefore)
		g := int64(*got.RelBefore)
		if w != g {
			return fmt.Sprintf("at path %q: relative time mismatch: want %ds (%s), got %ds (%s)",
				path, w, time.Duration(w*int64(time.Second)).String(),
				g, time.Duration(g*int64(time.Second)).String()), false
		}
		return "", true

	case xdr.ClaimPredicateTypeClaimPredicateNot:
		if want.NotPredicate == nil || *want.NotPredicate == nil ||
			got.NotPredicate == nil || *got.NotPredicate == nil {
			if (want.NotPredicate == nil || *want.NotPredicate == nil) !=
				(got.NotPredicate == nil || *got.NotPredicate == nil) {
				return fmt.Sprintf("at path %q: NOT predicate nil mismatch", path), false
			}
			return "", true
		}
		return diffClaimPredicateTree(path+".not", **want.NotPredicate, **got.NotPredicate)

	case xdr.ClaimPredicateTypeClaimPredicateAnd:
		wPreds := want.AndPredicates
		gPreds := got.AndPredicates
		if wPreds == nil || gPreds == nil {
			if (wPreds == nil) != (gPreds == nil) {
				return fmt.Sprintf("at path %q: AND child list nil mismatch", path), false
			}
			return "", true
		}
		if len(*wPreds) != len(*gPreds) {
			return fmt.Sprintf("at path %q: AND child count mismatch: want %d, got %d",
				path, len(*wPreds), len(*gPreds)), false
		}
		for i := range *wPreds {
			childPath := fmt.Sprintf("%s.and[%d]", path, i)
			if diff, ok := diffClaimPredicateTree(childPath, (*wPreds)[i], (*gPreds)[i]); !ok {
				return diff, false
			}
		}
		return "", true

	case xdr.ClaimPredicateTypeClaimPredicateOr:
		wPreds := want.OrPredicates
		gPreds := got.OrPredicates
		if wPreds == nil || gPreds == nil {
			if (wPreds == nil) != (gPreds == nil) {
				return fmt.Sprintf("at path %q: OR child list nil mismatch", path), false
			}
			return "", true
		}
		if len(*wPreds) != len(*gPreds) {
			return fmt.Sprintf("at path %q: OR child count mismatch: want %d, got %d",
				path, len(*wPreds), len(*gPreds)), false
		}
		for i := range *wPreds {
			childPath := fmt.Sprintf("%s.or[%d]", path, i)
			if diff, ok := diffClaimPredicateTree(childPath, (*wPreds)[i], (*gPreds)[i]); !ok {
				return diff, false
			}
		}
		return "", true
	}
	return "", true
}

func formatClaimPredicateType(t xdr.ClaimPredicateType) string {
	switch t {
	case xdr.ClaimPredicateTypeClaimPredicateUnconditional:
		return "UNCONDITIONAL"
	case xdr.ClaimPredicateTypeClaimPredicateAnd:
		return "AND"
	case xdr.ClaimPredicateTypeClaimPredicateOr:
		return "OR"
	case xdr.ClaimPredicateTypeClaimPredicateNot:
		return "NOT"
	case xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime:
		return "BEFORE_ABSOLUTE_TIME"
	case xdr.ClaimPredicateTypeClaimPredicateBeforeRelativeTime:
		return "BEFORE_RELATIVE_TIME"
	default:
		return t.String()
	}
}

// prettyClaimPredicateTree renders a human-readable indented view of a ClaimPredicate tree.
func prettyClaimPredicateTree(p xdr.ClaimPredicate, depth int) string {
	prefix := strings.Repeat("  ", depth)
	switch p.Type {
	case xdr.ClaimPredicateTypeClaimPredicateUnconditional:
		return prefix + "- unconditional"
	case xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime:
		if p.AbsBefore != nil {
			t := int64(*p.AbsBefore)
			return fmt.Sprintf("%s- before absolute time: %d (%s)", prefix, t, time.Unix(t, 0).UTC().Format(time.RFC3339))
		}
		return prefix + "- before absolute time (nil)"
	case xdr.ClaimPredicateTypeClaimPredicateBeforeRelativeTime:
		if p.RelBefore != nil {
			s := int64(*p.RelBefore)
			return fmt.Sprintf("%s- before relative time: %ds (%s)", prefix, s, (time.Duration(s) * time.Second).String())
		}
		return prefix + "- before relative time (nil)"
	case xdr.ClaimPredicateTypeClaimPredicateNot:
		if p.NotPredicate != nil && *p.NotPredicate != nil {
			return prefix + "- not:\n" + prettyClaimPredicateTree(**p.NotPredicate, depth+1)
		}
		return prefix + "- not (nil)"
	case xdr.ClaimPredicateTypeClaimPredicateAnd:
		if p.AndPredicates != nil {
			lines := []string{prefix + "- and:"}
			for _, child := range *p.AndPredicates {
				lines = append(lines, prettyClaimPredicateTree(child, depth+1))
			}
			return strings.Join(lines, "\n")
		}
		return prefix + "- and (nil)"
	case xdr.ClaimPredicateTypeClaimPredicateOr:
		if p.OrPredicates != nil {
			lines := []string{prefix + "- or:"}
			for _, child := range *p.OrPredicates {
				lines = append(lines, prettyClaimPredicateTree(child, depth+1))
			}
			return strings.Join(lines, "\n")
		}
		return prefix + "- or (nil)"
	default:
		return prefix + "- " + p.Type.String()
	}
}
