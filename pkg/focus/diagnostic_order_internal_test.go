package focus

import (
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
)

// TestDiagnosticStreamReportsAMetaItCannotRead covers the two shapes of meta
// this package is handed but cannot read: a version newer than the ones it
// knows, which the SDK refuses to walk, and a value claiming to be a meta
// without holding one. Both have to be reported rather than looking like a
// transaction that simply emitted nothing.
func TestDiagnosticStreamReportsAMetaItCannotRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		meta     *lens.Value
		contains string
	}{
		{
			name:     "a meta version that arrives before this package knows it",
			meta:     &lens.Value{Type: "TransactionMeta", Raw: &xdr.TransactionMeta{V: 5}},
			contains: "unsupported TransactionMeta version: 5",
		},
		{
			name:     "a value that claims to be a meta",
			meta:     &lens.Value{Type: "TransactionMeta", Raw: "not a meta"},
			contains: "want a TransactionMeta",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if _, _, ok := diagnosticStream(tb, "AssertDiagnosticEventOrder", tc.meta); ok {
				t.Fatal("an unreadable meta was reported as a readable one")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			if !strings.Contains(msg, tc.contains) {
				t.Errorf("message does not contain %q:\n%s", tc.contains, msg)
			}
		})
	}
}

// TestLedgerPins checks the ledger a meta pins itself to: the newest
// LastModifiedLedgerSeq of the entries it changed, with removals and unset
// sequences ignored, because a removal carries only a key and a zero sequence
// is an entry that was never written.
func TestLedgerPins(t *testing.T) {
	t.Parallel()

	created := xdr.LedgerEntry{LastModifiedLedgerSeq: 43}
	updated := xdr.LedgerEntry{LastModifiedLedgerSeq: 41}
	duplicate := xdr.LedgerEntry{LastModifiedLedgerSeq: 43}
	restored := xdr.LedgerEntry{LastModifiedLedgerSeq: 12}
	unset := xdr.LedgerEntry{}
	var removedKey xdr.LedgerKey

	changes := []xdr.LedgerEntryChange{
		{Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated, Created: &created},
		{Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated, Updated: &updated},
		{Type: xdr.LedgerEntryChangeTypeLedgerEntryState, State: &duplicate},
		{Type: xdr.LedgerEntryChangeTypeLedgerEntryRestored, Restored: &restored},
		{Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved, Removed: &removedKey},
		{Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated, Created: &unset},
	}

	tests := []struct {
		name string
		meta *xdr.TransactionMeta
		want []uint32
	}{
		{
			name: "version 3, sorted and de-duplicated",
			meta: &xdr.TransactionMeta{V: 3, V3: &xdr.TransactionMetaV3{TxChangesBefore: changes}},
			want: []uint32{12, 41, 43},
		},
		{
			name: "version 4, including the changes after the operations",
			meta: &xdr.TransactionMeta{V: 4, V4: &xdr.TransactionMetaV4{
				TxChangesBefore: changes[:1],
				Operations:      []xdr.OperationMetaV2{{Changes: changes[1:2]}},
				TxChangesAfter:  changes[2:],
			}},
			want: []uint32{12, 41, 43},
		},
		{
			name: "a meta with no changes pins nothing",
			meta: &xdr.TransactionMeta{V: 3, V3: &xdr.TransactionMetaV3{}},
		},
		{
			name: "a meta with a missing arm pins nothing",
			meta: &xdr.TransactionMeta{V: 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ledgerPins(tc.meta)
			if len(got) != len(tc.want) {
				t.Fatalf("ledgerPins = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ledgerPins = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestChangeEntry checks that every change kind a transaction can carry yields
// the entry it holds — and that a removal, which holds only a key, yields
// nothing to read a ledger from.
func TestChangeEntry(t *testing.T) {
	t.Parallel()

	entry := xdr.LedgerEntry{LastModifiedLedgerSeq: 7}
	var key xdr.LedgerKey

	tests := []struct {
		name string
		in   xdr.LedgerEntryChange
		want bool
	}{
		{"created", xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated, Created: &entry}, true},
		{"updated", xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated, Updated: &entry}, true},
		{"state", xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryState, State: &entry}, true},
		{"restored", xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryRestored, Restored: &entry}, true},
		{"removed", xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved, Removed: &key}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := changeEntry(tc.in); (got != nil) != tc.want {
				t.Errorf("changeEntry(%s) = %v, want an entry: %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestDecorateDiagnosticsWithoutABody covers an event whose body carries an
// arm this package does not know: it still has a place in the stream, so it is
// described rather than dropped, and it is not made to look like a match for a
// payload it does not have.
func TestDecorateDiagnosticsWithoutABody(t *testing.T) {
	t.Parallel()

	raw := []xdr.DiagnosticEvent{
		// No V0 arm, so no topics and no data to match on.
		{InSuccessfulContractCall: true, Event: xdr.ContractEvent{
			Type: xdr.ContractEventTypeDiagnostic,
			Body: xdr.ContractEventBody{V: 1},
		}},
	}

	events := decorateDiagnostics(raw)
	if len(events) != 1 {
		t.Fatalf("decorateDiagnostics returned %d events, want 1", len(events))
	}
	e := events[0]
	if e.Kind != DiagnosticOther {
		t.Errorf("kind is %q, want %q", e.Kind, DiagnosticOther)
	}
	if e.Topics != nil || e.Data != nil {
		t.Errorf("a body without a V0 arm should carry no payload, got topics %v and data %v",
			e.Topics, e.Data)
	}
	if !strings.Contains(e.detail, "no V0 arm") {
		t.Errorf("description does not explain the missing arm: %q", e.detail)
	}
	if diagnosticEventMatches(e, DiagnosticEventMatch{Data: "transfer"}) {
		t.Error("an event with no payload matched a payload")
	}
}

// TestDiagnosticKindNamesAnUnknownEventType covers an event type this package
// does not know, which a future protocol version could introduce: it still has
// to be reported, and it cannot be named after one of the types that do exist.
func TestDiagnosticKindNamesAnUnknownEventType(t *testing.T) {
	t.Parallel()

	if got := diagnosticKind(xdr.ContractEvent{Type: xdr.ContractEventType(9)}); got != "event_type_9" {
		t.Errorf("diagnosticKind(9) = %q, want %q", got, "event_type_9")
	}
}

// TestDecorateDiagnosticsClampsUnmatchedReturns checks that a return without a
// call to close leaves the depth where it is, so a truncated stream cannot
// report every event after it as nested somewhere it is not.
func TestDecorateDiagnosticsClampsUnmatchedReturns(t *testing.T) {
	t.Parallel()

	symbol := func(s string) xdr.ScVal {
		sym := xdr.ScSymbol(s)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	}
	event := func(kind string) xdr.DiagnosticEvent {
		return xdr.DiagnosticEvent{Event: xdr.ContractEvent{
			Type: xdr.ContractEventTypeDiagnostic,
			Body: xdr.ContractEventBody{V: 0, V0: &xdr.ContractEventV0{
				Topics: []xdr.ScVal{symbol(kind)},
				Data:   xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			}},
		}}
	}

	events := decorateDiagnostics([]xdr.DiagnosticEvent{
		event(DiagnosticFnReturn),
		event(DiagnosticFnReturn),
		event("log"),
	})

	for i, e := range events {
		if e.Depth != 0 {
			t.Errorf("event %d has depth %d, want 0", i, e.Depth)
		}
	}
}

// TestDiagnosticEventMatchesIgnoresAnUnknownOutcome checks that a CallOutcome
// outside the defined set filters nothing rather than rejecting every event,
// since a zero-value matcher has to keep matching.
func TestDiagnosticEventMatchesIgnoresAnUnknownOutcome(t *testing.T) {
	t.Parallel()

	e := DiagnosticEvent{
		Kind:                     DiagnosticContract,
		InSuccessfulContractCall: true,
		event: xdr.ContractEvent{
			Type: xdr.ContractEventTypeContract,
			Body: xdr.ContractEventBody{V: 0, V0: &xdr.ContractEventV0{
				Data: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			}},
		},
	}

	if !diagnosticEventMatches(e, DiagnosticEventMatch{Call: CallOutcome(9)}) {
		t.Error("an unknown call outcome filtered an event out")
	}
	if !diagnosticEventMatches(e, DiagnosticEventMatch{Kind: "CONTRACT"}) {
		t.Error("kind matching is not case-insensitive")
	}
	if diagnosticEventMatches(e, DiagnosticEventMatch{Call: CallOutcomeFailed}) {
		t.Error("a successful call matched CallOutcomeFailed")
	}
}

// TestDescribeCloseTime covers the two shapes a close time is rendered in, so
// an unset one reads as missing rather than as the zero instant.
func TestDescribeCloseTime(t *testing.T) {
	t.Parallel()

	if got := describeCloseTime(time.Time{}); got != "(no close time)" {
		t.Errorf("describeCloseTime(zero) = %q", got)
	}
	// Rendered in UTC, so the same instant reads the same everywhere.
	at := time.Date(2026, time.August, 5, 14, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	if got := describeCloseTime(at); got != "2026-08-05T12:00:00Z" {
		t.Errorf("describeCloseTime(%s) = %q", at, got)
	}
	if got := describeTimeline(DiagnosticTimeline{Ledger: 42, ClosedAt: at}); got != "ledger 42, closed 2026-08-05T12:00:00Z" {
		t.Errorf("describeTimeline = %q", got)
	}
}
