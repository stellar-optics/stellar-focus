package cli

// The verify command: re-check stored fixtures against the live network.
//
// Split from cli.go so the command that the library exists for — the one
// that tells you a fixture has gone stale — has a file of its own to grow
// in, with its tests beside it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// defaultVerifyDir is what `focus verify` checks when given no paths.
const defaultVerifyDir = "testdata"

// The output formats `focus verify` can produce.
const (
	// formatText prints one line per fixture and a summary, for a human
	// reading a terminal.
	formatText = "text"
	// formatJSON prints a single JSON document, for a script that has to
	// decide what to do about the result.
	formatJSON = "json"
)

// The statuses a fixture can be reported with in the JSON report.
const (
	// statusMatched means the fixture still matches the chain.
	statusMatched = "matched"
	// statusDrifted means the fixture no longer matches the chain.
	statusDrifted = "drifted"
	// statusSkipped means the fixture could not be checked and was not an
	// error either: it was written by hand, or captured with --sanitize.
	statusSkipped = "skipped"
	// statusErrored means the fixture could not be checked at all.
	statusErrored = "errored"
	// statusFiltered means the run's --since or --filter settings left the
	// fixture out before it was checked.
	statusFiltered = "filtered"
)

// VerifyReport is the machine-readable result of a verification run.
//
// It is what `focus verify --format json` prints: the counts a CI job needs
// to decide whether the run passed, and one entry per fixture so a report
// can name the ones that drifted and say what changed in them.
type VerifyReport struct {
	// Total is the number of fixtures that were considered.
	Total int `json:"total"`
	// Matched is the number that still match the chain.
	Matched int `json:"matched"`
	// Drifted is the number that no longer match the chain.
	Drifted int `json:"drifted"`
	// Skipped is the number that were not checkable and were not errors
	// either: hand-written fixtures, and fixtures captured with --sanitize.
	Skipped int `json:"skipped"`
	// Errored is the number that could not be checked at all.
	Errored int `json:"errored"`
	// Filtered is the number the run's --since or --filter settings left
	// out. A narrowed run reports them so that a run which checked nothing
	// cannot be read as a run that found nothing wrong.
	Filtered int `json:"filtered"`
	// Error explains why the run did not succeed: at least one fixture
	// drifted, or the check could not be completed. It is empty when every
	// fixture that could be checked still matches.
	Error string `json:"error,omitempty"`
	// Results holds one entry per fixture, in the order they were checked.
	Results []VerifyItemResult `json:"results"`
}

// VerifyItemResult is the outcome for one fixture.
type VerifyItemResult struct {
	// Path is the fixture that was checked.
	Path string `json:"path"`
	// Status is one of matched, drifted, skipped or errored.
	Status string `json:"status"`
	// Type is the XDR type the fixture holds.
	Type string `json:"type,omitempty"`
	// Error explains why a fixture was skipped, or why it could not be
	// checked. It is empty for a fixture that matched or drifted.
	Error string `json:"error,omitempty"`
	// DiffCount is the number of differences a drifted fixture has.
	DiffCount int `json:"diff_count,omitempty"`
	// DiffPaths names the fields that changed, in the order lens reports
	// them.
	DiffPaths []string `json:"diff_paths,omitempty"`
	// Diffs describes each of those differences: the field, the kind of
	// change, and the value on each side. It is the machine-readable form
	// of the lines the terminal diff prints, so a pipeline can quote what
	// changed without parsing coloured text.
	Diffs []VerifyDiff `json:"diffs,omitempty"`
}

// VerifyDiff is one path-level difference inside a drifted fixture.
type VerifyDiff struct {
	// Path locates the field, in the same notation the terminal diff and
	// the failure messages use.
	Path string `json:"path"`
	// Op is the kind of change: modified, added, removed or type_changed.
	Op string `json:"op"`
	// Before is the value the fixture stores. It is null when the field was
	// added, and zero, false and "" are reported as they are.
	Before any `json:"before"`
	// After is the value the chain returned. It is null when the field was
	// removed.
	After any `json:"after"`
}

func newVerifyCmd(cfg *config, stdout, stderr io.Writer) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "verify [paths...]",
		Short: "Check that stored fixtures still match the chain",
		Long: `Re-fetch each fixture from the network and report any that no longer match.

Only fixtures carrying capture provenance can be checked; hand-written ones,
and ones captured with --sanitize, are skipped and reported as such rather
than silently passing.

Paths may be files, directories or patterns. A directory is searched all the
way down, so fixtures kept in subdirectories are checked too; a pattern is
matched like a shell pattern, with "**" standing for any number of
directories. Hidden directories and the paths listed in a .focusignore file
are skipped.

With no path, testdata is checked.

A repository with hundreds of fixtures does not need all of them re-fetched on
every commit, so a run can be narrowed:

  --since <duration|RFC3339>   only check fixtures captured after a threshold
  --filter <pattern>           only check fixtures matching a glob or a
                               metadata selector such as network:testnet
  --drifted-only               only re-check fixtures that previously drifted
                               or could not be checked
  --concurrency <N>, -j <N>    check this many fixtures at once (default 4)

What each run found is cached under .focus, so --drifted-only knows what to
re-check. The cache is a speed-up, never a source of truth.

With --archive-url, a fixture whose transaction RPC no longer keeps is
checked against the history archive, using the ledger in its provenance.

With --format json, a single JSON document is printed instead of the
human-readable report: the counts, and one entry per fixture, including the
fields that changed and the value on each side of them for a fixture that
drifted. It is meant for a pipeline that has to act on the result — a
pull-request comment, a step summary, a test report — rather than read it.

Exit status is 0 when every checked fixture still matches, 2 when at least one
has drifted, and 1 when a fixture could not be checked at all. The exit
status is the same in either format.`,
		Example: `  focus verify
  focus verify testdata/ --network mainnet
  focus verify 'testdata/**/*.golden.json'
  focus verify testdata/ --since 48h
  focus verify testdata/ --filter 'envelope*'
  focus verify testdata/ --filter network:testnet
  focus verify testdata/ --drifted-only
  focus verify testdata/ --network mainnet \
    --archive-url https://history.stellar.org/prd/core-live/core_live_001
  focus verify testdata/ --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validVerifyFormat(format); err != nil {
				return err
			}

			report, err := verifyFixtures(cmd, cfg, stdout, stderr, args, format == formatJSON)
			if format == formatJSON {
				if err != nil {
					report.Error = err.Error()
				}
				if writeErr := writeVerifyReport(stdout, report); writeErr != nil {
					return writeErr
				}
			}
			return err
		},
	}

	cfg.bindVerifyFlags(cmd)
	cmd.Flags().StringVar(&format, "format", formatText,
		"output format: text for a human, json for a script")

	return cmd
}

// validVerifyFormat rejects an output format the command does not have.
//
// It is checked before any work is done, so a typo costs nothing and says
// what the choices are.
func validVerifyFormat(format string) error {
	switch format {
	case formatText, formatJSON:
		return nil
	default:
		return usageErrorf("format must be %q or %q, got %q", formatText, formatJSON, format)
	}
}

// fixtureCheck holds everything a single fixture's check produced: the
// result for the report, and the human rendering buffered so the run can
// print fixtures in path order no matter how many goroutines checked them.
type fixtureCheck struct {
	item    VerifyItemResult
	fixture *golden.Fixture
	line    string
	target  io.Writer
	diff    string
	// counted says whether the fixture was actually looked at: filtered
	// fixtures and ones skipped because their last check matched are not
	// counted, everything else is.
	counted bool
}

// verifyFixtures checks every fixture and reports what it found.
//
// Fixtures are independent of one another, so they are checked with a worker
// pool sized by --concurrency. The report and the terminal output are
// rendered in path order afterwards, so the run's output is stable and
// deterministic whatever the concurrency.
func verifyFixtures(
	cmd *cobra.Command,
	cfg *config,
	stdout, stderr io.Writer,
	args []string,
	asJSON bool,
) (*VerifyReport, error) {
	report := &VerifyReport{Results: []VerifyItemResult{}}

	// The narrowing settings are read before anything is loaded or fetched,
	// so an unusable one fails before the run has done any work.
	since, err := parseSince(cfg.since(), now())
	if err != nil {
		return report, usageErrorf("%v", err)
	}
	filter, err := parseFilter(cfg.filter())
	if err != nil {
		return report, usageErrorf("%v", err)
	}

	if len(args) == 0 {
		args = []string{defaultVerifyDir}
	}

	paths, err := collectFixtures(args)
	if err != nil {
		return report, failureErrorf("%v", err)
	}
	if len(paths) == 0 {
		return report, failureErrorf("no fixture files found in %s", strings.Join(args, ", "))
	}

	src, err := openSource(cfg)
	if err != nil {
		return report, err
	}

	cache := loadVerifyCache(cfg.verifyCachePath())

	concurrency := cfg.concurrency()
	checks := make([]fixtureCheck, len(paths))

	var wg sync.WaitGroup
	slot := make(chan struct{}, concurrency)
	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			slot <- struct{}{}
			defer func() { <-slot }()
			checks[i] = checkFixture(cmd, cfg, src, cache, filter, since, stdout, stderr, path)
		}(i, path)
	}
	wg.Wait()

	var checked, matched, drifted, skipped, errored, filtered int
	for _, ch := range checks {
		if !asJSON {
			fmt.Fprint(ch.target, ch.line)
			if ch.diff != "" {
				fmt.Fprint(stderr, ch.diff)
			}
		}

		switch ch.item.Status {
		case statusMatched:
			matched++
			cache.record(ch.item.Path, ch.fixture, outcomeOK)
		case statusDrifted:
			drifted++
			cache.record(ch.item.Path, ch.fixture, outcomeDrifted)
		case statusErrored:
			errored++
			cache.record(ch.item.Path, ch.fixture, outcomeErrored)
		case statusFiltered:
			filtered++
		case statusSkipped:
			skipped++
		}
		if ch.counted {
			checked++
		}

		report.Results = append(report.Results, ch.item)
	}

	if err := cache.save(cfg.verifyCachePath()); err != nil && !asJSON {
		// The cache only saves work, so failing to write it is worth
		// saying but is not worth failing the run over.
		fmt.Fprintf(stderr, "warning: could not write the verification cache: %v\n", err)
	}

	// Total counts every fixture the run accounted for: the ones it
	// checked, and the ones the narrowing settings left out of the check.
	report.Total = len(report.Results)
	report.Matched = matched
	report.Drifted = drifted
	report.Skipped = skipped
	report.Errored = errored
	report.Filtered = filtered

	if !asJSON {
		fmt.Fprintf(stdout, "\n%d checked: %d ok, %d drifted, %d skipped",
			checked, matched, drifted, skipped)
		if errored > 0 {
			fmt.Fprintf(stdout, ", %d errored", errored)
		}
		if filtered > 0 {
			fmt.Fprintf(stdout, ", %d filtered", filtered)
		}
		fmt.Fprintln(stdout)
	}

	if errored > 0 {
		return report, failureErrorf("%d fixture(s) could not be checked", errored)
	}
	if drifted > 0 {
		return report, driftErrorf("%d fixture(s) no longer match the chain", drifted)
	}
	return report, nil
}

// checkFixture checks one fixture and buffers its rendering, without writing
// to the terminal. Fixtures checked concurrently only touch their own
// fixtureCheck, so nothing here needs a lock.
func checkFixture(
	cmd *cobra.Command,
	cfg *config,
	src capture.Source,
	cache *verifyCache,
	filter *fixtureFilter,
	since time.Time,
	stdout, stderr io.Writer,
	path string,
) fixtureCheck {
	ch := fixtureCheck{item: VerifyItemResult{Path: path}}

	f, loadErr := golden.Load(path)
	if loadErr != nil {
		ch.item.Status = statusErrored
		ch.item.Error = loadErr.Error()
		ch.line = fmt.Sprintf("%-40s ERROR   %v\n", path, loadErr)
		ch.target = stderr
		ch.counted = true
		return ch
	}
	ch.fixture = f
	ch.item.Type = f.Type

	// A fixture the filters exclude is not checked at all, and is reported
	// as filtered rather than quietly passing.
	if reason, excluded := excludedBy(filter, since, path, f); excluded {
		ch.item.Status = statusFiltered
		ch.item.Error = reason
		ch.line = fmt.Sprintf("%-40s FILTERED %s\n", path, reason)
		ch.target = stdout
		return ch
	}

	// Under --drifted-only a fixture whose last check matched the chain,
	// and which has not changed since, is left alone.
	if cfg.driftedOnly() {
		if entry, known := cache.lookup(path, f); known && entry.Outcome == outcomeOK {
			ch.item.Status = statusSkipped
			ch.item.Error = fmt.Sprintf("last check on %s matched the chain", entry.CheckedAt)
			ch.line = fmt.Sprintf("%-40s SKIP    last check on %s matched the chain\n",
				path, entry.CheckedAt)
			ch.target = stdout
			return ch
		}
	}

	res := capture.Verify(cmd.Context(), src, path, f)
	switch {
	case res.Err != nil && errors.Is(res.Err, capture.ErrNotFound):
		// The transaction is gone from the network, so the fixture can no
		// longer be checked against it. It counts as drift.
		ch.item.Status = statusDrifted
		ch.item.Error = res.Err.Error()
		ch.line = fmt.Sprintf("%-40s GONE    %v\n", path, res.Err)
		ch.target = stderr
		ch.counted = true
	case res.Err != nil && (f.Captured == nil || f.Captured.TxHash == "" || f.Captured.Sanitized):
		// No capture provenance: there is nothing to re-fetch, so the
		// fixture is skipped but it was still looked at.
		ch.item.Status = statusSkipped
		ch.item.Error = res.Err.Error()
		ch.line = fmt.Sprintf("%-40s SKIP    %v\n", path, res.Err)
		ch.target = stdout
		ch.counted = true
	case res.Err != nil:
		// The endpoint or the file failed, so this fixture was not
		// checked. That is not drift and must not be counted as a pass.
		ch.item.Status = statusErrored
		ch.item.Error = res.Err.Error()
		ch.line = fmt.Sprintf("%-40s ERROR   %v\n", path, res.Err)
		ch.target = stderr
		ch.counted = true
	case res.Match:
		ch.item.Status = statusMatched
		ch.line = fmt.Sprintf("%-40s ok\n", path)
		ch.target = stdout
		ch.counted = true
	default:
		ch.item.Status = statusDrifted
		ch.line = fmt.Sprintf("%-40s DRIFTED\n", path)
		ch.target = stderr
		ch.counted = true
		if res.Diff != nil {
			// Render into a buffer instead of straight to stderr, so a
			// concurrent run can print the diff beside its line in order.
			var diffBuf bytes.Buffer
			format := lensformat.NewDiffFormatter(
				lensformat.WithDiffPalette(lensformat.PaletteFor(&diffBuf, cfg.colorMode())))
			if fErr := format.Format(&diffBuf, res.Diff); fErr != nil {
				fmt.Fprintf(&diffBuf, "  (could not render the diff: %v)\n", fErr)
			}
			ch.diff = diffBuf.String()
			ch.item.DiffCount = len(res.Diff.Changes)
			ch.item.DiffPaths, ch.item.Diffs = diffDetails(res.Diff)
		}
	}
	return ch
}

// diffDetails names the fields a diff reports as changed, and describes each
// change.
//
// The paths are lens's own, so a path in the JSON report is the same path
// the command-line diff prints and the same one a failure message quotes. The
// values are lens's decoded scalars, so they serialize as the JSON types a
// script expects rather than as a rendered string.
func diffDetails(diff *lens.DiffResult) ([]string, []VerifyDiff) {
	if diff == nil || len(diff.Changes) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, len(diff.Changes))
	diffs := make([]VerifyDiff, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		paths = append(paths, change.Path)
		diffs = append(diffs, VerifyDiff{
			Path:   change.Path,
			Op:     change.Op.String(),
			Before: change.Before,
			After:  change.After,
		})
	}
	return paths, diffs
}

// writeVerifyReport prints the report as an indented JSON document.
//
// It is indented rather than compact so a reader — human or machine — can
// see what the document holds, and so the file a pipeline saves is
// reviewable in a pull request.
func writeVerifyReport(w io.Writer, report *VerifyReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("rendering the report: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(data)); err != nil {
		return err
	}
	return nil
}
