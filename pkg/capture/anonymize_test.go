package capture_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

func TestAnonymizeIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	original := capture.Record{Type: "TransactionEnvelope", XDR: envelopeXDR(t)}
	first, err := capture.Anonymize([]capture.Record{original})
	if err != nil {
		t.Fatalf("Anonymize() error = %v", err)
	}
	second, err := capture.Anonymize([]capture.Record{original})
	if err != nil {
		t.Fatalf("second Anonymize() error = %v", err)
	}
	if first[0].XDR != second[0].XDR {
		t.Fatal("identical inputs produced different anonymized XDR")
	}
	if original.XDR != envelopeXDR(t) {
		t.Fatal("Anonymize() modified its input records")
	}
	if first[0].XDR == original.XDR {
		t.Fatal("Anonymize() did not change an address-bearing envelope")
	}

	before, err := golden.Build(original.XDR, original.Type, "", nil)
	if err != nil {
		t.Fatalf("building original fixture: %v", err)
	}
	after, err := golden.Build(first[0].XDR, first[0].Type, "", nil)
	if err != nil {
		t.Fatalf("building anonymized fixture: %v", err)
	}
	addresses := regexp.MustCompile(`G[A-Z2-7]{55}`).FindAll(before.Decoded, -1)
	if len(addresses) == 0 {
		t.Fatal("test envelope contains no public account address")
	}
	for _, address := range addresses {
		if strings.Contains(string(after.Decoded), string(address)) {
			t.Errorf("anonymized fixture still contains address %s", address)
		}
	}
}

// selfPayingEnvelope has the same account as its source and its destination,
// which is the relationship a pseudonymizer has to preserve.
func selfPayingEnvelope(t *testing.T) (*xdr.TransactionEnvelope, string) {
	t.Helper()
	kp, err := keypair.FromRawSeed([32]byte{9, 9, 9})
	if err != nil {
		t.Fatalf("building keypair: %v", err)
	}
	address := kp.Address()
	amount := xdr.Int64(100)

	return &xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: xdr.MustMuxedAddress(address),
				Fee:           100,
				SeqNum:        1,
				Operations: []xdr.Operation{{
					Body: xdr.OperationBody{
						Type: xdr.OperationTypePayment,
						PaymentOp: &xdr.PaymentOp{
							Destination: xdr.MustMuxedAddress(address),
							Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
							Amount:      amount,
						},
					},
				}},
			},
		},
	}, address
}

func TestAnonymizeEnvelopeProducesValidStrkeysAndPreservesRelationships(t *testing.T) {
	t.Parallel()

	env, original := selfPayingEnvelope(t)
	masked := capture.AnonymizeEnvelope(env, 42)

	if masked == nil {
		t.Fatal("AnonymizeEnvelope() returned nil")
	}
	if masked == env {
		t.Fatal("AnonymizeEnvelope() returned the input envelope")
	}

	source, err := masked.V1.Tx.SourceAccount.GetAddress()
	if err != nil {
		t.Fatalf("reading masked source: %v", err)
	}
	destination, err := masked.V1.Tx.Operations[0].Body.PaymentOp.Destination.GetAddress()
	if err != nil {
		t.Fatalf("reading masked destination: %v", err)
	}

	if source != destination {
		t.Errorf("same account mapped to different pseudonyms: source %s, destination %s", source, destination)
	}
	if source == original {
		t.Error("AnonymizeEnvelope() left the real address in place")
	}
	if _, err := strkey.Decode(strkey.VersionByteAccountID, source); err != nil {
		t.Errorf("pseudonym %s is not a valid Ed25519 account strkey: %v", source, err)
	}

	// The input must not be mutated; a caller that still holds it should see
	// the real address.
	unmasked, err := env.V1.Tx.SourceAccount.GetAddress()
	if err != nil {
		t.Fatalf("reading input source: %v", err)
	}
	if unmasked != original {
		t.Errorf("AnonymizeEnvelope() mutated its input: source is now %s, want %s", unmasked, original)
	}

	// Same seed, same pseudonym: the mapping is deterministic across calls and
	// therefore across separate envelopes and results.
	again := capture.AnonymizeEnvelope(env, 42)
	want, err := xdr.MarshalBase64(masked)
	if err != nil {
		t.Fatalf("encoding masked envelope: %v", err)
	}
	got, err := xdr.MarshalBase64(again)
	if err != nil {
		t.Fatalf("encoding second masked envelope: %v", err)
	}
	if got != want {
		t.Error("the same seed produced a different pseudonym")
	}

	// A different seed produces a different pseudonym for the same account.
	other := capture.AnonymizeEnvelope(env, 43)
	otherSource, err := other.V1.Tx.SourceAccount.GetAddress()
	if err != nil {
		t.Fatalf("reading other source: %v", err)
	}
	if otherSource == source {
		t.Error("a different seed produced the same pseudonym")
	}
}

func TestAnonymizeEnvelopeNil(t *testing.T) {
	t.Parallel()
	if got := capture.AnonymizeEnvelope(nil, 1); got != nil {
		t.Fatalf("AnonymizeEnvelope(nil) = %v, want nil", got)
	}
}

func TestAnonymizeRejectsInvalidXDR(t *testing.T) {
	t.Parallel()

	_, err := capture.Anonymize([]capture.Record{{Type: "TransactionEnvelope", XDR: "not-xdr"}})
	if err == nil {
		t.Fatal("Anonymize() accepted invalid XDR")
	}
}
