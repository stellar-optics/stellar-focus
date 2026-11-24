package focus

import "github.com/stellar/go-stellar-sdk/xdr"

// AssertRevokeSponsorship checks the revocation arm and an optional caller matcher.
func AssertRevokeSponsorship(t TB, op xdr.Operation, wantType xdr.RevokeSponsorshipType, matcher func(xdr.RevokeSponsorshipOp) bool, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertRevokeSponsorship"
	if op.Body.Type != xdr.OperationTypeRevokeSponsorship || op.Body.RevokeSponsorshipOp == nil {
		t.Errorf("%s: operation is %s, want RevokeSponsorship", assertion, operationTypeName(op.Body.Type))
		return false
	}
	got := op.Body.RevokeSponsorshipOp
	if got.Type != wantType || (matcher != nil && !matcher(*got)) {
		t.Errorf("%s: expected sponsorship type %s, got %s", assertion, wantType, got.Type)
		return false
	}
	_ = opts
	return true
}

// AssertRevokeAccountSponsorship checks that an account ledger entry is targeted.
func AssertRevokeAccountSponsorship(t TB, op xdr.Operation, accountID string, opts ...Option) bool {
	want, err := xdr.AddressToAccountId(accountID)
	if err != nil {
		t.Helper()
		t.Errorf("AssertRevokeAccountSponsorship: account ID could not be read: %v", err)
		return false
	}
	return AssertRevokeSponsorship(t, op, xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry, func(r xdr.RevokeSponsorshipOp) bool {
		if r.LedgerKey == nil || r.LedgerKey.Type != xdr.LedgerEntryTypeAccount || r.LedgerKey.Account == nil {
			return false
		}
		return r.LedgerKey.Account.AccountId.Equals(want)
	}, opts...)
}

// AssertRevokeSignerSponsorship checks that a signer on an account is targeted.
func AssertRevokeSignerSponsorship(t TB, op xdr.Operation, accountID string, signerKey xdr.SignerKey, opts ...Option) bool {
	want, err := xdr.AddressToAccountId(accountID)
	if err != nil {
		t.Helper()
		t.Errorf("AssertRevokeSignerSponsorship: account ID could not be read: %v", err)
		return false
	}
	return AssertRevokeSponsorship(t, op, xdr.RevokeSponsorshipTypeRevokeSponsorshipSigner, func(r xdr.RevokeSponsorshipOp) bool {
		if r.Signer == nil {
			return false
		}
		return r.Signer.AccountId.Equals(want) && r.Signer.SignerKey.Equals(signerKey)
	}, opts...)
}
