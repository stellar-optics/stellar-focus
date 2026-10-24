package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// createAccountOpFor builds a create-account operation.
func createAccountOpFor(tb testing.TB, dest string, startingBalance int64) xdr.Operation {
	tb.Helper()

	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeCreateAccount,
		CreateAccountOp: &xdr.CreateAccountOp{
			Destination:     mustAccountID(tb, dest),
			StartingBalance: xdr.Int64(startingBalance),
		},
	}}
}

// changeTrustOpFor builds a change-trust operation for a credit asset.
func changeTrustOpFor(tb testing.TB, code string, limit int64) xdr.Operation {
	tb.Helper()

	var asset xdr.Asset
	if err := asset.SetCredit(code, mustAccountID(tb, testIssuer)); err != nil {
		tb.Fatalf("building asset %s: %v", code, err)
	}

	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeChangeTrust,
		ChangeTrustOp: &xdr.ChangeTrustOp{
			Line:  asset.ToChangeTrustAsset(),
			Limit: xdr.Int64(limit),
		},
	}}
}

// usdc names the credit asset the operation tests use.
const usdc = "USDC:" + testIssuer

func TestAssertPayment(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t,
		paymentOp(t, 100_0000000),
		creditPaymentOp(t, 5),
	)

	t.Run("the payment matches", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertPayment(tb, env, 0, testDest, 100_0000000, "native") {
			t.Errorf("the payment did not match:\n%s", tb.Output())
		}
	})

	t.Run("a credit asset payment matches", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertPayment(tb, env, 1, testDest, 5, usdc) {
			t.Errorf("the payment did not match:\n%s", tb.Output())
		}
	})

	t.Run("the amount differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertPayment(tb, env, 0, testDest, 200_0000000, "native") {
			t.Fatal("the assertion passed with the wrong amount")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"AssertPayment",
			"amount is 1000000000 (100.0000000 XLM)",
			"want 2000000000 (200.0000000 XLM)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("the destination differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertPayment(tb, env, 0, testSource, 100_0000000, "native") {
			t.Fatal("the assertion passed with the wrong destination")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "destination is") || !strings.Contains(msg, "want") {
			t.Errorf("the message does not name the destination:\n%s", msg)
		}
	})

	t.Run("the asset differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertPayment(tb, env, 0, testDest, 100_0000000, usdc) {
			t.Fatal("the assertion passed with the wrong asset")
		}
		msg, _ := tb.Message()
		for _, want := range []string{"asset is native", "want " + usdc} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("the operation is not a payment", func(t *testing.T) {
		t.Parallel()

		other := buildEnvelope(t, createAccountOpFor(t, testDest, 20_0000000))

		tb := faketb.New()
		if focus.AssertPayment(tb, other, 0, testDest, 5, "native") {
			t.Fatal("the assertion passed on a create account")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "is create_account, want a payment") {
			t.Errorf("the message does not name the operation:\n%s", msg)
		}
	})

	t.Run("there is no operation at that index", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertPayment(tb, env, 9, testDest, 1, "native") {
			t.Fatal("the assertion passed with no operation at that index")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "no operation at index 9") {
			t.Errorf("the message does not explain the mistake:\n%s", msg)
		}
	})

	t.Run("the asset cannot be read", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertPayment(tb, env, 0, testDest, 1, "not an asset") {
			t.Fatal("the assertion passed with an unreadable asset")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "could not be read as an asset") {
			t.Errorf("the message does not explain the mistake:\n%s", msg)
		}
	})
}

func TestAssertCreateAccount(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, createAccountOpFor(t, testDest, 20_0000000))

	t.Run("the account matches", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertCreateAccount(tb, env, 0, testDest, 20_0000000) {
			t.Errorf("the create account did not match:\n%s", tb.Output())
		}
	})

	t.Run("the starting balance differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertCreateAccount(tb, env, 0, testDest, 10_0000000) {
			t.Fatal("the assertion passed with the wrong starting balance")
		}
		msg, _ := tb.Message()
		for _, want := range []string{
			"starting balance is 200000000 (20.0000000 XLM)",
			"want 100000000 (10.0000000 XLM)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("the destination differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertCreateAccount(tb, env, 0, testSource, 20_0000000) {
			t.Fatal("the assertion passed with the wrong destination")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "destination is") {
			t.Errorf("the message does not name the destination:\n%s", msg)
		}
	})
}

// TestAssertCreateAccountOnTheWrongOperation checks that the assertion names
// the operation it found rather than reporting a field mismatch.
func TestAssertCreateAccountOnTheWrongOperation(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 100))

	tb := faketb.New()
	if focus.AssertCreateAccount(tb, env, 0, testDest, 1) {
		t.Fatal("the assertion passed on a payment")
	}
	msg, _ := tb.Message()
	if !strings.Contains(msg, "is payment, want a create account") {
		t.Errorf("the message does not name the operation:\n%s", msg)
	}
}

func TestAssertChangeTrust(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, changeTrustOpFor(t, "USDC", 1000_0000000))

	t.Run("the change of trust matches", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertChangeTrust(tb, env, 0, usdc, 1000_0000000) {
			t.Errorf("the change of trust did not match:\n%s", tb.Output())
		}
	})

	t.Run("the limit differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertChangeTrust(tb, env, 0, usdc, 500_0000000) {
			t.Fatal("the assertion passed with the wrong limit")
		}
		msg, _ := tb.Message()
		for _, want := range []string{
			"limit is 10000000000 (1000.0000000)",
			"want 5000000000 (500.0000000)",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("the asset differs", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertChangeTrust(tb, env, 0, "native", 1000_0000000) {
			t.Fatal("the assertion passed with the wrong asset")
		}
		msg, _ := tb.Message()
		if !strings.Contains(msg, "asset is "+usdc) {
			t.Errorf("the message does not name the asset:\n%s", msg)
		}
	})
}

// TestAssertChangeTrustOnTheWrongOperation checks that the assertion names
// the operation it found rather than reporting a field mismatch.
func TestAssertChangeTrustOnTheWrongOperation(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 100))

	tb := faketb.New()
	if focus.AssertChangeTrust(tb, env, 0, usdc, 1) {
		t.Fatal("the assertion passed on a payment")
	}
	msg, _ := tb.Message()
	if !strings.Contains(msg, "is payment, want a change of trust") {
		t.Errorf("the message does not name the operation:\n%s", msg)
	}
}

// TestAssertChangeTrustWithNoLimit checks that the largest possible limit is
// reported as what it means rather than as a number.
func TestAssertChangeTrustWithNoLimit(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, changeTrustOpFor(t, "USDC", int64(1<<63-1)))

	tb := faketb.New()
	if focus.AssertChangeTrust(tb, env, 0, usdc, 1) {
		t.Fatal("the assertion passed with the wrong limit")
	}
	msg, _ := tb.Message()
	if !strings.Contains(msg, "no limit") {
		t.Errorf("the message does not explain the limit:\n%s", msg)
	}
}
