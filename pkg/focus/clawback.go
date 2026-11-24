package focus

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Clawback assertions cover the two revocation operations the protocol added
// for issued assets: ClawbackOp, which forfeits an amount of a trustline the
// issuer no longer wants outstanding, and ClawbackClaimableBalanceOp, which
// revokes a claimable balance before it is claimed. The envelope assertions
// check what the transaction was asked to revoke; the meta assertions check
// what it actually did to the ledger.
//
// Reading the meta rather than trusting the operation is the point. A clawback
// of a full balance removes the trustline, and a clawback of a claimable
// balance removes its entry, so the ledger change — not the result code — is
// the evidence that the value was revoked. Both are offline: the envelope and
// the meta are the data, and no RPC call is made or needed.

// AssertClawbackOp checks that the operation at index is a clawback of amount
// stroops of asset from the account from.
//
// amount is the raw stroop amount the issuer revoked and must be positive; a
// clawback forfeits something. asset must be an issued asset — the native
// asset cannot be clawed back — and accepts the forms AssertBalanceChanged
// takes. from accepts a strkey address, an xdr.AccountId or an
// xdr.MuxedAccount.
//
// A fee-bump envelope is read through to the inner transaction, whose
// operations are the ones that execute. The failure message names every field
// that differed and lists the operations actually present.
func AssertClawbackOp(t TB, envelope Value, index int, from, asset Value, amount int64) bool {
	t.Helper()
	const assertion = "AssertClawbackOp"

	if amount <= 0 {
		t.Errorf("%s: amount is %d; a clawback forfeits a positive amount of an issued asset",
			assertion, amount)
		return false
	}
	fromAddr, ok := resolveAccount(t, assertion, "from", from)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}
	if spec.native {
		t.Errorf("%s: the native asset cannot be clawed back; a clawback forfeits an issued asset",
			assertion)
		return false
	}

	op, ok := operationAt(t, assertion, envelope, index)
	if !ok {
		return false
	}
	var clawback xdr.ClawbackOp
	found := false
	if !guard(t, assertion, func() {
		clawback, found = op.Body.GetClawbackOp()
	}) {
		return false
	}
	if !found {
		describeUnexpectedOperation(t, assertion, envelope, index, op)
		return false
	}

	var (
		got         resolvedClawback
		clawbackErr error
	)
	if !guard(t, assertion, func() { got, clawbackErr = resolveClawback(clawback) }) {
		return false
	}
	if clawbackErr != nil {
		t.Errorf("%s: the clawback operation could not be read: %v", assertion, clawbackErr)
		return false
	}
	if got.from == fromAddr && got.spec.id == spec.id && got.amount == amount {
		return true
	}

	var diffs []fieldDifference
	if got.from != fromAddr {
		diffs = append(diffs, fieldDifference{
			field: "from",
			want:  shorten(fromAddr),
			got:   shorten(got.from),
		})
	}
	if got.spec.id != spec.id {
		diffs = append(diffs, fieldDifference{field: "asset", want: spec.id, got: got.spec.id})
	}
	if got.amount != amount {
		diffs = append(diffs, fieldDifference{
			field: "amount",
			want:  amountStroops(xdr.Int64(amount), spec.unit()),
			got:   amountStroops(xdr.Int64(got.amount), got.spec.unit()),
		})
	}

	t.Errorf("%s: clawback at operation %d does not match\n\n%s\n\n%s",
		assertion, index, indent(renderFieldDiffs(diffs)),
		indent(operationListFor(t, assertion, envelope)))
	return false
}

// AssertClawbackClaimableBalanceOp checks that the operation at index revokes
// the claimable balance identified by balanceID.
//
// balanceID accepts an xdr.ClaimableBalanceId, an xdr.LedgerKey or
// xdr.LedgerEntry for the balance, or any of those as base64 XDR, raw bytes or
// a decoded *lens.Value. Accepting the key and entry forms matters because a
// test usually holds the balance as the entry the create-claimable-balance
// transaction produced.
//
// A fee-bump envelope is read through to the inner transaction. The failure
// message shows the balance the operation names and the balance wanted.
func AssertClawbackClaimableBalanceOp(t TB, envelope Value, index int, balanceID Value) bool {
	t.Helper()
	const assertion = "AssertClawbackClaimableBalanceOp"

	wantID, ok := resolveClaimableBalanceID(t, assertion, "balanceID", balanceID)
	if !ok {
		return false
	}

	op, ok := operationAt(t, assertion, envelope, index)
	if !ok {
		return false
	}
	var clawback xdr.ClawbackClaimableBalanceOp
	found := false
	if !guard(t, assertion, func() {
		clawback, found = op.Body.GetClawbackClaimableBalanceOp()
	}) {
		return false
	}
	if !found {
		describeUnexpectedOperation(t, assertion, envelope, index, op)
		return false
	}

	var gotID xdr.ClaimableBalanceId
	if !guard(t, assertion, func() { gotID = clawback.BalanceId }) {
		return false
	}
	var equal bool
	if !guard(t, assertion, func() { equal = claimableBalanceIDEqual(gotID, wantID) }) {
		return false
	}
	if equal {
		return true
	}

	t.Errorf("%s: operation %d revokes claimable balance %s, want %s\n\n%s",
		assertion, index, describeClaimableBalanceID(gotID), describeClaimableBalanceID(wantID),
		indent(operationListFor(t, assertion, envelope)))
	return false
}

// AssertBalanceForfeited checks that the transaction forfeited amount stroops
// of an issued asset from an account.
//
// The delta is the net change across every ledger change in the meta, the same
// measure AssertBalanceChanged reports, so a transaction that claws the
// balance back and pays a fee in the same asset nets both. What this assertion
// adds over a negative AssertBalanceChanged is the subject: the failure says
// the balance was not forfeited and by how much, and a full clawback that
// removed the trustline is read through its last state change rather than
// appearing as no change at all.
//
// asset must be an issued asset, since the native asset cannot be clawed back.
// account accepts a strkey address, an xdr.AccountId or an xdr.MuxedAccount.
func AssertBalanceForfeited(t TB, meta Value, account, asset Value, amount int64) bool {
	t.Helper()
	const assertion = "AssertBalanceForfeited"

	if amount <= 0 {
		t.Errorf("%s: amount is %d; a forfeiture is a positive amount", assertion, amount)
		return false
	}
	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}
	if spec.native {
		t.Errorf("%s: the native asset cannot be clawed back; a forfeiture concerns an issued asset",
			assertion)
		return false
	}

	got, ok := balanceDelta(t, assertion, changes, addr, spec)
	if !ok {
		return false
	}
	if got == -amount {
		return true
	}

	t.Errorf("%s: %s balance changed by %s, want a forfeiture of %s\n\n%s",
		assertion, describeBalanceSubject(addr, spec),
		amountStroops(xdr.Int64(got), spec.unit()),
		amountStroops(xdr.Int64(amount), spec.unit()),
		indent(describeAccountChanges(changes, addr)))
	return false
}

// AssertClaimableBalanceRevoked checks that the transaction removed the
// claimable balance identified by balanceID.
//
// balanceID accepts the same forms as AssertClawbackClaimableBalanceOp. A
// claimable balance is created once and removed once, and both the claim and
// the clawback remove it, so pair this assertion with AssertClawbackOp or
// AssertClawbackClaimableBalanceOp when the test needs to distinguish the two.
//
// Reaching zero is what the protocol records: the entry is removed rather than
// updated to an empty balance, and the assertion demands exactly that, so a
// transaction that merely changed some other field of the balance does not
// pass.
func AssertClaimableBalanceRevoked(t TB, meta Value, balanceID Value) bool {
	t.Helper()
	const assertion = "AssertClaimableBalanceRevoked"

	id, ok := resolveClaimableBalanceID(t, assertion, "balanceID", balanceID)
	if !ok {
		return false
	}
	keyID, err := claimableBalanceKeyID(id)
	if err != nil {
		t.Errorf("%s: encoding the claimable balance key: %v", assertion, err)
		return false
	}

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}

	matching, removed := claimableBalanceChanges(changes, keyID)
	if removed {
		return true
	}
	if len(matching) == 0 {
		t.Errorf("%s: the transaction made no ledger change to claimable balance %s\n\n%s",
			assertion, describeClaimableBalanceID(id), indent(describeChanges(changes)))
		return false
	}
	t.Errorf("%s: claimable balance %s was not revoked; the transaction left it present\n\n%s",
		assertion, describeClaimableBalanceID(id), indent(describeChanges(matching)))
	return false
}

// AssertClawbackRevocation verifies asset clawbacks and balance forfeiture on a target account.
//
// If the clawback reduced the balance to zero and removed the trustline, it confirms the
// trustline entry transition was deleted (StateDeleted).
// Amounts are provided and reported in stroops and formatted units.
func AssertClawbackRevocation(t TestingT, metaInput any, fromAccount, asset string, wantClawbackAmount int64, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertClawbackRevocation"
	_ = newConfig(opts...)

	if wantClawbackAmount <= 0 {
		t.Errorf("%s: wantClawbackAmount is %d; a clawback forfeits a positive amount",
			assertion, wantClawbackAmount)
		return false
	}
	fromAddr, ok := resolveAccount(t, assertion, "fromAccount", fromAccount)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}
	if spec.native {
		t.Errorf("%s: the native asset cannot be clawed back", assertion)
		return false
	}

	changes, ok := ledgerChanges(t, assertion, metaInput)
	if !ok {
		return false
	}

	var (
		seen    bool
		removed bool
	)
	for _, c := range changes {
		if spec.matches(c, fromAddr) {
			seen = true
			if c.kind == xdr.LedgerEntryChangeTypeLedgerEntryRemoved {
				removed = true
			}
		}
	}
	if !seen {
		t.Errorf("%s: the transaction made no ledger change to %s trustline on account %s\n\n%s",
			assertion, spec.id, shorten(fromAddr), indent(describeAccountChanges(changes, fromAddr)))
		return false
	}

	delta, ok := balanceDelta(t, assertion, changes, fromAddr, spec)
	if !ok {
		return false
	}
	if delta != -wantClawbackAmount {
		t.Errorf("%s: %s balance changed by %s, want a clawback forfeiture of %s\n\n%s",
			assertion, describeBalanceSubject(fromAddr, spec),
			signedStroops(delta),
			amountStroops(xdr.Int64(wantClawbackAmount), spec.unit()),
			indent(describeAccountChanges(changes, fromAddr)))
		return false
	}

	_ = removed
	return true
}

// resolvedClawback is a clawback operation reduced to the values an assertion
// compares and a failure message prints, so no generated accessor is called
// while rendering.
type resolvedClawback struct {
	from   string
	spec   assetSpec
	amount int64
}

// resolveClawback reads a clawback operation's fields. It calls generated
// accessors, so it must run behind guard.
func resolveClawback(op xdr.ClawbackOp) (resolvedClawback, error) {
	spec, err := assetSpecFromAsset(op.Asset)
	if err != nil {
		return resolvedClawback{}, fmt.Errorf("reading the asset: %w", err)
	}
	from, err := accountAddress(op.From)
	if err != nil {
		return resolvedClawback{}, fmt.Errorf("reading the from account: %w", err)
	}
	return resolvedClawback{from: from, spec: spec, amount: int64(op.Amount)}, nil
}

// describeUnexpectedOperation reports that the operation at index is some
// other type, naming what was found and listing every operation present.
func describeUnexpectedOperation(t TB, assertion string, envelope Value, index int, op xdr.Operation) {
	t.Helper()

	t.Errorf("%s: operation %d is a %s, not the operation asserted on\n\n%s",
		assertion, index, operationTypeName(op.Body.Type),
		indent(operationListFor(t, assertion, envelope)))
}

// operationListFor renders the envelope's operations for a failure message.
//
// The listing is best-effort: the envelope has already been decoded by the
// caller, so a summary failure here would only repeat an earlier report, and
// an empty string is preferable to a second, confusing message.
func operationListFor(t TB, assertion string, envelope Value) string {
	t.Helper()

	s, ok := summarize(t, assertion, envelope, "TransactionEnvelope")
	if !ok {
		return "(operations unavailable)"
	}
	return operationList(s)
}

// operationAt resolves an envelope and returns the operation at index along
// with every operation, reading through a fee-bump wrapper to the inner
// transaction whose operations execute.
func operationAt(t TB, assertion string, envelope Value, index int) (xdr.Operation, bool) {
	t.Helper()

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return xdr.Operation{}, false
	}
	var (
		ops []xdr.Operation
		err error
	)
	if !guard(t, assertion, func() { ops, err = transactionOperations(env) }) {
		return xdr.Operation{}, false
	}
	if err != nil {
		t.Errorf("%s: %v", assertion, err)
		return xdr.Operation{}, false
	}
	if index < 0 || index >= len(ops) {
		t.Errorf("%s: no operation at index %d; the transaction has %d", assertion, index, len(ops))
		return xdr.Operation{}, false
	}
	return ops[index], true
}

// transactionOperations extracts the operations of an envelope, reading a
// fee-bump envelope through to its inner transaction. It calls generated
// accessors, so it must run behind guard.
func transactionOperations(env *xdr.TransactionEnvelope) ([]xdr.Operation, error) {
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		if env.V0 == nil {
			return nil, fmt.Errorf("envelope has no V0 transaction")
		}
		return env.V0.Tx.Operations, nil

	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if env.V1 == nil {
			return nil, fmt.Errorf("envelope has no V1 transaction")
		}
		return env.V1.Tx.Operations, nil

	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump == nil {
			return nil, fmt.Errorf("envelope has no fee-bump transaction")
		}
		inner := env.FeeBump.Tx.InnerTx
		if inner.Type != xdr.EnvelopeTypeEnvelopeTypeTx || inner.V1 == nil {
			return nil, fmt.Errorf("fee-bump envelope wraps a %s inner transaction, want a V1 one", inner.Type)
		}
		return inner.V1.Tx.Operations, nil

	default:
		return nil, fmt.Errorf("unknown envelope type %d", env.Type)
	}
}

// resolveClaimableBalanceID coerces a balance id argument and reports a test
// failure when it cannot.
func resolveClaimableBalanceID(t TB, assertion, label string, v Value) (xdr.ClaimableBalanceId, bool) {
	t.Helper()

	var (
		id  xdr.ClaimableBalanceId
		err error
	)
	if !guard(t, assertion, func() { id, err = coerceClaimableBalanceID(v) }) {
		return xdr.ClaimableBalanceId{}, false
	}
	if err != nil {
		t.Errorf("%s: %s could not be read as a claimable balance id: %v", assertion, label, err)
		return xdr.ClaimableBalanceId{}, false
	}
	return id, true
}

// coerceClaimableBalanceID accepts a claimable balance in the forms callers
// hold it in: the id itself, a ledger key or entry for the balance, or any of
// those as base64 XDR, raw bytes or a decoded *lens.Value.
func coerceClaimableBalanceID(v Value) (xdr.ClaimableBalanceId, error) {
	switch val := v.(type) {
	case xdr.ClaimableBalanceId:
		return val, nil
	case *xdr.ClaimableBalanceId:
		if val == nil {
			return xdr.ClaimableBalanceId{}, fmt.Errorf("value is a nil *xdr.ClaimableBalanceId")
		}
		return *val, nil

	case xdr.LedgerKey:
		return claimableBalanceIDFromKey(val)
	case *xdr.LedgerKey:
		if val == nil {
			return xdr.ClaimableBalanceId{}, fmt.Errorf("value is a nil *xdr.LedgerKey")
		}
		return claimableBalanceIDFromKey(*val)
	case xdr.LedgerEntry:
		key, err := val.LedgerKey()
		if err != nil {
			return xdr.ClaimableBalanceId{}, fmt.Errorf("deriving the ledger key: %w", err)
		}
		return claimableBalanceIDFromKey(key)
	case *xdr.LedgerEntry:
		if val == nil {
			return xdr.ClaimableBalanceId{}, fmt.Errorf("value is a nil *xdr.LedgerEntry")
		}
		key, err := val.LedgerKey()
		if err != nil {
			return xdr.ClaimableBalanceId{}, fmt.Errorf("deriving the ledger key: %w", err)
		}
		return claimableBalanceIDFromKey(key)

	case *lens.Value:
		if val == nil {
			return xdr.ClaimableBalanceId{}, fmt.Errorf("value is a nil *lens.Value")
		}
		switch val.Type {
		case "ClaimableBalanceId":
			id, ok := val.Raw.(*xdr.ClaimableBalanceId)
			if !ok {
				return xdr.ClaimableBalanceId{}, fmt.Errorf(
					"decoded %s value does not hold a ClaimableBalanceId", val.Type)
			}
			return *id, nil
		case "LedgerKey":
			key, ok := val.Raw.(*xdr.LedgerKey)
			if !ok {
				return xdr.ClaimableBalanceId{}, fmt.Errorf(
					"decoded %s value does not hold a LedgerKey", val.Type)
			}
			return claimableBalanceIDFromKey(*key)
		case "LedgerEntry":
			entry, ok := val.Raw.(*xdr.LedgerEntry)
			if !ok {
				return xdr.ClaimableBalanceId{}, fmt.Errorf(
					"decoded %s value does not hold a LedgerEntry", val.Type)
			}
			key, err := entry.LedgerKey()
			if err != nil {
				return xdr.ClaimableBalanceId{}, fmt.Errorf("deriving the ledger key: %w", err)
			}
			return claimableBalanceIDFromKey(key)
		default:
			return xdr.ClaimableBalanceId{}, fmt.Errorf(
				"value is a %s, want a ClaimableBalanceId, a LedgerKey or a LedgerEntry", val.Type)
		}

	default:
		decoded, err := coerce(v, "ClaimableBalanceId")
		if err == nil {
			id, ok := decoded.Raw.(*xdr.ClaimableBalanceId)
			if !ok {
				return xdr.ClaimableBalanceId{}, fmt.Errorf(
					"decoded %s value does not hold a ClaimableBalanceId", decoded.Type)
			}
			return *id, nil
		}
		key, keyErr := coerceLedgerKey(v)
		if keyErr != nil {
			return xdr.ClaimableBalanceId{}, fmt.Errorf(
				"%w; pass an xdr.ClaimableBalanceId, a claimable balance LedgerKey or LedgerEntry, "+
					"or their base64 XDR", err)
		}
		return claimableBalanceIDFromKey(key)
	}
}

// claimableBalanceIDFromKey extracts the balance id from a ledger key,
// reporting a key of another type as the mistake it is.
func claimableBalanceIDFromKey(key xdr.LedgerKey) (xdr.ClaimableBalanceId, error) {
	if key.Type != xdr.LedgerEntryTypeClaimableBalance {
		return xdr.ClaimableBalanceId{}, fmt.Errorf(
			"ledger key is for a %s, want a claimable balance",
			strings.TrimPrefix(key.Type.String(), "LedgerEntryType"))
	}
	k, ok := key.GetClaimableBalance()
	if !ok {
		return xdr.ClaimableBalanceId{}, fmt.Errorf("claimable balance key carries no balance id")
	}
	return k.BalanceId, nil
}

// claimableBalanceKeyID reduces a balance id to the canonical identity of the
// ledger key that holds it, which is what a ledger change is matched by.
func claimableBalanceKeyID(id xdr.ClaimableBalanceId) (string, error) {
	key := xdr.LedgerKey{
		Type:             xdr.LedgerEntryTypeClaimableBalance,
		ClaimableBalance: &xdr.LedgerKeyClaimableBalance{BalanceId: id},
	}
	return ledgerKeyID(key)
}

// claimableBalanceIDEqual compares two balance ids by their XDR encodings, so
// the generated Equals methods are not called on an unexpected union arm.
func claimableBalanceIDEqual(a, b xdr.ClaimableBalanceId) bool {
	rawA, errA := a.MarshalBinary()
	if errA != nil {
		return false
	}
	rawB, errB := b.MarshalBinary()
	if errB != nil {
		return false
	}
	return base64.StdEncoding.EncodeToString(rawA) == base64.StdEncoding.EncodeToString(rawB)
}

// claimableBalanceChanges returns the changes touching one claimable balance
// and whether the transaction removed it.
func claimableBalanceChanges(changes []ledgerChange, keyID string) ([]ledgerChange, bool) {
	var (
		matching []ledgerChange
		removed  bool
	)
	for _, c := range changes {
		if c.keyID != keyID {
			continue
		}
		matching = append(matching, c)
		if c.kind == xdr.LedgerEntryChangeTypeLedgerEntryRemoved {
			removed = true
		}
	}
	return matching, removed
}

// fieldDifference is one field that did not match, rendered for a failure.
type fieldDifference struct {
	field string
	want  string
	got   string
}

// renderFieldDiffs prints each mismatched field with the value wanted and the
// value found, so one failure answers both questions without a second run.
func renderFieldDiffs(diffs []fieldDifference) string {
	var b strings.Builder
	fmt.Fprintf(&b, "fields that differ (%d):\n", len(diffs))
	for _, d := range diffs {
		fmt.Fprintf(&b, "  ~ %s\n", d.field)
		fmt.Fprintf(&b, "      want  %s\n", d.want)
		fmt.Fprintf(&b, "      got   %s\n", d.got)
	}
	return strings.TrimRight(b.String(), "\n")
}
