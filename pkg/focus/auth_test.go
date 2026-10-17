package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// Contract invocation authorizations are read out of the envelope, so every
// test here builds the envelope the system under test would have produced.
// The credential forms are exercised in full — source account, address,
// address v2 and delegated address — because each resolves its invoker
// through a different union arm, and a failure that only exercised one would
// not prove the others read at all.
const (
	testNonce      = int64(42)
	testExpiry     = uint32(5000)
	realSorobanCtc = "CBGSBKYMYO6OMGHQXXNOBRGVUDFUDVC2XLC3SXON5R2SNXILR7XCKKY3"
)

// mustScAddress builds the ScAddress a strkey names, for credentials and
// invocations.
func mustScAddress(t *testing.T, addr string) xdr.ScAddress {
	t.Helper()

	value := scAddress(t, addr)
	address, ok := value.GetAddress()
	if !ok {
		t.Fatalf("scAddress(%q) did not build an address", addr)
	}
	return address
}

// authSignature is the shaped signature payload real address credentials
// carry. The assertion shows it but does not verify it, so any non-empty
// shape will do.
func authSignature() xdr.ScVal {
	sig := xdr.ScBytes{0x01, 0x02, 0x03, 0x04}
	return xdr.ScVal{
		Type: xdr.ScValTypeScvVec,
		Vec:  vecPtr([]xdr.ScVal{{Type: xdr.ScValTypeScvBytes, Bytes: &sig}}),
	}
}

// authInvocation builds the authorized invocation tree for a contract call.
func authInvocation(
	t *testing.T, contract, function string, args ...xdr.ScVal,
) xdr.SorobanAuthorizedInvocation {
	t.Helper()

	return xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{
				ContractAddress: mustScAddress(t, contract),
				FunctionName:    xdr.ScSymbol(function),
				Args:            args,
			},
		},
	}
}

// createContractArgs is the payload a create_contract host function carries.
func createContractArgs(t *testing.T, constructorArgs ...xdr.ScVal) xdr.CreateContractArgs {
	t.Helper()

	var hash xdr.Hash
	var salt xdr.Uint256
	for i := range hash {
		hash[i] = byte(i)
		salt[i] = byte(i + 1)
	}
	return xdr.CreateContractArgs{
		ContractIdPreimage: xdr.ContractIdPreimage{
			Type: xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
			FromAddress: &xdr.ContractIdPreimageFromAddress{
				Address: mustScAddress(t, testSource),
				Salt:    salt,
			},
		},
		Executable: xdr.ContractExecutable{
			Type:     xdr.ContractExecutableTypeContractExecutableWasm,
			WasmHash: &hash,
		},
	}
}

// createContractHostFunction is the host function that deploys a contract.
func createContractHostFunction(t *testing.T) xdr.HostFunction {
	t.Helper()

	args := createContractArgs(t)
	return xdr.HostFunction{
		Type:           xdr.HostFunctionTypeHostFunctionTypeCreateContract,
		CreateContract: &args,
	}
}

// invokeHostFunction is the host function a contract-call operation invokes.
func invokeHostFunction(
	t *testing.T, contract, function string, args ...xdr.ScVal,
) xdr.HostFunction {
	t.Helper()

	return xdr.HostFunction{
		Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
		InvokeContract: &xdr.InvokeContractArgs{
			ContractAddress: mustScAddress(t, contract),
			FunctionName:    xdr.ScSymbol(function),
			Args:            args,
		},
	}
}

// authOperation wraps a host function and its authorization entries in the
// only operation that carries them.
func authOperation(host xdr.HostFunction, entries []xdr.SorobanAuthorizationEntry) xdr.Operation {
	return xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeInvokeHostFunction,
		InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
			HostFunction: host,
			Auth:         entries,
		},
	}}
}

// sourceAuthEntry authorizes as the transaction's source account.
func sourceAuthEntry(
	t *testing.T, contract, function string, args ...xdr.ScVal,
) xdr.SorobanAuthorizationEntry {
	t.Helper()

	return xdr.SorobanAuthorizationEntry{
		Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
		RootInvocation: authInvocation(t, contract, function, args...),
	}
}

// addressAuthEntry authorizes as a named address, with a nonce and an
// expiration the failure listing shows.
func addressAuthEntry(
	t *testing.T, invoker, contract, function string, args ...xdr.ScVal,
) xdr.SorobanAuthorizationEntry {
	t.Helper()

	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type:    xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: addressCredentials(t, invoker),
		},
		RootInvocation: authInvocation(t, contract, function, args...),
	}
}

// addressCredentials is the address-credential payload in whichever form the
// kind names.
func addressCredentials(t *testing.T, invoker string) *xdr.SorobanAddressCredentials {
	t.Helper()

	return &xdr.SorobanAddressCredentials{
		Address:                   mustScAddress(t, invoker),
		Nonce:                     xdr.Int64(testNonce),
		SignatureExpirationLedger: xdr.Uint32(testExpiry),
		Signature:                 authSignature(),
	}
}

// v2AuthEntry authorizes with the v2 address credentials.
func v2AuthEntry(
	t *testing.T, invoker, contract, function string, args ...xdr.ScVal,
) xdr.SorobanAuthorizationEntry {
	t.Helper()

	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type:      xdr.SorobanCredentialsTypeSorobanCredentialsAddressV2,
			AddressV2: addressCredentials(t, invoker),
		},
		RootInvocation: authInvocation(t, contract, function, args...),
	}
}

// delegatedAuthEntry authorizes through a delegate chain.
func delegatedAuthEntry(
	t *testing.T, invoker, delegate, contract, function string,
) xdr.SorobanAuthorizationEntry {
	t.Helper()

	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates,
			AddressWithDelegates: &xdr.SorobanAddressCredentialsWithDelegates{
				AddressCredentials: *addressCredentials(t, invoker),
				Delegates: []xdr.SorobanDelegateSignature{{
					Address:   mustScAddress(t, delegate),
					Signature: authSignature(),
				}},
			},
		},
		RootInvocation: authInvocation(t, contract, function),
	}
}

// createAuthEntry authorizes a contract creation, which names no contract
// address or function — the contract does not exist until it runs.
func createAuthEntry(t *testing.T, v2 bool, constructorArgs ...xdr.ScVal) xdr.SorobanAuthorizationEntry {
	t.Helper()

	function := xdr.SorobanAuthorizedFunction{
		Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractHostFn,
	}
	if v2 {
		function.Type = xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractV2HostFn
		function.CreateContractV2HostFn = &xdr.CreateContractArgsV2{
			ContractIdPreimage: createContractArgs(t).ContractIdPreimage,
			Executable:         xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset},
			ConstructorArgs:    constructorArgs,
		}
	} else {
		created := createContractArgs(t)
		function.CreateContractHostFn = &created
	}

	return xdr.SorobanAuthorizationEntry{
		Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
		RootInvocation: xdr.SorobanAuthorizedInvocation{Function: function},
	}
}

// invokeEnvelope wraps contract-call operations in a v1 envelope whose
// source account is the familiar testSource.
func invokeEnvelope(t *testing.T, ops ...xdr.Operation) xdr.TransactionEnvelope {
	t.Helper()

	return envelopeValue(t, ops...)
}

func TestAssertContractAuthValidPasses(t *testing.T) {
	t.Parallel()

	alpha := constructorContractID(t, 0x10)
	beta := constructorContractID(t, 0x20)

	// An entry authorizing alpha.transfer from and to the two test accounts.
	transfer := addressAuthEntry(t, testDest, alpha, "transfer",
		scAddress(t, testSource), scAddress(t, testDest), scU32(7))

	tests := []struct {
		name     string
		envelope focus.Value
		invoker  string
		call     focus.ContractCall
	}{
		{
			name: "source-account credentials name the transaction source",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{sourceAuthEntry(t, alpha, "transfer")})),
			invoker: testSource,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
		},
		{
			name:     "address credentials name an account invoker",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"), []xdr.SorobanAuthorizationEntry{transfer})),
			invoker:  testDest,
			call:     focus.ContractCall{Contract: alpha, Function: "transfer"},
		},
		{
			name: "v2 address credentials",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{v2AuthEntry(t, testDest, alpha, "transfer")})),
			invoker: testDest,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
		},
		{
			name: "delegated address credentials",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{delegatedAuthEntry(t, testDest, testSource, alpha, "transfer")})),
			invoker: testDest,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
		},
		{
			name: "a contract may be the invoker",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{addressAuthEntry(t, beta, alpha, "transfer")})),
			invoker: beta,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
		},
		{
			name: "a fee bump authorizes through the inner source account",
			envelope: feeBumpEnvelopeValue(t, invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{sourceAuthEntry(t, alpha, "transfer")})), nil, nil),
			invoker: testSource,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
		},
		{
			name: "an open call matches any authorization by the invoker",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{transfer})),
			invoker: testDest,
			call:    focus.ContractCall{},
		},
		{
			name: "the contract may be left open",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{transfer})),
			invoker: testDest,
			call:    focus.ContractCall{Function: "transfer"},
		},
		{
			name: "arguments match by value, address and width",
			envelope: invokeEnvelope(t, authOperation(
				invokeHostFunction(t, alpha, "transfer", scAddress(t, testSource), scU32(7)),
				[]xdr.SorobanAuthorizationEntry{transfer})),
			invoker: testDest,
			call: focus.ContractCall{
				Contract: alpha,
				Function: "transfer",
				Args:     []any{testSource, testDest, uint32(7)},
			},
		},
		{
			name: "an empty argument list means a call with none",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{sourceAuthEntry(t, alpha, "transfer")})),
			invoker: testSource,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer", Args: []any{}},
		},
		{
			name: "a plain string argument stays a symbol",
			envelope: invokeEnvelope(t, authOperation(
				invokeHostFunction(t, alpha, "transfer", scSymbol("memo-42")),
				[]xdr.SorobanAuthorizationEntry{sourceAuthEntry(t, alpha, "transfer", scSymbol("memo-42"))})),
			invoker: testSource,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer", Args: []any{"memo-42"}},
		},
		{
			name: "the expected call may be a sub-invocation",
			envelope: func() focus.Value {
				root := authInvocation(t, alpha, "transfer")
				root.SubInvocations = []xdr.SorobanAuthorizedInvocation{authInvocation(t, beta, "burn")}
				entry := xdr.SorobanAuthorizationEntry{
					Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
					RootInvocation: root,
				}
				return invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
					[]xdr.SorobanAuthorizationEntry{entry}))
			}(),
			invoker: testSource,
			call:    focus.ContractCall{Contract: beta, Function: "burn"},
		},
		{
			name: "any matching entry among several passes",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
				[]xdr.SorobanAuthorizationEntry{
					addressAuthEntry(t, testDest, alpha, "burn"),
					sourceAuthEntry(t, alpha, "transfer"),
				})),
			invoker: testSource,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
		},
		{
			name: "a create_contract authorization covers constructor arguments",
			envelope: invokeEnvelope(t, authOperation(createContractHostFunction(t),
				[]xdr.SorobanAuthorizationEntry{createAuthEntry(t, true, scU32(9))})),
			invoker: testSource,
			call:    focus.ContractCall{Args: []any{uint32(9)}},
		},
		{
			name: "a create_contract authorization without arguments",
			envelope: invokeEnvelope(t, authOperation(createContractHostFunction(t),
				[]xdr.SorobanAuthorizationEntry{createAuthEntry(t, false)})),
			invoker: testSource,
			call:    focus.ContractCall{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertContractAuthValid(tb, tc.envelope, tc.invoker, tc.call) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("a passing assertion reported a failure:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertContractAuthValidFailureMessages checks that a mismatch names the
// invoker and the call, lists the authorizations that were present, and —
// when a partial match narrows the problem — shows the single difference
// that failed, on both sides.
func TestAssertContractAuthValidFailureMessages(t *testing.T) {
	t.Parallel()

	alpha := constructorContractID(t, 0x10)
	beta := constructorContractID(t, 0x20)

	transfer := invokeHostFunction(t, alpha, "transfer", scU32(7))
	burnEntry := func() xdr.SorobanAuthorizationEntry {
		return addressAuthEntry(t, testDest, alpha, "burn", scU32(9))
	}()

	tests := []struct {
		name     string
		envelope focus.Value
		invoker  string
		call     focus.ContractCall
		contains []string
		absent   []string
	}{
		{
			name:     "the invoker authorized a different call",
			envelope: invokeEnvelope(t, authOperation(transfer, []xdr.SorobanAuthorizationEntry{burnEntry})),
			invoker:  testDest,
			call:     focus.ContractCall{Contract: alpha, Function: "transfer", Args: []any{uint32(7)}},
			contains: []string{
				"AssertContractAuthValid: no authorization matches invoker " +
					shortenForTest(testDest) + " calling " + shortenForTest(alpha) + ".transfer(u32(7))",
				"authorizations present (1)",
				"[0] invoker " + shortenForTest(testDest) + " — address credentials (nonce 42, expires at ledger 5000)",
				"→ " + shortenForTest(alpha) + ".burn(u32(9))",
				"invoker matched, but the transaction authorizes a different call",
				"~ authorized call",
				"want  " + shortenForTest(alpha) + ".transfer(u32(7))",
				"got   " + shortenForTest(alpha) + ".burn(u32(9))",
			},
		},
		{
			name: "the call is authorized by a different invoker",
			envelope: invokeEnvelope(t, authOperation(transfer,
				[]xdr.SorobanAuthorizationEntry{sourceAuthEntry(t, alpha, "transfer", scU32(7))})),
			invoker: testDest,
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
			contains: []string{
				"no authorization matches invoker " + shortenForTest(testDest),
				"source-account credentials",
				"the call is authorized, but not by the expected invoker",
				"~ invoker",
				"want  " + shortenForTest(testDest),
				"got   " + shortenForTest(testSource),
			},
		},
		{
			name:     "neither invoker nor call match, and only the listing is shown",
			envelope: invokeEnvelope(t, authOperation(transfer, []xdr.SorobanAuthorizationEntry{burnEntry})),
			invoker:  testSource,
			call:     focus.ContractCall{Contract: beta, Function: "mint"},
			contains: []string{
				"no authorization matches invoker " + shortenForTest(testSource),
				"authorizations present (1)",
				"→ " + shortenForTest(alpha) + ".burn(u32(9))",
			},
			absent: []string{"invoker matched, but", "not by the expected invoker"},
		},
		{
			name:     "an open call still names the invoker alone in the headline",
			envelope: invokeEnvelope(t, authOperation(transfer, []xdr.SorobanAuthorizationEntry{burnEntry})),
			invoker:  testSource,
			call:     focus.ContractCall{},
			contains: []string{
				"no authorization matches invoker " + shortenForTest(testSource) + "\n",
				"the call is authorized, but not by the expected invoker",
				"~ invoker",
				"want  " + shortenForTest(testSource),
				"got   " + shortenForTest(testDest),
			},
			absent: []string{" calling "},
		},
		{
			name: "an open contract reads as a wildcard",
			envelope: invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "burn"),
				[]xdr.SorobanAuthorizationEntry{addressAuthEntry(t, testDest, alpha, "burn")})),
			invoker: testDest,
			call:    focus.ContractCall{Function: "transfer"},
			contains: []string{
				"calling *.transfer",
				"want  *.transfer",
				"got   " + shortenForTest(alpha) + ".burn()",
			},
		},
		{
			name: "several authorizations are all listed",
			envelope: invokeEnvelope(t, authOperation(transfer, []xdr.SorobanAuthorizationEntry{
				sourceAuthEntry(t, alpha, "transfer", scU32(7)),
				v2AuthEntry(t, testDest, alpha, "burn"),
				delegatedAuthEntry(t, testDest, testSource, alpha, "mint"),
			})),
			invoker: constructorContractID(t, 0x30),
			call:    focus.ContractCall{Contract: alpha, Function: "transfer"},
			contains: []string{
				"authorizations present (3)",
				"[0] invoker " + shortenForTest(testSource) + " — source-account credentials",
				"[1] invoker " + shortenForTest(testDest) + " — address v2 credentials (nonce 42, expires at ledger 5000)",
				"[2] invoker " + shortenForTest(testDest) + " — address credentials with delegates (nonce 42, expires at ledger 5000, 1 delegate(s))",
				"→ " + shortenForTest(alpha) + ".mint()",
			},
		},
		{
			name: "a long argument list is truncated",
			envelope: invokeEnvelope(t, authOperation(
				invokeHostFunction(t, alpha, "transfer",
					scU32(1), scU32(2), scU32(3), scU32(4), scU32(5), scU32(6)),
				[]xdr.SorobanAuthorizationEntry{sourceAuthEntry(t, alpha, "transfer",
					scU32(1), scU32(2), scU32(3), scU32(4), scU32(5), scU32(6))})),
			invoker: testSource,
			call:    focus.ContractCall{Contract: alpha, Function: "burn"},
			contains: []string{
				".transfer(u32(1), u32(2), u32(3), u32(4), … +2)",
			},
		},
		{
			name: "credentials that claim an address but carry none are named",
			envelope: func() focus.Value {
				env := invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
					[]xdr.SorobanAuthorizationEntry{{
						Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress},
						RootInvocation: authInvocation(t, alpha, "transfer"),
					}}))
				return &lens.Value{Type: "TransactionEnvelope", Raw: &env}
			}(),
			invoker: testDest,
			call:    focus.ContractCall{},
			contains: []string{
				"authorizations present (1)",
				"(unknown: the credentials claim an address but carry none)",
				"the call is authorized, but not by the expected invoker",
				"got   (unknown: the credentials claim an address but carry none)",
			},
		},
		{
			name: "an invocation arm that carries nothing is named",
			envelope: func() focus.Value {
				env := invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
					[]xdr.SorobanAuthorizationEntry{{
						Credentials: xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
						RootInvocation: xdr.SorobanAuthorizedInvocation{
							Function: xdr.SorobanAuthorizedFunction{
								Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
							},
						},
					}}))
				return &lens.Value{Type: "TransactionEnvelope", Raw: &env}
			}(),
			invoker: testSource,
			call:    focus.ContractCall{},
			contains: []string{
				"(invalid: the invocation claims a contract function but carries none)",
				"invoker matched, but the transaction authorizes a different call",
				"want  any contract call",
				"got   (invalid: the invocation claims a contract function but carries none)",
			},
		},
		{
			name: "a malformed sub-invocation invalidates the entry",
			envelope: func() focus.Value {
				root := authInvocation(t, alpha, "transfer")
				root.SubInvocations = []xdr.SorobanAuthorizedInvocation{{
					Function: xdr.SorobanAuthorizedFunction{
						Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
					},
				}}
				env := invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
					[]xdr.SorobanAuthorizationEntry{{
						Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
						RootInvocation: root,
					}}))
				return &lens.Value{Type: "TransactionEnvelope", Raw: &env}
			}(),
			invoker: testSource,
			call:    focus.ContractCall{},
			contains: []string{
				"(invalid: sub-invocation: the invocation claims a contract function but carries none)",
			},
		},
		{
			name: "credentials of an unknown kind are named as such",
			envelope: func() focus.Value {
				env := invokeEnvelope(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
					[]xdr.SorobanAuthorizationEntry{{
						Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsType(42)},
						RootInvocation: authInvocation(t, alpha, "transfer"),
					}}))
				return &lens.Value{Type: "TransactionEnvelope", Raw: &env}
			}(),
			invoker: testDest,
			call:    focus.ContractCall{},
			contains: []string{
				"credentials of unrecognized type 42",
				"(unknown: credentials type 42 is not a kind this package can read)",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertContractAuthValid(tb, tc.envelope, tc.invoker, tc.call) {
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
			for _, unwanted := range tc.absent {
				if strings.Contains(msg, unwanted) {
					t.Errorf("message unexpectedly contains %q:\n%s", unwanted, msg)
				}
			}
		})
	}
}

// TestAssertContractAuthValidWithoutAuthorizations checks the two very
// different transactions that carry no authorizations at all — one that
// never invoked a contract, and one whose call required none — because both
// read as an empty auth list and mean opposite things.
func TestAssertContractAuthValidWithoutAuthorizations(t *testing.T) {
	t.Parallel()

	t.Run("a classic transaction lists its operations", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		if focus.AssertContractAuthValid(tb, envelopeValue(t, paymentOp(t, 100)), testSource, focus.ContractCall{}) {
			t.Fatal("assertion passed on a transaction with no operations to invoke a contract")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"the transaction carries no contract invocation authorizations",
			"operations present:",
			"[0] payment",
			"invoke_host_function operations",
			"nothing to check against invoker " + shortenForTest(testSource),
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("a captured invocation that required no authorization says so", func(t *testing.T) {
		t.Parallel()

		// A real mainnet fee-bump envelope whose harvest call required no
		// authorization: the diagnosis must say so rather than suggest the
		// transaction is classic.
		tb := faketb.New()
		if focus.AssertContractAuthValid(tb, fixture(t, "tx_soroban.env"), testSource, focus.ContractCall{}) {
			t.Fatal("assertion passed on an invocation with no authorization entries")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"the transaction carries no contract invocation authorizations",
			"invoke " + shortenForTest(realSorobanCtc) + ".harvest — 0 authorization entries",
			"No operation required authorization",
			"require_auth",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("upload and create host functions are named", func(t *testing.T) {
		t.Parallel()

		wasm := []byte{0x00, 0x01}
		env := envelopeValue(t,
			authOperation(xdr.HostFunction{
				Type: xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm,
				Wasm: &wasm,
			}, nil),
			authOperation(createContractHostFunction(t), nil),
		)

		tb := faketb.New()
		if focus.AssertContractAuthValid(tb, env, testSource, focus.ContractCall{}) {
			t.Fatal("assertion passed on operations with no authorization entries")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{
			"[0] upload contract wasm — 0 authorization entries",
			"[1] create contract — 0 authorization entries",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not contain %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an operation with no function recorded is named as such", func(t *testing.T) {
		t.Parallel()

		env := envelopeValue(t, xdr.Operation{Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
		}})
		wrapped := &lens.Value{Type: "TransactionEnvelope", Raw: &env}

		tb := faketb.New()
		if focus.AssertContractAuthValid(tb, wrapped, testSource, focus.ContractCall{}) {
			t.Fatal("assertion passed on an operation with no function")
		}
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "invoke_host_function operation with no function recorded") {
			t.Errorf("message does not name the malformed operation:\n%s", msg)
		}
	})
}

// TestAssertContractAuthValidDiagnosesBadInput checks that every input
// mistake is reported as the mistake it is — before the envelope is read,
// for the expectation's own fields.
func TestAssertContractAuthValidDiagnosesBadInput(t *testing.T) {
	t.Parallel()

	alpha := constructorContractID(t, 0x10)
	env := envelopeValue(t, paymentOp(t, 100))

	tests := []struct {
		name     string
		envelope focus.Value
		invoker  string
		call     focus.ContractCall
		contains []string
		absent   []string
	}{
		{
			name:     "empty invoker",
			envelope: env,
			invoker:  "",
			call:     focus.ContractCall{},
			contains: []string{"expectedInvoker is empty", "strkey account (G…)"},
		},
		{
			name:     "invoker is not a strkey",
			envelope: env,
			invoker:  "not-an-address",
			call:     focus.ContractCall{},
			contains: []string{`expectedInvoker "not-an-address" is not a strkey account (G…) or contract (C…) address`},
		},
		{
			name:     "an account address is not a contract address",
			envelope: env,
			invoker:  testSource,
			call:     focus.ContractCall{Contract: testDest},
			contains: []string{"expectedCall.Contract", "is not a contract address", "strkey C…"},
		},
		{
			name:     "invalid contract strkey",
			envelope: env,
			invoker:  testSource,
			call:     focus.ContractCall{Contract: "CAAAA"},
			contains: []string{"not a valid strkey contract address"},
		},
		{
			name:     "unreadable argument",
			envelope: env,
			invoker:  testSource,
			call:     focus.ContractCall{Args: []any{struct{}{}}},
			contains: []string{"expectedCall.Args[0]", "unsupported type"},
			absent:   []string{"no contract invocation authorizations"},
		},
		{
			name:     "nil argument",
			envelope: env,
			invoker:  testSource,
			call:     focus.ContractCall{Args: []any{nil}},
			contains: []string{"expectedCall.Args[0]", "value is nil"},
		},
		{
			name:     "nil envelope",
			envelope: nil,
			invoker:  testSource,
			call:     focus.ContractCall{Contract: alpha},
			contains: []string{"envelope value could not be read", "value is nil"},
		},
		{
			name:     "not an envelope",
			envelope: 42,
			invoker:  testSource,
			call:     focus.ContractCall{},
			contains: []string{"envelope value could not be read", "unsupported type int"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertContractAuthValid(tb, tc.envelope, tc.invoker, tc.call) {
				t.Fatal("assertion passed for invalid input")
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
			for _, unwanted := range tc.absent {
				if strings.Contains(msg, unwanted) {
					t.Errorf("message unexpectedly contains %q:\n%s", unwanted, msg)
				}
			}
		})
	}
}

// TestAssertContractAuthValidReportsRatherThanPanics proves the two panic
// paths end as ordinary failures: a hand-built expected argument with an
// unset union arm, which the generated encoder dereferences, and an
// envelope shape the SDK accessors refuse.
func TestAssertContractAuthValidReportsRatherThanPanics(t *testing.T) {
	t.Parallel()

	alpha := constructorContractID(t, 0x10)

	t.Run("an argument with no value", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		focus.AssertContractAuthValid(tb, envelopeValue(t, paymentOp(t, 100)), testSource,
			focus.ContractCall{Contract: alpha, Args: []any{xdr.ScVal{Type: xdr.ScValTypeScvU32}}})
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "internal error while inspecting the value") ||
			!strings.Contains(msg, "bug in stellar-focus") {
			t.Errorf("the encoder panic was not reported as a bug report:\n%s", msg)
		}
	})

	t.Run("an envelope the accessors refuse", func(t *testing.T) {
		t.Parallel()

		wrapped := &lens.Value{Type: "TransactionEnvelope",
			Raw: &xdr.TransactionEnvelope{Type: xdr.EnvelopeType(42)}}
		tb := faketb.New()
		focus.AssertContractAuthValid(tb, wrapped, testSource, focus.ContractCall{})
		msg, err := tb.Message()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(msg, "internal error while inspecting the value") {
			t.Errorf("the accessor panic was not reported:\n%s", msg)
		}
	})
}

// TestRequireContractAuthValidMirror checks the Fatalf mirror: same message,
// different severity, silent when the authorization is valid.
func TestRequireContractAuthValidMirror(t *testing.T) {
	t.Parallel()

	alpha := constructorContractID(t, 0x10)
	valid := envelopeValue(t, authOperation(invokeHostFunction(t, alpha, "transfer"),
		[]xdr.SorobanAuthorizationEntry{sourceAuthEntry(t, alpha, "transfer")}))
	classic := envelopeValue(t, paymentOp(t, 100))

	t.Run("silent on success", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		require.ContractAuthValid(tb, valid, testSource, focus.ContractCall{Contract: alpha})
		if tb.Failed() {
			t.Errorf("require reported a failure for a valid authorization:\n%s", tb.Output())
		}
	})

	t.Run("fatal on failure with the assert message", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		require.ContractAuthValid(tb, classic, testSource, focus.ContractCall{})
		if len(tb.Errors) != 0 || len(tb.Fatals) != 1 {
			t.Errorf("require used Errorf instead of Fatalf (errors=%d fatals=%d)",
				len(tb.Errors), len(tb.Fatals))
		}
		if !strings.Contains(tb.Output(), "AssertContractAuthValid") {
			t.Errorf("require lost the assertion name:\n%s", tb.Output())
		}
	})
}
