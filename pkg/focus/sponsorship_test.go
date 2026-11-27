package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// sponsoredAccountEntryValue builds an account entry carrying a sponsor in its
// V1 extension, the way the protocol records sponsorship.
func sponsoredAccountEntryValue(t *testing.T, sponsor string) xdr.LedgerEntry {
	t.Helper()

	entry := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
	aid := mustAccountID(t, sponsor)
	entry.Ext = xdr.LedgerEntryExt{
		V:  1,
		V1: &xdr.LedgerEntryExtensionV1{SponsoringId: &aid},
	}
	return entry
}

func TestAssertEntrySponsoredBy(t *testing.T) {
	t.Parallel()

	sponsored := sponsoredAccountEntryValue(t, testSource)
	key := ledgerKeyOf(t, sponsored)
	_, lensKey, err := focus.Encode(key, "LedgerKey")
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}
	unsponsored := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))

	tests := []struct {
		name    string
		meta    string
		target  focus.Value
		sponsor focus.Value
	}{
		{
			"created with a sponsor",
			metaV2(t, []xdr.LedgerEntryChange{createdChange(sponsored)}),
			key, testSource,
		},
		{
			"sponsorship established over an existing entry",
			metaV2(t, []xdr.LedgerEntryChange{stateChange(unsponsored), updatedChange(sponsored)}),
			key, testSource,
		},
		{
			"sponsorship transferred to another account",
			metaV2(t, []xdr.LedgerEntryChange{
				stateChange(sponsoredAccountEntryValue(t, testIssuer)),
				updatedChange(sponsored),
			}),
			key, testSource,
		},
		{
			"target given as a ledger entry",
			metaV2(t, []xdr.LedgerEntryChange{createdChange(sponsored)}),
			sponsored, testSource,
		},
		{
			"target given as base64",
			metaV2(t, []xdr.LedgerEntryChange{createdChange(sponsored)}),
			mustMarshal(t, key), testSource,
		},
		{
			"target given as a decoded lens value",
			metaV2(t, []xdr.LedgerEntryChange{createdChange(sponsored)}),
			lensKey, testSource,
		},
		{
			"sponsor given as a muxed account",
			metaV2(t, []xdr.LedgerEntryChange{createdChange(sponsored)}),
			key, mustMuxed(t, testSource),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertEntrySponsoredBy(tb, tc.meta, tc.target, tc.sponsor) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

func TestAssertEntrySponsoredByFailureMessages(t *testing.T) {
	t.Parallel()

	sponsoredByIssuer := sponsoredAccountEntryValue(t, testIssuer)
	key := ledgerKeyOf(t, sponsoredByIssuer)

	t.Run("wrong sponsor names the transition", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{
			stateChange(sponsoredAccountEntryValue(t, testDest)),
			updatedChange(sponsoredByIssuer),
		})
		tb := faketb.New()
		if focus.AssertEntrySponsoredBy(tb, meta, key, testSource) {
			t.Fatal("assertion passed for the wrong sponsor")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"sponsorship transferred from",
			"want sponsored by GCJNH…ZST6",
			"sponsorship transferred from GAPB7…WYXZ to GA2JR…HVAW",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("unsponsored entry", func(t *testing.T) {
		t.Parallel()

		unsponsored := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(unsponsored), updatedChange(unsponsored)})
		tb := faketb.New()
		if focus.AssertEntrySponsoredBy(tb, meta, key, testSource) {
			t.Fatal("assertion passed for an unsponsored entry")
		}
		if !strings.Contains(tb.Output(), "is not sponsored, want sponsored by") {
			t.Errorf("message does not say the entry is unsponsored:\n%s", tb.Output())
		}
	})

	t.Run("revoked sponsorship", func(t *testing.T) {
		t.Parallel()

		unsponsored := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
		meta := metaV2(t, []xdr.LedgerEntryChange{stateChange(sponsoredByIssuer), updatedChange(unsponsored)})
		tb := faketb.New()
		if focus.AssertEntrySponsoredBy(tb, meta, key, testSource) {
			t.Fatal("assertion passed for a revoked sponsorship")
		}
		if !strings.Contains(tb.Output(), "sponsorship revoked from GA2JR…HVAW") {
			t.Errorf("message does not name the revocation:\n%s", tb.Output())
		}
	})

	t.Run("entry untouched", func(t *testing.T) {
		t.Parallel()

		other := accountEntryValue(t, testSource, 5_0000000, xdr.NewThreshold(1, 0, 0, 0))
		meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(other)})
		tb := faketb.New()
		if focus.AssertEntrySponsoredBy(tb, meta, key, testSource) {
			t.Fatal("assertion passed for an untouched entry")
		}
		if !strings.Contains(tb.Output(), "made no ledger change to") {
			t.Errorf("message does not say the entry was untouched:\n%s", tb.Output())
		}
	})

	t.Run("entry removed", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{
			stateChange(sponsoredByIssuer),
			removedChange(key),
		})
		tb := faketb.New()
		if focus.AssertEntrySponsoredBy(tb, meta, key, testSource) {
			t.Fatal("assertion passed for a removed entry")
		}
		if !strings.Contains(tb.Output(), "carries no sponsorship to inspect") {
			t.Errorf("message does not say the entry was removed:\n%s", tb.Output())
		}
	})

	t.Run("empty sponsor is an input error", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{createdChange(sponsoredByIssuer)})
		tb := faketb.New()
		if focus.AssertEntrySponsoredBy(tb, meta, key, "") {
			t.Fatal("assertion passed with an empty sponsor")
		}
		if !strings.Contains(tb.Output(), "sponsorAccount could not be read as an account") {
			t.Errorf("message does not report the input error:\n%s", tb.Output())
		}
	})
}

func TestAssertSponsorshipTransition(t *testing.T) {
	t.Parallel()

	unsponsored := accountEntryValue(t, testDest, 10_0000000, xdr.NewThreshold(1, 0, 0, 0))
	sponsoredSource := sponsoredAccountEntryValue(t, testSource)
	sponsoredIssuer := sponsoredAccountEntryValue(t, testIssuer)
	key := ledgerKeyOf(t, unsponsored)

	establishedMeta := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(unsponsored),
		updatedChange(sponsoredSource),
	})
	transferredMeta := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(sponsoredIssuer),
		updatedChange(sponsoredSource),
	})
	revokedMeta := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(sponsoredIssuer),
		updatedChange(unsponsored),
	})
	createdSponsoredMeta := metaV2(t, []xdr.LedgerEntryChange{
		createdChange(sponsoredSource),
	})

	t.Run("established sponsorship from unsponsored to sponsor", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertSponsorshipTransition(tb, establishedMeta, mustMarshal(t, key), "", testSource) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.SponsorshipTransition(t, establishedMeta, mustMarshal(t, key), "", testSource)
	})

	t.Run("transferred sponsorship from one sponsor to another", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertSponsorshipTransition(tb, transferredMeta, mustMarshal(t, key), testIssuer, testSource) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.SponsorshipTransition(t, transferredMeta, mustMarshal(t, key), testIssuer, testSource)
	})

	t.Run("revoked sponsorship to unsponsored", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertSponsorshipTransition(tb, revokedMeta, mustMarshal(t, key), testIssuer, "") {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.SponsorshipTransition(t, revokedMeta, mustMarshal(t, key), testIssuer, "")
	})

	t.Run("created entry with sponsor has no previous sponsor", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertSponsorshipTransition(tb, createdSponsoredMeta, mustMarshal(t, key), "", testSource) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("fails when previous sponsor does not match", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSponsorshipTransition(tb, transferredMeta, mustMarshal(t, key), testDest, testSource) {
			t.Fatal("assertion passed when previous sponsor differed")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "sponsorship transition") || !strings.Contains(msg, "does not match") {
			t.Errorf("message did not report previous sponsor mismatch:\n%s", msg)
		}
	})

	t.Run("fails when new sponsor does not match", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSponsorshipTransition(tb, transferredMeta, mustMarshal(t, key), testIssuer, testDest) {
			t.Fatal("assertion passed when new sponsor differed")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "sponsorship transition") || !strings.Contains(msg, "does not match") {
			t.Errorf("message did not report new sponsor mismatch:\n%s", msg)
		}
	})

	t.Run("fails when entry was untouched", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSponsorshipTransition(tb, metaV2(t), mustMarshal(t, key), "", testSource) {
			t.Fatal("assertion passed for untouched entry")
		}
		if !strings.Contains(tb.Output(), "made no ledger change") {
			t.Errorf("message did not report entry untouched:\n%s", tb.Output())
		}
	})
}

func TestAssertRevokeSponsorship(t *testing.T) {
	t.Parallel()

	account := mustAccountID(t, testDest)
	keys := []struct {
		name string
		key  xdr.LedgerKey
		kind xdr.LedgerEntryType
	}{
		{
			"account",
			xdr.LedgerKey{Type: xdr.LedgerEntryTypeAccount, Account: &xdr.LedgerKeyAccount{AccountId: account}},
			xdr.LedgerEntryTypeAccount,
		},
		{
			"trustline",
			xdr.LedgerKey{Type: xdr.LedgerEntryTypeTrustline, TrustLine: &xdr.LedgerKeyTrustLine{AccountId: account}},
			xdr.LedgerEntryTypeTrustline,
		},
		{
			"offer",
			xdr.LedgerKey{Type: xdr.LedgerEntryTypeOffer, Offer: &xdr.LedgerKeyOffer{SellerId: account, OfferId: 1}},
			xdr.LedgerEntryTypeOffer,
		},
		{
			"data",
			xdr.LedgerKey{Type: xdr.LedgerEntryTypeData, Data: &xdr.LedgerKeyData{AccountId: account, DataName: "name"}},
			xdr.LedgerEntryTypeData,
		},
		{
			"claimable balance",
			xdr.LedgerKey{Type: xdr.LedgerEntryTypeClaimableBalance, ClaimableBalance: &xdr.LedgerKeyClaimableBalance{}},
			xdr.LedgerEntryTypeClaimableBalance,
		},
	}

	for _, tc := range keys {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			op := xdr.Operation{Body: xdr.OperationBody{
				Type: xdr.OperationTypeRevokeSponsorship,
				RevokeSponsorshipOp: &xdr.RevokeSponsorshipOp{
					Type:      xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry,
					LedgerKey: &tc.key,
				},
			}}
			tb := faketb.New()
			if !focus.AssertRevokeSponsorship(tb, op,
				xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry,
				func(actual xdr.RevokeSponsorshipOp) bool {
					return actual.LedgerKey != nil && actual.LedgerKey.Type == tc.kind
				}) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("signer target and require helpers", func(t *testing.T) {
		t.Parallel()
		signerKey := xdr.MustSigner(testSource)
		op := xdr.Operation{Body: xdr.OperationBody{
			Type: xdr.OperationTypeRevokeSponsorship,
			RevokeSponsorshipOp: &xdr.RevokeSponsorshipOp{
				Type: xdr.RevokeSponsorshipTypeRevokeSponsorshipSigner,
				Signer: &xdr.RevokeSponsorshipOpSigner{
					AccountId: account,
					SignerKey: signerKey,
				},
			},
		}}
		tb := faketb.New()
		if !focus.AssertRevokeSignerSponsorship(tb, op, testDest, signerKey) {
			t.Errorf("signer assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.RevokeSignerSponsorship(t, op, testDest, signerKey)

		accountOp := xdr.Operation{Body: xdr.OperationBody{
			Type: xdr.OperationTypeRevokeSponsorship,
			RevokeSponsorshipOp: &xdr.RevokeSponsorshipOp{
				Type:      xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry,
				LedgerKey: &keys[0].key,
			},
		}}
		require.RevokeAccountSponsorship(t, accountOp, testDest)
	})

	t.Run("reports type and target mismatches", func(t *testing.T) {
		t.Parallel()
		op := xdr.Operation{Body: xdr.OperationBody{
			Type: xdr.OperationTypeRevokeSponsorship,
			RevokeSponsorshipOp: &xdr.RevokeSponsorshipOp{
				Type:      xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry,
				LedgerKey: &keys[0].key,
			},
		}}
		tb := faketb.New()
		if focus.AssertRevokeSponsorship(tb, op,
			xdr.RevokeSponsorshipTypeRevokeSponsorshipSigner, nil) {
			t.Fatal("assertion passed for a different sponsorship type")
		}
		if !strings.Contains(tb.Output(), "REVOKE_SPONSORSHIP_LEDGER_ENTRY") ||
			!strings.Contains(tb.Output(), "REVOKE_SPONSORSHIP_SIGNER") {
			t.Errorf("message does not show expected and actual types:\n%s", tb.Output())
		}

		tb = faketb.New()
		if focus.AssertRevokeSponsorship(tb, op,
			xdr.RevokeSponsorshipTypeRevokeSponsorshipLedgerEntry,
			func(xdr.RevokeSponsorshipOp) bool { return false }) {
			t.Fatal("assertion passed for a mismatched revocation target")
		}
		if !strings.Contains(tb.Output(), "revocation target did not match") {
			t.Errorf("message does not report the target mismatch:\n%s", tb.Output())
		}
	})
}
