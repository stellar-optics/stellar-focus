package capture_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// sensitiveSeed is a well-formed secret key, so the test exercises exactly
// what a leaked one would look like.
var sensitiveSeed = func() string {
	kp, err := keypair.FromRawSeed([32]byte{7, 7, 7})
	if err != nil {
		panic(err)
	}
	return kp.Seed()
}()

const sensitiveMemo = "cust-4471@example.com"

func strPtr(s string) *string { return &s }

// sensitiveEnvelope builds a transaction carrying every kind of data the
// sanitizer masks: a text memo, a ManageData value, a secret key as a data
// name, and a secret key in a Soroban string argument.
func sensitiveEnvelope(t *testing.T) string {
	t.Helper()

	source := xdr.MustMuxedAddress(keypair.MustParseFull(sensitiveSeed).Address())
	value := xdr.DataValue("private-value")
	seedName := xdr.String64(sensitiveSeed)
	seedValue := xdr.DataValue(sensitiveSeed)
	seedArg := xdr.ScString("seed=" + sensitiveSeed)
	contract := xdr.ContractId{1}

	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: source,
				Fee:           100,
				SeqNum:        1,
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoText, Text: strPtr(sensitiveMemo)},
				Operations: []xdr.Operation{
					{Body: xdr.OperationBody{
						Type:         xdr.OperationTypeManageData,
						ManageDataOp: &xdr.ManageDataOp{DataName: "customer", DataValue: &value},
					}},
					{Body: xdr.OperationBody{
						Type:         xdr.OperationTypeManageData,
						ManageDataOp: &xdr.ManageDataOp{DataName: seedName, DataValue: &seedValue},
					}},
					{Body: xdr.OperationBody{
						Type:         xdr.OperationTypeManageData,
						ManageDataOp: &xdr.ManageDataOp{DataName: "deleted"},
					}},
					{Body: xdr.OperationBody{
						Type: xdr.OperationTypeInvokeHostFunction,
						InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
							HostFunction: xdr.HostFunction{
								Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
								InvokeContract: &xdr.InvokeContractArgs{
									ContractAddress: xdr.ScAddress{
										Type:       xdr.ScAddressTypeScAddressTypeContract,
										ContractId: &contract,
									},
									FunctionName: "store",
									Args:         []xdr.ScVal{{Type: xdr.ScValTypeScvString, Str: &seedArg}},
								},
							},
						},
					}},
				},
			},
		},
	}
	encoded, err := xdr.MarshalBase64(env)
	if err != nil {
		t.Fatalf("encoding the envelope: %v", err)
	}
	return encoded
}

func sanitizeOne(t *testing.T, s capture.Sanitizer, record capture.Record) xdr.TransactionEnvelope {
	t.Helper()
	out, err := s.Sanitize([]capture.Record{record})
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}
	if !out[0].Sanitized {
		t.Error("the sanitized record is not marked Sanitized")
	}
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(out[0].XDR, &env); err != nil {
		t.Fatalf("the sanitized XDR does not decode: %v", err)
	}
	return env
}

func envelopeRecord(t *testing.T) capture.Record {
	t.Helper()
	return capture.Record{Name: "envelope", Type: "TransactionEnvelope", XDR: sensitiveEnvelope(t)}
}

func TestSanitizerMasksTheMemo(t *testing.T) {
	t.Parallel()

	env := sanitizeOne(t, capture.Sanitizer{MaskMemo: true}, envelopeRecord(t))
	if got := env.Memo().Text; got == nil || *got != capture.RedactedMemo {
		t.Errorf("memo = %v, want %q", got, capture.RedactedMemo)
	}
	// Nothing else is touched.
	if got := env.Operations()[0].Body.ManageDataOp.DataValue; string(*got) != "private-value" {
		t.Errorf("data value = %q, want it left alone", *got)
	}
}

func TestSanitizerScrubsDataValues(t *testing.T) {
	t.Parallel()

	env := sanitizeOne(t, capture.Sanitizer{ScrubData: true}, envelopeRecord(t))
	ops := env.Operations()
	for _, i := range []int{0, 1} {
		if got := ops[i].Body.ManageDataOp.DataValue; got == nil || string(*got) != capture.RedactedData {
			t.Errorf("operation %d data value = %v, want %q", i, got, capture.RedactedData)
		}
	}
	// A ManageData with no value deletes the entry; it must stay a delete.
	if got := ops[2].Body.ManageDataOp.DataValue; got != nil {
		t.Errorf("a delete became a write of %q", *got)
	}
	if got := *env.Memo().Text; got != sensitiveMemo {
		t.Errorf("memo = %q, want it left alone", got)
	}
}

func TestSanitizerScrubsSecretKeysEverywhere(t *testing.T) {
	t.Parallel()

	record := envelopeRecord(t)
	out, err := capture.Sanitizer{ScrubKeys: true}.Sanitize([]capture.Record{record})
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}

	env := sanitizeOne(t, capture.Sanitizer{ScrubKeys: true}, record)
	ops := env.Operations()
	if got := string(ops[1].Body.ManageDataOp.DataName); got != capture.RedactedSecretKey {
		t.Errorf("data name = %q, want %q", got, capture.RedactedSecretKey)
	}
	if got := string(*ops[1].Body.ManageDataOp.DataValue); got != capture.RedactedSecretKey {
		t.Errorf("data value = %q, want %q", got, capture.RedactedSecretKey)
	}
	arg := ops[3].Body.InvokeHostFunctionOp.HostFunction.InvokeContract.Args[0].Str
	if got := string(*arg); got != "seed="+capture.RedactedSecretKey {
		t.Errorf("Soroban argument = %q, want the key masked and the rest kept", got)
	}

	// The fixture built from it holds the secret nowhere, decoded or not.
	fixtures, err := capture.Capture(context.Background(), sourceWithTransaction(t, "abc123"), out, capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if strings.Contains(string(fixtures[0].Decoded), sensitiveSeed) {
		t.Error("the decoded section still contains the secret key")
	}
	if !fixtures[0].Captured.Sanitized {
		t.Error("the fixture provenance is not marked sanitized")
	}
}

func TestSanitizerMasksDataEntries(t *testing.T) {
	t.Parallel()

	account := xdr.MustAddress(keypair.MustParseFull(sensitiveSeed).Address())
	entry := xdr.LedgerEntry{
		LastModifiedLedgerSeq: 1,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeData,
			Data: &xdr.DataEntry{AccountId: account, DataName: "customer", DataValue: xdr.DataValue("private")},
		},
	}
	encoded, err := xdr.MarshalBase64(entry)
	if err != nil {
		t.Fatalf("encoding the entry: %v", err)
	}

	out, err := capture.Sanitizer{ScrubData: true}.Sanitize([]capture.Record{
		{Name: "entry", Type: "LedgerEntry", XDR: encoded},
	})
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}
	var got xdr.LedgerEntry
	if err := xdr.SafeUnmarshalBase64(out[0].XDR, &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if string(got.Data.Data.DataValue) != capture.RedactedData {
		t.Errorf("data entry value = %q, want %q", got.Data.Data.DataValue, capture.RedactedData)
	}
}

func TestDisabledSanitizerChangesNothing(t *testing.T) {
	t.Parallel()

	record := envelopeRecord(t)
	out, err := capture.Sanitizer{}.Sanitize([]capture.Record{record})
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}
	if out[0].XDR != record.XDR || out[0].Sanitized {
		t.Error("a sanitizer with nothing enabled changed the record")
	}
}

func TestVerifyRefusesSanitizedFixturesWithoutFetching(t *testing.T) {
	t.Parallel()

	src := sourceWithTransaction(t, "abc123")
	records, err := src.Transaction(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	records[0].Sanitized = true
	fixtures, err := capture.Capture(context.Background(), src, records[:1], capturedAt)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	calls := src.Calls
	got := capture.Verify(context.Background(), src, "envelope.golden.json", fixtures[0])
	if got.Err == nil || !strings.Contains(got.Err.Error(), "sanitized") {
		t.Errorf("Verify() error = %v, want a sanitized refusal", got.Err)
	}
	if src.Calls != calls {
		t.Error("Verify() fetched from the network for a fixture it cannot check")
	}
}
