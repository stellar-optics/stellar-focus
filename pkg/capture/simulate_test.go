package capture

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
)

// fakeSimulationSource is an offline SimulationSource standing in for the
// network, per the offline-first rule.
type fakeSimulationSource struct {
	network  string
	endpoint string
	resp     *protocol.SimulateTransactionResponse
	err      error
}

func (f *fakeSimulationSource) Network() string  { return f.network }
func (f *fakeSimulationSource) Endpoint() string { return f.endpoint }
func (f *fakeSimulationSource) SimulateTransaction(ctx context.Context, req protocol.SimulateTransactionRequest) (*protocol.SimulateTransactionResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestCaptureSimulationRecords(t *testing.T) {
	auth := []string{"base64ContractAuth1", "base64ContractAuth2"}
	ret := "base64ReturnValue"
	src := &fakeSimulationSource{
		network:  NetworkTestnet,
		endpoint: DefaultEndpoints[NetworkTestnet],
		resp: &protocol.SimulateTransactionResponse{
			LatestLedger:       700,
			MinResourceFee:     1234,
			TransactionDataXDR: "base64TxData",
			EventsXDR:          []string{"base64EventA", "base64EventB", "base64EventC"},
			Results: []protocol.SimulateHostFunctionResult{
				{AuthXDR: &auth, ReturnValueXDR: &ret},
			},
		},
	}

	sim, err := Simulate(context.Background(), src, protocol.SimulateTransactionRequest{Transaction: "base64Envelope"})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}

	got := sim.Records()
	// tx-data, 3 events, 2 auths, 1 return value.
	if want := 7; len(got) != want {
		t.Fatalf("want %d records, got %d: %+v", want, len(got), got)
	}

	byName := make(map[string]Record, len(got))
	for _, r := range got {
		byName[r.Name] = r
	}

	txData, ok := byName["tx-data"]
	if !ok {
		t.Fatalf("no tx-data record in %+v", got)
	}
	if txData.Type != "SorobanTransactionData" || txData.XDR != "base64TxData" {
		t.Errorf("tx-data record wrong: %+v", txData)
	}
	if txData.Ledger != 700 || txData.TxHash != "" {
		t.Errorf("tx-data provenance wrong: ledger=%d txHash=%q", txData.Ledger, txData.TxHash)
	}
	if txData.Description == "" {
		t.Error("tx-data description must name ledger and min resource fee")
	}

	// Events are 1-based so record names match the RPC's own numbering.
	if byName["event-1"].XDR != "base64EventA" || byName["event-3"].XDR != "base64EventC" {
		t.Errorf("event records wrong: %+v %+v", byName["event-1"], byName["event-3"])
	}

	// One host function result carrying a two-entry authorization tree:
	// entries are keyed op-then-entry so the name locates a node in a
	// multi-entry tree rather than an index into one list.
	if byName["auth-0-0"].XDR != "base64ContractAuth1" || byName["auth-0-1"].XDR != "base64ContractAuth2" {
		t.Errorf("auth records wrong: %+v %+v", byName["auth-0-0"], byName["auth-0-1"])
	}
	if byName["auth-0-0"].Type != "ContractAuth" {
		t.Errorf("auth record type wrong: %q", byName["auth-0-0"].Type)
	}
}

func TestCaptureSimulationFailedReportsHostError(t *testing.T) {
	src := &fakeSimulationSource{
		resp: &protocol.SimulateTransactionResponse{
			Error: "HostError: Contract(ICall)",
		},
	}
	sim, err := Simulate(context.Background(), src, protocol.SimulateTransactionRequest{})
	if sim == nil {
		t.Fatalf("Simulate returned nil Simulation with error; want the response for inspection")
	}
	if !errors.Is(err, ErrSimulationFailed) {
		t.Fatalf("want ErrSimulationFailed, got: %v", err)
	}
	if !contains(err.Error(), "HostError: Contract(ICall)") {
		t.Errorf("error lost the host message: %v", err)
	}
	if got := sim.Records(); len(got) != 0 {
		t.Errorf("failed simulation must produce no records, got %+v", got)
	}
}

func TestCaptureSimulationRecordsAuthTreesAndFootprint(t *testing.T) {
	auth := []string{"authEntryA", "authEntryB"}
	transactionData := xdr.SorobanTransactionData{
		Ext: xdr.SorobanTransactionDataExt{V: 0},
		Resources: xdr.SorobanResources{
			Instructions:  1_000_000,
			DiskReadBytes: 4096,
			WriteBytes:    1024,
		},
		ResourceFee: 54321,
	}
	raw, err := transactionData.MarshalBinary()
	if err != nil {
		t.Fatalf("marshalling transaction data: %v", err)
	}

	sim := &Simulation{
		Resp: &protocol.SimulateTransactionResponse{
			TransactionDataXDR: base64.StdEncoding.EncodeToString(raw),
			Results: []protocol.SimulateHostFunctionResult{
				{AuthXDR: &auth},
			},
		},
	}

	trees := sim.AuthTrees()
	if len(trees) != 1 {
		t.Fatalf("want one auth tree, got %+v", trees)
	}
	if trees[0].Operation != 0 || len(trees[0].Entries) != 2 || trees[0].Entries[1] != "authEntryB" {
		t.Errorf("auth tree is incomplete: %+v", trees[0])
	}

	footprint, err := sim.ResourceFootprint()
	if err != nil {
		t.Fatalf("ResourceFootprint: %v", err)
	}
	if footprint == nil {
		t.Fatal("ResourceFootprint returned nothing for a response with transaction data")
	}
	if footprint.Instructions != 1_000_000 || footprint.DiskReadBytes != 4096 || footprint.WriteBytes != 1024 {
		t.Errorf("resource allocations were not decoded: %+v", footprint)
	}
	if footprint.ResourceFee != 54321 {
		t.Errorf("resource fee = %d, want 54321", footprint.ResourceFee)
	}
}

func TestCaptureSimulationJSONRoundTrip(t *testing.T) {
	auth := []string{"authEntryA"}
	returnValue := "returnValueXDR"
	src := &fakeSimulationSource{network: NetworkTestnet, endpoint: "https://rpc.example"}
	resp := &protocol.SimulateTransactionResponse{
		LatestLedger:   900,
		MinResourceFee: 77,
		Results: []protocol.SimulateHostFunctionResult{
			{AuthXDR: &auth, ReturnValueXDR: &returnValue},
		},
	}

	sim := &Simulation{
		Format:   SimulationFormat,
		Network:  src.Network(),
		Endpoint: src.Endpoint(),
		Request:  &protocol.SimulateTransactionRequest{Transaction: "envelopeXDR"},
		Resp:     resp,
	}

	path := filepath.Join(t.TempDir(), "simulation.json")
	if err := sim.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadCaptureSimulation(path)
	if err != nil {
		t.Fatalf("LoadCaptureSimulation: %v", err)
	}
	if loaded.Format != SimulationFormat || loaded.Network != NetworkTestnet || loaded.Endpoint != "https://rpc.example" {
		t.Errorf("source context did not survive the round trip: %+v", loaded)
	}
	if loaded.Resp == nil || loaded.Resp.LatestLedger != 900 || loaded.Resp.MinResourceFee != 77 {
		t.Errorf("response did not survive the round trip: %+v", loaded.Resp)
	}
	if loaded.Request == nil || loaded.Request.Transaction != "envelopeXDR" {
		t.Errorf("request did not survive the round trip: %+v", loaded.Request)
	}
	if trees := loaded.AuthTrees(); len(trees) != 1 || trees[0].Entries[0] != "authEntryA" {
		t.Errorf("auth tree did not survive the round trip: %+v", trees)
	}
}

func TestCaptureSimulationNetworkError(t *testing.T) {
	src := &fakeSimulationSource{err: fmt.Errorf("connection refused")}
	_, err := Simulate(context.Background(), src, protocol.SimulateTransactionRequest{})
	if err == nil {
		t.Fatal("want an error on transport failure")
	}
	if errors.Is(err, ErrSimulationFailed) {
		t.Errorf("transport failure is not a host rejection: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; len(s) >= len(sub) && i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
