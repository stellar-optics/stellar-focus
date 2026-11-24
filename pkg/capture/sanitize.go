package capture

import (
	"encoding"
	"encoding/base64"
	"fmt"
	"reflect"
	"regexp"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Placeholders written in place of what a Sanitizer removes.
const (
	// RedactedMemo replaces the text of a text memo.
	RedactedMemo = "[REDACTED_MEMO]"
	// RedactedData replaces the value of a data entry.
	RedactedData = "[REDACTED_DATA]"
	// RedactedSecretKey replaces anything shaped like a secret key.
	RedactedSecretKey = "[REDACTED_SECRET_KEY]"
)

// secretKeyPattern matches a StrKey secret seed: an S followed by 55 more
// base32 characters. It is deliberately loose — a string that only looks
// like a seed is redacted too, since a missed secret costs far more than an
// over-eager mask.
var secretKeyPattern = regexp.MustCompile(`S[A-Z0-9]{55}`)

// Sanitizer removes sensitive data from captured records before they are
// written as fixtures, so a capture from a staging network can be committed
// to a public repository.
//
// It rewrites the XDR itself, so the decoded section of the fixture — which
// is rendered from the XDR — is clean too. A sanitized envelope no longer
// hashes to its transaction, and its signatures no longer verify; it cannot
// be re-checked against the network, which Verify reports.
type Sanitizer struct {
	// MaskMemo replaces the text of every text memo with RedactedMemo.
	MaskMemo bool
	// ScrubData replaces every data entry value — in a ManageData operation
	// and in a DataEntry — with RedactedData.
	ScrubData bool
	// ScrubKeys replaces every secret-key-shaped string anywhere in the XDR
	// — a data entry name or value, a Soroban string, symbol or bytes — with
	// RedactedSecretKey.
	ScrubKeys bool
}

// Enabled reports whether the sanitizer would change anything.
func (s Sanitizer) Enabled() bool {
	return s.MaskMemo || s.ScrubData || s.ScrubKeys
}

// Sanitize returns a copy of records with the sensitive data the sanitizer
// is configured for replaced by placeholders, each record marked Sanitized.
func (s Sanitizer) Sanitize(records []Record) ([]Record, error) {
	out := append([]Record(nil), records...)
	if !s.Enabled() {
		return out, nil
	}
	for i := range out {
		value, err := lens.DecodeAs(out[i].XDR, out[i].Type)
		if err != nil {
			return nil, fmt.Errorf("decoding %s for sanitization: %w", out[i].Type, err)
		}

		// Work on an addressable copy, so the walk can set fields however
		// the decoder handed the value back.
		root := reflect.New(reflect.TypeOf(value.Raw))
		root.Elem().Set(reflect.ValueOf(value.Raw))
		s.sanitizeValue(root)

		marshaler, ok := root.Elem().Interface().(encoding.BinaryMarshaler)
		if !ok {
			return nil, fmt.Errorf("XDR value %s cannot be encoded", out[i].Type)
		}
		encoded, err := marshaler.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("encoding sanitized %s: %w", out[i].Type, err)
		}
		out[i].XDR = base64.StdEncoding.EncodeToString(encoded)
		out[i].Sanitized = true
	}
	return out, nil
}

var (
	memoType      = reflect.TypeOf(xdr.Memo{})
	dataValueType = reflect.TypeOf(xdr.DataValue{})
)

// sanitizeValue walks an XDR value, rewriting what the sanitizer masks.
func (s Sanitizer) sanitizeValue(value reflect.Value) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			s.sanitizeValue(value.Elem())
		}
		return
	}

	switch value.Type() {
	case memoType:
		if s.MaskMemo && value.CanSet() {
			memo, ok := value.Interface().(xdr.Memo)
			if ok && memo.Type == xdr.MemoTypeMemoText && memo.Text != nil {
				text := RedactedMemo
				memo.Text = &text
				value.Set(reflect.ValueOf(memo))
				return
			}
		}
	case dataValueType:
		if s.ScrubData && value.CanSet() && !value.IsNil() {
			value.SetBytes([]byte(RedactedData))
			return
		}
	}

	switch value.Kind() {
	case reflect.String:
		if s.ScrubKeys && value.CanSet() {
			value.SetString(secretKeyPattern.ReplaceAllString(value.String(), RedactedSecretKey))
		}
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			if s.ScrubKeys && value.CanSet() && !value.IsNil() {
				scrubbed := secretKeyPattern.ReplaceAll(value.Bytes(), []byte(RedactedSecretKey))
				value.SetBytes(scrubbed)
			}
			return
		}
		for i := 0; i < value.Len(); i++ {
			s.sanitizeValue(value.Index(i))
		}
	case reflect.Array:
		// Fixed-size byte arrays are hashes and keys, binary rather than
		// text; there is no secret-key string to find in them.
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := 0; i < value.Len(); i++ {
			s.sanitizeValue(value.Index(i))
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			s.sanitizeValue(value.Field(i))
		}
	}
}
