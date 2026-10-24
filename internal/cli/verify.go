package cli

// The verify command: re-check stored fixtures against the live network.
//
// Split from cli.go so the command that the library exists for — the one
// that tells you a fixture has gone stale — has a file of its own to grow
// in, with its tests beside it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

Paths may be files or directories. With no path, testdata is checked.

A repository with hundreds of fixtures does not need all of them re-fetched on
every commit, so a run can be narrowed:

  --since <duration|RFC3339>   only check fixtures captured after a threshold
  --filter <pattern>           only check fixtures matching a glob or a
                               metadata selector such as network:testnet
  --drifted-only               only re-check fixtures that previously drifted
                               or could not be checked

What each run found is cached under .focus, so --drifted-only knows what to
re-check. The cache is a speed-up, never a source of truth.

With --archive-url, a fixture whose transaction RPC no longer keeps is
checked against the history archive, using the ledger in its provenance.

With --format json, a single JSON document is printed instead of the
human-readable report: the counts, and one entry per fixture. It is meant
for a pipeline that has to act on the result — a pull-request comment, a
step summary, a test report — rather than read it.

Exit status is 0 when every checked fixture still matches, 2 when at least one
has drifted, and 1 when a fixture could not be checked at all. The exit
status is the same in either format.`,
		Example: `  focus verify
  focus verify testdata/ --network mainnet
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

// verifyFixtures checks every fixture and reports what it found.
//
// The report is returned whatever happens, so the JSON output has a
// document to print even when the run failed before it could check
// anything. When asJSON is set, nothing is written to the terminal: the
// report is the output.
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

	var checked, matched, drifted, skipped, errored, filtered int
	for _, path := range paths {
		f, loadErr := golden.Load(path)
		if loadErr != nil {
			if !asJSON {
				fmt.Fprintf(stderr, "%-40s ERROR   %v\n", path, loadErr)
			}
			errored++
			checked++
			report.Results = append(report.Results, VerifyItemResult{
				Path:   path,
				Status: statusErrored,
				Error:  loadErr.Error(),
			})
			continue
		}

		// A fixture the filters exclude is not checked at all, and is
		// reported as filtered rather than quietly passing.
		if reason, excluded := excludedBy(filter, since, path, f); excluded {
			if !asJSON {
				fmt.Fprintf(stdout, "%-40s FILTERED %s\n", path, reason)
			}
			filtered++
			report.Results = append(report.Results, VerifyItemResult{
				Path:   path,
				Status: statusFiltered,
				Type:   f.Type,
				Error:  reason,
			})
			continue
		}

		// Under --drifted-only a fixture whose last check matched the
		// chain, and which has not changed since, is left alone.
		if cfg.driftedOnly() {
			entry, known := cache.lookup(path, f)
			if known && entry.Outcome == outcomeOK {
				if !asJSON {
					fmt.Fprintf(stdout, "%-40s SKIP    last check on %s matched the chain\n",
						path, entry.CheckedAt)
				}
				skipped++
				report.Results = append(report.Results, VerifyItemResult{
					Path:   path,
					Status: statusSkipped,
					Type:   f.Type,
					Error:  fmt.Sprintf("last check on %s matched the chain", entry.CheckedAt),
				})
				continue
			}
		}

		res := capture.Verify(cmd.Context(), src, path, f)
		switch {
		case res.Err != nil && errors.Is(res.Err, capture.ErrNotFound):
			// The transaction is gone from the network, so the fixture can
			// no longer be checked against it. It counts as drift.
			if !asJSON {
				fmt.Fprintf(stderr, "%-40s GONE    %v\n", path, res.Err)
			}
			drifted++
			cache.record(path, f, outcomeDrifted)
			report.Results = append(report.Results, VerifyItemResult{
				Path:   path,
				Status: statusDrifted,
				Type:   f.Type,
				Error:  res.Err.Error(),
			})
		case res.Err != nil && (f.Captured == nil || f.Captured.TxHash == "" || f.Captured.Sanitized):
			if !asJSON {
				fmt.Fprintf(stdout, "%-40s SKIP    %v\n", path, res.Err)
			}
			skipped++
			report.Results = append(report.Results, VerifyItemResult{
				Path:   path,
				Status: statusSkipped,
				Type:   f.Type,
				Error:  res.Err.Error(),
			})
		case res.Err != nil:
			// The endpoint or the file failed, so this fixture was
			// not checked. That is not drift and must not be counted
			// as a pass.
			if !asJSON {
				fmt.Fprintf(stderr, "%-40s ERROR   %v\n", path, res.Err)
			}
			errored++
			cache.record(path, f, outcomeErrored)
			report.Results = append(report.Results, VerifyItemResult{
				Path:   path,
				Status: statusErrored,
				Type:   f.Type,
				Error:  res.Err.Error(),
			})
		case res.Match:
			if !asJSON {
				fmt.Fprintf(stdout, "%-40s ok\n", path)
			}
			matched++
			cache.record(path, f, outcomeOK)
			report.Results = append(report.Results, VerifyItemResult{
				Path:   path,
				Status: statusMatched,
				Type:   f.Type,
			})
		default:
			if !asJSON {
				fmt.Fprintf(stderr, "%-40s DRIFTED\n", path)
			}
			if res.Diff != nil && !asJSON {
				f := lensformat.NewDiffFormatter(
					lensformat.WithDiffPalette(lensformat.PaletteFor(stderr, cfg.colorMode())))
				if fErr := f.Format(stderr, res.Diff); fErr != nil {
					fmt.Fprintf(stderr, "  (could not render the diff: %v)\n", fErr)
				}
			}
			drifted++
			cache.record(path, f, outcomeDrifted)
			item := VerifyItemResult{
				Path:   path,
				Status: statusDrifted,
				Type:   f.Type,
			}
			if res.Diff != nil {
				item.DiffCount = len(res.Diff.Changes)
				item.DiffPaths = diffPaths(res.Diff)
			}
			report.Results = append(report.Results, item)
		}
		checked++
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

// diffPaths names the fields a diff reports as changed.
//
// The paths are lens's own, so a path in the JSON report is the same path
// the command-line diff prints and the same one a failure message quotes.
func diffPaths(diff *lens.DiffResult) []string {
	if diff == nil || len(diff.Changes) == 0 {
		return nil
	}
	paths := make([]string, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		paths = append(paths, change.Path)
	}
	return paths
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

// collectFixtures expands files and directories into a list of fixture paths.
func collectFixtures(args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", arg, err)
		}
		if !info.IsDir() {
			out = append(out, arg)
			continue
		}
		entries, err := filepath.Glob(filepath.Join(arg, "*.golden.json"))
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", arg, err)
		}
		out = append(out, entries...)
	}
	return out, nil
}
