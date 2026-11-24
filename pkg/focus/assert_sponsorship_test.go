package focus

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestAssertRevokeSponsorshipTypes(t *testing.T) {
	targets := []xdr.LedgerEntryType{
		xdr.LedgerEntryTypeAccount,
		xdr.LedgerEntryTypeTrustline,
		xdr.LedgerEntryTypeOffer,
		xdr.LedgerEntryTypeData,
		xdr.LedgerEntryTypeClaimableBalance,
	}
	for _, target := range targets {
		t.Run(target.String(), func(t *testing.T) {
			key := xdr.LedgerKey{Type: target}
			revoke := &xdr.RevokeSponsorshipOp{Type: xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry, LedgerKey: &key}
			op := xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeRevokeSponsorship, RevokeSponsorshipOp: revoke}}
			if !AssertRevokeSponsorship(t, op, revoke.Type, func(got xdr.RevokeSponsorshipOp) bool { return got.LedgerKey != nil && got.LedgerKey.Type == target }) {
				t.Fatal("expected revocation type to match")
			}
		})
	}
	t.Run("signer", func(t *testing.T) {
		revoke := &xdr.RevokeSponsorshipOp{Type: xdr.RevokeSponsorshipTypeRevokeSponsorshipSigner}
		op := xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeRevokeSponsorship, RevokeSponsorshipOp: revoke}}
		if !AssertRevokeSponsorship(t, op, revoke.Type, nil) {
			t.Fatal("expected signer revocation type to match")
		}
	})
}
