package capture

import (
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// ErrLedgerRequired is returned when a source can find a transaction only
// if it is told which ledger the transaction closed in.
var ErrLedgerRequired = errors.New("the ledger the transaction closed in is required")

// LedgerTransactionSource is implemented by sources that can fetch a
// transaction faster, or at all, when they know the ledger it closed in.
//
// It is separate from Source so existing sources need not implement it.
// Verify uses it with the ledger recorded in a fixture's provenance.
type LedgerTransactionSource interface {
	TransactionInLedger(ctx context.Context, hash string, ledger uint32) ([]Record, error)
}

// FetchTransaction fetches a transaction, passing the ledger it closed in to
// a source that can use it. A ledger of zero means it is unknown.
func FetchTransaction(ctx context.Context, src Source, hash string, ledger uint32) ([]Record, error) {
	if ls, ok := src.(LedgerTransactionSource); ok && ledger != 0 {
		return ls.TransactionInLedger(ctx, hash, ledger)
	}
	return src.Transaction(ctx, hash)
}

// checkpointFrequency is the number of ledgers between history archive
// checkpoints.
const checkpointFrequency = 64

// maxArchiveRecord bounds a single XDR record in an archive stream, so a
// corrupt length prefix fails cleanly instead of allocating gigabytes.
const maxArchiveRecord = 64 << 20

// ArchivalSource captures transactions from a Stellar history archive.
//
// RPC keeps only recent history; an archive keeps all of it. An archive is
// laid out by checkpoint rather than indexed by hash, though, so a
// transaction is found through the ledger it closed in: use
// TransactionInLedger, or FetchTransaction with the ledger a fixture
// recorded. Transaction alone returns ErrLedgerRequired.
//
// An archive stores each transaction's envelope and result. It does not
// store transaction meta, and it stores ledger entries only inside bucket
// snapshots, which this source does not read.
type ArchivalSource struct {
	network  string
	endpoint string
	client   *http.Client

	mu         sync.Mutex
	passphrase string
}

// ArchiveSource is the name for the history archive source used in
// CaptureLedgerRange discussions; it is the same type as ArchivalSource.
type ArchiveSource = ArchivalSource

// NewArchiveSource returns a Source backed by a history archive, and is the
// spelling NewArchivalSource is also known by.
func NewArchiveSource(network, archiveURL string, timeout time.Duration) *ArchiveSource {
	return NewArchivalSource(network, archiveURL, timeout)
}

// LedgerHeaderAt implements LedgerHeaderSource by reading the header from
// the checkpoint that holds sequence.
func (s *ArchivalSource) LedgerHeaderAt(ctx context.Context, sequence uint32) (Record, error) {
	return s.LedgerHeader(ctx, sequence)
}

// NewArchivalSource returns a Source backed by the history archive at
// archiveURL, e.g. https://history.stellar.org/prd/core-live/core_live_001.
func NewArchivalSource(network, archiveURL string, timeout time.Duration) *ArchivalSource {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &ArchivalSource{
		network:  network,
		endpoint: strings.TrimRight(archiveURL, "/"),
		client:   &http.Client{Timeout: timeout},
	}
}

// Name implements Source.
func (s *ArchivalSource) Name() string { return "history-archive" }

// Network implements Source.
func (s *ArchivalSource) Network() string { return s.network }

// Endpoint implements Source.
func (s *ArchivalSource) Endpoint() string { return s.endpoint }

// NetworkPassphrase implements NetworkPassphraseSource, reading the
// passphrase the archive records in its history archive state. An archive
// too old to record one falls back to the passphrase of a known network.
func (s *ArchivalSource) NetworkPassphrase(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.passphrase != "" {
		return s.passphrase, nil
	}

	body, err := s.get(ctx, s.endpoint+"/.well-known/stellar-history.json")
	if err != nil {
		return "", err
	}
	defer body.Close()
	var state struct {
		NetworkPassphrase string `json:"networkPassphrase"`
	}
	if err := json.NewDecoder(body).Decode(&state); err != nil {
		return "", fmt.Errorf("reading history archive state: %w", err)
	}

	s.passphrase = state.NetworkPassphrase
	if s.passphrase == "" {
		s.passphrase = golden.PassphraseFor(s.network)
	}
	return s.passphrase, nil
}

// Transaction implements Source. An archive cannot find a transaction by
// hash alone, so it reports ErrLedgerRequired; see TransactionInLedger.
func (s *ArchivalSource) Transaction(_ context.Context, hash string) ([]Record, error) {
	return nil, fmt.Errorf("transaction %s: history archives are not indexed by hash: %w",
		hash, ErrLedgerRequired)
}

// TransactionInLedger implements LedgerTransactionSource, reading the
// envelope and result of a transaction from the checkpoint holding ledger.
func (s *ArchivalSource) TransactionInLedger(ctx context.Context, hash string, ledger uint32) ([]Record, error) {
	if ledger == 0 {
		return s.Transaction(ctx, hash)
	}
	want, err := hex.DecodeString(hash)
	if err != nil || len(want) != 32 {
		return nil, fmt.Errorf("transaction hash %q is not 64 hex characters", hash)
	}
	hash = strings.ToLower(hash)

	result, err := s.findResult(ctx, hash, ledger)
	if err != nil {
		return nil, err
	}
	envelope, err := s.findEnvelope(ctx, hash, ledger)
	if err != nil {
		return nil, err
	}

	return []Record{
		{
			Name:        "envelope",
			Type:        "TransactionEnvelope",
			XDR:         envelope,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("envelope of transaction %s", hash),
		},
		{
			Name:        "result",
			Type:        "TransactionResult",
			XDR:         result,
			Ledger:      ledger,
			TxHash:      hash,
			Description: fmt.Sprintf("result of transaction %s", hash),
		},
	}, nil
}

// LedgerEntry implements Source. An archive holds ledger entries only in its
// bucket snapshots, which this source does not read.
func (s *ArchivalSource) LedgerEntry(_ context.Context, key string) ([]Record, error) {
	return nil, fmt.Errorf("ledger key %s: history archive bucket snapshots are not read: %w",
		key, errors.ErrUnsupported)
}

// LedgerHeader reads the header of a ledger from the archive, as a
// LedgerHeaderHistoryEntry record.
func (s *ArchivalSource) LedgerHeader(ctx context.Context, ledger uint32) (Record, error) {
	var found *xdr.LedgerHeaderHistoryEntry
	err := s.stream(ctx, "ledger", ledger, func(data []byte) (bool, error) {
		var entry xdr.LedgerHeaderHistoryEntry
		if err := xdr.SafeUnmarshal(data, &entry); err != nil {
			return false, err
		}
		if uint32(entry.Header.LedgerSeq) != ledger {
			return false, nil
		}
		found = &entry
		return true, nil
	})
	if err != nil {
		return Record{}, err
	}
	if found == nil {
		return Record{}, fmt.Errorf("ledger %d: %w (not in its checkpoint)", ledger, ErrNotFound)
	}
	encoded, err := xdr.MarshalBase64(found)
	if err != nil {
		return Record{}, fmt.Errorf("encoding ledger header %d: %w", ledger, err)
	}
	return Record{
		Name:        "ledger-header",
		Type:        "LedgerHeaderHistoryEntry",
		XDR:         encoded,
		Ledger:      ledger,
		Description: fmt.Sprintf("header of ledger %d", ledger),
	}, nil
}

// findResult reads the result of transaction hash from ledger's checkpoint.
func (s *ArchivalSource) findResult(ctx context.Context, hash string, ledger uint32) (string, error) {
	var result string
	err := s.stream(ctx, "results", ledger, func(data []byte) (bool, error) {
		var entry xdr.TransactionHistoryResultEntry
		if err := xdr.SafeUnmarshal(data, &entry); err != nil {
			return false, err
		}
		if uint32(entry.LedgerSeq) != ledger {
			return false, nil
		}
		for _, pair := range entry.TxResultSet.Results {
			if hex.EncodeToString(pair.TransactionHash[:]) != hash {
				continue
			}
			encoded, err := xdr.MarshalBase64(pair.Result)
			if err != nil {
				return false, fmt.Errorf("encoding result: %w", err)
			}
			result = encoded
			return true, nil
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if result == "" {
		return "", fmt.Errorf("transaction %s: %w (not in ledger %d of the archive)", hash, ErrNotFound, ledger)
	}
	return result, nil
}

// findEnvelope reads the envelope of transaction hash from ledger's
// checkpoint. Envelopes are stored without their hash, so each is hashed
// with the network passphrase until one matches.
func (s *ArchivalSource) findEnvelope(ctx context.Context, hash string, ledger uint32) (string, error) {
	passphrase, err := s.NetworkPassphrase(ctx)
	if err != nil {
		return "", err
	}
	if passphrase == "" {
		return "", fmt.Errorf("the archive does not record its network passphrase, " +
			"which is needed to find a transaction by hash")
	}

	var envelope string
	err = s.stream(ctx, "transactions", ledger, func(data []byte) (bool, error) {
		var entry xdr.TransactionHistoryEntry
		if err := xdr.SafeUnmarshal(data, &entry); err != nil {
			return false, err
		}
		if uint32(entry.LedgerSeq) != ledger {
			return false, nil
		}
		for _, env := range historyEnvelopes(entry) {
			got, err := network.HashTransactionInEnvelope(env, passphrase)
			if err != nil {
				continue
			}
			if hex.EncodeToString(got[:]) != hash {
				continue
			}
			encoded, err := xdr.MarshalBase64(env)
			if err != nil {
				return false, fmt.Errorf("encoding envelope: %w", err)
			}
			envelope = encoded
			return true, nil
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if envelope == "" {
		return "", fmt.Errorf("transaction %s: %w (its result is in ledger %d, but no envelope "+
			"there hashes to it under passphrase %q)", hash, ErrNotFound, ledger, passphrase)
	}
	return envelope, nil
}

// historyEnvelopes lists every envelope in a ledger's transaction set,
// whether it is a legacy set or a generalized one, sequential or parallel.
func historyEnvelopes(entry xdr.TransactionHistoryEntry) []xdr.TransactionEnvelope {
	out := append([]xdr.TransactionEnvelope(nil), entry.TxSet.Txs...)
	set, ok := entry.Ext.GetGeneralizedTxSet()
	if !ok {
		return out
	}
	v1, ok := set.GetV1TxSet()
	if !ok {
		return out
	}
	for _, phase := range v1.Phases {
		if components, ok := phase.GetV0Components(); ok {
			for _, component := range components {
				if txs, ok := component.GetTxsMaybeDiscountedFee(); ok {
					out = append(out, txs.Txs...)
				}
			}
		}
		if parallel, ok := phase.GetParallelTxsComponent(); ok {
			for _, stage := range parallel.ExecutionStages {
				for _, cluster := range stage {
					out = append(out, cluster...)
				}
			}
		}
	}
	return out
}

// checkpointPath returns the archive path of a category's file for the
// checkpoint holding ledger, e.g.
// transactions/00/3c/87/transactions-003c877f.xdr.gz.
func checkpointPath(category string, ledger uint32) string {
	name := fmt.Sprintf("%08x", ledger|(checkpointFrequency-1))
	return fmt.Sprintf("%s/%s/%s/%s/%s-%s.xdr.gz", category, name[0:2], name[2:4], name[4:6], category, name)
}

// stream reads the checkpoint file of a category, handing each XDR record
// to visit until it reports it is done.
func (s *ArchivalSource) stream(
	ctx context.Context,
	category string,
	ledger uint32,
	visit func([]byte) (bool, error),
) error {
	url := s.endpoint + "/" + checkpointPath(category, ledger)
	body, err := s.get(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()

	unzipped, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("reading %s: %w", url, err)
	}
	defer unzipped.Close()

	if err := readXDRStream(unzipped, visit); err != nil {
		return fmt.Errorf("reading %s: %w", url, err)
	}
	return nil
}

// readXDRStream reads a stream of record-marked XDR values: each is
// preceded by a four-byte big-endian length whose high bit marks the last
// fragment.
func readXDRStream(r io.Reader, visit func([]byte) (bool, error)) error {
	var header [4]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(header[:]) &^ (1 << 31)
		if size > maxArchiveRecord {
			return fmt.Errorf("record of %d bytes exceeds the %d byte limit", size, maxArchiveRecord)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return fmt.Errorf("truncated record: %w", err)
		}
		done, err := visit(data)
		if err != nil || done {
			return err
		}
	}
}

// get fetches an archive file. A missing file — a checkpoint not yet
// published, or never — is reported as ErrNotFound.
//
// An endpoint that is not an HTTP URL is treated as a local directory laid
// out like an archive, so an offline copy of one can be ingested without a
// server in front of it.
func (s *ArchivalSource) get(ctx context.Context, url string) (io.ReadCloser, error) {
	if dir, ok := s.localDir(); ok {
		rel := strings.TrimPrefix(url, s.endpoint)
		rel = strings.TrimPrefix(rel, "/")
		path := filepath.Join(dir, filepath.FromSlash(rel))
		file, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", url, ErrNotFound)
		}
		if err != nil {
			return nil, fmt.Errorf("reading history archive file %s: %w", path, err)
		}
		return file, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("history archive request %s: %w", url, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("history archive request %s: %w", url, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %w", url, ErrNotFound)
	case resp.StatusCode >= http.StatusBadRequest:
		resp.Body.Close()
		return nil, fmt.Errorf("%s: history archive returned %s", url, resp.Status)
	}
	return resp.Body, nil
}

// localDir reports the directory an endpoint names, when the endpoint is a
// filesystem path rather than an HTTP URL. A file:// URL is accepted too.
func (s *ArchivalSource) localDir() (string, bool) {
	endpoint := s.endpoint
	if strings.HasPrefix(endpoint, "file://") {
		return strings.TrimPrefix(endpoint, "file://"), true
	}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return "", false
	}
	return endpoint, true
}

// FallbackSource tries a primary source and, for anything the primary no
// longer has, a fallback — typically RPC first, then a history archive for
// transactions older than RPC's retention window.
type FallbackSource struct {
	primary  Source
	fallback Source
}

// NewFallbackSource returns a source that asks primary first and fallback
// when primary reports ErrNotFound.
func NewFallbackSource(primary, fallback Source) *FallbackSource {
	return &FallbackSource{primary: primary, fallback: fallback}
}

// Name implements Source.
func (s *FallbackSource) Name() string { return s.primary.Name() + "+" + s.fallback.Name() }

// Network implements Source.
func (s *FallbackSource) Network() string { return s.primary.Network() }

// Endpoint implements Source.
func (s *FallbackSource) Endpoint() string { return s.primary.Endpoint() }

// NetworkPassphrase implements NetworkPassphraseSource, asking the primary
// source first and the fallback when the primary cannot say.
func (s *FallbackSource) NetworkPassphrase(ctx context.Context) (string, error) {
	for _, src := range []Source{s.primary, s.fallback} {
		ps, ok := src.(NetworkPassphraseSource)
		if !ok {
			continue
		}
		passphrase, err := ps.NetworkPassphrase(ctx)
		if err != nil || passphrase != "" {
			return passphrase, err
		}
	}
	return "", nil
}

// Transaction implements Source.
func (s *FallbackSource) Transaction(ctx context.Context, hash string) ([]Record, error) {
	return s.TransactionInLedger(ctx, hash, 0)
}

// TransactionInLedger implements LedgerTransactionSource, passing the ledger
// to whichever source can use it.
func (s *FallbackSource) TransactionInLedger(ctx context.Context, hash string, ledger uint32) ([]Record, error) {
	records, err := FetchTransaction(ctx, s.primary, hash, ledger)
	if !errors.Is(err, ErrNotFound) {
		return records, err
	}
	records, fallbackErr := FetchTransaction(ctx, s.fallback, hash, ledger)
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w; %s: %w", err, s.fallback.Name(), fallbackErr)
	}
	return records, nil
}

// LedgerEntry implements Source.
func (s *FallbackSource) LedgerEntry(ctx context.Context, key string) ([]Record, error) {
	records, err := s.primary.LedgerEntry(ctx, key)
	if !errors.Is(err, ErrNotFound) {
		return records, err
	}
	records, fallbackErr := s.fallback.LedgerEntry(ctx, key)
	if errors.Is(fallbackErr, errors.ErrUnsupported) {
		return nil, err
	}
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w; %s: %w", err, s.fallback.Name(), fallbackErr)
	}
	return records, nil
}
