package focus_test

import (
	"encoding/base64"
	"strings"
	"testing"

	rpcproto "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// hostErrorVal is the payload of a diagnostic error event for a contract that
// panicked with its own code, which is the form a trap takes most of the time.
func hostErrorVal(code uint32) xdr.ScVal {
	contractCode := xdr.Uint32(code)
	err := xdr.ScError{Type: xdr.ScErrorTypeSceContract, ContractCode: &contractCode}
	return xdr.ScVal{Type: xdr.ScValTypeScvError, Error: &err}
}

// vmErrorVal is the payload for a host-side fault, whose code is one of the
// protocol's named ScErrorCodes rather than a number a contract chose.
func vmErrorVal(code xdr.ScErrorCode) xdr.ScVal {
	c := code
	err := xdr.ScError{Type: xdr.ScErrorTypeSceWasmVm, Code: &c}
	return xdr.ScVal{Type: xdr.ScValTypeScvError, Error: &err}
}

// errorEvent is the host saying the call died: an "error" diagnostic topic and
// an ScError payload. Nothing else in the stream records a trap's reason.
func errorEvent(data xdr.ScVal) xdr.DiagnosticEvent {
	id := testContractID
	return diagnosticEvent(diagContractEvent(xdr.ContractEventTypeDiagnostic, &id, data,
		diagSymbol("error")), false)
}

// trappedStream is a failed call's diagnostic stream: the frame it opened, a
// line it logged, and the host's error where the return would have been.
func trappedStream() []xdr.DiagnosticEvent {
	return []xdr.DiagnosticEvent{
		fnCall("transfer", false),
		logEvent("transferring 10", false),
		errorEvent(hostErrorVal(4)),
	}
}

// trappedMeta wraps that stream in the version 3 meta a Soroban transaction
// produces, pinned to the ledger it was applied in.
func trappedMeta(t *testing.T) string {
	t.Helper()
	return diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), trappedStream()...)
}

// trappedEvidence pairs the captured trapped result with the meta that holds
// the diagnostics, which is how a fixture-based test supplies both halves.
func trappedEvidence(t *testing.T) focus.TrapEvidence {
	t.Helper()
	return focus.TrapEvidence{Result: fixture(t, "tx_failed.res"), Meta: trappedMeta(t)}
}

// trappedResponse is the RPC getTransaction response for the same trap, with
// the stream flattened into diagnosticEventsXdr as the endpoint returns it.
func trappedResponse(t *testing.T, events ...xdr.DiagnosticEvent) rpcproto.GetTransactionResponse {
	t.Helper()

	encoded := make([]string, 0, len(events))
	for _, e := range events {
		encoded = append(encoded, mustMarshal(t, e))
	}
	return rpcproto.GetTransactionResponse{
		TransactionDetails: rpcproto.TransactionDetails{
			Status:              "FAILED",
			ResultXDR:           fixture(t, "tx_failed.res"),
			DiagnosticEventsXDR: encoded,
		},
	}
}

// TestAssertContractTrappedWithPasses covers both evidence carriers and shows
// that the substring is searched across the whole rendered stream, not only
// the host error.
func TestAssertContractTrappedWithPasses(t *testing.T) {
	t.Parallel()

	evidence := trappedEvidence(t)
	response := trappedResponse(t, trappedStream()...)
	vmResponse := trappedResponse(t,
		fnCall("transfer", false),
		errorEvent(vmErrorVal(xdr.ScErrorCodeScecInvalidAction)))

	tests := []struct {
		name string
		call func(focus.TB) bool
	}{
		{"TrapEvidence", func(tb focus.TB) bool {
			return focus.AssertContractTrappedWith(tb, evidence, "Error(Contract, #4)")
		}},
		{"pointer to TrapEvidence", func(tb focus.TB) bool {
			return focus.AssertContractTrappedWith(tb, &evidence, "Error(Contract, #4)")
		}},
		{"RPC response", func(tb focus.TB) bool {
			return focus.AssertContractTrappedWith(tb, response, "Error(Contract, #4)")
		}},
		{"pointer to RPC response", func(tb focus.TB) bool {
			return focus.AssertContractTrappedWith(tb, &response, "Error(Contract, #4)")
		}},
		{"a line the contract logged", func(tb focus.TB) bool {
			return focus.AssertContractTrappedWith(tb, evidence, "transferring 10")
		}},
		{"a frame the host opened", func(tb focus.TB) bool {
			return focus.AssertContractTrappedWith(tb, response, "fn_call")
		}},
		{"a named host error code", func(tb focus.TB) bool {
			return focus.AssertContractTrappedWith(tb, vmResponse, "Error(WasmVm, InvalidAction)")
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb := faketb.New()
			if !tc.call(tb) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertContractTrappedWithFailureMessages pins what a failing assertion
// says. The failure has to name the wanted substring and list the diagnostics
// that were searched, so the reader can see what the host actually reported.
func TestAssertContractTrappedWithFailureMessages(t *testing.T) {
	t.Parallel()

	evidence := trappedEvidence(t)
	response := trappedResponse(t, trappedStream()...)

	untrapped := buildResult(t, xdr.TransactionResultCodeTxFailed,
		paymentResult(xdr.PaymentResultCodePaymentUnderfunded))

	noEvents := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger))

	sorobanMeta := trappedMeta(t)

	withoutResult := response
	withoutResult.ResultXDR = ""

	withoutEvents := response
	withoutEvents.DiagnosticEventsXDR = nil

	badBase64 := response
	badBase64.DiagnosticEventsXDR = []string{"not base64!"}

	badEvent := response
	badEvent.DiagnosticEventsXDR = []string{base64.StdEncoding.EncodeToString([]byte{0xff})}

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "substring absent",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, evidence, "Error(Contract, #99)")
			},
			contains: []string{
				"no host diagnostic contains \"Error(Contract, #99)\"",
				"host diagnostics searched (3 event(s), matched case-sensitively)",
				"Error(Contract, #4)",
				"transferring 10",
			},
		},
		{
			name: "case near miss is named",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, evidence, "error(contract, #4)")
			},
			contains: []string{
				"no host diagnostic contains \"error(contract, #4)\"",
				"different capitalisation",
				"case-sensitive",
			},
		},
		{
			name: "not trapped",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb,
					focus.TrapEvidence{Result: untrapped, Meta: sorobanMeta},
					"Error(Contract, #4)")
			},
			contains: []string{"no operation trapped", "payment_underfunded"},
		},
		{
			name: "blank substring",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, evidence, "   ")
			},
			contains: []string{"expected diagnostic substring is blank", "would match any stream"},
		},
		{
			name: "evidence without a result",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb,
					focus.TrapEvidence{Meta: sorobanMeta}, "Error(Contract, #4)")
			},
			contains: []string{"TrapEvidence carries no result"},
		},
		{
			name: "evidence without a meta",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb,
					focus.TrapEvidence{Result: fixture(t, "tx_failed.res")}, "Error(Contract, #4)")
			},
			contains: []string{"TrapEvidence carries no meta", "host diagnostics live in transaction meta"},
		},
		{
			name: "nil evidence pointer",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, (*focus.TrapEvidence)(nil), "Error(Contract, #4)")
			},
			contains: []string{"nil *TrapEvidence"},
		},
		{
			name: "nil response pointer",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb,
					(*rpcproto.GetTransactionResponse)(nil), "Error(Contract, #4)")
			},
			contains: []string{"nil *protocol.GetTransactionResponse"},
		},
		{
			name: "bare result explains where diagnostics live",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, fixture(t, "tx_failed.res"), "Error(Contract, #4)")
			},
			contains: []string{
				"carries no host diagnostics",
				"TrapEvidence{Result: …, Meta: …}",
			},
		},
		{
			name: "unreadable result value",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, 42, "Error(Contract, #4)")
			},
			contains: []string{"result value could not be read", "unsupported type int"},
		},
		{
			name: "response without a result",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, withoutResult, "Error(Contract, #4)")
			},
			contains: []string{"carries no result", "FAILED"},
		},
		{
			name: "response without diagnostics",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, withoutEvents, "Error(Contract, #4)")
			},
			contains: []string{"carries no diagnostic events", "diagnosticEventsXdr"},
		},
		{
			name: "meta with no events",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb,
					focus.TrapEvidence{Result: fixture(t, "tx_failed.res"), Meta: noEvents},
					"Error(Contract, #4)")
			},
			contains: []string{"diagnostics record no events", "Error(Contract, #4)"},
		},
		{
			name: "meta without a Soroban section",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb,
					focus.TrapEvidence{Result: fixture(t, "tx_failed.res"), Meta: metaV2(t)},
					"Error(Contract, #4)")
			},
			contains: []string{"version 2", "records no diagnostic events"},
		},
		{
			name: "diagnostic event that is not base64",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, badBase64, "Error(Contract, #4)")
			},
			contains: []string{"diagnostic event 0 is not valid base64"},
		},
		{
			name: "diagnostic event that does not decode",
			call: func(tb focus.TB) bool {
				return focus.AssertContractTrappedWith(tb, badEvent, "Error(Contract, #4)")
			},
			contains: []string{"diagnostic event 0 is not a DiagnosticEvent"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
				t.Fatal("assertion passed, want a failure")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("reading the failure message: %v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("failure message does not contain %q:\n%s", want, msg)
				}
			}
			if tb.Helpers == 0 {
				t.Error("assertion did not mark itself as a helper, so file:line points into the library")
			}
		})
	}
}

// TestContractTrappedWithRequiresFatalf is the documented difference between
// the two packages: require stops the test, and its message is unchanged.
func TestContractTrappedWithRequiresFatalf(t *testing.T) {
	t.Parallel()

	evidence := trappedEvidence(t)

	tb := faketb.New()
	require.ContractTrappedWith(tb, evidence, "Error(Contract, #99)")
	if len(tb.Fatals) != 1 || len(tb.Errors) != 0 {
		t.Fatalf("require reported through Errorf; want one Fatalf (errors=%d fatals=%d)",
			len(tb.Errors), len(tb.Fatals))
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading the failure message: %v", err)
	}
	if !strings.Contains(msg, "Error(Contract, #4)") {
		t.Errorf("require lost the diagnostic listing:\n%s", msg)
	}
}

// TestDescribeScErrorRendering pins the host-error form the substring is
// matched against: the type name, then either the contract's own code or the
// host's named code.
func TestDescribeScErrorRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event xdr.DiagnosticEvent
		want  string
	}{
		{
			name:  "contract code",
			event: errorEvent(hostErrorVal(4)),
			want:  "Error(Contract, #4)",
		},
		{
			name:  "host code",
			event: errorEvent(vmErrorVal(xdr.ScErrorCodeScecInvalidAction)),
			want:  "Error(WasmVm, InvalidAction)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), tc.event)
			if !focus.AssertContractTrappedWith(tb,
				focus.TrapEvidence{Result: fixture(t, "tx_failed.res"), Meta: meta}, tc.want) {
				t.Errorf("assertion did not find %q:\n%s", tc.want, tb.Output())
			}
		})
	}
}

func TestAssertContractPanicReason(t *testing.T) {
	t.Parallel()

	meta101 := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("transfer", false),
		errorEvent(hostErrorVal(101)))

	meta4 := trappedMeta(t)
	evidence := trappedEvidence(t)
	vmResponse := trappedResponse(t,
		fnCall("transfer", false),
		errorEvent(vmErrorVal(xdr.ScErrorCodeScecInvalidAction)))

	t.Run("succeeds with uint32 contract error code from meta", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if !focus.AssertContractPanicReason(tb, meta101, 101) {
			t.Errorf("assertion failed for error code 101: %s", tb.Output())
		}
		require.ContractPanicReason(t, meta101, 101)
	})

	t.Run("succeeds with TrapEvidence and RPC response", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if !focus.AssertContractPanicReason(tb, evidence, uint32(4)) {
			t.Errorf("assertion failed for evidence: %s", tb.Output())
		}
		tb2 := faketb.New()
		if !focus.AssertContractPanicReason(tb2, vmResponse, "InvalidAction") {
			t.Errorf("assertion failed for VM response: %s", tb2.Output())
		}
	})

	t.Run("succeeds with xdr.ScError", func(t *testing.T) {
		t.Parallel()
		code := xdr.Uint32(101)
		scErr := xdr.ScError{Type: xdr.ScErrorTypeSceContract, ContractCode: &code}
		tb := faketb.New()
		if !focus.AssertContractPanicReason(tb, meta101, scErr) {
			t.Errorf("assertion failed for scErr: %s", tb.Output())
		}
	})

	t.Run("fails stating transaction succeeded when no trap occurred", func(t *testing.T) {
		t.Parallel()
		successRes := buildResult(t, xdr.TransactionResultCodeTxSuccess)
		tb := faketb.New()
		if focus.AssertContractPanicReason(tb, focus.TrapEvidence{Result: successRes, Meta: meta101}, 101) {
			t.Fatal("assertion unexpectedly passed on successful transaction")
		}
		if !strings.Contains(tb.Output(), "transaction succeeded; no contract trap occurred") {
			t.Errorf("unexpected error message: %s", tb.Output())
		}
	})

	t.Run("fails with informative diagnostic dump when different error code was emitted", func(t *testing.T) {
		t.Parallel()
		tb := faketb.New()
		if focus.AssertContractPanicReason(tb, meta4, 101) {
			t.Fatal("assertion unexpectedly passed for different error code")
		}
		msg := tb.Output()
		for _, want := range []string{
			"AssertContractPanicReason",
			"contract trapped with different reason",
			"want Error(Contract, #101)",
			"got Error(Contract, #4)",
			"host diagnostics searched",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("failure message missing %q:\n%s", want, msg)
			}
		}
	})
}
