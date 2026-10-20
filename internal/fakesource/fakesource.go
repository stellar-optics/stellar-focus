// Package fakesource provides an in-memory capture.Source for tests.
//
// Capture and verify are the only parts of stellar-focus that touch the
// network, so they are the only parts that need a fake. Everything else is
// already offline by construction.
package fakesource

import (
	"context"
	"fmt"
	"sync"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// Source is a scriptable capture.Source.
//
// It is safe for concurrent use, because the batch capture calls it from
// several goroutines at once.
type Source struct {
	// Transactions maps a hash to the records returned for it.
	Transactions map[string][]capture.Record
	// Entries maps a base64 ledger key to the records returned for it.
	Entries map[string][]capture.Record
	// Err, when set, is returned by every call, simulating an endpoint that
	// is unreachable.
	Err error
	// Passphrase and PassphraseErr configure optional network metadata.
	Passphrase    string
	PassphraseErr error
	// Calls counts fetches, so tests can assert nothing was fetched when
	// nothing should have been.
	Calls int

	network  string
	endpoint string

	mu sync.Mutex
}

// New returns a fake pointed at a nominal network.
func New(network, endpoint string) *Source {
	return &Source{
		Transactions: map[string][]capture.Record{},
		Entries:      map[string][]capture.Record{},
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

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.PassphraseErr != nil {
		return "", s.PassphraseErr
	}
	return s.Passphrase, nil
}

// Transaction implements capture.Source.
func (s *Source) Transaction(ctx context.Context, hash string) ([]capture.Record, error) {
	s.mu.Lock()
	s.Calls++
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Err != nil {
		return nil, s.Err
	}

	s.mu.Lock()
	records, ok := s.Transactions[hash]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("transaction %s: %w", hash, capture.ErrNotFound)
	}
	return records, nil
}

// LedgerEntry implements capture.Source.
func (s *Source) LedgerEntry(ctx context.Context, key string) ([]capture.Record, error) {
	s.mu.Lock()
	s.Calls++
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Err != nil {
		return nil, s.Err
	}

	s.mu.Lock()
	records, ok := s.Entries[key]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("ledger key %s: %w", key, capture.ErrNotFound)
	}
	return records, nil
}
