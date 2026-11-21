package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// testClaimableBalanceID returns a deterministic balance id, so failure
// messages are stable across runs.
func testClaimableBalanceID() xdr.ClaimableBalanceId {
	h := xdr.Hash{0xab, 0xcd, 0xef}
	return xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &h,
	}
}

// otherClaimableBalanceID returns a different balance id, for mismatch tests.
func otherClaimableBalanceID() xdr.ClaimableBalanceId {
	h := xdr.Hash{0x11, 0x22}
	return xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &h,
	}
}

// clawbackOpValue builds an issuer clawback of a credit asset.
func clawbackOpValue(t *testing.T, from string, amount int64) xdr.Operation {
	t.Helper()
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeClawback,
		ClawbackOp: &xdr.ClawbackOp{
			Asset:  mustCreditAsset(t, "USDC"),
			From:   mustMuxed(t, from),
			Amount: xdr.Int64(amount),
		},
	}}
}

// clawbackBalanceOpValue builds a claimable balance revocation.
func clawbackBalanceOpValue(id xdr.ClaimableBalanceId) xdr.Operation {
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeClawbackClaimableBalance,
		ClawbackClaimableBalanceOp: &xdr.ClawbackClaimableBalanceOp{
			BalanceId: id,
		},
	}}
}

// claimableBalanceEntryValue builds a claimable balance ledger entry.
func claimableBalanceEntryValue(t *testing.T, id xdr.ClaimableBalanceId, amount int64) xdr.LedgerEntry {
	t.Helper()
	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 42,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeClaimableBalance,
			ClaimableBalance: &xdr.ClaimableBalanceEntry{
				BalanceId: id,
				Claimants: []xdr.Claimant{{
					Type: xdr.ClaimantTypeClaimantTypeV0,
					V0: &xdr.ClaimantV0{
						Destination: mustAccountID(t, testDest),
						Predicate: xdr.ClaimPredicate{
							Type: xdr.ClaimPredicateTypeClaimPredicateUnconditional,
						},
					},
				}},
				Asset:  mustCreditAsset(t, "USDC"),
				Amount: xdr.Int64(amount),
			},
		},
	}
}

func TestAssertClawbackOp(t *testing.T) {
	t.Parallel()

	const amount = 250_0000000

	tests := []struct {
		name     string
		envelope string
		index    int
		from     focus.Value
		asset    focus.Value
		amount   int64
	}{
		{
			"strkey account and canonical asset",
			buildEnvelope(t, clawbackOpValue(t, testDest, amount)), 0,
			testDest, "USDC:" + testIssuer, amount,
		},
		{
			"xdr account and xdr asset",
			buildEnvelope(t, clawbackOpValue(t, testDest, amount)), 0,
			mustAccountID(t, testDest), mustCreditAsset(t, "USDC"), amount,
		},
		{
			"muxed account",
			buildEnvelope(t, clawbackOpValue(t, testDest, amount)), 0,
			mustMuxed(t, testDest), mustCreditAsset(t, "USDC"), amount,
		},
		{
			"operation after another",
			buildEnvelope(t, paymentOp(t, 1), clawbackOpValue(t, testDest, amount)), 1,
			testDest, mustCreditAsset(t, "USDC"), amount,
		},
		{
			"v0 envelope",
			mustMarshal(t, v0EnvelopeValue(t, nil, clawbackOpValue(t, testDest, amount))), 0,
			testDest, mustCreditAsset(t, "USDC"), amount,
		},
		{
			"fee bump envelope",
			mustMarshal(t, feeBumpEnvelopeValue(t,
				envelopeValue(t, clawbackOpValue(t, testDest, amount)), nil, nil)), 0,
			testDest, mustCreditAsset(t, "USDC"), amount,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertClawbackOp(tb, tc.envelope, tc.index, tc.from, tc.asset, tc.amount) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertClawbackOpFailureMessage checks that a mismatch names each field
// that differed rather than only the first one.
func TestAssertClawbackOpFailureMessage(t *testing.T) {
	t.Parallel()

	envelope := buildEnvelope(t, clawbackOpValue(t, testSource, 100_0000000))

	tb := faketb.New()
	if focus.AssertClawbackOp(tb, envelope, 0, testDest, mustCreditAsset(t, "USDX"), 200_0000000) {
		t.Fatal("assertion passed for a mismatched clawback")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{
		"AssertClawbackOp: clawback at operation 0 does not match",
		"fields that differ (3)",
		"~ from",
		"want  GAPB7…WYXZ",
		"got   GCJNH…ZST6",
		"~ asset",
		"want  USDX:" + testIssuer,
		"got   USDC:" + testIssuer,
		"~ amount",
		"want  2000000000 (200.0000000)",
		"got   1000000000 (100.0000000)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertClawbackOpRejectsBadInput(t *testing.T) {
	t.Parallel()

	envelope := buildEnvelope(t, clawbackOpValue(t, testDest, 100_0000000))

	t.Run("non-positive amount", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackOp(tb, envelope, 0, testDest, mustCreditAsset(t, "USDC"), 0) {
			t.Fatal("assertion passed with a zero amount")
		}
		if !strings.Contains(tb.Output(), "forfeits a positive amount") {
			t.Errorf("message does not explain the amount:\n%s", tb.Output())
		}
	})

	t.Run("native asset", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackOp(tb, envelope, 0, testDest, "native", 100) {
			t.Fatal("assertion passed for a native clawback")
		}
		if !strings.Contains(tb.Output(), "native asset cannot be clawed back") {
			t.Errorf("message does not explain the native asset:\n%s", tb.Output())
		}
	})

	t.Run("wrong operation type", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackOp(tb, buildEnvelope(t, paymentOp(t, 1)), 0,
			testDest, mustCreditAsset(t, "USDC"), 100) {
			t.Fatal("assertion passed for a payment")
		}
		msg := tb.Output()
		if !strings.Contains(msg, "operation 0 is a payment, not the operation asserted on") {
			t.Errorf("message does not name the operation found:\n%s", msg)
		}
	})

	t.Run("index out of range", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackOp(tb, envelope, 3, testDest, mustCreditAsset(t, "USDC"), 100) {
			t.Fatal("assertion passed for an out-of-range index")
		}
		if !strings.Contains(tb.Output(), "no operation at index 3; the transaction has 1") {
			t.Errorf("message does not report the index:\n%s", tb.Output())
		}
	})
}

func TestAssertClawbackClaimableBalanceOp(t *testing.T) {
	t.Parallel()

	id := testClaimableBalanceID()
	entry := claimableBalanceEntryValue(t, id, 500_0000000)
	key := ledgerKeyOf(t, entry)
	_, lensKey, err := focus.Encode(key, "LedgerKey")
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}
	_, lensID, err := focus.Encode(id, "ClaimableBalanceId")
	if err != nil {
		t.Fatalf("encoding the id: %v", err)
	}
	_, lensEntry, err := focus.Encode(entry, "LedgerEntry")
	if err != nil {
		t.Fatalf("encoding the entry: %v", err)
	}

	tests := []struct {
		name      string
		envelope  string
		index     int
		balanceID focus.Value
	}{
		{"xdr id", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, id},
		{"xdr id pointer", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, &id},
		{"ledger key", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, key},
		{"ledger key pointer", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, &key},
		{"ledger entry", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, entry},
		{"ledger entry pointer", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, &entry},
		{"base64 id", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, mustMarshal(t, id)},
		{"base64 key", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, mustMarshal(t, key)},
		{"decoded id", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, lensID},
		{"decoded key", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, lensKey},
		{"decoded entry", buildEnvelope(t, clawbackBalanceOpValue(id)), 0, lensEntry},
		{"after another operation",
			buildEnvelope(t, paymentOp(t, 1), clawbackBalanceOpValue(id)), 1, id},
		{
			"fee bump envelope",
			mustMarshal(t, feeBumpEnvelopeValue(t, envelopeValue(t, clawbackBalanceOpValue(id)), nil, nil)),
			0, id,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertClawbackClaimableBalanceOp(tb, tc.envelope, tc.index, tc.balanceID) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

func TestAssertClawbackClaimableBalanceOpRejectsBadInput(t *testing.T) {
	t.Parallel()

	envelope := buildEnvelope(t, clawbackBalanceOpValue(testClaimableBalanceID()))
	account := accountEntryValue(t, testDest, 1, xdr.NewThreshold(1, 0, 0, 0))

	tests := []struct {
		name      string
		balanceID focus.Value
		contains  string
	}{
		{"an account key is not a balance", ledgerKeyOf(t, account), "want a claimable balance"},
		{"an integer is not a balance", 42, "could not be read as a claimable balance id"},
		{"nil", nil, "could not be read as a claimable balance id"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertClawbackClaimableBalanceOp(tb, envelope, 0, tc.balanceID) {
				t.Fatal("assertion passed for a bad balance id")
			}
			if !strings.Contains(tb.Output(), tc.contains) {
				t.Errorf("message does not contain %q:\n%s", tc.contains, tb.Output())
			}
		})
	}
}

func TestAssertClawbackClaimableBalanceOpFailure(t *testing.T) {
	t.Parallel()

	envelope := buildEnvelope(t, clawbackBalanceOpValue(testClaimableBalanceID()))

	tb := faketb.New()
	if focus.AssertClawbackClaimableBalanceOp(tb, envelope, 0, otherClaimableBalanceID()) {
		t.Fatal("assertion passed for a different balance")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{
		"AssertClawbackClaimableBalanceOp: operation 0 revokes claimable balance ",
		"want ",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertBalanceForfeited(t *testing.T) {
	t.Parallel()

	partial := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(trustlineEntryValue(t, 100_0000000, 500_0000000)),
		updatedChange(trustlineEntryValue(t, 40_0000000, 500_0000000)),
	})
	full := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(trustlineEntryValue(t, 100_0000000, 500_0000000)),
		removedChange(ledgerKeyOf(t, trustlineEntryValue(t, 100_0000000, 500_0000000))),
	})

	t.Run("partial forfeiture", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertBalanceForfeited(tb, partial, testDest, "USDC:"+testIssuer, 60_0000000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("full forfeiture removes the trustline", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertBalanceForfeited(tb, full, testDest, mustCreditAsset(t, "USDC"), 100_0000000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})

	t.Run("wrong amount", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertBalanceForfeited(tb, partial, testDest, mustCreditAsset(t, "USDC"), 100_0000000) {
			t.Fatal("assertion passed with the wrong amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "want a forfeiture of 1000000000 (100.0000000)") {
			t.Errorf("message does not state what was wanted:\n%s", msg)
		}
	})

	t.Run("native asset rejected", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertBalanceForfeited(tb, partial, testDest, "native", 1) {
			t.Fatal("assertion passed for the native asset")
		}
		if !strings.Contains(tb.Output(), "cannot be clawed back") {
			t.Errorf("message does not explain the native asset:\n%s", tb.Output())
		}
	})
}

func TestAssertClaimableBalanceRevoked(t *testing.T) {
	t.Parallel()

	id := testClaimableBalanceID()
	before := claimableBalanceEntryValue(t, id, 500_0000000)
	key := ledgerKeyOf(t, before)

	revoked := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(before),
		removedChange(key),
	})

	t.Run("passes when the balance is removed", func(t *testing.T) {
		t.Parallel()

		for name, v := range map[string]focus.Value{
			"xdr id":       id,
			"ledger key":   key,
			"ledger entry": before,
			"base64 key":   mustMarshal(t, key),
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				tb := faketb.New()
				if !focus.AssertClaimableBalanceRevoked(tb, revoked, v) {
					t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
				}
			})
		}
	})

	t.Run("fails when the balance is merely modified", func(t *testing.T) {
		t.Parallel()

		meta := metaV2(t, []xdr.LedgerEntryChange{
			stateChange(before),
			updatedChange(claimableBalanceEntryValue(t, id, 1)),
		})
		tb := faketb.New()
		if focus.AssertClaimableBalanceRevoked(tb, meta, id) {
			t.Fatal("assertion passed for a modified balance")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "was not revoked") {
			t.Errorf("message does not say the balance survived:\n%s", msg)
		}
	})

	t.Run("fails when the balance is absent", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClaimableBalanceRevoked(tb, metaV2(t), id) {
			t.Fatal("assertion passed for an untouched balance")
		}
		if !strings.Contains(tb.Output(), "made no ledger change to claimable balance") {
			t.Errorf("message does not say the balance was untouched:\n%s", tb.Output())
		}
	})
}

func TestAssertClawbackRevocation(t *testing.T) {
	t.Parallel()

	partial := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(trustlineEntryValue(t, 100_0000000, 500_0000000)),
		updatedChange(trustlineEntryValue(t, 40_0000000, 500_0000000)),
	})
	full := metaV2(t, []xdr.LedgerEntryChange{
		stateChange(trustlineEntryValue(t, 100_0000000, 500_0000000)),
		removedChange(ledgerKeyOf(t, trustlineEntryValue(t, 100_0000000, 500_0000000))),
	})

	t.Run("partial clawback revocation verifies balance decrease", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertClawbackRevocation(tb, partial, testDest, "USDC:"+testIssuer, 60_0000000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.ClawbackRevocation(t, partial, testDest, "USDC:"+testIssuer, 60_0000000)
	})

	t.Run("full clawback revocation removing trustline verifies deletion", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertClawbackRevocation(tb, full, testDest, "USDC:"+testIssuer, 100_0000000) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.ClawbackRevocation(t, full, testDest, "USDC:"+testIssuer, 100_0000000)
	})

	t.Run("amount mismatch reports diff in stroops and units", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackRevocation(tb, partial, testDest, "USDC:"+testIssuer, 50_0000000) {
			t.Fatal("assertion passed for incorrect clawback amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "-60.0000000") || !strings.Contains(msg, "50.0000000") {
			t.Errorf("failure message did not format units properly:\n%s", msg)
		}
	})

	t.Run("negative or zero amount fails", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackRevocation(tb, partial, testDest, "USDC:"+testIssuer, -10) {
			t.Fatal("assertion passed for negative amount")
		}
		if !strings.Contains(tb.Output(), "positive amount") {
			t.Errorf("message did not report non-positive amount error:\n%s", tb.Output())
		}
	})

	t.Run("native asset cannot be clawed back", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackRevocation(tb, partial, testDest, "native", 100) {
			t.Fatal("assertion passed for native asset")
		}
		if !strings.Contains(tb.Output(), "native asset cannot be clawed back") {
			t.Errorf("message did not reject native asset:\n%s", tb.Output())
		}
	})

	t.Run("untouched account fails", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertClawbackRevocation(tb, partial, testSource, "USDC:"+testIssuer, 60_0000000) {
			t.Fatal("assertion passed for untouched account")
		}
		if !strings.Contains(tb.Output(), "made no ledger change") {
			t.Errorf("message did not report no ledger change:\n%s", tb.Output())
		}
	})
}
