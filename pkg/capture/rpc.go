package capture

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/go-stellar-sdk/clients/rpcclient"
	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
)

// RPCSource captures from a Soroban RPC endpoint.
//
// This is the only type in stellar-focus that opens a network connection.
type RPCSource struct {
	client   *rpcclient.Client
	network  string
	endpoint string
}

// NewRPCSource returns a Source backed by Soroban RPC.
func NewRPCSource(network, endpoint string, timeout time.Duration) *RPCSource {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &RPCSource{
		client:   rpcclient.NewClient(endpoint, &http.Client{Timeout: timeout}),
		network:  network,
		endpoint: endpoint,
	}
}

// Name implements Source.
func (s *RPCSource) Name() string { return "soroban-rpc" }

// Network implements Source.
func (s *RPCSource) Network() string { return s.network }

// Endpoint implements Source.
func (s *RPCSource) Endpoint() string { return s.endpoint }

// Close releases the underlying HTTP resources.
func (s *RPCSource) Close() error {
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("closing rpc client: %w", err)
	}
	return nil
}

// Transaction implements Source, returning the envelope, result and meta as
// separate records so each can become its own fixture.
func (s *RPCSource) Transaction(ctx context.Context, hash string) ([]Record, error) {
	resp, err := s.client.GetTransaction(ctx, protocol.GetTransactionRequest{Hash: hash})
	if err != nil {
		return nil, fmt.Errorf("rpc getTransaction: %w", err)
	}

	if resp.Status == protocol.TransactionStatusNotFound {
		return nil, fmt.Errorf(
			"transaction %s: %w (RPC keeps only recent history, so an older transaction needs an archive)",
			hash, ErrNotFound)
	}

	ledger := resp.Ledger
	var out []Record

	if resp.EnvelopeXDR != "" {
		out = append(out, Record{
			Name:        "envelope",
			Type:        "TransactionEnvelope",
			XDR:         resp.EnvelopeXDR,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("envelope of transaction %s", hash),
		})
	}
	if resp.ResultXDR != "" {
		out = append(out, Record{
			Name:        "result",
			Type:        "TransactionResult",
			XDR:         resp.ResultXDR,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("result of transaction %s (%s)", hash, resp.Status),
		})
	}
	if resp.ResultMetaXDR != "" {
		out = append(out, Record{
			Name:        "meta",
			Type:        "TransactionMeta",
			XDR:         resp.ResultMetaXDR,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("meta of transaction %s", hash),
		})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("transaction %s returned no XDR payloads", hash)
	}
	return out, nil
}

// LedgerEntry implements Source, fetching one entry by its base64 LedgerKey.
func (s *RPCSource) LedgerEntry(ctx context.Context, key string) ([]Record, error) {
	resp, err := s.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{key}})
	if err != nil {
		return nil, fmt.Errorf("rpc getLedgerEntries: %w", err)
	}
	if len(resp.Entries) == 0 {
		return nil, fmt.Errorf("ledger key %s: %w", key, ErrNotFound)
	}

	out := make([]Record, 0, len(resp.Entries))
	for i, e := range resp.Entries {
		name := "entry"
		if len(resp.Entries) > 1 {
			name = fmt.Sprintf("entry-%d", i)
		}
		out = append(out, Record{
			Name:        name,
			Type:        "LedgerEntryData",
			XDR:         e.DataXDR,
			Ledger:      e.LastModifiedLedger,
			Description: "ledger entry " + key,
		})
	}
	return out, nil
}
