package cli

// Capturing a batch of transactions.
//
// Recording a suite of fixtures one transaction at a time means one process
// per transaction: each reconnects to the endpoint, each prints its own
// output, and the first failure ends the run. This file does the same work
// in one run — the hashes come from a file, a small pool of workers fetches
// them, and one transaction failing does not stop the others.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

// batchEntry is one transaction of a batch.
type batchEntry struct {
	// hash is the transaction hash.
	hash string
	// prefix overrides the capture's prefix for this entry.
	prefix string
	// line is the line the entry was read from, for error messages.
	line int
	// index is the entry's position in the batch, counting from one.
	index int
}

// batchOutcome is what happened to one entry of a batch.
type batchOutcome struct {
	// entry is the entry the outcome belongs to.
	entry batchEntry
	// paths are the fixtures written, when the capture succeeded.
	paths []string
	// err is why the capture failed, when it did.
	err error
}

// batchReporter prints the progress of a batch as it goes.
//
// The pool writes from several goroutines at once, so the printing is
// serialised: interleaved half-lines would be worse than no progress at all.
type batchReporter struct {
	mu     sync.Mutex
	total  int
	stdout io.Writer
	stderr io.Writer

	captured int
	missing  int
	failed   int
}

// report prints one entry's outcome and counts it.
func (r *batchReporter) report(outcome batchOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := outcome.entry
	switch {
	case outcome.err == nil:
		r.captured++
		target := ""
		if len(outcome.paths) > 0 {
			target = outcome.paths[0]
		}
		fmt.Fprintf(r.stdout, "[%d/%d] captured tx %s -> %s\n",
			entry.index, r.total, shortHash(entry.hash), target)
	case errors.Is(outcome.err, capture.ErrNotFound):
		r.missing++
		fmt.Fprintf(r.stderr, "[%d/%d] not found tx %s: %v\n",
			entry.index, r.total, shortHash(entry.hash), outcome.err)
	default:
		r.failed++
		fmt.Fprintf(r.stderr, "[%d/%d] failed tx %s: %v\n",
			entry.index, r.total, shortHash(entry.hash), outcome.err)
	}
}

// summary prints the totals of a batch.
func (r *batchReporter) summary(stdout io.Writer) {
	fmt.Fprintf(stdout, "\n%d transaction(s): %d captured, %d not found, %d failed\n",
		r.total, r.captured, r.missing, r.failed)
}

// captureBatchCmd captures every transaction listed in a batch file.
func captureBatchCmd(ctx context.Context, cfg *config, stdout, stderr io.Writer) error {
	endpoint, err := capture.ResolveEndpoint(cfg.network(), cfg.rpcURL())
	if err != nil {
		return usageErrorf("%v", err)
	}
	src := newSource(cfg.network(), endpoint, cfg.timeout())

	entries, err := readBatchEntries(cfg.batch(), stdin)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if len(entries) == 0 {
		return usageErrorf("%s lists no transaction hashes", cfg.batch())
	}

	reporter := &batchReporter{total: len(entries), stdout: stdout, stderr: stderr}
	captureBatch(ctx, src, entries, cfg, capture.NewLimiter(cfg.rate()), reporter)
	reporter.summary(stdout)

	if reporter.failed > 0 || reporter.missing > 0 {
		return failureErrorf("%d transaction(s) could not be captured",
			reporter.failed+reporter.missing)
	}
	return nil
}

// captureBatch captures every entry of a batch, a few at a time.
//
// The pool is what keeps a batch from arriving at the endpoint as a burst:
// --concurrency decides how many captures are in flight at once and --rate
// spaces them out. A transaction that cannot be captured is reported and the
// batch carries on, because one hash that has aged out of the endpoint's
// retention window says nothing about the other twenty-nine.
func captureBatch(
	ctx context.Context,
	src capture.Source,
	entries []batchEntry,
	cfg *config,
	limiter *capture.Limiter,
	reporter *batchReporter,
) {
	workers := cfg.concurrency()
	if workers < 1 {
		workers = 1
	}
	if workers > len(entries) {
		workers = len(entries)
	}

	outcomes := make([]batchOutcome, len(entries))
	queue := make(chan batchEntry)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range queue {
				outcome := captureBatchEntry(ctx, src, entry, cfg, limiter)
				outcomes[entry.index-1] = outcome
				reporter.report(outcome)
			}
		}()
	}

	for _, entry := range entries {
		queue <- entry
	}
	close(queue)
	wg.Wait()
}

// captureBatchEntry captures one transaction of a batch.
func captureBatchEntry(
	ctx context.Context,
	src capture.Source,
	entry batchEntry,
	cfg *config,
	limiter *capture.Limiter,
) batchOutcome {
	outcome := batchOutcome{entry: entry}

	if err := limiter.Wait(ctx); err != nil {
		outcome.err = err
		return outcome
	}

	records, err := src.Transaction(ctx, entry.hash)
	if err != nil {
		outcome.err = err
		return outcome
	}

	prefix := batchPrefix(cfg.prefix(), entry.prefix, entry.hash)

	written, err := writeRecords(ctx, src, records, cfg.outDir(), prefix,
		cfg.force(), cfg.maskAddresses())
	if err != nil {
		outcome.err = err
		return outcome
	}
	for _, w := range written {
		outcome.paths = append(outcome.paths, w.path)
	}
	return outcome
}

// batchPrefix is the filename prefix of a batch fixture.
//
// The short hash is always part of it: a batch captures several transactions,
// and each of them produces files with the same record names, so the hash is
// what keeps them apart. A prefix given by the user goes in front of it.
//
//	failed_payment 2b2e276a…  ->  failed_payment_2b2e276a_envelope.golden.json
func batchPrefix(userPrefix, entryPrefix, hash string) string {
	prefix := entryPrefix
	if prefix == "" {
		prefix = userPrefix
	}
	if prefix == "" {
		return shortHash(hash)
	}
	return prefix + "_" + shortHash(hash)
}

// readBatchEntries reads the entries of a batch file.
//
// The file holds one transaction hash per line. A line may carry a prefix
// for the fixtures it produces, either as "prefix hash" or as "prefix:hash";
// blank lines and lines starting with "#" are ignored, so a batch file can be
// commented.
//
// "-" reads standard input, so a batch can be piped in.
func readBatchEntries(path string, input io.Reader) ([]batchEntry, error) {
	if path == "-" {
		entries, err := parseBatchEntries(input)
		if err != nil {
			return nil, err
		}
		return entries, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	entries, err := parseBatchEntries(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return entries, nil
}

// parseBatchEntries reads the entries of a batch file.
func parseBatchEntries(r io.Reader) ([]batchEntry, error) {
	scanner := bufio.NewScanner(r)

	var entries []batchEntry
	line := 0
	for scanner.Scan() {
		line++

		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		entry, err := parseBatchEntry(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		entry.line = line
		entry.index = len(entries) + 1
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading the batch file: %w", err)
	}
	return entries, nil
}

// parseBatchEntry parses one line of a batch file.
//
// A line is a transaction hash, optionally preceded by a prefix. The prefix
// may be separated by whitespace or by a colon, because both are natural to
// type and neither is worth a flag.
func parseBatchEntry(text string) (batchEntry, error) {
	var prefix, hash string

	if idx := strings.Index(text, ":"); idx >= 0 {
		prefix = strings.TrimSpace(text[:idx])
		hash = strings.TrimSpace(text[idx+1:])
	} else {
		fields := strings.Fields(text)
		switch len(fields) {
		case 1:
			hash = fields[0]
		case 2:
			prefix, hash = fields[0], fields[1]
		default:
			return batchEntry{}, fmt.Errorf(
				"expected a transaction hash, optionally preceded by a prefix")
		}
	}

	if hash == "" {
		return batchEntry{}, fmt.Errorf("no transaction hash")
	}
	return batchEntry{hash: hash, prefix: prefix}, nil
}

// shortHash shortens a transaction hash for a progress line.
func shortHash(hash string) string {
	const width = 8
	if len(hash) <= width {
		return hash
	}
	return hash[:width]
}
