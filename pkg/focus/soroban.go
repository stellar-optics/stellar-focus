package focus

import (
	"fmt"
	"strings"

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
type Event struct {
	// ContractID is the strkey contract address. Empty matches any contract.
	ContractID string
	// Topics are the event topics, in order. Nil matches any topics. A nil
	// element matches any single topic, so []any{"transfer", nil} matches a
	// two-topic event whose first topic is the symbol "transfer".
	Topics []any
	// Data is the event payload. Nil matches any payload.
	Data any
}

// AssertEventEmitted checks that a transaction meta contains a matching
// contract event.
func AssertEventEmitted(t TB, meta Value, want Event) bool {
	t.Helper()

	events, ok := contractEvents(t, "AssertEventEmitted", meta)
	if !ok {
		return false
	}

	for _, e := range events {
		if eventMatches(e, want) {
			return true
		}
	}

	t.Errorf("AssertEventEmitted: no event matched %s\n\n%s",
		describeWantedEvent(want), indent(describeEvents(events), "  "))
	return false
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
			len(events), want, indent(describeEvents(events), "  "))
		return false
	}
	return true
}

// AssertNoEvents checks that a transaction emitted no contract events.
func AssertNoEvents(t TB, meta Value) bool {
	t.Helper()
	return AssertEventCount(t, meta, 0)
}

// AssertReturnValue checks the value a contract invocation returned.
func AssertReturnValue(t TB, meta Value, want Value) bool {
	t.Helper()

	soroban, ok := sorobanMeta(t, "AssertReturnValue", meta)
	if !ok {
		return false
	}

	wantVal, ok := scVal(want)
	if !ok {
		t.Errorf("AssertReturnValue: want value could not be read as an ScVal")
		return false
	}

	if soroban.ReturnValue.Equals(wantVal) {
		return true
	}

	t.Errorf("AssertReturnValue: contract returned %s, want %s",
		describeScVal(soroban.ReturnValue), describeScVal(wantVal))
	return false
}

// AssertContractTrapped checks that a transaction failed because the contract
// itself trapped, rather than being rejected by the protocol.
//
// This is the Soroban failure developers hit most, and distinguishing it from
// a malformed transaction is usually the first question worth answering.
func AssertContractTrapped(t TB, result Value) bool {
	t.Helper()

	s, ok := summarize(t, "AssertContractTrapped", result, "TransactionResult")
	if !ok {
		return false
	}
	for _, op := range s.Operations {
		if op.Result != nil && op.Result.Code == "invoke_host_function_trapped" {
			return true
		}
	}

	t.Errorf("AssertContractTrapped: no operation trapped\n%s", indent(explainOutcome(s), "  "))
	return false
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

// eventMatches reports whether an event satisfies a matcher.
func eventMatches(e xdr.ContractEvent, want Event) bool {
	if want.ContractID != "" {
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

	if want.Topics != nil {
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

	if want.Data != nil {
		wv, ok := scVal(want.Data)
		if !ok || !body.Data.Equals(wv) {
			return false
		}
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
func describeEvents(events []xdr.ContractEvent) string {
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
			for _, tpc := range body.Topics {
				topics = append(topics, describeScVal(tpc))
			}
			fmt.Fprintf(&b, "  topics[%s]  data %s", strings.Join(topics, ", "), describeScVal(body.Data))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeWantedEvent renders a matcher, naming which fields were wildcards
// so a failing match is diagnosable.
func describeWantedEvent(want Event) string {
	var parts []string
	if want.ContractID != "" {
		parts = append(parts, "contract "+shorten(want.ContractID))
	}
	if want.Topics != nil {
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
	}
	if want.Data != nil {
		if sv, ok := scVal(want.Data); ok {
			parts = append(parts, "data "+describeScVal(sv))
		}
	}
	if len(parts) == 0 {
		return "any event"
	}
	return strings.Join(parts, "  ")
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
	}
	return strings.TrimPrefix(v.Type.String(), "ScValTypeScv")
}

// shorten abbreviates a strkey for inline display, matching how lens and
// prism abbreviate addresses.
func shorten(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:5] + "…" + s[len(s)-4:]
}
