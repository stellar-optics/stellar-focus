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
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// defaultVerifyDir is what `focus verify` checks when given no paths.
const defaultVerifyDir = "testdata"

func newVerifyCmd(g *globalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "verify [paths...]",
		Short: "Check that stored fixtures still match the chain",
		Long: `Re-fetch each fixture from the network and report any that no longer match.

Only fixtures carrying capture provenance can be checked; hand-written ones
are skipped and reported as such rather than silently passing.

Paths may be files or directories. With no path, testdata is checked.`,
		Example: `  focus verify
  focus verify testdata/ --network mainnet`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{defaultVerifyDir}
			}

			paths, err := collectFixtures(args)
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("no fixture files found in %s", strings.Join(args, ", "))
			}

			endpoint, err := capture.ResolveEndpoint(g.network, g.rpcURL)
			if err != nil {
				return err
			}
			src := newSource(g.network, endpoint, g.timeout)

			var checked, matched, drifted, skipped int
			for _, path := range paths {
				f, loadErr := golden.Load(path)
				if loadErr != nil {
					fmt.Fprintf(stderr, "%-40s ERROR   %v\n", path, loadErr)
					drifted++
					continue
				}

				res := capture.Verify(cmd.Context(), src, path, f)
				switch {
				case res.Err != nil && errors.Is(res.Err, capture.ErrNotFound):
					fmt.Fprintf(stderr, "%-40s GONE    %v\n", path, res.Err)
					drifted++
				case res.Err != nil:
					fmt.Fprintf(stdout, "%-40s SKIP    %v\n", path, res.Err)
					skipped++
				case res.Match:
					fmt.Fprintf(stdout, "%-40s ok\n", path)
					matched++
				default:
					fmt.Fprintf(stderr, "%-40s DRIFTED\n", path)
					if res.Diff != nil {
						f := lensformat.NewDiffFormatter(
							lensformat.WithDiffPalette(lensformat.PaletteFor(stderr, g.color)))
						if fErr := f.Format(stderr, res.Diff); fErr != nil {
							fmt.Fprintf(stderr, "  (could not render the diff: %v)\n", fErr)
						}
					}
					drifted++
				}
				checked++
			}

			fmt.Fprintf(stdout, "\n%d checked: %d ok, %d drifted, %d skipped\n",
				checked, matched, drifted, skipped)

			if drifted > 0 {
				return &exitError{code: 1, msg: fmt.Sprintf("%d fixture(s) no longer match the chain", drifted)}
			}
			return nil
		},
	}
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
