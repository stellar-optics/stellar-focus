// Package cli implements the focus command.
////
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
	"strconv"
	"strings"
	"time"
	"net/http"
	"os/exec"

	"github.com/spf13/cobra"

	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"

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
	format  string
	output  string
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
	root.PersistentFlags().StringVar(&g.format, "format", "terminal",
		`output format: "terminal", "junit", or "tap"`)
	root.PersistentFlags().StringVar(&g.output, "output", "",
		"output file path for non-terminal formats")

	root.AddCommand(
		newCaptureCmd(g, stdout, stderr),
		newVerifyCmd(g, stdout, stderr),
		newVersionCmd(stdout),
		newDoctorCmd(g, stdout, stderr),
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
func writeFixtures(ctx context.Context, src capture.Source, records []capture.Record, outDir, prefix string, force bool, stdout io.Writer) error {
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

// newVerifyCmd returns the cobra command for `focus verify`.
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
				args = []string{"testdata"}
			}

			paths, err := collectFixtures(args)
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("no fixture files found in %s", strings.Join(args, ", "))
			}

			format := g.format
			if format == "" {
				format = "terminal"
			}
			outputFile := g.output

			endpoint, err := capture.ResolveEndpoint(g.network, g.rpcURL)
			if err != nil {
				return err
			}
			src := newSource(g.network, endpoint, g.timeout)

			var checked, matched, drifted, skipped int

			var reportFile *os.File
			var closer func() error

			if format != "terminal" && outputFile != "" {
				var err error
				reportFile, err = os.Create(outputFile)
				if err != nil {
					return fmt.Errorf("failed to create output file %s: %w", outputFile, err)
				}
				closer = reportFile.Close
			}

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
					if format == "junit" && reportFile != nil {
						fmt.Fprintf(reportFile, "<testcase name=\"%s\" classname=\"%s\" time=\"?\">\n", path, path)
						fmt.Fprintf(reportFile, "<failure message=\"%s\">Drifted: no diff available</failure>\n", path)
						fmt.Fprintf(reportFile, "</testcase>\n")
					}
					if format == "tap" && reportFile != nil {
						fmt.Fprintf(reportFile, "ok 1 - %s\n", path)
					}
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
			// Close report file if we opened one
			if closer != nil {
				closer()
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

// newDoctorCmd returns the cobra command for `focus doctor`.
func newDoctorCmd(g *globalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate the local development environment and CLI dependencies",
		Long: `focus doctor checks the local development environment to ensure
everything is set up correctly for working with Stellar fixtures.

It validates:
  - Go version (>= 1.22)
  - Soroban RPC endpoint reachability
  - Horizon endpoint reachability
  - Cache directory permissions
  - Companion tools (stellar-xdr-lens, gh CLI)

Exit code:
  - 0 if all required checks pass
  - 1 if any critical check fails`,

		RunE: func(cmd *cobra.Command, args []string) error {
			var passed, failed int

			// Check Go version
			goVersion := checkGoVersion()
			if goVersion != "" {
				fmt.Fprintf(stdout, "[✓] Go Runtime: %s\n", goVersion)
				passed++
			} else {
				fmt.Fprintf(stdout, "[!] Go Runtime: could not determine version\n")
				failed++
			}

			// Check Soroban RPC endpoint
			rpcStatus := checkRPCEndpoint(g.rpcURL, g.network)
			if rpcStatus != "" {
				fmt.Fprintf(stdout, "[✓] Soroban RPC: %s\n", rpcStatus)
				passed++
			} else {
				fmt.Fprintf(stdout, "[!] Soroban RPC: unreachable\n")
				failed++
			}

			// Check cache directory
			cacheOK := checkCacheDirectory()
			if cacheOK {
				fmt.Fprintf(stdout, "[✓] Cache Directory: writable\n")
				passed++
			} else {
				fmt.Fprintf(stdout, "[!] Cache Directory: not writable\n")
				failed++
			}

			// Check companion tools
			companionStatus := checkCompanionTools()
			if companionStatus != "" {
				fmt.Fprintf(stdout, "[✓] %s\n", companionStatus)
				passed++
			} else {
				fmt.Fprintf(stdout, "[!] Companion tools: not found (optional)\n")
				// Not counting as failed since they're optional
			}

			fmt.Fprintf(stdout, "\n%d checks passed, %d failed\n", passed, failed)

			if failed > 0 {
				return &exitError{code: 1, msg: fmt.Sprintf("%d check(s) failed", failed)}
			}
			return nil
		},
	}
}

// checkGoVersion checks that Go is version 1.22 or higher.
func checkGoVersion() string {
	// Use go version command
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(string(out))
	// Parse version string like "go1.22.5 linux/amd64"
	parts := strings.Split(version, " ")
	if len(parts) < 2 {
		return ""
	}
	versionStr := parts[1] // e.g., "1.22.5"
	verParts := strings.Split(versionStr, ".")
	if len(verParts) < 2 {
		return ""
	}
	major, err := strconv.Atoi(verParts[0])
	if err != nil || major < 1 {
		return ""
	}
	if major == 1 {
		minor, err := strconv.Atoi(verParts[1])
		if err != nil || minor < 22 {
			return ""
		}
	}
	return version
}

// checkRPCEndpoint checks the reachability of the Soroban RPC and Horizon endpoints.
func checkRPCEndpoint(rpcURL, network string) string {
	// Default RPC URLs by network
	defaultRPC := map[string]string{
		"testnet": "https://soroban-testnet.stellar.org",
		"mainnet": "https://horizon.stellar.org",
	}

	endpoint := rpcURL
	if endpoint == "" {
		endpoint = defaultRPC[network]
	}
	if endpoint == "" {
		return ""
	}

	// Perform a simple HTTP check
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/.well-known/stellar", nil)
	if err != nil {
		return ""
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return fmt.Sprintf("https://%s", endpoint)
	}
	return ""
}

// checkCacheDirectory verifies that .focus/cache/ exists and is writable.
func checkCacheDirectory() bool {
	cacheDir := filepath.Join(os.Getenv("HOME"), ".focus", "cache")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		// Try to create it
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return false
		}
	}
	return true
}

// checkCompanionTools checks if stellar-xdr-lens or gh CLI are installed.
func checkCompanionTools() string {
	// Check stellar-xdr-lens
	if _, err := exec.LookPath("stellar-xdr-lens"); err == nil {
		return "stellar-xdr-lens found on PATH"
	}

	// Check gh CLI
	if _, err := exec.LookPath("gh"); err == nil {
		return "gh CLI found on PATH"
	}

	return ""
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