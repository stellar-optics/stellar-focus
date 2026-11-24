package capture

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// NewAccountLedgerKey builds the XDR key for an account from its StrKey address.
func NewAccountLedgerKey(address string) (xdr.LedgerKey, error) {
	accountID, err := xdr.AddressToAccountId(address)
	if err != nil {
		return xdr.LedgerKey{}, fmt.Errorf("parse account address: %w", err)
	}
	var key xdr.LedgerKey
	if err := key.SetAccount(accountID); err != nil {
		return xdr.LedgerKey{}, fmt.Errorf("build account ledger key: %w", err)
	}
	return key, nil
}

// EncodeLedgerKey returns a LedgerKey's XDR encoding as base64 for capture.Source.
func EncodeLedgerKey(key xdr.LedgerKey) (string, error) {
	encoded, err := key.MarshalBinaryBase64()
	if err != nil {
		return "", fmt.Errorf("encode ledger key: %w", err)
	}
	return encoded, nil
}
