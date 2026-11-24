package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HorizonSource captures fixtures from a Horizon REST endpoint.
type HorizonSource struct {
	network  string
	endpoint string
	client   *http.Client
}

// NewHorizonSource returns a Source backed by Horizon.
func NewHorizonSource(network, endpoint string, timeout time.Duration) *HorizonSource {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HorizonSource{
		network:  network,
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   &http.Client{Timeout: timeout},
	}
}

// NewHorizonSourceWithRetry returns a HorizonSource whose HTTP transport
// automatically retries on transient errors (429, 502, 503, 504) using the
// given RetryConfig.
//
// Use this variant when capturing against public endpoints that rate-limit
// requests. NewHorizonSource uses a plain (no-retry) HTTP client.
func NewHorizonSourceWithRetry(network, endpoint string, timeout time.Duration, cfg RetryConfig) *HorizonSource {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HorizonSource{
		network:  network,
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   RetryHTTPClient(timeout, cfg),
	}
}

func (s *HorizonSource) Name() string     { return "horizon" }
func (s *HorizonSource) Network() string  { return s.network }
func (s *HorizonSource) Endpoint() string { return s.endpoint }

// Transaction implements Source by fetching a Horizon transaction record.
func (s *HorizonSource) Transaction(ctx context.Context, hash string) ([]Record, error) {
	path := fmt.Sprintf("%s/transactions/%s", s.endpoint, url.PathEscape(hash))
	body, err := s.get(ctx, path)
	if err != nil {
		return nil, err
	}

	var tx struct {
		EnvelopeXDR   string `json:"envelope_xdr"`
		ResultXDR     string `json:"result_xdr"`
		ResultMetaXDR string `json:"result_meta_xdr"`
		Ledger        uint32 `json:"ledger"`
		LedgerAttr    uint32 `json:"ledger_attr"`
	}
	if err := json.Unmarshal(body, &tx); err != nil {
		return nil, fmt.Errorf("decode horizon transaction %s: %w", hash, err)
	}
	ledger := tx.Ledger
	if ledger == 0 {
		ledger = tx.LedgerAttr
	}

	out := make([]Record, 0, 3)
	if tx.EnvelopeXDR != "" {
		out = append(out, Record{
			Name:        "envelope",
			Type:        "TransactionEnvelope",
			XDR:         tx.EnvelopeXDR,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("envelope of transaction %s", hash),
		})
	}
	if tx.ResultXDR != "" {
		out = append(out, Record{
			Name:        "result",
			Type:        "TransactionResult",
			XDR:         tx.ResultXDR,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("result of transaction %s", hash),
		})
	}
	if tx.ResultMetaXDR != "" {
		out = append(out, Record{
			Name:        "meta",
			Type:        "TransactionMeta",
			XDR:         tx.ResultMetaXDR,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("meta of transaction %s", hash),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("transaction %s: %w", hash, ErrNotFound)
	}
	return out, nil
}

// LedgerEntry implements Source by looking up a ledger entry via Horizon.
func (s *HorizonSource) LedgerEntry(ctx context.Context, key string) ([]Record, error) {
	for _, endpoint := range []string{
		fmt.Sprintf("%s/ledger_entries?key=%s", s.endpoint, url.QueryEscape(key)),
		fmt.Sprintf("%s/ledgers?key=%s", s.endpoint, url.QueryEscape(key)),
	} {
		body, err := s.get(ctx, endpoint)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}

		records, err := parseLedgerEntryRecords(body)
		if err != nil {
			return nil, fmt.Errorf("decode horizon ledger entry %s: %w", key, err)
		}
		if len(records) == 0 {
			continue
		}

		out := make([]Record, 0, len(records))
		for i, rec := range records {
			name := "entry"
			if len(records) > 1 {
				name = fmt.Sprintf("entry-%d", i)
			}
			if rec.DataXDR == "" {
				continue
			}
			out = append(out, Record{
				Name:        name,
				Type:        "LedgerEntryData",
				XDR:         rec.DataXDR,
				Ledger:      rec.Ledger,
				Description: "ledger entry " + key,
			})
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("ledger key %s: %w", key, ErrNotFound)
		}
		return out, nil
	}
	return nil, fmt.Errorf("ledger key %s: %w", key, ErrNotFound)
}

func parseLedgerEntryRecords(data []byte) ([]struct {
	DataXDR string
	Ledger  uint32
}, error) {
	var payload struct {
		Records []struct {
			DataXDR string `json:"data_xdr"`
			Ledger  uint32 `json:"last_modified_ledger"`
		} `json:"records"`
		Embedded struct {
			Records []struct {
				DataXDR string `json:"data_xdr"`
				Ledger  uint32 `json:"last_modified_ledger"`
			} `json:"records"`
		} `json:"_embedded"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	records := payload.Records
	if len(records) == 0 {
		records = payload.Embedded.Records
	}
	out := make([]struct {
		DataXDR string
		Ledger  uint32
	}, 0, len(records))
	for _, r := range records {
		out = append(out, struct {
			DataXDR string
			Ledger  uint32
		}{DataXDR: r.DataXDR, Ledger: r.Ledger})
	}
	return out, nil
}

// LedgerHeaderAt implements LedgerHeaderSource by reading a ledger from
// Horizon's /ledgers collection, which reports the header XDR alongside the
// close time.
func (s *HorizonSource) LedgerHeaderAt(ctx context.Context, sequence uint32) (Record, error) {
	path := fmt.Sprintf("%s/ledgers/%d", s.endpoint, sequence)
	body, err := s.get(ctx, path)
	if err != nil {
		return Record{}, err
	}

	var ledger struct {
		Hash      string `json:"hash"`
		Sequence  uint32 `json:"sequence"`
		ClosedAt  string `json:"closed_at"`
		HeaderXDR string `json:"header_xdr"`
	}
	if err := json.Unmarshal(body, &ledger); err != nil {
		return Record{}, fmt.Errorf("decode horizon ledger %d: %w", sequence, err)
	}
	if ledger.HeaderXDR == "" {
		return Record{}, fmt.Errorf("horizon ledger %d: response carried no header XDR", sequence)
	}

	return Record{
		Name:        "ledger-header",
		Type:        "LedgerHeaderHistoryEntry",
		XDR:         ledger.HeaderXDR,
		Ledger:      sequence,
		Description: fmt.Sprintf("header of ledger %d (%s, closed %s)", sequence, ledger.Hash, ledger.ClosedAt),
	}, nil
}

func (s *HorizonSource) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("horizon request %s: %w", endpoint, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("horizon request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading horizon response %s: %w", endpoint, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", endpoint, ErrNotFound)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s: horizon returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
