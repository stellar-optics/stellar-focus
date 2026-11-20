package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultCacheDir is where a Scraper keeps fetched records, relative to the
// working directory. It sits under .focus so a single directory name covers
// every local artifact the tool writes, and it is meant to be gitignored:
// the cache is a speed-up, never a source of truth.
const DefaultCacheDir = ".focus/cache"

// CacheFormat identifies the cache entry layout, so a future change is
// detected rather than silently misread.
const CacheFormat = "focus/cache/v1"

// DefaultCacheTTL is how long a cached fetch stays fresh when no TTL is
// given. A day is long enough that a re-run of the same captures is served
// from disk, and short enough that a re-capture picks up chain movement
// without anyone having to think about it.
const DefaultCacheTTL = 24 * time.Hour

// CacheEntry is the on-disk form of one cached fetch.
type CacheEntry struct {
	// Format identifies the layout version.
	Format string `json:"format"`
	// Endpoint and Key record what was fetched, so an entry found by hash
	// can still be inspected and sanity-checked by a human.
	Endpoint string `json:"endpoint"`
	Key      string `json:"key"`
	StoredAt string `json:"storedAt"`
	// LastAccessedAt is refreshed on a cache hit. Prune uses it to evict the
	// least-recently-used entries when the cache outgrows its byte limit.
	LastAccessedAt string   `json:"lastAccessedAt,omitempty"`
	Records        []Record `json:"records"`
}

// Cache stores fetched records on disk, keyed by CacheKey.
//
// The zero TTL means entries never expire; any positive TTL ages them out.
// A Cache is safe for concurrent use.
type Cache struct {
	dir string
	ttl time.Duration
	now func() time.Time

	mu sync.Mutex
}

// NewCache returns a cache rooted at dir. A dir of "" disables storage:
// Get always misses and Put is a no-op, so callers can pass a nil cache or
// an empty-dir cache without branching.
func NewCache(dir string, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Cache{dir: dir, ttl: ttl, now: time.Now}
}

// Dir reports the cache root.
func (c *Cache) Dir() string { return c.dir }

// CacheKey derives a stable cache key from the parts of a request — the
// endpoint, the method, the hash or ledger key. NUL joins the parts so no
// combination of part boundaries can collide: ("ab","c") and ("a","bc")
// hash differently.
func CacheKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Get returns the cached records for key, if present and fresh.
//
// A malformed or unreadable entry is a miss, not an error: the cache exists
// to save network calls, and refusing to fall back to the network because a
// cache file is damaged would make the cache a new way to fail.
func (c *Cache) Get(key string) ([]Record, bool) {
	if c == nil || c.dir == "" {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, false
	}

	var entry CacheEntry
	if json.Unmarshal(data, &entry) != nil || entry.Format != CacheFormat {
		return nil, false
	}

	now := c.now()
	storedAt, err := time.Parse(time.RFC3339, entry.StoredAt)
	if err != nil {
		return nil, false
	}
	if c.ttl > 0 && now.Sub(storedAt) > c.ttl {
		return nil, false
	}

	// Record the hit so Prune can evict by least-recent use. A failure to
	// rewrite the timestamp must not turn a valid cache hit into a miss;
	// the cache is a speed-up, not a source of truth.
	entry.LastAccessedAt = now.UTC().Format(time.RFC3339)
	_ = c.writeEntry(key, entry)

	return entry.Records, true
}

// Put stores records under key, replacing any previous entry.
func (c *Cache) Put(key string, records []Record) error {
	if c == nil || c.dir == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now().UTC()
	entry := CacheEntry{
		Format:         CacheFormat,
		Key:            key,
		StoredAt:       now.Format(time.RFC3339),
		LastAccessedAt: now.Format(time.RFC3339),
		Records:        records,
	}
	return c.writeEntry(key, entry)
}

// writeEntry replaces the file backing key, atomically.
func (c *Cache) writeEntry(key string, entry CacheEntry) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding cache entry %s: %w", key, err)
	}

	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("creating cache dir %s: %w", c.dir, err)
	}

	// Write to a temporary file and rename, so an interrupted write can
	// never leave a truncated entry that a later Get would read as valid.
	tmp, err := os.CreateTemp(c.dir, "entry-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp cache file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("writing cache entry %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("closing temp cache file: %w", err)
	}
	if err := os.Rename(tmp.Name(), c.path(key)); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("storing cache entry %s: %w", key, err)
	}
	return nil
}

// Prune expires stale entries and evicts the least-recently-used ones until
// the cache fits within maxBytes.
//
// A ttl of zero or less uses the cache's configured TTL. A maxBytes of zero
// or less leaves the size unbounded, so a caller can prune by age alone. An
// entry's last access is refreshed by Get, so repeated fetches keep a hot
// entry alive while a cold one ages out first. Missing files are ignored: a
// concurrent Get or another process may have removed them already.
func (c *Cache) Prune(maxBytes int64, ttl time.Duration) error {
	if c == nil || c.dir == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	dirEntries, err := os.ReadDir(c.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading cache dir %s: %w", c.dir, err)
	}

	if ttl <= 0 {
		ttl = c.ttl
	}
	now := c.now()

	files := make([]cacheFile, 0, len(dirEntries))
	var total int64
	for _, entry := range dirEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			// Temporary files from an interrupted Put are not entries.
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		file := cacheFile{
			path:     filepath.Join(c.dir, entry.Name()),
			size:     info.Size(),
			accessed: info.ModTime(),
		}
		if data, err := os.ReadFile(file.path); err == nil {
			var stored CacheEntry
			if json.Unmarshal(data, &stored) == nil && stored.Format == CacheFormat {
				if at, err := time.Parse(time.RFC3339, stored.StoredAt); err == nil {
					file.storedAt = at
				}
				if stored.LastAccessedAt != "" {
					if at, err := time.Parse(time.RFC3339, stored.LastAccessedAt); err == nil {
						file.accessed = at
					}
				}
			}
		}

		if ttl > 0 && !file.storedAt.IsZero() && now.Sub(file.storedAt) > ttl {
			if err := os.Remove(file.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("pruning expired cache entry %s: %w", file.path, err)
			}
			continue
		}

		files = append(files, file)
		total += file.size
	}

	if maxBytes <= 0 || total <= maxBytes {
		return nil
	}

	// Oldest access first. A stable sort keeps the outcome deterministic when
	// two entries share a timestamp.
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].accessed.Before(files[j].accessed)
	})
	for _, file := range files {
		if total <= maxBytes {
			break
		}
		if err := os.Remove(file.path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("evicting cache entry %s: %w", file.path, err)
		}
		total -= file.size
	}
	return nil
}

// cacheFile is one cache entry as Prune sees it.
type cacheFile struct {
	path     string
	size     int64
	storedAt time.Time
	accessed time.Time
}

// path is the file backing one key.
func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key+".json")
}

// Limiter spaces out calls to a live endpoint so a capture run does not
// arrive as a burst. It waits rather than failing: a slow run is better than
// a rate-limited one.
//
// Limiter is safe for concurrent use.
type Limiter struct {
	// Interval is the minimum time between two acquisitions.
	Interval time.Duration

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	last time.Time
}

// NewLimiter returns a limiter with the given minimum interval. A non-
// positive interval disables limiting: Wait returns immediately.
func NewLimiter(interval time.Duration) *Limiter {
	return &Limiter{
		Interval: interval,
		now:      time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

// Wait blocks until the caller may make the next call, or until ctx is
// done.
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil || l.Interval <= 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.last.IsZero() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if wait := l.Interval - l.now().Sub(l.last); wait > 0 {
			if err := l.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	l.last = l.now()
	return nil
}

// Scraper is a Source that wraps another Source with rate limiting and a
// local cache — the "live chain scraper" the capture commands use so a
// repetitive run is fast for the caller and gentle on the endpoint.
//
// Every fetch is rate limited; a fresh cache entry skips the network
// entirely. Errors from the underlying source pass through unchanged, so
// ErrNotFound stays distinguishable with errors.Is.
//
// Scraper is safe for concurrent use, as long as the wrapped Source is.
type Scraper struct {
	src     Source
	cache   *Cache
	limiter *Limiter
}

// NewScraper wraps src. A nil cache disables caching; a nil or
// zero-interval limiter disables rate limiting, so NewScraper(src, nil, 0)
// is a plain pass-through with the same behaviour as src itself.
func NewScraper(src Source, cache *Cache, limiter *Limiter) *Scraper {
	return &Scraper{src: src, cache: cache, limiter: limiter}
}

// Name implements Source.
func (s *Scraper) Name() string { return s.src.Name() }

// Network implements Source.
func (s *Scraper) Network() string { return s.src.Network() }

// Endpoint implements Source.
func (s *Scraper) Endpoint() string { return s.src.Endpoint() }

// Transaction implements Source, serving from the cache when the entry is
// fresh and hitting the network otherwise.
func (s *Scraper) Transaction(ctx context.Context, hash string) ([]Record, error) {
	key := CacheKey(s.src.Endpoint(), "transaction", hash)
	if records, ok := s.cache.Get(key); ok {
		return records, nil
	}

	if err := s.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("waiting for rate limit: %w", err)
	}

	records, err := s.src.Transaction(ctx, hash)
	if err != nil {
		return nil, err
	}

	if err := s.cache.Put(key, records); err != nil {
		return nil, fmt.Errorf("transaction %s: %w", hash, err)
	}
	return records, nil
}

// LedgerEntry implements Source, with the same cache and rate-limit
// behaviour as Transaction.
func (s *Scraper) LedgerEntry(ctx context.Context, key string) ([]Record, error) {
	cacheKey := CacheKey(s.src.Endpoint(), "ledger-entry", key)
	if records, ok := s.cache.Get(cacheKey); ok {
		return records, nil
	}

	if err := s.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("waiting for rate limit: %w", err)
	}

	records, err := s.src.LedgerEntry(ctx, key)
	if err != nil {
		return nil, err
	}

	if err := s.cache.Put(cacheKey, records); err != nil {
		return nil, fmt.Errorf("ledger entry %s: %w", key, err)
	}
	return records, nil
}
