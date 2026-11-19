package capture

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// HeaderSource serves the latest ledger header.
//
// A second seam next to SimulationSource so a capture can be driven offline:
// tests substitute a canned response, and nothing here opens a connection.
type HeaderSource interface {
	// Network reports which network the source is pointed at.
	Network() string
	// Endpoint reports the URL, for provenance.
	Endpoint() string
	// LedgerHeader returns the RPC's latest-ledger response, whose
	// headerXdr holds the base64 LedgerHeaderHistoryEntry.
	LedgerHeader(ctx context.Context) (*protocol.GetLatestLedgerResponse, error)
}

// FetchLedgerHeader fetches the latest ledger header and turns it into a
// single capture record.
//
// The header is what a historical capture is anchored to: its sequence and
// hash name the ledger, and its version, base fee and total coins are the
// network parameters the assertion layer (pkg/focus) checks. One record, one
// fixture — splitting it further would assert on parts of an object the
// network returns whole.
func FetchLedgerHeader(ctx context.Context, src HeaderSource) (Record, error) {
	resp, err := src.LedgerHeader(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("getLatestLedger: %w", err)
	}
	if resp == nil {
		return Record{}, fmt.Errorf("getLatestLedger: endpoint returned no response")
	}
	if resp.LedgerHeader == "" {
		return Record{}, fmt.Errorf("getLatestLedger: response carried no header XDR")
	}

	return Record{
		Name:        "header",
		Type:        "LedgerHeaderHistoryEntry",
		XDR:         resp.LedgerHeader,
		Ledger:      resp.Sequence,
		Description: fmt.Sprintf("ledger header %s (protocol %d, close time %d)", resp.Hash, resp.ProtocolVersion, resp.LedgerCloseTime),
	}, nil
}

// LedgerHeaderFields are the header fields assertions are written against.
// Only the values a test can name precisely are exposed; the rest of the
// header stays inspectable through the Record itself.
type LedgerHeaderFields struct {
	// Version is the protocol version the ledger closed under.
	Version uint32
	// BaseFee is the network's base fee in stroops for this ledger.
	BaseFee uint32
	// BaseReserve is the minimum notional price of one base reserve unit.
	BaseReserve uint32
	// TotalCoins is the total lumens in existence, in stroops.
	TotalCoins int64
	// Sequence is this ledger's own number.
	Sequence uint32
}

// ParseLedgerHeader decodes a base64 LedgerHeaderHistoryEntry — the shape
// getLatestLedger's headerXdr carries — into the fields a capture asserts on.
//
// An input that fails to decode is an error, not a zero struct: silently
// reporting version 0 and fee 0 would make the following assertions pass
// against nothing.
func ParseLedgerHeader(b64 string) (LedgerHeaderFields, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return LedgerHeaderFields{}, fmt.Errorf("decoding ledger header: %w", err)
	}

	entry, err := decodeLedgerHeader(raw)
	if err != nil {
		return LedgerHeaderFields{}, err
	}

	return LedgerHeaderFields{
		Version:     uint32(entry.Header.LedgerVersion),
		BaseFee:     uint32(entry.Header.BaseFee),
		BaseReserve: uint32(entry.Header.BaseReserve),
		TotalCoins:  int64(entry.Header.TotalCoins),
		Sequence:    uint32(entry.Header.LedgerSeq),
	}, nil
}

// LedgerHeader implements HeaderSource against the real endpoint, next to the
// transaction and entry captures on Source so one configured source serves
// all of them.
func (s *RPCSource) LedgerHeader(ctx context.Context) (*protocol.GetLatestLedgerResponse, error) {
	resp, err := s.client.GetLatestLedger(ctx)
	if err != nil {
		return nil, fmt.Errorf("rpc getLatestLedger: %w", err)
	}
	return &resp, nil
}

// LedgerHeaderAt implements LedgerHeaderSource, asking RPC for the ledger at
// sequence. RPC reports headers with their close metadata, so one request
// fills a ledger batch entry.
func (s *RPCSource) LedgerHeaderAt(ctx context.Context, sequence uint32) (Record, error) {
	resp, err := s.client.GetLedgers(ctx, protocol.GetLedgersRequest{
		StartLedger: sequence,
		Pagination:  &protocol.LedgerPaginationOptions{Limit: 1},
	})
	if err != nil {
		return Record{}, fmt.Errorf("rpc getLedgers(%d): %w", sequence, err)
	}
	for _, ledger := range resp.Ledgers {
		if ledger.Sequence != sequence {
			continue
		}
		if ledger.LedgerHeader == "" {
			return Record{}, fmt.Errorf("rpc getLedgers(%d): response carried no header XDR", sequence)
		}
		return Record{
			Name:   "ledger-header",
			Type:   "LedgerHeaderHistoryEntry",
			XDR:    ledger.LedgerHeader,
			Ledger: sequence,
			Description: fmt.Sprintf("header of ledger %d (%s, close time %d)",
				sequence, ledger.Hash, ledger.LedgerCloseTime),
		}, nil
	}
	return Record{}, fmt.Errorf("ledger %d: %w", sequence, ErrNotFound)
}

// decodeLedgerHeader unmarshals raw XDR bytes into a LedgerHeaderHistoryEntry.
//
// Kept as its own function so tests can exercise the decode-then-assert path
// without a fake header source, and so the error names the step that failed.
func decodeLedgerHeader(raw []byte) (xdr.LedgerHeaderHistoryEntry, error) {
	var entry xdr.LedgerHeaderHistoryEntry
	if _, err := xdr.Unmarshal(bytes.NewReader(raw), &entry); err != nil {
		return xdr.LedgerHeaderHistoryEntry{}, fmt.Errorf("parsing ledger header: %w", err)
	}
	return entry, nil
}
