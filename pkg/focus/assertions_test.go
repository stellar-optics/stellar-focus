package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func TestFieldAssertionsPass(t *testing.T) {
	t.Parallel()

	env := buildEnvelopeSeq(t, 4242, paymentOp(t, 100_0000000), creditPaymentOp(t, "USDC", 5))

	tests := []struct {
		name string
		fn   func(focus.TB) bool
	}{
		{"source account", func(tb focus.TB) bool { return focus.AssertSourceAccount(tb, env, testSource) }},
		{"fee", func(tb focus.TB) bool { return focus.AssertFee(tb, env, 200) }},
		{"sequence", func(tb focus.TB) bool { return focus.AssertSequence(tb, env, 4242) }},
		{"memo", func(tb focus.TB) bool { return focus.AssertMemo(tb, env, "") }},
		{"operation count", func(tb focus.TB) bool { return focus.AssertOperationCount(tb, env, 2) }},
		{"operation types", func(tb focus.TB) bool { return focus.AssertOperationTypes(tb, env, "payment", "payment") }},
		{"operation type by index", func(tb focus.TB) bool { return focus.AssertOperationType(tb, env, 1, "payment") }},
		{"signature count", func(tb focus.TB) bool { return focus.AssertSignatureCount(tb, env, 0) }},
		{"not fee bumped", func(tb focus.TB) bool { return focus.AssertFeeBump(tb, env, false) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb := faketb.New()
			if !tc.fn(tb) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

// TestFieldAssertionMessages checks that a mismatch says what was found as
// well as what was wanted — a message giving only the expectation makes the
// reader go and look the actual value up.
func TestFieldAssertionMessages(t *testing.T) {
	t.Parallel()

	env := buildEnvelopeSeq(t, 7, paymentOp(t, 100), creditPaymentOp(t, "USDC", 5))

	tests := []struct {
		name     string
		fn       func(focus.TB) bool
		contains []string
	}{
		{
			name:     "fee shows both units",
			fn:       func(tb focus.TB) bool { return focus.AssertFee(tb, env, 999) },
			contains: []string{"AssertFee", "fee is 200", "XLM", "want 999"},
		},
		{
			name:     "sequence",
			fn:       func(tb focus.TB) bool { return focus.AssertSequence(tb, env, 8) },
			contains: []string{"AssertSequence", "sequence number is 7", "want 8"},
		},
		{
			name:     "operation count lists what is there",
			fn:       func(tb focus.TB) bool { return focus.AssertOperationCount(tb, env, 5) },
			contains: []string{"has 2 operation(s), want 5", "[0] payment", "[1] payment"},
		},
		{
			name:     "operation types lists both sides",
			fn:       func(tb focus.TB) bool { return focus.AssertOperationTypes(tb, env, "change_trust") },
			contains: []string{"operations are [payment, payment]", "want [change_trust]"},
		},
		{
			name:     "memo hints at the expected form",
			fn:       func(tb focus.TB) bool { return focus.AssertMemo(tb, env, "hello") },
			contains: []string{"AssertMemo", `want "hello"`, "text:"},
		},
		{
			name:     "index out of range",
			fn:       func(tb focus.TB) bool { return focus.AssertOperationType(tb, env, 9, "payment") },
			contains: []string{"no operation at index 9", "has 2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.fn(tb) {
				t.Fatal("assertion returned true when it should have failed")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

func TestResultAssertions(t *testing.T) {
	t.Parallel()

	success := buildResult(t, xdr.TransactionResultCodeTxSuccess,
		paymentResult(xdr.PaymentResultCodePaymentSuccess))
	failed := buildResult(t, xdr.TransactionResultCodeTxFailed,
		paymentResult(xdr.PaymentResultCodePaymentSuccess),
		paymentResult(xdr.PaymentResultCodePaymentUnderfunded))

	t.Run("succeeded", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertSucceeded(tb, success) {
			t.Errorf("AssertSucceeded failed:\n%s", tb.Output())
		}
	})

	t.Run("failed", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertFailed(tb, failed) {
			t.Errorf("AssertFailed failed:\n%s", tb.Output())
		}
	})

	t.Run("result code", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertResultCode(tb, failed, "tx_failed") {
			t.Errorf("AssertResultCode failed:\n%s", tb.Output())
		}
	})

	t.Run("operation succeeded", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertOperationSucceeded(tb, failed, 0) {
			t.Errorf("AssertOperationSucceeded failed:\n%s", tb.Output())
		}
	})

	t.Run("operation failed with code", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertOperationFailed(tb, failed, 1, "payment_underfunded") {
			t.Errorf("AssertOperationFailed failed:\n%s", tb.Output())
		}
	})

	t.Run("operation failed without a code", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertOperationFailed(tb, failed, 1, "") {
			t.Errorf("AssertOperationFailed failed:\n%s", tb.Output())
		}
	})

	t.Run("failed operation indexes", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertFailedOperations(tb, failed, 1) {
			t.Errorf("AssertFailedOperations failed:\n%s", tb.Output())
		}
	})
}

// TestResultFailureMessagesExplainInPlainEnglish is the assertion that this
// library's result errors are worth reading: the code alone is not an
// explanation, so lens's plain-English summary and hint must come through.
func TestResultFailureMessagesExplainInPlainEnglish(t *testing.T) {
	t.Parallel()

	failed := buildResult(t, xdr.TransactionResultCodeTxFailed,
		paymentResult(xdr.PaymentResultCodePaymentSuccess),
		paymentResult(xdr.PaymentResultCodePaymentNoTrust))

	t.Run("wrong operation code", func(t *testing.T) {
		tb := faketb.New()
		if focus.AssertOperationFailed(tb, failed, 1, "payment_underfunded") {
			t.Fatal("assertion passed with the wrong code")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"operation 1",
			"payment_no_trust",
			"want \"payment_underfunded\"",
			"trustline", // lens's plain-English summary
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("expected success", func(t *testing.T) {
		tb := faketb.New()
		if focus.AssertOperationSucceeded(tb, failed, 1) {
			t.Fatal("assertion passed for a failed operation")
		}
		msg, _ := tb.Message()
		// The whole outcome is shown, not only the operation asserted on.
		for _, want := range []string{"payment_no_trust", "[0]", "[1]", "FAILED"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("wrong transaction code", func(t *testing.T) {
		tb := faketb.New()
		if focus.AssertResultCode(tb, failed, "tx_bad_seq") {
			t.Fatal("assertion passed with the wrong code")
		}
		msg, _ := tb.Message()
		for _, want := range []string{"tx_failed", "tx_bad_seq", "at least one of its operations failed"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})
}

// TestSorobanAssertionsOnNonSorobanMeta checks the message a developer gets
// when they point a Soroban assertion at a classic transaction, which is an
// easy and confusing mistake to make.
func TestSorobanAssertionsOnNonSorobanMeta(t *testing.T) {
	t.Parallel()

	classic := mustMarshal(t, xdr.TransactionMeta{V: 0, Operations: &[]xdr.OperationMeta{}})

	tb := faketb.New()
	if focus.AssertEventCount(tb, classic, 1) {
		t.Fatal("assertion passed on a meta with no Soroban section")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"no Soroban section", "contract invocation"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not explain the mistake (%q):\n%s", want, msg)
		}
	}
}

func TestSorobanEventAssertions(t *testing.T) {
	t.Parallel()

	meta := buildSorobanMeta(t,
		contractEvent(t, "transfer", 100),
		contractEvent(t, "mint", 50),
	)

	t.Run("matches by topic symbol", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertEventEmitted(tb, meta, focus.Event{Topics: []any{"transfer"}}) {
			t.Errorf("event not matched:\n%s", tb.Output())
		}
	})

	t.Run("matches any event", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertEventEmitted(tb, meta, focus.Event{}) {
			t.Errorf("wildcard match failed:\n%s", tb.Output())
		}
	})

	t.Run("counts events", func(t *testing.T) {
		tb := faketb.New()
		if !focus.AssertEventCount(tb, meta, 2) {
			t.Errorf("count failed:\n%s", tb.Output())
		}
	})

	t.Run("no match lists what was there", func(t *testing.T) {
		tb := faketb.New()
		if focus.AssertEventEmitted(tb, meta, focus.Event{Topics: []any{"burn"}}) {
			t.Fatal("matched an event that is not present")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{`"burn"`, "events present (2)", `"transfer"`, `"mint"`} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("return value", func(t *testing.T) {
		tb := faketb.New()
		want := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: u32ptr(7)}
		if !focus.AssertReturnValue(tb, meta, want) {
			t.Errorf("return value did not match:\n%s", tb.Output())
		}
	})

	t.Run("wrong return value", func(t *testing.T) {
		tb := faketb.New()
		if focus.AssertReturnValue(tb, meta, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: u32ptr(9)}) {
			t.Fatal("assertion passed with the wrong return value")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "u32(7)") || !strings.Contains(msg, "u32(9)") {
			t.Errorf("message does not show both values:\n%s", msg)
		}
	})

	t.Run("Events escape hatch", func(t *testing.T) {
		tb := faketb.New()
		got := focus.Events(tb, meta)
		if len(got) != 2 {
			t.Fatalf("Events returned %d events, want 2", len(got))
		}
	})
}

func TestAssertContractTrapped(t *testing.T) {
	t.Parallel()

	trapped := fixture(t, "tx_failed.res")

	tb := faketb.New()
	if !focus.AssertContractTrapped(tb, trapped) {
		t.Errorf("AssertContractTrapped failed on a real trapped transaction:\n%s", tb.Output())
	}

	notTrapped := buildResult(t, xdr.TransactionResultCodeTxFailed,
		paymentResult(xdr.PaymentResultCodePaymentUnderfunded))
	tb2 := faketb.New()
	if focus.AssertContractTrapped(tb2, notTrapped) {
		t.Error("AssertContractTrapped passed for a non-Soroban failure")
	}
}

// TestRequireUsesFatalf checks the documented difference between the two
// packages: focus reports, require stops.
func TestRequireUsesFatalf(t *testing.T) {
	t.Parallel()

	want := buildEnvelopeSeq(t, 1, paymentOp(t, 100))
	got := buildEnvelopeSeq(t, 1, paymentOp(t, 200))

	assertTB := faketb.New()
	focus.AssertEnvelopeEqual(assertTB, want, got)
	if len(assertTB.Errors) != 1 || len(assertTB.Fatals) != 0 {
		t.Errorf("focus reported via Fatalf; want Errorf (errors=%d fatals=%d)",
			len(assertTB.Errors), len(assertTB.Fatals))
	}

	requireTB := faketb.New()
	require.EnvelopeEqual(requireTB, want, got)
	if len(requireTB.Fatals) != 1 || len(requireTB.Errors) != 0 {
		t.Errorf("require reported via Errorf; want Fatalf (errors=%d fatals=%d)",
			len(requireTB.Errors), len(requireTB.Fatals))
	}
	// The message must be identical; only the severity differs.
	if !strings.Contains(requireTB.Output(), "Body.PaymentOp.Amount") {
		t.Errorf("require lost the diff:\n%s", requireTB.Output())
	}
}

// TestAssertionsNeverPanic feeds deliberately hostile values through every
// exported assertion. A panic inside someone's test binary destroys the
// output of every other test in the package, so this is a hard requirement.
func TestAssertionsNeverPanic(t *testing.T) {
	t.Parallel()

	hostile := []focus.Value{
		nil, "", "!!!", "AA==", []byte{0x00}, []byte{}, 42, struct{}{},
		fixture(t, "tx_failed.env"), fixture(t, "tx_failed.res"),
	}

	for _, v := range hostile {
		tb := faketb.New()
		// Each of these must report rather than panic.
		focus.AssertEnvelopeEqual(tb, v, v)
		focus.AssertResultEqual(tb, v, v)
		focus.AssertMetaEqual(tb, v, v)
		focus.AssertEqual(tb, v, v)
		focus.AssertSourceAccount(tb, v, "G")
		focus.AssertFee(tb, v, 1)
		focus.AssertSequence(tb, v, 1)
		focus.AssertMemo(tb, v, "")
		focus.AssertOperationCount(tb, v, 1)
		focus.AssertOperationTypes(tb, v, "payment")
		focus.AssertOperationType(tb, v, 0, "payment")
		focus.AssertSignatureCount(tb, v, 0)
		focus.AssertFeeBump(tb, v, false)
		focus.AssertSucceeded(tb, v)
		focus.AssertFailed(tb, v)
		focus.AssertResultCode(tb, v, "tx_failed")
		focus.AssertOperationSucceeded(tb, v, 0)
		focus.AssertOperationFailed(tb, v, 0, "")
		focus.AssertFailedOperations(tb, v, 0)
		focus.AssertEventEmitted(tb, v, focus.Event{})
		focus.AssertEventCount(tb, v, 0)
		focus.AssertNoEvents(tb, v)
		focus.AssertReturnValue(tb, v, "x")
		focus.AssertContractTrapped(tb, v)
		focus.Events(tb, v)
		focus.Summarize(tb, v)
	}
}

func u32ptr(v uint32) *xdr.Uint32 {
	u := xdr.Uint32(v)
	return &u
}
