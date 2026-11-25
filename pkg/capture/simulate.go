package capture

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stellar/go-stellar-sdk/xdr"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
)

// SimulationSource serves simulateTransaction responses.
//
// Like Source, it is a seam: the network-backed implementation calls a real
// RPC endpoint, and tests substitute a canned response so a capture can be
// built and asserted on offline.
type SimulationSource interface {
	// Network reports which network the source is pointed at.
	Network() string
	// Endpoint reports the URL, for provenance.
	Endpoint() string
	// SimulateTransaction runs one simulateTransaction request.
	SimulateTransaction(ctx context.Context, req protocol.SimulateTransactionRequest) (*protocol.SimulateTransactionResponse, error)
}

// ErrSimulationFailed is returned when the RPC accepted the request but the
// host rejected the invocation: the response carries an error instead of a
// result. The cause says where the host stopped, which is exactly what a
// preflight wants to know.
var ErrSimulationFailed = errors.New("simulation failed")

// SimulationFormat identifies the JSON layout written by Save.
const SimulationFormat = "focus/simulation/v1"

// Simulation is one captured simulateTransaction response.
//
// The full RPC response is kept, because the point of a simulation capture is
// to replay it offline: the return value, diagnostic events, authorization
// trees and resource footprint are all in there, and projecting them into a
// smaller struct would lose the parts a later assertion needs. Network,
// Endpoint and Request record the context that produced it.
//
// For guide and best practices on preflight simulation and resource assertions,
// see docs/guides/simulation_testing.md.
type Simulation struct {
	// Format identifies the serialized layout.
	Format string `json:"format,omitempty"`
	// Network and Endpoint locate the source the simulation ran against.
	Network  string `json:"network,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// Request is the simulateTransaction request that was sent.
	Request *protocol.SimulateTransactionRequest `json:"request,omitempty"`
	// Resp is the RPC's own response, unmodified, including its error field
	// when Simulate passed it through for inspection.
	Resp *protocol.SimulateTransactionResponse `json:"response"`
}

// CaptureSimulation is the explicit name for a recorded simulation. The two
// names are interchangeable so existing callers can keep using Simulation.
//
//nolint:revive // The public name is fixed by issue #238.
type CaptureSimulation = Simulation

// AuthTree is the authorization entries recorded for one host function result.
//
// The RPC returns one list per result, which is the authorization "tree" as it
// appears on the wire: each entry is a base64 ContractAuth a replay can decode.
type AuthTree struct {
	Operation int      `json:"operation"`
	Entries   []string `json:"entries"`
}

// AuthTrees returns the authorization trees recorded in the response, one per
// host function result that carried any.
func (s *Simulation) AuthTrees() []AuthTree {
	if s == nil || s.Resp == nil {
		return nil
	}
	var out []AuthTree
	for i, result := range s.Resp.Results {
		if result.AuthXDR == nil || len(*result.AuthXDR) == 0 {
			continue
		}
		entries := append([]string(nil), (*result.AuthXDR)...)
		out = append(out, AuthTree{Operation: i, Entries: entries})
	}
	return out
}

// ResourceFootprint is the resource allocation a simulation reported, decoded
// from its SorobanTransactionData.
type ResourceFootprint struct {
	Instructions  uint32   `json:"instructions"`
	DiskReadBytes uint32   `json:"diskReadBytes"`
	WriteBytes    uint32   `json:"writeBytes"`
	ResourceFee   int64    `json:"resourceFee"`
	ReadOnly      []string `json:"readOnly,omitempty"`
	ReadWrite     []string `json:"readWrite,omitempty"`
}

// ResourceFootprint decodes the transactionData allocation the simulation
// reported. A response without transaction data has no footprint and returns
// nil rather than an error, because a failed simulation legitimately has none.
func (s *Simulation) ResourceFootprint() (*ResourceFootprint, error) {
	if s == nil || s.Resp == nil || s.Resp.TransactionDataXDR == "" {
		return nil, nil
	}

	raw, err := base64.StdEncoding.DecodeString(s.Resp.TransactionDataXDR)
	if err != nil {
		return nil, fmt.Errorf("decoding simulation transaction data: %w", err)
	}

	var data xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parsing simulation transaction data: %w", err)
	}

	footprint := &ResourceFootprint{
		Instructions:  uint32(data.Resources.Instructions),
		DiskReadBytes: uint32(data.Resources.DiskReadBytes),
		WriteBytes:    uint32(data.Resources.WriteBytes),
		ResourceFee:   int64(data.ResourceFee),
	}
	footprint.ReadOnly, err = encodeLedgerKeys(data.Resources.Footprint.ReadOnly)
	if err != nil {
		return nil, err
	}
	footprint.ReadWrite, err = encodeLedgerKeys(data.Resources.Footprint.ReadWrite)
	if err != nil {
		return nil, err
	}
	return footprint, nil
}

// encodeLedgerKeys renders footprint keys as base64, the form a replay hands
// back to getLedgerEntries.
func encodeLedgerKeys(keys []xdr.LedgerKey) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		raw, err := key.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("encoding footprint ledger key: %w", err)
		}
		out = append(out, base64.StdEncoding.EncodeToString(raw))
	}
	return out, nil
}

// Save writes the captured simulation as JSON so it can be replayed without a
// network. The source context and the full response travel with it.
func (s *Simulation) Save(path string) error {
	if s == nil || s.Resp == nil {
		return fmt.Errorf("saving simulation capture: no response to write")
	}
	capture := *s
	if capture.Format == "" {
		capture.Format = SimulationFormat
	}
	data, err := json.MarshalIndent(&capture, "", "  ")
	if err != nil {
		return fmt.Errorf("rendering simulation capture: %w", err)
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// LoadCaptureSimulation reads a simulation capture written by Save.
func LoadCaptureSimulation(path string) (*CaptureSimulation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var capture CaptureSimulation
	if err := json.Unmarshal(data, &capture); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if capture.Resp == nil {
		return nil, fmt.Errorf("parsing %s: capture has no response", path)
	}
	return &capture, nil
}

// Simulate runs one preflight simulation and records it.
//
// A host-rejected invocation is reported as an error wrapping
// ErrSimulationFailed rather than returned as an unmarked success: a capture
// whose result is an error string is still information, but the caller must
// handle both branches explicitly, and the error chain carries the host's
// message for that.
func Simulate(ctx context.Context, src SimulationSource, req protocol.SimulateTransactionRequest) (*Simulation, error) {
	resp, err := src.SimulateTransaction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("simulateTransaction: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("simulateTransaction: endpoint returned no response")
	}

	simulation := &Simulation{
		Format:   SimulationFormat,
		Network:  src.Network(),
		Endpoint: src.Endpoint(),
		Request:  &req,
		Resp:     resp,
	}
	if resp.Error != "" {
		return simulation, fmt.Errorf("simulateTransaction: %w: %s", ErrSimulationFailed, resp.Error)
	}
	return simulation, nil
}

// Records splits the response into capture records, one per XDR payload, so
// each can become its own fixture.
//
// The resource footprint and resource fee live inside the SorobanTransactionData
// record, so capturing that one record captures both. The authorization tree
// comes back as one base64 ContractAuth per required address, each its own
// record, which is what "authorization tree" means in the RPC response: a
// list, not a single structure.
func (s *Simulation) Records() []Record {
	if s == nil || s.Resp == nil {
		return nil
	}
	resp := s.Resp
	ledger := resp.LatestLedger

	context := fmt.Sprintf("simulation against %s (min resource fee %d)", ledgerName(ledger), resp.MinResourceFee)
	record := func(name, typ, xdr string) Record {
		return Record{
			Name:        name,
			Type:        typ,
			XDR:         xdr,
			Ledger:      ledger,
			Description: context,
		}
	}

	var out []Record
	if resp.TransactionDataXDR != "" {
		out = append(out,
			record("tx-data", "SorobanTransactionData", resp.TransactionDataXDR))
	}
	for i, ev := range resp.EventsXDR {
		out = append(out, record(fmt.Sprintf("event-%d", i+1), "DiagnosticEvent", ev))
	}
	for op, res := range resp.Results {
		if res.AuthXDR != nil {
			for j, a := range *res.AuthXDR {
				size := len(*res.AuthXDR)
				out = append(out, record(
					authName(op, j, size),
					"ContractAuth", a))
			}
		}
		if res.ReturnValueXDR != nil && *res.ReturnValueXDR != "" {
			out = append(out, record("return", "ScVal", *res.ReturnValueXDR))
		}
	}
	return out
}

// ledgerName labels the ledger a simulation ran against, only for
// descriptions; zero means the endpoint did not report one.
func ledgerName(ledger uint32) string {
	if ledger == 0 {
		return "unknown ledger"
	}
	return fmt.Sprintf("ledger %d", ledger)
}

// authName names one authorization record within one host function result:
// the RPC returns an authorization list per host function, so the name says
// which tree and which entry it is.
func authName(op, idx, size int) string {
	if size == 1 {
		return fmt.Sprintf("auth-%d", op)
	}
	return fmt.Sprintf("auth-%d-%d", op, idx)
}

// SimulateTransaction implements SimulationSource against the real endpoint.
//
// It lives on RPCSource rather than a separate type so one configured source
// serves both transaction history, ledger entries and preflight simulations.
func (s *RPCSource) SimulateTransaction(ctx context.Context, req protocol.SimulateTransactionRequest) (*protocol.SimulateTransactionResponse, error) {
	resp, err := s.client.SimulateTransaction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("rpc simulateTransaction: %w", err)
	}
	return &resp, nil
}
