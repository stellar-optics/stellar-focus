package focus

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

type assertionRecorder struct{ messages []string }

func (r *assertionRecorder) Helper() {}
func (r *assertionRecorder) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}
func (r *assertionRecorder) Fatalf(format string, args ...any) { r.Errorf(format, args...) }
func (r *assertionRecorder) Name() string                      { return "assertion" }

func TestAssertAccountFlags(t *testing.T) {
	set := xdr.Uint32(xdr.AccountFlagsAuthRequiredFlag | xdr.AccountFlagsAuthRevocableFlag)
	clear := xdr.Uint32(xdr.AccountFlagsAuthImmutableFlag)
	op := xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeSetOptions, SetOptionsOp: &xdr.SetOptionsOp{SetFlags: &set, ClearFlags: &clear}}}
	if !AssertAccountFlags(t, op, xdr.AccountFlags(set), xdr.AccountFlags(clear)) {
		t.Fatal("expected account flag masks to match")
	}
	recorder := &assertionRecorder{}
	if AssertAccountFlags(recorder, op, 0, 0) {
		t.Fatal("expected mismatched flags to fail")
	}
	if len(recorder.messages) != 1 || !strings.Contains(recorder.messages[0], "AUTH_REVOCABLE") || !strings.Contains(recorder.messages[0], "AUTH_IMMUTABLE") {
		t.Fatalf("expected named flag diagnostics, got %v", recorder.messages)
	}
	if AssertAccountFlags(recorder, xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypePayment}}, 0, 0) {
		t.Fatal("expected non-SetOptions to fail")
	}
}
