// Package capture pulls real data off a Stellar network and records it as a
// golden fixture.
//
// This is the only part of stellar-focus that touches the network, and it is
// kept behind the Source interface so everything above it is testable
// offline. The focus command wires in an RPC-backed Source; tests wire in a
// fake.
package capture

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// Networks recognised by name, so a capture records where it came from
// without the user restating the endpoint.
const (
	NetworkTestnet = "testnet"
	NetworkMainnet = "mainnet"
	NetworkCustom  = "custom"
)

// DefaultEndpoints maps a network name to its public RPC endpoint.
var DefaultEndpoints = map[string]string{
	NetworkTestnet: "https://soroban-testnet.stellar.org",
	NetworkMainnet: "https://mainnet.sorobanrpc.com",
}

// ErrNotFound is returned when the requested item is not on the network, or
// has aged out of the endpoint's retention window.
var ErrNotFound = errors.New("not found on this network")

// Record is one captured item, before it becomes a fixture file.
type Record struct {
	// Name suggests a fixture filename, without a suffix.
	Name string
	// Type is the XDR type, e.g. "TransactionEnvelope".
	Type string
	// XDR is the base64 payload.
	XDR string
	// Ledger and TxHash locate the item on the chain.
	Ledger uint32
	TxHash string
	// Description is a human note recorded in the fixture.
	Description string
}

// Source fetches data from a network.
//
// It is the seam that keeps the network out of the rest of the library. To
// capture from somewhere new — Horizon, a local core instance, an archive —
// implement this interface; nothing above it needs to change.
type Source interface {
	// Name identifies the source in messages, e.g. "soroban-rpc".
	Name() string
	// Network reports which network the source is pointed at.
	Network() string
	// Endpoint reports the URL, for provenance.
	Endpoint() string
	// Transaction fetches a transaction by hash, returning its envelope,
	// result and meta as separate records.
	Transaction(ctx context.Context, hash string) ([]Record, error)
	// LedgerEntry fetches a ledger entry by its base64 LedgerKey.
	LedgerEntry(ctx context.Context, key string) ([]Record, error)
}

// Capture fetches items and turns them into fixtures, stamping each with the
// provenance needed to verify or refresh it later.
//
// now is passed in rather than read from the clock so that callers — and
// tests — control the timestamp written into the fixture.
func Capture(ctx context.Context, src Source, records []Record, now time.Time) ([]*golden.Fixture, error) {
	out := make([]*golden.Fixture, 0, len(records))
	for _, r := range records {
		provenance := golden.NewProvenance(src.Network(), src.Endpoint(), r.Ledger, r.TxHash, now)

		fixture, err := golden.Build(r.XDR, r.Type, r.Description, provenance)
		if err != nil {
			return nil, fmt.Errorf("building fixture %q: %w", r.Name, err)
		}
		out = append(out, fixture)
	}
	return out, nil
}

// VerifyResult reports whether a stored fixture still matches the chain.
type VerifyResult struct {
	// Path is the fixture that was checked.
	Path string
	// Match reports whether the stored XDR still matches what the network
	// returns.
	Match bool
	// Diff describes what changed, when it did not match.
	Diff *lens.DiffResult
	// Err is set when the fixture could not be checked at all — no
	// provenance to re-fetch from, or the item is gone from the network.
	Err error
}

// Verify re-fetches a fixture from the network and compares it with what is
// stored.
//
// A fixture with no provenance cannot be verified, and that is reported
// rather than silently passing: a hand-written fixture is not stale, it is
// simply not checkable this way.
func Verify(ctx context.Context, src Source, path string, f *golden.Fixture) VerifyResult {
	result := VerifyResult{Path: path}

	if f.Captured == nil || f.Captured.TxHash == "" {
		result.Err = fmt.Errorf(
			"no capture provenance, so there is nothing to re-fetch (was this fixture written by hand?)")
		return result
	}

	records, err := src.Transaction(ctx, f.Captured.TxHash)
	if err != nil {
		result.Err = fmt.Errorf("re-fetching %s: %w", f.Captured.TxHash, err)
		return result
	}

	for _, r := range records {
		if r.Type != f.Type {
			continue
		}
		if r.XDR == f.XDR {
			result.Match = true
			return result
		}

		stored, sErr := lens.DecodeAs(f.XDR, f.Type)
		fresh, fErr := lens.DecodeAs(r.XDR, r.Type)
		if sErr == nil && fErr == nil {
			if diff, dErr := lens.Diff(stored, fresh); dErr == nil {
				result.Diff = diff
			}
		}
		return result
	}

	result.Err = fmt.Errorf("the network no longer returns a %s for transaction %s",
		f.Type, f.Captured.TxHash)
	return result
}

// ResolveEndpoint picks the URL for a network, honouring an explicit
// override.
func ResolveEndpoint(network, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	url, ok := DefaultEndpoints[network]
	if !ok {
		return "", fmt.Errorf(
			"unknown network %q; use testnet or mainnet, or give an explicit --rpc-url", network)
	}
	return url, nil
}
