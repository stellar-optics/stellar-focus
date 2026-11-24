package focus_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// testDiagnosticLedger and testDiagnosticCloseTime are the timeline a
// diagnostic stream in these tests belongs to.
const testDiagnosticLedger = 3966820

var testDiagnosticCloseTime = time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)

// testContractID and testContractAddress are a fixed contract, rendered
// through strkey so the assertions under test see the same "C…" form a user
// does.
var testContractID, testContractAddress = makeTestContract()

func makeTestContract() (xdr.ContractId, string) {
	var id xdr.ContractId
	copy(id[:], []byte("focus-diagnostic-contract-id-32!"))
	return id, strkey.MustEncode(strkey.VersionByteContract, id[:])
}

// diagnosticEvent wraps a contract event as a diagnostic event with the given
// in-successful-call flag, which is the only thing the wrapper adds.
func diagnosticEvent(e xdr.ContractEvent, inSuccessfulCall bool) xdr.DiagnosticEvent {
	return xdr.DiagnosticEvent{InSuccessfulContractCall: inSuccessfulCall, Event: e}
}

// ScVal builders for the values a diagnostic stream carries.
func diagSymbol(symbol string) xdr.ScVal {
	sym := xdr.ScSymbol(symbol)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func diagU32(v uint32) xdr.ScVal {
	u := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

func diagString(s string) xdr.ScVal {
	str := xdr.ScString(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
}

func diagVoid() xdr.ScVal { return xdr.ScVal{Type: xdr.ScValTypeScvVoid} }

// contractAddressVal is the test contract as an ScVal, which is how a
// diagnostic topic carries an address.
func contractAddressVal() xdr.ScVal {
	id := testContractID
	return xdr.ScVal{
		Type: xdr.ScValTypeScvAddress,
		Address: &xdr.ScAddress{
			Type:       xdr.ScAddressTypeScAddressTypeContract,
			ContractId: &id,
		},
	}
}

// diagContractEvent builds a contract event around topics and data.
func diagContractEvent(
	kind xdr.ContractEventType, id *xdr.ContractId, data xdr.ScVal, topics ...xdr.ScVal,
) xdr.ContractEvent {
	return xdr.ContractEvent{
		Type:       kind,
		ContractId: id,
		Body: xdr.ContractEventBody{
			V:  0,
			V0: &xdr.ContractEventV0{Topics: topics, Data: data},
		},
	}
}

// fnCall is the event a contract call opens with: topics of the function name
// and the contract it is called on.
func fnCall(fn string, inSuccessfulCall bool) xdr.DiagnosticEvent {
	id := testContractID
	return diagnosticEvent(diagContractEvent(xdr.ContractEventTypeDiagnostic, &id, diagVoid(),
		diagSymbol(focus.DiagnosticFnCall), contractAddressVal(), diagSymbol(fn)), inSuccessfulCall)
}

// fnReturn closes the frame its fnCall opened, carrying the call's return
// value.
func fnReturn(fn string, ret xdr.ScVal, inSuccessfulCall bool) xdr.DiagnosticEvent {
	id := testContractID
	return diagnosticEvent(diagContractEvent(xdr.ContractEventTypeDiagnostic, &id, ret,
		diagSymbol(focus.DiagnosticFnReturn), diagSymbol(fn)), inSuccessfulCall)
}

// logEvent is a line the contract logged.
func logEvent(msg string, inSuccessfulCall bool) xdr.DiagnosticEvent {
	id := testContractID
	return diagnosticEvent(diagContractEvent(xdr.ContractEventTypeDiagnostic, &id, diagString(msg),
		diagSymbol("log")), inSuccessfulCall)
}

// contractEventInDiag is a contract event as it appears in the diagnostic
// stream, which is where the events of a failed call survive.
func contractEventInDiag(inSuccessfulCall bool) xdr.DiagnosticEvent {
	id := testContractID
	return diagnosticEvent(diagContractEvent(xdr.ContractEventTypeContract, &id, diagU32(7),
		diagSymbol("transfer")), inSuccessfulCall)
}

// systemEvent is emitted by the protocol rather than a contract, so it
// belongs to no contract.
func systemEvent(inSuccessfulCall bool) xdr.DiagnosticEvent {
	return diagnosticEvent(diagContractEvent(xdr.ContractEventTypeSystem, nil, diagVoid(),
		diagSymbol("core_metrics")), inSuccessfulCall)
}

// pinChanges builds one state change per ledger, which is how a transaction
// meta pins the ledger it was applied in.
func pinChanges(t *testing.T, ledgers ...uint32) []xdr.LedgerEntryChange {
	t.Helper()

	out := make([]xdr.LedgerEntryChange, 0, len(ledgers))
	for _, ledger := range ledgers {
		entry := accountEntryValue(t, testSource, 100_0000000, xdr.Thresholds{1, 0, 0, 0})
		entry.LastModifiedLedgerSeq = xdr.Uint32(ledger)
		out = append(out, stateChange(entry))
	}
	return out
}

// diagnosticMeta wraps a stream in the version 3 meta a Soroban transaction
// produces.
func diagnosticMeta(t *testing.T, pins []xdr.LedgerEntryChange, events ...xdr.DiagnosticEvent) string {
	t.Helper()
	return mustMarshal(t, xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			TxChangesBefore: pins,
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue:      diagVoid(),
				DiagnosticEvents: events,
			},
		},
	})
}

// diagnosticMetaV4 is the same stream in the version 4 meta newer protocols
// produce.
func diagnosticMetaV4(t *testing.T, pins []xdr.LedgerEntryChange, events ...xdr.DiagnosticEvent) string {
	t.Helper()
	return mustMarshal(t, xdr.TransactionMeta{
		V: 4,
		V4: &xdr.TransactionMetaV4{
			TxChangesBefore:  pins,
			DiagnosticEvents: events,
		},
	})
}

// nestedStream is a stream of the shape a contract call actually produces: an
// outer call that emits an event and logs, a nested call that returns, and the
// outer call returning. The system event follows, outside every frame.
func nestedStream(t *testing.T) []xdr.DiagnosticEvent {
	t.Helper()
	return []xdr.DiagnosticEvent{
		fnCall("transfer", true),
		contractEventInDiag(true),
		logEvent("minting", true),
		fnCall("mint", true),
		fnReturn("mint", diagU32(1), true),
		fnReturn("transfer", diagU32(7), true),
		systemEvent(true),
	}
}

// TestDiagnosticEvents reads a stream back, checking the position, depth and
// kind of each event — the decoration that makes an ordering question
// answerable at all.
func TestDiagnosticEvents(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), nestedStream(t)...)

	tb := faketb.New()
	events := focus.DiagnosticEvents(tb, meta)
	if len(events) != 7 {
		t.Fatalf("DiagnosticEvents returned %d events, want 7:\\n%s", len(events), tb.Output())
	}

	tests := []struct {
		index   int
		kind    string
		depth   int
		hasBody bool
	}{
		{0, focus.DiagnosticFnCall, 0, true},
		{1, focus.DiagnosticContract, 1, true},
		{2, "log", 1, true},
		{3, focus.DiagnosticFnCall, 1, true},
		{4, focus.DiagnosticFnReturn, 1, true},
		{5, focus.DiagnosticFnReturn, 0, true},
		{6, focus.DiagnosticSystem, 0, true},
	}

	for _, tc := range tests {
		got := events[tc.index]
		if got.Index != tc.index {
			t.Errorf("event %d reports index %d", tc.index, got.Index)
		}
		if got.Kind != tc.kind {
			t.Errorf("event %d has kind %q, want %q", tc.index, got.Kind, tc.kind)
		}
		if got.Depth != tc.depth {
			t.Errorf("event %d has depth %d, want %d", tc.index, got.Depth, tc.depth)
		}
		if !got.InSuccessfulContractCall {
			t.Errorf("event %d should be marked as emitted in a successful call", tc.index)
		}
		if !tc.hasBody {
			continue
		}
		if len(got.Topics) == 0 {
			t.Errorf("event %d has no topics", tc.index)
		}
	}

	if events[0].ContractID != testContractAddress {
		t.Errorf("event 0 belongs to %q, want %q", events[0].ContractID, testContractAddress)
	}
	if events[6].ContractID != "" {
		t.Errorf("the system event belongs to %q, want no contract", events[6].ContractID)
	}
	if data, ok := events[1].Data.(xdr.ScVal); !ok || data.Type != xdr.ScValTypeScvU32 {
		t.Errorf("event 1 carries a %v payload, want u32", events[1].Data)
	}
}

// TestDiagnosticEventsRecordsFailedCalls checks the flag that makes diagnostic
// events worth asserting on: the events of a call that failed survive here and
// nowhere else.
func TestDiagnosticEventsRecordsFailedCalls(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("transfer", false),
		contractEventInDiag(false),
		fnReturn("transfer", diagVoid(), false),
	)

	tb := faketb.New()
	events := focus.DiagnosticEvents(tb, meta)
	for _, e := range events {
		if e.InSuccessfulContractCall {
			t.Errorf("event %d of a failed call is marked as successful", e.Index)
		}
	}

	// A failure listing has to say so, or two streams that differ only in this
	// flag would read identically.
	tb = faketb.New()
	if focus.AssertDiagnosticEventOrder(tb, meta, []focus.DiagnosticEventMatch{
		{Kind: focus.DiagnosticFnCall, Call: focus.CallOutcomeSucceeded},
	}) {
		t.Fatal("assertion matched an event from a failed call")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "in a call that succeeded") {
		t.Errorf("the matcher is not described in the message:\n%s", msg)
	}
	if !strings.Contains(msg, "(call failed)") {
		t.Errorf("the events present are not marked as failed:\n%s", msg)
	}
}

func TestAssertDiagnosticEventOrderPasses(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), nestedStream(t)...)
	empty := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger))

	tests := []struct {
		name string
		meta string
		want []focus.DiagnosticEventMatch
	}{
		{
			"the whole stream pins the whole order",
			meta,
			[]focus.DiagnosticEventMatch{
				{Kind: focus.DiagnosticFnCall},
				{Kind: focus.DiagnosticContract, Topics: []any{"transfer"}},
				{Kind: "log"},
				{Kind: focus.DiagnosticFnCall},
				{Kind: focus.DiagnosticFnReturn},
				{Kind: focus.DiagnosticFnReturn},
				{Kind: focus.DiagnosticSystem},
			},
		},
		{
			"a subsequence pins the relative order",
			meta,
			[]focus.DiagnosticEventMatch{
				{Kind: focus.DiagnosticFnCall},
				{Kind: focus.DiagnosticContract, Data: diagU32(7)},
				{Kind: focus.DiagnosticFnReturn},
				{Kind: focus.DiagnosticSystem},
			},
		},
		{
			"the nested call is what comes second",
			meta,
			[]focus.DiagnosticEventMatch{
				{Kind: focus.DiagnosticFnCall, Topics: []any{"fn_call", nil, "transfer"}},
				{Kind: focus.DiagnosticFnCall, Topics: []any{"fn_call", nil, "mint"}},
			},
		},
		{
			"kind matching is case-insensitive",
			meta,
			[]focus.DiagnosticEventMatch{{Kind: "FN_CALL"}, {Kind: "Fn_Return"}},
		},
		{
			"contract events are filtered by the contract that emitted them",
			meta,
			[]focus.DiagnosticEventMatch{{Kind: focus.DiagnosticContract, ContractID: testContractAddress}},
		},
		{
			"events are filtered by the outcome of the call",
			meta,
			[]focus.DiagnosticEventMatch{{Kind: focus.DiagnosticFnReturn, Call: focus.CallOutcomeSucceeded}},
		},
		{
			"wanting nothing is satisfied",
			meta,
			nil,
		},
		{
			"wanting nothing is satisfied by an empty stream",
			empty,
			nil,
		},
		{
			"the payload is matched by value",
			meta,
			[]focus.DiagnosticEventMatch{{Kind: "log", Data: diagString("minting")}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertDiagnosticEventOrder(tb, tc.meta, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertDiagnosticEventOrderMessages checks that a failure says which
// wanted event went unmatched, where the search had reached, and — when the
// events are all present but in the wrong order — where the event it wanted
// actually is, which is the swap a set comparison would never notice.
func TestAssertDiagnosticEventOrderMessages(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("transfer", true),
		contractEventInDiag(true),
		fnReturn("transfer", diagU32(7), true),
	)
	empty := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger))

	tests := []struct {
		name     string
		meta     string
		want     []focus.DiagnosticEventMatch
		contains []string
	}{
		{
			name: "the last wanted event is absent",
			meta: meta,
			want: []focus.DiagnosticEventMatch{
				{Kind: focus.DiagnosticFnCall},
				{Kind: focus.DiagnosticContract, Data: diagU32(9)},
			},
			contains: []string{
				"AssertDiagnosticEventOrder: no event at or after stream index 1 matched want[1] of 2 (contract  data u32(9))",
				"1 of the 2 wanted events matched, in order",
				"diagnostic events present (3):",
				"[0] fn_call   depth 0  contract C",
				"[1] contract  depth 1  contract C",
				"[2] fn_return depth 0  contract C",
				"data u32(7)",
			},
		},
		{
			name: "the events are present in the wrong order",
			meta: meta,
			want: []focus.DiagnosticEventMatch{
				{Kind: focus.DiagnosticContract},
				{Kind: focus.DiagnosticFnCall},
			},
			contains: []string{
				"the diagnostic events are in the wrong order",
				"want[1] (fn_call) is present at stream index 0",
				"at or after index 2",
			},
		},
		{
			name: "the payload differs",
			meta: meta,
			want: []focus.DiagnosticEventMatch{{Kind: focus.DiagnosticContract, Data: diagU32(9)}},
			contains: []string{
				`want[0] of 1 (contract  data u32(9))`,
				"data u32(7)",
			},
		},
		{
			name: "topics are matched element by element",
			meta: meta,
			want: []focus.DiagnosticEventMatch{{Kind: focus.DiagnosticContract, Topics: []any{"transfer", nil}}},
			contains: []string{
				`topics["transfer", *]`,
				`topics["transfer"]`,
			},
		},
		{
			name: "a topic that is not a value is shown as it was written",
			meta: meta,
			want: []focus.DiagnosticEventMatch{{Kind: focus.DiagnosticContract, Topics: []any{42}}},
			contains: []string{
				"topics[42]",
			},
		},
		{
			name: "the wrong contract",
			meta: meta,
			want: []focus.DiagnosticEventMatch{{ContractID: testSource}},
			contains: []string{
				"contract " + testSource[:5],
			},
		},
		{
			name: "the empty stream says so",
			meta: empty,
			want: []focus.DiagnosticEventMatch{{Kind: focus.DiagnosticFnCall}},
			contains: []string{
				"no event at or after stream index 0 matched want[0] of 1 (fn_call)",
				"the transaction emitted no diagnostic events",
			},
		},
		{
			name: "a classic transaction is not a diagnostic stream",
			meta: metaV1(t, nil, nil),
			want: []focus.DiagnosticEventMatch{{Kind: focus.DiagnosticFnCall}},
			contains: []string{
				"this transaction meta is version 1, which records no diagnostic events",
				"contract invocation",
			},
		},
		{
			name: "an envelope is not a transaction meta",
			meta: fixture(t, "tx_failed.env"),
			want: []focus.DiagnosticEventMatch{{Kind: focus.DiagnosticFnCall}},
			contains: []string{
				"meta value could not be read",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertDiagnosticEventOrder(tb, tc.meta, tc.want) {
				t.Fatal("assertion passed when it should have failed")
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

func TestAssertDiagnosticEventTimelinePasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		meta     string
		timeline focus.DiagnosticTimeline
	}{
		{
			"the newest pin is the ledger the transaction was applied in",
			diagnosticMeta(t, pinChanges(t, testDiagnosticLedger-1, testDiagnosticLedger),
				fnCall("transfer", true)),
			focus.DiagnosticTimeline{Ledger: testDiagnosticLedger, ClosedAt: testDiagnosticCloseTime},
		},
		{
			"a single pin",
			diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), fnCall("transfer", true)),
			focus.DiagnosticTimeline{Ledger: testDiagnosticLedger, ClosedAt: testDiagnosticCloseTime},
		},
		{
			"a version 4 meta places its stream the same way",
			diagnosticMetaV4(t, pinChanges(t, testDiagnosticLedger), fnCall("transfer", true)),
			focus.DiagnosticTimeline{Ledger: testDiagnosticLedger, ClosedAt: testDiagnosticCloseTime},
		},
		{
			"a close time in another location is the same instant",
			diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), fnCall("transfer", true)),
			focus.DiagnosticTimeline{
				Ledger:   testDiagnosticLedger,
				ClosedAt: testDiagnosticCloseTime.In(time.FixedZone("UTC+2", 2*60*60)),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertDiagnosticEventTimeline(tb, tc.meta, tc.timeline) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertDiagnosticEventTimelineMessages checks that a stream that cannot
// be placed in the ledger the caller asserted is reported with both ledgers,
// the pins that decided it, and the stream itself.
func TestAssertDiagnosticEventTimelineMessages(t *testing.T) {
	t.Parallel()

	placed := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger-1, testDiagnosticLedger),
		fnCall("transfer", true), fnReturn("transfer", diagU32(7), true))

	tests := []struct {
		name     string
		meta     string
		timeline focus.DiagnosticTimeline
		contains []string
	}{
		{
			name:     "the wrong ledger",
			meta:     placed,
			timeline: focus.DiagnosticTimeline{Ledger: 100, ClosedAt: testDiagnosticCloseTime},
			contains: []string{
				"AssertDiagnosticEventTimeline: the transaction was applied in ledger 3966820, want ledger 100, closed 2026-08-05T12:00:00Z",
				"ledger pins (2): 3966819, 3966820",
				"restored out of the archival state",
				"diagnostic events present (2):",
				"[0] fn_call   depth 0",
			},
		},
		{
			name:     "no ledger was asserted",
			meta:     placed,
			timeline: focus.DiagnosticTimeline{ClosedAt: testDiagnosticCloseTime},
			contains: []string{
				"the timeline is incomplete: ledger 0, closed 2026-08-05T12:00:00Z",
				"both are required",
			},
		},
		{
			name:     "no close time was asserted",
			meta:     placed,
			timeline: focus.DiagnosticTimeline{Ledger: testDiagnosticLedger},
			contains: []string{
				"the timeline is incomplete: ledger 3966820, closed (no close time)",
				"carries no timestamp of its own",
			},
		},
		{
			name:     "the meta pins no ledger",
			meta:     diagnosticMeta(t, nil, fnCall("transfer", true)),
			timeline: focus.DiagnosticTimeline{Ledger: testDiagnosticLedger, ClosedAt: testDiagnosticCloseTime},
			contains: []string{
				"the meta pins no ledger",
				"cannot say which ledger it came from",
			},
		},
		{
			name:     "there is no stream to place",
			meta:     diagnosticMeta(t, pinChanges(t, testDiagnosticLedger)),
			timeline: focus.DiagnosticTimeline{Ledger: testDiagnosticLedger, ClosedAt: testDiagnosticCloseTime},
			contains: []string{
				"the transaction emitted no diagnostic events",
				"no stream to place in ledger 3966820",
			},
		},
		{
			name:     "an unreadable meta",
			meta:     "!!!",
			timeline: focus.DiagnosticTimeline{Ledger: testDiagnosticLedger, ClosedAt: testDiagnosticCloseTime},
			contains: []string{
				"meta value could not be read",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertDiagnosticEventTimeline(tb, tc.meta, tc.timeline) {
				t.Fatal("assertion passed when it should have failed")
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

// TestDiagnosticEventsEscapeHatch checks that an unreadable meta reports a
// failure and returns nothing, rather than returning a misleading empty
// stream.
func TestDiagnosticEventsEscapeHatch(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	if events := focus.DiagnosticEvents(tb, nil); events != nil {
		t.Errorf("DiagnosticEvents returned %d events for a nil meta, want none", len(events))
	}
	if !tb.Failed() {
		t.Error("DiagnosticEvents reported nothing for an unreadable meta")
	}
}

// TestDiagnosticEventKindNames pins the names a matcher is written against.
// They are the protocol's own words rather than this package's invention, so
// a matcher reads the same way the stream does.
func TestDiagnosticEventKindNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event xdr.DiagnosticEvent
		kind  string
	}{
		{"fn_call", fnCall("transfer", true), focus.DiagnosticFnCall},
		{"fn_return", fnReturn("transfer", diagVoid(), true), focus.DiagnosticFnReturn},
		{"log", logEvent("hello", true), "log"},
		{"system event", systemEvent(true), focus.DiagnosticSystem},
		{"contract event", contractEventInDiag(true), focus.DiagnosticContract},
		{"an unnamed diagnostic event", diagnosticEvent(
			diagContractEvent(xdr.ContractEventTypeDiagnostic, nil, diagVoid(), diagU32(1)), true),
			focus.DiagnosticOther},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), tc.event)
			tb := faketb.New()
			events := focus.DiagnosticEvents(tb, meta)
			if len(events) != 1 {
				t.Fatalf("read %d events, want 1:\n%s", len(events), tb.Output())
			}
			if events[0].Kind != tc.kind {
				t.Errorf("kind is %q, want %q", events[0].Kind, tc.kind)
			}
		})
	}
}

func TestAssertContractLogMessage(t *testing.T) {
	t.Parallel()

	meta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("deposit", true),
		logEvent("user 101 deposited 500 tokens successfully", true),
		fnReturn("deposit", diagVoid(), true),
	)

	t.Run("matches literal substring in diagnostic log", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertContractLogMessage(tb, meta, "deposited 500") {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.ContractLogMessage(t, meta, "deposited 500")
	})

	t.Run("matches regular expression enclosed in slashes", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertContractLogMessage(tb, meta, `/user \d+ deposited/`) {
			t.Errorf("assertion failed with regex:\n%s", tb.Output())
		}
		require.ContractLogMessage(t, meta, `/user \d+ deposited/`)
	})

	t.Run("fails and dumps captured events when no match is found", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertContractLogMessage(tb, meta, "withdrew") {
			t.Fatal("assertion passed when log message was absent")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, `no diagnostic log message matched "withdrew"`) {
			t.Errorf("message did not report missing pattern:\n%s", msg)
		}
		if !strings.Contains(msg, "captured diagnostic event(s)") || !strings.Contains(msg, "user 101 deposited") {
			t.Errorf("message did not dump captured events:\n%s", msg)
		}
	})

	t.Run("fails when regex is invalid", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertContractLogMessage(tb, meta, "/[unclosed/") {
			t.Fatal("assertion passed with invalid regex")
		}
		if !strings.Contains(tb.Output(), "invalid regular expression") {
			t.Errorf("message did not report invalid regex:\n%s", tb.Output())
		}
	})

	t.Run("fails when transaction has no diagnostic events", func(t *testing.T) {
		t.Parallel()

		emptyMeta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger))
		tb := faketb.New()
		if focus.AssertContractLogMessage(tb, emptyMeta, "hello") {
			t.Fatal("assertion passed on empty diagnostic stream")
		}
		if !strings.Contains(tb.Output(), "emitted no diagnostic events") {
			t.Errorf("message did not report empty events:\n%s", tb.Output())
		}
	})
}
