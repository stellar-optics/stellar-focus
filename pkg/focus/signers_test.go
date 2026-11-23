package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func TestAssertSignersEqual(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	entry := accountEntryValue(t, testSource, 100, thresholds,
		signerValue(t, testDest, 2), signerValue(t, testIssuer, 5))
	meta := metaV2(t, []xdr.LedgerEntryChange{updatedChange(entry)})
	want := map[string]uint32{testDest: 2, testIssuer: 5}

	tests := []struct {
		name    string
		meta    string
		account focus.Value
		want    map[string]uint32
	}{
		{"exact match", meta, testSource, want},
		{"xdr account id", meta, mustAccountID(t, testSource), want},
		{"muxed account", meta, mustMuxed(t, testSource), want},
		{
			name:    "no signers wanted, none present",
			meta:    metaV2(t, []xdr.LedgerEntryChange{updatedChange(accountEntryValue(t, testSource, 100, thresholds))}),
			account: testSource,
			want:    nil,
		},
		{
			name:    "a created account counts",
			meta:    metaV2(t, []xdr.LedgerEntryChange{createdChange(entry)}),
			account: testSource,
			want:    want,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertSignersEqual(tb, tc.meta, tc.account, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

func TestAssertSignersEqualFailures(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	both := accountEntryValue(t, testSource, 100, thresholds,
		signerValue(t, testDest, 2), signerValue(t, testIssuer, 5))
	meta := metaV2(t, []xdr.LedgerEntryChange{updatedChange(both)})

	t.Run("wrong weight", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignersEqual(tb, meta, testSource, map[string]uint32{testDest: 3, testIssuer: 5}) {
			t.Fatal("assertion passed with the wrong weight")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertSignersEqual: account GCJNH…ZST6 signers differ — 1 difference",
			"~ GAPB7…WYXZ weight is 2, want 3",
			"signers after the transaction (2)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("missing signer", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignersEqual(tb, meta, testSource, map[string]uint32{testIssuer: 5}) {
			t.Fatal("assertion passed with a signer missing")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "- GAPB7…WYXZ weight 2") {
			t.Errorf("message does not mark the extra signer:\n%s", msg)
		}
	})

	t.Run("unexpected signer", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		want := map[string]uint32{testDest: 2, testIssuer: 5, testSource: 1}
		if focus.AssertSignersEqual(tb, meta, testSource, want) {
			t.Fatal("assertion passed with a signer missing")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "+ GCJNH…ZST6 weight 1") {
			t.Errorf("message does not mark the missing signer:\n%s", msg)
		}
	})

	t.Run("all signers wanted but none present", func(t *testing.T) {
		t.Parallel()

		empty := metaV2(t, []xdr.LedgerEntryChange{
			updatedChange(accountEntryValue(t, testSource, 100, thresholds)),
		})
		tb := faketb.New()
		if focus.AssertSignersEqual(tb, empty, testSource, map[string]uint32{testDest: 2}) {
			t.Fatal("assertion passed with a signer missing")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "signers after the transaction: none") {
			t.Errorf("message does not show the empty signer set:\n%s", msg)
		}
	})
}

func TestAssertSignersEqualMissingEntry(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	other := accountEntryValue(t, testDest, 100, thresholds)

	t.Run("meta has no changes at all", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignersEqual(tb, metaV2(t), testSource, nil) {
			t.Fatal("assertion passed with no changes in the meta")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertSignersEqual: this meta carries no account entry for GCJNH…ZST6, so its signers cannot be read from it",
			"no ledger entry change in this meta touches account GCJNH…ZST6",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("meta only touches other accounts", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignersEqual(tb, metaV2(t, []xdr.LedgerEntryChange{updatedChange(other)}),
			testSource, nil) {
			t.Fatal("assertion passed for an untouched account")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "no ledger entry change in this meta touches account GCJNH…ZST6") {
			t.Errorf("message does not say the account was untouched:\n%s", msg)
		}
	})

	t.Run("invalid account", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignersEqual(tb, metaV2(t), 42, nil) {
			t.Fatal("assertion passed with an invalid account")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "account could not be read as an account") {
			t.Errorf("message does not name the invalid input:\n%s", msg)
		}
	})
}

func TestAssertThresholdsEqual(t *testing.T) {
	t.Parallel()

	entry := accountEntryValue(t, testSource, 100, xdr.NewThreshold(1, 2, 3, 4))
	meta := metaV2(t, []xdr.LedgerEntryChange{updatedChange(entry)})

	tests := []struct {
		name string
		want focus.Value
	}{
		{"focus thresholds", focus.Thresholds{Master: 1, Low: 2, Medium: 3, High: 4}},
		{"xdr thresholds", xdr.NewThreshold(1, 2, 3, 4)},
		{"array of bytes", [4]byte{1, 2, 3, 4}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertThresholdsEqual(tb, meta, testSource, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}

	t.Run("a zero-valued account", func(t *testing.T) {
		t.Parallel()

		fresh := accountEntryValue(t, testDest, 0, xdr.NewThreshold(0, 0, 0, 0))
		tb := faketb.New()
		if !focus.AssertThresholdsEqual(tb,
			metaV2(t, []xdr.LedgerEntryChange{createdChange(fresh)}),
			testDest, focus.Thresholds{}) {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
	})
}

func TestAssertThresholdsEqualFailures(t *testing.T) {
	t.Parallel()

	entry := accountEntryValue(t, testSource, 100, xdr.NewThreshold(1, 2, 3, 4))
	meta := metaV2(t, []xdr.LedgerEntryChange{updatedChange(entry)})

	t.Run("differing thresholds name each one", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertThresholdsEqual(tb, meta, testSource,
			focus.Thresholds{Master: 1, Low: 0, Medium: 3, High: 5}) {
			t.Fatal("assertion passed with differing thresholds")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertThresholdsEqual: account GCJNH…ZST6 thresholds differ",
			"low threshold is 2, want 0",
			"high threshold is 4, want 5",
			"got  master 1  low 2  medium 3  high 4",
			"want master 1  low 0  medium 3  high 5",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("want of an unsupported type", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertThresholdsEqual(tb, meta, testSource, 42) {
			t.Fatal("assertion passed with an unsupported want")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "want could not be read as thresholds: unsupported type int") {
			t.Errorf("message does not explain the want:\n%s", msg)
		}
	})

	t.Run("missing account entry", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertThresholdsEqual(tb, metaV2(t), testSource, focus.Thresholds{}) {
			t.Fatal("assertion passed with no changes in the meta")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "so its thresholds cannot be read from it") {
			t.Errorf("message does not explain the missing entry:\n%s", msg)
		}
	})
}

func TestAssertSignerWeight(t *testing.T) {
	t.Parallel()

	edKey := testDest
	hashXRaw := make([]byte, 32)
	hashXRaw[0] = 0xab
	hashXKey, err := strkey.Encode(strkey.VersionByteHashX, hashXRaw)
	if err != nil {
		t.Fatalf("encoding hashX key: %v", err)
	}

	preAuthRaw := make([]byte, 32)
	preAuthRaw[0] = 0xcd
	preAuthKey, err := strkey.Encode(strkey.VersionByteHashTx, preAuthRaw)
	if err != nil {
		t.Fatalf("encoding preAuthTx key: %v", err)
	}

	signerEd := signerValue(t, edKey, 20)
	signerHashX := signerValue(t, hashXKey, 15)
	signerPreAuth := signerValue(t, preAuthKey, 10)
	signerRemoved := signerValue(t, edKey, 0)

	t.Run("SetOptionsOp adding signers", func(t *testing.T) {
		t.Parallel()

		opEd := xdr.SetOptionsOp{Signer: &signerEd}
		opHashX := xdr.SetOptionsOp{Signer: &signerHashX}
		opPreAuth := xdr.SetOptionsOp{Signer: &signerPreAuth}

		tb := faketb.New()
		if !focus.AssertSignerWeight(tb, opEd, edKey, 20) {
			t.Errorf("AssertSignerWeight failed for Ed25519:\n%s", tb.Output())
		}
		require.SignerWeight(t, opEd, edKey, 20)

		tb = faketb.New()
		if !focus.AssertSignerWeight(tb, opHashX, hashXKey, 15) {
			t.Errorf("AssertSignerWeight failed for HashX:\n%s", tb.Output())
		}
		require.SignerWeight(t, opHashX, hashXKey, 15)

		tb = faketb.New()
		if !focus.AssertSignerWeight(tb, opPreAuth, preAuthKey, 10) {
			t.Errorf("AssertSignerWeight failed for PreAuthTx:\n%s", tb.Output())
		}
		require.SignerWeight(t, opPreAuth, preAuthKey, 10)
	})

	t.Run("SetOptionsOp updating signer weight", func(t *testing.T) {
		t.Parallel()

		updatedSigner := signerValue(t, edKey, 50)
		op := xdr.SetOptionsOp{Signer: &updatedSigner}

		tb := faketb.New()
		if !focus.AssertSignerWeight(tb, op, edKey, 50) {
			t.Errorf("AssertSignerWeight failed on updated weight:\n%s", tb.Output())
		}
	})

	t.Run("SetOptionsOp removing signer with weight 0", func(t *testing.T) {
		t.Parallel()

		op := xdr.SetOptionsOp{Signer: &signerRemoved}

		tb := faketb.New()
		if !focus.AssertSignerWeight(tb, op, edKey, 0) {
			t.Errorf("AssertSignerWeight failed for removed signer in SetOptionsOp:\n%s", tb.Output())
		}
		require.SignerWeight(t, op, edKey, 0)
	})

	t.Run("AccountEntry signers", func(t *testing.T) {
		t.Parallel()

		thresholds := xdr.NewThreshold(1, 0, 0, 0)
		entry := accountEntryValue(t, testSource, 100, thresholds, signerEd, signerHashX)

		tb := faketb.New()
		if !focus.AssertSignerWeight(tb, entry, edKey, 20) {
			t.Errorf("AssertSignerWeight failed for Ed25519 in AccountEntry:\n%s", tb.Output())
		}
		tb = faketb.New()
		if !focus.AssertSignerWeight(tb, entry, hashXKey, 15) {
			t.Errorf("AssertSignerWeight failed for HashX in AccountEntry:\n%s", tb.Output())
		}
		tb = faketb.New()
		if !focus.AssertSignerWeight(tb, entry, preAuthKey, 0) {
			t.Errorf("AssertSignerWeight failed for absent/removed signer in AccountEntry:\n%s", tb.Output())
		}
	})

	t.Run("TransactionEnvelope containing SetOptionsOp", func(t *testing.T) {
		t.Parallel()

		op := xdr.Operation{
			Body: xdr.OperationBody{
				Type:         xdr.OperationTypeSetOptions,
				SetOptionsOp: &xdr.SetOptionsOp{Signer: &signerEd},
			},
		}
		env := envelopeValue(t, op)

		tb := faketb.New()
		if !focus.AssertSignerWeight(tb, env, edKey, 20) {
			t.Errorf("AssertSignerWeight failed for envelope:\n%s", tb.Output())
		}
	})

	t.Run("TransactionMeta containing account entry", func(t *testing.T) {
		t.Parallel()

		thresholds := xdr.NewThreshold(1, 0, 0, 0)
		entry := accountEntryValue(t, testSource, 100, thresholds, signerEd)
		meta := metaV2(t, []xdr.LedgerEntryChange{updatedChange(entry)})

		tb := faketb.New()
		if !focus.AssertSignerWeight(tb, meta, edKey, 20) {
			t.Errorf("AssertSignerWeight failed for meta:\n%s", tb.Output())
		}
	})
}

func TestAssertSignerWeightFailures(t *testing.T) {
	t.Parallel()

	signerEd := signerValue(t, testDest, 20)
	opEd := xdr.SetOptionsOp{Signer: &signerEd}

	t.Run("mismatched weight in SetOptionsOp", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignerWeight(tb, opEd, testDest, 10) {
			t.Fatal("assertion passed with mismatched weight")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("reading message: %v", err)
		}
		if !strings.Contains(msg, "weight is 20, want 10") {
			t.Errorf("unexpected failure message:\n%s", msg)
		}
	})

	t.Run("missing signer in SetOptionsOp", func(t *testing.T) {
		t.Parallel()

		emptyOp := xdr.SetOptionsOp{}
		tb := faketb.New()
		if focus.AssertSignerWeight(tb, emptyOp, testDest, 20) {
			t.Fatal("assertion passed with nil Signer in SetOptionsOp")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("reading message: %v", err)
		}
		if !strings.Contains(msg, "missing from SetOptionsOp") {
			t.Errorf("unexpected failure message:\n%s", msg)
		}
	})

	t.Run("mismatched signer key in SetOptionsOp", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignerWeight(tb, opEd, testIssuer, 20) {
			t.Fatal("assertion passed with mismatched signer key")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("reading message: %v", err)
		}
		if !strings.Contains(msg, "signer key is") || !strings.Contains(msg, "want") {
			t.Errorf("unexpected failure message:\n%s", msg)
		}
	})

	t.Run("not removed when wantWeight is 0", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignerWeight(tb, opEd, testDest, 0) {
			t.Fatal("assertion passed when signer was not removed")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("reading message: %v", err)
		}
		if !strings.Contains(msg, "was not removed: weight is 20, want 0") {
			t.Errorf("unexpected failure message:\n%s", msg)
		}
	})

	t.Run("missing signer from AccountEntry", func(t *testing.T) {
		t.Parallel()

		thresholds := xdr.NewThreshold(1, 0, 0, 0)
		entry := accountEntryValue(t, testSource, 100, thresholds)

		tb := faketb.New()
		if focus.AssertSignerWeight(tb, entry, testDest, 20) {
			t.Fatal("assertion passed for missing signer in AccountEntry")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("reading message: %v", err)
		}
		if !strings.Contains(msg, "missing from account entry") {
			t.Errorf("unexpected failure message:\n%s", msg)
		}
	})

	t.Run("signer present when asserted removed in AccountEntry", func(t *testing.T) {
		t.Parallel()

		thresholds := xdr.NewThreshold(1, 0, 0, 0)
		entry := accountEntryValue(t, testSource, 100, thresholds, signerEd)

		tb := faketb.New()
		if focus.AssertSignerWeight(tb, entry, testDest, 0) {
			t.Fatal("assertion passed when signer still present in AccountEntry")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("reading message: %v", err)
		}
		if !strings.Contains(msg, "was not removed from account entry") {
			t.Errorf("unexpected failure message:\n%s", msg)
		}
	})

	t.Run("nil input handles gracefully", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSignerWeight(tb, nil, testDest, 20) {
			t.Fatal("assertion passed with nil input")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("reading message: %v", err)
		}
		if !strings.Contains(msg, "opOrEntry is nil") {
			t.Errorf("unexpected failure message:\n%s", msg)
		}
	})
}
