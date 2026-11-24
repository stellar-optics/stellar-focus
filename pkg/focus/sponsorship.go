package focus

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Sponsorship assertions identify *who* pays the reserve for a ledger entry.
//
// The protocol records sponsorship in the entry itself: a sponsored entry
// carries the sponsor's account id in its LedgerEntryExtensionV1.SponsoringID,
// and a transaction that creates, transfers or revokes a sponsorship leaves
// that transition in its meta as ordinary ledger entry changes. Reading the
// transition rather than the operation matters, because a revoke-sponsorship
// operation can transfer, establish or revoke a sponsorship depending on the
// entry's state, and the result code says only that the operation succeeded.
//
// Everything here is offline: the meta is the evidence, so no RPC call, ledger
// snapshot or clock is involved, and the same meta gives the same answer every
// run.

// AssertEntrySponsoredBy checks that, after the transaction, the ledger entry
// identified by targetEntry is sponsored by sponsorAccount.
//
// targetEntry accepts the same forms as the entry assertions: an xdr.LedgerKey,
// an xdr.LedgerEntry, or either as base64 XDR, raw bytes or a decoded
// *lens.Value. sponsorAccount accepts a strkey account address, an
// xdr.AccountId or an xdr.MuxedAccount. The sponsor identity is strict — an
// empty or malformed address is reported as an input error, not as a
// mismatch — because "sponsored by nobody in particular" is not a property a
// test should be able to assert by accident.
//
// The entry must have been created, modified or restored by the transaction.
// A removed entry carries no sponsorship to inspect, and a transaction that
// never touched the entry says nothing about who sponsors it; both are
// reported as such rather than treated as a missing sponsor.
//
// A failure names the sponsorship transition the transaction made — for
// example "sponsorship transferred from GA… to GB…" — so "wrong sponsor" and
// "no sponsor at all" never read the same.
func AssertEntrySponsoredBy(t TB, meta Value, targetEntry, sponsorAccount Value) bool {
	t.Helper()
	const assertion = "AssertEntrySponsoredBy"

	want, ok := resolveAccount(t, assertion, "sponsorAccount", sponsorAccount)
	if !ok {
		return false
	}
	key, ok := resolveLedgerKey(t, assertion, targetEntry)
	if !ok {
		return false
	}
	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}

	var (
		entry   *xdr.LedgerEntry
		before  sponsorshipState
		seen    bool
		removed bool
	)
	if !guard(t, assertion, func() {
		for _, c := range changes {
			if c.keyID != key.keyID {
				continue
			}
			seen = true
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if state, has := entrySponsorship(c.entry); has {
					before = state
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated,
				xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				entry = c.entry
				removed = false
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				removed = true
			}
		}
	}) {
		return false
	}

	if !seen {
		t.Errorf("%s: the transaction made no ledger change to %s\n\n%s",
			assertion, key.label, indent(describeChanges(changes)))
		return false
	}
	if removed || entry == nil {
		t.Errorf("%s: the transaction removed %s, which carries no sponsorship to inspect\n\n%s",
			assertion, key.label, indent(describeEntryChanges(changes, key.keyID)))
		return false
	}

	var after sponsorshipState
	if !guard(t, assertion, func() { after, _ = entrySponsorship(entry) }) {
		return false
	}
	if after.sponsored && after.sponsor == want {
		return true
	}

	t.Errorf("%s: %s is %s, want sponsored by %s\n\n  %s\n\n%s",
		assertion, key.label, after.describe(), shorten(want),
		describeSponsorshipTransition(before, after),
		indent(describeEntryChanges(changes, key.keyID)))
	return false
}

// sponsorshipState is the sponsorship an entry carries at one point in the
// meta: whether it is sponsored, and by whom.
type sponsorshipState struct {
	sponsored bool
	sponsor   string
}

// describe names the state for a failure message.
func (s sponsorshipState) describe() string {
	if !s.sponsored {
		return "not sponsored"
	}
	return "sponsored by " + shorten(s.sponsor)
}

// entrySponsorship reads an entry's sponsor from its extension. It calls
// generated accessors, so it must run behind guard.
//
// The V1 extension with a nil SponsoringID is the protocol's way of spelling
// "this entry is not sponsored", and is distinct from an entry carrying no V1
// extension at all; both are reported as unsponsored, because no test can act
// on the difference.
func entrySponsorship(entry *xdr.LedgerEntry) (sponsorshipState, bool) {
	if entry == nil {
		return sponsorshipState{}, false
	}
	ext, ok := entry.Ext.GetV1()
	if !ok || ext.SponsoringId == nil {
		return sponsorshipState{}, true
	}
	addr, err := ext.SponsoringId.GetAddress()
	if err != nil {
		return sponsorshipState{}, true
	}
	return sponsorshipState{sponsored: true, sponsor: addr}, true
}

// describeSponsorshipTransition names what the transaction did to the
// sponsorship, so the failure distinguishes a transfer from an establishment
// or a revocation.
func describeSponsorshipTransition(before, after sponsorshipState) string {
	switch {
	case before.sponsored && after.sponsored && before.sponsor != after.sponsor:
		return fmt.Sprintf("sponsorship transferred from %s to %s",
			shorten(before.sponsor), shorten(after.sponsor))
	case !before.sponsored && after.sponsored:
		return "sponsorship established by " + shorten(after.sponsor)
	case before.sponsored && !after.sponsored:
		return "sponsorship revoked from " + shorten(before.sponsor)
	case before.sponsored && after.sponsored:
		return "sponsorship unchanged (" + after.describe() + ")"
	default:
		return "the entry remains unsponsored"
	}
}

// describeEntryChanges renders only the changes touching one entry, so a
// sponsorship failure shows the state transitions for the entry in question
// rather than the whole meta.
func describeEntryChanges(changes []ledgerChange, keyID string) string {
	matching := make([]ledgerChange, 0, len(changes))
	for _, c := range changes {
		if c.keyID == keyID {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return "no ledger entry change in this meta touches that entry"
	}
	return describeChanges(matching)
}

// AssertSponsorshipTransition verifies that a ledger entry matching targetKey underwent
// a sponsorship transition from wantPrevSponsor to wantNewSponsor.
//
// An empty string or "none" for wantPrevSponsor asserts that the entry was previously unsponsored.
// An empty string or "none" for wantNewSponsor asserts that the entry's sponsorship was revoked (now unsponsored).
func AssertSponsorshipTransition(t TestingT, metaInput any, targetKey string, wantPrevSponsor, wantNewSponsor string, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertSponsorshipTransition"
	_ = newConfig(opts...)

	key, hasKey := resolveLedgerKey(t, assertion, targetKey)
	changes, ok := ledgerChanges(t, assertion, metaInput)
	if !ok {
		return false
	}

	normalizeSponsor := func(s string) (string, bool) {
		s = strings.TrimSpace(s)
		if s == "" || strings.EqualFold(s, "none") {
			return "", true
		}
		addr, ok := resolveAccount(t, assertion, "sponsor", s)
		return addr, ok
	}

	expectedPrev, ok := normalizeSponsor(wantPrevSponsor)
	if !ok {
		return false
	}
	expectedNew, ok := normalizeSponsor(wantNewSponsor)
	if !ok {
		return false
	}

	var (
		before sponsorshipState
		after  sponsorshipState
		seen   bool
		keyID  string
	)
	if hasKey {
		keyID = key.keyID
	} else {
		keyID = targetKey
	}

	if !guard(t, assertion, func() {
		for _, c := range changes {
			matches := (hasKey && c.keyID == key.keyID) || c.keyID == targetKey || c.account == targetKey
			if !matches {
				continue
			}
			seen = true
			if c.keyID != "" {
				keyID = c.keyID
			}
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if state, has := entrySponsorship(c.entry); has {
					before = state
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated,
				xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				if state, has := entrySponsorship(c.entry); has {
					after = state
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				after = sponsorshipState{sponsored: false}
			}
		}
	}) {
		return false
	}

	if !seen {
		label := targetKey
		if hasKey {
			label = key.label
		}
		t.Errorf("%s: the transaction made no ledger change to %s\n\n%s",
			assertion, label, indent(describeChanges(changes)))
		return false
	}

	actualPrev := ""
	if before.sponsored {
		actualPrev = before.sponsor
	}
	actualNew := ""
	if after.sponsored {
		actualNew = after.sponsor
	}

	if actualPrev == expectedPrev && actualNew == expectedNew {
		return true
	}

	label := targetKey
	if hasKey {
		label = key.label
	}

	sponsorLabel := func(s string) string {
		if s == "" {
			return "none"
		}
		return shorten(s)
	}

	t.Errorf("%s: sponsorship transition for %s does not match\n\n"+
		"  got:  prev %s, new %s (%s)\n"+
		"  want: prev %s, new %s\n\n%s",
		assertion, label,
		sponsorLabel(actualPrev), sponsorLabel(actualNew), describeSponsorshipTransition(before, after),
		sponsorLabel(expectedPrev), sponsorLabel(expectedNew),
		indent(describeEntryChanges(changes, keyID)))
	return false
}

// AssertRevokeSponsorship checks the type and target of a revoke-sponsorship
// operation. A nil matcher checks only the operation type.
func AssertRevokeSponsorship(
	t TestingT, op xdr.Operation, wantType xdr.RevokeSponsorshipType,
	matcher func(xdr.RevokeSponsorshipOp) bool, opts ...Option,
) bool {
	t.Helper()
	const assertion = "AssertRevokeSponsorship"
	_ = newConfig(opts...)

	if op.Body.Type != xdr.OperationTypeRevokeSponsorship || op.Body.RevokeSponsorshipOp == nil {
		t.Errorf("%s: expected sponsorship type %s, got operation %s (sponsorship type unavailable)",
			assertion, revokeSponsorshipTypeName(wantType), operationTypeName(op.Body.Type))
		return false
	}
	actual := *op.Body.RevokeSponsorshipOp
	if actual.Type != wantType {
		t.Errorf("%s: sponsorship type is %s, want %s",
			assertion, revokeSponsorshipTypeName(actual.Type), revokeSponsorshipTypeName(wantType))
		return false
	}
	if matcher != nil && !matcher(actual) {
		t.Errorf("%s: revocation target did not match; expected sponsorship type %s, got %s",
			assertion, revokeSponsorshipTypeName(wantType), revokeSponsorshipTypeName(actual.Type))
		return false
	}
	return true
}

// AssertRevokeAccountSponsorship checks that an operation revokes sponsorship
// for accountID.
func AssertRevokeAccountSponsorship(t TestingT, op xdr.Operation, accountID string, opts ...Option) bool {
	t.Helper()
	account, err := xdr.AddressToAccountId(accountID)
	if err != nil {
		t.Errorf("AssertRevokeAccountSponsorship: account ID could not be read: %v", err)
		return false
	}
	key := xdr.LedgerKey{
		Type:    xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{AccountId: account},
	}
	return AssertRevokeSponsorship(t, op, xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry,
		func(actual xdr.RevokeSponsorshipOp) bool {
			got, ok := actual.GetLedgerKey()
			return ok && got.Equals(key)
		}, opts...)
}

// AssertRevokeSignerSponsorship checks that an operation revokes sponsorship
// for the account's signer key.
func AssertRevokeSignerSponsorship(
	t TestingT, op xdr.Operation, accountID string, signerKey xdr.SignerKey, opts ...Option,
) bool {
	t.Helper()
	account, err := xdr.AddressToAccountId(accountID)
	if err != nil {
		t.Errorf("AssertRevokeSignerSponsorship: account ID could not be read: %v", err)
		return false
	}
	return AssertRevokeSponsorship(t, op, xdr.RevokeSponsorshipTypeRevokeSponsorshipSigner,
		func(actual xdr.RevokeSponsorshipOp) bool {
			signer, ok := actual.GetSigner()
			return ok && signer.AccountId.Equals(account) && signer.SignerKey.Equals(signerKey)
		}, opts...)
}

func revokeSponsorshipTypeName(value xdr.RevokeSponsorshipType) string {
	switch value {
	case xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry:
		return "REVOKE_SPONSORSHIP_LEDGER_ENTRY"
	case xdr.RevokeSponsorshipTypeRevokeSponsorshipSigner:
		return "REVOKE_SPONSORSHIP_SIGNER"
	default:
		return value.String()
	}
}
