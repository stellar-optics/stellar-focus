package focus

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Diagnostic events are the protocol's own account of what a contract call
// did. Where a contract event is the contract speaking to the outside world,
// a diagnostic event is the host narrating the call: the frame it opened
// (`fn_call`), the frame it closed (`fn_return`), what the contract logged, and
// what went wrong. They are also the only place the events of a *failed* call
// survive, which is why they are worth asserting on even in a passing suite.
//
// Two properties of a stream are worth checking, and they are checked
// separately here:
//
//   - the order of the events, which is the call's own sequence — a stream
//     with the same events in a different order describes a different program;
//   - the ledger and close time the stream belongs to, which is the only time
//     coordinate the protocol gives a diagnostic event.
//
// Both assertions read nothing but the XDR they are handed. No clock is read
// and no network is touched, so the same meta gives the same answer every run.

// Kinds a diagnostic event is classified into.
//
// The protocol's own diagnostic events carry their kind as the first topic
// symbol, so `fn_call`, `log` and the rest are reported verbatim; everything
// else is classified by the event's type.
const (
	// DiagnosticFnCall opens a contract call frame. Its topics are the
	// contract and the function being called.
	DiagnosticFnCall = "fn_call"
	// DiagnosticFnReturn closes the frame its fn_call opened.
	DiagnosticFnReturn = "fn_return"
	// DiagnosticContract is a contract event carried in the diagnostic stream,
	// which is where the events of a failed call survive.
	DiagnosticContract = "contract"
	// DiagnosticError is the host saying the call died. Its payload is the
	// ScError the invocation failed with, which is the only place the reason
	// for a trap survives.
	DiagnosticError = "error"
	// DiagnosticSystem is a system event, emitted by the protocol rather than
	// by a contract.
	DiagnosticSystem = "system"
	// DiagnosticOther is a diagnostic event whose kind is not one of the
	// protocol's named ones.
	DiagnosticOther = "diagnostic"
)

// DiagnosticEvent is one event in a transaction's diagnostic stream, with the
// position and depth it was emitted at.
//
// It is the same shape as Event, so an event read out of a stream can be fed
// straight back into the event matchers. ContractID, Topics and Data carry the
// payload; Index, Depth, Kind and InSuccessfulContractCall carry the stream
// position that a diagnostic event has instead of a timestamp.
type DiagnosticEvent = Event

// DiagnosticEventMatch describes one diagnostic event a test expects to find,
// and the order in which it expects to find it.
//
// A zero field matches anything, as it does in Event. Listing every event of a
// stream pins the whole order; listing a few pins only their relative order,
// which is usually what a test about ordering means to say.
type DiagnosticEventMatch struct {
	// Kind matches DiagnosticEvent.Kind, case-insensitively. Empty matches any
	// kind.
	Kind string
	// ContractID is the strkey contract address. Empty matches any contract.
	ContractID string
	// Topics match the event's topics, in order. Nil matches any topics; a nil
	// element matches any single topic.
	Topics []any
	// Data matches the event payload. Nil matches any payload.
	Data any
	// Spec is the contract's specification: the ScSpecEntry list that names
	// its types and events. It is optional, and it changes only how the
	// payload is described, never whether it matches.
	Spec []xdr.ScSpecEntry
	// Call filters by whether the call that emitted the event succeeded.
	Call CallOutcome
}

// CallOutcome filters diagnostic events by the outcome of the call that
// emitted them. The zero value matches either.
type CallOutcome uint8

const (
	// CallOutcomeAny matches an event from a successful call and one from a
	// failed call alike.
	CallOutcomeAny CallOutcome = iota
	// CallOutcomeSucceeded matches only events from a call that completed.
	CallOutcomeSucceeded
	// CallOutcomeFailed matches only events that survived a call that failed —
	// which is the case diagnostic events exist for.
	CallOutcomeFailed
)

// DiagnosticTimeline is the ledger a diagnostic stream was emitted in, and
// that ledger's close time.
//
// A Soroban diagnostic event has no timestamp of its own: its XDR is a bool
// and a contract event. The only time coordinate the protocol records at all
// is the ledger the transaction was applied in, so a stream is placed in time
// by that ledger and its close time. Both are given rather than looked up:
// reading a clock or asking a network would make an assertion neither
// deterministic nor offline, and `focus capture` already records exactly these
// two values as fixture provenance.
type DiagnosticTimeline struct {
	// Ledger is the ledger sequence the transaction was applied in.
	Ledger uint32
	// ClosedAt is that ledger's close time.
	ClosedAt time.Time
}

// AssertDiagnosticEventOrder checks that a transaction's diagnostic events
// contain the wanted events, in the wanted order.
//
// Order is the point. A diagnostic stream is the protocol's record of what a
// contract call did as it did it, so the order carries meaning a set of events
// does not: `fn_call` opens a frame, the contract's own events, `log`s and
// `error`s happen inside it, `fn_return` closes it, and a nested call opens a
// frame of its own between the two. Two streams holding the same events in
// different orders describe different programs.
//
// The match is a subsequence rather than an equality: each wanted event is
// looked for at or after the position the previous one was found at, so a test
// that lists every event pins the whole stream and a test that lists two pins
// their relative order. Wanting nothing is trivially satisfied.
//
// A failure names which wanted event went unmatched, where the search had got
// to, and whether the same event is present earlier in the stream — the swap
// case, which is the one that actually happens — and then lists the events
// that were emitted, with their indexes and call depths.
func AssertDiagnosticEventOrder(t TB, meta Value, want []DiagnosticEventMatch) bool {
	t.Helper()
	const assertion = "AssertDiagnosticEventOrder"

	events, _, ok := diagnosticStream(t, assertion, meta)
	if !ok {
		return false
	}

	pos := 0
	for i, w := range want {
		found := indexOfDiagnosticEvent(events, w, pos)
		if found < 0 {
			t.Errorf("%s: %s\n\n%s", assertion,
				describeDiagnosticOrderFailure(i, want, pos, events),
				indent(describeDiagnosticStream(events)))
			return false
		}
		pos = found + 1
	}
	return true
}

// AssertDiagnosticEventTimeline checks that a transaction's diagnostic stream
// was emitted in the ledger a test expects.
//
// A diagnostic stream has no timestamp per event, so the question "when was
// this emitted" has exactly one answer the protocol can give: the ledger the
// transaction was applied in. That ledger is read from the meta itself — a
// transaction writes the entries it changes during the ledger it executes in,
// so their LastModifiedLedgerSeq is that ledger — and compared with the
// wanted one, which gives the stream its place in the chain and its close
// time.
//
// Both halves of the wanted timeline are required. They come from fixture
// provenance or a block explorer, never from a clock, so an incomplete
// timeline means the caller has not said what it is comparing against, and
// that is reported rather than guessed. A stream with no events at all, or a
// meta whose changes pin no ledger, is reported for the same reason: there is
// nothing to place.
func AssertDiagnosticEventTimeline(t TB, meta Value, want DiagnosticTimeline) bool {
	t.Helper()
	const assertion = "AssertDiagnosticEventTimeline"

	if want.Ledger == 0 || want.ClosedAt.IsZero() {
		t.Errorf("%s: the timeline is incomplete: ledger %d, closed %s.\n"+
			"A diagnostic event carries no timestamp of its own, so the ledger the stream was emitted "+
			"in and that ledger's close time have to be supplied, and both are required.",
			assertion, want.Ledger, describeCloseTime(want.ClosedAt))
		return false
	}

	events, m, ok := diagnosticStream(t, assertion, meta)
	if !ok {
		return false
	}
	if len(events) == 0 {
		t.Errorf("%s: the transaction emitted no diagnostic events, so there is no stream to place in %s",
			assertion, describeTimeline(want))
		return false
	}

	var pins []uint32
	if !guard(t, assertion, func() { pins = ledgerPins(m) }) {
		return false
	}
	if len(pins) == 0 {
		t.Errorf("%s: the meta pins no ledger, so the stream cannot be placed in %s.\n"+
			"A transaction meta pins its ledger with the LastModifiedLedgerSeq of the entries it "+
			"changed; a meta that changed no entry cannot say which ledger it came from.",
			assertion, describeTimeline(want))
		return false
	}

	if got := pins[len(pins)-1]; got != want.Ledger {
		t.Errorf("%s: the transaction was applied in ledger %d, want %s\n\n%s\n\n%s",
			assertion, got, describeTimeline(want),
			indent(describeLedgerPins(pins)),
			indent(describeDiagnosticStream(events)))
		return false
	}
	return true
}

// DiagnosticEvents returns the diagnostic events a transaction meta carries,
// as an escape hatch for checks this package does not provide. It reports a
// test failure and returns nil when the meta cannot be read.
func DiagnosticEvents(t TB, meta Value) []DiagnosticEvent {
	t.Helper()

	events, _, ok := diagnosticStream(t, "DiagnosticEvents", meta)
	if !ok {
		return nil
	}
	return events
}

// diagnosticStream decodes a transaction meta and returns its diagnostic
// events, decorated with the position, depth and description each one is
// reported with, along with the meta itself.
func diagnosticStream(t TB, assertion string, meta Value) ([]DiagnosticEvent, *xdr.TransactionMeta, bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "meta", meta, "TransactionMeta")
	if !ok {
		return nil, nil, false
	}
	m, ok := decoded.Raw.(*xdr.TransactionMeta)
	if !ok {
		t.Errorf("%s: value is a %s, want a TransactionMeta; diagnostic events are recorded in "+
			"transaction meta", assertion, decoded.Type)
		return nil, nil, false
	}
	if m.V < 3 {
		t.Errorf("%s: this transaction meta is version %d, which records no diagnostic events.\n"+
			"Diagnostic events are a Soroban feature and appear in the version 3 and version 4 metas "+
			"(was the transaction a contract invocation?)", assertion, m.V)
		return nil, nil, false
	}

	var (
		events []DiagnosticEvent
		err    error
	)
	if !guard(t, assertion, func() {
		raw, streamErr := m.GetDiagnosticEvents()
		if streamErr != nil {
			err = streamErr
			return
		}
		events = decorateDiagnostics(raw)
	}) {
		return nil, nil, false
	}
	if err != nil {
		t.Errorf("%s: reading the diagnostic events: %v", assertion, err)
		return nil, nil, false
	}
	return events, m, true
}

// decorateDiagnostics turns raw diagnostic events into the form the
// assertions read. It calls generated accessors and renders descriptions, so
// it must run behind guard.
func decorateDiagnostics(raw []xdr.DiagnosticEvent) []DiagnosticEvent {
	out := make([]DiagnosticEvent, 0, len(raw))
	depth := 0
	for i, de := range raw {
		ev := DiagnosticEvent{
			Index:                    i,
			Kind:                     diagnosticKind(de.Event),
			InSuccessfulContractCall: de.InSuccessfulContractCall,
			event:                    de.Event,
		}
		if de.Event.ContractId != nil {
			if id, err := contractIDString(*de.Event.ContractId); err == nil {
				ev.ContractID = id
			}
		}

		// A fn_return closes the frame its fn_call opened, so the pair shares a
		// depth. The clamp keeps an unmatched return — which a truncated or
		// hand-built stream can hold — from dragging every later event into a
		// frame that does not enclose it.
		if ev.Kind == DiagnosticFnReturn && depth > 0 {
			depth--
		}
		ev.Depth = depth
		if ev.Kind == DiagnosticFnCall {
			depth++
		}

		body, hasBody := de.Event.Body.GetV0()
		if hasBody {
			if len(body.Topics) > 0 {
				ev.Topics = make([]any, len(body.Topics))
				for i, topic := range body.Topics {
					ev.Topics[i] = topic
				}
			}
			ev.Data = body.Data
		}
		ev.detail = describeDiagnosticEvent(ev, hasBody)
		out = append(out, ev)
	}
	return out
}

// diagnosticKind classifies an event. The protocol's own diagnostic events
// carry their kind as the first topic symbol, so those are reported verbatim;
// everything else is classified by the event's type.
func diagnosticKind(e xdr.ContractEvent) string {
	switch e.Type {
	case xdr.ContractEventTypeContract:
		return DiagnosticContract
	case xdr.ContractEventTypeSystem:
		return DiagnosticSystem
	case xdr.ContractEventTypeDiagnostic:
		body, ok := e.Body.GetV0()
		if !ok || len(body.Topics) == 0 {
			return DiagnosticOther
		}
		if sym, ok := body.Topics[0].GetSym(); ok {
			return string(sym)
		}
		return DiagnosticOther
	default:
		return fmt.Sprintf("event_type_%d", e.Type)
	}
}

// diagnosticEventMatches reports whether an event satisfies a matcher.
func diagnosticEventMatches(e DiagnosticEvent, w DiagnosticEventMatch) bool {
	if w.Kind != "" && !strings.EqualFold(w.Kind, e.Kind) {
		return false
	}
	switch w.Call {
	case CallOutcomeSucceeded:
		if !e.InSuccessfulContractCall {
			return false
		}
	case CallOutcomeFailed:
		if e.InSuccessfulContractCall {
			return false
		}
	default:
		// CallOutcomeAny, and any value outside the defined set, filters
		// nothing.
	}
	return eventMatches(e.event, Event{
		ContractID: w.ContractID,
		Topics:     w.Topics,
		Data:       w.Data,
		Spec:       w.Spec,
	}, newSpecIndex(w.Spec))
}

// indexOfDiagnosticEvent returns the position of the first event at or after
// from that satisfies the matcher, or -1.
func indexOfDiagnosticEvent(events []DiagnosticEvent, w DiagnosticEventMatch, from int) int {
	for i := from; i < len(events); i++ {
		if diagnosticEventMatches(events[i], w) {
			return i
		}
	}
	return -1
}

// describeDiagnosticOrderFailure explains why a wanted sequence did not match,
// naming the wanted event that went unmatched, where the search had reached,
// and whether the same event is present earlier in the stream. That last case
// is the one a sequence assertion exists to catch: the events are all there,
// in the wrong order.
func describeDiagnosticOrderFailure(i int, want []DiagnosticEventMatch, pos int, events []DiagnosticEvent) string {
	w := want[i]
	if earlier := indexOfDiagnosticEvent(events, w, 0); earlier >= 0 {
		return fmt.Sprintf("the diagnostic events are in the wrong order: want[%d] (%s) is present "+
			"at stream index %d, but the events wanted before it place it at or after index %d",
			i, describeDiagnosticMatch(w), earlier, pos)
	}
	return fmt.Sprintf("no event at or after stream index %d matched want[%d] of %d (%s); "+
		"%d of the %d wanted events matched, in order",
		pos, i, len(want), describeDiagnosticMatch(w), i, len(want))
}

// describeDiagnosticStream lists the events that were emitted, so a failure
// shows what is there rather than only what was wanted.
func describeDiagnosticStream(events []DiagnosticEvent) string {
	if len(events) == 0 {
		return "the transaction emitted no diagnostic events"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "diagnostic events present (%d):\n", len(events))
	for _, e := range events {
		fmt.Fprintf(&b, "  %s\n", e.detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeDiagnosticEvent renders one event as a single line: its position,
// kind and call depth first, because those are what an ordering question is
// about, then the contract and payload that identify it.
func describeDiagnosticEvent(e DiagnosticEvent, hasBody bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%d] %-9s depth %d", e.Index, e.Kind, e.Depth)
	if e.ContractID != "" {
		fmt.Fprintf(&b, "  contract %s", shorten(e.ContractID))
	}
	if body, ok := e.event.Body.GetV0(); hasBody && ok {
		if len(body.Topics) > 0 {
			topics := make([]string, 0, len(body.Topics))
			for _, topic := range body.Topics {
				topics = append(topics, describeScVal(topic))
			}
			fmt.Fprintf(&b, "  topics[%s]", strings.Join(topics, ", "))
		}
		fmt.Fprintf(&b, "  data %s", describeScVal(body.Data))
	} else {
		b.WriteString("  (the event body carries no V0 arm, so it has no topics or data)")
	}
	if !e.InSuccessfulContractCall {
		b.WriteString("  (call failed)")
	}
	return b.String()
}

// describeDiagnosticMatch renders a matcher, naming the wildcards so a failing
// match is diagnosable.
func describeDiagnosticMatch(w DiagnosticEventMatch) string {
	var parts []string
	if w.Kind != "" {
		parts = append(parts, w.Kind)
	} else {
		parts = append(parts, "any kind")
	}
	if w.ContractID != "" {
		parts = append(parts, "contract "+shorten(w.ContractID))
	}
	if w.Topics != nil {
		topics := make([]string, 0, len(w.Topics))
		for _, topic := range w.Topics {
			if topic == nil {
				topics = append(topics, "*")
				continue
			}
			if sv, ok := scVal(topic); ok {
				topics = append(topics, describeScVal(sv))
				continue
			}
			topics = append(topics, fmt.Sprintf("%v", topic))
		}
		parts = append(parts, "topics["+strings.Join(topics, ", ")+"]")
	}
	if w.Data != nil {
		if sv, ok := scVal(w.Data); ok {
			parts = append(parts, "data "+describeScVal(sv))
		}
	}
	switch w.Call {
	case CallOutcomeSucceeded:
		parts = append(parts, "in a call that succeeded")
	case CallOutcomeFailed:
		parts = append(parts, "in a call that failed")
	default:
	}
	return strings.Join(parts, "  ")
}

// ledgerPins returns the ledgers the transaction's ledger changes were last
// modified in, de-duplicated and sorted.
//
// The newest pin is the ledger the transaction was applied in: the entries a
// transaction writes are written during the ledger it executes in, so their
// LastModifiedLedgerSeq is that ledger. An entry restored out of the Soroban
// archival state can keep an older last-modified ledger, which is why the
// newest pin is the one that answers the question rather than all of them
// having to agree.
func ledgerPins(m *xdr.TransactionMeta) []uint32 {
	seen := make(map[uint32]bool)
	var pins []uint32
	for _, ch := range metaChanges(m) {
		entry := changeEntry(ch)
		if entry == nil {
			continue
		}
		ledger := uint32(entry.LastModifiedLedgerSeq)
		if ledger == 0 || seen[ledger] {
			continue
		}
		seen[ledger] = true
		pins = append(pins, ledger)
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i] < pins[j] })
	return pins
}

// changeEntry returns the ledger entry a change carries, or nil for a removal,
// which carries only the key of the entry that went away.
func changeEntry(ch xdr.LedgerEntryChange) *xdr.LedgerEntry {
	switch ch.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return ch.Created
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return ch.Updated
	case xdr.LedgerEntryChangeTypeLedgerEntryState:
		return ch.State
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		return ch.Restored
	default:
		return nil
	}
}

// describeLedgerPins renders the ledgers pinned by a meta's changes.
func describeLedgerPins(pins []uint32) string {
	parts := make([]string, len(pins))
	for i, p := range pins {
		parts[i] = fmt.Sprintf("%d", p)
	}
	return fmt.Sprintf("ledger pins (%d): %s\n"+
		"the newest pin is the ledger the transaction was applied in; an older one can come from an "+
		"entry restored out of the archival state",
		len(pins), strings.Join(parts, ", "))
}

// describeTimeline renders a wanted timeline for a failure message.
func describeTimeline(want DiagnosticTimeline) string {
	return fmt.Sprintf("ledger %d, closed %s", want.Ledger, describeCloseTime(want.ClosedAt))
}

// describeCloseTime renders a close time in the form the rest of the toolkit
// prints chain times, and says so plainly when there is none.
func describeCloseTime(t time.Time) string {
	if t.IsZero() {
		return "(no close time)"
	}
	return t.UTC().Format(time.RFC3339)
}

// AssertContractLogMessage checks that a transaction's diagnostic events contain
// a log message matching pattern.
//
// pattern can be a literal substring, or a regular expression enclosed in slashes
// (e.g. "/user \d+ deposited/").
//
// When no match is found, failure output shows all captured diagnostic log events.
func AssertContractLogMessage(t TestingT, metaInput any, pattern string, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertContractLogMessage"
	_ = newConfig(opts...)

	var (
		re       *regexp.Regexp
		isRegex  bool
		regexErr error
	)
	if len(pattern) >= 2 && strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/") {
		isRegex = true
		re, regexErr = regexp.Compile(pattern[1 : len(pattern)-1])
		if regexErr != nil {
			t.Errorf("%s: invalid regular expression %s: %v", assertion, pattern, regexErr)
			return false
		}
	}

	matchStr := func(s string) bool {
		if isRegex {
			return re.MatchString(s)
		}
		return strings.Contains(s, pattern)
	}

	events, _, ok := diagnosticStream(t, assertion, metaInput)
	if !ok {
		return false
	}

	for _, e := range events {
		if matchStr(e.detail) {
			return true
		}
		if body, hasBody := e.event.Body.GetV0(); hasBody {
			for _, topic := range body.Topics {
				for _, str := range extractScValStrings(topic) {
					if matchStr(str) {
						return true
					}
				}
				if matchStr(describeScVal(topic)) {
					return true
				}
			}
			for _, str := range extractScValStrings(body.Data) {
				if matchStr(str) {
					return true
				}
			}
			if matchStr(describeScVal(body.Data)) {
				return true
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: no diagnostic log message matched %q\n\n", assertion, pattern)
	if len(events) == 0 {
		b.WriteString("  the transaction emitted no diagnostic events")
	} else {
		fmt.Fprintf(&b, "  captured diagnostic event(s) (%d):\n%s",
			len(events), indent(describeDiagnosticStream(events)))
	}
	t.Errorf("%s", b.String())
	return false
}

func extractScValStrings(v xdr.ScVal) []string {
	var res []string
	switch v.Type {
	case xdr.ScValTypeScvString:
		if s, ok := v.GetStr(); ok {
			res = append(res, string(s))
		}
	case xdr.ScValTypeScvSymbol:
		if s, ok := v.GetSym(); ok {
			res = append(res, string(s))
		}
	case xdr.ScValTypeScvBytes:
		if b, ok := v.GetBytes(); ok {
			res = append(res, string(b))
		}
	case xdr.ScValTypeScvVec:
		if vec, ok := v.GetVec(); ok && vec != nil {
			for _, item := range *vec {
				res = append(res, extractScValStrings(item)...)
			}
		}
	case xdr.ScValTypeScvMap:
		if m, ok := v.GetMap(); ok && m != nil {
			for _, entry := range *m {
				res = append(res, extractScValStrings(entry.Key)...)
				res = append(res, extractScValStrings(entry.Val)...)
			}
		}
	}
	return res
}
