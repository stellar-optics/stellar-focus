package focus

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertClaimableBalanceID checks the ID produced by a successful
// CreateClaimableBalance operation. wantID may be an XDR ID, a 32-byte hash,
// raw 32-byte slice, or its hexadecimal/base64 encoding.
func AssertClaimableBalanceID(t TB, result xdr.OperationResult, wantID any, opts ...Option) bool {
	t.Helper()
	const assertion = "AssertClaimableBalanceID"
	if result.Code != xdr.OperationResultCodeOpInner || result.Tr == nil || result.Tr.Type != xdr.OperationTypeCreateClaimableBalance || result.Tr.CreateClaimableBalanceResult == nil {
		t.Errorf("%s: operation result is not a CreateClaimableBalance result", assertion)
		return false
	}
	actual, ok := result.Tr.CreateClaimableBalanceResult.GetBalanceId()
	if !ok {
		t.Errorf("%s: create claimable balance did not return an ID", assertion)
		return false
	}
	want, err := claimableIDValue(wantID)
	if err != nil {
		t.Errorf("%s: expected ID is invalid: %v", assertion, err)
		return false
	}
	_ = opts
	actualHash, actualOK := actual.GetV0()
	wantHash, wantOK := want.GetV0()
	if !actualOK || !wantOK {
		t.Errorf("%s: only V0 claimable balance IDs are supported", assertion)
		return false
	}
	if actualHash != wantHash {
		t.Errorf("%s: claimable balance ID is %x, want %x", assertion, actualHash, wantHash)
		return false
	}
	return true
}

func claimableIDValue(value any) (xdr.ClaimableBalanceId, error) {
	if id, ok := value.(xdr.ClaimableBalanceId); ok {
		return id, nil
	}
	var raw []byte
	switch id := value.(type) {
	case xdr.Hash:
		raw = id[:]
	case [32]byte:
		raw = id[:]
	case []byte:
		raw = id
	case string:
		decoded, err := hex.DecodeString(id)
		if err == nil {
			raw = decoded
		} else {
			raw, err = base64.StdEncoding.DecodeString(id)
			if err != nil {
				return xdr.ClaimableBalanceId{}, fmt.Errorf("must be 32 raw bytes, 64 hex characters, or base64")
			}
		}
	default:
		return xdr.ClaimableBalanceId{}, fmt.Errorf("unsupported ID type %T", value)
	}
	if len(raw) != 32 {
		return xdr.ClaimableBalanceId{}, fmt.Errorf("ID has %d bytes, want 32", len(raw))
	}
	var hash xdr.Hash
	copy(hash[:], raw)
	return xdr.NewClaimableBalanceId(xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0, hash)
}
