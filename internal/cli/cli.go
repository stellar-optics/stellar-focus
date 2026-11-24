// Package cli implements the focus command.
//
// The command exists to record and re-check fixtures; the assertions live in
// pkg/focus, which this package imports but which never imports Cobra. That
// separation is deliberate: a test helper pulled into someone else's project
// should not drag a CLI framework in with it.
package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// Version is the build version, overridden at release time via ldflags.
var Version = "dev"

// currentRetryConfig is the retry policy applied by default when creating network sources.
var currentRetryConfig = capture.DefaultRetryConfig

// newSource builds the capture source. It is a variable so tests can
// substitute a fake and exercise the commands without a network.
//
// The kind is the source the user asked for — "rpc", "horizon" or "file" —
// and the endpoint is the URL, or for a file source the directory, that kind
// resolves to.
var newSource = func(kind, network, endpoint string, timeout time.Duration) capture.Source {
	switch kind {
	case sourceFile:
		return capture.NewFileSource(endpoint).ForNetwork(network)
	case sourceHorizon:
		return capture.NewHorizonSourceWithRetry(network, endpoint, timeout, currentRetryConfig)
	default:
		return capture.NewRPCSourceWithRetry(network, endpoint, timeout, currentRetryConfig)
	}
}

// openSource builds the configured capture source: falling back to a
// history archive when --archive-url names one, and bound to an explicit
// network passphrase when one was given.
func openSource(cfg *config) (capture.Source, error) {
	endpoint, err := cfg.endpoint()
	if err != nil {
		return nil, usageErrorf("%v", err)
	}
	currentRetryConfig = cfg.retryConfig()
	src := newSource(cfg.source(), cfg.network(), endpoint, cfg.timeout())
	if url := cfg.archiveURL(); url != "" {
		archive := capture.NewArchivalSource(cfg.network(), url, cfg.timeout())
		src = capture.NewFallbackSource(src, archive)
	}
	return capture.WithPassphrase(src, cfg.passphrase()), nil
}

// now is injectable so captures in tests carry a fixed timestamp.
var now = time.Now

// stdin is where a batch read from "-" comes from. It is a variable so a
// test can supply one without a process.
var stdin io.Reader = os.Stdin

// Execute builds and runs the root command.
//
// extra commands are added to the root before execution: cmd/focus passes
// `focus assert` (cmd/focus/interactive.go), which keeps the interactive
// assertion generator beside main without moving fixtures — capture and
// verify — out of this package.
func Execute(args []string, stdout, stderr io.Writer, extra ...*cobra.Command) error {
	root := newRootCmd(stdout, stderr)
	root.AddCommand(extra...)
	root.SetArgs(args)
	return root.Execute()
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	cfg := newConfig()

	root := &cobra.Command{
		Use:   "focus",
		Short: "Capture and verify Stellar XDR test fixtures",
		Long: `focus records real Stellar and Soroban data as test fixtures, and checks
that stored fixtures still match the chain.

The assertions this repository provides live in the Go package
github.com/stellar-optics/stellar-focus/pkg/focus; this command exists to
manage the fixtures those assertions run against.

Settings may come from flags, FOCUS_* environment variables, or a config file
(--config, or .focus.yaml in the working directory), in that order of
precedence.`,
		Example: `  # Record a transaction from testnet as fixtures
  focus capture tx 2b2e276a… --out testdata/

  # Check that stored fixtures still match the chain
  focus verify testdata/

  # Check them offline, against XDR dumps instead of the network
  focus verify testdata/ --file-source testdata/dumps/

  # Use mainnet for one invocation
  FOCUS_NETWORK=mainnet focus verify testdata/`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return cfg.load()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.SetOut(stdout)
	root.SetErr(stderr)

	cfg.bindRootFlags(root)

	root.AddCommand(
		newCaptureCmd(cfg, stdout, stderr),
		newEncodeCmd(cfg, stdout),
		newLedgerKeyCmd(stdout),
		newVerifyCmd(cfg, stdout, stderr),
		newRefreshCmd(stdout, stderr),
		newFmtCmd(stdout, stderr),
		newVersionCmd(stdout),
		newLintCmd(stdout, stderr),
		newDoctorCmd(cfg, stdout),
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

// newEncodeCmd builds the command that turns a raw XDR payload into a
// golden fixture.
//
// It exists because the alternative is a shell script around a Go program:
// a payload that is already in hand — in a debug log, a test case, an RPC
// response — should not need a network to become a reviewable fixture.
func newEncodeCmd(cfg *config, stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encode [xdr]",
		Short: "Turn a raw XDR payload into a golden fixture",
		Long: `Write a golden fixture from an XDR payload, without contacting a network.

The payload is base64 or hex, given as an argument or on standard input. The
XDR type is detected when --type is not given, which is ambiguous for short
payloads, so naming it is better wherever the type is known.

The fixture is written to --out, in the format its name implies, or printed to
standard output when no file is given.`,
		Example: `  focus encode "AAAAAgAAA..." --type TransactionEnvelope --out testdata/payment.golden.json
  cat envelope.xdr | focus encode --type TransactionEnvelope --out testdata/payment.golden.json --description "a payment fixture"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := readPayload(args, stdin)
			if err != nil {
				return usageErrorf("%v", err)
			}

			raw, err := payloadBytes(payload)
			if err != nil {
				return usageErrorf("%v", err)
			}

			fixture, err := golden.Build(raw, cfg.encodeType(), cfg.encodeDescription(), nil)
			if err != nil {
				return usageErrorf("%v", err)
			}

			out := cfg.outFile()
			if out == "" {
				return printFixture(fixture, stdout)
			}
			if err := golden.Save(out, fixture); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "wrote %s  (%s)\n", out, fixture.Type)
			return nil
		},
	}

	cfg.bindEncodeFlags(cmd)
	return cmd
}

// readPayload returns the XDR payload, from the argument or from standard
// input.
func readPayload(args []string, input io.Reader) (string, error) {
	if len(args) > 0 {
		return strings.TrimSpace(args[0]), nil
	}
	if isTerminal(input) {
		return "", fmt.Errorf("give the XDR payload as an argument or on standard input")
	}

	data, err := io.ReadAll(input)
	if err != nil {
		return "", fmt.Errorf("reading standard input: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// isTerminal reports whether a reader is a terminal, so a command that would
// otherwise wait for input says what it wants instead.
func isTerminal(r io.Reader) bool {
	file, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// payloadBytes decodes a payload that may be base64 or hex.
//
// Both turn up in debug logs and RPC responses, and a command that accepted
// only one of them would be half a command. When a payload reads as both —
// a short string of hex digits is valid base64 too — the reading that
// decodes as XDR wins.
func payloadBytes(payload string) ([]byte, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, fmt.Errorf("no XDR payload")
	}

	var candidates [][]byte
	if b, err := base64.StdEncoding.DecodeString(payload); err == nil {
		candidates = append(candidates, b)
	}
	if b, err := hex.DecodeString(payload); err == nil {
		candidates = append(candidates, b)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("the payload is neither base64 nor hex")
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	for _, candidate := range candidates {
		encoded := base64.StdEncoding.EncodeToString(candidate)
		if _, err := lens.Decode(encoded); err == nil {
			return candidate, nil
		}
	}
	return candidates[0], nil
}

// printFixture writes a fixture to a writer, in the JSON format the rest of
// the package uses.
func printFixture(fixture *golden.Fixture, stdout io.Writer) error {
	serializer, err := golden.SerializerFor(golden.JSON)
	if err != nil {
		return err
	}
	data, err := serializer.Marshal(fixture)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(data))
	return err
}

func newLedgerKeyCmd(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ledger-key",
		Short: "Construct a base64 LedgerKey for capture",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "account <G...>",
		Short: "Encode an account LedgerKey",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := capture.NewAccountLedgerKey(args[0])
			if err != nil {
				return err
			}
			encoded, err := capture.EncodeLedgerKey(key)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, encoded)
			return err
		},
	})
	return cmd
}

func newCaptureCmd(cfg *config, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Record real chain data as fixture files",
	}

	var ledger uint32

	txCmd := &cobra.Command{
		Use:   "tx <hash>",
		Short: "Record a transaction's envelope, result and meta",
		Long: `Fetch a transaction and write its envelope, result and meta as separate
fixture files, each stamped with the network, ledger and capture time so it
can be re-checked later with focus verify.

With --batch the hashes are read from a file instead, one per line, and the
transactions are captured a few at a time. A transaction that cannot be
captured is reported and the batch carries on.

A batch names each fixture after the transaction it came from, so two
transactions in one batch do not overwrite each other.

A capture can be narrowed before anything is written: --min-ops keeps only
transactions with at least that many operations, --only-soroban keeps only
transactions that invoke the Soroban host, and --failed-only keeps only
transactions whose result is a failure. A transaction that matches none of
them is skipped, and no fixture is written for it.

A capture from a private or staging network may carry data that must not be
committed. --sanitize masks text memos, data entry values and anything shaped
like a secret key; --mask-memo, --scrub-data and --scrub-keys pick one each.
A sanitized fixture no longer matches the chain, so verify skips it.

RPC keeps only recent history. With --archive-url, a transaction RPC no
longer has is read from a Stellar history archive instead; an archive is
not indexed by hash, so give the ledger it closed in with --ledger. An
archive holds envelopes and results but not transaction meta.`,
		Example: `  focus capture tx 2b2e276a… --out testdata/
  focus capture tx 2b2e276a… --out testdata/ --prefix failed_payment
  focus capture tx 2b2e276a… --out testdata/ --sanitize
  focus capture tx --batch hashes.txt --out testdata/ --concurrency 4
  focus capture tx --batch hashes.txt --out testdata/ --only-soroban --failed-only
  focus capture tx --batch hashes.txt --out testdata/ --min-ops 5
  focus capture tx 2b2e276a… --network standalone --rpc-url http://localhost:8000/rpc \
    --passphrase "Standalone Network ; February 2017"

  # A transaction older than RPC keeps, from the history archive
  focus capture tx 2b2e276a… --network mainnet --ledger 3966820 \
    --archive-url https://history.stellar.org/prd/core-live/core_live_001`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.batch() != "" {
				if len(args) > 0 {
					return usageErrorf("give a transaction hash or --batch, not both")
				}
				return captureBatchCmd(cmd.Context(), cfg, stdout, stderr)
			}
			if len(args) == 0 {
				return usageErrorf("give a transaction hash, or --batch to read the hashes from a file")
			}

			src, err := openSource(cfg)
			if err != nil {
				return err
			}

			records, err := capture.FetchTransaction(cmd.Context(), src, args[0], ledger)
			if errors.Is(err, capture.ErrLedgerRequired) {
				return usageErrorf("%v\n(give the ledger the transaction closed in with --ledger)", err)
			}
			if err != nil {
				return err
			}

			records, err = capture.FilterRecords(records, cfg.filters())
			if err != nil {
				return err
			}
			if len(records) == 0 {
				fmt.Fprintf(stdout, "skipped tx %s: it does not match the capture filters\n",
					shortHash(args[0]))
				return nil
			}

			return writeFixtures(cmd.Context(), src, records,
				cfg.outDir(), cfg.prefix(), cfg.force(), cfg.maskAddresses(), cfg.sanitizer(), stdout)
		},
	}

	entryCmd := &cobra.Command{
		Use:   "ledger-entry <base64-ledger-key>",
		Short: "Record a ledger entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := openSource(cfg)
			if err != nil {
				return err
			}

			records, err := src.LedgerEntry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			// Ledger entries are not transactions, so the capture filters do
			// not apply to them.
			return writeFixtures(cmd.Context(), src, records,
				cfg.outDir(), cfg.prefix(), cfg.force(), cfg.maskAddresses(), cfg.sanitizer(), stdout)
		},
	}

	simCmd := &cobra.Command{
		Use:   "simulate <envelope-xdr>",
		Short: "Record a Soroban simulateTransaction response",
		Long: `Run a transaction against the current ledger state and record the whole
response: return value, diagnostic events, authorization trees and the
SorobanTransactionData resource footprint.

A host-rejected simulation is still recorded, because the error is the
interesting part; the command then exits non-zero so a pipeline notices.`,
		Example: `  focus capture simulate "AAAAAgAAAAB..." --out testdata/`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := openSource(cfg)
			if err != nil {
				return err
			}
			simSrc, ok := src.(capture.SimulationSource)
			if !ok {
				return usageErrorf("source %q cannot simulate transactions", src.Name())
			}

			sim, simErr := capture.Simulate(cmd.Context(), simSrc,
				protocol.SimulateTransactionRequest{Transaction: args[0]})
			if sim == nil {
				return simErr
			}

			path := filepath.Join(cfg.outDir(), cfg.prefix()+"simulation.json")
			if !cfg.force() {
				if _, err := os.Stat(path); err == nil {
					return usageErrorf("%s already exists; pass --force to overwrite it", path)
				}
			}
			if err := sim.Save(path); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(stdout, "wrote %s\n", path); err != nil {
				return err
			}
			return simErr
		},
	}

	wasmCmd := &cobra.Command{
		Use:   "wasm <contract-id>",
		Short: "Record a deployed contract's WASM, hash and specification",
		Long: `Fetch a contract's WASM bytecode, verify that SHA-256 of the bytes matches
the hash the ledger stores, extract the embedded contract specification, and
write the binary beside its JSON metadata.

The code hash mismatch is reported as an error rather than written: bytes that
do not hash to the on-chain value do not describe the contract the ledger is
talking about.`,
		Example: `  focus capture wasm 0102030405060708090a0b0c0d0e0f10... --out testdata/`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := openSource(cfg)
			if err != nil {
				return err
			}

			contract, err := capture.CaptureContractWASM(cmd.Context(), src, args[0])
			if err != nil {
				return err
			}

			metadataPath, wasmPath := contract.Paths(cfg.outDir())
			if !cfg.force() {
				for _, path := range []string{metadataPath, wasmPath} {
					if _, err := os.Stat(path); err == nil {
						return usageErrorf("%s already exists; pass --force to overwrite it", path)
					}
				}
			}
			if _, _, err := contract.Save(cfg.outDir()); err != nil {
				return err
			}
			_, err = fmt.Fprintf(stdout, "wrote %s and %s\n", wasmPath, metadataPath)
			return err
		},
	}

	txCmd.Flags().Uint32Var(&ledger, "ledger", 0,
		"ledger the transaction closed in; needed to find it in a history archive")

	cfg.bindCaptureFlags(cmd)
	cfg.bindFilterFlags(txCmd)
	cfg.bindBatchFlags(txCmd)
	cmd.AddCommand(txCmd, entryCmd, simCmd, wasmCmd)
	return cmd
}

// writeFixtures captures records and writes them, refusing to clobber
// existing files unless asked.
func writeFixtures(
	ctx context.Context,
	src capture.Source,
	records []capture.Record,
	outDir, prefix string,
	force, maskAddresses bool,
	sanitizer capture.Sanitizer,
	stdout io.Writer,
) error {
	written, err := writeRecords(ctx, src, records, outDir, prefix, force, maskAddresses, sanitizer)
	if err != nil {
		return err
	}

	for _, w := range written {
		fmt.Fprintf(stdout, "wrote %s  (%s, ledger %d)\n",
			w.path, w.fixture.Type, fixtureLedger(w.fixture))
	}
	return nil
}

// writtenFixture is a fixture written to disk.
type writtenFixture struct {
	// path is where the fixture was written.
	path string
	// fixture is the fixture itself.
	fixture *golden.Fixture
}

// writeRecords captures records and writes them, refusing to clobber
// existing files unless asked.
//
// It is the part of a capture that touches the disk, separated from the
// reporting so a batch can capture many transactions and say what each of
// them produced.
func writeRecords(
	ctx context.Context,
	src capture.Source,
	records []capture.Record,
	outDir, prefix string,
	force, maskAddresses bool,
	sanitizer capture.Sanitizer,
) ([]writtenFixture, error) {
	if maskAddresses {
		masked, err := capture.Anonymize(records)
		if err != nil {
			return nil, err
		}
		for i := range masked {
			masked[i].Anonymized = true
		}
		records = masked
	}
	if sanitizer.Enabled() {
		sanitized, err := sanitizer.Sanitize(records)
		if err != nil {
			return nil, err
		}
		records = sanitized
	}
	fixtures, err := capture.Capture(ctx, src, records, now())
	if err != nil {
		return nil, err
	}

	written := make([]writtenFixture, 0, len(fixtures))
	for i, f := range fixtures {
		name := records[i].Name
		if prefix != "" {
			name = prefix + "_" + name
		}
		path := filepath.Join(outDir, name+".golden.json")

		if !force {
			if _, statErr := os.Stat(path); statErr == nil {
				return nil, fmt.Errorf(
					"%s already exists; pass --force to overwrite it, or --prefix to write alongside it", path)
			}
		}

		if err := golden.Save(path, f); err != nil {
			return nil, err
		}
		written = append(written, writtenFixture{path: path, fixture: f})
	}
	return written, nil
}

// fixtureLedger reports the ledger a fixture was captured at, for the capture
// summary.
func fixtureLedger(f *golden.Fixture) uint32 {
	if f.Captured == nil {
		return 0
	}
	return f.Captured.Ledger
}

// newRefreshCmd builds the command that re-renders the decoded section of
// stored fixtures.
//
// The refresh belongs to the fixture, not to the test that produced it: the
// XDR is the source of truth, and the decoded tree beside it is what a human
// reads. When the formatter improves, that tree goes stale, and the only way
// to fix it today is to re-record every fixture — which re-records the data
// too. This command re-renders the tree and leaves the data alone.
func newRefreshCmd(stdout, stderr io.Writer) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "refresh [paths...]",
		Short: "Re-render the decoded section of stored fixtures",
		Long: `Re-render the decoded section of every fixture from the XDR it stores.

The XDR is the source of truth; the decoded tree beside it is what a human
reads in a pull request. When the formatter improves, or the protocol gains
a field, that tree goes stale — and re-recording the fixtures to fix it
would re-record the data too. This command re-renders only the decoded
section, leaving the XDR, the type, the description and the capture
provenance as they are.

Paths may be files, directories or patterns. A directory is searched all the
way down, so fixtures kept in subdirectories are refreshed too; a pattern is
matched like a shell pattern, with "**" standing for any number of
directories. Hidden directories and the paths listed in a .focusignore file
are skipped.

With no path, testdata is refreshed.

With --dry-run nothing is written: the difference each fixture would get is
printed instead.`,
		Example: `  focus refresh testdata/
  focus refresh testdata/ --dry-run`,
		Args: cobra.ArbitraryArgs,
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

			var updated, unchanged, errored int
			for _, path := range paths {
				changed, diff, err := refreshFixture(path, dryRun)
				if err != nil {
					fmt.Fprintf(stderr, "%-40s ERROR   %v\n", path, err)
					errored++
					continue
				}
				if changed {
					word := "updated"
					if dryRun {
						word = "would change"
					}
					fmt.Fprintf(stdout, "%-40s %s\n", path, word)
					if diff != "" {
						fmt.Fprint(stdout, diff)
					}
					updated++
					continue
				}
				fmt.Fprintf(stdout, "%-40s unchanged\n", path)
				unchanged++
			}

			fmt.Fprintf(stdout, "\n%d checked: %d %s, %d unchanged",
				len(paths), updated, refreshVerb(dryRun), unchanged)
			if errored > 0 {
				fmt.Fprintf(stdout, ", %d errored", errored)
			}
			fmt.Fprintln(stdout)

			if errored > 0 {
				return failureErrorf("%d fixture(s) could not be refreshed", errored)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"show what would change without writing anything")

	return cmd
}

// refreshVerb names what the command did, which is not the same word in a
// dry run as in a real one.
func refreshVerb(dryRun bool) string {
	if dryRun {
		return "would change"
	}
	return "updated"
}

// refreshFixture re-renders one fixture, or — in a dry run — works out the
// difference it would get and writes nothing.
//
// It reports whether the fixture changed, and the diff of that change when
// there is one to show.
func refreshFixture(path string, dryRun bool) (bool, string, error) {
	if !dryRun {
		changed, err := golden.Refresh(path)
		return changed, "", err
	}

	fixture, err := golden.Load(path)
	if err != nil {
		return false, "", err
	}
	decoded, err := golden.RefreshedDecoded(fixture)
	if err != nil {
		return false, "", err
	}

	before, err := indentJSON(fixture.Decoded)
	if err != nil {
		return false, "", err
	}
	after, err := indentJSON(decoded)
	if err != nil {
		return false, "", err
	}
	if before == after {
		return false, "", nil
	}
	return true, diffLines(before, after), nil
}

// indentJSON re-indents a JSON document, so two renderings of the same tree
// can be compared line by line.
//
// The stored section carries the indentation of the file it sits in, and the
// fresh one carries the indentation of the formatter; comparing them without
// this step would report every line as changed.
func indentJSON(data []byte) (string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return "", fmt.Errorf("reading the decoded section: %w", err)
	}
	return buf.String(), nil
}

// diffLines renders the lines that differ between two texts.
//
// The diff is computed over the longest common subsequence, so a line that
// was added or removed is reported as such rather than as a change to every
// line after it.
func diffLines(before, after string) string {
	oldLines := strings.Split(before, "\n")
	newLines := strings.Split(after, "\n")

	var b strings.Builder
	for _, line := range changedLines(oldLines, newLines) {
		switch line.kind {
		case lineRemoved:
			fmt.Fprintf(&b, "  - %s\n", line.text)
		case lineAdded:
			fmt.Fprintf(&b, "  + %s\n", line.text)
		}
	}
	return b.String()
}

// lineKind says what a diff line is.
type lineKind int

const (
	// lineRemoved is a line that was in the old text and not in the new one.
	lineRemoved lineKind = iota
	// lineAdded is a line that is in the new text and not in the old one.
	lineAdded
)

// diffLine is one line of a diff.
type diffLine struct {
	kind lineKind
	text string
}

// maxDiffCells bounds the diff table. A fixture large enough to exceed it is
// compared line by line instead, which is rougher but never allocates a
// table the size of the file.
const maxDiffCells = 4_000_000

// changedLines returns the lines that differ between two texts, oldest
// first.
func changedLines(oldLines, newLines []string) []diffLine {
	if len(oldLines)*len(newLines) > maxDiffCells {
		return changedLinesLineByLine(oldLines, newLines)
	}

	// lcs[i][j] is the length of the longest common subsequence of
	// oldLines[i:] and newLines[j:].
	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			switch {
			case oldLines[i] == newLines[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []diffLine
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, diffLine{kind: lineRemoved, text: oldLines[i]})
			i++
		default:
			out = append(out, diffLine{kind: lineAdded, text: newLines[j]})
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		out = append(out, diffLine{kind: lineRemoved, text: oldLines[i]})
	}
	for ; j < len(newLines); j++ {
		out = append(out, diffLine{kind: lineAdded, text: newLines[j]})
	}
	return out
}

// changedLinesLineByLine compares two texts without a diff table, for texts
// too large for one.
func changedLinesLineByLine(oldLines, newLines []string) []diffLine {
	var out []diffLine
	for i := 0; i < len(oldLines) || i < len(newLines); i++ {
		switch {
		case i >= len(oldLines):
			out = append(out, diffLine{kind: lineAdded, text: newLines[i]})
		case i >= len(newLines):
			out = append(out, diffLine{kind: lineRemoved, text: oldLines[i]})
		case oldLines[i] != newLines[i]:
			out = append(out, diffLine{kind: lineRemoved, text: oldLines[i]})
			out = append(out, diffLine{kind: lineAdded, text: newLines[i]})
		}
	}
	return out
}

// newFmtCmd builds the command that rewrites golden fixtures in canonical
// form.
//
// It is the gofmt of fixtures: the data is already valid, but its encoding can
// drift — a hand-edit leaves four-space indentation, a Windows checkout leaves
// CRLF, a merge reorders YAML keys. Re-serializing with the format's own
// serializer settles all of that, and --check lets CI fail on drift without
// rewriting anything.
func newFmtCmd(stdout, stderr io.Writer) *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "fmt [paths...]",
		Short: "Reformat golden fixtures in place",
		Long: `Reformat golden fixture files in place.

Every file is parsed and re-serialized by the canonical serializer for its
format: JSON uses the field order the writer defines with two-space
indentation, YAML and the text form sort their keys, and every line ending is
LF with trailing whitespace removed. The fixture's data is not refreshed — a
stale decoded tree stays stale until focus refresh — only its encoding is
normalized.

Paths may be files, directories or patterns. A directory is searched all the
way down, so fixtures kept in subdirectories are formatted too; a pattern is
matched like a shell pattern, with "**" standing for any number of
directories. Hidden directories and the paths listed in a .focusignore file
are skipped.

With no path, testdata is formatted.

With --check nothing is written: the command exits with status 1 when any
fixture is not formatted, which is what a CI job should run.`,
		Example: `  focus fmt testdata/
  focus fmt testdata/ --check
  focus fmt testdata/payment.golden.json`,
		Args: cobra.ArbitraryArgs,
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

			var changed, unchanged, errored int
			for _, path := range paths {
				formatted, err := formatFixture(path, check)
				if err != nil {
					fmt.Fprintf(stderr, "%-40s ERROR   %v\n", path, err)
					errored++
					continue
				}
				if formatted {
					word := "updated"
					if check {
						word = "would change"
					}
					fmt.Fprintf(stdout, "%-40s %s\n", path, word)
					changed++
					continue
				}
				fmt.Fprintf(stdout, "%-40s unchanged\n", path)
				unchanged++
			}

			fmt.Fprintf(stdout, "\n%d checked: %d %s, %d unchanged",
				len(paths), changed, fmtVerb(check), unchanged)
			if errored > 0 {
				fmt.Fprintf(stdout, ", %d errored", errored)
			}
			fmt.Fprintln(stdout)

			if errored > 0 {
				return failureErrorf("%d fixture(s) could not be formatted", errored)
			}
			if check && changed > 0 {
				return failureErrorf("%d fixture(s) are not formatted", changed)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false,
		"exit with status 1 if any file is not formatted (CI-friendly)")

	return cmd
}

// formatFixture canonicalizes one fixture, or — in check mode — reports
// whether it would change and writes nothing.
func formatFixture(path string, check bool) (bool, error) {
	if check {
		return golden.CheckFile(path)
	}
	return golden.FormatFile(path)
}

// fmtVerb names what the command did, which is not the same word in a check
// run as in a formatting one.
func fmtVerb(check bool) string {
	if check {
		return "would change"
	}
	return "updated"
}
