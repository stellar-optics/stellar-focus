package golden

import (
	"testing"
)

func TestValidateFixtureSchemaStrict(t *testing.T) {
	validJSON := []byte(`{
		"format": "1.0",
		"type": "TransactionEnvelope",
		"xdr": "AAAA"
	}`)

	if err := ValidateFixtureSchema(validJSON, true); err != nil {
		t.Errorf("expected valid fixture to pass strict schema validation, got: %v", err)
	}

	invalidJSON := []byte(`{
		"format": "1.0",
		"type": "TransactionEnvelope",
		"xdr": "AAAA",
		"unknown_field": true
	}`)

	if err := ValidateFixtureSchema(invalidJSON, true); err == nil {
		t.Error("expected strict schema validation to fail on unknown field, got nil")
	}

	missingRequired := []byte(`{
		"format": "1.0",
		"type": "TransactionEnvelope"
	}`)

	if err := ValidateFixtureSchema(missingRequired, true); err == nil {
		t.Error("expected strict schema validation to fail when xdr is missing, got nil")
	}
}
