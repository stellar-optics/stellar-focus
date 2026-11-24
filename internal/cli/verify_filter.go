package cli

// Narrowing a verify run.
//
// A repository that has been capturing fixtures for a while has hundreds of
// them, and most of them have not changed since they were captured. Checking
// all of them on every commit is slow and puts the endpoint under load for no
// benefit, so a run can be narrowed three ways: by when a fixture was
// captured, by what it is called or what it holds, and by what the last run
// found.
//
// Nothing here decides whether a fixture is correct. The filters only decide
// which fixtures are looked at, and every fixture left out is reported, so a
// narrowed run never looks like a run that found nothing wrong.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// defaultVerifyCachePath is where the verification cache lives.
//
// It sits under .focus, the same directory the capture cache uses, so one
// name covers every local artifact the tool writes and one .gitignore entry
// keeps them all out of the repository.
const defaultVerifyCachePath = ".focus/verify-cache.json"

// verifyCacheFormat identifies the cache layout, so a future change is
// detected rather than silently misread.
const verifyCacheFormat = "focus/verify-cache/v1"

// verifyOutcome is what a check of one fixture found.
type verifyOutcome string

const (
	// outcomeOK means the fixture still matched the chain.
	outcomeOK verifyOutcome = "ok"
	// outcomeDrifted means the fixture no longer matched the chain.
	outcomeDrifted verifyOutcome = "drifted"
	// outcomeErrored means the fixture could not be checked at all.
	outcomeErrored verifyOutcome = "errored"
)

// verifyCacheEntry records what the last check of one fixture found.
type verifyCacheEntry struct {
	// Outcome is what the check found.
	Outcome verifyOutcome `json:"outcome"`
	// CheckedAt is when it was found, in RFC3339.
	CheckedAt string `json:"checkedAt"`
	// Fingerprint identifies the fixture the outcome belongs to, so a
	// fixture that has been re-captured since is checked again rather than
	// trusted.
	Fingerprint string `json:"fingerprint"`
}

// verifyCache is the on-disk record of what previous runs found.
type verifyCache struct {
	// Format identifies the layout version.
	Format string `json:"format"`
	// Fixtures maps a fixture's absolute path to its last outcome.
	Fixtures map[string]verifyCacheEntry `json:"fixtures"`
}

// loadVerifyCache reads the cache.
//
// A missing or damaged cache is not an error: the cache exists to save work,
// and refusing to run because a cache file is broken would make it a new way
// to fail.
func loadVerifyCache(cachePath string) *verifyCache {
	cache := &verifyCache{Format: verifyCacheFormat, Fixtures: map[string]verifyCacheEntry{}}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return cache
	}
	var loaded verifyCache
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.Format != verifyCacheFormat {
		return cache
	}
	if loaded.Fixtures != nil {
		cache.Fixtures = loaded.Fixtures
	}
	return cache
}

// save writes the cache.
func (c *verifyCache) save(cachePath string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the verification cache: %w", err)
	}
	if dir := filepath.Dir(cachePath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", cachePath, err)
	}
	return nil
}

// lookup returns what the last run found for a fixture.
//
// The fingerprint has to match as well: a fixture that has been re-captured
// since the last check is a different fixture, and the old outcome says
// nothing about it.
func (c *verifyCache) lookup(fixturePath string, f *golden.Fixture) (verifyCacheEntry, bool) {
	entry, known := c.Fixtures[cacheKey(fixturePath)]
	if !known {
		return verifyCacheEntry{}, false
	}
	if entry.Fingerprint != "" && entry.Fingerprint != fingerprint(f) {
		return verifyCacheEntry{}, false
	}
	return entry, true
}

// record notes what a check found.
func (c *verifyCache) record(fixturePath string, f *golden.Fixture, outcome verifyOutcome) {
	if c.Fixtures == nil {
		c.Fixtures = map[string]verifyCacheEntry{}
	}
	c.Fixtures[cacheKey(fixturePath)] = verifyCacheEntry{
		Outcome:     outcome,
		CheckedAt:   now().UTC().Format(time.RFC3339),
		Fingerprint: fingerprint(f),
	}
}

// cacheKey is the key a fixture is cached under: its absolute path, so the
// cache does not depend on the directory the command was run from.
func cacheKey(fixturePath string) string {
	abs, err := filepath.Abs(fixturePath)
	if err != nil {
		return fixturePath
	}
	return abs
}

// fingerprint identifies a fixture's contents.
func fingerprint(f *golden.Fixture) string {
	if f == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(f.Type + "\x00" + f.XDR))
	return hex.EncodeToString(sum[:])
}

// parseSince reads a --since value: a duration counted back from now, or an
// RFC3339 timestamp.
//
// An empty value means no threshold, which is the default.
func parseSince(value string, reference time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		return reference.Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf(
		"--since %q is neither a duration (48h) nor an RFC3339 timestamp (2026-10-01T00:00:00Z)", value)
}

// fixtureTags are the metadata fields a --filter pattern can select on.
var fixtureTags = map[string]bool{
	"network":     true,
	"type":        true,
	"description": true,
	"ledger":      true,
	"txhash":      true,
	"endpoint":    true,
	"at":          true,
}

// fixtureFilter selects the fixtures a run should check.
//
// It is either a glob over the filename, or a metadata selector of the form
// "key:value".
type fixtureFilter struct {
	// pattern is the glob, when the filter is a glob.
	pattern string
	// key and value are the metadata selector, when the filter is one.
	key   string
	value string
	// isTag says which of the two forms this filter takes.
	isTag bool
}

// parseFilter reads a --filter value.
func parseFilter(pattern string) (*fixtureFilter, error) {
	if pattern == "" {
		return nil, nil
	}
	if key, value, ok := strings.Cut(pattern, ":"); ok && fixtureTags[strings.ToLower(key)] {
		return &fixtureFilter{key: strings.ToLower(key), value: value, isTag: true}, nil
	}
	if _, err := path.Match(pattern, "x"); err != nil {
		return nil, fmt.Errorf("--filter %q is not a valid pattern: %w", pattern, err)
	}
	return &fixtureFilter{pattern: pattern}, nil
}

// matches reports whether a fixture passes the filter.
func (f *fixtureFilter) matches(fixturePath string, fixture *golden.Fixture) bool {
	if f == nil {
		return true
	}
	if f.isTag {
		return f.matchesTag(fixture)
	}
	return matchGlob(f.pattern, filepath.Base(fixturePath)) || matchGlob(f.pattern, fixturePath)
}

// matchesTag reports whether a fixture's metadata matches the selector.
func (f *fixtureFilter) matchesTag(fixture *golden.Fixture) bool {
	if fixture == nil {
		return false
	}
	var got string
	switch f.key {
	case "type":
		got = fixture.Type
	case "description":
		got = fixture.Description
	case "network":
		if fixture.Captured != nil {
			got = fixture.Captured.Network
		}
	case "ledger":
		if fixture.Captured != nil {
			got = strconv.FormatUint(uint64(fixture.Captured.Ledger), 10)
		}
	case "txhash":
		if fixture.Captured != nil {
			got = fixture.Captured.TxHash
		}
	case "endpoint":
		if fixture.Captured != nil {
			got = fixture.Captured.Endpoint
		}
	case "at":
		if fixture.Captured != nil {
			got = fixture.Captured.At
		}
	}
	return got != "" && strings.EqualFold(got, f.value)
}

// matchGlob matches a name against a glob pattern.
//
// The pattern is matched against the file's base name and against the path
// as it was given, so both "envelope*" and "testdata/*" do what they look
// like they do.
func matchGlob(pattern, name string) bool {
	matched, err := path.Match(pattern, name)
	return err == nil && matched
}

// excludedBy reports whether a fixture is excluded from a run, and why.
//
// The reason is reported rather than assumed, so a narrowed run says what it
// left out and on what grounds.
func excludedBy(filter *fixtureFilter, since time.Time, fixturePath string, f *golden.Fixture) (string, bool) {
	if !since.IsZero() {
		at, ok := capturedAt(f)
		if !ok {
			return "the fixture carries no capture time, so it cannot be newer than the threshold", true
		}
		if at.Before(since) {
			return fmt.Sprintf("captured %s, before %s",
				at.UTC().Format(time.RFC3339), since.UTC().Format(time.RFC3339)), true
		}
	}
	if filter != nil && !filter.matches(fixturePath, f) {
		return "excluded by --filter " + filter.pattern, true
	}
	return "", false
}

// capturedAt reads the time a fixture was captured.
func capturedAt(f *golden.Fixture) (time.Time, bool) {
	if f == nil || f.Captured == nil || f.Captured.At == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, f.Captured.At)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}
