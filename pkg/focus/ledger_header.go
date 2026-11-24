package focus

import (
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Ledger header assertions read the network parameters a ledger closed with:
// its own sequence, the base fee it charged and the base reserve it held.
// They are the protocol-level counterpart to transaction assertions, and the
// reason a captured header is worth keeping — a fixture whose base reserve
// silently changed no longer describes the ledger it claims to.

// AssertLedgerHeader checks a ledger header's sequence, base fee and base
// reserve.
//
// header accepts an xdr.LedgerHeader, an xdr.LedgerHeaderHistoryEntry (the
// shape a captured ledger-header fixture holds) or either as base64 XDR, raw
// bytes or a decoded *lens.Value. Reading a history entry asserts on its
// header, since the hash and close metadata around it are provenance rather
// than network parameters.
//
// Every field is checked and every mismatch reported, so one run names all
// three values that moved. Fees and reserves are shown in stroops and XLM
// because both units are used in practice.
func AssertLedgerHeader(t TB, header Value, wantSeq, wantBaseFee, wantBaseReserve uint32) bool {
	t.Helper()
	const assertion = "AssertLedgerHeader"

	h, ok := ledgerHeaderIn(t, assertion, header)
	if !ok {
		return false
	}

	ok = true
	if got := uint32(h.LedgerSeq); got != wantSeq {
		t.Errorf("%s: ledger sequence is %d, want %d", assertion, got, wantSeq)
		ok = false
	}
	if got := uint32(h.BaseFee); got != wantBaseFee {
		t.Errorf("%s: ledger %d base fee is %s, want %s",
			assertion, h.LedgerSeq, stroops(int64(got)), stroops(int64(wantBaseFee)))
		ok = false
	}
	if got := uint32(h.BaseReserve); got != wantBaseReserve {
		t.Errorf("%s: ledger %d base reserve is %s, want %s",
			assertion, h.LedgerSeq, stroops(int64(got)), stroops(int64(wantBaseReserve)))
		ok = false
	}
	return ok
}

// ledgerHeaderIn coerces and unwraps a ledger header argument, reporting a
// test failure when it cannot.
//
// Detection is left to the decoder rather than forced to one type, because
// the two accepted shapes differ by a wrapper and the caller may hand over
// either.
func ledgerHeaderIn(t TB, assertion string, v Value) (xdr.LedgerHeader, bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "header", v, "")
	if !ok {
		return xdr.LedgerHeader{}, false
	}
	switch raw := decoded.Raw.(type) {
	case *xdr.LedgerHeader:
		return *raw, true
	case *xdr.LedgerHeaderHistoryEntry:
		return raw.Header, true
	default:
		t.Errorf("%s: value is a %s, want a LedgerHeader or LedgerHeaderHistoryEntry",
			assertion, decoded.Type)
		return xdr.LedgerHeader{}, false
	}
}
