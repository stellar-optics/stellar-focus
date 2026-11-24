package focus_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// accountLedgerKeyValue builds the ledger key of an account, for the
// footprint and key-function tests.
func accountLedgerKeyValue(tb testing.TB, addr string) xdr.LedgerKey {
	tb.Helper()

	var key xdr.LedgerKey
	if err := key.SetAccount(mustAccountID(tb, addr)); err != nil {
		tb.Fatalf("building ledger key for %s: %v", addr, err)
	}
	return key
}

// footprintValue builds Soroban transaction data whose footprint carries the
// given keys, in the order they are given.
func footprintValue(readOnly, readWrite []xdr.LedgerKey) xdr.SorobanTransactionData {
	return xdr.SorobanTransactionData{
		Resources: xdr.SorobanResources{
			Footprint: xdr.LedgerFootprint{
				ReadOnly:  readOnly,
				ReadWrite: readWrite,
			},
		},
	}
}

// contractAddressValue builds a contract address from a repeated byte, and
// returns its strkey form alongside it.
func contractAddressValue(tb testing.TB, seed byte) (xdr.ScAddress, string) {
	tb.Helper()

	var id xdr.ContractId
	for i := range id {
		id[i] = seed
	}
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}
	encoded, err := strkey.Encode(strkey.VersionByteContract, id[:])
	if err != nil {
		tb.Fatalf("encoding contract address: %v", err)
	}
	return addr, encoded
}

// sorobanAuthEntry builds an authorization entry whose credentials are
// deliberately distinct for each nonce and seed. The authorized invocation is
// fixed, so comparisons isolate the credentials.
func sorobanAuthEntry(tb testing.TB, nonce int64, credentialSeed byte) xdr.SorobanAuthorizationEntry {
	tb.Helper()

	credentialAddr, _ := contractAddressValue(tb, credentialSeed)
	invokedAddr, _ := contractAddressValue(tb, 9)
	payload := xdr.Uint32(credentialSeed)

	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: &xdr.SorobanAddressCredentials{
				Address:                   credentialAddr,
				Nonce:                     xdr.Int64(nonce),
				SignatureExpirationLedger: 42,
				Signature:                 xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &payload},
			},
		},
		RootInvocation: xdr.SorobanAuthorizedInvocation{
			Function: xdr.SorobanAuthorizedFunction{
				Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
				ContractFn: &xdr.InvokeContractArgs{
					ContractAddress: invokedAddr,
					FunctionName:    "invoke",
				},
			},
			SubInvocations: []xdr.SorobanAuthorizedInvocation{{
				Function: xdr.SorobanAuthorizedFunction{
					Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
					ContractFn: &xdr.InvokeContractArgs{
						ContractAddress: invokedAddr,
						FunctionName:    "sub",
					},
				},
			}},
		},
	}
}

// sorobanInvokeEnvelope builds an envelope whose single operation invokes a
// contract with the given authorization entries.
func sorobanInvokeEnvelope(
	tb testing.TB, contractAddr xdr.ScAddress, auth ...xdr.SorobanAuthorizationEntry,
) string {
	tb.Helper()

	return mustMarshal(tb, envelopeValue(tb, xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeInvokeHostFunction,
		InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
			HostFunction: xdr.HostFunction{
				Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
				InvokeContract: &xdr.InvokeContractArgs{
					ContractAddress: contractAddr,
					FunctionName:    "invoke",
				},
			},
			Auth: auth,
		},
	}}))
}

// mustPanicWith runs fn and fails the test unless it panics with a message
// containing want.
func mustPanicWith(t *testing.T, want string, fn func()) {
	t.Helper()

	defer func() {
		t.Helper()
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected a panic containing %q, got none", want)
		}
		if msg := fmt.Sprint(recovered); !strings.Contains(msg, want) {
			t.Fatalf("panic %q does not contain %q", msg, want)
		}
	}()
	fn()
}

// TestSortPathKeyFunction covers the optional key function: it is passed each
// decoded element, controls the ordering, and falls back to canonical ordering
// when it panics.
func TestSortPathKeyFunction(t *testing.T) {
	t.Parallel()

	want := footprintValue(
		[]xdr.LedgerKey{accountLedgerKeyValue(t, testSource), accountLedgerKeyValue(t, testDest)},
		nil,
	)
	got := footprintValue(
		[]xdr.LedgerKey{accountLedgerKeyValue(t, testDest), accountLedgerKeyValue(t, testSource)},
		nil,
	)

	t.Run("the key function orders the elements", func(t *testing.T) {
		t.Parallel()

		var calls int
		keyFn := func(elem any) string {
			calls++
			n, ok := elem.(*lens.Node)
			if !ok {
				return ""
			}
			return n.Child("Account").Child("AccountId").String()
		}

		tb := faketb.New()
		if !focus.AssertEqualAs(tb, "SorobanTransactionData", want, got,
			focus.SortPath("**.Footprint.ReadOnly", keyFn)) {
			t.Errorf("the keyed sort did not treat the keys as a set:\n%s", tb.Output())
		}
		if calls == 0 {
			t.Error("the key function was never called")
		}
	})

	t.Run("a key that cannot distinguish elements does not hide the order", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertEqualAs(tb, "SorobanTransactionData", want, got,
			focus.SortPath("**.Footprint.ReadOnly", func(any) string { return "same" })) {
			t.Fatal("a constant key function made reordered keys compare equal")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "ReadOnly") {
			t.Errorf("the diff does not name the reordered array:\n%s", msg)
		}
	})

	t.Run("a panicking key function falls back to canonical order", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertEqualAs(tb, "SorobanTransactionData", want, got,
			focus.SortPath("**.Footprint.ReadOnly", func(any) string { panic("boom") })) {
			t.Errorf("a panicking key function did not fall back:\n%s", tb.Output())
		}
	})
}

// TestSortSigners treats the signer list as a set while leaving the weights
// meaningful.
func TestSortSigners(t *testing.T) {
	t.Parallel()

	thresholds := xdr.NewThreshold(1, 0, 0, 0)
	want := xdr.AccountEntry{
		AccountId:  mustAccountID(t, testSource),
		Balance:    100,
		Thresholds: thresholds,
		Signers: []xdr.Signer{
			signerValue(t, testDest, 2),
			signerValue(t, testIssuer, 5),
		},
	}
	got := want
	got.Signers = []xdr.Signer{
		signerValue(t, testIssuer, 5),
		signerValue(t, testDest, 2),
	}

	t.Run("order alone is a difference", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertEqualAs(tb, "AccountEntry", want, got) {
			t.Fatal("reordered signers compared equal without the option")
		}
	})

	t.Run("SortSigners ignores the order", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertEqualAs(tb, "AccountEntry", want, got, focus.SortSigners()) {
			t.Errorf("reordered signers were reported as a difference:\n%s", tb.Output())
		}
	})

	t.Run("a wrong weight is still a difference", func(t *testing.T) {
		t.Parallel()

		changed := got
		changed.Signers = []xdr.Signer{
			signerValue(t, testIssuer, 4),
			signerValue(t, testDest, 2),
		}

		tb := faketb.New()
		if focus.AssertEqualAs(tb, "AccountEntry", want, changed, focus.SortSigners()) {
			t.Fatal("a wrong signer weight was reported as equal")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "Weight") {
			t.Errorf("the diff does not name the changed weight:\n%s", msg)
		}
	})
}

// TestSortFootprint treats both sides of a Soroban footprint as sets.
func TestSortFootprint(t *testing.T) {
	t.Parallel()

	want := footprintValue(
		[]xdr.LedgerKey{accountLedgerKeyValue(t, testSource), accountLedgerKeyValue(t, testDest)},
		[]xdr.LedgerKey{accountLedgerKeyValue(t, testIssuer)},
	)
	got := footprintValue(
		[]xdr.LedgerKey{accountLedgerKeyValue(t, testDest), accountLedgerKeyValue(t, testSource)},
		[]xdr.LedgerKey{accountLedgerKeyValue(t, testIssuer)},
	)

	t.Run("order alone is a difference", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertEqualAs(tb, "SorobanTransactionData", want, got) {
			t.Fatal("reordered read-only keys compared equal without the option")
		}
	})

	t.Run("SortFootprint ignores both orders", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if !focus.AssertEqualAs(tb, "SorobanTransactionData", want, got, focus.SortFootprint()) {
			t.Errorf("reordered footprint keys were reported as a difference:\n%s", tb.Output())
		}
	})

	t.Run("a missing key is still a difference", func(t *testing.T) {
		t.Parallel()

		changed := footprintValue(
			[]xdr.LedgerKey{accountLedgerKeyValue(t, testSource)},
			[]xdr.LedgerKey{accountLedgerKeyValue(t, testIssuer)},
		)

		tb := faketb.New()
		if focus.AssertEqualAs(tb, "SorobanTransactionData", want, changed, focus.SortFootprint()) {
			t.Fatal("a missing footprint key was reported as equal")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "ReadOnly") {
			t.Errorf("the diff does not name the read-only footprint:\n%s", msg)
		}
	})
}

// TestSortPathStalePatterns keeps the warning that makes sort rules
// trustworthy: a pattern that matched nothing fails the assertion even when
// the values are equal.
func TestSortPathStalePatterns(t *testing.T) {
	t.Parallel()

	entry := xdr.AccountEntry{
		AccountId:  mustAccountID(t, testSource),
		Balance:    100,
		Thresholds: xdr.NewThreshold(1, 0, 0, 0),
		Signers:    []xdr.Signer{signerValue(t, testDest, 2)},
	}

	tb := faketb.New()
	if focus.AssertEqualAs(tb, "AccountEntry", entry, entry, focus.SortPath("**.SignersTypo")) {
		t.Fatal("a sort pattern that matched nothing was accepted silently")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(msg, "**.SignersTypo") {
		t.Errorf("the stale pattern is not named:\n%s", msg)
	}
}

// TestPatternValidation covers the anti-typo rule: a malformed ignore or
// normalize pattern panics when the option is built, rather than silently
// matching nothing and being reported later as a stale rule.
func TestPatternValidation(t *testing.T) {
	t.Parallel()

	valid := []string{
		"**",
		"*",
		"V1.Tx.SeqNum",
		"**.Signatures",
		"operations[*].sourceaccount",
		"operations[!1].sourceaccount",
		"**.Operations[1*]",
		"**.Operations[[0-9]]",
		"**.Seq[Nn]um",
		"V?.Tx.SeqNum",
	}
	for _, pattern := range valid {
		t.Run("valid_"+pattern, func(t *testing.T) {
			t.Parallel()
			focus.IgnorePaths(pattern)
			focus.NormalizePath(pattern, func(v any) any { return v })
		})
	}

	invalid := []string{
		"",
		".V1.Tx.SeqNum",
		"V1.Tx.SeqNum.",
		"V1..Tx.SeqNum",
		"Operations[0",
		"Operations]",
		"Operations[]",
		"Operations[abc]",
		"Operations[0-9]",
		"Operations[!]",
		"Operations[0]]",
		"V1.***",
		"V1.Tx[",
	}
	for _, pattern := range invalid {
		t.Run("invalid_"+pattern, func(t *testing.T) {
			t.Parallel()

			mustPanicWith(t, pattern, func() { focus.IgnorePaths(pattern) })
			mustPanicWith(t, pattern, func() {
				focus.NormalizePath(pattern, func(v any) any { return v })
			})
		})
	}
}

// TestIgnoreSorobanAuth suppresses the volatile credentials tree while still
// failing on a difference in the authorized invocation itself.
func TestIgnoreSorobanAuth(t *testing.T) {
	t.Parallel()

	contract, _ := contractAddressValue(t, 9)
	want := sorobanInvokeEnvelope(t, contract, sorobanAuthEntry(t, 1, 1))
	got := sorobanInvokeEnvelope(t, contract, sorobanAuthEntry(t, 2, 2))

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got) {
		t.Fatal("credential differences compared equal without IgnoreSorobanAuth")
	}

	tb = faketb.New()
	if !focus.AssertEnvelopeEqual(tb, want, got, focus.IgnoreSorobanAuth()) {
		t.Errorf("volatile Soroban credentials were not ignored:\n%s", tb.Output())
	}

	// The invocation is the meaningful part of an auth entry; a change there
	// must still fail, and the ignored credential paths must be disclosed.
	entry := sorobanAuthEntry(t, 2, 2)
	entry.RootInvocation.Function.ContractFn.FunctionName = "different"
	changedInvocation := sorobanInvokeEnvelope(t, contract, entry)

	tb = faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, changedInvocation, focus.IgnoreSorobanAuth()) {
		t.Fatal("a changed authorized invocation was ignored along with the credentials")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, part := range []string{"ignored:", "Credentials", "FunctionName"} {
		if !strings.Contains(msg, part) {
			t.Errorf("failure message does not contain %q:\n%s", part, msg)
		}
	}
}

func TestIgnoreLedgerHeader(t *testing.T) {
	t.Parallel()

	want := xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{
		LedgerSeq:  1,
		TotalCoins: 100,
		FeePool:    200,
		IdPool:     3,
		ScpValue:   xdr.StellarValue{CloseTime: 10},
	}}
	got := xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{
		LedgerSeq:  2,
		TotalCoins: 101,
		FeePool:    201,
		IdPool:     4,
		ScpValue:   xdr.StellarValue{CloseTime: 11},
	}}

	tb := faketb.New()
	if !focus.AssertEqualAs(tb, "LedgerHeaderHistoryEntry", want, got, focus.IgnoreLedgerHeader()) {
		t.Errorf("volatile ledger header fields were not ignored:\n%s", tb.Output())
	}

	got.Header.LedgerVersion = want.Header.LedgerVersion + 1
	tb = faketb.New()
	if focus.AssertEqualAs(tb, "LedgerHeaderHistoryEntry", want, got, focus.IgnoreLedgerHeader()) {
		t.Fatal("a meaningful ledger header difference was ignored")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading assertion message: %v", err)
	}
	for _, field := range []string{"CloseTime", "LedgerSeq", "TotalCoins", "FeePool", "IdPool"} {
		if !strings.Contains(msg, field) {
			t.Errorf("failure message does not report ignored field %q:\n%s", field, msg)
		}
	}
}

// TestNormalizeAddress maps the dynamic addresses in both values to the same
// symbolic aliases, so identity is preserved while the concrete keys are not.
func TestNormalizeAddress(t *testing.T) {
	t.Parallel()

	wantEnv := envelopeValueSeq(t, 1, paymentOp(t, 100))
	gotEnv := envelopeValueSeq(t, 1, paymentOp(t, 100))
	gotEnv.V1.Tx.SourceAccount = mustMuxed(t, testIssuer)

	want := mustMarshal(t, wantEnv)
	got := mustMarshal(t, gotEnv)

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got) {
		t.Fatal("different source accounts compared equal without NormalizeAddress")
	}

	aliases := map[string]string{
		testSource: "ALICE_ADDRESS",
		testIssuer: "ALICE_ADDRESS",
	}
	tb = faketb.New()
	if !focus.AssertEnvelopeEqual(tb, want, got, focus.NormalizeAddress(aliases)) {
		t.Errorf("mapped source accounts were reported as different:\n%s", tb.Output())
	}

	// A mapping that only covers one side must not hide the difference.
	tb = faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got, focus.NormalizeAddress(map[string]string{
		testSource: "ALICE_ADDRESS",
	})) {
		t.Error("NormalizeAddress suppressed an unmapped address difference")
	}
}

// TestNormalizeAddressContractID covers the contract spelling: lens renders a
// contract ID as hex, while the mapping is keyed by its C... strkey.
func TestNormalizeAddressContractID(t *testing.T) {
	t.Parallel()

	wantAddr, wantKey := contractAddressValue(t, 1)
	gotAddr, gotKey := contractAddressValue(t, 2)

	want := sorobanInvokeEnvelope(t, wantAddr)
	got := sorobanInvokeEnvelope(t, gotAddr)

	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got) {
		t.Fatal("different contract IDs compared equal without NormalizeAddress")
	}

	aliases := map[string]string{
		wantKey: "CONTRACT_ADDRESS",
		gotKey:  "CONTRACT_ADDRESS",
	}
	tb = faketb.New()
	if !focus.AssertEnvelopeEqual(tb, want, got, focus.NormalizeAddress(aliases)) {
		t.Errorf("mapped contract IDs were reported as different:\n%s", tb.Output())
	}
}
