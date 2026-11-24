package focus

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// A Soroban transaction says who may act on its behalf through the
// SorobanAuthorizationEntry list on each invoke_host_function operation: one
// entry per address the contract required authorization from, naming the
// invoker (its credentials) and the invocation tree that invoker authorized
// (its root invocation, with every call beneath it).
//
// This file reads that list straight out of the envelope a test already
// holds. No RPC call, no simulator, no clock — the transaction is the
// evidence, so the same envelope gives the same answer every run and the
// suite stays offline, like the rest of this package.
//
// What is checked is the authorization's identity and structure: that a
// well-formed entry names the expected invoker and covers the expected call.
// The signature itself is not verified — what it signs is derived from the
// network passphrase, which an assertion is deliberately never handed — so a
// failure here means "the wrong party authorized the wrong call", not "the
// signature would not verify".

// ContractCall describes the contract invocation an authorization is expected
// to cover.
//
// A zero field matches anything, so ContractCall{} asserts only that the
// expected invoker authorized some call. Contract and Function match
// exactly — case-sensitively, the way the host evaluates them. The call may
// sit anywhere in the entry's invocation tree, because an authorization
// covers its root invocation and every call beneath it, and a test may assert
// on any of them.
type ContractCall struct {
	// Contract is the strkey "C…" address of the invoked contract. Empty
	// matches any contract.
	Contract string
	// Function is the invoked function name. Empty matches any function,
	// including the create_contract host functions, which name none.
	Function string
	// Args are the authorized arguments, in order. nil matches any
	// arguments; an empty, non-nil slice matches a call with none.
	//
	// Elements accept the same forms AssertConstructorState accepts for
	// storage values: an xdr.ScVal, base64 or raw XDR, or a Go bool,
	// integer or string — a strkey becomes an address, anything else a
	// symbol.
	Args []any
}

// AssertContractAuthValid checks that an envelope carries a valid contract
// invocation authorization: a well-formed entry whose credentials name
// expectedInvoker and whose invocation tree covers expectedCall.
//
// expectedInvoker is the strkey account ("G…") or contract ("C…") address
// that must have authorized the call; it may not be empty, because it is the
// subject of the assertion. An entry with SOROBAN_CREDENTIALS_SOURCE_ACCOUNT
// authorizes as the transaction's source account, so passing the source
// account matches either credential kind, and for a fee-bump envelope the
// source account read is the inner transaction's.
//
// expectedCall is the invocation to look for, with zero fields matching
// anything (see ContractCall). The assertion fails when the envelope carries
// no authorizations at all, when no entry names the expected invoker, when
// the invoker authorized a different call, or when the entry that matches is
// structurally invalid — credentials that claim an address but carry none,
// for instance. Entries the expectation did not mention are not judged: the
// assertion is about this invoker and this call.
//
// Every failure lists the authorizations the envelope does carry — each
// invoker, credential kind, nonce and expiration, and the invocation tree it
// authorizes — and, when a partial match narrows the problem, the single
// difference that failed. The listing is built while the value is known to be
// well-formed, so printing a failure never calls a generated accessor on a
// value that could panic.
//
// The signature on an address credential is shown but not checked: verifying
// it needs the network passphrase, and this assertion reads only what the
// envelope already says.
func AssertContractAuthValid(t TB, env Value, expectedInvoker string, expectedCall ContractCall) bool {
	t.Helper()
	const assertion = "AssertContractAuthValid"

	// The expectation runs behind the same guard as the inspection: an
	// expected argument is usually an xdr.ScVal the test built by hand, and
	// a hand-built value with an unset union arm panics the generated
	// encoder — which must surface as a test failure, not a crashed run.
	var (
		invoker authInvoker
		call    *authCallExpectation
		valid   bool
	)
	if !guard(t, assertion, func() {
		invoker, valid = expectAuthInvoker(t, assertion, expectedInvoker)
		if !valid {
			return
		}
		call, valid = expectAuthCall(t, assertion, expectedCall)
	}) {
		return false
	}
	if !valid {
		return false
	}

	envelope, _, ok := envelopeFrom(t, assertion, env)
	if !ok {
		return false
	}

	var scan authScan
	if !guard(t, assertion, func() {
		scan = scanAuthorizations(envelope)
	}) {
		return false
	}

	if len(scan.entries) == 0 {
		msg, ok := noAuthorizationsMessage(t, assertion, env, scan, invoker)
		if !ok {
			return false
		}
		t.Errorf("%s", msg)
		return false
	}

	if scan.authorized(invoker, call) {
		return true
	}
	t.Errorf("%s", authMismatchMessage(assertion, scan, invoker, call))
	return false
}

// authInvoker is the expected invoker in canonical form.
type authInvoker struct {
	// short is the strkey abbreviated for display.
	short string
	// id is the canonical encoding of the address, compared so two
	// invokers are equal exactly when their encodings are.
	id string
}

// authCallExpectation is a ContractCall in canonical form.
type authCallExpectation struct {
	// contract is the canonical strkey of the invoked contract; "" matches
	// any contract.
	contract string
	// function is the function name; "" matches any function.
	function string
	// args are the canonical argument encodings; nil matches any
	// arguments.
	args []string
	// argDescs are the same arguments rendered for a failure message.
	argDescs []string
}

// expectAuthInvoker validates the expected invoker and canonicalizes it. The
// input is checked before the envelope is read, so a mistake in the
// expectation is never reported as a missing authorization.
func expectAuthInvoker(t TB, assertion, raw string) (authInvoker, bool) {
	t.Helper()

	raw = strings.TrimSpace(raw)
	if raw == "" {
		t.Errorf("%s: expectedInvoker is empty; pass the strkey account (G…) or "+
			"contract (C…) address that authorizes the call", assertion)
		return authInvoker{}, false
	}
	val, ok := scAddressValue(raw)
	if !ok {
		t.Errorf("%s: expectedInvoker %q is not a strkey account (G…) or contract (C…) address",
			assertion, raw)
		return authInvoker{}, false
	}
	id, err := scValID(val)
	if err != nil {
		t.Errorf("%s: encoding the expected invoker: %v", assertion, err)
		return authInvoker{}, false
	}
	return authInvoker{short: shorten(raw), id: id}, true
}

// expectAuthCall validates the expected call and canonicalizes it for
// comparison. Arguments convert the way AssertConstructorState converts
// storage values, so a strkey argument stays an address and a plain string a
// symbol — the two things a test writes most.
func expectAuthCall(t TB, assertion string, want ContractCall) (*authCallExpectation, bool) {
	t.Helper()

	out := &authCallExpectation{function: want.Function}
	if contract := strings.TrimSpace(want.Contract); contract != "" {
		if !strings.HasPrefix(contract, "C") {
			t.Errorf("%s: expectedCall.Contract %q is not a contract address; it must be "+
				"the strkey C… address of the invoked contract", assertion, want.Contract)
			return nil, false
		}
		if _, err := strkey.Decode(strkey.VersionByteContract, contract); err != nil {
			t.Errorf("%s: expectedCall.Contract %q is not a valid strkey contract address: %v",
				assertion, want.Contract, err)
			return nil, false
		}
		out.contract = contract
	}

	if want.Args == nil {
		return out, true
	}
	out.args = make([]string, 0, len(want.Args))
	out.argDescs = make([]string, 0, len(want.Args))
	for i, arg := range want.Args {
		value, err := constructorValue(arg)
		if err != nil {
			t.Errorf("%s: expectedCall.Args[%d]: %v", assertion, i, err)
			return nil, false
		}
		id, err := scValID(value)
		if err != nil {
			t.Errorf("%s: expectedCall.Args[%d] could not be encoded: %v", assertion, i, err)
			return nil, false
		}
		out.args = append(out.args, id)
		out.argDescs = append(out.argDescs, describeValue(value))
	}
	return out, true
}

// authScan is what an envelope's invoke_host_function operations carry: the
// authorization entries themselves, plus the operations to name when there
// are none.
type authScan struct {
	entries   []authEntry
	invokeOps []invokeOpDetail
}

// invokeOpDetail names one invoke_host_function operation, for the diagnosis
// of an empty authorization list.
type invokeOpDetail struct {
	index  int
	target string
}

// authorized reports whether some well-formed entry names the expected
// invoker and covers the expected call.
func (s authScan) authorized(inv authInvoker, call *authCallExpectation) bool {
	for _, e := range s.entries {
		if e.authorizes(inv, call) {
			return true
		}
	}
	return false
}

// byInvoker returns the entries whose credentials name the expected invoker,
// whether or not their authorized call matched.
func (s authScan) byInvoker(inv authInvoker) []authEntry {
	var out []authEntry
	for _, e := range s.entries {
		if e.invokerID != "" && e.invokerID == inv.id {
			out = append(out, e)
		}
	}
	return out
}

// byCall returns the entries whose invocation tree covers the expected call,
// whether or not their credentials name the expected invoker.
func (s authScan) byCall(call *authCallExpectation) []authEntry {
	var out []authEntry
	for _, e := range s.entries {
		if e.invMalformed == "" && e.covers(call) {
			out = append(out, e)
		}
	}
	return out
}

// authEntry is one SorobanAuthorizationEntry in sanitized form: every
// generated accessor has already run, so a failure message never touches a
// union arm that could panic.
type authEntry struct {
	// kind names the credential form, with its nonce and expiration where
	// the form carries them.
	kind string
	// invoker is the strkey of the party that authorized; empty when the
	// credentials do not name one.
	invoker string
	// invokerID is the canonical encoding of invoker, empty when unknown.
	invokerID string
	// credMalformed records why the credentials cannot name an invoker.
	credMalformed string
	// invMalformed records why the invocation tree could not be read.
	invMalformed string
	// calls is the invocation tree the entry authorizes, root first.
	calls []authCall
}

// authCall is one invocation inside an entry's tree, pre-rendered while the
// value is known to be well-formed.
type authCall struct {
	// contract is the invoked contract's strkey; empty for the
	// create_contract authorizations, which name no address yet.
	contract string
	// function is the invoked function name; empty for create_contract.
	function string
	// kindLabel replaces contract.function when there is neither to show.
	kindLabel string
	// args are the canonical argument encodings, compared for a match.
	args []string
	// argDescs are the same arguments rendered for display.
	argDescs []string
	// depth is the call's distance from the root invocation.
	depth int
}

// authorizes reports whether a well-formed entry names the expected invoker
// and covers the expected call somewhere in its invocation tree.
func (e authEntry) authorizes(inv authInvoker, call *authCallExpectation) bool {
	if e.credMalformed != "" || e.invMalformed != "" {
		return false
	}
	return e.invokerID != "" && e.invokerID == inv.id && e.covers(call)
}

// covers reports whether the entry's invocation tree contains the expected
// call.
func (e authEntry) covers(call *authCallExpectation) bool {
	for _, c := range e.calls {
		if c.matches(call) {
			return true
		}
	}
	return false
}

// matches reports whether one authorized invocation satisfies the expected
// call. Every expectation that was set must match; one left open matches
// anything.
func (c authCall) matches(want *authCallExpectation) bool {
	if want.contract != "" && c.contract != want.contract {
		return false
	}
	if want.function != "" && (c.function == "" || c.function != want.function) {
		return false
	}
	if want.args == nil {
		return true
	}
	if len(c.args) != len(want.args) {
		return false
	}
	for i := range want.args {
		if c.args[i] != want.args[i] {
			return false
		}
	}
	return true
}

// invokerDisplay renders the entry's invoker, naming unreadable credentials
// as what they are rather than printing an empty address.
func (e authEntry) invokerDisplay() string {
	if e.invoker == "" {
		if e.credMalformed != "" {
			return "(unknown: " + e.credMalformed + ")"
		}
		return "(unknown)"
	}
	return shorten(e.invoker)
}

// scanAuthorizations walks an envelope's operations and sanitizes every
// authorization entry they carry. It calls generated accessors, so it must
// run behind guard.
//
// For a fee-bump envelope the operations scanned are the inner
// transaction's, which is where the invocation lives.
func scanAuthorizations(env *xdr.TransactionEnvelope) authScan {
	var scan authScan
	source, haveSource := sourceAccountIdentity(env)

	for i, op := range env.Operations() {
		if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
			continue
		}
		invoke := op.Body.InvokeHostFunctionOp
		if invoke == nil {
			scan.invokeOps = append(scan.invokeOps, invokeOpDetail{
				index:  i,
				target: "invoke_host_function operation with no function recorded",
			})
			continue
		}
		scan.invokeOps = append(scan.invokeOps, invokeOpDetail{
			index:  i,
			target: describeHostFunction(&invoke.HostFunction),
		})
		for _, raw := range invoke.Auth {
			scan.entries = append(scan.entries, buildAuthEntry(raw, source, haveSource))
		}
	}
	return scan
}

// sourceAccountIdentity resolves the envelope's source account — the party a
// SOROBAN_CREDENTIALS_SOURCE_ACCOUNT entry authorizes as — into display and
// canonical form.
func sourceAccountIdentity(env *xdr.TransactionEnvelope) (authInvoker, bool) {
	address, err := xdr.NewScAddress(
		xdr.ScAddressTypeScAddressTypeAccount, env.SourceAccount().ToAccountId())
	if err != nil {
		return authInvoker{}, false
	}
	raw, err := address.String()
	if err != nil {
		return authInvoker{}, false
	}
	id, err := scAddressID(address)
	if err != nil {
		return authInvoker{}, false
	}
	return authInvoker{short: shorten(raw), id: id}, true
}

// buildAuthEntry sanitizes one authorization entry: it resolves the invoker
// from whichever credential arm is present and pre-renders the invocation
// tree. It calls generated accessors, so it must run behind guard.
func buildAuthEntry(raw xdr.SorobanAuthorizationEntry, source authInvoker, haveSource bool) authEntry {
	entry := authEntry{}
	creds := raw.Credentials

	switch creds.Type {
	case xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount:
		entry.kind = "source-account credentials"
		if haveSource {
			entry.invoker = source.short
			entry.invokerID = source.id
		} else {
			entry.credMalformed = "the envelope has no source account to authorize as"
		}

	case xdr.SorobanCredentialsTypeSorobanCredentialsAddress:
		entry.kind = "address credentials"
		if creds.Address == nil {
			entry.credMalformed = "the credentials claim an address but carry none"
			break
		}
		entry.kind = fmt.Sprintf("address credentials (nonce %d, expires at ledger %d)",
			creds.Address.Nonce, creds.Address.SignatureExpirationLedger)
		setAuthInvoker(&entry, creds.Address.Address)

	case xdr.SorobanCredentialsTypeSorobanCredentialsAddressV2:
		entry.kind = "address v2 credentials"
		if creds.AddressV2 == nil {
			entry.credMalformed = "the credentials claim an address but carry none"
			break
		}
		entry.kind = fmt.Sprintf("address v2 credentials (nonce %d, expires at ledger %d)",
			creds.AddressV2.Nonce, creds.AddressV2.SignatureExpirationLedger)
		setAuthInvoker(&entry, creds.AddressV2.Address)

	case xdr.SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates:
		entry.kind = "address credentials with delegates"
		if creds.AddressWithDelegates == nil {
			entry.credMalformed = "the credentials claim delegated addresses but carry none"
			break
		}
		delegates := creds.AddressWithDelegates
		entry.kind = fmt.Sprintf(
			"address credentials with delegates (nonce %d, expires at ledger %d, %d delegate(s))",
			delegates.AddressCredentials.Nonce,
			delegates.AddressCredentials.SignatureExpirationLedger,
			len(delegates.Delegates))
		setAuthInvoker(&entry, delegates.AddressCredentials.Address)

	default:
		entry.kind = fmt.Sprintf("credentials of unrecognized type %d", creds.Type)
		entry.credMalformed = fmt.Sprintf("credentials type %d is not a kind this package can read",
			creds.Type)
	}

	calls, err := flattenAuthInvocation(raw.RootInvocation, 0)
	if err != nil {
		entry.invMalformed = err.Error()
		entry.calls = nil
		return entry
	}
	entry.calls = calls
	return entry
}

// setAuthInvoker records the invoker an address credential names.
func setAuthInvoker(entry *authEntry, address xdr.ScAddress) {
	raw, err := address.String()
	if err != nil {
		entry.credMalformed = "the credential's address could not be read"
		return
	}
	id, err := scAddressID(address)
	if err != nil {
		entry.credMalformed = "the credential's address could not be encoded"
		return
	}
	entry.invoker = raw
	entry.invokerID = id
}

// flattenAuthInvocation renders one node of an invocation tree — and
// everything beneath it — into sanitized calls.
//
// An unreadable node invalidates the whole entry: an authorization whose
// invocation cannot be read is not a valid authorization. It returns an
// error rather than panicking, because generated accessors dereference union
// arms without checking the discriminant.
func flattenAuthInvocation(inv xdr.SorobanAuthorizedInvocation, depth int) ([]authCall, error) {
	var out []authCall

	switch fn := inv.Function; fn.Type {
	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn:
		contract := fn.ContractFn
		if contract == nil {
			return nil, errors.New("the invocation claims a contract function but carries none")
		}
		address, err := contract.ContractAddress.String()
		if err != nil {
			return nil, fmt.Errorf("the invocation's contract address could not be read: %w", err)
		}
		args, descs, err := renderAuthArgs(contract.Args)
		if err != nil {
			return nil, err
		}
		out = append(out, authCall{
			contract: address,
			function: string(contract.FunctionName),
			args:     args,
			argDescs: descs,
			depth:    depth,
		})

	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractHostFn:
		if fn.CreateContractHostFn == nil {
			return nil, errors.New("the invocation claims a contract creation but carries none")
		}
		out = append(out, authCall{kindLabel: "create contract", depth: depth})

	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractV2HostFn:
		create := fn.CreateContractV2HostFn
		if create == nil {
			return nil, errors.New("the invocation claims a contract creation but carries none")
		}
		args, descs, err := renderAuthArgs(create.ConstructorArgs)
		if err != nil {
			return nil, err
		}
		out = append(out, authCall{
			kindLabel: "create contract v2",
			args:      args,
			argDescs:  descs,
			depth:     depth,
		})

	default:
		return nil, fmt.Errorf("the invocation's function type %d is not a kind this package can read",
			fn.Type)
	}

	for _, sub := range inv.SubInvocations {
		nested, err := flattenAuthInvocation(sub, depth+1)
		if err != nil {
			return nil, fmt.Errorf("sub-invocation: %w", err)
		}
		out = append(out, nested...)
	}
	return out, nil
}

// renderAuthArgs canonicalizes arguments for comparison and renders them for
// a failure message, while the values are known to be well-formed.
func renderAuthArgs(args []xdr.ScVal) ([]string, []string, error) {
	ids := make([]string, 0, len(args))
	descs := make([]string, 0, len(args))
	for i, arg := range args {
		id, err := scValID(arg)
		if err != nil {
			return nil, nil, fmt.Errorf("argument %d could not be encoded: %w", i, err)
		}
		ids = append(ids, id)
		descs = append(descs, describeValue(arg))
	}
	return ids, descs, nil
}

// scAddressID returns a canonical encoding of an address, so two addresses
// are equal exactly when their encodings are.
func scAddressID(address xdr.ScAddress) (string, error) {
	return scValID(xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address})
}

// describeHostFunction names the host function an operation calls, for the
// diagnosis of an empty authorization list.
func describeHostFunction(hf *xdr.HostFunction) string {
	switch hf.Type {
	case xdr.HostFunctionTypeHostFunctionTypeInvokeContract:
		invoke := hf.InvokeContract
		if invoke == nil {
			return "invoke contract (no invocation recorded)"
		}
		address, err := invoke.ContractAddress.String()
		if err != nil {
			return "invoke contract (the contract address could not be read)"
		}
		return "invoke " + shorten(address) + "." + string(invoke.FunctionName)
	case xdr.HostFunctionTypeHostFunctionTypeCreateContract:
		return "create contract"
	case xdr.HostFunctionTypeHostFunctionTypeCreateContractV2:
		return "create contract v2"
	case xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm:
		return "upload contract wasm"
	default:
		return fmt.Sprintf("host function of unrecognized type %d", hf.Type)
	}
}

// noAuthorizationsMessage renders the failure for an envelope with no
// authorization entries, distinguishing a transaction that never invoked a
// contract from one whose call required no authorization — the two look
// identical in the auth list and mean very different things.
//
// The second result is false when the operation list could not be read for
// the first of those; summarize has already reported why, so the caller adds
// nothing.
func noAuthorizationsMessage(
	t TB, assertion string, env Value, scan authScan, inv authInvoker,
) (string, bool) {
	t.Helper()

	var b strings.Builder
	fmt.Fprintf(&b, "%s: the transaction carries no contract invocation authorizations\n", assertion)

	if len(scan.invokeOps) > 0 {
		b.WriteString("\n  invoke_host_function operations:\n")
		for _, op := range scan.invokeOps {
			fmt.Fprintf(&b, "    [%d] %s — 0 authorization entries\n", op.index, op.target)
		}
		b.WriteString("\n  No operation required authorization, so there is no entry to check " +
			"against invoker ")
		b.WriteString(inv.short)
		b.WriteString(". An authorization entry appears here once the contract calls require_auth.")
		return strings.TrimRight(b.String(), "\n"), true
	}

	summary, ok := summarize(t, assertion, env, "TransactionEnvelope")
	if !ok {
		return "", false
	}
	b.WriteString("\n  operations present:\n")
	b.WriteString(indentWith(operationList(summary), "    "))
	b.WriteString("\n\n  Soroban authorizations are carried on invoke_host_function operations; " +
		"this envelope has none, so there is nothing to check against invoker ")
	b.WriteString(inv.short)
	b.WriteString(".")
	return strings.TrimRight(b.String(), "\n"), true
}

// authMismatchMessage renders the failure when authorizations exist but none
// matches: what the envelope carries first, then — when a partial match
// narrows the problem — the single difference that actually failed.
func authMismatchMessage(assertion string, scan authScan, inv authInvoker, want *authCallExpectation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: no authorization matches invoker %s", assertion, inv.short)
	if described := describeExpectedCall(want); described != anyContractCall {
		fmt.Fprintf(&b, " calling %s", described)
	}
	b.WriteString("\n\n")
	b.WriteString(indentWith(describeAuthEntries(scan.entries), "  "))
	b.WriteString("\n")

	if matched := scan.byInvoker(inv); len(matched) > 0 {
		gots := make([]string, 0, len(matched))
		for _, e := range matched {
			gots = append(gots, representativeCall(e, want))
		}
		b.WriteString("\n  invoker matched, but the transaction authorizes a different call:\n")
		b.WriteString(indentWith(renderAuthDifference("authorized call", describeExpectedCall(want), gots), "    "))
		b.WriteString("\n")
	} else if matched := scan.byCall(want); len(matched) > 0 {
		gots := make([]string, 0, len(matched))
		for _, e := range matched {
			gots = append(gots, e.invokerDisplay())
		}
		b.WriteString("\n  the call is authorized, but not by the expected invoker:\n")
		b.WriteString(indentWith(renderAuthDifference("invoker", inv.short, gots), "    "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// representativeCall picks the call that best explains a mismatch: one that
// agrees with the expectation apart from its arguments when such a call
// exists, otherwise the entry's root. An unreadable invocation is reported
// as what it is rather than as a call it did not make.
func representativeCall(e authEntry, want *authCallExpectation) string {
	if e.invMalformed != "" {
		return "(invalid: " + e.invMalformed + ")"
	}
	for _, c := range e.calls {
		if (want.contract == "" || c.contract == want.contract) &&
			(want.function == "" || c.function == want.function) {
			return describeAuthCall(c)
		}
	}
	return describeAuthCall(e.calls[0])
}

// renderAuthDifference prints one difference: the value wanted and every
// value found — the shape the rest of this package's failures use.
func renderAuthDifference(field, want string, gots []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "~ %s\n", field)
	fmt.Fprintf(&b, "      want  %s\n", want)
	for _, got := range gots {
		fmt.Fprintf(&b, "      got   %s\n", got)
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeAuthEntries lists the authorizations an envelope carries: the
// invoker behind each, the credential kind with its nonce and expiration,
// and the invocation tree it authorizes.
func describeAuthEntries(entries []authEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "authorizations present (%d):\n", len(entries))
	for i, e := range entries {
		fmt.Fprintf(&b, "  [%d] invoker %s — %s\n", i, e.invokerDisplay(), e.kind)
		if e.invMalformed != "" {
			fmt.Fprintf(&b, "%s→ (invalid: %s)\n", strings.Repeat(" ", 6), e.invMalformed)
			continue
		}
		for _, c := range e.calls {
			fmt.Fprintf(&b, "%s→ %s\n", strings.Repeat(" ", 6+2*c.depth), describeAuthCall(c))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeAuthCall renders one authorized call as the contract.function a
// developer knows, with its arguments.
func describeAuthCall(c authCall) string {
	if c.kindLabel != "" {
		return c.kindLabel
	}
	return shorten(c.contract) + "." + c.function + "(" + describeAuthArgs(c.argDescs) + ")"
}

// anyContractCall is how a fully open expectation reads in a failure message.
const anyContractCall = "any contract call"

// describeExpectedCall renders the expected call, naming every field the
// test left open so a failure never looks stricter than the assertion was.
func describeExpectedCall(want *authCallExpectation) string {
	if want.contract == "" && want.function == "" && want.args == nil {
		return anyContractCall
	}
	contract := want.contract
	if contract == "" {
		contract = "*"
	} else {
		contract = shorten(contract)
	}
	function := want.function
	if function == "" {
		function = "*"
	}
	out := contract + "." + function
	if want.args != nil {
		out += "(" + describeAuthArgs(want.argDescs) + ")"
	}
	return out
}

// describeAuthArgs renders an argument list, truncated so a long payload
// does not dominate the failure message.
func describeAuthArgs(args []string) string {
	const maxShown = 4
	if len(args) <= maxShown {
		return strings.Join(args, ", ")
	}
	shown := make([]string, 0, maxShown+1)
	shown = append(shown, args[:maxShown]...)
	shown = append(shown, fmt.Sprintf("… +%d", len(args)-maxShown))
	return strings.Join(shown, ", ")
}

// indentWith prefixes every non-empty line of s with prefix.
func indentWith(s, prefix string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
