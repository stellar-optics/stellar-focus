// Package ledgerkey builds the base64-encoded LedgerKey XDR that
// `focus capture ledger-entry` and capture.Source expect.
//
// Constructing a LedgerKey directly with the generated SDK types means filling
// in nested union arms and pointers for each entry kind. These helpers take
// the values a test already has — an address, an asset, a contract id — and
// return the canonical key string.
//
// ConfigSetting covers the network's upgradeable values, the ConfigSettingEntry
// records a protocol upgrade writes: a cost model, the state archival
// parameters, the eviction cursor. The key names the setting by its
// ConfigSettingID alone, because the id is what identifies the entry:
//
//	key := ledgerkey.ConfigSetting(xdr.ConfigSettingIdConfigSettingStateArchival)
//
// The state-eviction cursor is one of those settings rather than an entry of
// its own, so EvictionIterator is the shortcut for its key:
//
//	cursor := ledgerkey.EvictionIterator()
//
// Both return base64 XDR, ready for capture.Source or AssertLedgerKeyEqual.
package ledgerkey

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// Account returns the LedgerKey for an account, as base64 XDR.
func Account(address string) (string, error) {
	accountID, err := xdr.AddressToAccountId(address)
	if err != nil {
		return "", fmt.Errorf("parse account address %q: %w", address, err)
	}

	var key xdr.LedgerKey
	if err := key.SetAccount(accountID); err != nil {
		return "", fmt.Errorf("build account ledger key: %w", err)
	}
	return encode(key)
}

// Trustline returns the LedgerKey for a trustline owned by address, as base64
// XDR. The asset must be native or a credit asset; a pool-share asset is not a
// trustline key.
func Trustline(address string, asset xdr.Asset) (string, error) {
	accountID, err := xdr.AddressToAccountId(address)
	if err != nil {
		return "", fmt.Errorf("parse account address %q: %w", address, err)
	}
	if err := validateAsset(asset); err != nil {
		return "", err
	}

	var key xdr.LedgerKey
	if err := key.SetTrustline(accountID, asset.ToTrustLineAsset()); err != nil {
		return "", fmt.Errorf("build trustline ledger key: %w", err)
	}
	return encode(key)
}

// ContractData returns the LedgerKey for a contract's data entry, as base64
// XDR.
//
// contractID is the strkey "C…" address. key is the contract data key and
// accepts an xdr.ScVal, base64 XDR, raw XDR bytes, or a plain string, which is
// treated as a symbol because that is the common contract key.
func ContractData(
	contractID string,
	key focus.Value,
	durability xdr.ContractDataDurability,
) (string, error) {
	contract, err := contractAddress(contractID)
	if err != nil {
		return "", err
	}
	switch durability {
	case xdr.ContractDataDurabilityPersistent, xdr.ContractDataDurabilityTemporary:
	default:
		return "", fmt.Errorf("contract data durability %d is neither persistent nor temporary", durability)
	}

	keyVal, err := scVal(key)
	if err != nil {
		return "", fmt.Errorf("parse contract data key: %w", err)
	}

	var ledgerKey xdr.LedgerKey
	if err := ledgerKey.SetContractData(contract, keyVal, durability); err != nil {
		return "", fmt.Errorf("build contract data ledger key: %w", err)
	}
	return encode(ledgerKey)
}

// ContractCode returns the LedgerKey for contract code with the given hash, as
// base64 XDR.
func ContractCode(hash [32]byte) (string, error) {
	var key xdr.LedgerKey
	if err := key.SetContractCode(xdr.Hash(hash)); err != nil {
		return "", fmt.Errorf("build contract code ledger key: %w", err)
	}
	return encode(key)
}

// ConfigSetting returns the LedgerKey for the network configuration setting
// identified by settingID, as base64 XDR.
//
// A configuration setting is the entry a protocol upgrade writes to change a
// network-wide value, so a test that exercises an upgrade — or reads back what
// the upgrade left — names the setting by its id and no other field.
//
// Every id in the ConfigSettingID enum builds a key. An id the enum does not
// define is not a setting the protocol can hold, so there is no entry to
// address and the builder returns the empty string; a caller that has an id of
// unknown provenance can check for it with ValidEnum first.
func ConfigSetting(settingID xdr.ConfigSettingId) string {
	key := xdr.LedgerKey{
		Type:          xdr.LedgerEntryTypeConfigSetting,
		ConfigSetting: &xdr.LedgerKeyConfigSetting{ConfigSettingId: settingID},
	}
	encoded, err := encode(key)
	if err != nil {
		// The only key that does not encode is one whose id falls outside the
		// enum, which this builder's signature has no error to report.
		return ""
	}
	return encoded
}

// EvictionIterator returns the LedgerKey for the state-eviction cursor, as
// base64 XDR.
//
// The cursor is not an entry of its own: the protocol keeps an
// xdr.EvictionIterator — the bucket list level, bucket and offset where the
// eviction scan stopped — in the CONFIG_SETTING_EVICTION_ITERATOR setting. Its
// key is that setting's ConfigSetting key, which is what this returns.
func EvictionIterator() string {
	return ConfigSetting(xdr.ConfigSettingIdConfigSettingEvictionIterator)
}

// contractAddress decodes a strkey contract id into the ScAddress a contract
// data key names.
func contractAddress(contractID string) (xdr.ScAddress, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		return xdr.ScAddress{}, fmt.Errorf("parse contract address %q: %w", contractID, err)
	}

	var id xdr.ContractId
	copy(id[:], raw)
	return xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &id,
	}, nil
}

// scVal converts an accepted contract data key into an ScVal.
func scVal(v focus.Value) (xdr.ScVal, error) {
	switch val := v.(type) {
	case xdr.ScVal:
		return val, nil
	case *xdr.ScVal:
		if val == nil {
			return xdr.ScVal{}, fmt.Errorf("key is a nil *xdr.ScVal")
		}
		return *val, nil
	case string:
		// Try base64 XDR first, then fall back to a symbol, matching how the
		// event matchers read a plain string.
		if sv, err := decodeScVal(val); err == nil {
			return sv, nil
		}
		sym := xdr.ScSymbol(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}, nil
	default:
		encoded, _, err := focus.Encode(v, "ScVal")
		if err != nil {
			return xdr.ScVal{}, err
		}
		return decodeScVal(encoded)
	}
}

// decodeScVal decodes a base64-encoded ScVal.
func decodeScVal(payload string) (xdr.ScVal, error) {
	var out xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(payload, &out); err != nil {
		return xdr.ScVal{}, err
	}
	return out, nil
}

// validateAsset rejects an asset whose union arm does not match its type, so
// building the key reports an error instead of panicking inside the SDK's
// conversion helper.
func validateAsset(asset xdr.Asset) error {
	switch asset.Type {
	case xdr.AssetTypeAssetTypeNative:
		return nil
	case xdr.AssetTypeAssetTypeCreditAlphanum4:
		if asset.AlphaNum4 == nil {
			return fmt.Errorf("asset is alphanum4 but carries no alphanum4 arm")
		}
		return nil
	case xdr.AssetTypeAssetTypeCreditAlphanum12:
		if asset.AlphaNum12 == nil {
			return fmt.Errorf("asset is alphanum12 but carries no alphanum12 arm")
		}
		return nil
	default:
		return fmt.Errorf("asset type %d is not a trustline asset", asset.Type)
	}
}

// encode returns the canonical base64 XDR of a ledger key.
func encode(key xdr.LedgerKey) (string, error) {
	encoded, err := key.MarshalBinaryBase64()
	if err != nil {
		return "", fmt.Errorf("encode ledger key: %w", err)
	}
	return encoded, nil
}
