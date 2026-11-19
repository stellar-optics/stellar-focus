package capture

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// rangeSource is an offline Source plus LedgerHeaderSource, so a range
// capture can be exercised without a network.
type rangeSource struct {
	network  string
	endpoint string
	records  map[uint32]Record
	// failFirst makes the first N requests for a ledger fail, so retry
	// behaviour is observable.
	failFirst map[uint32]int
	calls     []uint32
}

func (s *rangeSource) Name() string     { return "range-fake" }
func (s *rangeSource) Network() string  { return s.network }
func (s *rangeSource) Endpoint() string { return s.endpoint }

func (s *rangeSource) Transaction(context.Context, string) ([]Record, error) {
	return nil, errors.ErrUnsupported
}

func (s *rangeSource) LedgerEntry(context.Context, string) ([]Record, error) {
	return nil, errors.ErrUnsupported
}

func (s *rangeSource) LedgerHeaderAt(ctx context.Context, sequence uint32) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	s.calls = append(s.calls, sequence)
	if s.failFirst[sequence] > 0 {
		s.failFirst[sequence]--
		return Record{}, errors.New("transient endpoint failure")
	}
	record, ok := s.records[sequence]
	if !ok {
		return Record{}, ErrNotFound
	}
	return record, nil
}

func newRangeSource(t *testing.T, ledgers ...uint32) *rangeSource {
	t.Helper()
	src := &rangeSource{
		network:   NetworkTestnet,
		endpoint:  "https://fake.example",
		records:   make(map[uint32]Record, len(ledgers)),
		failFirst: make(map[uint32]int),
	}
	for _, ledger := range ledgers {
		src.records[ledger] = Record{
			Name:   "ledger-header",
			Type:   "LedgerHeaderHistoryEntry",
			XDR:    headerFixture(t, 20, 100, 5000, ledger, 105000000),
			Ledger: ledger,
		}
	}
	return src
}

func fastRangeOptions() LedgerRangeOptions {
	return LedgerRangeOptions{Interval: time.Millisecond, Retries: 2, Backoff: time.Millisecond}
}

func TestCaptureLedgerRangeCapturesConsecutiveLedgers(t *testing.T) {
	t.Parallel()

	src := newRangeSource(t, 50000000, 50000001, 50000002)
	bundle, err := CaptureLedgerRangeWithOptions(
		context.Background(), src, 50000000, 50000002, fastRangeOptions())
	if err != nil {
		t.Fatalf("CaptureLedgerRangeWithOptions() error = %v", err)
	}

	if bundle.Format != LedgerBatchFormat {
		t.Errorf("format = %q, want %q", bundle.Format, LedgerBatchFormat)
	}
	if bundle.Start != 50000000 || bundle.End != 50000002 {
		t.Errorf("range = %d..%d, want 50000000..50000002", bundle.Start, bundle.End)
	}
	if bundle.Network != NetworkTestnet || bundle.Endpoint != "https://fake.example" {
		t.Errorf("provenance = %s %s", bundle.Network, bundle.Endpoint)
	}
	if len(bundle.Ledgers) != 3 {
		t.Fatalf("captured %d ledgers, want 3", len(bundle.Ledgers))
	}

	for i, entry := range bundle.Ledgers {
		wantSeq := uint32(50000000 + i)
		if entry.Sequence != wantSeq {
			t.Errorf("ledger %d has sequence %d, want %d", i, entry.Sequence, wantSeq)
		}
		if entry.CloseTime != 1700000000 {
			t.Errorf("ledger %d close time = %d, want 1700000000", entry.Sequence, entry.CloseTime)
		}
		if entry.ProtocolVersion != 20 {
			t.Errorf("ledger %d protocol = %d, want 20", entry.Sequence, entry.ProtocolVersion)
		}
		if entry.Hash == "" || entry.XDR != src.records[wantSeq].XDR {
			t.Errorf("ledger %d metadata is incomplete: %+v", entry.Sequence, entry)
		}
	}
}

func TestCaptureLedgerRangeRetriesTransientFailures(t *testing.T) {
	t.Parallel()

	src := newRangeSource(t, 10, 11)
	src.failFirst[11] = 2

	if _, err := CaptureLedgerRangeWithOptions(
		context.Background(), src, 10, 11, fastRangeOptions()); err != nil {
		t.Fatalf("CaptureLedgerRangeWithOptions() error = %v", err)
	}

	attempts := 0
	for _, call := range src.calls {
		if call == 11 {
			attempts++
		}
	}
	if attempts != 3 {
		t.Errorf("ledger 11 was requested %d times, want 3 (one try plus two retries)", attempts)
	}
}

func TestCaptureLedgerRangeStopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	src := newRangeSource(t, 20, 21)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := CaptureLedgerRangeWithOptions(ctx, src, 20, 21, fastRangeOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestCaptureLedgerRangeRejectsInvalidRanges(t *testing.T) {
	t.Parallel()

	src := newRangeSource(t, 5)
	if _, err := CaptureLedgerRange(context.Background(), src, 5, 4); err == nil {
		t.Error("CaptureLedgerRange() accepted an end before the start")
	}
	if _, err := CaptureLedgerRange(context.Background(), src, 0, 5); err == nil {
		t.Error("CaptureLedgerRange() accepted a zero start")
	}
}

// plainSource implements Source and nothing else.
type plainSource struct{}

func (plainSource) Name() string     { return "plain" }
func (plainSource) Network() string  { return NetworkTestnet }
func (plainSource) Endpoint() string { return "https://fake.example" }
func (plainSource) Transaction(context.Context, string) ([]Record, error) {
	return nil, errors.ErrUnsupported
}
func (plainSource) LedgerEntry(context.Context, string) ([]Record, error) {
	return nil, errors.ErrUnsupported
}

func TestCaptureLedgerRangeRequiresAHeaderSource(t *testing.T) {
	t.Parallel()

	if _, err := CaptureLedgerRange(context.Background(), plainSource{}, 1, 2); err == nil {
		t.Fatal("CaptureLedgerRange() accepted a Source without ledger headers")
	}
}

func TestLedgerBatchBundleRoundTripAndVerify(t *testing.T) {
	t.Parallel()

	src := newRangeSource(t, 30, 31)
	bundle, err := CaptureLedgerRangeWithOptions(
		context.Background(), src, 30, 31, fastRangeOptions())
	if err != nil {
		t.Fatalf("CaptureLedgerRangeWithOptions() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "range.bundle.json")
	if err := bundle.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := LoadLedgerBatchBundle(path)
	if err != nil {
		t.Fatalf("LoadLedgerBatchBundle() error = %v", err)
	}
	if len(loaded.Ledgers) != 2 || loaded.Ledgers[0].Sequence != 30 {
		t.Fatalf("loaded bundle = %+v", loaded)
	}

	results := VerifyLedgerBatch(context.Background(), src, loaded)
	if len(results) != 2 {
		t.Fatalf("VerifyLedgerBatch() returned %d results, want 2", len(results))
	}
	for _, result := range results {
		if result.Err != nil || !result.Match {
			t.Errorf("ledger %s did not verify: err=%v match=%v", result.Path, result.Err, result.Match)
		}
	}
}
