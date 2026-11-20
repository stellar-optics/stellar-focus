package capture_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

func TestCacheKeyIsStableAndBoundarySafe(t *testing.T) {
	a := capture.CacheKey("endpoint", "transaction", "abc")
	b := capture.CacheKey("endpoint", "transaction", "abc")
	if a != b {
		t.Errorf("cache key is not deterministic: %s != %s", a, b)
	}

	// The parts must not be joinable by accident: ("ab","c") and ("a","bc")
	// are different requests and must not share an entry.
	if capture.CacheKey("ab", "c") == capture.CacheKey("a", "bc") {
		t.Error("cache keys collide when part boundaries move")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	cache := capture.NewCache(t.TempDir(), time.Hour)
	key := capture.CacheKey("endpoint", "transaction", "hash")

	if _, ok := cache.Get(key); ok {
		t.Fatal("empty cache reported a hit")
	}

	want := []capture.Record{{
		Name:   "envelope",
		Type:   "TransactionEnvelope",
		XDR:    "AAAA",
		Ledger: 123,
		TxHash: "hash",
	}}
	if err := cache.Put(key, want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("cache miss after Put")
	}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("round trip changed the records: %+v", got)
	}
}

func TestCacheEntryIsHumanReadableJSON(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)
	key := capture.CacheKey("endpoint", "transaction", "hash")
	if err := cache.Put(key, []capture.Record{{Name: "envelope", XDR: "AAAA"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		t.Fatalf("reading entry: %v", err)
	}
	var entry capture.CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("entry is not JSON: %v", err)
	}
	if entry.Format != capture.CacheFormat {
		t.Errorf("entry format %q, want %q", entry.Format, capture.CacheFormat)
	}
	if entry.Key != key {
		t.Errorf("entry key %q, want %q", entry.Key, key)
	}
	if len(entry.Records) != 1 {
		t.Errorf("entry holds %d records, want 1", len(entry.Records))
	}
}

// ageEntry rewrites an entry's storedAt timestamp, so expiry can be tested
// without waiting on a clock.
func ageEntry(t *testing.T, dir, key string, at time.Time) {
	t.Helper()
	path := filepath.Join(dir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading entry: %v", err)
	}
	var entry capture.CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	entry.StoredAt = at.Format(time.RFC3339)
	aged, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("re-encoding entry: %v", err)
	}
	if err := os.WriteFile(path, aged, 0o644); err != nil {
		t.Fatalf("writing aged entry: %v", err)
	}
}

func TestCacheExpiry(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)
	key := capture.CacheKey("endpoint", "transaction", "hash")
	if err := cache.Put(key, []capture.Record{{Name: "envelope", XDR: "AAAA"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	ageEntry(t, dir, key, time.Now().Add(-2*time.Hour))

	if _, ok := cache.Get(key); ok {
		t.Error("expired entry reported a hit")
	}
}

func TestCacheCorruptEntryIsAMiss(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)
	key := capture.CacheKey("endpoint", "transaction", "hash")
	if err := cache.Put(key, []capture.Record{{Name: "envelope", XDR: "AAAA"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, key+".json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupting entry: %v", err)
	}

	if _, ok := cache.Get(key); ok {
		t.Error("corrupt entry reported a hit; it should fall through to the network")
	}
}

func TestCacheForeignFormatIsAMiss(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)
	key := capture.CacheKey("endpoint", "transaction", "hash")

	entry := capture.CacheEntry{
		Format:   "focus/cache/v999",
		Key:      key,
		StoredAt: time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("encoding entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, key+".json"), data, 0o644); err != nil {
		t.Fatalf("writing entry: %v", err)
	}

	if _, ok := cache.Get(key); ok {
		t.Error("entry from a future format reported a hit")
	}
}

func TestNilAndDisabledCachesAreHarmless(t *testing.T) {
	var nilCache *capture.Cache
	if _, ok := nilCache.Get("anything"); ok {
		t.Error("nil cache reported a hit")
	}
	if err := nilCache.Put("anything", nil); err != nil {
		t.Errorf("nil cache Put: %v", err)
	}

	disabled := capture.NewCache("", time.Hour)
	if _, ok := disabled.Get("anything"); ok {
		t.Error("disabled cache reported a hit")
	}
	if err := disabled.Put("anything", nil); err != nil {
		t.Errorf("disabled cache Put: %v", err)
	}
}

func TestLimiterSpacesOutCalls(t *testing.T) {
	interval := 50 * time.Millisecond
	limiter := capture.NewLimiter(interval)

	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := limiter.Wait(context.Background()); err != nil {
			t.Fatalf("Wait %d: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed < 2*interval {
		t.Errorf("three waits over two intervals took %v, want at least %v", elapsed, 2*interval)
	}
}

func TestLimiterHonoursContext(t *testing.T) {
	limiter := capture.NewLimiter(time.Hour)
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait on cancelled context: %v, want context.Canceled", err)
	}
}

func TestLimiterDisabledWithNonPositiveInterval(t *testing.T) {
	limiter := capture.NewLimiter(0)
	start := time.Now()
	for i := 0; i < 100; i++ {
		if err := limiter.Wait(context.Background()); err != nil {
			t.Fatalf("Wait %d: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("disabled limiter took %v for 100 waits", elapsed)
	}
}

func TestScraperServesFromCache(t *testing.T) {
	src := fakesource.New(capture.NetworkTestnet, "https://example.test")
	src.Transactions["hash"] = []capture.Record{{Name: "envelope", Type: "TransactionEnvelope", XDR: "AAAA", TxHash: "hash"}}

	scraper := capture.NewScraper(src, capture.NewCache(t.TempDir(), time.Hour), capture.NewLimiter(0))

	if _, err := scraper.Transaction(context.Background(), "hash"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if src.Calls != 1 {
		t.Fatalf("first fetch made %d calls, want 1", src.Calls)
	}

	records, err := scraper.Transaction(context.Background(), "hash")
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if src.Calls != 1 {
		t.Errorf("second fetch hit the network: %d calls, want 1", src.Calls)
	}
	if len(records) != 1 || records[0].XDR != "AAAA" {
		t.Errorf("cached records wrong: %+v", records)
	}
}

func TestScraperRateLimitsNetworkCalls(t *testing.T) {
	src := fakesource.New(capture.NetworkTestnet, "https://example.test")
	src.Transactions["hash"] = []capture.Record{{Name: "envelope", XDR: "AAAA", TxHash: "hash"}}

	scraper := capture.NewScraper(src, nil, capture.NewLimiter(30*time.Millisecond))

	start := time.Now()
	for i := 0; i < 2; i++ {
		if _, err := scraper.Transaction(context.Background(), "hash"); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("two uncached fetches took %v, want the limiter interval to space them", elapsed)
	}
	if src.Calls != 2 {
		t.Errorf("made %d calls, want 2", src.Calls)
	}
}

func TestScraperPassesSourceErrorsThrough(t *testing.T) {
	src := fakesource.New(capture.NetworkTestnet, "https://example.test")
	scraper := capture.NewScraper(src, capture.NewCache(t.TempDir(), time.Hour), nil)

	_, err := scraper.Transaction(context.Background(), "missing")
	if !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("error %v, want ErrNotFound to survive wrapping", err)
	}
}

func TestScraperCachesLedgerEntries(t *testing.T) {
	src := fakesource.New(capture.NetworkTestnet, "https://example.test")
	src.Entries["key"] = []capture.Record{{Name: "entry", Type: "LedgerEntryData", XDR: "AAAA"}}

	scraper := capture.NewScraper(src, capture.NewCache(t.TempDir(), time.Hour), nil)

	if _, err := scraper.LedgerEntry(context.Background(), "key"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if _, err := scraper.LedgerEntry(context.Background(), "key"); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if src.Calls != 1 {
		t.Errorf("second ledger entry fetch hit the network: %d calls, want 1", src.Calls)
	}
}

func TestScraperIsASource(t *testing.T) {
	// The scraper must stay substitutable for the source it wraps, or the
	// capture commands cannot use it without branching.
	var _ capture.Source = capture.NewScraper(
		fakesource.New(capture.NetworkTestnet, "https://example.test"), nil, nil)
}

func TestScraperDelegatesIdentity(t *testing.T) {
	inner := fakesource.New(capture.NetworkMainnet, "https://mainnet.example")
	s := capture.NewScraper(inner, nil, nil)

	if s.Name() != inner.Name() {
		t.Errorf("Name = %q, want %q", s.Name(), inner.Name())
	}
	if s.Network() != capture.NetworkMainnet {
		t.Errorf("Network = %q, want mainnet", s.Network())
	}
	if s.Endpoint() != "https://mainnet.example" {
		t.Errorf("Endpoint = %q", s.Endpoint())
	}
}

func TestCacheDirReportsItsRoot(t *testing.T) {
	dir := t.TempDir()
	if got := capture.NewCache(dir, time.Hour).Dir(); got != dir {
		t.Errorf("Dir = %q, want %q", got, dir)
	}
}

// setAccess rewrites an entry's last-access timestamp, so eviction order can
// be tested without sleeping.
func setAccess(t *testing.T, dir, key string, at time.Time) {
	t.Helper()
	path := filepath.Join(dir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading entry: %v", err)
	}
	var entry capture.CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	entry.LastAccessedAt = at.Format(time.RFC3339)
	updated, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("re-encoding entry: %v", err)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatalf("writing entry: %v", err)
	}
}

// cacheDirSize is the total size of the entry files in a cache directory.
func cacheDirSize(t *testing.T, dir string) int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading cache dir: %v", err)
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat entry: %v", err)
		}
		total += info.Size()
	}
	return total
}

func TestCachePruneExpiresOldEntries(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)
	stale := capture.CacheKey("endpoint", "transaction", "stale")
	fresh := capture.CacheKey("endpoint", "transaction", "fresh")
	if err := cache.Put(stale, []capture.Record{{Name: "envelope", XDR: "AAAA"}}); err != nil {
		t.Fatalf("Put stale: %v", err)
	}
	if err := cache.Put(fresh, []capture.Record{{Name: "envelope", XDR: "BBBB"}}); err != nil {
		t.Fatalf("Put fresh: %v", err)
	}
	ageEntry(t, dir, stale, time.Now().Add(-2*time.Hour))

	if err := cache.Prune(0, time.Hour); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if _, ok := cache.Get(stale); ok {
		t.Error("expired entry survived Prune")
	}
	if _, ok := cache.Get(fresh); !ok {
		t.Error("fresh entry was pruned")
	}
}

func TestCachePruneEvictsLeastRecentlyUsed(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)

	payload := strings.Repeat("x", 1024)
	keys := []string{
		capture.CacheKey("endpoint", "transaction", "one"),
		capture.CacheKey("endpoint", "transaction", "two"),
		capture.CacheKey("endpoint", "transaction", "three"),
	}
	for _, key := range keys {
		if err := cache.Put(key, []capture.Record{{Name: "envelope", XDR: payload}}); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	now := time.Now()
	setAccess(t, dir, keys[0], now.Add(-3*time.Hour))
	setAccess(t, dir, keys[1], now.Add(-2*time.Hour))
	setAccess(t, dir, keys[2], now.Add(-time.Hour))

	total := cacheDirSize(t, dir)
	// Room for two entries: the oldest access must go.
	if err := cache.Prune(total-1, 0); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if _, ok := cache.Get(keys[0]); ok {
		t.Error("the least-recently-used entry survived a size prune")
	}
	if _, ok := cache.Get(keys[1]); !ok {
		t.Error("the second-most-recent entry was evicted")
	}
	if _, ok := cache.Get(keys[2]); !ok {
		t.Error("the most-recently-used entry was evicted")
	}
}

func TestCachePruneKeepsTheCacheWithinItsByteLimit(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)
	payload := strings.Repeat("y", 512)
	for _, part := range []string{"a", "b", "c", "d"} {
		key := capture.CacheKey("endpoint", "transaction", part)
		if err := cache.Put(key, []capture.Record{{Name: "envelope", XDR: payload}}); err != nil {
			t.Fatalf("Put %s: %v", part, err)
		}
	}

	const maxBytes = 1400
	if err := cache.Prune(maxBytes, time.Hour); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := cacheDirSize(t, dir); got > maxBytes {
		t.Errorf("cache is %d bytes after pruning, want at most %d", got, maxBytes)
	}
}

func TestCacheGetRefreshesLastAccess(t *testing.T) {
	dir := t.TempDir()
	cache := capture.NewCache(dir, time.Hour)
	key := capture.CacheKey("endpoint", "transaction", "hot")
	if err := cache.Put(key, []capture.Record{{Name: "envelope", XDR: "AAAA"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	setAccess(t, dir, key, old)

	if _, ok := cache.Get(key); !ok {
		t.Fatal("cache miss after Put")
	}

	data, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		t.Fatalf("reading entry: %v", err)
	}
	var entry capture.CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	accessed, err := time.Parse(time.RFC3339, entry.LastAccessedAt)
	if err != nil {
		t.Fatalf("parsing lastAccess: %v", err)
	}
	if !accessed.After(old) {
		t.Errorf("Get did not refresh last access: still %v", accessed)
	}
}

func TestPruneIsHarmlessForNilDisabledAndMissingCaches(t *testing.T) {
	var nilCache *capture.Cache
	if err := nilCache.Prune(10, time.Hour); err != nil {
		t.Errorf("nil cache Prune: %v", err)
	}
	if err := capture.NewCache("", time.Hour).Prune(10, time.Hour); err != nil {
		t.Errorf("disabled cache Prune: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "absent")
	if err := capture.NewCache(missing, time.Hour).Prune(10, time.Hour); err != nil {
		t.Errorf("missing cache dir Prune: %v", err)
	}
}

func TestOpenCachePrunesOnOpen(t *testing.T) {
	dir := t.TempDir()
	staleKey := capture.CacheKey("endpoint", "transaction", "stale")
	cache := capture.NewCache(dir, time.Hour)
	if err := cache.Put(staleKey, []capture.Record{{Name: "envelope", XDR: "AAAA"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ageEntry(t, dir, staleKey, time.Now().Add(-2*time.Hour))

	opened, err := capture.OpenCache(capture.CacheOptions{Dir: dir, TTL: time.Hour})
	if err != nil {
		t.Fatalf("OpenCache: %v", err)
	}
	if _, ok := opened.Get(staleKey); ok {
		t.Error("OpenCache did not prune the expired entry")
	}
}

func TestCachePutFailsWhenTheRootIsNotADirectory(t *testing.T) {
	// A file where the cache directory should be is a configuration
	// problem worth surfacing, not a silent miss.
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}

	cache := capture.NewCache(filepath.Join(blocker, "cache"), time.Hour)
	if err := cache.Put("key", []capture.Record{{Name: "envelope"}}); err == nil {
		t.Fatal("Put succeeded with a file in place of the cache directory")
	}
}
