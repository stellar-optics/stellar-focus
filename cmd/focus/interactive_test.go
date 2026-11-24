package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
)

// Real mainnet addresses (the same pair pkg/focus uses), so lens's strkey
// enrichers fire and the generated snippets contain what users will see.
const (
	txSource = "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"
	txDest   = "GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ"
)

// testTx is the input to buildTx: every field a suggestion is generated
// from, with zero values meaning "leave it out".
type testTx struct {
	source   string // defaults to txSource
	fee      uint32
	seq      int64
	memoText string // raw memo text; "" builds no memo
	ops      int    // number of native payments
	sigs     []string
	tb       *xdr.TimeBounds
	lb       *xdr.LedgerBounds
}

// testMuxed parses an address into a muxed account.
func testMuxed(t *testing.T, addr string) xdr.MuxedAccount {
	t.Helper()
	var aid xdr.AccountId
	if err := aid.SetAddress(addr); err != nil {
		t.Fatalf("parsing address %s: %v", addr, err)
	}
	return aid.ToMuxedAccount()
}

// testSignature builds a signature whose hint is the hint the protocol
// derives for the signer, so lens's enrichers treat it like a real one.
func testSignature(t *testing.T, addr string) xdr.DecoratedSignature {
	t.Helper()

	vb, err := strkey.Version(addr)
	if err != nil {
		t.Fatalf("reading the version of %s: %v", addr, err)
	}
	raw, err := strkey.Decode(vb, addr)
	if err != nil {
		t.Fatalf("decoding %s: %v", addr, err)
	}
	var hint xdr.SignatureHint
	copy(hint[:], raw[:4])
	return xdr.DecoratedSignature{
		Hint:      hint,
		Signature: xdr.Signature(bytes.Repeat([]byte{0x5a}, 64)),
	}
}

// buildTx assembles and base64-encodes a v1 envelope.
func buildTx(t *testing.T, cfg testTx) string {
	t.Helper()

	if cfg.source == "" {
		cfg.source = txSource
	}
	if cfg.ops <= 0 {
		cfg.ops = 1
	}

	ops := make([]xdr.Operation, 0, cfg.ops)
	for i := 0; i < cfg.ops; i++ {
		ops = append(ops, xdr.Operation{Body: xdr.OperationBody{
			Type: xdr.OperationTypePayment,
			PaymentOp: &xdr.PaymentOp{
				Destination: testMuxed(t, txDest),
				Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
				Amount:      xdr.Int64(1_000_000_000),
			},
		}})
	}

	memo := xdr.Memo{Type: xdr.MemoTypeMemoNone}
	if cfg.memoText != "" {
		text := cfg.memoText
		memo = xdr.Memo{Type: xdr.MemoTypeMemoText, Text: &text}
	}

	cond := xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone}
	switch {
	case cfg.tb != nil && cfg.lb != nil:
		cond = xdr.Preconditions{
			Type: xdr.PreconditionTypePrecondV2,
			V2:   &xdr.PreconditionsV2{TimeBounds: cfg.tb, LedgerBounds: cfg.lb},
		}
	case cfg.tb != nil:
		cond = xdr.Preconditions{
			Type:       xdr.PreconditionTypePrecondTime,
			TimeBounds: cfg.tb,
		}
	case cfg.lb != nil:
		cond = xdr.Preconditions{
			Type: xdr.PreconditionTypePrecondV2,
			V2:   &xdr.PreconditionsV2{LedgerBounds: cfg.lb},
		}
	}

	var sigs []xdr.DecoratedSignature
	for _, addr := range cfg.sigs {
		sigs = append(sigs, testSignature(t, addr))
	}

	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: testMuxed(t, cfg.source),
				Fee:           xdr.Uint32(cfg.fee),
				SeqNum:        xdr.SequenceNumber(cfg.seq),
				Cond:          cond,
				Memo:          memo,
				Operations:    ops,
			},
			Signatures: sigs,
		},
	}
	return mustEncodeTx(t, env)
}

// buildFeeBumpTx wraps a v1 envelope in a fee-bump envelope.
func buildFeeBumpTx(t *testing.T, inner string) string {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		t.Fatalf("decoding inner envelope: %v", err)
	}
	var env xdr.TransactionEnvelope
	if err := env.UnmarshalBinary(raw); err != nil {
		t.Fatalf("unmarshalling inner envelope: %v", err)
	}
	if env.V1 == nil {
		t.Fatalf("inner envelope is %s, want a v1 envelope", env.Type)
	}

	return mustEncodeTx(t, xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTxFeeBump,
		FeeBump: &xdr.FeeBumpTransactionEnvelope{
			Tx: xdr.FeeBumpTransaction{
				FeeSource: testMuxed(t, txDest),
				Fee:       200,
				InnerTx: xdr.FeeBumpTransactionInnerTx{
					Type: xdr.EnvelopeTypeEnvelopeTypeTx,
					V1: &xdr.TransactionV1Envelope{
						Tx:         env.V1.Tx,
						Signatures: env.V1.Signatures,
					},
				},
			},
		},
	})
}

// mustEncodeTx marshals an envelope to base64 XDR.
func mustEncodeTx(t *testing.T, env xdr.TransactionEnvelope) string {
	t.Helper()
	raw, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("marshalling envelope: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// originalTx is the rich transaction every suggestion is generated from:
// memo, signatures, and both kinds of bound.
func originalTx(t *testing.T) string {
	t.Helper()
	return buildTx(t, testTx{
		fee:      1200,
		seq:      777,
		memoText: "hello",
		ops:      1,
		sigs:     []string{txSource},
		tb:       &xdr.TimeBounds{MinTime: 1735689600, MaxTime: 1735776000},
		lb:       &xdr.LedgerBounds{MinLedger: 100, MaxLedger: 200},
	})
}

// otherTx differs from originalTx in every field an assertion is generated
// for, so each generated assertion must fail against it.
func otherTx(t *testing.T) string {
	t.Helper()
	return buildTx(t, testTx{
		source:   txDest,
		fee:      555,
		seq:      42,
		memoText: "bye",
		ops:      2,
		tb:       &xdr.TimeBounds{MinTime: 0, MaxTime: 999},
	})
}

// buildTxResult returns a base64 TransactionResult: valid XDR, but not an
// envelope — the type check the command must refuse.
func buildTxResult(t *testing.T) string {
	t.Helper()
	res := xdr.TransactionResult{
		FeeCharged: 100,
		Result:     xdr.TransactionResultResult{Code: xdr.TransactionResultCodeTxSuccess},
	}
	results := []xdr.OperationResult{}
	res.Result.Results = &results
	raw, err := res.MarshalBinary()
	if err != nil {
		t.Fatalf("marshalling result: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// summaryOf decodes an envelope the way the command does.
func summaryOf(t *testing.T, payload string) (*lens.Summary, *xdr.TransactionEnvelope) {
	t.Helper()
	decoded, err := lens.DecodeAs(payload, "TransactionEnvelope")
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	summary, err := lens.Explain(decoded)
	if err != nil {
		t.Fatalf("explaining: %v", err)
	}
	env, ok := decoded.Raw.(*xdr.TransactionEnvelope)
	if !ok {
		t.Fatalf("decoded a %T, want a TransactionEnvelope", decoded.Raw)
	}
	return summary, env
}

// runAssertForTest executes the command with scripted stdin and captured
// outputs.
func runAssertForTest(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	cmd := newAssertCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

// failingReader fails every read, so the command's read-error paths are
// exercised without a real broken pipe.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// TestAssertAllPrintsEverySuggestion pins the non-interactive contract:
// stdout is nothing but the generated calls, in transaction-field order,
// with the default variable name.
func TestAssertAllPrintsEverySuggestion(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t, "", originalTx(t), "--all")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := strings.Join([]string{
		`focus.AssertSourceAccount(t, tx, "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6")`,
		`focus.AssertFee(t, tx, 1200)`,
		`focus.AssertSequence(t, tx, 777)`,
		`focus.AssertMemo(t, tx, "text: hello")`,
		`focus.AssertOperationCount(t, tx, 1)`,
		`focus.AssertOperationTypes(t, tx, "payment")`,
		`focus.AssertSignatureCount(t, tx, 1)`,
		`focus.AssertTimeboundsValid(t, tx, 1735689600, 1735776000)`,
		`focus.AssertLedgerboundsValid(t, tx, 100, 200)`,
	}, "\n") + "\n"

	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
	for _, want := range []string{"9 suggestion(s)", "Generated 9 of 9"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

// TestAssertVarFlagNamesTheTransaction checks the variable name flag
// end-to-end.
func TestAssertVarFlagNamesTheTransaction(t *testing.T) {
	t.Parallel()

	stdout, _, err := runAssertForTest(t, "", originalTx(t), "--all", "--var", "env")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout, "focus.AssertFee(t, env, 1200)") {
		t.Errorf("--var was not applied to the generated code:\n%s", stdout)
	}
	if strings.Contains(stdout, "(t, tx,") {
		t.Errorf("generated code still uses the default variable:\n%s", stdout)
	}
}

// TestAssertInteractiveKeepsAndSkips walks a full set of answers: keep the
// first, skip the second, keep the rest — stdout must contain exactly the
// kept suggestions in order.
func TestAssertInteractiveKeepsAndSkips(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t,
		"y\nn\ny\ny\ny\ny\ny\ny\ny\n", originalTx(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if strings.Contains(stdout, "AssertFee(") {
		t.Errorf("a skipped suggestion reached stdout:\n%s", stdout)
	}
	if want := 8; len(strings.Split(strings.TrimSpace(stdout), "\n")) != want {
		t.Errorf("kept %d suggestions, want %d:\n%s",
			len(strings.Split(strings.TrimSpace(stdout), "\n")), want, stdout)
	}
	if !strings.HasPrefix(stdout, "focus.AssertSourceAccount(") {
		t.Errorf("first kept suggestion is not the source account:\n%s", stdout)
	}
	for _, want := range []string{"[9/9]", "Generated 8 of 9"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

// TestAssertInteractiveQuitStopsEarly checks q keeps what was accepted so
// far and asks nothing further.
func TestAssertInteractiveQuitStopsEarly(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t, "y\nq\n", originalTx(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if want := "focus.AssertSourceAccount(t, tx, \"GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6\")\n"; stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
	if strings.Contains(stderr, "[3/9]") {
		t.Errorf("questions were asked after quit:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Generated 1 of 9") {
		t.Errorf("stderr does not report the count:\n%s", stderr)
	}
}

// TestAssertEOFAcceptsTheRest covers the documented end-of-input default:
// no answers at all keeps every suggestion.
func TestAssertEOFAcceptsTheRest(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t, "", originalTx(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := 9; len(strings.Split(strings.TrimSpace(stdout), "\n")) != want {
		t.Errorf("kept %d suggestions, want %d:\n%s",
			len(strings.Split(strings.TrimSpace(stdout), "\n")), want, stdout)
	}
	if !strings.Contains(stderr, "Generated 9 of 9") {
		t.Errorf("stderr does not report the count:\n%s", stderr)
	}
}

// TestAssertSkipThenEOFKeepsTheRest checks the partial-answer case: one
// explicit no, then input ends — the no stands, the rest are kept.
func TestAssertSkipThenEOFKeepsTheRest(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t, "n\n", originalTx(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(stdout, "AssertSourceAccount(") {
		t.Errorf("the skipped suggestion reached stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Generated 8 of 9") {
		t.Errorf("stderr does not report the count:\n%s", stderr)
	}
}

// TestAssertAllKeywordKeepsEverythingLeft checks the mid-session a answer.
func TestAssertAllKeywordKeepsEverythingLeft(t *testing.T) {
	t.Parallel()

	stdout, _, err := runAssertForTest(t, "y\na\n", originalTx(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := 9; len(strings.Split(strings.TrimSpace(stdout), "\n")) != want {
		t.Errorf("kept %d suggestions, want %d:\n%s",
			len(strings.Split(strings.TrimSpace(stdout), "\n")), want, stdout)
	}
}

// TestAssertInvalidAnswerIsReprompted checks a bad answer is refused
// without losing the suggestion it was about.
func TestAssertInvalidAnswerIsReprompted(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t, "huh\nn\n", originalTx(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stderr, "please answer y, n, a or q") {
		t.Errorf("stderr does not report the bad answer:\n%s", stderr)
	}
	if strings.Contains(stdout, "AssertSourceAccount(") {
		t.Errorf("the refused suggestion reached stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Generated 8 of 9") {
		t.Errorf("stderr does not report the count:\n%s", stderr)
	}
}

// TestAssertPartialAnswerAtEOFKeepsRest covers an answer that arrived with
// no trailing newline when the input ended on it: it counts as the end of
// input, and the rest are kept.
func TestAssertPartialAnswerAtEOFKeepsRest(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t, "huh", originalTx(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := 9; len(strings.Split(strings.TrimSpace(stdout), "\n")) != want {
		t.Errorf("kept %d suggestions, want %d:\n%s",
			len(strings.Split(strings.TrimSpace(stdout), "\n")), want, stdout)
	}
	if !strings.Contains(stderr, "Generated 9 of 9") {
		t.Errorf("stderr does not report the count:\n%s", stderr)
	}
}

// TestAssertPromptsForMissingPayload checks the no-argument flow: the
// base64 is pasted when asked for.
func TestAssertPromptsForMissingPayload(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runAssertForTest(t, originalTx(t)+"\nq\n")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout != "" {
		t.Errorf("quit at the first question but stdout =\n%s", stdout)
	}
	for _, want := range []string{
		"Transaction base64 (paste and press Enter)",
		"[1/9] source account",
		"Generated 0 of 9",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

// TestAssertMissingPayloadErrors checks an empty session says so instead of
// printing nothing quietly.
func TestAssertMissingPayloadErrors(t *testing.T) {
	t.Parallel()

	stdout, _, err := runAssertForTest(t, "")
	if err == nil {
		t.Fatal("Execute() error = nil, want an error when no transaction is given")
	}
	if !strings.Contains(err.Error(), "no transaction given") {
		t.Errorf("error = %v, want it to name the missing input", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing on failure", stdout)
	}
}

// TestAssertEmptyArgumentErrors covers the explicit-but-empty argument.
func TestAssertEmptyArgumentErrors(t *testing.T) {
	t.Parallel()

	_, _, err := runAssertForTest(t, "", "   ")
	if err == nil {
		t.Fatal("Execute() error = nil, want an error for an empty argument")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %v, want it to say the argument is empty", err)
	}
}

// TestAssertRejectsUndecodablePayload checks a non-XDR argument fails with
// lens's own explanation of why.
func TestAssertRejectsUndecodablePayload(t *testing.T) {
	t.Parallel()

	_, _, err := runAssertForTest(t, "", "!!! not base64 !!!", "--all")
	if err == nil {
		t.Fatal("Execute() error = nil, want a decode failure")
	}
	for _, want := range []string{"reading the transaction", "TransactionEnvelope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// TestAssertRejectsNonEnvelopePayload checks a value that is valid XDR but
// not an envelope is rejected as such rather than half-decoded.
func TestAssertRejectsNonEnvelopePayload(t *testing.T) {
	t.Parallel()

	result := buildTxResult(t)
	_, _, err := runAssertForTest(t, "", result, "--all")
	if err == nil {
		t.Fatal("Execute() error = nil, want a type failure for a result payload")
	}
	if !strings.Contains(err.Error(), "reading the transaction") {
		t.Errorf("error = %v, want the decode failure", err)
	}
}

// TestAssertRejectsBadVarName checks the generated identifier is validated
// before anything is printed.
func TestAssertRejectsBadVarName(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"1tx", "my tx", "my-var", "_", ""} {
		stdout, _, err := runAssertForTest(t, "", originalTx(t), "--all", "--var", bad)
		if err == nil {
			t.Errorf("--var %q was accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "Go identifier") {
			t.Errorf("--var %q error = %v, want it to name the identifier rule", bad, err)
		}
		if stdout != "" {
			t.Errorf("--var %q printed before failing:\n%s", bad, stdout)
		}
	}
}

// TestAssertRejectsTooManyArguments checks the cobra argument bound.
func TestAssertRejectsTooManyArguments(t *testing.T) {
	t.Parallel()

	_, _, err := runAssertForTest(t, "", originalTx(t), "extra")
	if err == nil {
		t.Fatal("Execute() error = nil, want an argument-count failure")
	}
	if !strings.Contains(err.Error(), "arg") {
		t.Errorf("error = %v, want an argument-count message", err)
	}
}

// TestAssertReadErrorsSurface checks a broken stdin is reported as an
// error, not as an empty session.
func TestAssertReadErrorsSurface(t *testing.T) {
	t.Parallel()

	t.Run("payload read", func(t *testing.T) {
		cmd := newAssertCmd()
		var out, errBuf bytes.Buffer
		cmd.SetIn(failingReader{})
		cmd.SetOut(&out)
		cmd.SetErr(&errBuf)
		err := cmd.Execute()
		if err == nil {
			t.Fatal("Execute() error = nil, want the read failure")
		}
		if !strings.Contains(err.Error(), "reading the transaction") {
			t.Errorf("error = %v, want the wrapped read failure", err)
		}
	})

	t.Run("answer read", func(t *testing.T) {
		cmd := newAssertCmd()
		var out, errBuf bytes.Buffer
		cmd.SetIn(failingReader{})
		cmd.SetOut(&out)
		cmd.SetErr(&errBuf)
		cmd.SetArgs([]string{originalTx(t)})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("Execute() error = nil, want the read failure")
		}
		if !strings.Contains(err.Error(), "reading your answer") {
			t.Errorf("error = %v, want the wrapped read failure", err)
		}
	})
}

// TestAssertHelp documents the command through its own help text.
func TestAssertHelp(t *testing.T) {
	t.Parallel()

	stdout, _, err := runAssertForTest(t, "", "--help")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"focus assert", "--all", "--var", "offline"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not mention %q:\n%s", want, stdout)
		}
	}
}

// TestBuildCandidatesOffersOnlyCarriedFields checks the suggestion set:
// every field a transaction carries gets exactly one suggestion, in field
// order, and fields it does not carry are never offered.
func TestBuildCandidatesOffersOnlyCarriedFields(t *testing.T) {
	t.Parallel()

	feeBump := buildFeeBumpTx(t, buildTx(t, testTx{fee: 100, seq: 5, ops: 1}))

	tests := []struct {
		name    string
		payload string
		want    []string
	}{
		{
			"minimal transaction",
			buildTx(t, testTx{fee: 100, seq: 5, ops: 1}),
			[]string{
				"source account", "fee", "sequence number",
				"operation count", "operation types", "signature count",
			},
		},
		{
			"every optional field",
			originalTx(t),
			[]string{
				"source account", "fee", "sequence number", "memo",
				"operation count", "operation types", "signature count",
				"time bounds", "ledger bounds",
			},
		},
		{
			"fee-bump wrapper",
			feeBump,
			[]string{
				"source account", "fee", "sequence number",
				"operation count", "operation types", "signature count",
				"fee-bump wrapper",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			summary, env := summaryOf(t, tc.payload)
			candidates := buildCandidates(summary, env, "tx")

			got := make([]string, 0, len(candidates))
			for _, c := range candidates {
				got = append(got, c.title)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("titles = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("titles[%d] = %q, want %q (full list %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// TestGeneratedAssertionsHoldAndFailDescriptively is the proof the issue
// asks for, via internal/faketb: every generated assertion passes against
// the transaction it was generated from, and against a different
// transaction it fails with a message that names the assertion and shows
// the wanted value — a descriptive diff, not a bare false.
func TestGeneratedAssertionsHoldAndFailDescriptively(t *testing.T) {
	t.Parallel()

	original := originalTx(t)
	other := otherTx(t)
	summary, env := summaryOf(t, original)
	candidates := buildCandidates(summary, env, "tx")
	if len(candidates) == 0 {
		t.Fatal("no assertions were generated")
	}

	for _, c := range candidates {
		t.Run(c.title, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !c.check(tb, original) {
				t.Errorf("generated assertion does not hold for its own transaction:\n%s",
					tb.Output())
			}
			if c.check(tb, other) {
				t.Error("generated assertion passes against a different transaction")
				return
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("expected exactly one failure: %v", err)
			}
			for _, want := range []string{"Assert", "want"} {
				if !strings.Contains(msg, want) {
					t.Errorf("failure does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestParseAnswer covers every response the prompt accepts, plus the
// refusal of anything else.
func TestParseAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line       string
		want       answer
		wantAnswer bool
	}{
		{"", answerYes, true},
		{"y", answerYes, true},
		{"Y", answerYes, true},
		{"yes", answerYes, true},
		{"  y  ", answerYes, true},
		{"n", answerNo, true},
		{"No", answerNo, true},
		{"a", answerAll, true},
		{"ALL", answerAll, true},
		{"q", answerQuit, true},
		{"Quit", answerQuit, true},
		{"maybe", answerYes, false},
		{"1", answerYes, false},
	}

	for _, tc := range tests {
		got, ok := parseAnswer(tc.line)
		if ok != tc.wantAnswer {
			t.Errorf("parseAnswer(%q) ok = %v, want %v", tc.line, ok, tc.wantAnswer)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseAnswer(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestIsGoIdentifier guards the --var validation against both accepting
// broken code and rejecting legal names.
func TestIsGoIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		want bool
	}{
		{"tx", true},
		{"Env", true},
		{"_tx2", true},
		{"交易", true},
		{"", false},
		{"_", false},
		{"__", false},
		{"2tx", false},
		{"my tx", false},
		{"my-var", false},
		{"tx;", false},
	}

	for _, tc := range tests {
		if got := isGoIdentifier(tc.s); got != tc.want {
			t.Errorf("isGoIdentifier(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}
