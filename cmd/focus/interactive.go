// focus assert — the interactive assertion generator.
//
// The command turns a transaction someone already has — a base64 string from
// a log, a fixture file, a capture — into the focus assertions that describe
// it, so the first draft of a test writes itself.
//
// The flow is deliberately small: decode the base64 XDR with lens, summarise
// it, offer one suggestion per field the transaction actually carries, and
// print the kept ones. Prompts go to standard error and generated code to
// standard output, so `focus assert … > snippet.go` yields a file of nothing
// but assertions. Nothing here touches the network: decode, summary and the
// assertions behind each suggestion all run locally, exactly as they would
// inside a failing test.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// newAssertCmd builds the `focus assert` subcommand.
//
// The command is assembled here rather than in internal/cli so the whole
// interactive flow lives in one file; cmd/focus passes it to cli.Execute as
// an extra root command at startup, which keeps internal/cli about fixtures
// (capture, verify) and this file about test authoring.
func newAssertCmd() *cobra.Command {
	var (
		acceptAll bool
		varName   string
	)

	cmd := &cobra.Command{
		Use:   "assert [transaction-xdr]",
		Short: "Interactively generate focus assertions for a transaction",
		Long: `focus assert decodes a transaction and walks you through the focus
assertions it suggests, printing the ones you keep as paste-ready Go.

Give the transaction as base64 XDR as an argument, or press Enter at the
prompt and paste it. Prompts are written to standard error and generated
code to standard output, so the output can go straight into a test file:

  focus assert AAAA… --var tx > assertions.txt

Each suggestion shows the exact call it generates. Keep it with y or just
Enter, drop it with n, keep everything left with a, stop with q. When input
ends — a pipe ran dry or Ctrl-D — the remaining suggestions are kept.

Everything runs offline: lens decodes the transaction and pkg/focus
summarises it locally; no network call is made.`,
		Example: `  # Walk through the suggestions for a transaction
  focus assert AAAA…  # Emit every suggestion without prompting
  focus assert AAAA… --all --var env`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssert(cmd, args, acceptAll, varName)
		},
	}

	cmd.Flags().BoolVarP(&acceptAll, "all", "a", false,
		"keep every suggestion without prompting")
	cmd.Flags().StringVar(&varName, "var", "tx",
		"variable name the generated assertions refer to the transaction by")
	return cmd
}

// runAssert is the command body: pick the payload, decode it, build the
// suggestions, walk the user through them, print what was kept.
func runAssert(cmd *cobra.Command, args []string, acceptAll bool, varName string) error {
	if !isGoIdentifier(varName) {
		return fmt.Errorf("--var must be a Go identifier, got %q", varName)
	}

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	in := bufio.NewReader(cmd.InOrStdin())

	payload, err := transactionPayload(in, stderr, args)
	if err != nil {
		return err
	}

	decoded, err := lens.DecodeAs(payload, "TransactionEnvelope")
	if err != nil {
		return fmt.Errorf("reading the transaction: %w", err)
	}
	summary, err := lens.Explain(decoded)
	if err != nil {
		return fmt.Errorf("reading the transaction: %w", err)
	}
	env, ok := decoded.Raw.(*xdr.TransactionEnvelope)
	if !ok {
		return fmt.Errorf("internal: decoded a %T, want a TransactionEnvelope", decoded.Raw)
	}

	candidates := buildCandidates(summary, env, varName)
	if len(candidates) == 0 {
		return errors.New("the transaction carries nothing to assert")
	}

	if summary.Headline != "" {
		fmt.Fprintln(stderr, summary.Headline)
	}
	fmt.Fprintf(stderr, "%d suggestion(s) below; generated code goes to stdout.\n",
		len(candidates))

	accepted := candidates
	if !acceptAll {
		var err error
		accepted, err = selectSuggestions(in, stderr, candidates)
		if err != nil {
			return err
		}
		// The last prompt has no trailing newline of its own.
		fmt.Fprintln(stderr)
	}

	for _, c := range accepted {
		fmt.Fprintln(stdout, c.snippet)
	}
	fmt.Fprintf(stderr, "Generated %d of %d suggested assertion(s).\n",
		len(accepted), len(candidates))
	return nil
}

// transactionPayload picks the base64 XDR to decode: the positional
// argument when one was given, otherwise the first line pasted at the
// prompt.
func transactionPayload(in *bufio.Reader, stderr io.Writer, args []string) (string, error) {
	if len(args) == 1 {
		payload := strings.TrimSpace(args[0])
		if payload == "" {
			return "", errors.New("the transaction argument is empty")
		}
		return payload, nil
	}

	fmt.Fprint(stderr, "Transaction base64 (paste and press Enter): ")
	line, err := readLine(in)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the transaction: %w", err)
	}
	if line == "" {
		return "", errors.New("no transaction given: pass the base64 XDR as an " +
			"argument, or paste it at the prompt")
	}
	return line, nil
}

// selectSuggestions shows each suggestion in turn and asks whether to keep
// it.
//
// y or an empty line keeps, n drops, a keeps everything left, q stops.
// When input ends — the answers ran out, or stdin closed — the remaining
// suggestions are kept, so a scripted prefix of answers still yields the
// whole file rather than a truncated one. Prompts go to stderr; stdout
// stays reserved for generated code.
func selectSuggestions(in *bufio.Reader, stderr io.Writer, candidates []candidate) ([]candidate, error) {
	var accepted []candidate
	for i, c := range candidates {
		fmt.Fprintf(stderr, "\n[%d/%d] %s\n      %s\n",
			i+1, len(candidates), c.title, c.snippet)
	prompt:
		for {
			fmt.Fprint(stderr, "  keep? [Y/n/a/q] ")
			line, err := readLine(in)
			if err != nil && !errors.Is(err, io.EOF) {
				return accepted, fmt.Errorf("reading your answer: %w", err)
			}
			eof := errors.Is(err, io.EOF)

			answer, answered := parseAnswer(line)
			if !answered {
				if eof {
					return append(accepted, candidates[i:]...), nil
				}
				fmt.Fprintln(stderr, "  please answer y, n, a or q")
				continue
			}

			if answer == answerAll {
				return append(accepted, candidates[i:]...), nil
			}
			if answer == answerQuit {
				return accepted, nil
			}
			if answer == answerYes {
				accepted = append(accepted, c)
			}
			if eof {
				return append(accepted, candidates[i+1:]...), nil
			}
			// The question is answered; move to the next suggestion.
			break prompt
		}
	}
	return accepted, nil
}

// candidate is one suggested assertion: its prompt title, the code it
// generates, and the assertion itself. check runs that assertion against
// any transaction, which is how the tests prove a generated assertion holds
// for the transaction it came from and fails with a descriptive diff on a
// transaction it did not.
type candidate struct {
	title   string
	snippet string
	check   func(t focus.TB, tx focus.Value) bool
}

// buildCandidates turns a decoded transaction into the assertions worth
// suggesting.
//
// Optional fields — memo, fee-bump wrapper, time and ledger bounds — are
// offered only when the transaction actually carries them: suggesting
// AssertMemo for a memo-less transaction would be noise the user has to
// answer no to.
func buildCandidates(s *lens.Summary, env *xdr.TransactionEnvelope, varName string) []candidate {
	var out []candidate
	add := func(title, snippet string, check func(focus.TB, focus.Value) bool) {
		out = append(out, candidate{title: title, snippet: snippet, check: check})
	}

	if s.Source != "" {
		add("source account",
			fmt.Sprintf("focus.AssertSourceAccount(t, %s, %q)", varName, s.Source),
			func(t focus.TB, tx focus.Value) bool {
				return focus.AssertSourceAccount(t, tx, s.Source)
			})
	}

	add("fee",
		fmt.Sprintf("focus.AssertFee(t, %s, %d)", varName, s.Fee),
		func(t focus.TB, tx focus.Value) bool {
			return focus.AssertFee(t, tx, s.Fee)
		})

	add("sequence number",
		fmt.Sprintf("focus.AssertSequence(t, %s, %d)", varName, s.SeqNum),
		func(t focus.TB, tx focus.Value) bool {
			return focus.AssertSequence(t, tx, s.SeqNum)
		})

	if s.Memo != "" {
		add("memo",
			fmt.Sprintf("focus.AssertMemo(t, %s, %q)", varName, s.Memo),
			func(t focus.TB, tx focus.Value) bool {
				return focus.AssertMemo(t, tx, s.Memo)
			})
	}

	add("operation count",
		fmt.Sprintf("focus.AssertOperationCount(t, %s, %d)", varName, len(s.Operations)),
		func(t focus.TB, tx focus.Value) bool {
			return focus.AssertOperationCount(t, tx, len(s.Operations))
		})

	if len(s.Operations) > 0 {
		types := make([]string, 0, len(s.Operations))
		for _, op := range s.Operations {
			types = append(types, op.Type)
		}
		quoted := make([]string, 0, len(types))
		for _, opType := range types {
			quoted = append(quoted, fmt.Sprintf("%q", opType))
		}
		add("operation types",
			fmt.Sprintf("focus.AssertOperationTypes(t, %s, %s)", varName, strings.Join(quoted, ", ")),
			func(t focus.TB, tx focus.Value) bool {
				return focus.AssertOperationTypes(t, tx, types...)
			})
	}

	add("signature count",
		fmt.Sprintf("focus.AssertSignatureCount(t, %s, %d)", varName, s.SignatureCt),
		func(t focus.TB, tx focus.Value) bool {
			return focus.AssertSignatureCount(t, tx, s.SignatureCt)
		})

	if s.FeeBumpSource != "" {
		add("fee-bump wrapper",
			fmt.Sprintf("focus.AssertFeeBump(t, %s, true)", varName),
			func(t focus.TB, tx focus.Value) bool {
				return focus.AssertFeeBump(t, tx, true)
			})
	}

	if tb := env.TimeBounds(); tb != nil {
		minTime, maxTime := int64(tb.MinTime), int64(tb.MaxTime)
		add("time bounds",
			fmt.Sprintf("focus.AssertTimeboundsValid(t, %s, %d, %d)", varName, minTime, maxTime),
			func(t focus.TB, tx focus.Value) bool {
				return focus.AssertTimeboundsValid(t, tx, minTime, maxTime)
			})
	}

	if lb := env.LedgerBounds(); lb != nil {
		minLedger, maxLedger := uint32(lb.MinLedger), uint32(lb.MaxLedger)
		add("ledger bounds",
			fmt.Sprintf("focus.AssertLedgerboundsValid(t, %s, %d, %d)", varName, minLedger, maxLedger),
			func(t focus.TB, tx focus.Value) bool {
				return focus.AssertLedgerboundsValid(t, tx, minLedger, maxLedger)
			})
	}

	return out
}

// answer is one response to a keep prompt.
type answer int

const (
	answerYes answer = iota
	answerNo
	answerAll
	answerQuit
)

// parseAnswer reads a keep response. An empty line is yes — the prompt
// shows [Y/n/a/q] — so pressing Enter simply takes the suggestion.
func parseAnswer(line string) (answer, bool) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return answerYes, true
	case "n", "no":
		return answerNo, true
	case "a", "all":
		return answerAll, true
	case "q", "quit":
		return answerQuit, true
	}
	return 0, false
}

// readLine reads one input line with the newline and surrounding space
// trimmed. It returns io.EOF — alongside whatever partial line came before
// it — when input ends.
func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	return strings.TrimSpace(line), err
}

// isGoIdentifier reports whether s can name a Go variable: a letter or
// underscore followed by letters, digits or underscores, and not nothing but
// underscores — the blank identifier names nothing, and a generated
// assertion that reads focus.AssertFee(t, _, 100) would not compile.
func isGoIdentifier(s string) bool {
	if s == "" || strings.Trim(s, "_") == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}
