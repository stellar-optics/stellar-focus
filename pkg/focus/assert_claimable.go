package focus

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertClaimableBalanceID checks that a CreateClaimableBalance operation's
// result assigned the wanted balance id.
//
// The id is derived by the protocol from the source account and the
// operation's sequence number and index, so it is not something a test can
// compute from the envelope alone — it exists only once the operation has
// run and the result records what the ledger assigned.
//
// wantID accepts the id as an xdr.ClaimableBalanceId, a 32-byte raw hash
// ([32]byte or []byte), or a 64-character hex string.
func AssertClaimableBalanceID(t TB, result xdr.OperationResult, wantID Value, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertClaimableBalanceID"
	_ = newConfig(opts...)

	want, ok := resolveWantedBalanceHash(t, assertion, wantID)
	if !ok {
		return false
	}

	got, ok := createdBalanceHashOf(t, assertion, result)
	if !ok {
		return false
	}

	if got == want {
		return true
	}

	t.Errorf("%s: claimable balance id does not match\n%s",
		assertion, indent(highlightHexDiff(hex.EncodeToString(want[:]), hex.EncodeToString(got[:]))))
	return false
}

// createdBalanceHashOf reads the balance id a CreateClaimableBalance
// operation result assigned, reporting why there is none when the operation
// did not successfully create a balance.
func createdBalanceHashOf(t TB, assertion string, result xdr.OperationResult) (xdr.Hash, bool) {
	t.Helper()

	if result.Code != xdr.OperationResultCodeOpInner || result.Tr == nil {
		t.Errorf("%s: operation result is %s, not a successful inner result",
			assertion, result.Code.String())
		return xdr.Hash{}, false
	}

	var (
		cb    xdr.CreateClaimableBalanceResult
		hasCB bool
		hash  xdr.Hash
		found bool
		fail  string
	)
	ok := guard(t, assertion, func() {
		cb, hasCB = result.Tr.GetCreateClaimableBalanceResult()
		if !hasCB {
			fail = fmt.Sprintf("operation result is %s, want CreateClaimableBalance",
				result.Tr.Type.String())
			return
		}
		if cb.Code != xdr.CreateClaimableBalanceResultCodeCreateClaimableBalanceSuccess || cb.BalanceId == nil {
			fail = fmt.Sprintf("create claimable balance result is %s, which carries no balance id",
				cb.Code.String())
			return
		}
		v0, has := cb.BalanceId.GetV0()
		if !has {
			fail = fmt.Sprintf("balance id is %s, want the V0 form", cb.BalanceId.Type.String())
			return
		}
		hash, found = v0, true
	})
	if !ok {
		return xdr.Hash{}, false
	}
	if !found {
		t.Errorf("%s: %s", assertion, fail)
		return xdr.Hash{}, false
	}
	return hash, true
}

// resolveWantedBalanceHash coerces a wanted balance id into its 32-byte
// hash, accepting the forms AssertClaimableBalanceID documents.
func resolveWantedBalanceHash(t TB, assertion string, v Value) (xdr.Hash, bool) {
	t.Helper()

	var (
		hash xdr.Hash
		err  error
	)
	if !guard(t, assertion, func() { hash, err = balanceHashOf(v) }) {
		return xdr.Hash{}, false
	}
	if err != nil {
		t.Errorf("%s: wanted balance id could not be read: %v", assertion, err)
		return xdr.Hash{}, false
	}
	return hash, true
}

// balanceHashOf accepts a claimable balance id as an xdr.ClaimableBalanceId,
// a raw 32-byte hash, or a 64-character hex string (with or without a "0x"
// prefix).
func balanceHashOf(v Value) (xdr.Hash, error) {
	switch val := v.(type) {
	case xdr.ClaimableBalanceId:
		v0, ok := val.GetV0()
		if !ok {
			return xdr.Hash{}, fmt.Errorf("balance id is %s, want the V0 form", val.Type.String())
		}
		return v0, nil
	case *xdr.ClaimableBalanceId:
		if val == nil {
			return xdr.Hash{}, fmt.Errorf("value is a nil *xdr.ClaimableBalanceId")
		}
		return balanceHashOf(*val)
	case xdr.Hash:
		return val, nil
	case *xdr.Hash:
		if val == nil {
			return xdr.Hash{}, fmt.Errorf("value is a nil *xdr.Hash")
		}
		return *val, nil
	case [32]byte:
		return xdr.Hash(val), nil
	case []byte:
		if len(val) != len(xdr.Hash{}) {
			return xdr.Hash{}, fmt.Errorf("raw hash is %d byte(s), want %d", len(val), len(xdr.Hash{}))
		}
		var h xdr.Hash
		copy(h[:], val)
		return h, nil
	case string:
		trimmed := strings.TrimPrefix(strings.TrimSpace(val), "0x")
		raw, err := hex.DecodeString(trimmed)
		if err != nil {
			return xdr.Hash{}, fmt.Errorf("%q is not hex: %w", val, err)
		}
		if len(raw) != len(xdr.Hash{}) {
			return xdr.Hash{}, fmt.Errorf("%q is %d byte(s) of hex, want %d (%d hex characters)",
				val, len(raw), len(xdr.Hash{}), 2*len(xdr.Hash{}))
		}
		var h xdr.Hash
		copy(h[:], raw)
		return h, nil
	default:
		return xdr.Hash{}, fmt.Errorf(
			"unsupported type %T; pass an xdr.ClaimableBalanceId, a 32-byte hash, or 64 hex characters", v)
	}
}

// highlightHexDiff renders two equal-length hex strings with a marker line
// underneath flagging exactly which characters differ, so a mismatched hash
// shows where without the reader diffing it by eye.
func highlightHexDiff(want, got string) string {
	if len(want) != len(got) {
		return fmt.Sprintf("want: %s\n got: %s", want, got)
	}
	marks := make([]byte, len(want))
	for i := 0; i < len(want); i++ {
		if want[i] == got[i] {
			marks[i] = ' '
		} else {
			marks[i] = '^'
		}
	}
	return fmt.Sprintf("want: %s\n got: %s\n      %s", want, got, marks)
}
