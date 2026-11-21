package focus

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertTrustlineModified checks that a transaction modified an account's
// trustline for an asset and left it with expectedLimit as its limit.
//
// account and asset accept the same forms as AssertBalanceChanged; the native
// asset is rejected because it has no trustline. Creating a trustline counts
// as modifying it, since the entry is new to the ledger with that limit.
//
// The limit is in stroops, the raw unit the protocol stores; treating it as a
// human amount is the caller's business. Later modifications win, so a
// transaction that changes the limit twice is checked against its final,
// as-committed state.
func AssertTrustlineModified(t TB, meta Value, account Value, asset Value, expectedLimit int64) bool {
	t.Helper()
	const assertion = "AssertTrustlineModified"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}
	if spec.native {
		t.Errorf("AssertTrustlineModified: the native asset has no trustline; " +
			"use AssertBalanceChanged to assert on an account's native balance")
		return false
	}

	mod, ok := findTrustlineModification(t, assertion, changes, addr, spec)
	if !ok {
		return false
	}
	if !mod.modified {
		t.Errorf("AssertTrustlineModified: the transaction did not modify trustline %s on account %s\n\n%s",
			spec.id, shorten(addr), indent(describeAccountChanges(changes, addr)))
		return false
	}
	if int64(mod.limit) == expectedLimit {
		return true
	}

	t.Errorf("AssertTrustlineModified: trustline %s on account %s has limit %s, want %s\n\n  %s",
		spec.id, shorten(addr),
		amountStroops(mod.limit, ""), amountStroops(xdr.Int64(expectedLimit), ""),
		describeTrustlineModification(mod))
	return false
}

// trustlineModification is the outcome of walking the changes for one
// trustline: whether it was touched, and its limit before and after.
type trustlineModification struct {
	modified  bool
	created   bool
	before    xdr.Int64
	hasBefore bool
	limit     xdr.Int64
}

// findTrustlineModification finds the last change to a trustline and the
// state preceding it.
func findTrustlineModification(
	t TB, assertion string, changes []ledgerChange, account string, spec assetSpec,
) (trustlineModification, bool) {
	var mod trustlineModification
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeTrustline || c.account != account || c.asset != spec.id {
				continue
			}
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if limit, has := trustlineLimit(c.entry); has {
					mod.before = limit
					mod.hasBefore = true
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				if limit, has := trustlineLimit(c.entry); has {
					mod.limit = limit
					mod.modified = true
					mod.created = true
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				if limit, has := trustlineLimit(c.entry); has {
					mod.limit = limit
					mod.modified = true
				}
			}
		}
	})
	if !ok {
		return trustlineModification{}, false
	}
	return mod, true
}

// trustlineLimit reads a trustline entry's limit.
func trustlineLimit(entry *xdr.LedgerEntry) (xdr.Int64, bool) {
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeTrustline || entry.Data.TrustLine == nil {
		return 0, false
	}
	return entry.Data.TrustLine.Limit, true
}

// describeTrustlineModification renders the limit movement, so a mismatch
// shows what the limit moved from as well as to.
func describeTrustlineModification(mod trustlineModification) string {
	switch {
	case mod.created:
		return "new trustline, limit " + amountStroops(mod.limit, "")
	case mod.hasBefore:
		return fmt.Sprintf("limit %s → %s",
			amountStroops(mod.before, ""), amountStroops(mod.limit, ""))
	default:
		return "limit " + amountStroops(mod.limit, "")
	}
}

// AssertTrustlineFlags checks that an account's trustline for asset has flags matching wantFlags.
//
// metaOrEntry accepts a TransactionMeta (in any form accepted by focus) or a direct
// xdr.TrustLineEntry or xdr.LedgerEntry.
//
// account and asset accept the same forms as AssertTrustlineModified.
// Flags are compared against wantFlags bitmask, and failure messages display
// human-readable active flag names (e.g. AUTHORIZED, CLAWBACK_ENABLED).
func AssertTrustlineFlags(t TestingT, metaOrEntry any, account string, asset string, wantFlags uint32, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertTrustlineFlags"
	_ = newConfig(opts...)

	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}
	if spec.native {
		t.Errorf("%s: the native asset has no trustline", assertion)
		return false
	}

	switch v := metaOrEntry.(type) {
	case xdr.TrustLineEntry:
		actualFlags := uint32(v.Flags)
		return checkTrustlineFlags(t, assertion, spec.id, addr, actualFlags, wantFlags, trustlineFlagsModification{modified: true, flags: actualFlags})
	case *xdr.TrustLineEntry:
		if v == nil {
			t.Errorf("%s: metaOrEntry is a nil *xdr.TrustLineEntry", assertion)
			return false
		}
		actualFlags := uint32(v.Flags)
		return checkTrustlineFlags(t, assertion, spec.id, addr, actualFlags, wantFlags, trustlineFlagsModification{modified: true, flags: actualFlags})
	case xdr.LedgerEntry:
		if v.Data.Type == xdr.LedgerEntryTypeTrustline && v.Data.TrustLine != nil {
			actualFlags := uint32(v.Data.TrustLine.Flags)
			return checkTrustlineFlags(t, assertion, spec.id, addr, actualFlags, wantFlags, trustlineFlagsModification{modified: true, flags: actualFlags})
		}
	case *xdr.LedgerEntry:
		if v != nil && v.Data.Type == xdr.LedgerEntryTypeTrustline && v.Data.TrustLine != nil {
			actualFlags := uint32(v.Data.TrustLine.Flags)
			return checkTrustlineFlags(t, assertion, spec.id, addr, actualFlags, wantFlags, trustlineFlagsModification{modified: true, flags: actualFlags})
		}
	}

	changes, ok := ledgerChanges(t, assertion, metaOrEntry)
	if !ok {
		return false
	}

	mod, ok := findTrustlineFlagsModification(t, assertion, changes, addr, spec)
	if !ok {
		return false
	}
	if !mod.modified {
		t.Errorf("%s: the transaction did not modify trustline %s on account %s\n\n%s",
			assertion, spec.id, shorten(addr), indent(describeAccountChanges(changes, addr)))
		return false
	}

	return checkTrustlineFlags(t, assertion, spec.id, addr, mod.flags, wantFlags, mod)
}

// DescribeTrustlineFlags returns human-readable active flag names for a trustline flag bitmask.
func DescribeTrustlineFlags(flags uint32) string {
	var parts []string
	if flags&uint32(xdr.TrustLineFlagsAuthorizedFlag) != 0 {
		parts = append(parts, "AUTHORIZED")
	}
	if flags&uint32(xdr.TrustLineFlagsAuthorizedToMaintainLiabilitiesFlag) != 0 {
		parts = append(parts, "AUTHORIZED_TO_MAINTAIN_LIABILITIES")
	}
	if flags&uint32(xdr.TrustLineFlagsTrustlineClawbackEnabledFlag) != 0 {
		parts = append(parts, "CLAWBACK_ENABLED")
	}
	known := uint32(xdr.TrustLineFlagsAuthorizedFlag) |
		uint32(xdr.TrustLineFlagsAuthorizedToMaintainLiabilitiesFlag) |
		uint32(xdr.TrustLineFlagsTrustlineClawbackEnabledFlag)
	if remaining := flags &^ known; remaining != 0 {
		parts = append(parts, fmt.Sprintf("UNKNOWN_0x%x", remaining))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "|")
}

type trustlineFlagsModification struct {
	modified  bool
	created   bool
	hasBefore bool
	before    uint32
	flags     uint32
}

func findTrustlineFlagsModification(
	t TB, assertion string, changes []ledgerChange, account string, spec assetSpec,
) (trustlineFlagsModification, bool) {
	var mod trustlineFlagsModification
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeTrustline || c.account != account || c.asset != spec.id {
				continue
			}
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if flags, has := trustlineEntryFlags(c.entry); has {
					mod.before = flags
					mod.hasBefore = true
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				if flags, has := trustlineEntryFlags(c.entry); has {
					mod.flags = flags
					mod.modified = true
					mod.created = true
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				if flags, has := trustlineEntryFlags(c.entry); has {
					mod.flags = flags
					mod.modified = true
				}
			}
		}
	})
	if !ok {
		return trustlineFlagsModification{}, false
	}
	return mod, true
}

func trustlineEntryFlags(entry *xdr.LedgerEntry) (uint32, bool) {
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeTrustline || entry.Data.TrustLine == nil {
		return 0, false
	}
	return uint32(entry.Data.TrustLine.Flags), true
}

func checkTrustlineFlags(t TB, assertion, assetID, addr string, gotFlags, wantFlags uint32, mod trustlineFlagsModification) bool {
	t.Helper()
	if gotFlags == wantFlags {
		return true
	}
	t.Errorf("%s: trustline %s on account %s has flags %s (0x%x), want %s (0x%x)\n\n  %s",
		assertion, assetID, shorten(addr),
		DescribeTrustlineFlags(gotFlags), gotFlags,
		DescribeTrustlineFlags(wantFlags), wantFlags,
		describeTrustlineFlagsMovement(mod))
	return false
}

func describeTrustlineFlagsMovement(mod trustlineFlagsModification) string {
	switch {
	case mod.created:
		return "new trustline, flags " + DescribeTrustlineFlags(mod.flags)
	case mod.hasBefore:
		return fmt.Sprintf("flags %s → %s",
			DescribeTrustlineFlags(mod.before), DescribeTrustlineFlags(mod.flags))
	default:
		return "flags " + DescribeTrustlineFlags(mod.flags)
	}
}
