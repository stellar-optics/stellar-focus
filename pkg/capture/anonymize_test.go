package capture_test

import (
	"regexp"
	"strings"
	"testing"

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

func TestAnonymizeRejectsInvalidXDR(t *testing.T) {
	t.Parallel()

	_, err := capture.Anonymize([]capture.Record{{Type: "TransactionEnvelope", XDR: "not-xdr"}})
	if err == nil {
		t.Fatal("Anonymize() accepted invalid XDR")
	}
}