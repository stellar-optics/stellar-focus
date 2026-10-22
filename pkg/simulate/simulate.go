// Package simulate runs a preflight simulation of a Soroban transaction.
//
// A simulation runs the transaction against the current ledger state without
// submitting it, and reports what would have happened: the value the
// invocation would return, the ledger entries it would read and write, and
// the resources it would consume. That makes a resource regression — a
// contract invocation that suddenly needs ten times the instructions —
// visible before the transaction is submitted, rather than after it has
// failed on chain.
//
// # A simulation is not a fixture
//
// A simulation runs against the state of the ledger as it is right now. The
// same transaction simulated twice, seconds apart, can report a different
// footprint and a different resource usage, because the ledger moved in
// between. Assert on a simulation with a budget and a shape — "fewer than
// this many instructions", "returns this value" — rather than with an exact
// snapshot, and expect the numbers to move with the chain.
//
// # The network stays outside
//
// This package is where the network call lives. pkg/focus and pkg/golden
// never make one: the assertions take the result this package produces and
// nothing else.
//
// The seam is the Source interface. capture.RPCSource implements it against
// a real endpoint, and a test substitutes a canned response so a suite stays
// offline.
package simulate

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Source is the seam a simulation runs through.
//
// capture.RPCSource implements it against a real endpoint; a test implements
// it with a canned response.
type Source interface {
	// SimulateTransaction runs one simulateTransaction request.
	SimulateTransaction(
		ctx context.Context,
		req protocol.SimulateTransactionRequest,
	) (*protocol.SimulateTransactionResponse, error)
}

// SimulateResult holds the outcome of a simulateTransaction call.
//
//nolint:revive // the package name is part of the name the issue asks for.
type SimulateResult struct {
	// ReturnValue is the value the invocation returned, when it returned
	// one. It is nil for a transaction that returns nothing, and for a
	// simulation that was rejected.
	ReturnValue *xdr.ScVal
	// Footprint is the set of ledger entries the transaction would read and
	// write. It is nil when the host reported no transaction data.
	Footprint *xdr.LedgerFootprint
	// Instructions is the number of instructions the transaction would
	// consume.
	Instructions uint32
	// ReadBytes is the number of ledger bytes the transaction would read.
	ReadBytes uint32
	// WriteBytes is the number of ledger bytes the transaction would write.
	WriteBytes uint32
	// Error is the host's rejection message. It is empty when the
	// simulation ran.
	Error string
	// LatestLedger is the ledger the simulation ran against, which is
	// what makes the result a statement about a moment in time.
	LatestLedger uint32
}

// LedgerEntries reports how many ledger entries the footprint reads and
// writes.
//
// It is the shape of a simulation rather than its size: a transaction that
// suddenly reads fifty entries where it used to read two is worth knowing
// about even when its instruction count has not moved.
func (r *SimulateResult) LedgerEntries() (read, write int) {
	if r == nil || r.Footprint == nil {
		return 0, 0
	}
	return len(r.Footprint.ReadOnly), len(r.Footprint.ReadWrite)
}

// Simulate runs one preflight simulation of an envelope.
//
// envelopeXDR is the base64 XDR of the transaction envelope to simulate.
//
// A simulation the host rejected is not an error here: the host answered,
// and what it said is that the transaction would not run. The rejection is
// recorded in the result's Error field, and focus.AssertSimulationSucceeded
// is what reports it. An error is returned when the call itself failed —
// the endpoint was unreachable, or the answer could not be read.
//
// The result is a statement about the ledger as it is now, not a fixture:
// the same envelope simulated twice can report different resources.
func Simulate(ctx context.Context, src Source, envelopeXDR string) (*SimulateResult, error) {
	if src == nil {
		return nil, errors.New("simulate: no source to simulate against")
	}

	envelopeXDR = strings.TrimSpace(envelopeXDR)
	if envelopeXDR == "" {
		return nil, errors.New("simulate: no envelope to simulate")
	}

	resp, err := src.SimulateTransaction(ctx, protocol.SimulateTransactionRequest{
		Transaction: envelopeXDR,
	})
	if err != nil {
		return nil, fmt.Errorf("simulate: %w", err)
	}
	if resp == nil {
		return nil, errors.New("simulate: the endpoint returned no response")
	}

	result := &SimulateResult{
		Error:        resp.Error,
		LatestLedger: resp.LatestLedger,
	}
	if resp.Error != "" {
		// The host rejected the transaction. Everything else it said is
		// absent, and the rejection is the whole result.
		return result, nil
	}

	if err := readTransactionData(resp, result); err != nil {
		return nil, err
	}
	if err := readReturnValue(resp, result); err != nil {
		return nil, err
	}
	return result, nil
}

// readTransactionData decodes the transaction data the host returned, which
// is where the footprint and the resource usage live.
func readTransactionData(resp *protocol.SimulateTransactionResponse, result *SimulateResult) error {
	if resp.TransactionDataXDR == "" {
		return nil
	}

	raw, err := base64.StdEncoding.DecodeString(resp.TransactionDataXDR)
	if err != nil {
		return fmt.Errorf("simulate: decoding the transaction data: %w", err)
	}

	var data xdr.SorobanTransactionData
	if _, err := xdr.Unmarshal(bytes.NewReader(raw), &data); err != nil {
		return fmt.Errorf("simulate: parsing the transaction data: %w", err)
	}

	footprint := data.Resources.Footprint
	result.Footprint = &footprint
	result.Instructions = uint32(data.Resources.Instructions)
	result.ReadBytes = uint32(data.Resources.DiskReadBytes)
	result.WriteBytes = uint32(data.Resources.WriteBytes)
	return nil
}

// readReturnValue decodes the value the invocation returned.
//
// A simulation of a transaction with more than one host function carries a
// result for each; the first one with a value is the one a test means.
func readReturnValue(resp *protocol.SimulateTransactionResponse, result *SimulateResult) error {
	for _, hostResult := range resp.Results {
		if hostResult.ReturnValueXDR == nil || *hostResult.ReturnValueXDR == "" {
			continue
		}

		raw, err := base64.StdEncoding.DecodeString(*hostResult.ReturnValueXDR)
		if err != nil {
			return fmt.Errorf("simulate: decoding the return value: %w", err)
		}

		var value xdr.ScVal
		if _, err := xdr.Unmarshal(bytes.NewReader(raw), &value); err != nil {
			return fmt.Errorf("simulate: parsing the return value: %w", err)
		}
		result.ReturnValue = &value
		return nil
	}
	return nil
}
