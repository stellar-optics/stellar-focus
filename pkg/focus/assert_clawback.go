package focus

import "github.com/stellar/go-stellar-sdk/xdr"

// AssertClawbackClaimableBalanceMeta verifies that meta records removal of
// the specified claimable balance as part of a clawback.
func AssertClawbackClaimableBalanceMeta(t TB, meta xdr.TransactionMeta, wantBalanceID xdr.ClaimableBalanceId, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertClawbackClaimableBalanceMeta"
	name := newConfig(opts...).assertionName(assertion)
	keyID, err := claimableBalanceKeyID(wantBalanceID)
	if err != nil {
		t.Errorf("%s: encoding the claimable balance key: %v", name, err)
		return false
	}
	changes, ok := ledgerChanges(t, name, meta)
	if !ok {
		return false
	}
	matching, removed := claimableBalanceChanges(changes, keyID)
	if removed {
		return true
	}
	if len(matching) == 0 {
		t.Errorf("%s: the transaction made no ledger change to claimable balance %s\n\n%s",
			name, describeClaimableBalanceID(wantBalanceID), indent(describeChanges(changes)))
		return false
	}
	t.Errorf("%s: claimable balance %s was not revoked; the transaction left it present\n\n%s",
		name, describeClaimableBalanceID(wantBalanceID), indent(describeChanges(matching)))
	return false
}
