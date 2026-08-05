// Package focus provides XDR-aware assertions for Go tests.
//
// The point of this package is the failure message. Comparing two Stellar
// transactions with a general-purpose assertion library tells you that two
// base64 strings differ, which is true and useless. focus decodes both sides
// and shows you the field that changed:
//
//	~ V1.Tx.Operations[0].Body.PaymentOp.Amount
//	    - 1000000000  100.0000000 (stroops: 1000000000)
//	    + 2000000000  200.0000000 (stroops: 2000000000)
//
// Decoding and diffing are done by stellar-xdr-lens, so the diff in a test
// failure is the same one `lens diff` prints at the command line.
//
// # Dependencies
//
// This package depends only on the standard library, the Stellar Go SDK and
// stellar-xdr-lens. Test helpers get pulled into other people's projects, so
// it deliberately carries no assertion framework, no diff library, and no CLI
// framework. The focus command lives in a separate package that this one does
// not import.
//
// # Failure behaviour
//
// Assertions report through TB.Errorf and return false, so a test continues
// and can report several problems in one run. Import the require subpackage
// for the same assertions with TB.Fatalf semantics.
//
// Assertions never panic. A malformed input, a nil value or a decoder failure
// is reported as a test failure like any other, because a panic inside
// someone else's test run destroys the output of every other test in the
// package.
package focus

import (
	"encoding"
	"fmt"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// TB is the subset of testing.TB that focus uses.
//
// Taking an interface rather than *testing.T means these assertions work in
// benchmarks and fuzz targets, can be wrapped by a project's own helpers, and
// — importantly — lets focus test its own failure messages by passing a
// recording implementation.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Name() string
}

// Value is any XDR payload an assertion accepts. The permitted forms are:
//
//	string                          base64-encoded XDR
//	[]byte                          raw XDR bytes
//	encoding.BinaryMarshaler        any xdr.* type from the Stellar SDK
//	*lens.Value                     an already-decoded value
//
// Anything else is reported as a test failure naming what was accepted, so a
// mistake surfaces immediately rather than as a confusing diff.
//
// The looseness is deliberate. Test code holds XDR in whichever form the
// system under test produced, and requiring callers to convert at every call
// site is friction in exactly the common case.
type Value = any

// coerce turns an accepted Value into a decoded lens value.
//
// typeName, when non-empty, forces the XDR type instead of detecting it.
// Forcing is preferred wherever the caller's intent is known, because
// detection is ambiguous for short payloads.
func coerce(v Value, typeName string) (*lens.Value, error) {
	switch val := v.(type) {
	case nil:
		return nil, fmt.Errorf("value is nil")

	case *lens.Value:
		if val == nil {
			return nil, fmt.Errorf("value is a nil *lens.Value")
		}
		if typeName != "" && !strings.EqualFold(val.Type, typeName) {
			return nil, fmt.Errorf("value is a %s, want a %s", val.Type, typeName)
		}
		return val, nil

	case lens.Value:
		return coerce(&val, typeName)

	case string:
		return decodeString(val, typeName)

	case []byte:
		// Raw XDR bytes rather than base64: re-encode so the same decode path
		// handles both, keeping error messages identical.
		return decodeString(base64Encode(val), typeName)

	case encoding.BinaryMarshaler:
		b, err := val.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("marshalling %T: %w", v, err)
		}
		return decodeString(base64Encode(b), typeName)

	default:
		return nil, fmt.Errorf(
			"unsupported type %T; expected a base64 string, []byte, an xdr.* value, or a *lens.Value", v)
	}
}

// decodeString decodes base64 XDR, forcing typeName when one is given.
func decodeString(payload, typeName string) (*lens.Value, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, fmt.Errorf("value is an empty string")
	}

	var (
		decoded *lens.Value
		err     error
	)
	if typeName != "" {
		decoded, err = lens.DecodeAs(payload, typeName)
	} else {
		decoded, err = lens.Decode(payload)
	}
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

// resolve coerces a value and reports a test failure if it cannot.
//
// Every assertion funnels its inputs through here, so a bad input always
// produces the same shape of message naming which argument was at fault.
func resolve(t TB, assertion, label string, v Value, typeName string) (*lens.Value, bool) {
	t.Helper()
	decoded, err := coerce(v, typeName)
	if err != nil {
		t.Errorf("%s: %s value could not be read: %v", assertion, label, err)
		return nil, false
	}
	return decoded, true
}

// guard converts a panic into a test failure.
//
// The generated XDR accessors dereference union arms without checking the
// discriminant, so a malformed value can panic deep inside the SDK. A panic
// escaping into someone's test binary takes down every other test in the
// package, so assertions run their work behind this.
func guard(t TB, assertion string, fn func()) (ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: internal error while inspecting the value: %v\n"+
				"This is a bug in stellar-focus; please report it with the XDR that triggered it.",
				assertion, r)
			ok = false
		}
	}()
	fn()
	return true
}
