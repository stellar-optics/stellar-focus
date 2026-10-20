package cli

// The verify command: re-check stored fixtures against the live network.
//
// Split from cli.go so the command that the library exists for — the one
// that tells you a fixture has gone stale — has a file of its own to grow
// in, with its tests beside it.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// defaultVerifyDir is what `focus verify` checks when given no paths.
const defaultVerifyDir = "testdata"

func newVerifyCmd(cfg *config, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "verify [paths...]",
		Short: "Check that stored fixtures still match the chain",
		Long: `Re-fetch each fixture from the network and report any that no longer match.

Only fixtures carrying capture provenance can be checked; hand-written ones
are skipped and reported as such rather than silently passing.

Paths may be files, directories or patterns. A directory is searched all the
way down, so fixtures kept in subdirectories are checked too; a pattern is
matched like a shell pattern, with "**" standing for any number of
directories. Hidden directories and the paths listed in a .focusignore file
are skipped.

With no path, testdata is checked.

Exit status is 0 when every checked fixture still matches, 2 when at least one
has drifted, and 1 when a fixture could not be checked at all.`,
		Example: `  focus verify
  focus verify testdata/ --network mainnet
  focus verify 'testdata/**/*.golden.json'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{defaultVerifyDir}
			}

			paths, err := collectFixtures(args)
			if err != nil {
				return failureErrorf("%v", err)
			}
			if len(paths) == 0 {
				return failureErrorf("no fixture files found in %s", strings.Join(args, ", "))
			}

			endpoint, err := capture.ResolveEndpoint(cfg.network(), cfg.rpcURL())
			if err != nil {
				return usageErrorf("%v", err)
			}
			src := newSource(cfg.network(), endpoint, cfg.timeout())

			var checked, matched, drifted, skipped, errored int
			for _, path := range paths {
				f, loadErr := golden.Load(path)
				if loadErr != nil {
					fmt.Fprintf(stderr, "%-40s ERROR   %v\n", path, loadErr)
					errored++
					checked++
					continue
				}

				res := capture.Verify(cmd.Context(), src, path, f)
				switch {
				case res.Err != nil && errors.Is(res.Err, capture.ErrNotFound):
					fmt.Fprintf(stderr, "%-40s GONE    %v\n", path, res.Err)
					drifted++
				case res.Err != nil && (f.Captured == nil || f.Captured.TxHash == ""):
					fmt.Fprintf(stdout, "%-40s SKIP    %v\n", path, res.Err)
					skipped++
				case res.Err != nil:
					// The endpoint or the file failed, so this fixture was
					// not checked. That is not drift and must not be counted
					// as a pass.
					fmt.Fprintf(stderr, "%-40s ERROR   %v\n", path, res.Err)
					errored++
				case res.Match:
					fmt.Fprintf(stdout, "%-40s ok\n", path)
					matched++
				default:
					fmt.Fprintf(stderr, "%-40s DRIFTED\n", path)
					if res.Diff != nil {
						f := lensformat.NewDiffFormatter(
							lensformat.WithDiffPalette(lensformat.PaletteFor(stderr, cfg.colorMode())))
						if fErr := f.Format(stderr, res.Diff); fErr != nil {
							fmt.Fprintf(stderr, "  (could not render the diff: %v)\n", fErr)
						}
					}
					drifted++
				}
				checked++
			}

			fmt.Fprintf(stdout, "\n%d checked: %d ok, %d drifted, %d skipped",
				checked, matched, drifted, skipped)
			if errored > 0 {
				fmt.Fprintf(stdout, ", %d errored", errored)
			}
			fmt.Fprintln(stdout)

			if errored > 0 {
				return failureErrorf("%d fixture(s) could not be checked", errored)
			}
			if drifted > 0 {
				return driftErrorf("%d fixture(s) no longer match the chain", drifted)
			}
			return nil
		},
	}
}
