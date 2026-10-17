// Package cli implements the focus command.
//
// The command exists to record and re-check fixtures; the assertions live in
// pkg/focus, which this package imports but which never imports Cobra. That
// separation is deliberate: a test helper pulled into someone else's project
// should not drag a CLI framework in with it.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// Version is the build version, overridden at release time via ldflags.
var Version = "dev"

// newSource builds the capture source. It is a variable so tests can
// substitute a fake and exercise the commands without a network.
var newSource = func(network, endpoint string, timeout time.Duration) capture.Source {
	return capture.NewRPCSource(network, endpoint, timeout)
}

// now is injectable so captures in tests carry a fixed timestamp.
var now = time.Now

type globalFlags struct {
	network string
	rpcURL  string
	timeout time.Duration
	color   string
}

// Execute builds and runs the root command.
func Execute(args []string, stdout, stderr io.Writer) error {
	root := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	return root.Execute()
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	g := &globalFlags{}

	root := &cobra.Command{
		Use:   "focus",
		Short: "Capture and verify Stellar XDR test fixtures",
		Long: `focus records real Stellar and Soroban data as test fixtures, and checks
that stored fixtures still match the chain.

The assertions this repository provides live in the Go package
github.com/stellar-optics/stellar-focus/pkg/focus; this command exists to
manage the fixtures those assertions run against.`,
		Example: `  # Record a transaction from testnet as fixtures
  focus capture tx 2b2e276a… --out testdata/

  # Check that stored fixtures still match the chain
  focus verify testdata/`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.SetOut(stdout)
	root.SetErr(stderr)

	root.PersistentFlags().StringVar(&g.network, "network", capture.NetworkTestnet,
		"network to talk to: testnet or mainnet")
	root.PersistentFlags().StringVar(&g.rpcURL, "rpc-url", "",
		"RPC endpoint, overriding the network default")
	root.PersistentFlags().DurationVar(&g.timeout, "timeout", 30*time.Second,
		"timeout for a single RPC call")
	root.PersistentFlags().StringVar(&g.color, "color", "auto",
		`when to colourise output: "auto", "always" or "never"`)

	root.AddCommand(
		newCaptureCmd(g, stdout, stderr),
		newVerifyCmd(g, stdout, stderr),
		newVersionCmd(stdout),
	)
	return root
}

func newVersionCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the focus version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(stdout, Version)
			return err
		},
	}
}

func newCaptureCmd(g *globalFlags, stdout, stderr io.Writer) *cobra.Command {
	var (
		outDir string
		prefix string
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Record real chain data as fixture files",
	}

	txCmd := &cobra.Command{
		Use:   "tx <hash>",
		Short: "Record a transaction's envelope, result and meta",
		Long: `Fetch a transaction and write its envelope, result and meta as separate
fixture files, each stamped with the network, ledger and capture time so it
can be re-checked later with focus verify.`,
		Example: `  focus capture tx 2b2e276a… --out testdata/
  focus capture tx 2b2e276a… --out testdata/ --prefix failed_payment`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint, err := capture.ResolveEndpoint(g.network, g.rpcURL)
			if err != nil {
				return err
			}
			src := newSource(g.network, endpoint, g.timeout)

			records, err := src.Transaction(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeFixtures(cmd.Context(), src, records, outDir, prefix, force, stdout)
		},
	}

	entryCmd := &cobra.Command{
		Use:   "ledger-entry <base64-ledger-key>",
		Short: "Record a ledger entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint, err := capture.ResolveEndpoint(g.network, g.rpcURL)
			if err != nil {
				return err
			}
			src := newSource(g.network, endpoint, g.timeout)

			records, err := src.LedgerEntry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeFixtures(cmd.Context(), src, records, outDir, prefix, force, stdout)
		},
	}

	for _, c := range []*cobra.Command{txCmd, entryCmd} {
		c.Flags().StringVarP(&outDir, "out", "o", "testdata",
			"directory to write fixture files into")
		c.Flags().StringVar(&prefix, "prefix", "",
			"filename prefix; defaults to the record name")
		c.Flags().BoolVar(&force, "force", false,
			"overwrite fixture files that already exist")
	}

	cmd.AddCommand(txCmd, entryCmd)
	return cmd
}

// writeFixtures captures records and writes them, refusing to clobber
// existing files unless asked.
func writeFixtures(
	ctx context.Context,
	src capture.Source,
	records []capture.Record,
	outDir, prefix string,
	force bool,
	stdout io.Writer,
) error {
	fixtures, err := capture.Capture(ctx, src, records, now())
	if err != nil {
		return err
	}

	for i, f := range fixtures {
		name := records[i].Name
		if prefix != "" {
			name = prefix + "_" + name
		}
		path := filepath.Join(outDir, name+".golden.json")

		if !force {
			if _, statErr := os.Stat(path); statErr == nil {
				return fmt.Errorf(
					"%s already exists; pass --force to overwrite it, or --prefix to write alongside it", path)
			}
		}

		if err := golden.Save(path, f); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "wrote %s  (%s, ledger %d)\n", path, f.Type, fixtureLedger(f))
	}
	return nil
}

func fixtureLedger(f *golden.Fixture) uint32 {
	if f.Captured == nil {
		return 0
	}
	return f.Captured.Ledger
}

// exitError carries an explicit process exit code.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// ExitCode maps an error to a process exit code, matching the conventions of
// the sibling repositories: 0 success, 1 error, 2 a specific negative
// outcome.
func ExitCode(err error) (code int, printable bool) {
	if err == nil {
		return 0, false
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code, true
	}
	return 1, true
}
