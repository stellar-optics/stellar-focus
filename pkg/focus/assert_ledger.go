package focus

import (
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertLedgerCloseMetaProtocol checks the protocol version recorded in a
// ledger close meta, for any supported LedgerCloseMeta version.
func AssertLedgerCloseMetaProtocol(t TB, meta xdr.LedgerCloseMeta, wantProtocol uint32, opts ...Option) bool {
	t.Helper()
	name := newConfig(opts...).assertionName("AssertLedgerCloseMetaProtocol")
	var got uint32
	if !guard(t, name, func() { got = meta.ProtocolVersion() }) {
		return false
	}
	if got == wantProtocol {
		return true
	}
	t.Errorf("%s: protocol version = %d, want %d", name, got, wantProtocol)
	return false
}

// AssertLedgerCloseTime checks that the close time is within inclusive Unix
// timestamp bounds, for any supported LedgerCloseMeta version.
func AssertLedgerCloseTime(t TB, meta xdr.LedgerCloseMeta, minUnix, maxUnix int64, opts ...Option) bool {
	t.Helper()
	name := newConfig(opts...).assertionName("AssertLedgerCloseTime")
	if minUnix > maxUnix {
		t.Errorf("%s: minimum timestamp %d exceeds maximum %d", name, minUnix, maxUnix)
		return false
	}
	var got int64
	if !guard(t, name, func() { got = meta.LedgerCloseTime() }) {
		return false
	}
	if got >= minUnix && got <= maxUnix {
		return true
	}
	format := func(unix int64) string {
		return fmt.Sprintf("%d (%s)", unix, time.Unix(unix, 0).UTC().Format(time.RFC3339))
	}
	t.Errorf("%s: ledger close time = %s, want between %s and %s", name, format(got), format(minUnix), format(maxUnix))
	return false
}
