package focus

import (
	"fmt"
	"math/big"
	"strings"

	rpcproto "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// contractIDString renders a contract id as its strkey "C…" form.
//
// ContractId is a bare 32-byte hash in the generated code, so the strkey
// encoding has to be applied here rather than read off a helper.
func contractIDString(id xdr.ContractId) (string, error) {
	return strkey.Encode(strkey.VersionByteContract, id[:])
}

// Event describes a contract event to look for.
//
// A zero field matches anything, so asserting only that a contract emitted
// something is as easy as asserting on the exact payload. Topics and Data
// accept the same forms as any other value in this package, plus a plain
// string for the common case of a symbol topic:
//
//	focus.AssertEventEmitted(t, meta, focus.Event{
//	    ContractID: "CBGSB…",
//	    Topics:     []any{"transfer", from, to},
//	})
//
// With a contract's specification, the data can be named the way the
// contract's author named it, and a failure says which field differs:
//
//	focus.AssertEventEmitted(t, meta, focus.Event{
//	    Topics: []any{"transfer"},
//	    Data:   map[string]any{"from": from, "amount": 100},
//	    Spec:   spec,
//	})
type Event struct {
	// ContractID is the strkey contract address. Empty matches any contract.
	ContractID string
	// Topics are the event topics, in order. Nil matches any topics. A nil
	// element matches any single topic, so []any{"transfer", nil} matches a
	// two-topic event whose first topic is the symbol "transfer".
	Topics []any
	// Data is the event payload. Nil matches any payload.
	//
	// With a specification, a map of field names is matched against the
	// fields the contract declares, and a failure names the field that
	// differs. The map must name every field the payload has: a field the
	// payload carries and the matcher does not name is a mismatch, so a
	// test says what it means rather than what it happens to check.
	//
	// Without a specification, the payload is compared as it is.
	Data any
	// Spec is the contract's specification: the ScSpecEntry list that
	// names its types and events. It is optional, and it changes only how
	// the payload is described, never whether it matches.
	//
	// focus.ContractSpec extracts one from a compiled contract.
	Spec []xdr.ScSpecEntry

	// The fields below are populated when the Event comes from
	// DiagnosticEvents, where a stream position and call depth matter. They
	// are ignored by the event matchers.
	//
	// Index is the event's zero-based position in the diagnostic stream.
	Index int
	// Depth is the contract call depth the event was emitted at.
	Depth int
	// Kind classifies a diagnostic event: one of the Diagnostic* constants,
	// or the event's first topic symbol.
	Kind string
	// InSuccessfulContractCall reports whether the call that emitted the
	// event completed. Events from a failed call survive only in diagnostics.
	InSuccessfulContractCall bool

	// event is the raw diagnostic event, kept for matching.
	event xdr.ContractEvent
	// detail is the pre-rendered diagnostic description.
	detail string
}

// EventOption configures an event assertion.
//
// Options only relax what a matcher checks, never tighten it: an option can
// let a difference through, but it can never turn a match into a failure.
// That keeps a test's meaning simple — the matcher says what must be there,
// and the options say what need not be.
//
// Whatever an option relaxes is named in the failure output, because an
// assertion that quietly stops checking something is how a test starts
// passing for the wrong reason.
type EventOption func(*eventConfig)

// eventConfig holds the resolved settings of an event assertion.
type eventConfig struct {
	ignoreContractID bool
	ignoreTopics     bool
	ignoreData       bool
}

// newEventConfig resolves the options of an event assertion.
func newEventConfig(opts ...EventOption) *eventConfig {
	c := &eventConfig{}
	for _, o := range opts {
		o(c)
	}
	return c
}

// IgnoreEventContractID ignores the contract id of every wanted event, so
// only the payload is compared.
//
// A matcher that names a contract is usually written that way for the
// reader's benefit — the address is the first thing anyone asks about —
// while the test itself is about the topics. This option says so
// explicitly rather than leaving the contract id out of the matcher and
// losing the documentation.
func IgnoreEventContractID() EventOption {
	return func(c *eventConfig) { c.ignoreContractID = true }
}

// IgnoreEventTopics ignores the topics of every wanted event, so only the
// payload and the order of the events are compared.
func IgnoreEventTopics() EventOption {
	return func(c *eventConfig) { c.ignoreTopics = true }
}

// IgnoreEventData ignores the payload of every wanted event, so only the
// topics and the order of the events are compared.
//
// This is the option for a contract whose payload carries a value that
// legitimately differs between runs — a timestamp, a generated id — while
// the sequence of topics is what the test is about.
func IgnoreEventData() EventOption {
	return func(c *eventConfig) { c.ignoreData = true }
}

// ignoredFields names the fields the options relaxed, for the failure output.
func (c *eventConfig) ignoredFields() string {
	var names []string
	if c.ignoreContractID {
		names = append(names, "contract id")
	}
	if c.ignoreTopics {
		names = append(names, "topics")
	}
	if c.ignoreData {
		names = append(names, "data")
	}
	return strings.Join(names, ", ")
}

// AssertEventEmitted checks that a transaction meta contains a matching
// contract event.
func AssertEventEmitted(t TB, meta Value, want Event) bool {
	t.Helper()

	events, ok := contractEvents(t, "AssertEventEmitted", meta)
	if !ok {
		return false
	}

	spec := newSpecIndex(want.Spec)

	for _, e := range events {
		if eventMatches(e, want, spec) {
			return true
		}
	}

	t.Errorf("AssertEventEmitted: no event matched %s\n\n%s%s%s",
		describeWantedEvent(want), indent(describeEvents(events, spec)),
		describeEventDiffs(events, want, spec), describeSpecHint(want))
	return false
}

// AssertDiagnosticEventEmitted checks that a transaction meta contains a
// matching diagnostic event.
//
// Diagnostic events are the host's account of a contract call: the frames it
// opened, what the contract logged, and the error it trapped with. Unlike
// contract events, they are recorded even when the call fails, so this is the
// assertion for a trapped contract or an authorization failure.
//
// want accepts the same matcher as AssertEventEmitted. The stream position,
// depth and call outcome that DiagnosticEvents exposes are not part of the
// matcher; use AssertDiagnosticEventOrder when order or outcome matters.
func AssertDiagnosticEventEmitted(t TB, meta Value, want Event) bool {
	t.Helper()
	const assertion = "AssertDiagnosticEventEmitted"

	events, ok := diagnosticContractEvents(t, assertion, meta)
	if !ok {
		return false
	}

	spec := newSpecIndex(want.Spec)
	for _, e := range events {
		if eventMatches(e, want, spec) {
			return true
		}
	}

	t.Errorf("%s: no diagnostic event matched %s\n\n%s%s%s",
		assertion, describeWantedEvent(want), indent(describeEvents(events, spec)),
		describeEventDiffs(events, want, spec), describeSpecHint(want))
	return false
}

// diagnosticContractEvents returns the raw contract-event form of every
// diagnostic event a meta carries, in stream order.
func diagnosticContractEvents(t TB, assertion string, meta Value) ([]xdr.ContractEvent, bool) {
	t.Helper()

	events, _, ok := diagnosticStream(t, assertion, meta)
	if !ok {
		return nil, false
	}
	out := make([]xdr.ContractEvent, 0, len(events))
	for _, e := range events {
		out = append(out, e.event)
	}
	return out, true
}

// AssertEventCount checks how many contract events a meta carries.
func AssertEventCount(t TB, meta Value, want int) bool {
	t.Helper()

	events, ok := contractEvents(t, "AssertEventCount", meta)
	if !ok {
		return false
	}
	if len(events) != want {
		t.Errorf("AssertEventCount: meta has %d event(s), want %d\n\n%s",
			len(events), want, indent(describeEvents(events, nil)))
		return false
	}
	return true
}

// AssertNoEvents checks that a transaction emitted no contract events.
func AssertNoEvents(t TB, meta Value) bool {
	t.Helper()
	return AssertEventCount(t, meta, 0)
}

// AssertEventsEqual checks that the contract events in a meta are exactly the
// wanted events, in the wanted order.
//
// A contract that runs through several steps says so with a sequence of
// events — init, deposit, transfer, audit — and the sequence is the point:
// the same events in a different order describe a different program. This
// assertion pins the whole list, so both the count and the order of the
// events have to be right.
//
// Each wanted event is matched with the same rules as AssertEventEmitted, so
// a zero field matches anything and a nil topic matches any single topic.
// The options relax the comparison for the whole list; see IgnoreEventData
// and its neighbours.
//
// A failure names the index that deviated and prints what was emitted beside
// what was wanted, so the two can be read against each other, and then lists
// every event that was emitted.
func AssertEventsEqual(t TB, meta Value, want []Event, opts ...EventOption) bool {
	t.Helper()
	const assertion = "AssertEventsEqual"

	events, ok := contractEvents(t, assertion, meta)
	if !ok {
		return false
	}

	cfg := newEventConfig(opts...)

	if len(events) != len(want) {
		t.Errorf("%s: meta has %d event(s), want %d\n\n%s",
			assertion, len(events), len(want), indent(describeEvents(events, nil)))
		return false
	}

	for i, w := range want {
		if eventMatchesWith(events[i], w, cfg, newSpecIndex(w.Spec)) {
			continue
		}
		t.Errorf("%s: %s\n\n%s",
			assertion,
			describeEventMismatch(i, len(want), events[i], w, cfg),
			indent(describeEvents(events, nil)))
		return false
	}
	return true
}

// AssertReturnValue checks the value a contract invocation returned.
func AssertReturnValue(t TB, meta Value, want Value) bool {
	t.Helper()
	return assertReturnValue(t, "AssertReturnValue", meta, want, "", nil)
}

// AssertReturnValueWithSpec checks the value a contract invocation returned,
// naming the parts of the value the way the contract's author named them.
//
// function names the contract function whose output type describes the
// value — "transfer", say. An empty name is accepted when the
// specification declares exactly one function.
//
// spec is the contract's ScSpecEntry list; focus.ContractSpec extracts one
// from a compiled contract. The specification changes how the value is
// described, not whether it matches: the wanted value must name every
// field the returned value has.
//
//	focus.AssertReturnValueWithSpec(t, meta,
//	    map[string]any{"from": from, "amount": 100},
//	    "transfer", spec)
func AssertReturnValueWithSpec(t TB, meta Value, want Value, function string, spec []xdr.ScSpecEntry) bool {
	t.Helper()
	return assertReturnValue(t, "AssertReturnValueWithSpec", meta, want, function, spec)
}

// assertReturnValue checks a return value, with an optional specification
// describing its parts.
func assertReturnValue(t TB, assertion string, meta Value, want Value, function string, entries []xdr.ScSpecEntry) bool {
	t.Helper()

	soroban, ok := sorobanMeta(t, assertion, meta)
	if !ok {
		return false
	}

	index := newSpecIndex(entries)

	// The specification describes the return value, so the wanted value is
	// matched against it field by field rather than as one opaque value.
	if index.empty() {
		wantVal, ok := scVal(want)
		if !ok {
			t.Errorf("%s: want value could not be read as an ScVal", assertion)
			return false
		}
		if soroban.ReturnValue.Equals(wantVal) {
			return true
		}
		t.Errorf("%s: contract returned %s, want %s",
			assertion, describeScVal(soroban.ReturnValue), describeScVal(wantVal))
		return false
	}

	name := strings.TrimSpace(function)
	if name == "" {
		only, ok := index.onlyFunction()
		if !ok {
			t.Errorf("%s: the contract specification declares %d function(s), so the one to check "+
				"must be named", assertion, len(index.functions))
			return false
		}
		name = only
	}

	output, ok := index.functionOutput(name)
	if !ok {
		t.Errorf("%s: the contract specification has no function named %q", assertion, name)
		return false
	}

	returnType := index.resolveType(&output)
	diffs := index.specDiff(returnType, want, soroban.ReturnValue)
	if len(diffs) == 0 {
		return true
	}

	t.Errorf("%s: contract returned %s, want %s\n\n%s",
		assertion,
		index.describeSpecValue(returnType, soroban.ReturnValue),
		index.describeWantedValue(returnType, want),
		indent(strings.Join(diffs, "\n")))
	return false
}

// AssertContractTrapped checks that a transaction failed because the contract
// itself trapped, rather than being rejected by the protocol.
//
// This is the Soroban failure developers hit most, and distinguishing it from
// a malformed transaction is usually the first question worth answering.
func AssertContractTrapped(t TB, result Value) bool {
	t.Helper()
	return contractTrapped(t, "AssertContractTrapped", result)
}

// AssertSorobanResources checks that a Soroban transaction's resource limits
// fit inside the given budgets.
//
// The limits are the ones the transaction bids: CPU instructions, bytes read
// from the ledger, and bytes written back to it. They are budgets rather than
// measurements — a transaction asks for what it thinks it needs, and the
// network charges it for that — so this is the assertion a regression suite
// uses to keep a contract's cost from creeping up.
//
// A budget of zero means the dimension is not checked, which is how a test
// pins the instructions without pinning the memory.
//
// A failure names every dimension that went over its budget, and then shows
// the limits the transaction asked for.
func AssertSorobanResources(t TB, envelope Value, maxInstructions uint32, readBytes, writeBytes uint32) bool {
	t.Helper()
	const assertion = "AssertSorobanResources"

	data, ok := sorobanData(t, assertion, envelope)
	if !ok {
		return false
	}

	var over []string
	if maxInstructions > 0 && uint32(data.Resources.Instructions) > maxInstructions {
		over = append(over, fmt.Sprintf("CPU instructions are %d, budget is %d",
			data.Resources.Instructions, maxInstructions))
	}
	if readBytes > 0 && uint32(data.Resources.DiskReadBytes) > readBytes {
		over = append(over, fmt.Sprintf("read bytes are %d, budget is %d",
			data.Resources.DiskReadBytes, readBytes))
	}
	if writeBytes > 0 && uint32(data.Resources.WriteBytes) > writeBytes {
		over = append(over, fmt.Sprintf("write bytes are %d, budget is %d",
			data.Resources.WriteBytes, writeBytes))
	}
	if len(over) == 0 {
		return true
	}

	t.Errorf("%s: %s\n\n%s",
		assertion, strings.Join(over, "; "),
		indent(describeSorobanResources(data.Resources)))
	return false
}

// AssertResourceFee checks the refundable resource fee a Soroban transaction
// bids, in stroops.
//
// The fee is what the sender is willing to pay for the resources the
// transaction reserves; the unused part is refunded once the transaction
// lands. It is a bid rather than a charge, so this is the assertion that
// pins what a contract call costs the caller.
//
// The fee is in XLM, so the failure message shows both units.
func AssertResourceFee(t TB, envelope Value, wantFee int64) bool {
	t.Helper()
	const assertion = "AssertResourceFee"

	data, ok := sorobanData(t, assertion, envelope)
	if !ok {
		return false
	}

	got := int64(data.ResourceFee)
	if got == wantFee {
		return true
	}

	t.Errorf("%s: resource fee is %s, want %s", assertion, stroops(got), stroops(wantFee))
	return false
}

// AssertContractError checks that a contract invocation failed with a
// particular error, and that the error is the one the test expects.
//
// A Soroban contract does not fail with a bare "the contract trapped". It
// fails with an error the host records in full: a category and a code. A
// contract that panics with a code of its own fails with ScErrorTypeContract
// and the number it chose; a contract that reads a key that is not there fails
// with ScErrorTypeStorage and the host's ScErrorCodeMissingValue.
// AssertContractTrapped says that the invocation trapped; this assertion says
// which error it trapped with.
//
// metaOrResult accepts:
//
//	TransactionMeta                  the meta, whose Soroban section holds the
//	                                  return value and the diagnostic events;
//	SorobanTransactionMeta           the Soroban section on its own;
//	TrapEvidence                     a result paired with the meta that holds
//	                                  the diagnostics;
//	protocol.GetTransactionResponse  the Soroban RPC response, whose
//	                                  diagnosticEventsXdr carries the error.
//
// The error is read from the Soroban return value when that is an error, and
// from the host's error event otherwise — the error event is where the error
// of a trapped invocation lives.
//
// wantCode is the number the error carries: a contract's own code for
// ScErrorTypeContract, and the numeric value of an xdr.ScErrorCode for the
// host's categories, so a missing value is
// uint32(xdr.ScErrorCodeScecMissingValue).
func AssertContractError(t TB, metaOrResult Value, wantType xdr.ScErrorType, wantCode uint32) bool {
	t.Helper()
	const assertion = "AssertContractError"

	got, found, ok := contractError(t, assertion, metaOrResult)
	if !ok {
		return false
	}
	if !found {
		t.Errorf("%s: the invocation recorded no error, so there is nothing to compare with %s.\n"+
			"A contract error is recorded in the transaction meta — as the Soroban return value or as "+
			"the host's error diagnostic — so pass the meta, or the response that carries it.",
			assertion, describeScErrorCode(wantType, wantCode))
		return false
	}

	gotType, gotCode, gotHasCode := scErrorParts(got)
	if gotType == wantType && gotHasCode && gotCode == wantCode {
		return true
	}

	t.Errorf("%s: got %s, want %s", assertion,
		describeScErrorParts(gotType, gotCode, gotHasCode),
		describeScErrorCode(wantType, wantCode))
	return false
}

// contractError finds the error an invocation recorded, and reports whether
// one was found.
//
// The switch is the abstraction seam: another carrier — a Horizon response, a
// bundle record — is one more case here, and the assertion above never needs
// to know which one was used.
func contractError(t TB, assertion string, v Value) (xdr.ScError, bool, bool) {
	t.Helper()

	switch value := v.(type) {
	case rpcproto.GetTransactionResponse:
		return contractErrorFromResponse(t, assertion, &value)

	case *rpcproto.GetTransactionResponse:
		if value == nil {
			t.Errorf("%s: value is a nil *protocol.GetTransactionResponse", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromResponse(t, assertion, value)

	case TrapEvidence:
		if value.Meta == nil {
			t.Errorf("%s: the trap evidence carries no meta, and the error of a failed invocation "+
				"lives in the meta's return value or diagnostics", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromMetaValue(t, assertion, value.Meta)

	case *TrapEvidence:
		if value == nil {
			t.Errorf("%s: value is a nil *TrapEvidence", assertion)
			return xdr.ScError{}, false, false
		}
		if value.Meta == nil {
			t.Errorf("%s: the trap evidence carries no meta, and the error of a failed invocation "+
				"lives in the meta's return value or diagnostics", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromMetaValue(t, assertion, value.Meta)

	case xdr.SorobanTransactionMeta:
		meta := value
		return contractErrorFromSorobanMeta(t, &meta)

	case *xdr.SorobanTransactionMeta:
		if value == nil {
			t.Errorf("%s: value is a nil *SorobanTransactionMeta", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromSorobanMeta(t, value)

	default:
		return contractErrorFromMetaValue(t, assertion, v)
	}
}

// contractErrorFromMetaValue reads the error out of a transaction meta, in
// any of the forms an assertion accepts.
func contractErrorFromMetaValue(t TB, assertion string, v Value) (xdr.ScError, bool, bool) {
	t.Helper()

	if v == nil {
		t.Errorf("%s: value is nil, so there is no error to check", assertion)
		return xdr.ScError{}, false, false
	}

	decoded, err := coerce(v, "TransactionMeta")
	if err != nil {
		// A transaction result records that the invocation failed but not
		// which error it was, so it is called out rather than reported as an
		// unreadable value.
		if _, resultErr := coerce(v, "TransactionResult"); resultErr == nil {
			t.Errorf("%s: a transaction result records that an operation failed but not which error "+
				"it was.\nThe error is in the transaction meta, so pass the meta, or the getTransaction "+
				"response whose diagnosticEventsXdr carries it.", assertion)
			return xdr.ScError{}, false, false
		}
		t.Errorf("%s: meta value could not be read: %v", assertion, err)
		return xdr.ScError{}, false, false
	}

	m, ok := decoded.Raw.(*xdr.TransactionMeta)
	if !ok {
		t.Errorf("%s: value is a %s, want a TransactionMeta", assertion, decoded.Type)
		return xdr.ScError{}, false, false
	}
	return contractErrorFromMeta(t, assertion, m)
}

// contractErrorFromMeta reads the error a transaction meta records.
//
// The return value comes first: it is the value the invocation itself
// produced, where the diagnostic stream is the host's account of it. The
// error event is the fallback, because a trapped invocation leaves an error
// there and nowhere else.
func contractErrorFromMeta(t TB, assertion string, m *xdr.TransactionMeta) (xdr.ScError, bool, bool) {
	t.Helper()

	if ret, ok := metaReturnValue(m); ok {
		if e, ok := scValError(ret); ok {
			return e, true, true
		}
	}

	var (
		found bool
		err   xdr.ScError
	)
	if !guard(t, assertion, func() {
		raw, readErr := m.GetDiagnosticEvents()
		if readErr != nil {
			t.Errorf("%s: reading the diagnostic events: %v", assertion, readErr)
			return
		}
		for _, e := range decorateDiagnostics(raw) {
			if e.Kind != DiagnosticError {
				continue
			}
			data, hasData := eventData(e)
			if !hasData {
				continue
			}
			scErr, isError := scValError(data)
			if !isError {
				continue
			}
			err, found = scErr, true
			return
		}
	}) {
		return xdr.ScError{}, false, false
	}
	if !found {
		return xdr.ScError{}, false, true
	}
	return err, true, true
}

// contractErrorFromSorobanMeta reads the error out of a Soroban section.
func contractErrorFromSorobanMeta(t TB, meta *xdr.SorobanTransactionMeta) (xdr.ScError, bool, bool) {
	t.Helper()

	if e, ok := scValError(meta.ReturnValue); ok {
		return e, true, true
	}
	return xdr.ScError{}, false, true
}

// contractErrorFromResponse reads the error out of a Soroban RPC
// getTransaction response, whose diagnostic events carry it.
func contractErrorFromResponse(t TB, assertion string, resp *rpcproto.GetTransactionResponse) (xdr.ScError, bool, bool) {
	t.Helper()

	if len(resp.DiagnosticEventsXDR) == 0 {
		t.Errorf("%s: the transaction response carries no diagnostic events, so there is no error "+
			"to check.\nA Soroban RPC reports a failed contract call's error in diagnosticEventsXdr; "+
			"without it this check cannot be made.", assertion)
		return xdr.ScError{}, false, false
	}

	var (
		events []xdr.DiagnosticEvent
		err    error
	)
	if !guard(t, assertion, func() {
		events, err = decodeDiagnosticEvents(resp.DiagnosticEventsXDR)
	}) {
		return xdr.ScError{}, false, false
	}
	if err != nil {
		t.Errorf("%s: reading the response's diagnostic events: %v", assertion, err)
		return xdr.ScError{}, false, false
	}

	for _, e := range decorateDiagnostics(events) {
		if e.Kind != DiagnosticError {
			continue
		}
		data, hasData := eventData(e)
		if !hasData {
			continue
		}
		scErr, isError := scValError(data)
		if !isError {
			continue
		}
		return scErr, true, true
	}
	return xdr.ScError{}, false, true
}

// metaReturnValue returns the Soroban return value a transaction meta
// records, and whether the meta has one at all.
//
// A version 4 meta keeps the return value in the version 2 Soroban section,
// where it is optional; a version 3 meta keeps it in the version 1 section,
// where it is always present.
func metaReturnValue(m *xdr.TransactionMeta) (xdr.ScVal, bool) {
	switch m.V {
	case 3:
		v3, ok := m.GetV3()
		if !ok || v3.SorobanMeta == nil {
			return xdr.ScVal{}, false
		}
		return v3.SorobanMeta.ReturnValue, true
	case 4:
		v4, ok := m.GetV4()
		if !ok || v4.SorobanMeta == nil || v4.SorobanMeta.ReturnValue == nil {
			return xdr.ScVal{}, false
		}
		return *v4.SorobanMeta.ReturnValue, true
	default:
		return xdr.ScVal{}, false
	}
}

// eventData returns an event's payload as an ScVal.
//
// Event.Data accepts any form a matcher understands, but the diagnostic
// helpers store the raw xdr.ScVal of a body arm there. Reporting no payload
// for anything else keeps a hand-built event from being mistaken for an error.
func eventData(e Event) (xdr.ScVal, bool) {
	switch v := e.Data.(type) {
	case xdr.ScVal:
		return v, true
	case *xdr.ScVal:
		if v == nil {
			return xdr.ScVal{}, false
		}
		return *v, true
	default:
		return xdr.ScVal{}, false
	}
}

// scValError reads the error an ScVal carries, and reports whether it is one.
func scValError(v xdr.ScVal) (xdr.ScError, bool) {
	if v.Type != xdr.ScValTypeScvError {
		return xdr.ScError{}, false
	}
	e, ok := v.GetError()
	if !ok {
		return xdr.ScError{}, false
	}
	return e, true
}

// scErrorParts splits an error into the category and the number it carries.
//
// A contract error carries the number the contract chose; the host's
// categories carry one of the protocol's named codes. An error that carries
// neither is malformed, which the third return value reports.
func scErrorParts(e xdr.ScError) (xdr.ScErrorType, uint32, bool) {
	if e.Type == xdr.ScErrorTypeSceContract {
		if e.ContractCode == nil {
			return e.Type, 0, false
		}
		return e.Type, uint32(*e.ContractCode), true
	}
	if e.Code == nil {
		return e.Type, 0, false
	}
	return e.Type, uint32(int32(*e.Code)), true
}

// describeScErrorCode renders a wanted error as "ScErrorTypeContract(101)".
//
// The host's categories carry a named code, so those are named rather than
// numbered: "ScErrorTypeStorage(MissingValue)" says what went wrong, where a
// bare 3 would not.
func describeScErrorCode(t xdr.ScErrorType, code uint32) string {
	return describeScErrorParts(t, code, true)
}

// describeScErrorParts renders an error as "ScErrorTypeContract(101)", naming
// the category and the code the error carries.
func describeScErrorParts(t xdr.ScErrorType, code uint32, hasCode bool) string {
	name := scErrorTypeName(t)
	if !hasCode {
		return name + "(no code)"
	}
	if t == xdr.ScErrorTypeSceContract {
		return fmt.Sprintf("%s(%d)", name, code)
	}
	// A category the protocol does not define has no named codes either, so
	// the number is all there is to show.
	if t.String() == "" {
		return fmt.Sprintf("%s(%d)", name, code)
	}
	if named := scErrorCodeName(code); named != "" {
		return fmt.Sprintf("%s(%s)", name, named)
	}
	return fmt.Sprintf("%s(%d)", name, code)
}

// scErrorTypeName names an error category in plain English: the contract's
// own category is ScErrorTypeContract, not ScErrorTypeSceContract. A value
// outside the enum is named by number rather than left blank.
func scErrorTypeName(t xdr.ScErrorType) string {
	name := strings.TrimPrefix(t.String(), "ScErrorTypeSce")
	if name == "" {
		return fmt.Sprintf("ScErrorType(%d)", int32(t))
	}
	return "ScErrorType" + name
}

// scErrorCodeName names a host error code, and returns "" for a number the
// protocol does not define.
func scErrorCodeName(code uint32) string {
	name := strings.TrimPrefix(xdr.ScErrorCode(int32(code)).String(), "ScErrorCodeScec")
	if name == "" {
		return ""
	}
	return name
}

// Events returns the contract events in a meta, as an escape hatch for checks
// this package does not provide. It reports a test failure and returns nil
// when the meta cannot be read.
func Events(t TB, meta Value) []Event {
	t.Helper()

	raw, ok := contractEvents(t, "Events", meta)
	if !ok {
		return nil
	}
	out := make([]Event, 0, len(raw))
	for _, e := range raw {
		out = append(out, toEvent(e))
	}
	return out
}

// sorobanMeta extracts the Soroban section of a transaction meta.
func sorobanMeta(t TB, assertion string, meta Value) (*xdr.SorobanTransactionMeta, bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "", meta, "TransactionMeta")
	if !ok {
		return nil, false
	}

	m, ok := decoded.Raw.(*xdr.TransactionMeta)
	if !ok {
		t.Errorf("%s: value is a %s, want a TransactionMeta", assertion, decoded.Type)
		return nil, false
	}

	var soroban *xdr.SorobanTransactionMeta
	if !guard(t, assertion, func() {
		if v3, has := m.GetV3(); has {
			soroban = v3.SorobanMeta
		}
	}) {
		return nil, false
	}
	if soroban == nil {
		t.Errorf("%s: this transaction meta carries no Soroban section, so it has no "+
			"contract events or return value (was the transaction a contract invocation?)", assertion)
		return nil, false
	}
	return soroban, true
}

// contractEvents returns the contract events in a meta.
func contractEvents(t TB, assertion string, meta Value) ([]xdr.ContractEvent, bool) {
	t.Helper()
	soroban, ok := sorobanMeta(t, assertion, meta)
	if !ok {
		return nil, false
	}
	return soroban.Events, true
}

// sorobanData returns the Soroban transaction data of an envelope.
//
// The resource limits and the resource fee live in the transaction's
// extension, so they are only there for a contract invocation; an envelope
// without them is reported rather than treated as one that happens to ask
// for nothing.
func sorobanData(t TB, assertion string, envelope Value) (*xdr.SorobanTransactionData, bool) {
	t.Helper()

	env, _, ok := envelopeFrom(t, assertion, envelope)
	if !ok {
		return nil, false
	}

	var data *xdr.SorobanTransactionData
	if !guard(t, assertion, func() {
		switch {
		case env.V1 != nil:
			data = env.V1.Tx.Ext.SorobanData
		case env.V0 != nil, env.FeeBump != nil:
			// Neither form carries a transaction extension.
			data = nil
		}
	}) {
		return nil, false
	}
	if data == nil {
		t.Errorf("%s: this transaction carries no Soroban data.\n"+
			"Resource limits and the resource fee live in the Soroban transaction data, which only "+
			"a contract invocation has (was this transaction a contract call?)", assertion)
		return nil, false
	}
	return data, true
}

// describeSorobanResources renders the resource limits a transaction bids, so
// a failure shows what was asked for rather than only what was allowed.
func describeSorobanResources(r xdr.SorobanResources) string {
	return fmt.Sprintf("resource limits: %d CPU instructions, %d read bytes, %d write bytes",
		r.Instructions, r.DiskReadBytes, r.WriteBytes)
}

// eventMatches reports whether an event satisfies a matcher.
//
// spec is the contract's specification, when the matcher supplied one. It
// describes the payload; it does not change whether the payload matches.
func eventMatches(e xdr.ContractEvent, want Event, spec *specIndex) bool {
	return eventMatchesWith(e, want, newEventConfig(), spec)
}

// eventMatchesWith reports whether an event satisfies a matcher under the
// given options.
//
// The options only relax the comparison, so the matcher still decides what
// has to be there.
func eventMatchesWith(e xdr.ContractEvent, want Event, cfg *eventConfig, spec *specIndex) bool {
	if !cfg.ignoreContractID && want.ContractID != "" {
		if e.ContractId == nil {
			return false
		}
		id, err := contractIDString(*e.ContractId)
		if err != nil || id != want.ContractID {
			return false
		}
	}

	body, ok := e.Body.GetV0()
	if !ok {
		return false
	}

	if !cfg.ignoreTopics && want.Topics != nil {
		if len(want.Topics) != len(body.Topics) {
			return false
		}
		for i, wt := range want.Topics {
			if wt == nil {
				continue
			}
			wv, ok := scVal(wt)
			if !ok || !body.Topics[i].Equals(wv) {
				return false
			}
		}
	}

	if !cfg.ignoreData && want.Data != nil {
		return spec.dataMatches(spec.findEvent(body.Topics), body.Data, want.Data)
	}
	return true
}

// scVal converts an accepted value into an ScVal.
//
// A plain string is treated as a symbol, because a symbol topic is by far the
// most common thing a test writes, and requiring the base64 of
// ScVal{Sym:"transfer"} would make the common case unreadable.
func scVal(v Value) (xdr.ScVal, bool) {
	switch val := v.(type) {
	case xdr.ScVal:
		return val, true
	case *xdr.ScVal:
		if val == nil {
			return xdr.ScVal{}, false
		}
		return *val, true
	case string:
		// Try base64 XDR first; fall back to treating it as a symbol.
		decoded, err := coerce(val, "ScVal")
		if err == nil {
			if sv, ok := decoded.Raw.(*xdr.ScVal); ok {
				return *sv, true
			}
		}
		sym := xdr.ScSymbol(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}, true
	default:
		decoded, err := coerce(v, "ScVal")
		if err != nil {
			return xdr.ScVal{}, false
		}
		sv, ok := decoded.Raw.(*xdr.ScVal)
		if !ok {
			return xdr.ScVal{}, false
		}
		return *sv, true
	}
}

// toEvent converts an XDR event into the matcher-shaped form returned by
// Events.
func toEvent(e xdr.ContractEvent) Event {
	out := Event{}
	if e.ContractId != nil {
		if id, err := contractIDString(*e.ContractId); err == nil {
			out.ContractID = id
		}
	}
	if body, ok := e.Body.GetV0(); ok {
		for _, tpc := range body.Topics {
			out.Topics = append(out.Topics, tpc)
		}
		out.Data = body.Data
	}
	return out
}

// describeEvents renders the events actually present, so a non-match shows
// what was there instead of only what was missing.
//
// spec, when given, names the fields of the payloads and the topics.
func describeEvents(events []xdr.ContractEvent, spec *specIndex) string {
	if len(events) == 0 {
		return "the transaction emitted no contract events"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "events present (%d):\n", len(events))
	for i, e := range events {
		contract := "(no contract)"
		if e.ContractId != nil {
			if id, err := contractIDString(*e.ContractId); err == nil {
				contract = shorten(id)
			}
		}
		fmt.Fprintf(&b, "  [%d] %s", i, contract)

		if body, ok := e.Body.GetV0(); ok {
			topics := make([]string, 0, len(body.Topics))
			for n, tpc := range body.Topics {
				topics = append(topics, describeTopic(spec, body.Topics, n, tpc))
			}
			fmt.Fprintf(&b, "  topics[%s]  data %s",
				strings.Join(topics, ", "), describeEventData(spec, body.Topics, body.Data))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeEventFields renders an event's topics and payload on one line, so
// an event can be shown beside the event that was wanted.
func describeEventFields(body xdr.ContractEventV0) string {
	topics := make([]string, 0, len(body.Topics))
	for _, tpc := range body.Topics {
		topics = append(topics, describeScVal(tpc))
	}
	return fmt.Sprintf("topics[%s]  data %s", strings.Join(topics, ", "), describeScVal(body.Data))
}

// describeEventMismatch renders the event that deviated, with what was
// emitted beside what was wanted.
//
// The two are printed on adjacent lines so they can be read against each
// other: a failure names the index, and the reader should not have to hold
// two descriptions in their head to see which topic or payload differs.
func describeEventMismatch(index, total int, actual xdr.ContractEvent, want Event, cfg *eventConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "event %d of %d does not match\n", index, total)

	actualFields := "(the event body carries no V0 arm, so it has no topics or data)"
	body, hasBody := actual.Body.GetV0()
	if hasBody {
		actualFields = describeEventFields(body)
	}

	fmt.Fprintf(&b, "  actual:   %s\n", actualFields)
	fmt.Fprintf(&b, "  expected: %s\n", describeWantedFieldsWith(want, cfg, hasBody))

	if ignored := cfg.ignoredFields(); ignored != "" {
		fmt.Fprintf(&b, "  (%s ignored by option, so it did not have to match)\n", ignored)
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeTopic renders one topic, naming it when the specification says
// which parameter it is.
func describeTopic(spec *specIndex, topics []xdr.ScVal, index int, topic xdr.ScVal) string {
	rendered := describeScVal(topic)
	if spec == nil || spec.empty() {
		return rendered
	}
	event := spec.findEvent(topics)
	if event == nil {
		return rendered
	}
	names := spec.eventTopicNames(event, topics)
	if index >= len(names) || names[index] == "" {
		return rendered
	}
	return names[index] + "=" + rendered
}

// describeEventData renders an event's payload, naming its fields when the
// specification describes them.
//
// The XDR type is kept alongside the names, so the reader can see both what
// the contract called the parts and what the wire calls them.
func describeEventData(spec *specIndex, topics []xdr.ScVal, data xdr.ScVal) string {
	if spec == nil || spec.empty() {
		return describeScVal(data)
	}
	event := spec.findEvent(topics)
	if event == nil {
		return describeScVal(data)
	}
	rendered := spec.describeSpecValue(spec.eventDataType(event), data)
	return fmt.Sprintf("%s [%s]", rendered, data.Type.String())
}

// describeWantedEvent renders a matcher, naming which fields were wildcards
// so a failing match is diagnosable.
func describeWantedEvent(want Event) string {
	return describeWantedFields(want, newEventConfig())
}

// describeWantedFields renders the fields a matcher checks, naming the
// wildcards so a failing match is diagnosable.
//
// A field the options relaxed is left out, because it is not part of what
// the matcher is checking any more.
func describeWantedFields(want Event, cfg *eventConfig) string {
	return describeWantedFieldsWith(want, cfg, false)
}

// describeWantedFieldsWith renders the fields a matcher checks, alongside the
// event that was emitted.
//
// A field the matcher left open is shown as a wildcard when the emitted event
// has one, so the two sides of the comparison line up and a reader can see
// at a glance that the payload was not what failed.
func describeWantedFieldsWith(want Event, cfg *eventConfig, openBody bool) string {
	var parts []string
	if !cfg.ignoreContractID && want.ContractID != "" {
		parts = append(parts, "contract "+shorten(want.ContractID))
	}
	if !cfg.ignoreTopics {
		switch {
		case want.Topics != nil:
			topics := make([]string, 0, len(want.Topics))
			for _, tpc := range want.Topics {
				if tpc == nil {
					topics = append(topics, "*")
					continue
				}
				if sv, ok := scVal(tpc); ok {
					topics = append(topics, describeScVal(sv))
					continue
				}
				topics = append(topics, fmt.Sprintf("%v", tpc))
			}
			parts = append(parts, "topics["+strings.Join(topics, ", ")+"]")
		case openBody:
			parts = append(parts, "topics[*]")
		}
	}
	if !cfg.ignoreData {
		switch {
		case want.Data != nil:
			parts = append(parts, "data "+describeWantedData(want))
		case openBody:
			parts = append(parts, "data *")
		}
	}
	if len(parts) == 0 {
		return "any event"
	}
	return strings.Join(parts, "  ")
}

// describeWantedData renders the payload a matcher wants, with field names
// when the matcher supplied a specification.
func describeWantedData(want Event) string {
	spec := newSpecIndex(want.Spec)
	if spec.empty() {
		if sv, ok := scVal(want.Data); ok {
			return describeScVal(sv)
		}
		return fmt.Sprintf("%v", want.Data)
	}

	// The event the matcher is after is named by its first topic, which is
	// how a contract's events are named on the wire.
	var name string
	for _, topic := range want.Topics {
		if topic == nil {
			continue
		}
		if sv, ok := scVal(topic); ok {
			if sym, ok := sv.GetSym(); ok {
				name = string(sym)
				break
			}
		}
	}
	event := spec.event(name)
	if event == nil {
		if val, ok := scVal(want.Data); ok {
			return describeScVal(val)
		}
		return fmt.Sprintf("%v", want.Data)
	}
	return spec.describeWantedValue(spec.eventDataType(event), want.Data)
}

// describeSpecHint explains a mismatch that a specification would have
// explained.
//
// A payload of field names cannot be compared with the wire shape, so a
// matcher that gives one without a specification is asking a question the
// library cannot answer. Saying so is more useful than a diff against
// something that was never going to match.
func describeSpecHint(want Event) string {
	if want.Spec != nil {
		return ""
	}
	if _, named := want.Data.(map[string]any); !named {
		return ""
	}
	return "\n\nthe payload names fields, so it needs the contract's specification: " +
		"set Event.Spec to match by field name."
}

// describeEventDiffs names the fields that differ between the wanted
// payload and the payloads that are there.
//
// The event with the fewest differences is the one the matcher most likely
// meant, so its differences are the ones worth showing.
func describeEventDiffs(events []xdr.ContractEvent, want Event, spec *specIndex) string {
	if spec == nil || spec.empty() || want.Data == nil {
		return ""
	}

	var (
		best    []string
		bestSet bool
	)
	for _, e := range events {
		body, ok := e.Body.GetV0()
		if !ok {
			continue
		}
		event := spec.findEvent(body.Topics)
		if event == nil {
			continue
		}
		diffs := spec.specDiff(spec.eventDataType(event), want.Data, body.Data)
		if len(diffs) == 0 {
			continue
		}
		if !bestSet || len(diffs) < len(best) {
			best = diffs
			bestSet = true
		}
	}
	if !bestSet {
		return describeUnknownEvent(events, spec)
	}

	var b strings.Builder
	b.WriteString("\n\ndifferences from the closest event:")
	for _, diff := range best {
		b.WriteString("\n  ")
		b.WriteString(diff)
	}
	return b.String()
}

// describeUnknownEvent says that the specification does not describe the
// event that was emitted, which is a different problem from a payload that
// does not match.
func describeUnknownEvent(events []xdr.ContractEvent, spec *specIndex) string {
	for _, e := range events {
		body, ok := e.Body.GetV0()
		if !ok {
			continue
		}
		for _, topic := range body.Topics {
			sym, ok := topic.GetSym()
			if !ok {
				continue
			}
			if spec.event(string(sym)) != nil {
				continue
			}
			return fmt.Sprintf("\n\nthe contract specification does not describe the event %q", string(sym))
		}
	}
	return ""
}

// describeScVal renders an ScVal compactly for a one-line event listing.
func describeScVal(v xdr.ScVal) string {
	switch v.Type {
	case xdr.ScValTypeScvSymbol:
		if s, ok := v.GetSym(); ok {
			return fmt.Sprintf("%q", string(s))
		}
	case xdr.ScValTypeScvString:
		if s, ok := v.GetStr(); ok {
			return fmt.Sprintf("%q", string(s))
		}
	case xdr.ScValTypeScvU32:
		if n, ok := v.GetU32(); ok {
			return fmt.Sprintf("u32(%d)", n)
		}
	case xdr.ScValTypeScvI32:
		if n, ok := v.GetI32(); ok {
			return fmt.Sprintf("i32(%d)", n)
		}
	case xdr.ScValTypeScvU64:
		if n, ok := v.GetU64(); ok {
			return fmt.Sprintf("u64(%d)", n)
		}
	case xdr.ScValTypeScvI64:
		if n, ok := v.GetI64(); ok {
			return fmt.Sprintf("i64(%d)", n)
		}
	case xdr.ScValTypeScvBool:
		if b, ok := v.GetB(); ok {
			return fmt.Sprintf("%t", b)
		}
	case xdr.ScValTypeScvVoid:
		return "void"
	case xdr.ScValTypeScvAddress:
		if a, ok := v.GetAddress(); ok {
			if s, err := a.String(); err == nil {
				return shorten(s)
			}
		}
	case xdr.ScValTypeScvU128:
		if n, ok := v.GetU128(); ok {
			return fmt.Sprintf("u128(%s)", u128String(n))
		}
	case xdr.ScValTypeScvI128:
		if n, ok := v.GetI128(); ok {
			return fmt.Sprintf("i128(%s)", i128String(n))
		}
	case xdr.ScValTypeScvBytes:
		if b, ok := v.GetBytes(); ok {
			return describeBytes(b)
		}
	case xdr.ScValTypeScvVec:
		if vec, ok := v.GetVec(); ok {
			return describeScVec(vec)
		}
	case xdr.ScValTypeScvMap:
		if m, ok := v.GetMap(); ok {
			return describeScMap(m)
		}
	case xdr.ScValTypeScvContractInstance:
		return "contract instance"
	case xdr.ScValTypeScvLedgerKeyContractInstance:
		return "contract instance key"
	case xdr.ScValTypeScvLedgerKeyNonce:
		return "nonce key"
	case xdr.ScValTypeScvError:
		if e, ok := v.GetError(); ok {
			return describeScError(e)
		}
	case xdr.ScValTypeScvTimepoint:
		if t, ok := v.GetTimepoint(); ok {
			return fmt.Sprintf("timepoint(%d)", t)
		}
	case xdr.ScValTypeScvDuration:
		if d, ok := v.GetDuration(); ok {
			return fmt.Sprintf("duration(%d)", d)
		}
	}
	return strings.TrimPrefix(v.Type.String(), "ScValTypeScv")
}

// describeScVec renders a vector compactly, truncating long ones so a large
// payload does not dominate a failure message.
func describeScVec(vec *xdr.ScVec) string {
	if vec == nil || len(*vec) == 0 {
		return "vec[]"
	}
	const maxShown = 3
	parts := make([]string, 0, maxShown+1)
	for i, element := range *vec {
		if i == maxShown {
			parts = append(parts, fmt.Sprintf("… +%d", len(*vec)-maxShown))
			break
		}
		parts = append(parts, describeScVal(element))
	}
	return "vec[" + strings.Join(parts, ", ") + "]"
}

// describeScMap renders a map compactly, truncating long ones.
func describeScMap(m *xdr.ScMap) string {
	if m == nil || len(*m) == 0 {
		return "map{}"
	}
	const maxShown = 3
	parts := make([]string, 0, maxShown+1)
	for i, entry := range *m {
		if i == maxShown {
			parts = append(parts, fmt.Sprintf("… +%d", len(*m)-maxShown))
			break
		}
		parts = append(parts, fmt.Sprintf("%s: %s",
			describeScVal(entry.Key), describeScVal(entry.Val)))
	}
	return "map{" + strings.Join(parts, ", ") + "}"
}

// describeBytes renders a byte array as hex, truncated so a large payload does
// not dominate a failure message.
func describeBytes(b xdr.ScBytes) string {
	const maxShown = 8
	if len(b) == 0 {
		return "bytes(0x)"
	}
	if len(b) > maxShown {
		return fmt.Sprintf("bytes(0x%x…, %d bytes)", b[:maxShown], len(b))
	}
	return fmt.Sprintf("bytes(0x%x)", b)
}

// u128String renders the two-word unsigned representation as one decimal.
func u128String(parts xdr.UInt128Parts) string {
	value := new(big.Int).SetUint64(uint64(parts.Hi))
	value.Lsh(value, 64)
	value.Or(value, new(big.Int).SetUint64(uint64(parts.Lo)))
	return value.String()
}

// i128String renders the two-word signed representation as one decimal. Lsh on
// the signed high word sign-extends, so adding the low word gives the value.
func i128String(parts xdr.Int128Parts) string {
	value := new(big.Int).SetInt64(int64(parts.Hi))
	value.Lsh(value, 64)
	value.Add(value, new(big.Int).SetUint64(uint64(parts.Lo)))
	return value.String()
}

// shorten abbreviates a strkey for inline display, matching how lens and
// prism abbreviate addresses.
func shorten(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:5] + "…" + s[len(s)-4:]
}
