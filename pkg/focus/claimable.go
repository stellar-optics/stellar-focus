package focus

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Claimable balance assertions read the two ends of a claimable balance's
// life out of a transaction meta: the entry a create-claimable-balance
// operation puts in the ledger, and its removal when a claimant claims it.
//
// Neither end is visible in the envelope. The balance id is derived by the
// protocol from the operation and the transaction's source account and
// sequence number, so a test that only inspects what it submitted cannot name
// the balance it created — the meta is the first place the id exists. The
// amount is worth checking there for the same reason: the operation says what
// was asked for, the entry says what the ledger now holds.
//
// Both assertions read the flattened changes the entry assertions use
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
			indent(describeChanges(changes), "  "))
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
			indent(describeClaimableBalances(created), "  "))
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
			indent(describeClaimableBalances(created), "  "))
		return false
	}
	t.Errorf("%s: account %s can claim %s, holding %s; want one of %s\n\n%s",
		assertion, shorten(addr), pluralBalances(len(forClaimant)),
		strings.Join(balanceAmounts(forClaimant), ", "),
		amountStroops(xdr.Int64(amount), ""),
		indent(describeClaimableBalances(created), "  "))
	return false
}

// AssertClaimableBalanceClaimed checks that the transaction removed the
// claimable balance identified by balanceID, which is what claiming one does.
//
// balanceID accepts the balance id as hex — with or without the four-byte
// discriminant the XDR carries, so both the 64- and 72-character forms work —
// an xdr.ClaimableBalanceId, an xdr.LedgerKey or an xdr.LedgerEntry for the
// balance, or the XDR of any of those as base64, raw bytes or a decoded
// *lens.Value.
//
// Clawing a balance back removes the entry too. The two are the same ledger
// change and the protocol distinguishes them only by the operation that
// caused it, so this assertion is about the entry being gone; pair it with an
// operation-result assertion when which operation removed it matters.
func AssertClaimableBalanceClaimed(t TB, meta Value, balanceID Value) bool {
	t.Helper()
	const assertion = "AssertClaimableBalanceClaimed"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	want, ok := resolveClaimableBalanceID(t, assertion, balanceID)
	if !ok {
		return false
	}

	var present bool
	for _, c := range changes {
		if c.entryType != xdr.LedgerEntryTypeClaimableBalance || c.id != want {
			continue
		}
		if c.kind == xdr.LedgerEntryChangeTypeLedgerEntryRemoved {
			return true
		}
		present = true
	}

	if present {
		t.Errorf("%s: claimable balance %s was touched but not removed; "+
			"claiming a balance deletes its entry\n\n%s",
			assertion, shortenID(want), indent(describeClaimableBalanceChanges(changes), "  "))
		return false
	}
	t.Errorf("%s: no claimable balance %s was removed by this transaction\n\n%s",
		assertion, shortenID(want), indent(describeClaimableBalanceChanges(changes), "  "))
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

// resolveClaimableBalanceID coerces a balance id argument to its hex form and
// reports a test failure when it cannot.
func resolveClaimableBalanceID(t TB, assertion string, v Value) (string, bool) {
	t.Helper()

	var (
		id  string
		err error
	)
	if !guard(t, assertion, func() { id, err = claimableBalanceIDOf(v) }) {
		return "", false
	}
	if err != nil {
		t.Errorf("%s: balance id could not be read: %v", assertion, err)
		return "", false
	}
	return id, true
}

// claimableBalanceIDOf accepts a balance id in the forms callers hold it in.
func claimableBalanceIDOf(v Value) (string, error) {
	switch val := v.(type) {
	case string:
		return claimableBalanceIDFromHex(val)
	case xdr.ClaimableBalanceId:
		return describeClaimableBalanceID(val), nil
	case *xdr.ClaimableBalanceId:
		if val == nil {
			return "", fmt.Errorf("value is a nil *xdr.ClaimableBalanceId")
		}
		return describeClaimableBalanceID(*val), nil
	case xdr.Hash:
		return hex.EncodeToString(val[:]), nil
	default:
		// Fall back to the shared key coercion, so a LedgerKey, a LedgerEntry
		// or either as XDR identifies the balance the same way it does
		// everywhere else in the package.
		key, err := coerceLedgerKey(v)
		if err != nil {
			return "", fmt.Errorf(
				"%w; pass the balance id as hex, an xdr.ClaimableBalanceId, "+
					"or the LedgerKey of the balance", err)
		}
		k, ok := key.GetClaimableBalance()
		if !ok {
			return "", fmt.Errorf("value identifies a %s entry, not a claimable balance",
				strings.TrimPrefix(key.Type.String(), "LedgerEntryType"))
		}
		return describeClaimableBalanceID(k.BalanceId), nil
	}
}

// claimableBalanceIDFromHex accepts the two hex spellings of a balance id.
//
// The XDR is a four-byte union discriminant followed by a 32-byte hash, and
// both the 72-character encoding of the whole thing and the bare 64-character
// hash are in circulation. The hash is what identifies the balance, so the
// discriminant is dropped when it is present and names the V0 arm.
func claimableBalanceIDFromHex(s string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "0x")))
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return "", fmt.Errorf("%q is not hex: %w", s, err)
	}
	switch len(raw) {
	case 32:
		return trimmed, nil
	case 36:
		var id xdr.ClaimableBalanceId
		if err := id.UnmarshalBinary(raw); err != nil {
			return "", fmt.Errorf("%q is not a claimable balance id: %w", s, err)
		}
		return describeClaimableBalanceID(id), nil
	default:
		return "", fmt.Errorf(
			"%q is %d bytes of hex; a claimable balance id is 32 bytes (64 characters), "+
				"or 36 with its XDR discriminant", s, len(raw))
	}
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

// describeClaimableBalanceChanges lists only the changes touching claimable
// balances, so a claim failure names what happened to them rather than to the
// whole ledger.
func describeClaimableBalanceChanges(changes []ledgerChange) string {
	matching := make([]ledgerChange, 0, len(changes))
	for _, c := range changes {
		if c.entryType == xdr.LedgerEntryTypeClaimableBalance {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return "no ledger entry change in this meta touches a claimable balance"
	}
	return describeChanges(matching)
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
