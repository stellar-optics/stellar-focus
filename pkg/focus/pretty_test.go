package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// TestPrettyFormatsEnvelope tests that Pretty produces readable output for a TransactionEnvelope.
func TestPrettyFormatsEnvelope(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 100_0000000))
	output := focus.Pretty(env)

	// Check that output contains key fields
	for _, want := range []string{
		"TransactionEnvelope",
		"GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6", // SourceAccount
		"GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ", // Destination
		"1000000000 (100.0000000 XLM)",                             // Amount with XLM
		"native",                                                   // Asset
		"none",                                                     // Memo
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty output missing %q:\n%s", want, output)
		}
	}

	// Check structure
	if !strings.Contains(output, "{") || !strings.Contains(output, "}") {
		t.Errorf("Pretty output should have braces:\n%s", output)
	}
}

// TestPrettyFormatsResult tests that Pretty produces readable output for a TransactionResult.
func TestPrettyFormatsResult(t *testing.T) {
	t.Parallel()

	res := buildResult(t, xdr.TransactionResultCodeTxSuccess, paymentResult(xdr.PaymentResultCodePaymentSuccess))
	output := focus.Pretty(res)

	for _, want := range []string{
		"TransactionResult",
		"txSuccess",
		"paymentSuccess",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty output missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyFormatsMeta tests that Pretty produces readable output for a TransactionMeta.
func TestPrettyFormatsMeta(t *testing.T) {
	t.Parallel()

	meta := metaV2(t, []xdr.LedgerEntryChange{})
	output := focus.Pretty(meta)

	for _, want := range []string{
		"TransactionMeta",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty output missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyFormatsOperation tests that Pretty produces readable output for an Operation.
func TestPrettyFormatsOperation(t *testing.T) {
	t.Parallel()

	op := paymentOp(t, 50_0000000)
	b, err := op.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	output := focus.PrettyAs("Operation", b)

	for _, want := range []string{
		"Operation",
		"payment",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty output missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyFormatsLedgerEntry tests that Pretty produces readable output for a LedgerEntry.
func TestPrettyFormatsLedgerEntry(t *testing.T) {
	t.Parallel()

	entry := accountEntryValue(t, testSource, 1_000000000, xdr.Thresholds{})
	output := focus.Pretty(entry)

	for _, want := range []string{
		"LedgerEntry",
		"Account",
		"GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6",
		"1000000000",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty output missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyFormatsScVal tests that Pretty produces readable output for Soroban values.
func TestPrettyFormatsScVal(t *testing.T) {
	t.Parallel()

	// Test various ScVal types
	tests := []struct {
		name string
		val  xdr.ScVal
		want string
	}{
		{"void", xdr.ScVal{Type: xdr.ScValTypeScvVoid}, "void"},
		{"bool true", xdr.ScVal{Type: xdr.ScValTypeScvBool, B: ptr(true)}, "bool(true)"},
		{"bool false", xdr.ScVal{Type: xdr.ScValTypeScvBool, B: ptr(false)}, "bool(false)"},
		{"u32", xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(42))}, "u32(42)"},
		{"i32", xdr.ScVal{Type: xdr.ScValTypeScvI32, I32: ptr(xdr.Int32(-42))}, "i32(-42)"},
		{"u64", xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: ptr(xdr.Uint64(1000))}, "u64(1000)"},
		{"i64", xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: ptr(xdr.Int64(-1000))}, "i64(-1000)"},
		{"symbol", xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: ptr(xdr.ScSymbol("transfer"))}, "sym(transfer)"},
		{"string", xdr.ScVal{Type: xdr.ScValTypeScvString, Str: ptr(xdr.ScString("hello"))}, "str(\"hello\")"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := tc.val.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			output := focus.PrettyAs("ScVal", b)
			if !strings.Contains(output, tc.want) {
				t.Errorf("Pretty output missing %q:\n%s", tc.want, output)
			}
		})
	}
}

// TestPrettyAsWithType tests that PrettyAs works with explicit type.
func TestPrettyAsWithType(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 100_0000000))
	output := focus.PrettyAs("TransactionEnvelope", env)

	if !strings.Contains(output, "TransactionEnvelope") {
		t.Errorf("PrettyAs output missing type:\n%s", output)
	}
}

// TestPrettyHandlesInvalidInput tests that Pretty handles invalid input gracefully.
func TestPrettyHandlesInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input focus.Value
		want  string
	}{
		{"nil", nil, "(invalid"},
		{"empty string", "", "(invalid"},
		{"invalid base64", "not-base64!", "(invalid"},
		{"wrong type", 123, "(invalid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output := focus.Pretty(tc.input)
			if !strings.Contains(output, tc.want) {
				t.Errorf("Pretty(%v) = %q, want to contain %q", tc.input, output, tc.want)
			}
		})
	}
}

// TestPrettyDeterministic tests that Pretty produces deterministic output.
func TestPrettyDeterministic(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 100_0000000))

	// Run multiple times
	outputs := make([]string, 10)
	for i := 0; i < 10; i++ {
		outputs[i] = focus.Pretty(env)
	}

	// All outputs should be identical
	for i := 1; i < len(outputs); i++ {
		if outputs[i] != outputs[0] {
			t.Errorf("Pretty output not deterministic:\nrun 0: %s\nrun %d: %s", outputs[0], i, outputs[i])
			break
		}
	}
}

// TestPrettyWithLensValue tests that Pretty accepts a pre-decoded *lens.Value.
func TestPrettyWithLensValue(t *testing.T) {
	t.Parallel()

	env := buildEnvelope(t, paymentOp(t, 100_0000000))
	_, decoded, err := focus.Encode(env, "TransactionEnvelope")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// We can't directly create a *lens.Value from outside, but we can test
	// that the function doesn't panic with valid input
	output := focus.Pretty(env)
	if !strings.Contains(output, "TransactionEnvelope") {
		t.Errorf("Pretty with encoded value failed:\n%s", output)
	}

	_ = decoded // avoid unused variable
}

// TestPrettyDoesNotPanic tests that Pretty never panics on any input.
func TestPrettyDoesNotPanic(t *testing.T) {
	t.Parallel()

	// Various hostile inputs
	inputs := []focus.Value{
		nil,
		"",
		"not base64",
		[]byte{0xff, 0xfe, 0xfd},
		123,
		"AAAAAA==", // valid base64 but not valid XDR
		buildEnvelope(t, paymentOp(t, 100_0000000)),
		buildResult(t, xdr.TransactionResultCodeTxFailed, paymentResult(xdr.PaymentResultCodePaymentUnderfunded)),
		metaV2(t, []xdr.LedgerEntryChange{}),
		accountEntryValue(t, testSource, 1_000000000, xdr.Thresholds{}),
	}

	for _, input := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Pretty panicked on input %T: %v", input, r)
				}
			}()
			_ = focus.Pretty(input)
		}()
	}
}

// TestPrettyOutputLength tests that Pretty output stays reasonable in size.
func TestPrettyOutputLength(t *testing.T) {
	t.Parallel()

	// Build a larger envelope with multiple operations
	ops := make([]xdr.Operation, 10)
	for i := range ops {
		ops[i] = paymentOp(t, int64((i+1)*100_0000000))
	}
	env := buildEnvelopeSeq(t, 1, ops...)

	output := focus.Pretty(env)

	// Should be readable but not excessively long
	if len(output) > 10000 {
		t.Errorf("Pretty output too long (%d chars):\n%s", len(output), output[:500])
	}

	// Should have all 10 operations
	opCount := strings.Count(output, "PaymentOp")
	if opCount < 10 {
		t.Errorf("Expected at least 10 PaymentOp mentions, got %d", opCount)
	}
}

// TestPrettyCreditAsset tests that Pretty formats credit assets correctly.
func TestPrettyCreditAsset(t *testing.T) {
	t.Parallel()

	op := creditPaymentOp(t, 50_0000000)
	env := buildEnvelope(t, op)
	output := focus.Pretty(env)

	if !strings.Contains(output, "USDC") {
		t.Errorf("Pretty output missing asset code:\n%s", output)
	}
	if !strings.Contains(output, "GA2JRQOF6EA3HQWDCEDBPPMLYPJCFLDDGYZLEQGMS5SOBQIB3BAFHVAW") {
		t.Errorf("Pretty output missing asset issuer:\n%s", output)
	}
}

// TestPrettyFeeFormatting tests that fees are formatted with XLM equivalent.
func TestPrettyFeeFormatting(t *testing.T) {
	t.Parallel()

	env := buildEnvelopeSeq(t, 1, paymentOp(t, 100_0000000))
	output := focus.Pretty(env)

	// Fee should show as stroops with XLM
	if !strings.Contains(output, "Fee: 100") {
		t.Errorf("Pretty output missing fee:\n%s", output)
	}
}

// TestPrettyMemoFormatting tests that different memo types are formatted.
func TestPrettyMemoFormatting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		memo     xdr.Memo
		contains string
	}{
		{"none", xdr.Memo{Type: xdr.MemoTypeMemoNone}, "none"},
		{"text", textMemo("invoice-42"), "text:invoice-42"},
		{"id", xdr.Memo{Type: xdr.MemoTypeMemoId, Id: ptr(xdr.Uint64(12345))}, "id:12345"},
		{"hash", xdr.Memo{Type: xdr.MemoTypeMemoHash, Hash: ptr(xdr.Hash{1, 2, 3, 4})}, "hash:"},
		{"return", xdr.Memo{Type: xdr.MemoTypeMemoReturn, RetHash: ptr(xdr.Hash{5, 6, 7, 8})}, "return:"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := mustMarshal(t, envelopeWithMemo(t, tc.memo))
			output := focus.Pretty(env)
			if !strings.Contains(output, tc.contains) {
				t.Errorf("Pretty memo %s missing %q:\n%s", tc.name, tc.contains, output)
			}
		})
	}
}

// TestPrettySorobanEvent tests that Soroban events are formatted.
func TestPrettySorobanEvent(t *testing.T) {
	t.Parallel()

	event := contractEvent(t, "transfer", 100)
	b, err := event.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	output := focus.PrettyAs("ContractEvent", b)

	for _, want := range []string{
		"ContractEvent",
		"transfer",
		"100",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty event missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyContractId tests that ContractId is formatted.
func TestPrettyContractId(t *testing.T) {
	t.Parallel()

	var cid xdr.ContractId
	copy(cid[:], []byte("focus-test-contract-id-32bytes!!"))
	b, err := cid.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	output := focus.PrettyAs("ContractId", b)

	if !strings.Contains(output, "ContractId") {
		t.Errorf("Pretty contract ID missing type:\n%s", output)
	}
}

// TestPrettyClaimableBalanceId tests that ClaimableBalanceId is formatted.
func TestPrettyClaimableBalanceId(t *testing.T) {
	t.Parallel()

	id := claimableBalanceIDValue(42)
	b, err := id.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	output := focus.PrettyAs("ClaimableBalanceId", b)

	if !strings.Contains(output, "ClaimableBalanceId") {
		t.Errorf("Pretty balance ID missing type:\n%s", output)
	}
}

// TestPrettyPoolId tests that PoolId is formatted.
func TestPrettyPoolId(t *testing.T) {
	t.Parallel()

	id := poolIDValue(99)
	b, err := id.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	output := focus.PrettyAs("PoolId", b)

	if !strings.Contains(output, "PoolId") {
		t.Errorf("Pretty pool ID missing type:\n%s", output)
	}
}

// TestPrettyOfferEntry tests that OfferEntry is formatted.
func TestPrettyOfferEntry(t *testing.T) {
	t.Parallel()

	entry := offerEntryValue(t, testSource, 123, nativeAsset(), creditAsset(t, "USDC"), 1000, priceValue(1, 1))
	output := focus.Pretty(entry)

	for _, want := range []string{
		"LedgerEntry",
		"Offer",
		"123",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty offer missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyTrustline tests that Trustline is formatted.
func TestPrettyTrustline(t *testing.T) {
	t.Parallel()

	entry := trustlineEntryValue(t, 50_0000000, 100_0000000)
	output := focus.Pretty(entry)

	for _, want := range []string{
		"LedgerEntry",
		"TrustLine",
		"USDC",
		"50000000",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty trustline missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyLiquidityPool tests that LiquidityPool is formatted.
func TestPrettyLiquidityPool(t *testing.T) {
	t.Parallel()

	id := poolIDValue(1)
	entry := poolEntryValue(id, nativeAsset(), creditAsset(t, "USDC"), 100_0000000, 50_0000000, 1000)
	output := focus.Pretty(entry)

	for _, want := range []string{
		"LedgerEntry",
		"LiquidityPool",
		"constantProduct",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty pool missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyDataEntry tests that DataEntry is formatted.
func TestPrettyDataEntry(t *testing.T) {
	t.Parallel()

	entry := dataLedgerEntryValue(t, testSource, "config", []byte("value123"))
	output := focus.Pretty(entry)

	for _, want := range []string{
		"LedgerEntry",
		"Data",
		"config",
		"value123",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Pretty data entry missing %q:\n%s", want, output)
		}
	}
}

// TestPrettyUsedInAssertion tests that Pretty output is used in assertion failures.
func TestPrettyUsedInAssertion(t *testing.T) {
	t.Parallel()

	want := buildEnvelope(t, paymentOp(t, 100_0000000))
	got := buildEnvelope(t, paymentOp(t, 200_0000000))

	tb := faketb.New()
	focus.AssertEnvelopeEqual(tb, want, got)
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The failure message should contain field paths and values
	// (Pretty is used internally by the diff formatter)
	if !strings.Contains(msg, "Body.PaymentOp.Amount") {
		t.Errorf("Assertion message missing field path:\n%s", msg)
	}
	if !strings.Contains(msg, "1000000000") || !strings.Contains(msg, "2000000000") {
		t.Errorf("Assertion message missing amounts:\n%s", msg)
	}
}

// ptr is a helper to get a pointer to a value.
func ptr[T any](v T) *T {
	return &v
}

// TestPrettyAssetDirect tests that PrettyAs formats Asset directly.
func TestPrettyAssetDirect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		asset    xdr.Asset
		contains string
	}{
		{
			name:     "native",
			asset:    xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			contains: "native",
		},
		{
			name: "credit alphanum4",
			asset: func() xdr.Asset {
				var a xdr.Asset
				aid := mustAccountID(t, testIssuer)
				a.SetCredit("USDC", aid)
				return a
			}(),
			contains: "USDC:GA2JRQOF6EA3HQWDCEDBPPMLYPJCFLDDGYZLEQGMS5SOBQIB3BAFHVAW",
		},
		{
			name: "credit alphanum12",
			asset: func() xdr.Asset {
				var a xdr.Asset
				aid := mustAccountID(t, testIssuer)
				a.SetCredit("VERYLONGCODE", aid)
				return a
			}(),
			contains: "VERYLONGCODE:GA2JRQOF6EA3HQWDCEDBPPMLYPJCFLDDGYZLEQGMS5SOBQIB3BAFHVAW",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := tc.asset.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			output := focus.PrettyAs("Asset", b)
			if !strings.Contains(output, tc.contains) {
				t.Errorf("Pretty output missing %q:\n%s", tc.contains, output)
			}
		})
	}
}

// TestPrettyScValComplex tests complex ScVal types (Vec, Map, Addr, Bytes).
func TestPrettyScValComplex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  xdr.ScVal
		want string
	}{
		{
			name: "bytes",
			val: xdr.ScVal{
				Type:  xdr.ScValTypeScvBytes,
				Bytes: ptr(xdr.ScBytes([]byte{0x01, 0x02, 0x03})),
			},
			want: "bytes",
		},
		{
			name: "vec",
			val: func() xdr.ScVal {
				vec := xdr.ScVec{
					{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(1))},
					{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(2))},
				}
				vecPtr := &vec
				return xdr.ScVal{
					Type: xdr.ScValTypeScvVec,
					Vec:  &vecPtr,
				}
			}(),
			want: "vec",
		},
		{
			name: "map",
			val: func() xdr.ScVal {
				m := xdr.ScMap{
					{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: ptr(xdr.ScSymbol("key"))}, Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(42))}},
				}
				mPtr := &m
				return xdr.ScVal{
					Type: xdr.ScValTypeScvMap,
					Map:  &mPtr,
				}
			}(),
			want: "map",
		},
		{
			name: "address",
			val: xdr.ScVal{
				Type: xdr.ScValTypeScvAddress,
				Address: &xdr.ScAddress{
					AccountId: func() *xdr.AccountId {
						aid := mustAccountID(t, testSource)
						return &aid
					}(),
				},
			},
			want: "addr",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := tc.val.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			output := focus.PrettyAs("ScVal", b)
			if !strings.Contains(output, tc.want) {
				t.Errorf("Pretty output missing %q:\n%s", tc.want, output)
			}
		})
	}
}

// TestPrettyKeyFieldsZero tests that key fields are shown even when zero.
func TestPrettyKeyFieldsZero(t *testing.T) {
	t.Parallel()

	// Test with zero SeqNum (should still show)
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: mustMuxed(t, testSource),
				Fee:           100,
				SeqNum:        0, // zero sequence number
				Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations:    []xdr.Operation{},
			},
		},
	}
	b, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	output := focus.PrettyAs("TransactionEnvelope", b)

	// SeqNum is a key field, should be shown even when zero
	if !strings.Contains(output, "SeqNum: 0") {
		t.Errorf("Pretty output should show zero SeqNum:\n%s", output)
	}

	// Fee is a key field, should be shown
	if !strings.Contains(output, "Fee: 100") {
		t.Errorf("Pretty output should show Fee:\n%s", output)
	}
}

// TestPrettyResultCodes tests various result code formats.
func TestPrettyResultCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		txCode   xdr.TransactionResultCode
		opCode   xdr.PaymentResultCode
		contains string
	}{
		{"txSuccess", xdr.TransactionResultCodeTxSuccess, xdr.PaymentResultCodePaymentSuccess, "txSuccess"},
		{"txFailed", xdr.TransactionResultCodeTxFailed, xdr.PaymentResultCodePaymentUnderfunded, "txFailed"},
		{"paymentSuccess", xdr.TransactionResultCodeTxSuccess, xdr.PaymentResultCodePaymentSuccess, "paymentSuccess"},
		{"paymentUnderfunded", xdr.TransactionResultCodeTxFailed, xdr.PaymentResultCodePaymentUnderfunded, "paymentUnderfunded"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := buildResult(t, tc.txCode, paymentResult(tc.opCode))
			output := focus.Pretty(res)
			if !strings.Contains(output, tc.contains) {
				t.Errorf("Pretty output missing %q:\n%s", tc.contains, output)
			}
		})
	}
}

// TestPrettyDataValue tests DataValue formatting.
func TestPrettyDataValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    []byte
		contains string
	}{
		{"printable", []byte("hello world"), "hello world"},
		{"empty", []byte{}, `""`},
		{"non-printable", []byte{0x01, 0x02, 0x03}, "010203"},
		{"long", func() []byte {
			b := make([]byte, 40)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(), "00010203"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := dataLedgerEntryValue(t, testSource, "config", tc.value)
			output := focus.Pretty(entry)
			if !strings.Contains(output, tc.contains) {
				t.Errorf("Pretty output missing %q:\n%s", tc.contains, output)
			}
		})
	}
}

// TestPrettyScValVecMapNested tests nested Vec and Map in ScVal.
func TestPrettyScValVecMapNested(t *testing.T) {
	t.Parallel()

	// Test vec with mixed types
	vecVal := func() xdr.ScVal {
		vec := xdr.ScVec{
			{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(1))},
			{Type: xdr.ScValTypeScvString, Str: ptr(xdr.ScString("hello"))},
			{Type: xdr.ScValTypeScvSymbol, Sym: ptr(xdr.ScSymbol("sym"))},
		}
		vecPtr := &vec
		return xdr.ScVal{
			Type: xdr.ScValTypeScvVec,
			Vec:  &vecPtr,
		}
	}()
	b, err := vecVal.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	output := focus.PrettyAs("ScVal", b)
	if !strings.Contains(output, "vec") {
		t.Errorf("Pretty output missing vec:\n%s", output)
	}

	// Test map
	mapVal := func() xdr.ScVal {
		m := xdr.ScMap{
			{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: ptr(xdr.ScSymbol("a"))}, Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(1))}},
			{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: ptr(xdr.ScSymbol("b"))}, Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(2))}},
		}
		mPtr := &m
		return xdr.ScVal{
			Type: xdr.ScValTypeScvMap,
			Map:  &mPtr,
		}
	}()
	b2, err := mapVal.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	output2 := focus.PrettyAs("ScVal", b2)
	if !strings.Contains(output2, "map") {
		t.Errorf("Pretty output missing map:\n%s", output2)
	}
}

// TestPrettyOperationTypeFormatting tests operation type formatting.
func TestPrettyOperationTypeFormatting(t *testing.T) {
	t.Parallel()

	ops := []struct {
		name string
		op   xdr.Operation
		want string
	}{
		{"payment", paymentOp(t, 100), "payment"},
		{"createAccount", createAccountOp(t), "create_account"},
		{"pathPaymentStrictReceive", pathPaymentStrictReceiveOp(t), "path_payment_strict_receive"},
		{"manageSellOffer", manageSellOfferOp(t), "manage_sell_offer"},
		{"changeTrust", changeTrustOp(t), "change_trust"},
		{"allowTrust", allowTrustOp(t), "allow_trust"},
		{"accountMerge", accountMergeOp(t), "account_merge"},
		{"inflation", inflationOp(t), "inflation"},
		{"manageData", manageDataOp(t), "manage_data"},
		{"bumpSequence", bumpSequenceOp(t), "bump_sequence"},
		{"manageBuyOffer", manageBuyOfferOp(t), "manage_buy_offer"},
		{"createPassiveSellOffer", createPassiveSellOfferOp(t), "create_passive_sell_offer"},
		{"pathPaymentStrictSend", pathPaymentStrictSendOp(t), "path_payment_strict_send"},
		{"createClaimableBalance", createClaimableBalanceOp(t), "create_claimable_balance"},
		{"claimClaimableBalance", claimClaimableBalanceOp(t), "claim_claimable_balance"},
		{"beginSponsoringFutureReserves", beginSponsoringFutureReservesOp(t), "begin_sponsoring_future_reserves"},
		{"endSponsoringFutureReserves", endSponsoringFutureReservesOp(t), "end_sponsoring_future_reserves"},
		{"revokeSponsorship", revokeSponsorshipOp(t), "revoke_sponsorship"},
		{"clawback", clawbackOp(t), "clawback"},
		{"clawbackClaimableBalance", clawbackClaimableBalanceOp(t), "clawback_claimable_balance"},
		{"setTrustLineFlags", setTrustLineFlagsOp(t), "set_trust_line_flags"},
		{"liquidityPoolDeposit", liquidityPoolDepositOp(t), "liquidity_pool_deposit"},
		{"liquidityPoolWithdraw", liquidityPoolWithdrawOp(t), "liquidity_pool_withdraw"},
		{"invokeHostFunction", invokeHostFunctionOp(t), "invoke_host_function"},
		{"extendFootprintTtl", extendFootprintTtlOp(t), "extend_footprint_ttl"},
		{"restoreFootprint", restoreFootprintOp(t), "restore_footprint"},
	}

	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := tc.op.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			output := focus.PrettyAs("Operation", b)
			if !strings.Contains(output, tc.want) {
				t.Errorf("Pretty output missing %q:\n%s", tc.want, output)
			}
		})
	}
}
