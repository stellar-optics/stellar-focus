package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// operationResultWithCode builds a bare operation result carrying an
// operation-level code, as opposed to an opINNER result.
func operationResultWithCode(code xdr.OperationResultCode) xdr.OperationResult {
	return xdr.OperationResult{Code: code}
}

func TestAssertOperationResultCodePasses(t *testing.T) {
	t.Parallel()

	mixed := buildResult(t, xdr.TransactionResultCodeTxFailed,
		paymentResult(xdr.PaymentResultCodePaymentSuccess),
		paymentResult(xdr.PaymentResultCodePaymentUnderfunded))
	succeeded := buildResult(t, xdr.TransactionResultCodeTxSuccess,
		paymentResult(xdr.PaymentResultCodePaymentSuccess))
	wrapperCode := buildResult(t, xdr.TransactionResultCodeTxFailed,
		operationResultWithCode(xdr.OperationResultCodeOpBadAuth))

	tests := []struct {
		name   string
		result string
		index  int
		want   string
	}{
		{"failure code on a failed operation", mixed, 1, "payment_underfunded"},
		{"success code on a successful operation", mixed, 0, "payment_success"},
		{"success code inside a failed transaction", mixed, 0, "payment_success"},
		{"operation-level wrapper code", wrapperCode, 0, "op_bad_auth"},
		{"success code in a successful transaction", succeeded, 0, "payment_success"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertOperationResultCode(tb, tc.result, tc.index, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("passing assertion reported a failure:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertOperationResultCodeFailureMessages checks the messages for the two
// ways this assertion fails: the operation answered with a different code, and
// the expected code could never match because it is not a real short code.
func TestAssertOperationResultCodeFailureMessages(t *testing.T) {
	t.Parallel()

	failed := buildResult(t, xdr.TransactionResultCodeTxFailed,
		paymentResult(xdr.PaymentResultCodePaymentSuccess),
		paymentResult(xdr.PaymentResultCodePaymentNoTrust))

	tests := []struct {
		name     string
		result   focus.Value
		index    int
		want     string
		contains []string
	}{
		{
			name:     "wrong code explains both sides",
			result:   failed,
			index:    1,
			want:     "payment_underfunded",
			contains: []string{"operation 1", "payment_no_trust", `want "payment_underfunded"`, "trustline"},
		},
		{
			name:     "typo is named as an unknown code",
			result:   failed,
			index:    1,
			want:     "paymnt_underfunded",
			contains: []string{"not a known result code", "short forms", "paymnt_underfunded"},
		},
		{
			name:     "xdr constant is named as the wrong form",
			result:   failed,
			index:    1,
			want:     "PaymentResultCodePaymentUnderfunded",
			contains: []string{"XDR constant", "payment_underfunded"},
		},
		{
			name:     "index out of range names the transaction size",
			result:   failed,
			index:    7,
			want:     "payment_success",
			contains: []string{"no operation at index 7", "2 operation(s)"},
		},

		{
			name:     "bad result input names the argument",
			result:   nil,
			index:    0,
			want:     "payment_success",
			contains: []string{"AssertOperationResultCode", "value could not be read"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertOperationResultCode(tb, tc.result, tc.index, tc.want) {
				t.Fatal("assertion passed when it should have failed")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not contain %q:\n%s", want, msg)
				}
			}
		})
	}
}

func feeBumpResultValue(outerCode xdr.TransactionResultCode, innerCode xdr.TransactionResultCode) xdr.TransactionResult {
	var ops *[]xdr.OperationResult
	if innerCode == xdr.TransactionResultCodeTxSuccess || innerCode == xdr.TransactionResultCodeTxFailed {
		empty := []xdr.OperationResult{}
		ops = &empty
	}
	return xdr.TransactionResult{
		FeeCharged: 100,
		Result: xdr.TransactionResultResult{
			Code: outerCode,
			InnerResultPair: &xdr.InnerTransactionResultPair{
				TransactionHash: xdr.Hash{1, 2, 3},
				Result: xdr.InnerTransactionResult{
					FeeCharged: 0,
					Result: xdr.InnerTransactionResultResult{
						Code:    innerCode,
						Results: ops,
					},
				},
			},
		},
	}
}

func TestAssertFeeBumpInnerResultCode(t *testing.T) {
	t.Parallel()

	fbFailed := feeBumpResultValue(xdr.TransactionResultCodeTxFeeBumpInnerFailed, xdr.TransactionResultCodeTxBadSeq)
	fbSuccess := feeBumpResultValue(xdr.TransactionResultCodeTxFeeBumpInnerSuccess, xdr.TransactionResultCodeTxSuccess)
	regularFailed := buildResult(t, xdr.TransactionResultCodeTxFailed, paymentResult(xdr.PaymentResultCodePaymentUnderfunded))

	t.Run("inner failure matches expected short code", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertFeeBumpInnerResultCode(tb, fbFailed, "tx_bad_seq") {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.FeeBumpInnerResultCode(t, fbFailed, "tx_bad_seq")
	})

	t.Run("inner success matches expected short code", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertFeeBumpInnerResultCode(tb, fbSuccess, "tx_success") {
			t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
		}
		require.FeeBumpInnerResultCode(t, fbSuccess, "tx_success")
	})

	t.Run("inner code mismatch reports outer vs inner and remediation", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertFeeBumpInnerResultCode(tb, fbFailed, "tx_bad_auth") {
			t.Fatal("assertion passed when inner code mismatched")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, `inner transaction result code is "tx_bad_seq", want "tx_bad_auth"`) {
			t.Errorf("message did not report inner codes:\n%s", msg)
		}
		if !strings.Contains(msg, "outer:") || !strings.Contains(msg, "tx_fee_bump_inner_failed") {
			t.Errorf("message did not report outer code:\n%s", msg)
		}
		if !strings.Contains(msg, "got inner:") || !strings.Contains(msg, "sequence") {
			t.Errorf("message did not include plain-English remediation:\n%s", msg)
		}
	})

	t.Run("non-fee-bump transaction result fails", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertFeeBumpInnerResultCode(tb, regularFailed, "tx_bad_seq") {
			t.Fatal("assertion passed for non-fee-bump result")
		}
		if !strings.Contains(tb.Output(), "is not a fee-bump transaction with an inner result") {
			t.Errorf("message did not report that result is not a fee-bump:\n%s", tb.Output())
		}
	})

	t.Run("invalid result input fails", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertFeeBumpInnerResultCode(tb, nil, "tx_bad_seq") {
			t.Fatal("assertion passed for nil input")
		}
		if !strings.Contains(tb.Output(), "could not be read") {
			t.Errorf("message did not report unreadable input:\n%s", tb.Output())
		}
	})
}
