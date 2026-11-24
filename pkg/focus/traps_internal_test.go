package focus

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// TestDescribeScError covers the union's arms and its malformed cases, which
// cannot be built through the XDR encoder and so are tested here rather than
// through a meta.
func TestDescribeScError(t *testing.T) {
	t.Parallel()

	code := xdr.Uint32(4)
	hostCode := xdr.ScErrorCodeScecInvalidAction
	unknownHostCode := xdr.ScErrorCode(99)

	tests := []struct {
		name string
		err  xdr.ScError
		want string
	}{
		{
			name: "contract code",
			err:  xdr.ScError{Type: xdr.ScErrorTypeSceContract, ContractCode: &code},
			want: "Error(Contract, #4)",
		},
		{
			name: "contract error without its code",
			err:  xdr.ScError{Type: xdr.ScErrorTypeSceContract},
			want: "Error(Contract)",
		},
		{
			name: "named host code",
			err:  xdr.ScError{Type: xdr.ScErrorTypeSceWasmVm, Code: &hostCode},
			want: "Error(WasmVm, InvalidAction)",
		},
		{
			name: "host error without its code",
			err:  xdr.ScError{Type: xdr.ScErrorTypeSceContext},
			want: "Error(Context)",
		},
		{
			name: "host code outside the enum",
			err:  xdr.ScError{Type: xdr.ScErrorTypeSceAuth, Code: &unknownHostCode},
			want: "Error(Auth, code_99)",
		},
		{
			name: "error type outside the enum",
			err:  xdr.ScError{Type: xdr.ScErrorType(99)},
			want: "Error(type_99)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := describeScError(tc.err); got != tc.want {
				t.Errorf("describeScError() = %q, want %q", got, tc.want)
			}
		})
	}
}
