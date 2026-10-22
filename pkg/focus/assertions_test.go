package focus_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

func TestFieldAssertionsPass(t *testing.T) {
	t.Parallel()

	env := buildEnvelopeSeq(t, 4242, paymentOp(t, 100_0000000), creditPaymentOp(t, 5))

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

	env := buildEnvelopeSeq(t, 7, paymentOp(t, 100), creditPaymentOp(t, 5))

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

// TestUnorderedSignatureComparison covers the multi-sig case: the same
// envelope signed in a different order is the same envelope, and a comparison
// that says so must not hide a signature that is actually missing.
func TestUnorderedSignatureComparison(t *testing.T) {
	t.Parallel()

	sigA := []byte{1, 2, 3, 4}
	sigB := []byte{5, 6, 7, 8}

	want := envelopeWithSignatures(t, sigA, sigB)
	got := envelopeWithSignatures(t, sigB, sigA)

	t.Run("order alone is a difference", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertEnvelopeEqual(tb, want, got) {
			t.Fatal("envelopes with reordered signatures compared equal")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "Signatures") {
			t.Errorf("the diff does not name the signatures:\n%s", msg)
		}
	})

	t.Run("unordered wrapper passes", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertEnvelopeEqualUnordered(tb, want, got) {
			t.Errorf("reordered signatures were reported as a difference:\n%s", tb.Output())
		}
	})

	t.Run("SortPath does the same", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertEnvelopeEqual(tb, want, got, focus.SortPath("**.Signatures")) {
			t.Errorf("SortPath did not treat the signatures as a set:\n%s", tb.Output())
		}
	})

	t.Run("a missing signature is still a difference", func(t *testing.T) {
		t.Parallel()

		other := envelopeWithSignatures(t, sigA, []byte{9, 9, 9, 9})
		tb := faketb.New()
		if focus.AssertEnvelopeEqualUnordered(tb, want, other) {
			t.Fatal("a missing signature was reported as equal")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		// The failure says how the array was compared, so a reader knows the
		// order was not the problem.
		if !strings.Contains(msg, "compared as unordered sets") {
			t.Errorf("the failure does not say the array was unordered:\n%s", msg)
		}
		if !strings.Contains(msg, "Signatures") {
			t.Errorf("the failure does not name the signatures:\n%s", msg)
		}
	})

	t.Run("a sort pattern that matches nothing is reported", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertEnvelopeEqual(tb, want, want, focus.SortPath("**.SignaturesTypo")) {
			t.Fatal("a sort pattern that matched nothing was accepted silently")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "sort patterns matched no array") {
			t.Errorf("the stale pattern was not reported:\n%s", msg)
		}
		if !strings.Contains(msg, "**.SignaturesTypo") {
			t.Errorf("the stale pattern is not named:\n%s", msg)
		}
	})

	t.Run("the caller's value is not reordered", func(t *testing.T) {
		t.Parallel()

		decoded, err := lens.Decode(want)
		if err != nil {
			t.Fatalf("decoding: %v", err)
		}
		before := fmt.Sprintf("%v", decoded.Node.Child("V1").Child("Signatures").Children[0].Child("Signature").Value)

		tb := faketb.New()
		if !focus.AssertEnvelopeEqualUnordered(tb, decoded, decoded) {
			t.Errorf("a value compared with itself failed:\n%s", tb.Output())
		}

		after := fmt.Sprintf("%v", decoded.Node.Child("V1").Child("Signatures").Children[0].Child("Signature").Value)
		if before != after {
			t.Errorf("the assertion reordered the caller's value: %v became %v", before, after)
		}
	})
}

// TestSorobanSpecAwareAssertions checks the behaviour a contract
// specification buys: a payload can be named the way the contract's author
// named it, and a failure says which field differs.
func TestSorobanSpecAwareAssertions(t *testing.T) {
	t.Parallel()

	spec := transferSpec()
	meta := buildSorobanMeta(t, transferEvent(t, testSource, 100))

	t.Run("matches a payload by field name", func(t *testing.T) {
		tb := faketb.New()
		want := focus.Event{
			Topics: []any{"transfer"},
			Data:   map[string]any{"from": testSource, "amount": 100},
			Spec:   spec,
		}
		if !focus.AssertEventEmitted(tb, meta, want) {
			t.Errorf("spec-aware match failed:\n%s", tb.Output())
		}
	})

	t.Run("names the field that differs", func(t *testing.T) {
		tb := faketb.New()
		want := focus.Event{
			Topics: []any{"transfer"},
			Data:   map[string]any{"from": testSource, "amount": 200},
			Spec:   spec,
		}
		if focus.AssertEventEmitted(tb, meta, want) {
			t.Fatal("matched a payload with the wrong amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"amount",               // the field name the contract chose
			"from",                 // and the other one
			"u128(100)",            // what was there
			"u128(200)",            // what was wanted
			"ScValTypeScvMap",      // the XDR discriminant, for transparency
			"differences from the", // and the diff itself
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a missing field is named", func(t *testing.T) {
		tb := faketb.New()
		want := focus.Event{
			Topics: []any{"transfer"},
			Data:   map[string]any{"from": testSource, "amount": 100, "memo": "hi"},
			Spec:   spec,
		}
		if focus.AssertEventEmitted(tb, meta, want) {
			t.Fatal("matched a payload with an unexpected field")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "memo") {
			t.Errorf("message does not name the unexpected field:\n%s", msg)
		}
	})

	t.Run("without a specification the payload is compared as it is", func(t *testing.T) {
		tb := faketb.New()
		want := focus.Event{
			Topics: []any{"transfer"},
			Data:   map[string]any{"from": testSource, "amount": 100},
		}
		if focus.AssertEventEmitted(tb, meta, want) {
			t.Fatal("matched a payload that cannot be read without a specification")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "needs the contract's specification") {
			t.Errorf("message does not explain what is missing:\n%s", msg)
		}
	})

	t.Run("a specification does not change what matches", func(t *testing.T) {
		tb := faketb.New()
		want := focus.Event{Topics: []any{"transfer"}}
		if !focus.AssertEventEmitted(tb, meta, want) {
			t.Errorf("wildcard match failed:\n%s", tb.Output())
		}
	})
}

// TestSorobanSpecNamesTopics checks that a topic the contract declares is
// named in the failure listing, so a reader can tell which topic is wrong.
func TestSorobanSpecNamesTopics(t *testing.T) {
	t.Parallel()

	address := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}
	spec := []xdr.ScSpecEntry{
		{
			Kind: xdr.ScSpecEntryKindScSpecEntryEventV0,
			EventV0: &xdr.ScSpecEventV0{
				Name: "approve",
				Params: []xdr.ScSpecEventParamV0{
					{
						Name:     "spender",
						Type:     address,
						Location: xdr.ScSpecEventParamLocationV0ScSpecEventParamLocationTopicList,
					},
				},
			},
		},
	}

	name := xdr.ScSymbol("approve")
	id, err := xdr.AddressToAccountId(testDest)
	if err != nil {
		t.Fatalf("parsing address: %v", err)
	}
	addressValue := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &id}

	event := xdr.ContractEvent{
		Type: xdr.ContractEventTypeContract,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: []xdr.ScVal{
					{Type: xdr.ScValTypeScvSymbol, Sym: &name},
					{Type: xdr.ScValTypeScvAddress, Address: &addressValue},
				},
				Data: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
	}
	meta := buildSorobanMeta(t, event)

	tb := faketb.New()
	want := focus.Event{
		Topics: []any{"approve", testSource},
		Spec:   spec,
	}
	if focus.AssertEventEmitted(tb, meta, want) {
		t.Fatal("matched an event with the wrong topic")
	}
	msg, _ := tb.Message()
	if !strings.Contains(msg, "spender=") {
		t.Errorf("message does not name the topic:\n%s", msg)
	}
}

// TestAssertReturnValueWithSpec checks that a return value can be matched
// and described by the names the contract's author gave it.
func TestAssertReturnValueWithSpec(t *testing.T) {
	t.Parallel()

	spec := []xdr.ScSpecEntry{
		{
			Kind: xdr.ScSpecEntryKindScSpecEntryUdtStructV0,
			UdtStructV0: &xdr.ScSpecUdtStructV0{
				Name: "Receipt",
				Fields: []xdr.ScSpecUdtStructFieldV0{
					{Name: "amount", Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU128}},
				},
			},
		},
		{
			Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
			FunctionV0: &xdr.ScSpecFunctionV0{
				Name: "withdraw",
				Outputs: []xdr.ScSpecTypeDef{
					{Type: xdr.ScSpecTypeScSpecTypeUdt, Udt: &xdr.ScSpecTypeUdt{Name: "Receipt"}},
				},
			},
		},
	}

	amount := xdr.UInt128Parts{Hi: 0, Lo: 42}
	key := xdr.ScSymbol("amount")
	entries := xdr.ScMap{
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &key}, Val: xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &amount}},
	}
	mapPtr := &entries
	meta := mustMarshal(t, xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr},
			},
		},
	})

	t.Run("matches by field name", func(t *testing.T) {
		tb := faketb.New()
		want := map[string]any{"amount": 42}
		if !focus.AssertReturnValueWithSpec(tb, meta, want, "withdraw", spec) {
			t.Errorf("spec-aware return value did not match:\n%s", tb.Output())
		}
	})

	t.Run("names the field that differs", func(t *testing.T) {
		tb := faketb.New()
		want := map[string]any{"amount": 43}
		if focus.AssertReturnValueWithSpec(tb, meta, want, "withdraw", spec) {
			t.Fatal("matched a return value with the wrong amount")
		}
		msg, _ := tb.Message()
		for _, want := range []string{"amount", "u128(42)", "u128(43)"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an unknown function is reported", func(t *testing.T) {
		tb := faketb.New()
		if focus.AssertReturnValueWithSpec(tb, meta, map[string]any{"amount": 42}, "nope", spec) {
			t.Fatal("matched a function the contract does not declare")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "nope") {
			t.Errorf("message does not name the function:\n%s", msg)
		}
	})
}

// TestContractSpec checks that a contract's specification can be read out
// of a compiled contract, which is how a test gets one without a second
// file.
func TestContractSpec(t *testing.T) {
	t.Parallel()

	wasm := specWasm(t, transferSpec())

	spec, err := focus.ContractSpec(wasm)
	if err != nil {
		t.Fatalf("reading the contract specification: %v", err)
	}
	if len(spec) != len(transferSpec()) {
		t.Fatalf("read %d entries, want %d", len(spec), len(transferSpec()))
	}

	event, ok := spec[1].GetEventV0()
	if !ok {
		t.Fatal("the second entry is not an event")
	}
	if string(event.Name) != "transfer" {
		t.Errorf("the event is named %q, want %q", event.Name, "transfer")
	}

	t.Run("a module without a specification has none", func(t *testing.T) {
		spec, err := focus.ContractSpec([]byte("\x00asm\x01\x00\x00\x00"))
		if err != nil {
			t.Fatalf("reading the contract specification: %v", err)
		}
		if len(spec) != 0 {
			t.Errorf("read %d entries, want none", len(spec))
		}
	})

	t.Run("rubbish is reported, not decoded", func(t *testing.T) {
		if _, err := focus.ContractSpec([]byte("not a contract")); err == nil {
			t.Fatal("accepted a file that is not a WASM module")
		}
	})
}

// specWasm builds a WASM module carrying a contract specification in a
// custom section, the way a compiled contract does.
func specWasm(tb testing.TB, entries []xdr.ScSpecEntry) []byte {
	tb.Helper()

	var payload bytes.Buffer
	if err := binary.Write(&payload, binary.BigEndian, uint32(len(entries))); err != nil {
		tb.Fatalf("writing the specification count: %v", err)
	}
	for _, entry := range entries {
		if _, err := xdr.Marshal(&payload, entry); err != nil {
			tb.Fatalf("writing a specification entry: %v", err)
		}
	}

	var content bytes.Buffer
	name := "contractspecv0"
	writeLEB128(&content, uint64(len(name)))
	content.WriteString(name)
	content.Write(payload.Bytes())

	var module bytes.Buffer
	module.WriteString("\x00asm")
	module.Write([]byte{1, 0, 0, 0})
	module.WriteByte(0) // a custom section
	writeLEB128(&module, uint64(content.Len()))
	module.Write(content.Bytes())
	return module.Bytes()
}

// writeLEB128 writes an unsigned integer the way the WebAssembly binary
// format encodes lengths.
func writeLEB128(w *bytes.Buffer, value uint64) {
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			b |= 0x80
		}
		w.WriteByte(b)
		if value == 0 {
			return
		}
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
		focus.AssertEnvelopeEqualUnordered(tb, v, v)
		focus.AssertEnvelopeEqual(tb, v, v, focus.SortPath("**.Signatures"))
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
		focus.AssertFeeBumpInner(tb, v, func(focus.Value) bool { return true })
		focus.InnerEnvelope(tb, v)
		focus.AssertSucceeded(tb, v)
		focus.AssertFailed(tb, v)
		focus.AssertResultCode(tb, v, "tx_failed")
		focus.AssertOperationSucceeded(tb, v, 0)
		focus.AssertOperationFailed(tb, v, 0, "")
		focus.AssertOperationResultCode(tb, v, 0, "tx_failed")
		focus.AssertFailedOperations(tb, v, 0)
		focus.AssertConsumedFeeWithin(tb, v, 1)
		focus.AssertRefundFeeEqual(tb, v, 1)
		focus.AssertSimulationResourcesWithin(tb, v, 1, 1)
		focus.AssertEventEmitted(tb, v, focus.Event{})
		focus.AssertEventEmitted(tb, v, focus.Event{Spec: []xdr.ScSpecEntry{{}}})
		focus.AssertEventCount(tb, v, 0)
		focus.AssertNoEvents(tb, v)
		focus.AssertReturnValue(tb, v, "x")
		focus.AssertReturnValueWithSpec(tb, v, "x", "fn", nil)
		focus.AssertReturnValueWithSpec(tb, v, "x", "fn", []xdr.ScSpecEntry{{}})
		focus.AssertContractTrapped(tb, v)
		focus.AssertContractTrappedWith(tb, v, "Error(Contract, #4)")
		focus.AssertEntryCreated(tb, v, v)
		focus.AssertEntryModified(tb, v, v)
		focus.AssertEntryDeleted(tb, v, v)
		focus.AssertBalanceChanged(tb, v, v, v, 0)
		focus.AssertTrustlineModified(tb, v, v, v, 0)
		focus.AssertClawbackOp(tb, v, 0, v, v, 1)
		focus.AssertClawbackClaimableBalanceOp(tb, v, 0, v)
		focus.AssertBalanceForfeited(tb, v, v, v, 1)
		focus.AssertClaimableBalanceRevoked(tb, v, v)
		focus.AssertEntrySponsoredBy(tb, v, v, v)
		focus.AssertInflationPayouts(tb, v, []focus.InflationPayout{{Destination: v, Amount: 1}})
		focus.AssertInflationPaid(tb, v, v, 1)
		focus.AssertFeePool(tb, v, 1)
		focus.AssertFeePoolChanged(tb, v, v, 1)
		focus.AssertClaimableBalanceCreated(tb, v, v, 0)
		focus.AssertAccountDataEqual(tb, v, "k", v)
		focus.AssertAccountDataEqualFor(tb, v, v, "k", v)
		focus.AssertAccountDataDeleted(tb, v, "k")
		focus.AssertLiquidityPoolDeposit(tb, v, v, focus.PoolChange{ReserveA: 1, ReserveB: 1, Shares: 1})
		focus.AssertLiquidityPoolWithdrawal(tb, v, v, focus.PoolChange{ReserveA: 1, ReserveB: 1, Shares: 1})
		focus.AssertOfferCreated(tb, v, focus.Offer{Seller: v, Selling: v, Buying: v, Price: v})
		focus.AssertTradeExecuted(tb, v, focus.Trade{Seller: v, Pool: v, Sold: v, Bought: v})
		focus.AssertSignersEqual(tb, v, v, nil)
		focus.AssertThresholdsEqual(tb, v, v, focus.Thresholds{})
		focus.AssertTimeboundsValid(tb, v, 0, 0)
		focus.AssertLedgerboundsValid(tb, v, 0, 0)
		focus.AssertTimeBounds(tb, v, 0, 0)
		focus.AssertTimeBoundsWindow(tb, v, time.Minute)
		focus.AssertNoTimeBounds(tb, v)
		focus.AssertLedgerBounds(tb, v, 0, 0)
		focus.AssertMemoTextEqual(tb, v, "")
		focus.AssertMemoIDEqual(tb, v, 0)
		focus.AssertMemoHashEqual(tb, v, "")
		focus.AssertMemoReturnEqual(tb, v, "")
		focus.AssertMemoText(tb, v, "")
		focus.AssertMemoID(tb, v, 0)
		focus.AssertMemoHash(tb, v, [32]byte{})
		focus.AssertNoMemo(tb, v)
		focus.AssertOperationSequence(tb, v, nil)
		focus.AssertOperationSourceAccount(tb, v, 0, "G")
		focus.AssertSignaturesPresent(tb, v, "G")
		focus.AssertSignatureThreshold(tb, v, 1, "G")
		focus.AssertSignatureHints(tb, v, [4]byte{})
		focus.AssertSignedBy(tb, v, "G")
		focus.AssertContractError(tb, v, xdr.ScErrorTypeSceContract, 4)
		focus.AssertDiagnosticEventOrder(tb, v, []focus.DiagnosticEventMatch{{Kind: focus.DiagnosticFnCall}})
		focus.AssertDiagnosticEventTimeline(tb, v, focus.DiagnosticTimeline{
			Ledger:   3966820,
			ClosedAt: time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC),
		})
		focus.AssertConstructorState(tb, v, focus.ConstructorState{
			ContractID: "CAAAA",
			Owner:      testSource,
			Entries:    map[string]any{"owner": v},
		})
		focus.AssertContractOwner(tb, v, "CAAAA", testSource)
		focus.AssertContractAuthValid(tb, v, testSource, focus.ContractCall{Args: []any{v}})
		focus.AssertContractAuthValid(tb, v, testSource, focus.ContractCall{})
		focus.AssertContractAuthValid(tb, v, "not-a-strkey", focus.ContractCall{})
		focus.Events(tb, v)
		focus.DiagnosticEvents(tb, v)
		focus.Summarize(tb, v)
	}
}

func u32ptr(v uint32) *xdr.Uint32 {
	u := xdr.Uint32(v)
	return &u
}
