// Package capture pulls real data off a Stellar network and records it as a
// golden fixture.
//
// This is the only part of stellar-focus that touches the network, and it is
// kept behind the Source interface so everything above it is testable
// offline. The focus command wires in an RPC-backed Source; tests wire in a
// fake.
package capture

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	"github.com/stellar/go-stellar-sdk/xdr"

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

// DefaultHorizonEndpoints maps a network name to its public Horizon
// endpoint.
//
// Horizon is a separate service from Soroban RPC, so it has its own
// defaults: a capture that asks for Horizon and gets an RPC URL would fail
// in a way that took a while to understand.
var DefaultHorizonEndpoints = map[string]string{
	NetworkTestnet: "https://horizon-testnet.stellar.org",
	NetworkMainnet: "https://horizon.stellar.org",
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
	// Anonymized marks XDR whose public account addresses were replaced.
	Anonymized bool
	// Sanitized marks XDR whose memos, data values or secret keys were
	// masked by a Sanitizer.
	Sanitized bool
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

// NetworkPassphraseSource optionally exposes the passphrase reported by the
// network. It is separate from Source to preserve compatibility with existing
// capture sources.
type NetworkPassphraseSource interface {
	NetworkPassphrase(ctx context.Context) (string, error)
}

// resolvePassphrase works out which network passphrase a source is bound to.
//
// A source that reports its passphrase is believed first, since that is what
// the network itself says. Otherwise the passphrase of a known network name
// is used, and for an unknown network with a silent source the result is
// empty.
func resolvePassphrase(ctx context.Context, src Source) (string, error) {
	if source, ok := src.(NetworkPassphraseSource); ok {
		passphrase, err := source.NetworkPassphrase(ctx)
		if err != nil {
			return "", fmt.Errorf("detecting network passphrase: %w", err)
		}
		if passphrase != "" {
			return passphrase, nil
		}
	}
	return golden.PassphraseFor(src.Network()), nil
}

// WithPassphrase binds a source to an explicit network passphrase.
//
// It is for networks the library cannot name — a standalone core, a private
// network — and for sources such as Horizon that do not report a passphrase
// of their own. When the wrapped source does report one, the two must agree:
// a fixture stamped with the wrong passphrase would carry a hash that no
// longer identifies its transaction.
func WithPassphrase(src Source, passphrase string) Source {
	if passphrase == "" {
		return src
	}
	return &passphraseSource{Source: src, passphrase: passphrase}
}

// passphraseSource is a Source with an explicitly supplied passphrase.
type passphraseSource struct {
	Source
	passphrase string
}

// NetworkPassphrase implements NetworkPassphraseSource, checking the
// explicit passphrase against the one the wrapped source reports, if any.
func (s *passphraseSource) NetworkPassphrase(ctx context.Context) (string, error) {
	inner, ok := s.Source.(NetworkPassphraseSource)
	if !ok {
		return s.passphrase, nil
	}
	reported, err := inner.NetworkPassphrase(ctx)
	if err != nil {
		return "", err
	}
	if reported != "" && reported != s.passphrase {
		return "", fmt.Errorf("network passphrase mismatch: %q was given, but %s reports %q",
			s.passphrase, s.Name(), reported)
	}
	return s.passphrase, nil
}

// TransactionInLedger implements LedgerTransactionSource, so wrapping a
// source that can use a ledger hint — an archive fallback — keeps the hint.
func (s *passphraseSource) TransactionInLedger(ctx context.Context, hash string, ledger uint32) ([]Record, error) {
	return FetchTransaction(ctx, s.Source, hash, ledger)
}

// Capture fetches items and turns them into fixtures, stamping each with the
// provenance needed to verify or refresh it later.
//
// now is passed in rather than read from the clock so that callers — and
// tests — control the timestamp written into the fixture.
func Capture(ctx context.Context, src Source, records []Record, now time.Time) ([]*golden.Fixture, error) {
	out := make([]*golden.Fixture, 0, len(records))
	passphrase := ""
	if len(records) > 0 {
		var err error
		passphrase, err = resolvePassphrase(ctx, src)
		if err != nil {
			return nil, err
		}
	}
	for _, r := range records {
		provenance := golden.NewProvenance(src.Network(), src.Endpoint(), r.Ledger, r.TxHash, now)
		provenance.NetworkPassphrase = passphrase
		provenance.Anonymized = r.Anonymized
		provenance.Sanitized = r.Sanitized

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
	if f.Captured != nil && f.Captured.Anonymized {
		result.Err = fmt.Errorf("fixture addresses were anonymized, so it cannot be verified against the live network")
		return result
	}

	if f.Captured != nil && f.Captured.Sanitized {
		result.Err = fmt.Errorf("fixture data was sanitized, so it cannot be verified against the live network")
		return result
	}

	if f.Captured == nil || f.Captured.TxHash == "" {
		result.Err = fmt.Errorf(
			"no capture provenance, so there is nothing to re-fetch (was this fixture written by hand?)")
		return result
	}
	if f.Captured.NetworkPassphrase != "" {
		passphrase, err := resolvePassphrase(ctx, src)
		if err != nil {
			result.Err = err
			return result
		}
		if passphrase == "" {
			result.Err = fmt.Errorf("capture source cannot verify the fixture network passphrase " +
				"(give the passphrase explicitly with --passphrase)")
			return result
		}
		if passphrase != f.Captured.NetworkPassphrase {
			result.Err = fmt.Errorf("network passphrase mismatch: fixture is bound to %q, source reports %q",
				f.Captured.NetworkPassphrase, passphrase)
			return result
		}
	}

	records, err := FetchTransaction(ctx, src, f.Captured.TxHash, f.Captured.Ledger)
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

// LedgerHeaderSource serves the header of one ledger by sequence.
//
// It is the seam CaptureLedgerRange needs: RPC, Horizon and history archives
// all provide it, and a test supplies a canned map of ledgers so a range
// capture stays offline.
type LedgerHeaderSource interface {
	// LedgerHeaderAt fetches the header of one ledger as a
	// LedgerHeaderHistoryEntry record.
	LedgerHeaderAt(ctx context.Context, sequence uint32) (Record, error)
}

// LedgerRangeOptions tunes a multi-ledger capture.
//
// The zero value means "use the defaults": a short pause between requests
// and a few retries with exponential backoff. Retries are counted after the
// first attempt, so Retries: 2 makes at most three requests per ledger.
type LedgerRangeOptions struct {
	// Interval is the minimum time between two requests. Zero uses 100ms.
	Interval time.Duration
	// Retries is how many times a failed ledger is retried. Zero uses 3.
	Retries int
	// Backoff is the wait before the first retry; it doubles each time.
	// Zero uses 250ms.
	Backoff time.Duration
}

// defaultLedgerRangeOptions is the rate limit and retry policy a caller gets
// when it does not ask for one, chosen to be kind to a public endpoint.
func defaultLedgerRangeOptions() LedgerRangeOptions {
	return LedgerRangeOptions{
		Interval: 100 * time.Millisecond,
		Retries:  3,
		Backoff:  250 * time.Millisecond,
	}
}

// CaptureLedgerRange captures a consecutive run of ledgers — start through
// end, inclusive — as one verifiable ledger batch.
//
// It is the multi-ledger counterpart to a single transaction capture: each
// ledger's close metadata and header are recorded in order, so a test can
// reason about a re-org, a fee change, or state archival across the run
// rather than one ledger at a time. Requests are spaced out and retried with
// backoff, and the context cancels the whole capture between attempts.
//
//nolint:revive // the name follows the documented capture API.
func CaptureLedgerRange(ctx context.Context, src Source, start, end uint32) (*LedgerBatchBundle, error) {
	return CaptureLedgerRangeWithOptions(ctx, src, start, end, LedgerRangeOptions{})
}

// CaptureLedgerRangeWithOptions is CaptureLedgerRange with an explicit rate
// limit and retry policy.
//
//nolint:revive // the name follows the documented capture API.
func CaptureLedgerRangeWithOptions(
	ctx context.Context,
	src Source,
	start, end uint32,
	opts LedgerRangeOptions,
) (*LedgerBatchBundle, error) {
	if start == 0 {
		return nil, fmt.Errorf("a ledger range needs a first ledger of at least 1")
	}
	if end < start {
		return nil, fmt.Errorf("ledger range end %d is before start %d", end, start)
	}
	headerSource, ok := src.(LedgerHeaderSource)
	if !ok {
		return nil, fmt.Errorf("source %q cannot capture a ledger range: it does not serve ledger headers", src.Name())
	}

	defaults := defaultLedgerRangeOptions()
	if opts.Interval <= 0 {
		opts.Interval = defaults.Interval
	}
	if opts.Retries <= 0 {
		opts.Retries = defaults.Retries
	}
	if opts.Backoff <= 0 {
		opts.Backoff = defaults.Backoff
	}

	bundle := &LedgerBatchBundle{
		Format:     LedgerBatchFormat,
		Network:    src.Network(),
		Endpoint:   src.Endpoint(),
		Start:      start,
		End:        end,
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for sequence := start; ; sequence++ {
		if sequence > start {
			if err := wait(ctx, opts.Interval); err != nil {
				return nil, err
			}
		}

		record, err := fetchLedgerHeader(ctx, headerSource, sequence, opts)
		if err != nil {
			return nil, fmt.Errorf("capturing ledger %d: %w", sequence, err)
		}
		entry, err := ledgerBatchEntry(record, sequence)
		if err != nil {
			return nil, fmt.Errorf("capturing ledger %d: %w", sequence, err)
		}
		bundle.Ledgers = append(bundle.Ledgers, entry)

		if sequence == end || sequence == math.MaxUint32 {
			break
		}
	}
	return bundle, nil
}

// fetchLedgerHeader fetches one header, retrying with exponential backoff. A
// cancelled context stops immediately rather than sleeping out the policy.
func fetchLedgerHeader(
	ctx context.Context,
	src LedgerHeaderSource,
	sequence uint32,
	opts LedgerRangeOptions,
) (Record, error) {
	var lastErr error
	for attempt := 0; attempt <= opts.Retries; attempt++ {
		if attempt > 0 {
			backoff := opts.Backoff
			for i := 1; i < attempt; i++ {
				backoff *= 2
			}
			if err := wait(ctx, backoff); err != nil {
				return Record{}, err
			}
		}

		record, err := src.LedgerHeaderAt(ctx, sequence)
		if err == nil {
			return record, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return Record{}, ctx.Err()
		}
	}
	return Record{}, lastErr
}

// wait sleeps for d, or returns early when the context is cancelled.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ledgerBatchEntry turns a header record into the close metadata a ledger
// batch stores, decoding the header to name the ledger precisely.
func ledgerBatchEntry(record Record, sequence uint32) (LedgerBatchEntry, error) {
	raw, err := base64.StdEncoding.DecodeString(record.XDR)
	if err != nil {
		return LedgerBatchEntry{}, fmt.Errorf("header XDR is not base64: %w", err)
	}
	var header xdr.LedgerHeaderHistoryEntry
	if _, err := xdr.Unmarshal(bytes.NewReader(raw), &header); err != nil {
		return LedgerBatchEntry{}, fmt.Errorf("parsing header: %w", err)
	}
	if got := uint32(header.Header.LedgerSeq); got != sequence {
		return LedgerBatchEntry{}, fmt.Errorf("source returned the header of ledger %d, want %d", got, sequence)
	}

	entryType := record.Type
	if entryType == "" {
		entryType = "LedgerHeaderHistoryEntry"
	}
	return LedgerBatchEntry{
		Sequence:        sequence,
		Hash:            hex.EncodeToString(header.Hash[:]),
		CloseTime:       int64(header.Header.ScpValue.CloseTime),
		ProtocolVersion: uint32(header.Header.LedgerVersion),
		Type:            entryType,
		XDR:             record.XDR,
	}, nil
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

// ResolveHorizonEndpoint picks the Horizon URL for a network, honouring an
// explicit override.
func ResolveHorizonEndpoint(network, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	url, ok := DefaultHorizonEndpoints[network]
	if !ok {
		return "", fmt.Errorf(
			"unknown network %q; use testnet or mainnet, or give an explicit --rpc-url", network)
	}
	return url, nil
}
