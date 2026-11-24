package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func TestAssertOperationSequencePasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		envelope xdr.TransactionEnvelope
		want     []xdr.OperationType
	}{
		{
			"payment alone",
			envelopeValue(t, paymentOp(t, 100)),
			[]xdr.OperationType{xdr.OperationTypePayment},
		},
		{
			"multi-operation order",
			envelopeValue(t, changeTrustOp(t), paymentOp(t, 100), bumpSequenceOp(t)),
			[]xdr.OperationType{
				xdr.OperationTypeChangeTrust,
				xdr.OperationTypePayment,
				xdr.OperationTypeBumpSequence,
			},
		},
		{
			"repeated types keep their positions",
			envelopeValue(t, paymentOp(t, 100), changeTrustOp(t), paymentOp(t, 200)),
			[]xdr.OperationType{
				xdr.OperationTypePayment,
				xdr.OperationTypeChangeTrust,
				xdr.OperationTypePayment,
			},
		},
		{
			"no operations",
			envelopeValue(t),
			nil,
		},
		{
			"fee bump reads the inner transaction's operations",
			feeBumpEnvelopeValue(t, envelopeValue(t, paymentOp(t, 100), changeTrustOp(t)), nil, nil),
			[]xdr.OperationType{xdr.OperationTypePayment, xdr.OperationTypeChangeTrust},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertOperationSequence(tb, tc.envelope, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertOperationSequenceMessages checks that a failure names the first
// position that diverges and shows both sequences with their indexes, so a
// swap is visible without comparing two bracketed lists by eye.
func TestAssertOperationSequenceMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		envelope xdr.TransactionEnvelope
		want     []xdr.OperationType
		contains []string
	}{
		{
			name:     "wrong type at index 1",
			envelope: envelopeValue(t, paymentOp(t, 100), changeTrustOp(t), bumpSequenceOp(t)),
			want: []xdr.OperationType{
				xdr.OperationTypePayment,
				xdr.OperationTypeCreateAccount,
				xdr.OperationTypeBumpSequence,
			},
			contains: []string{
				"AssertOperationSequence: operation sequence differs at index 1",
				`it is "change_trust", want "create_account"`,
				"got   [0] payment  [1] change_trust  [2] bump_sequence",
				"want  [0] payment  [1] create_account  [2] bump_sequence",
			},
		},
		{
			name:     "too few operations",
			envelope: envelopeValue(t, paymentOp(t, 100), changeTrustOp(t)),
			want: []xdr.OperationType{
				xdr.OperationTypePayment,
				xdr.OperationTypeChangeTrust,
				xdr.OperationTypeBumpSequence,
			},
			contains: []string{
				"the transaction has 2 operation(s), want 3",
				`index 2 should be "bump_sequence"`,
			},
		},
		{
			name:     "too many operations",
			envelope: envelopeValue(t, paymentOp(t, 100), changeTrustOp(t)),
			want:     []xdr.OperationType{xdr.OperationTypePayment},
			contains: []string{
				"the transaction has 2 operation(s), want 1",
				"index 1 should not exist",
			},
		},
		{
			name:     "swapped operations",
			envelope: envelopeValue(t, paymentOp(t, 100), changeTrustOp(t)),
			want:     []xdr.OperationType{xdr.OperationTypeChangeTrust, xdr.OperationTypePayment},
			contains: []string{
				"differs at index 0",
				`it is "payment", want "change_trust"`,
			},
		},
		{
			name:     "no operations present",
			envelope: envelopeValue(t),
			want:     []xdr.OperationType{xdr.OperationTypePayment},
			contains: []string{
				"got   (no operations)",
				`index 0 should be "payment"`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertOperationSequence(tb, tc.envelope, tc.want) {
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

// TestOperationSequenceNames checks the snake_case names mapped from the XDR
// constants, including the multi-word cases an underscore algorithm gets
// wrong.
func TestOperationSequenceNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		opType xdr.OperationType
		want   string
	}{
		{xdr.OperationTypePathPaymentStrictReceive, "path_payment_strict_receive"},
		{xdr.OperationTypePathPaymentStrictSend, "path_payment_strict_send"},
		{xdr.OperationTypeCreatePassiveSellOffer, "create_passive_sell_offer"},
		{xdr.OperationTypeSetTrustLineFlags, "set_trust_line_flags"},
		{xdr.OperationTypeBeginSponsoringFutureReserves, "begin_sponsoring_future_reserves"},
		{xdr.OperationTypeExtendFootprintTtl, "extend_footprint_ttl"},
		{xdr.OperationTypeInvokeHostFunction, "invoke_host_function"},
		{xdr.OperationType(99), "operation_type_99"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertOperationSequence(tb, envelopeValue(t, paymentOp(t, 100)), []xdr.OperationType{tc.opType}) {
				t.Fatal("assertion passed with the wrong operation type")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("%v", err)
			}
			if !strings.Contains(msg, "want  [0] "+tc.want) {
				t.Errorf("message does not render %s as %q:\n%s", tc.opType, tc.want, msg)
			}
		})
	}
}

// TestOperationSequenceNamesMatchLens checks that the names this assertion
// builds for wanted types are the same words lens uses for operations it
// reports, so the two sides of a failure never disagree.
func TestOperationSequenceNamesMatchLens(t *testing.T) {
	t.Parallel()

	ops := []xdr.Operation{
		paymentOp(t, 100),
		createAccountOp(t),
		changeTrustOp(t),
		bumpSequenceOp(t),
		manageDataOp(t),
	}

	for i, op := range ops {
		env := envelopeValue(t, op)

		lensSummary := focus.Summarize(faketb.New(), env)
		if lensSummary == nil || len(lensSummary.Operations) != 1 {
			t.Fatalf("operation %d: lens did not summarise the operation", i)
		}
		lensName := lensSummary.Operations[0].Type

		tb := faketb.New()
		focus.AssertOperationSequence(tb, env, nil)
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("operation %d: %v", i, err)
		}
		if !strings.Contains(msg, "[0] "+lensName) {
			t.Errorf("operation %d: message does not use lens's name %q:\n%s", i, lensName, msg)
		}
	}
}
