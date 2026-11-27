package focus

import (
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Ledger close meta assertions read the protocol version and close time a
// LedgerCloseMeta fixture was captured at. Tests comparing fixtures across
// networks need to know both: a fixture that belongs to the wrong protocol,
// or whose close time falls outside the window a test believes it is
// looking at, invalidates whatever else the fixture shows.
//
// LedgerCloseMeta carries its ledger header in one of three arms — V0, V1 or
// V2 — that otherwise differ in what else they record. Both assertions read
// through xdr.LedgerCloseMeta's own ProtocolVersion and LedgerCloseTime
// accessors, which already switch on the arm, so there is nothing
// version-specific here beyond trusting the SDK to keep that switch current.

// AssertLedgerCloseMetaProtocol checks that meta belongs to protocol version
// wantProtocol.
//
// It accepts any LedgerCloseMeta — V0, V1 or V2 alike.
func AssertLedgerCloseMetaProtocol(t TB, meta xdr.LedgerCloseMeta, wantProtocol uint32, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertLedgerCloseMetaProtocol"
	_ = newConfig(opts...)

	var got uint32
	if !guard(t, assertion, func() { got = meta.ProtocolVersion() }) {
		return false
	}
	if got == wantProtocol {
		return true
	}

	t.Errorf("%s: ledger %d closed at protocol version %d, want %d",
		assertion, ledgerSeqOf(meta), got, wantProtocol)
	return false
}

// AssertLedgerCloseTime checks that meta's close time falls within
// [minUnix, maxUnix], inclusive, where both bounds are Unix seconds.
//
// It accepts any LedgerCloseMeta — V0, V1 or V2 alike.
func AssertLedgerCloseTime(t TB, meta xdr.LedgerCloseMeta, minUnix, maxUnix int64, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertLedgerCloseTime"
	_ = newConfig(opts...)

	if minUnix > maxUnix {
		t.Errorf("%s: minUnix %d is after maxUnix %d; the bounds are reversed",
			assertion, minUnix, maxUnix)
		return false
	}

	var got int64
	if !guard(t, assertion, func() { got = meta.LedgerCloseTime() }) {
		return false
	}
	if got >= minUnix && got <= maxUnix {
		return true
	}

	t.Errorf("%s: ledger %d closed at %s (unix %d), want between %s and %s (unix %d..%d)",
		assertion, ledgerSeqOf(meta),
		formatUnix(got), got,
		formatUnix(minUnix), formatUnix(maxUnix), minUnix, maxUnix)
	return false
}

// ledgerSeqOf names the ledger a failure message is about. It calls a
// generated accessor through the same union switch ProtocolVersion and
// LedgerCloseTime use, so a malformed meta that would panic there is
// already behind this function's own guard in the caller.
func ledgerSeqOf(meta xdr.LedgerCloseMeta) uint32 {
	return meta.LedgerSequence()
}

// formatUnix renders a Unix second count as UTC RFC3339, alongside which
// every message here also prints the raw seconds.
func formatUnix(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}
