package simulate_test

import (
	"context"
	"strings"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/simulate"
)

// The envelope is a placeholder: the simulation is a preflight of whatever
// the caller passes, and the fake answers by envelope.
const testEnvelope = "AAAA envelope"

func TestSimulateReadsTheResourceUsageAndReturnValue(t *testing.T) {
	t.Parallel()

	src := fakesource.New("testnet", "https://fake.example")
	src.Simulations[testEnvelope] = &protocol.SimulateTransactionResponse{
		LatestLedger:       1234,
		TransactionDataXDR: transactionData(t, 500_000, 4096, 8192),
		Results: []protocol.SimulateHostFunctionResult{
			{ReturnValueXDR: stringPtr(returnValue(t, 7))},
		},
	}

	result, err := simulate.Simulate(context.Background(), src, testEnvelope)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	if result.Error != "" {
		t.Errorf("the simulation reported an error: %s", result.Error)
	}
	if result.Instructions != 500_000 {
		t.Errorf("Instructions = %d, want 500000", result.Instructions)
	}
	if result.ReadBytes != 4096 {
		t.Errorf("ReadBytes = %d, want 4096", result.ReadBytes)
	}
	if result.WriteBytes != 8192 {
		t.Errorf("WriteBytes = %d, want 8192", result.WriteBytes)
	}
	if result.LatestLedger != 1234 {
		t.Errorf("LatestLedger = %d, want 1234", result.LatestLedger)
	}
	if result.Footprint == nil {
		t.Fatal("the footprint was not read")
	}
	read, write := result.LedgerEntries()
	if read != 1 || write != 1 {
		t.Errorf("LedgerEntries() = %d read, %d write, want 1 and 1", read, write)
	}
	if result.ReturnValue == nil {
		t.Fatal("the return value was not read")
	}
	if got := result.ReturnValue.U32; got == nil || *got != 7 {
		t.Errorf("the return value is %v, want u32(7)", result.ReturnValue)
	}
}

// TestSimulateRecordsARejection checks that a transaction the host would not
// run is a result rather than an error: the rejection is the answer.
func TestSimulateRecordsARejection(t *testing.T) {
	t.Parallel()

	src := fakesource.New("testnet", "https://fake.example")
	src.Simulations[testEnvelope] = &protocol.SimulateTransactionResponse{
		Error: "HostError: Error(Contract, #1)",
	}

	result, err := simulate.Simulate(context.Background(), src, testEnvelope)
	if err != nil {
		t.Fatalf("Simulate() error = %v; a rejection is a result, not a failure", err)
	}
	if result == nil {
		t.Fatal("Simulate() returned no result")
	}
	if !strings.Contains(result.Error, "Error(Contract, #1)") {
		t.Errorf("the rejection message was lost: %q", result.Error)
	}
	if result.ReturnValue != nil {
		t.Error("a rejected simulation reported a return value")
	}
}

func TestSimulateReportsAFailedCall(t *testing.T) {
	t.Parallel()

	src := fakesource.New("testnet", "https://fake.example")
	src.SimulationErr = context.DeadlineExceeded

	_, err := simulate.Simulate(context.Background(), src, testEnvelope)
	if err == nil {
		t.Fatal("Simulate() returned no error for a failed call")
	}
	if !strings.Contains(err.Error(), "simulate") {
		t.Errorf("the error does not say what failed: %v", err)
	}
}

func TestSimulateRejectsAnEmptyEnvelope(t *testing.T) {
	t.Parallel()

	src := fakesource.New("testnet", "https://fake.example")

	if _, err := simulate.Simulate(context.Background(), src, "  "); err == nil {
		t.Fatal("Simulate() accepted an empty envelope")
	}
	if src.Calls != 0 {
		t.Errorf("the endpoint was contacted %d time(s) for an empty envelope", src.Calls)
	}
}

// TestSimulateStaysOffline is the promise the package makes: the simulation
// runs against whatever the source answers, and a test answers from memory.
func TestSimulateStaysOffline(t *testing.T) {
	t.Parallel()

	src := fakesource.New("testnet", "https://fake.example")
	src.Simulations[testEnvelope] = &protocol.SimulateTransactionResponse{LatestLedger: 1}

	if _, err := simulate.Simulate(context.Background(), src, testEnvelope); err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if src.Calls != 1 {
		t.Errorf("the source was called %d time(s), want 1", src.Calls)
	}
}

// transactionData encodes the transaction data the host returns: the
// footprint plus the resources the simulation would consume.
func transactionData(t *testing.T, instructions, readBytes, writeBytes uint32) string {
	t.Helper()

	data := xdr.SorobanTransactionData{
		Resources: xdr.SorobanResources{
			Footprint: xdr.LedgerFootprint{
				ReadOnly:  []xdr.LedgerKey{contractDataKey(t, "config")},
				ReadWrite: []xdr.LedgerKey{contractDataKey(t, "counter")},
			},
			Instructions:  xdr.Uint32(instructions),
			DiskReadBytes: xdr.Uint32(readBytes),
			WriteBytes:    xdr.Uint32(writeBytes),
		},
		ResourceFee: xdr.Int64(100),
	}

	encoded, err := xdr.MarshalBase64(data)
	if err != nil {
		t.Fatalf("encoding the transaction data: %v", err)
	}
	return encoded
}

// contractDataKey builds a ledger key for one contract's data entry.
func contractDataKey(t *testing.T, name string) xdr.LedgerKey {
	t.Helper()

	var contractID xdr.ContractId
	copy(contractID[:], []byte("focus-test-contract-id-32bytes!!"))
	contract := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &contractID,
	}

	sym := xdr.ScSymbol(name)
	return xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.LedgerKeyContractData{
			Contract:   contract,
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Durability: xdr.ContractDataDurabilityPersistent,
		},
	}
}

// returnValue encodes the value an invocation returned.
func returnValue(t *testing.T, v uint32) string {
	t.Helper()

	value := xdr.Uint32(v)
	encoded, err := xdr.MarshalBase64(xdr.ScVal{
		Type: xdr.ScValTypeScvU32,
		U32:  &value,
	})
	if err != nil {
		t.Fatalf("encoding the return value: %v", err)
	}
	return encoded
}

func stringPtr(s string) *string { return &s }
