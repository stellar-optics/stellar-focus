// This file implements `focus golden resolve`: the interactive review step
// between "a fixture changed somewhere" and "the golden file now says so".

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// decision is the answer to one conflict prompt.
type decision int

const (
	decisionAccept decision = iota
	decisionReject
	decisionAcceptAll
	decisionQuit
)

// resolveRequest is one run of the resolver: proposed fixtures on one side,
// golden files on the other, and the terminal in between.
type resolveRequest struct {
	// candidate is a file or directory of proposed fixtures.
	candidate string
	// paths are the golden files to resolve against; testdata when empty.
	paths []string
	// color is the diff palette mode: "auto", "always" or "never".
	color string
	// in holds the answers to the accept/reject prompts.
	in io.Reader
	// out receives the diffs, prompts and summary.
	out io.Writer
	// errOut receives warnings about fixtures that could not be processed.
	errOut io.Writer
}

// newGoldenCmd builds the `golden` command group. The core is
// resolveGoldenUpdates; this is only the flag and stream plumbing.
func newGoldenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "golden",
		Short: "Maintain golden fixture files",
	}

	var candidate string
	resolve := &cobra.Command{
		Use:   "resolve [paths...]",
		Short: "Interactively accept or reject proposed golden updates",
		Long: `Show the diff for every proposed fixture that differs from the golden file
of the same name, then ask whether to accept the proposal or keep the stored
one.

The candidate file or directory mirrors the golden files it proposes to
update, matched by filename — a .golden.json.gz proposal updates the plain
fixture of the same name and vice versa. Paths may be files or directories;
with no path, testdata is resolved. Nothing here touches the network.`,
		Example: `  focus golden resolve --candidate updates/
  focus golden resolve --candidate updates/ testdata/`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if candidate == "" {
				return errors.New(
					"pass --candidate <file-or-directory> with the proposed fixtures to resolve")
			}
			paths := args
			if len(paths) == 0 {
				paths = []string{"testdata"}
			}
			// --color is the root command's persistent flag, inherited here
			// when the command runs under the focus root; standalone there
			// is nothing to inherit and "auto" applies.
			color := "auto"
			if f := cmd.Flags().Lookup("color"); f != nil {
				color = f.Value.String()
			}
			return resolveGoldenUpdates(resolveRequest{
				candidate: candidate,
				paths:     paths,
				color:     color,
				in:        cmd.InOrStdin(),
				out:       cmd.OutOrStdout(),
				errOut:    cmd.ErrOrStderr(),
			})
		},
	}
	resolve.Flags().StringVarP(&candidate, "candidate", "c", "",
		"file or directory holding the proposed fixtures")

	cmd.AddCommand(resolve)
	return cmd
}

// resolveGoldenUpdates walks the proposed fixtures, shows each difference
// against the golden file it targets, and applies the answers.
//
// It is entirely offline: every input is a local file and the only effect is
// on files the user pointed it at.
func resolveGoldenUpdates(req resolveRequest) error {
	candidates, err := proposedFixtures(req.candidate)
	if err != nil {
		return err
	}
	targets, err := goldenTargets(req.paths)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(req.in)
	var accepted, rejected, unchanged, skipped, unresolved, failed int
	acceptAll := false

	for i, candidate := range candidates {
		target, ok := targets[fixtureKey(candidate)]
		if !ok {
			skipped++
			fmt.Fprintf(req.out, "%s: no golden file to update; skipped\n", candidate)
			continue
		}

		proposed, err := golden.ReadFile(candidate)
		if err != nil {
			failed++
			fmt.Fprintf(req.errOut, "%s: %v\n", candidate, err)
			continue
		}
		current, err := golden.ReadFile(target)
		if err != nil {
			failed++
			fmt.Fprintf(req.errOut, "%s: %v\n", target, err)
			continue
		}
		if bytes.Equal(proposed, current) {
			unchanged++
			continue
		}
		if _, err := parseFixture(candidate, proposed); err != nil {
			failed++
			fmt.Fprintf(req.errOut, "%v\n", err)
			continue
		}
		if _, err := parseFixture(target, current); err != nil {
			failed++
			fmt.Fprintf(req.errOut, "%v\n", err)
			continue
		}

		if !acceptAll {
			fmt.Fprintf(req.out, "\n%s\n", target)
			if diffErr := renderFixtureDiff(req.out, current, proposed, req.color); diffErr != nil {
				fmt.Fprintf(req.out, "  (could not render the diff: %v)\n", diffErr)
			}
			answer, err := promptDecision(req, scanner)
			if err != nil {
				return err
			}
			if answer == decisionQuit {
				unresolved = len(candidates) - i
				fmt.Fprintf(req.out, "\nstopping; %d conflict(s) left unresolved\n", unresolved)
				break
			}
			if answer == decisionAcceptAll {
				acceptAll = true
			}
			if answer == decisionReject {
				rejected++
				fmt.Fprintf(req.out, "  kept %s\n", target)
				continue
			}
		}

		if err := golden.WriteFile(target, proposed); err != nil {
			failed++
			fmt.Fprintf(req.errOut, "%s: %v\n", target, err)
			continue
		}
		accepted++
		fmt.Fprintf(req.out, "  accepted %s\n", target)
	}

	fmt.Fprintf(req.out,
		"\n%d proposed: %d accepted, %d rejected, %d unchanged, %d skipped, %d unresolved\n",
		len(candidates), accepted, rejected, unchanged, skipped, unresolved)

	if failed > 0 {
		return fmt.Errorf("%d proposed fixture(s) could not be processed", failed)
	}
	return nil
}

// promptDecision asks what to do with one conflict, re-asking until the
// answer is recognisable. A closed input stream stops the run rather than
// guessing: an automated caller that forgot the answers must not get a
// half-resolved directory.
func promptDecision(req resolveRequest, scanner *bufio.Scanner) (decision, error) {
	for {
		fmt.Fprint(req.out, "  accept? [y]es [n]o [a]ll [q]uit: ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return decisionQuit, fmt.Errorf("reading answers: %w", err)
			}
			fmt.Fprintln(req.out, "\ninput closed; stopping")
			return decisionQuit, nil
		}
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "y", "yes":
			return decisionAccept, nil
		case "n", "no":
			return decisionReject, nil
		case "a", "all":
			return decisionAcceptAll, nil
		case "q", "quit":
			return decisionQuit, nil
		default:
			fmt.Fprintln(req.out, "  please answer y, n, a or q")
		}
	}
}

// proposedFixtures lists the candidate files to review, sorted so the order
// of review is the same on every machine.
func proposedFixtures(candidate string) ([]string, error) {
	info, err := os.Stat(candidate)
	if err != nil {
		return nil, fmt.Errorf("reading candidate %s: %w", candidate, err)
	}
	if !info.IsDir() {
		return []string{candidate}, nil
	}
	found, err := globFixtures(candidate)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no proposed fixtures found in %s", candidate)
	}
	return found, nil
}

// goldenTargets indexes the golden files by fixtureKey, mirroring how
// `focus verify` collects paths: files are taken as they are, directories
// are scanned for fixture files.
func goldenTargets(paths []string) (map[string]string, error) {
	targets := map[string]string{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if !info.IsDir() {
			targets[fixtureKey(path)] = path
			continue
		}
		found, err := globFixtures(path)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			targets[fixtureKey(f)] = f
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no fixture files found in %s", strings.Join(paths, ", "))
	}
	return targets, nil
}

// globFixtures finds fixture files in dir, compressed or not, sorted.
func globFixtures(dir string) ([]string, error) {
	var out []string
	for _, pattern := range []string{"*.golden.json", "*.golden.json" + golden.GzipSuffix} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", dir, err)
		}
		out = append(out, matches...)
	}
	slices.Sort(out)
	return out, nil
}

// fixtureKey is the name a proposal and a golden file share, with the
// compression suffix erased, so either form proposes an update to the other.
func fixtureKey(path string) string {
	return strings.TrimSuffix(filepath.Base(path), golden.GzipSuffix)
}

// parseFixture validates that raw really is a golden fixture — the same
// contract Load enforces — before anything proposes to overwrite a golden
// file with it.
func parseFixture(path string, raw []byte) (*golden.Fixture, error) {
	var f golden.Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s is not a golden fixture: %w", path, err)
	}
	if f.XDR == "" {
		return nil, fmt.Errorf("%s has no xdr field", path)
	}
	return &f, nil
}

// renderFixtureDiff writes the difference between the stored and proposed
// fixture: the same structured, field-path diff `focus verify` shows, so a
// decision here reads like every other drift report in the toolkit. When only
// metadata or formatting moved, that is said plainly instead of printing an
// empty diff.
func renderFixtureDiff(out io.Writer, current, proposed []byte, color string) error {
	old, err := parseFixture("stored fixture", current)
	if err != nil {
		return err
	}
	fresh, err := parseFixture("proposed fixture", proposed)
	if err != nil {
		return err
	}
	if old.XDR == fresh.XDR {
		fmt.Fprintln(out, "  payload unchanged; only metadata or formatting differs")
		return nil
	}

	typeName := old.Type
	if typeName == "" {
		typeName = fresh.Type
	}
	diff, err := lens.DiffBase64(old.XDR, fresh.XDR, typeName)
	if err != nil {
		return err
	}
	f := lensformat.NewDiffFormatter(
		lensformat.WithDiffPalette(lensformat.PaletteFor(out, color)))
	if err := f.Format(out, diff); err != nil {
		return fmt.Errorf("rendering the diff: %w", err)
	}
	return nil
}
