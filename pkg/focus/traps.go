package focus

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	rpcproto "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// A trap is the one Soroban failure a test can hardly work around: the host
// aborted the invocation and the contract never got to return, so the only
// account of what happened is the host's own diagnostic stream. That stream
// lives in the transaction meta — or, in a Soroban RPC getTransaction
// response, beside the result — and this file reads it.
//
// Everything here is offline: a meta or a response is data the caller already
// holds, and the assertion never asks a node, reads a clock, or decodes a
// contract spec.

// TrapEvidence pairs a trapped transaction's result with the meta that records
// the host's diagnostics.
//
// Two forms of evidence are needed in practice. A Soroban RPC getTransaction
// response carries both halves in one value and is accepted directly by
// AssertContractTrappedWith; a captured fixture set keeps the result and the
// meta in separate files, and TrapEvidence is how those two are handed over
// together. Both are read from memory, so the assertion stays offline.
type TrapEvidence struct {
	// Result is the transaction result, in any form a focus assertion accepts.
	// It records that the invocation trapped but says nothing about why.
	Result Value
	// Meta is the transaction meta, in any form a focus assertion accepts. Its
	// diagnostic events are the host's account of the call.
	Meta Value
}

// AssertContractTrappedWith checks that the transaction failed because the
// contract trapped, and that the host's diagnostics explain why.
//
// AssertContractTrapped answers the first half from a transaction result: an
// operation result of invoke_host_function_trapped. The reason lives
// somewhere else. The protocol puts it in the transaction meta's diagnostic
// stream, as an error event whose payload is an ScError — Error(Contract, #4)
// for a contract's own panic code, Error(WasmVm, …) for a VM fault, and so on.
// This assertion reads that stream and requires one of its rendered events to
// contain expectedDiagnosticSubstring.
//
// result accepts either:
//
//	protocol.GetTransactionResponse   the Soroban RPC getTransaction response,
//	                                  whose ResultXDR names the trap and whose
//	                                  DiagnosticEventsXDR carries the stream;
//	TrapEvidence                      a result paired with the meta that holds
//	                                  the stream, for fixture-based tests.
//
// A bare TransactionResult is rejected with an explanation rather than
// passed: it proves the trap but carries no diagnostics, and an assertion
// that cannot make the check it was asked for must say so instead of
// returning true. The empty substring is rejected for the same reason — it
// would match every stream and quietly weaken the test.
//
// The substring is matched case-sensitively against the same one-line
// rendering the failure message lists, so what the assertion searched for and
// what it shows can never disagree. A match that differs only in case is
// named in the failure, because that is usually a capitalisation mistake
// rather than a missing diagnostic.
func AssertContractTrappedWith(t TB, result Value, expectedDiagnosticSubstring string) bool {
	t.Helper()
	const assertion = "AssertContractTrappedWith"

	if strings.TrimSpace(expectedDiagnosticSubstring) == "" {
		t.Errorf("%s: the expected diagnostic substring is blank, so it would match any stream.\n"+
			"Name the host error the contract is expected to hit, such as %q.",
			assertion, "Error(Contract, #4)")
		return false
	}

	resultValue, events, ok := trapEvidence(t, assertion, result)
	if !ok {
		return false
	}

	// The trap is checked before the diagnostics: a missing host message on a
	// transaction that never trapped would point at the wrong problem.
	if !contractTrapped(t, assertion, resultValue) {
		return false
	}

	if len(events) == 0 {
		t.Errorf("%s: the invocation trapped, but its diagnostics record no events, "+
			"so there is nothing to search for %q.", assertion, expectedDiagnosticSubstring)
		return false
	}

	for _, e := range events {
		if strings.Contains(e.detail, expectedDiagnosticSubstring) {
			return true
		}
	}

	t.Errorf("%s: the invocation trapped, but no host diagnostic contains %q\n\n%s",
		assertion, expectedDiagnosticSubstring,
		indent(describeDiagnosticMiss(events, expectedDiagnosticSubstring)))
	return false
}

// contractTrapped reports whether any operation result trapped, and explains
// the outcome when none did. It is the shared half of AssertContractTrapped
// and AssertContractTrappedWith, so both answer "did it trap" identically.
func contractTrapped(t TB, assertion string, result Value) bool {
	t.Helper()

	s, ok := summarize(t, assertion, result, "TransactionResult")
	if !ok {
		return false
	}
	for _, op := range s.Operations {
		if op.Result != nil && op.Result.Code == "invoke_host_function_trapped" {
			return true
		}
	}

	t.Errorf("%s: no operation trapped\n%s", assertion, indent(explainOutcome(s)))
	return false
}

// trapEvidence resolves the transaction result and the diagnostic events the
// host recorded for it.
//
// The switch is the abstraction seam: another carrier — a Horizon response, a
// bundle record — is one more case here, and the assertions above never need
// to know which one was used.
func trapEvidence(t TB, assertion string, result Value) (Value, []DiagnosticEvent, bool) {
	t.Helper()

	switch r := result.(type) {
	case rpcproto.GetTransactionResponse:
		return trapEvidenceFromResponse(t, assertion, &r)

	case *rpcproto.GetTransactionResponse:
		if r == nil {
			t.Errorf("%s: result is a nil *protocol.GetTransactionResponse", assertion)
			return nil, nil, false
		}
		return trapEvidenceFromResponse(t, assertion, r)

	case TrapEvidence:
		return trapEvidenceFromFields(t, assertion, r)

	case *TrapEvidence:
		if r == nil {
			t.Errorf("%s: result is a nil *TrapEvidence", assertion)
			return nil, nil, false
		}
		return trapEvidenceFromFields(t, assertion, *r)

	default:
		decoded, err := coerce(result, "TransactionResult")
		if err != nil {
			t.Errorf("%s: result value could not be read: %v\n"+
				"Pass the transaction result with the meta that records the host's diagnostics, "+
				"as TrapEvidence{Result: …, Meta: …}, or the Soroban RPC getTransaction response.",
				assertion, err)
			return nil, nil, false
		}
		t.Errorf("%s: a %s records that an operation trapped but carries no host diagnostics.\n"+
			"Diagnostics live in the transaction meta, so pass TrapEvidence{Result: …, Meta: …}, "+
			"or the getTransaction response whose diagnosticEventsXdr carries them.",
			assertion, decoded.Type)
		return nil, nil, false
	}
}

// trapEvidenceFromResponse reads a Soroban RPC getTransaction response: the
// result from ResultXDR, the host's account from DiagnosticEventsXDR.
func trapEvidenceFromResponse(
	t TB, assertion string, resp *rpcproto.GetTransactionResponse,
) (Value, []DiagnosticEvent, bool) {
	t.Helper()

	if resp.ResultXDR == "" {
		t.Errorf("%s: the transaction response carries no result (status %q), "+
			"so there is no outcome to check for a trap", assertion, resp.Status)
		return nil, nil, false
	}
	if len(resp.DiagnosticEventsXDR) == 0 {
		t.Errorf("%s: the transaction response carries no diagnostic events, so there is "+
			"nothing to search for the expected host message.\n"+
			"A Soroban RPC reports a failed contract call's stream in diagnosticEventsXdr; "+
			"without it this check cannot be made.", assertion)
		return nil, nil, false
	}

	var (
		events []DiagnosticEvent
		err    error
	)
	if !guard(t, assertion, func() {
		var raw []xdr.DiagnosticEvent
		raw, err = decodeDiagnosticEvents(resp.DiagnosticEventsXDR)
		if err != nil {
			return
		}
		events = decorateDiagnostics(raw)
	}) {
		return nil, nil, false
	}
	if err != nil {
		t.Errorf("%s: reading the response's diagnostic events: %v", assertion, err)
		return nil, nil, false
	}
	return resp.ResultXDR, events, true
}

// trapEvidenceFromFields reads TrapEvidence, taking the stream from the meta
// through the same seam the diagnostic stream assertions use.
func trapEvidenceFromFields(
	t TB, assertion string, ev TrapEvidence,
) (Value, []DiagnosticEvent, bool) {
	t.Helper()

	if ev.Result == nil {
		t.Errorf("%s: TrapEvidence carries no result, so there is no outcome to check for a trap. "+
			"Pass the transaction result captured with the meta.", assertion)
		return nil, nil, false
	}
	if ev.Meta == nil {
		t.Errorf("%s: TrapEvidence carries no meta, and host diagnostics live in transaction meta. "+
			"Pass the meta captured with the result.", assertion)
		return nil, nil, false
	}

	events, _, ok := diagnosticStream(t, assertion, ev.Meta)
	if !ok {
		return nil, nil, false
	}
	return ev.Result, events, true
}

// decodeDiagnosticEvents decodes the RPC's flattened diagnosticEventsXdr: one
// base64 DiagnosticEvent per element, in stream order.
func decodeDiagnosticEvents(payloads []string) ([]xdr.DiagnosticEvent, error) {
	out := make([]xdr.DiagnosticEvent, 0, len(payloads))
	for i, payload := range payloads {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
		if err != nil {
			return nil, fmt.Errorf("diagnostic event %d is not valid base64: %w", i, err)
		}
		var de xdr.DiagnosticEvent
		if err := de.UnmarshalBinary(raw); err != nil {
			return nil, fmt.Errorf("diagnostic event %d is not a DiagnosticEvent: %w", i, err)
		}
		out = append(out, de)
	}
	return out, nil
}

// describeDiagnosticMiss lists the events that were searched and names a
// case-insensitive match when one exists, which is the mistake a
// case-sensitive comparison actually produces.
func describeDiagnosticMiss(events []DiagnosticEvent, want string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "host diagnostics searched (%d event(s), matched case-sensitively):\n", len(events))
	for _, e := range events {
		fmt.Fprintf(&b, "  %s\n", e.detail)
	}
	if nearMissCase(events, want) {
		fmt.Fprintf(&b, "\n  The same text is present with different capitalisation; "+
			"the comparison is case-sensitive.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// nearMissCase reports whether any event contains want ignoring case.
func nearMissCase(events []DiagnosticEvent, want string) bool {
	lowered := strings.ToLower(want)
	for _, e := range events {
		if strings.Contains(strings.ToLower(e.detail), lowered) {
			return true
		}
	}
	return false
}

// describeScError renders a host error the way the SDK and the Stellar CLI
// print it: the error's type, then either the contract's own numeric code or
// the host's named code. A contract error reads Error(Contract, #4), a VM
// fault Error(WasmVm, InvalidAction).
func describeScError(e xdr.ScError) string {
	name := strings.TrimPrefix(e.Type.String(), "ScErrorTypeSce")
	if name == "" {
		// A type outside the enum is a malformed value rather than an error
		// anyone can act on, so it is named by number and never left blank.
		return fmt.Sprintf("Error(type_%d)", int32(e.Type))
	}
	// The union's arms are read directly rather than through the generated
	// accessors: those dereference the arm without checking it, which panics on
	// a malformed value instead of rendering it.
	if e.Type == xdr.ScErrorTypeSceContract {
		if e.ContractCode != nil {
			return fmt.Sprintf("Error(%s, #%d)", name, *e.ContractCode)
		}
		return fmt.Sprintf("Error(%s)", name)
	}
	if e.Code != nil {
		codeName := strings.TrimPrefix(e.Code.String(), "ScErrorCodeScec")
		if codeName == "" {
			codeName = fmt.Sprintf("code_%d", int32(*e.Code))
		}
		return fmt.Sprintf("Error(%s, %s)", name, codeName)
	}
	return fmt.Sprintf("Error(%s)", name)
}

// AssertContractPanicReason checks that the transaction failed with a contract trap,
// and that the host diagnostic events record the expected panic reason.
//
// resultOrMeta accepts:
//   - protocol.GetTransactionResponse (or pointer to it)
//   - TrapEvidence (or pointer to it)
//   - TransactionMeta (in any form accepted by focus: xdr.TransactionMeta, *xdr.TransactionMeta, base64, bytes, *lens.Value)
//
// wantReason accepts:
//   - uint32 / int / numeric (contract error code, e.g. 101 -> Error(Contract, #101))
//   - xdr.ScError (exact host error structure)
//   - string (error symbol name or expected diagnostic substring)
func AssertContractPanicReason(t TestingT, resultOrMeta any, wantReason any, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertContractPanicReason"
	_ = newConfig(opts...)

	events, rawMeta, ok := extractTrapEvents(t, resultOrMeta)
	if !ok {
		return false
	}

	matched, wantDesc, actualDesc := matchPanicReason(wantReason, events, rawMeta)
	if matched {
		return true
	}

	t.Errorf("%s: contract trapped with different reason: want %s, got %s\n\n%s",
		assertion, wantDesc, actualDesc,
		indent(describeDiagnosticMiss(events, wantDesc)))
	return false
}

func extractTrapEvents(t TB, resultOrMeta any) ([]DiagnosticEvent, *xdr.TransactionMeta, bool) {
	t.Helper()
	const assertion = "AssertContractPanicReason"

	switch r := resultOrMeta.(type) {
	case rpcproto.GetTransactionResponse:
		return extractTrapEventsFromResponse(t, &r)
	case *rpcproto.GetTransactionResponse:
		if r == nil {
			t.Errorf("%s: resultOrMeta is a nil *protocol.GetTransactionResponse", assertion)
			return nil, nil, false
		}
		return extractTrapEventsFromResponse(t, r)
	case TrapEvidence:
		return extractTrapEventsFromEvidence(t, r)
	case *TrapEvidence:
		if r == nil {
			t.Errorf("%s: resultOrMeta is a nil *TrapEvidence", assertion)
			return nil, nil, false
		}
		return extractTrapEventsFromEvidence(t, *r)
	default:
		// Attempt to read as TransactionMeta directly
		events, rawMeta, ok := diagnosticStream(t, assertion, resultOrMeta)
		if !ok {
			return nil, nil, false
		}
		// Check whether a trap/error is recorded
		if !metaIndicatesTrap(events, rawMeta) {
			t.Errorf("%s: transaction succeeded; no contract trap occurred", assertion)
			return nil, nil, false
		}
		return events, rawMeta, true
	}
}

func extractTrapEventsFromResponse(t TB, resp *rpcproto.GetTransactionResponse) ([]DiagnosticEvent, *xdr.TransactionMeta, bool) {
	t.Helper()
	const assertion = "AssertContractPanicReason"
	if resp.Status == "SUCCESS" || (resp.ResultXDR != "" && !isResultTrapped(resp.ResultXDR)) {
		t.Errorf("%s: transaction succeeded; no contract trap occurred", assertion)
		return nil, nil, false
	}
	if len(resp.DiagnosticEventsXDR) == 0 {
		t.Errorf("%s: the transaction response carries no diagnostic events", assertion)
		return nil, nil, false
	}
	var raw []xdr.DiagnosticEvent
	var err error
	if !guard(t, assertion, func() {
		raw, err = decodeDiagnosticEvents(resp.DiagnosticEventsXDR)
	}) {
		return nil, nil, false
	}
	if err != nil {
		t.Errorf("%s: decoding diagnostic events: %v", assertion, err)
		return nil, nil, false
	}
	return decorateDiagnostics(raw), nil, true
}

func extractTrapEventsFromEvidence(t TB, ev TrapEvidence) ([]DiagnosticEvent, *xdr.TransactionMeta, bool) {
	t.Helper()
	const assertion = "AssertContractPanicReason"
	if ev.Result != nil && !isResultTrapped(ev.Result) {
		t.Errorf("%s: transaction succeeded; no contract trap occurred", assertion)
		return nil, nil, false
	}
	if ev.Meta == nil {
		t.Errorf("%s: TrapEvidence carries no meta", assertion)
		return nil, nil, false
	}
	events, rawMeta, ok := diagnosticStream(t, assertion, ev.Meta)
	if !ok {
		return nil, nil, false
	}
	if len(events) == 0 {
		t.Errorf("%s: the transaction meta carries no diagnostic events", assertion)
		return nil, nil, false
	}
	return events, rawMeta, true
}

func isResultTrapped(res Value) bool {
	decoded, err := coerce(res, "TransactionResult")
	if err != nil {
		return false
	}
	s, err := lens.Explain(decoded)
	if err != nil || s == nil {
		return false
	}
	for _, op := range s.Operations {
		if op.Result != nil && op.Result.Code == "invoke_host_function_trapped" {
			return true
		}
	}
	return false
}

func metaIndicatesTrap(events []DiagnosticEvent, rawMeta *xdr.TransactionMeta) bool {
	if rawMeta != nil {
		if retVal, ok := metaReturnValue(rawMeta); ok {
			if _, isErr := scValError(retVal); isErr {
				return true
			}
		}
	}
	for _, e := range events {
		if e.Kind == DiagnosticError || !e.InSuccessfulContractCall {
			return true
		}
		if data, ok := eventData(e); ok {
			if _, isErr := scValError(data); isErr {
				return true
			}
		}
	}
	return false
}

func toUint32(v any) (uint32, bool) {
	switch val := v.(type) {
	case uint32:
		return val, true
	case int:
		if val >= 0 {
			return uint32(val), true
		}
	case int32:
		if val >= 0 {
			return uint32(val), true
		}
	case int64:
		if val >= 0 {
			return uint32(val), true
		}
	case uint:
		return uint32(val), true
	case uint64:
		return uint32(val), true
	}
	return 0, false
}

func matchPanicReason(wantReason any, events []DiagnosticEvent, rawMeta *xdr.TransactionMeta) (matched bool, wantDesc string, actualDesc string) {
	var actualReasons []string

	// Collect any actual errors from events and rawMeta
	collectActual := func(e xdr.ScError) {
		desc := describeScError(e)
		for _, a := range actualReasons {
			if a == desc {
				return
			}
		}
		actualReasons = append(actualReasons, desc)
	}

	for _, e := range events {
		if data, ok := eventData(e); ok {
			if scErr, ok := scValError(data); ok {
				collectActual(scErr)
			}
		}
	}
	if rawMeta != nil {
		if retVal, ok := metaReturnValue(rawMeta); ok {
			if scErr, ok := scValError(retVal); ok {
				collectActual(scErr)
			}
		}
	}

	formatActual := func() string {
		if len(actualReasons) == 0 {
			return "none"
		}
		return strings.Join(actualReasons, ", ")
	}

	if code, ok := toUint32(wantReason); ok {
		wantDesc = fmt.Sprintf("Error(Contract, #%d)", code)
		for _, e := range events {
			if data, ok := eventData(e); ok {
				if scErr, ok := scValError(data); ok {
					if scErr.Type == xdr.ScErrorTypeSceContract && scErr.ContractCode != nil && uint32(*scErr.ContractCode) == code {
						return true, wantDesc, wantDesc
					}
				}
			}
			if strings.Contains(e.detail, fmt.Sprintf("Error(Contract, #%d)", code)) {
				return true, wantDesc, wantDesc
			}
		}
		if rawMeta != nil {
			if retVal, ok := metaReturnValue(rawMeta); ok {
				if scErr, ok := scValError(retVal); ok {
					if scErr.Type == xdr.ScErrorTypeSceContract && scErr.ContractCode != nil && uint32(*scErr.ContractCode) == code {
						return true, wantDesc, wantDesc
					}
				}
			}
		}
		return false, wantDesc, formatActual()
	}

	switch w := wantReason.(type) {
	case xdr.ScError:
		wantDesc = describeScError(w)
		for _, e := range events {
			if data, ok := eventData(e); ok {
				if scErr, ok := scValError(data); ok {
					if scErr.Type == w.Type {
						if w.Type == xdr.ScErrorTypeSceContract {
							if w.ContractCode != nil && scErr.ContractCode != nil && *w.ContractCode == *scErr.ContractCode {
								return true, wantDesc, wantDesc
							}
						} else {
							if w.Code != nil && scErr.Code != nil && *w.Code == *scErr.Code {
								return true, wantDesc, wantDesc
							}
						}
					}
				}
			}
			if strings.Contains(e.detail, wantDesc) {
				return true, wantDesc, wantDesc
			}
		}
		if rawMeta != nil {
			if retVal, ok := metaReturnValue(rawMeta); ok {
				if scErr, ok := scValError(retVal); ok {
					if scErr.Type == w.Type {
						if w.Type == xdr.ScErrorTypeSceContract {
							if w.ContractCode != nil && scErr.ContractCode != nil && *w.ContractCode == *scErr.ContractCode {
								return true, wantDesc, wantDesc
							}
						} else {
							if w.Code != nil && scErr.Code != nil && *w.Code == *scErr.Code {
								return true, wantDesc, wantDesc
							}
						}
					}
				}
			}
		}
		return false, wantDesc, formatActual()

	case string:
		wantDesc = w
		for _, e := range events {
			if strings.Contains(e.detail, wantDesc) {
				return true, wantDesc, wantDesc
			}
			if data, ok := eventData(e); ok {
				if scErr, ok := scValError(data); ok {
					if describeScError(scErr) == wantDesc {
						return true, wantDesc, wantDesc
					}
					if scErr.Code != nil && strings.TrimPrefix(scErr.Code.String(), "ScErrorCodeScec") == wantDesc {
						return true, wantDesc, wantDesc
					}
				}
				if data.Type == xdr.ScValTypeScvSymbol && data.Sym != nil && string(*data.Sym) == wantDesc {
					return true, wantDesc, wantDesc
				}
			}
			for _, topic := range e.Topics {
				if sym, ok := topic.(xdr.ScSymbol); ok && string(sym) == wantDesc {
					return true, wantDesc, wantDesc
				}
				if sv, ok := topic.(xdr.ScVal); ok && sv.Type == xdr.ScValTypeScvSymbol && sv.Sym != nil && string(*sv.Sym) == wantDesc {
					return true, wantDesc, wantDesc
				}
			}
		}
		return false, wantDesc, formatActual()

	default:
		wantDesc = fmt.Sprintf("%v", wantReason)
		return false, wantDesc, formatActual()
	}
}
