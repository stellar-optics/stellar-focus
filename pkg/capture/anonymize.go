package capture

import (
	"crypto/sha256"
	"encoding"
	"encoding/base64"
	"fmt"
	"reflect"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

type addressGetter interface {
	GetAddress() (string, error)
}

type addressSetter interface {
	SetAddress(string) error
}

// Anonymize returns a copy of records with account addresses replaced by
// deterministic, valid mock Stellar accounts. Equal addresses map to the
// same mock account across all records. Non-address signer keys are unchanged.
func Anonymize(records []Record) ([]Record, error) {
	return AnonymizeWithSeed(records, 0)
}

// AnonymizeWithSeed is Anonymize with a seed mixed into the pseudonym
// derivation, so the same real address maps to a different fake one under a
// different seed while staying stable within one seed.
func AnonymizeWithSeed(records []Record, seed int64) ([]Record, error) {
	out := append([]Record(nil), records...)
	mappings := make(map[string]string)
	for i := range out {
		value, err := lens.DecodeAs(out[i].XDR, out[i].Type)
		if err != nil {
			return nil, fmt.Errorf("decoding %s for anonymization: %w", out[i].Type, err)
		}
		if err := anonymizeValue(reflect.ValueOf(value.Raw), mappings, seed); err != nil {
			return nil, fmt.Errorf("masking addresses in %s: %w", out[i].Type, err)
		}
		marshaler, ok := value.Raw.(encoding.BinaryMarshaler)
		if !ok {
			return nil, fmt.Errorf("XDR value %s cannot be encoded", out[i].Type)
		}
		encoded, err := marshaler.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("encoding anonymized %s: %w", out[i].Type, err)
		}
		out[i].XDR = base64.StdEncoding.EncodeToString(encoded)
	}
	return out, nil
}

// AnonymizeEnvelope replaces every account address in env with a
// deterministic, valid Stellar Ed25519 public key derived from seed and the
// original address.
//
// The derivation is a pure function of the two, so the same real account maps
// to the same pseudonym in every envelope and result — a payment's source and
// destination, an operation source, a fee-bump payer — without the caller
// having to carry a mapping table between calls. The input envelope is not
// modified.
func AnonymizeEnvelope(env *xdr.TransactionEnvelope, seed int64) *xdr.TransactionEnvelope {
	if env == nil {
		return nil
	}
	raw, err := env.MarshalBinary()
	if err != nil {
		return env
	}
	var out xdr.TransactionEnvelope
	if err := out.UnmarshalBinary(raw); err != nil {
		return env
	}
	// The pointer makes every field addressable, so the walk can set the
	// union arms it finds.
	root := reflect.New(reflect.TypeOf(out))
	root.Elem().Set(reflect.ValueOf(out))
	if err := anonymizeValue(root.Elem(), make(map[string]string), seed); err != nil {
		return env
	}
	anonymized, ok := root.Elem().Interface().(xdr.TransactionEnvelope)
	if !ok {
		return env
	}
	return &anonymized
}

func anonymizeValue(value reflect.Value, mappings map[string]string, seed int64) error {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		if value.Kind() == reflect.Pointer && value.CanInterface() {
			if err := anonymizeAddressUnion(value, mappings, seed); err != nil {
				return err
			}
			if isAddressUnion(value.Type().Elem()) {
				return nil
			}
		}
		return anonymizeValue(value.Elem(), mappings, seed)
	}
	switch value.Kind() {
	case reflect.Struct:
		if isAddressUnion(value.Type()) && value.CanAddr() {
			if err := anonymizeAddressUnion(value.Addr(), mappings, seed); err != nil {
				return err
			}
			return nil
		}
		for i := 0; i < value.NumField(); i++ {
			if err := anonymizeValue(value.Field(i), mappings, seed); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			if err := anonymizeValue(value.Index(i), mappings, seed); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			mapValue := reflect.New(value.Type().Elem()).Elem()
			mapValue.Set(iter.Value())
			if err := anonymizeValue(mapValue, mappings, seed); err != nil {
				return err
			}
			value.SetMapIndex(iter.Key(), mapValue)
		}
	}
	return nil
}

func isAddressUnion(valueType reflect.Type) bool {
	if valueType.PkgPath() != "github.com/stellar/go-stellar-sdk/xdr" {
		return false
	}
	switch valueType.Name() {
	case "AccountId", "MuxedAccount", "SignerKey":
		return true
	default:
		return false
	}
}

func anonymizeAddressUnion(value reflect.Value, mappings map[string]string, seed int64) error {
	if !isAddressUnion(value.Type().Elem()) || !value.Elem().CanAddr() {
		return nil
	}
	union := value.Elem().Addr().Interface()
	getter, ok := union.(addressGetter)
	if !ok {
		return nil
	}
	// A union that does not resolve to an address has nothing to anonymise.
	// Both the unreadable and the empty case are left untouched rather than
	// reported, so the surrounding walk carries on with the rest of the
	// record; the address is only mocked when it could actually be read.
	address, err := getter.GetAddress()
	if err == nil && address != "" {
		return setMockedAddress(value, union, address, mappings, seed)
	}
	return nil
}

func setMockedAddress(value reflect.Value, union any, address string, mappings map[string]string, seed int64) error {
	mockAddress, ok := mappings[address]
	if !ok {
		var err error
		mockAddress, err = mockAccount(address, seed)
		if err != nil {
			return err
		}
		mappings[address] = mockAddress
	}

	if value.Type().Elem().Name() == "MuxedAccount" {
		return setMuxedAccountKey(value.Elem(), mockAddress)
	}
	setter, ok := union.(addressSetter)
	if !ok {
		return nil
	}
	return setter.SetAddress(mockAddress)
}

func mockAccount(address string, seed int64) (string, error) {
	input := fmt.Sprintf("stellar-focus-masked-account:%d:%s", seed, address)
	digest := sha256.Sum256([]byte(input))
	mock, err := strkey.Encode(strkey.VersionByteAccountID, digest[:])
	if err != nil {
		return "", fmt.Errorf("encoding mock account: %w", err)
	}
	return mock, nil
}

func setMuxedAccountKey(value reflect.Value, address string) error {
	decoded, err := strkey.Decode(strkey.VersionByteAccountID, address)
	if err != nil {
		return fmt.Errorf("decoding mock account: %w", err)
	}
	key := value.FieldByName("Ed25519")
	if key.IsNil() {
		muxed := value.FieldByName("Med25519")
		if muxed.IsNil() {
			return nil
		}
		key = muxed.Elem().FieldByName("Ed25519")
	}
	for i, b := range decoded {
		key.Elem().Index(i).SetUint(uint64(b))
	}
	return nil
}
