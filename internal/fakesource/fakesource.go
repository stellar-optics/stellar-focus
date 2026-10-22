// Package fakesource provides an in-memory capture.Source for tests.
//
// Capture, verify and simulate are the only parts of stellar-focus that
// touch the network, so they are the only parts that need a fake.
// Everything else is already offline by construction.
package fakesource

import (
	"context"
	"fmt"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// Source is a scriptable capture.Source.
type Source struct {
	// Transactions maps a hash to the records returned for it.
	Transactions map[string][]capture.Record
	// Entries maps a base64 ledger key to the records returned for it.
	Entries map[string][]capture.Record
	// Simulations maps a base64 envelope to the simulation response
	// returned for it.
	Simulations map[string]*protocol.SimulateTransactionResponse
	// Err, when set, is returned by every call, simulating an endpoint that
	// is unreachable.
	Err error
	// SimulationErr, when set, is returned by every simulation, simulating
	// an endpoint that cannot answer a preflight.
	SimulationErr error
	// Passphrase and PassphraseErr configure optional network metadata.
	Passphrase    string
	PassphraseErr error
	// Calls counts fetches, so tests can assert nothing was fetched when
	// nothing should have been.
	Calls int

	network  string
	endpoint string
}

// New returns a fake pointed at a nominal network.
func New(network, endpoint string) *Source {
	return &Source{
		Transactions: map[string][]capture.Record{},
		Entries:      map[string][]capture.Record{},
		Simulations:  map[string]*protocol.SimulateTransactionResponse{},
		network:      network,
		endpoint:     endpoint,
	}
}

// Name implements capture.Source.
func (s *Source) Name() string { return "fake" }

// Network implements capture.Source.
func (s *Source) Network() string { return s.network }

// Endpoint implements capture.Source.
func (s *Source) Endpoint() string { return s.endpoint }

// NetworkPassphrase implements capture.NetworkPassphraseSource.
func (s *Source) NetworkPassphrase(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.PassphraseErr != nil {
		return "", s.PassphraseErr
	}
	return s.Passphrase, nil
}

// Transaction implements capture.Source.
func (s *Source) Transaction(ctx context.Context, hash string) ([]capture.Record, error) {
	s.Calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Err != nil {
		return nil, s.Err
	}
	records, ok := s.Transactions[hash]
	if !ok {
		return nil, fmt.Errorf("transaction %s: %w", hash, capture.ErrNotFound)
	}
	return records, nil
}

// LedgerEntry implements capture.Source.
func (s *Source) LedgerEntry(ctx context.Context, key string) ([]capture.Record, error) {
	s.Calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Err != nil {
		return nil, s.Err
	}
	records, ok := s.Entries[key]
	if !ok {
		return nil, fmt.Errorf("ledger key %s: %w", key, capture.ErrNotFound)
	}
	return records, nil
}

// SimulateTransaction implements simulate.Source, so a suite can preflight a
// transaction offline.
//
// The response is the one canned for the envelope in the request; an
// envelope with no canned response is an error rather than an empty
// simulation, so a test that forgot to set one up fails loudly.
func (s *Source) SimulateTransaction(
	ctx context.Context,
	req protocol.SimulateTransactionRequest,
) (*protocol.SimulateTransactionResponse, error) {
	s.Calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.SimulationErr != nil {
		return nil, s.SimulationErr
	}
	resp, ok := s.Simulations[req.Transaction]
	if !ok {
		return nil, fmt.Errorf("no simulation is canned for that envelope")
	}
	return resp, nil
}
