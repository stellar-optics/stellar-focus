package focus_test

import (
	"strings"
	"testing"

	rpcproto "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// eventWithTopics builds a contract event carrying the given symbol topics
// and a u32 payload.
//
// The sequence assertions are about order, so the events of a test have to be
// told apart: distinct topics and distinct payloads do that.
func eventWithTopics(tb testing.TB, data uint32, topics ...string) xdr.ContractEvent {
	tb.Helper()

	event := contractEvent(tb, topics[0], data)
	body, ok := event.Body.GetV0()
	if !ok {
		tb.Fatal("the event has no V0 body")
	}
	body.Topics = body.Topics[:0]
	for _, topic := range topics {
		sym := xdr.ScSymbol(topic)
		body.Topics = append(body.Topics, xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym})
	}
	return event
}

// sorobanEnvelope builds an envelope carrying Soroban transaction data with
// the given resource limits and resource fee.
func sorobanEnvelope(
	tb testing.TB, instructions, readBytes, writeBytes uint32, resourceFee int64,
) xdr.TransactionEnvelope {
	tb.Helper()

	env := envelopeValue(tb)
	env.V1.Tx.Ext = xdr.TransactionExt{
		V: 1,
		SorobanData: &xdr.SorobanTransactionData{
			Ext: xdr.SorobanTransactionDataExt{V: 0},
			Resources: xdr.SorobanResources{
				Footprint:     xdr.LedgerFootprint{},
				Instructions:  xdr.Uint32(instructions),
				DiskReadBytes: xdr.Uint32(readBytes),
				WriteBytes:    xdr.Uint32(writeBytes),
			},
			ResourceFee: xdr.Int64(resourceFee),
		},
	}
	return env
}

func TestAssertEventsEqualPasses(t *testing.T) {
	t.Parallel()

	meta := buildSorobanMeta(t,
		eventWithTopics(t, 1, "init"),
		eventWithTopics(t, 2, "deposit"),
		eventWithTopics(t, 3, "audit"),
	)

	tests := []struct {
		name string
		want []focus.Event
		opts []focus.EventOption
	}{
		{
			name: "the whole sequence",
			want: []focus.Event{
				{Topics: []any{"init"}, Data: scU32(1)},
				{Topics: []any{"deposit"}, Data: scU32(2)},
				{Topics: []any{"audit"}, Data: scU32(3)},
			},
		},
		{
			name: "a nil topic matches any topic",
			want: []focus.Event{
				{Topics: []any{"init"}},
				{Topics: []any{nil}, Data: scU32(2)},
				{Topics: []any{"audit"}},
			},
		},
		{
			name: "a matcher with no fields matches anything",
			want: []focus.Event{{}, {}, {}},
		},
		{
			name: "the payload can be ignored",
			want: []focus.Event{
				{Topics: []any{"init"}, Data: scU32(99)},
				{Topics: []any{"deposit"}, Data: scU32(99)},
				{Topics: []any{"audit"}, Data: scU32(99)},
			},
			opts: []focus.EventOption{focus.IgnoreEventData()},
		},
		{
			name: "the topics can be ignored",
			want: []focus.Event{
				{Topics: []any{"init"}, Data: scU32(1)},
				{Topics: []any{"deposit"}, Data: scU32(2)},
				{Topics: []any{"audit"}, Data: scU32(3)},
			},
			opts: []focus.EventOption{focus.IgnoreEventTopics()},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertEventsEqual(tb, meta, tc.want, tc.opts...) {
				t.Errorf("the sequence did not match:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertEventsEqualOnAnEmptySequence checks the boundary case: a
// transaction that emitted nothing matches an empty list, and one that
// emitted something does not.
func TestAssertEventsEqualOnAnEmptySequence(t *testing.T) {
	t.Parallel()

	empty := buildSorobanMeta(t)

	tb := faketb.New()
	if !focus.AssertEventsEqual(tb, empty, nil) {
		t.Errorf("an empty sequence did not match:\n%s", tb.Output())
	}

	tb = faketb.New()
	if focus.AssertEventsEqual(tb, empty, []focus.Event{{Topics: []any{"init"}}}) {
		t.Error("a transaction with no events matched a wanted event")
	}
}

// TestAssertEventsEqualFailures is the point of the assertion: the events are
// all there, but the sequence is not the one that was expected, and the
// failure says which event deviated and shows both sides of it.
func TestAssertEventsEqualFailures(t *testing.T) {
	t.Parallel()

	meta := buildSorobanMeta(t,
		eventWithTopics(t, 1, "init"),
		eventWithTopics(t, 2, "deposit"),
		eventWithTopics(t, 3, "audit"),
	)

	tests := []struct {
		name     string
		want     []focus.Event
		opts     []focus.EventOption
		contains []string
	}{
		{
			name: "too many events wanted",
			want: []focus.Event{
				{Topics: []any{"init"}},
				{Topics: []any{"deposit"}},
			},
			contains: []string{"AssertEventsEqual", "meta has 3 event(s), want 2", `"init"`, `"audit"`},
		},
		{
			name: "too few events wanted",
			want: []focus.Event{
				{Topics: []any{"init"}},
				{Topics: []any{"deposit"}},
				{Topics: []any{"audit"}},
				{Topics: []any{"close"}},
			},
			contains: []string{"meta has 3 event(s), want 4"},
		},
		{
			name: "the events are in the wrong order",
			want: []focus.Event{
				{Topics: []any{"init"}},
				{Topics: []any{"audit"}},
				{Topics: []any{"deposit"}},
			},
			contains: []string{
				"event 1 of 3 does not match",
				"actual:", `"deposit"`,
				"expected:", `"audit"`,
			},
		},
		{
			name: "an event is missing",
			want: []focus.Event{
				{Topics: []any{"init"}},
				{Topics: []any{"transfer"}},
				{Topics: []any{"audit"}},
			},
			contains: []string{"event 1 of 3 does not match", `"deposit"`, `"transfer"`},
		},
		{
			name: "the payload differs",
			want: []focus.Event{
				{Topics: []any{"init"}, Data: scU32(1)},
				{Topics: []any{"deposit"}, Data: scU32(99)},
				{Topics: []any{"audit"}, Data: scU32(3)},
			},
			contains: []string{"event 1 of 3 does not match", "u32(2)", "u32(99)"},
		},
		{
			name: "an ignored field is not what failed",
			want: []focus.Event{
				{Topics: []any{"init"}},
				{Topics: []any{"withdraw"}, Data: scU32(99)},
				{Topics: []any{"audit"}},
			},
			opts:     []focus.EventOption{focus.IgnoreEventData()},
			contains: []string{"data ignored by option"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertEventsEqual(tb, meta, tc.want, tc.opts...) {
				t.Fatal("the assertion passed when the sequence did not match")
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

// TestAssertEventsEqualShowsBothSides checks the shape of the diagnostic: the
// event that was emitted and the event that was wanted are printed beside
// each other, so a reader does not have to hold two descriptions in their
// head to see what differs.
func TestAssertEventsEqualShowsBothSides(t *testing.T) {
	t.Parallel()

	meta := buildSorobanMeta(t,
		eventWithTopics(t, 1, "init"),
		eventWithTopics(t, 2, "deposit", "GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6"),
	)

	tb := faketb.New()
	focus.AssertEventsEqual(tb, meta, []focus.Event{
		{Topics: []any{"init"}},
		{Topics: []any{"withdraw", nil}},
	})
	msg, _ := tb.Message()

	lines := strings.Split(msg, "\n")
	var actual, expected string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "  actual:"):
			actual = line
		case strings.HasPrefix(line, "  expected:"):
			expected = line
		}
	}
	if actual == "" || expected == "" {
		t.Fatalf("the failure does not show both sides:\n%s", msg)
	}
	if !strings.Contains(actual, `"deposit"`) {
		t.Errorf("the actual event is not shown:\n%s", msg)
	}
	if !strings.Contains(expected, `"withdraw"`) {
		t.Errorf("the wanted event is not shown:\n%s", msg)
	}
	// The wildcard topic is named as such rather than left out.
	if !strings.Contains(expected, "*") {
		t.Errorf("the wildcard topic is not named:\n%s", msg)
	}
	// A field the matcher left open is shown as a wildcard, so the two sides
	// line up and the reader can see the payload was not what failed.
	if !strings.Contains(expected, "data *") {
		t.Errorf("the open payload is not shown as a wildcard:\n%s", msg)
	}
}

func TestAssertSorobanResources(t *testing.T) {
	t.Parallel()

	env := sorobanEnvelope(t, 1000, 200, 300, 100_000_000)

	t.Run("within the budget", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertSorobanResources(tb, env, 1000, 200, 300) {
			t.Errorf("the resources were reported as over budget:\n%s", tb.Output())
		}
	})

	t.Run("a zero budget is not checked", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertSorobanResources(tb, env, 0, 0, 0) {
			t.Errorf("an unchecked dimension was reported:\n%s", tb.Output())
		}
	})

	t.Run("the instructions are over budget", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSorobanResources(tb, env, 999, 200, 300) {
			t.Fatal("the assertion passed with the instructions over budget")
		}
		msg, _ := tb.Message()
		for _, want := range []string{"AssertSorobanResources", "CPU instructions are 1000, budget is 999"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("every dimension over budget is named", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		focus.AssertSorobanResources(tb, env, 1, 1, 1)
		msg, _ := tb.Message()
		for _, want := range []string{
			"CPU instructions are 1000, budget is 1",
			"read bytes are 200, budget is 1",
			"write bytes are 300, budget is 1",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("the limits are shown", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		focus.AssertSorobanResources(tb, env, 1, 200, 300)
		msg, _ := tb.Message()
		if !strings.Contains(msg, "1000 CPU instructions") {
			t.Errorf("the message does not show the limits the transaction asked for:\n%s", msg)
		}
	})

	t.Run("a transaction with no Soroban data", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertSorobanResources(tb, buildEnvelope(t), 1, 1, 1) {
			t.Fatal("the assertion passed on a transaction with no Soroban data")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "no Soroban data") {
			t.Errorf("the message does not explain the mistake:\n%s", msg)
		}
	})
}

func TestAssertResourceFee(t *testing.T) {
	t.Parallel()

	env := sorobanEnvelope(t, 1000, 200, 300, 100_000_000)

	t.Run("the fee matches", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertResourceFee(tb, env, 100_000_000) {
			t.Errorf("the resource fee did not match:\n%s", tb.Output())
		}
	})

	t.Run("the fee differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertResourceFee(tb, env, 50_000_000) {
			t.Fatal("the assertion passed with the wrong resource fee")
		}
		msg, _ := tb.Message()
		for _, want := range []string{
			"AssertResourceFee",
			"resource fee is 100000000 (10.0000000 XLM)",
			"want 50000000 (5.0000000 XLM)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a transaction with no Soroban data", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertResourceFee(tb, buildEnvelope(t), 1) {
			t.Fatal("the assertion passed on a transaction with no Soroban data")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "no Soroban data") {
			t.Errorf("the message does not explain the mistake:\n%s", msg)
		}
	})
}

// storageErrorVal is the payload of a host error for a contract that read a
// key that is not there: the storage category with the host's missing-value
// code.
func storageErrorVal() xdr.ScVal {
	code := xdr.ScErrorCodeScecMissingValue
	err := xdr.ScError{Type: xdr.ScErrorTypeSceStorage, Code: &code}
	return xdr.ScVal{Type: xdr.ScValTypeScvError, Error: &err}
}

// errorReturnMeta wraps an error return value in the version 3 meta a
// Soroban transaction produces.
func errorReturnMeta(t *testing.T, value xdr.ScVal) string {
	t.Helper()
	return mustMarshal(t, xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{ReturnValue: value},
		},
	})
}

// errorReturnMetaV4 wraps an error return value in the version 4 meta newer
// protocols produce, where the return value is optional.
func errorReturnMetaV4(t *testing.T, value xdr.ScVal) string {
	t.Helper()
	return mustMarshal(t, xdr.TransactionMeta{
		V: 4,
		V4: &xdr.TransactionMetaV4{
			SorobanMeta: &xdr.SorobanTransactionMetaV2{ReturnValue: &value},
		},
	})
}

// errorResponse is the RPC getTransaction response for a call that died with
// the given error, with the host's account of it flattened into
// diagnosticEventsXdr as the endpoint returns it.
func errorResponse(t *testing.T, value xdr.ScVal) rpcproto.GetTransactionResponse {
	t.Helper()

	return rpcproto.GetTransactionResponse{
		TransactionDetails: rpcproto.TransactionDetails{
			Status:              "FAILED",
			ResultXDR:           fixture(t, "tx_failed.res"),
			DiagnosticEventsXDR: []string{mustMarshal(t, errorEvent(value))},
		},
	}
}

// TestAssertContractErrorPasses covers the carriers an error can arrive in
// and the two places a meta records it: the Soroban return value and the
// host's error diagnostic.
func TestAssertContractErrorPasses(t *testing.T) {
	t.Parallel()

	contractError := errorReturnMeta(t, hostErrorVal(101))
	storageError := errorReturnMeta(t, storageErrorVal())

	trappedMeta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("transfer", false),
		errorEvent(hostErrorVal(4)))

	trappedStorageMeta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("withdraw", false),
		errorEvent(storageErrorVal()))

	response := errorResponse(t, hostErrorVal(4))

	tests := []struct {
		name  string
		value focus.Value
		want  xdr.ScErrorType
		code  uint32
	}{
		{
			name:  "contract error in the return value",
			value: contractError,
			want:  xdr.ScErrorTypeSceContract,
			code:  101,
		},
		{
			name:  "host error in the return value",
			value: storageError,
			want:  xdr.ScErrorTypeSceStorage,
			code:  uint32(xdr.ScErrorCodeScecMissingValue),
		},
		{
			name:  "contract error in the diagnostics",
			value: trappedMeta,
			want:  xdr.ScErrorTypeSceContract,
			code:  4,
		},
		{
			name:  "host error in the diagnostics",
			value: trappedStorageMeta,
			want:  xdr.ScErrorTypeSceStorage,
			code:  uint32(xdr.ScErrorCodeScecMissingValue),
		},
		{
			name:  "contract error in a version 4 meta",
			value: errorReturnMetaV4(t, hostErrorVal(101)),
			want:  xdr.ScErrorTypeSceContract,
			code:  101,
		},
		{
			name: "contract error in a version 4 meta's diagnostics",
			value: diagnosticMetaV4(t, pinChanges(t, testDiagnosticLedger),
				errorEvent(hostErrorVal(4))),
			want: xdr.ScErrorTypeSceContract,
			code: 4,
		},
		{
			name: "Soroban section on its own",
			value: xdr.SorobanTransactionMeta{
				ReturnValue: hostErrorVal(101),
			},
			want: xdr.ScErrorTypeSceContract,
			code: 101,
		},
		{
			name: "trap evidence",
			value: focus.TrapEvidence{
				Result: fixture(t, "tx_failed.res"),
				Meta:   trappedMeta,
			},
			want: xdr.ScErrorTypeSceContract,
			code: 4,
		},
		{
			name:  "RPC response",
			value: response,
			want:  xdr.ScErrorTypeSceContract,
			code:  4,
		},
		{
			name:  "pointer to an RPC response",
			value: &response,
			want:  xdr.ScErrorTypeSceContract,
			code:  4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertContractError(tb, tc.value, tc.want, tc.code) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertContractErrorFailureMessages pins what a failing assertion says.
//
// The message has to name both the error that was found and the one that was
// wanted, in the plain form the issue asks for, because that is the whole
// point of the assertion: a test that fails says which error it got.
func TestAssertContractErrorFailureMessages(t *testing.T) {
	t.Parallel()

	contractError := errorReturnMeta(t, hostErrorVal(101))
	trappedMeta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		errorEvent(hostErrorVal(4)))

	noError := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), fnCall("transfer", false))

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "wrong code",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorTypeSceContract, 404)
			},
			contains: []string{
				"AssertContractError: got ScErrorTypeContract(101), want ScErrorTypeContract(404)",
			},
		},
		{
			name: "wrong category",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorTypeSceStorage, uint32(xdr.ScErrorCodeScecMissingValue))
			},
			contains: []string{
				"got ScErrorTypeContract(101), want ScErrorTypeStorage(MissingValue)",
			},
		},
		{
			name: "wrong code in the diagnostics",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, trappedMeta,
					xdr.ScErrorTypeSceContract, 99)
			},
			contains: []string{
				"got ScErrorTypeContract(4), want ScErrorTypeContract(99)",
			},
		},
		{
			name: "no error recorded",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, noError,
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"the invocation recorded no error",
				"ScErrorTypeContract(4)",
			},
		},
		{
			name: "a bare transaction result",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, fixture(t, "tx_failed.res"),
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"a transaction result records that an operation failed but not which error it was",
				"transaction meta",
			},
		},
		{
			name: "a value that cannot be read",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, 42,
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"meta value could not be read",
				"unsupported type int",
			},
		},
		{
			name: "a nil value",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, nil,
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{"value is nil"},
		},
		{
			name: "a response without diagnostics",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, rpcproto.GetTransactionResponse{},
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"carries no diagnostic events",
				"diagnosticEventsXdr",
			},
		},
		{
			name: "a nil evidence pointer",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, (*focus.TrapEvidence)(nil),
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{"nil *TrapEvidence"},
		},
		{
			name: "evidence without a meta",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, focus.TrapEvidence{},
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{"the trap evidence carries no meta"},
		},
		{
			name: "an error outside the protocol's categories",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorType(42), 4)
			},
			contains: []string{
				"got ScErrorTypeContract(101), want ScErrorType(42)(4)",
			},
		},
		{
			name: "a code the protocol does not define",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorTypeSceStorage, 999)
			},
			contains: []string{
				"got ScErrorTypeContract(101), want ScErrorTypeStorage(999)",
			},
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

// TestAssertDiagnosticEventEmittedPasses covers the events that only exist in
// the diagnostic stream: a frame the host opened, a line the contract logged,
// and a contract event carried by a call that failed.
func TestAssertDiagnosticEventEmittedPasses(t *testing.T) {
	t.Parallel()

	contractID := testContractAddress

	tests := []struct {
		name string
		meta string
		want focus.Event
	}{
		{
			name: "fn call frame",
			meta: diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
				fnCall("transfer", true)),
			want: focus.Event{
				ContractID: contractID,
				Topics:     []any{focus.DiagnosticFnCall, nil, "transfer"},
			},
		},
		{
			name: "logged line",
			meta: diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
				logEvent("minted 10", true)),
			want: focus.Event{
				ContractID: contractID,
				Topics:     []any{"log"},
				Data:       diagString("minted 10"),
			},
		},
		{
			name: "contract event from a failed call",
			meta: diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
				contractEventInDiag(false)),
			want: focus.Event{
				ContractID: contractID,
				Topics:     []any{"transfer"},
				Data:       diagU32(7),
			},
		},
		{
			name: "version 4 diagnostic stream",
			meta: diagnosticMetaV4(t, pinChanges(t, testDiagnosticLedger),
				fnCall("transfer", true)),
			want: focus.Event{
				Topics: []any{focus.DiagnosticFnCall, nil, "transfer"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertDiagnosticEventEmitted(tb, tc.meta, tc.want) {
				t.Errorf("diagnostic event was not matched:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertDiagnosticEventEmittedFailureMessage checks that a miss names the
// wanted event and lists the diagnostics that were there instead.
func TestAssertDiagnosticEventEmittedFailureMessage(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("transfer", false),
		logEvent("transferring 10", false),
	)

	tb := faketb.New()
	if focus.AssertDiagnosticEventEmitted(tb, meta, focus.Event{
		Topics: []any{"fn_call", nil, "withdraw"},
	}) {
		t.Fatal("assertion matched an event that was not emitted")
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading the failure message: %v", err)
	}
	for _, want := range []string{
		"AssertDiagnosticEventEmitted",
		"no diagnostic event matched",
		"events present (2)",
		"fn_call",
		"transferring 10",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("failure message does not contain %q:\n%s", want, msg)
		}
	}
}

// TestDiagnosticEventsReturnsMatcherEvents pins the escape hatch's shape: the
// events can be read back as focus.Event values and carry the stream position
// as well as the payload.
func TestDiagnosticEventsReturnsMatcherEvents(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("transfer", true),
		contractEventInDiag(false),
	)

	tb := faketb.New()
	events := focus.DiagnosticEvents(tb, meta)
	if len(events) != 2 {
		t.Fatalf("DiagnosticEvents returned %d events, want 2:\n%s", len(events), tb.Output())
	}

	first := events[0]
	if first.Kind != focus.DiagnosticFnCall || first.Depth != 0 || first.Index != 0 {
		t.Errorf("first event = kind %q depth %d index %d, want fn_call at depth 0 index 0",
			first.Kind, first.Depth, first.Index)
	}
	if first.ContractID != testContractAddress {
		t.Errorf("first event contract = %q, want %q", first.ContractID, testContractAddress)
	}

	second := events[1]
	if second.InSuccessfulContractCall {
		t.Error("an event from a failed call is marked successful")
	}
	if data, ok := second.Data.(xdr.ScVal); !ok || data.Type != xdr.ScValTypeScvU32 {
		t.Errorf("second event payload = %v, want u32(7)", second.Data)
	}
}

// TestDiagnosticEventEmittedRequiresFatalf covers the require mirror.
func TestDiagnosticEventEmittedRequiresFatalf(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), fnCall("transfer", true))

	tb := faketb.New()
	require.DiagnosticEventEmitted(tb, meta, focus.Event{Topics: []any{"withdraw"}})
	if len(tb.Fatals) != 1 || len(tb.Errors) != 0 {
		t.Fatalf("require reported through Errorf; want one Fatalf (errors=%d fatals=%d)",
			len(tb.Errors), len(tb.Fatals))
	}
}

// TestContractErrorRequiresFatalf is the documented difference between the
// two packages: require stops the test, and its message is unchanged.
func TestContractErrorRequiresFatalf(t *testing.T) {
	t.Parallel()

	meta := errorReturnMeta(t, hostErrorVal(101))

	tb := faketb.New()
	require.ContractError(tb, meta, xdr.ScErrorTypeSceContract, 404)
	if len(tb.Fatals) != 1 || len(tb.Errors) != 0 {
		t.Fatalf("require reported through Errorf; want one Fatalf (errors=%d fatals=%d)",
			len(tb.Errors), len(tb.Fatals))
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading the failure message: %v", err)
	}
	if !strings.Contains(msg, "got ScErrorTypeContract(101), want ScErrorTypeContract(404)") {
		t.Errorf("require lost the error comparison:\n%s", msg)
	}
}
