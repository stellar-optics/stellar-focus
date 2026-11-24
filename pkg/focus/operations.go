package focus

import (
	"fmt"
	"math"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Typed operation assertions.
//
// AssertOperationTypes and AssertOperationType check the discriminator of an
// operation: that it is a payment, or that it is a change of trust. They say
// nothing about what the operation does, so a test that cares about the
// destination, the amount or the asset has to reach for the decoded
// transaction and walk it by hand.
//
// These assertions read those fields directly, so the common operations can
// be pinned in one line, and a failure says which field differs and shows
// both values — the amount in stroops and in XLM, because fee and amount
// discussions happen in both units.
//
// Amounts are in stroops, the unit the protocol stores. A destination is a
// strkey account address; a muxed address is accepted and reduced to the
// account underneath it, because that is what the ledger keys on.

// AssertPayment checks that the operation at opIndex is a payment of
// wantAmount stroops of wantAsset to wantDest.
//
// The asset is named the way the rest of the package names assets: "native"
// (or "XLM") for the native one, and "CODE:ISSUER" for a credit asset.
func AssertPayment(t TB, envelope Value, opIndex int, wantDest string, wantAmount int64, wantAsset string) bool {
	t.Helper()
	const assertion = "AssertPayment"

	op, ok := operationAt(t, assertion, envelope, opIndex)
	if !ok {
		return false
	}
	if op.Body.Type != xdr.OperationTypePayment || op.Body.PaymentOp == nil {
		t.Errorf("%s: operation %d is %s, want a payment",
			assertion, opIndex, operationTypeName(op.Body.Type))
		return false
	}
	payment := op.Body.PaymentOp

	wantSpec, ok := resolveAssetSpec(t, assertion, "asset", wantAsset)
	if !ok {
		return false
	}
	gotSpec, err := assetSpecFromAsset(payment.Asset)
	if err != nil {
		t.Errorf("%s: operation %d carries an asset that could not be read: %v",
			assertion, opIndex, err)
		return false
	}
	if gotSpec.id != wantSpec.id {
		t.Errorf("%s: operation %d asset is %s, want %s",
			assertion, opIndex, gotSpec.id, wantSpec.id)
		return false
	}

	dest, ok := muxedAddress(t, assertion, payment.Destination)
	if !ok {
		return false
	}
	want, err := addressFromString(wantDest)
	if err != nil {
		t.Errorf("%s: destination could not be read: %v", assertion, err)
		return false
	}
	if dest != want {
		t.Errorf("%s: operation %d destination is %s, want %s",
			assertion, opIndex, shorten(dest), shorten(want))
		return false
	}

	if got := int64(payment.Amount); got != wantAmount {
		t.Errorf("%s: operation %d amount is %s, want %s",
			assertion, opIndex,
			amountStroops(payment.Amount, gotSpec.unit()),
			amountStroops(xdr.Int64(wantAmount), gotSpec.unit()))
		return false
	}
	return true
}

// AssertCreateAccount checks that the operation at opIndex creates wantDest
// with a starting balance of wantStartingBalance stroops.
//
// The starting balance is what the account holds the moment it is created,
// before any fee is taken from it.
func AssertCreateAccount(t TB, envelope Value, opIndex int, wantDest string, wantStartingBalance int64) bool {
	t.Helper()
	const assertion = "AssertCreateAccount"

	op, ok := operationAt(t, assertion, envelope, opIndex)
	if !ok {
		return false
	}
	if op.Body.Type != xdr.OperationTypeCreateAccount || op.Body.CreateAccountOp == nil {
		t.Errorf("%s: operation %d is %s, want a create account",
			assertion, opIndex, operationTypeName(op.Body.Type))
		return false
	}
	create := op.Body.CreateAccountOp

	dest, ok := accountAddressOf(t, assertion, create.Destination)
	if !ok {
		return false
	}
	want, err := addressFromString(wantDest)
	if err != nil {
		t.Errorf("%s: destination could not be read: %v", assertion, err)
		return false
	}
	if dest != want {
		t.Errorf("%s: operation %d destination is %s, want %s",
			assertion, opIndex, shorten(dest), shorten(want))
		return false
	}

	if got := int64(create.StartingBalance); got != wantStartingBalance {
		t.Errorf("%s: operation %d starting balance is %s, want %s",
			assertion, opIndex,
			amountStroops(create.StartingBalance, nativeAssetSpec().unit()),
			amountStroops(xdr.Int64(wantStartingBalance), nativeAssetSpec().unit()))
		return false
	}
	return true
}

// AssertChangeTrust checks that the operation at opIndex changes trust in
// wantAsset to a limit of wantLimit stroops.
//
// The limit is the most the account is willing to hold of the asset, in
// stroops. The largest possible limit means "no limit", and is reported as
// such, because a limit of 9223372036854775807 read as a plain number tells
// the reader nothing.
func AssertChangeTrust(t TB, envelope Value, opIndex int, wantAsset string, wantLimit int64) bool {
	t.Helper()
	const assertion = "AssertChangeTrust"

	op, ok := operationAt(t, assertion, envelope, opIndex)
	if !ok {
		return false
	}
	if op.Body.Type != xdr.OperationTypeChangeTrust || op.Body.ChangeTrustOp == nil {
		t.Errorf("%s: operation %d is %s, want a change of trust",
			assertion, opIndex, operationTypeName(op.Body.Type))
		return false
	}
	change := op.Body.ChangeTrustOp

	wantSpec, ok := resolveAssetSpec(t, assertion, "asset", wantAsset)
	if !ok {
		return false
	}
	gotSpec, err := assetSpecFromChangeTrustAsset(change.Line)
	if err != nil {
		t.Errorf("%s: operation %d carries an asset that could not be read: %v",
			assertion, opIndex, err)
		return false
	}
	if gotSpec.id != wantSpec.id {
		t.Errorf("%s: operation %d asset is %s, want %s",
			assertion, opIndex, gotSpec.id, wantSpec.id)
		return false
	}

	if got := int64(change.Limit); got != wantLimit {
		t.Errorf("%s: operation %d limit is %s, want %s",
			assertion, opIndex,
			describeTrustLimit(got, gotSpec.unit()),
			describeTrustLimit(wantLimit, wantSpec.unit()))
		return false
	}
	return true
}

// resolveAssetSpec reads an asset named in a string.
func resolveAssetSpec(t TB, assertion, label, want string) (assetSpec, bool) {
	t.Helper()

	spec, err := assetSpecFromString(want)
	if err != nil {
		t.Errorf("%s: %s could not be read as an asset: %v", assertion, label, err)
		return assetSpec{}, false
	}
	return spec, true
}

// muxedAddress reduces a muxed account to the address of the account
// underneath it.
func muxedAddress(t TB, assertion string, m xdr.MuxedAccount) (string, bool) {
	t.Helper()

	var (
		addr string
		err  error
	)
	if !guard(t, assertion, func() {
		account := m.ToAccountId()
		addr, err = account.GetAddress()
	}) {
		return "", false
	}
	if err != nil {
		t.Errorf("%s: destination could not be read: %v", assertion, err)
		return "", false
	}
	return addr, true
}

// accountAddressOf renders an account id as its strkey address.
func accountAddressOf(t TB, assertion string, id xdr.AccountId) (string, bool) {
	t.Helper()

	var (
		addr string
		err  error
	)
	// GetAddress has a pointer receiver, so it takes a copy of the id.
	if !guard(t, assertion, func() {
		account := id
		addr, err = account.GetAddress()
	}) {
		return "", false
	}
	if err != nil {
		t.Errorf("%s: destination could not be read: %v", assertion, err)
		return "", false
	}
	return addr, true
}

// describeTrustLimit renders a trust limit, naming the "no limit" case rather
// than printing the largest int64 as if it were an amount.
func describeTrustLimit(limit int64, unit string) string {
	if limit == math.MaxInt64 {
		return "no limit"
	}
	return amountStroops(xdr.Int64(limit), unit)
}

// OpMatcher is a functional predicate for matching an operation.
// It returns an error describing why the operation did not match, or nil if it matched.
type OpMatcher func(op *xdr.Operation) error

// MatchPayment returns an OpMatcher that checks if an operation is a Payment.
// If dest is non-empty, it verifies the payment destination address.
// If amount >= 0, it verifies the payment amount in stroops.
func MatchPayment(dest string, amount int64) OpMatcher {
	return func(op *xdr.Operation) error {
		if op == nil {
			return fmt.Errorf("operation is nil")
		}
		if op.Body.Type != xdr.OperationTypePayment || op.Body.PaymentOp == nil {
			return fmt.Errorf("operation is %s, want Payment", operationTypeName(op.Body.Type))
		}
		payment := op.Body.PaymentOp
		if dest != "" {
			var gotDest string
			account := payment.Destination.ToAccountId()
			if addr, err := account.GetAddress(); err == nil {
				gotDest = addr
			}
			if wantAddr, err := addressFromString(dest); err == nil {
				if gotDest != wantAddr {
					return fmt.Errorf("payment destination is %s, want %s", shorten(gotDest), shorten(wantAddr))
				}
			} else if gotDest != dest {
				return fmt.Errorf("payment destination is %s, want %s", shorten(gotDest), shorten(dest))
			}
		}
		if amount >= 0 {
			if int64(payment.Amount) != amount {
				return fmt.Errorf("payment amount is %d stroops, want %d", payment.Amount, amount)
			}
		}
		return nil
	}
}

// MatchSetOptions returns an OpMatcher that checks if an operation is a SetOptions operation.
func MatchSetOptions() OpMatcher {
	return func(op *xdr.Operation) error {
		if op == nil {
			return fmt.Errorf("operation is nil")
		}
		if op.Body.Type != xdr.OperationTypeSetOptions || op.Body.SetOptionsOp == nil {
			return fmt.Errorf("operation is %s, want SetOptions", operationTypeName(op.Body.Type))
		}
		return nil
	}
}

// MatchInvokeHost returns an OpMatcher that checks if an operation is an InvokeHostFunction operation.
func MatchInvokeHost() OpMatcher {
	return func(op *xdr.Operation) error {
		if op == nil {
			return fmt.Errorf("operation is nil")
		}
		if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
			return fmt.Errorf("operation is %s, want InvokeHostFunction", operationTypeName(op.Body.Type))
		}
		return nil
	}
}

// AssertOperationsMatch verifies a transaction envelope's operation list against a sequence of OpMatchers.
// It reports a mismatch at the first failing matcher, or if the number of operations does not match.
func AssertOperationsMatch(t TestingT, envInput any, matchers ...OpMatcher) bool {
	t.Helper()
	const assertion = "AssertOperationsMatch"

	env, _, ok := envelopeFrom(t, assertion, envInput)
	if !ok {
		return false
	}
	var (
		ops []xdr.Operation
		err error
	)
	if !guard(t, assertion, func() { ops, err = transactionOperations(env) }) {
		return false
	}
	if err != nil {
		t.Errorf("%s: %v", assertion, err)
		return false
	}

	if len(ops) != len(matchers) {
		t.Errorf("%s: transaction has %d operation(s), want %d to match", assertion, len(ops), len(matchers))
		return false
	}

	for i, m := range matchers {
		if m == nil {
			continue
		}
		if err := m(&ops[i]); err != nil {
			t.Errorf("%s: operation %d does not match: %v", assertion, i, err)
			return false
		}
	}
	return true
}
