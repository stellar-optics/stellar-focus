package focus_test

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// constructorContractID builds a deterministic, valid contract strkey, so a failure
// message shows the same address on every run.
func constructorContractID(t *testing.T, seed byte) string {
	t.Helper()

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	id, err := strkey.Encode(strkey.VersionByteContract, raw)
	if err != nil {
		t.Fatalf("encoding contract id: %v", err)
	}
	return id
}

func scSymbol(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func scString(s string) xdr.ScVal {
	str := xdr.ScString(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
}

func scU32(n uint32) xdr.ScVal {
	v := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}
}

func scU64(n uint64) xdr.ScVal {
	v := xdr.Uint64(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &v}
}

func scI32(n int32) xdr.ScVal {
	v := xdr.Int32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvI32, I32: &v}
}

func scI64(n int64) xdr.ScVal {
	v := xdr.Int64(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: &v}
}

func scBool(b bool) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b}
}

// scAddress builds an address value from a strkey account or contract address.
func scAddress(t *testing.T, addr string) xdr.ScVal {
	t.Helper()

	var (
		address xdr.ScAddress
		err     error
	)
	if strings.HasPrefix(addr, "C") {
		var raw []byte
		raw, err = strkey.Decode(strkey.VersionByteContract, addr)
		if err == nil {
			var id xdr.ContractId
			copy(id[:], raw)
			address, err = xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeContract, id)
		}
	} else {
		var account xdr.AccountId
		account, err = xdr.AddressToAccountId(addr)
		if err == nil {
			address, err = xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeAccount, account)
		}
	}
	if err != nil {
		t.Fatalf("building address value for %s: %v", addr, err)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address}
}

func scValPtr(v xdr.ScVal) *xdr.ScVal { return &v }

// contractDataEntry builds one contract data ledger entry.
func contractDataEntry(
	t *testing.T,
	contractID string,
	key, value xdr.ScVal,
	durability xdr.ContractDataDurability,
) xdr.LedgerEntry {
	t.Helper()

	raw, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		t.Fatalf("decoding contract id %s: %v", contractID, err)
	}
	var id xdr.ContractId
	copy(id[:], raw)
	contract, err := xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeContract, id)
	if err != nil {
		t.Fatalf("building contract address: %v", err)
	}

	return xdr.LedgerEntry{
		LastModifiedLedgerSeq: 1000,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeContractData,
			ContractData: &xdr.ContractDataEntry{
				Contract:   contract,
				Key:        key,
				Durability: durability,
				Val:        value,
			},
		},
	}
}

// contractInstanceEntry builds the instance entry a deployment creates. Its
// storage map is the contract's instance storage.
func contractInstanceEntry(t *testing.T, contractID string, storage ...xdr.ScMapEntry) xdr.LedgerEntry {
	t.Helper()

	var hash xdr.Hash
	for i := range hash {
		hash[i] = 0xab
	}
	instance := xdr.ScVal{
		Type: xdr.ScValTypeScvContractInstance,
		Instance: &xdr.ScContractInstance{
			Executable: xdr.ContractExecutable{
				Type:     xdr.ContractExecutableTypeContractExecutableWasm,
				WasmHash: &hash,
			},
			Storage: instanceStorage(storage...),
		},
	}
	return contractDataEntry(t, contractID,
		xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		instance, xdr.ContractDataDurabilityPersistent)
}

func instanceStorage(entries ...xdr.ScMapEntry) *xdr.ScMap {
	if entries == nil {
		return nil
	}
	m := xdr.ScMap(entries)
	return &m
}

// deploymentMeta wraps the ledger changes of a deployment in a Soroban-era
// transaction meta, which is where a real deployment transaction puts them.
func deploymentMeta(t *testing.T, changes ...xdr.LedgerEntryChange) string {
	t.Helper()
	return metaV3WithTx(t, nil, nil, changes)
}

// richContract builds the deployment every passing test starts from: an owner
// and several values in instance storage, plus persistent and temporary
// entries and a second contract's data to prove it is ignored.
func richContract(t *testing.T) (contract, meta string) {
	t.Helper()

	contract = constructorContractID(t, 0x10)
	other := constructorContractID(t, 0x40)

	instance := contractInstanceEntry(t, contract,
		xdr.ScMapEntry{Key: scSymbol("Owner"), Val: scAddress(t, testSource)},
		xdr.ScMapEntry{Key: scSymbol("name"), Val: scString("USD Coin")},
		xdr.ScMapEntry{Key: scSymbol("supply"), Val: scU32(1000)},
		xdr.ScMapEntry{Key: scSymbol("grant"), Val: scU32(1000)},
		xdr.ScMapEntry{Key: scSymbol("limit"), Val: scU64(9_000_000_000)},
		xdr.ScMapEntry{Key: scSymbol("fee"), Val: scI32(-5)},
		xdr.ScMapEntry{Key: scSymbol("offset"), Val: scI64(-9000)},
		xdr.ScMapEntry{Key: scSymbol("flag"), Val: scBool(true)},
	)
	persistent := contractDataEntry(t, contract,
		scSymbol("admin"), scAddress(t, testDest), xdr.ContractDataDurabilityPersistent)
	temporary := contractDataEntry(t, contract,
		scSymbol("locked"), scBool(true), xdr.ContractDataDurabilityTemporary)
	unrelated := contractDataEntry(t, other,
		scSymbol("unrelated"), scU32(1), xdr.ContractDataDurabilityPersistent)

	meta = deploymentMeta(t,
		createdChange(instance),
		createdChange(persistent),
		createdChange(temporary),
		createdChange(unrelated),
	)
	return contract, meta
}

func TestAssertConstructorStatePasses(t *testing.T) {
	t.Parallel()

	contract, meta := richContract(t)
	_, lensValue, err := focus.Encode(scU32(1000), "ScVal")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	tests := []struct {
		name string
		want focus.ConstructorState
	}{
		{
			name: "instance storage and owner",
			want: focus.ConstructorState{
				ContractID: contract,
				Owner:      testSource,
				Entries:    map[string]any{"supply": uint32(1000)},
			},
		},
		{
			// A #[contracttype] enum variant is commonly "Owner" while the
			// field is "owner"; key matching is deliberately forgiving.
			name: "owner key ignores case",
			want: focus.ConstructorState{ContractID: contract, Owner: testSource, OwnerKey: "OWNER"},
		},
		{
			name: "custom owner key",
			want: focus.ConstructorState{ContractID: contract, Owner: testSource, OwnerKey: "Owner"},
		},
		{
			name: "persistent entry",
			want: focus.ConstructorState{ContractID: contract, Entries: map[string]any{"admin": testDest}},
		},
		{
			name: "temporary entry",
			want: focus.ConstructorState{ContractID: contract, Entries: map[string]any{"locked": true}},
		},
		{
			name: "string key and value",
			want: focus.ConstructorState{ContractID: contract, Entries: map[string]any{"name": scString("USD Coin")}},
		},
		{
			name: "uint32 value",
			want: focus.ConstructorState{ContractID: contract, Entries: map[string]any{"grant": uint32(1000)}},
		},
		{
			// Checking only that the deployment created the instance entry is
			// a legitimate, if weak, assertion.
			name: "no expectations",
			want: focus.ConstructorState{ContractID: contract},
		},
		{
			name: "every documented value form",
			want: focus.ConstructorState{
				ContractID: contract,
				Entries: map[string]any{
					"supply": scValPtr(scU32(1000)),
					"limit":  uint64(9_000_000_000),
					"fee":    int32(-5),
					"offset": int(-9000),
					"flag":   mustMarshal(t, scBool(true)),
					"name":   scString("USD Coin"),
					"admin":  testDest,
					"owner":  scAddress(t, testSource),
					"grant":  lensValue,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertConstructorState(tb, meta, tc.want) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("a passing assertion reported a failure:\n%s", tb.Output())
			}
		})
	}
}

func TestAssertConstructorStateFailureMessages(t *testing.T) {
	t.Parallel()

	contract, meta := richContract(t)

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "wrong value names the key and both sides",
			call: func(tb focus.TB) bool {
				return focus.AssertConstructorState(tb, meta, focus.ConstructorState{
					ContractID: contract,
					Entries:    map[string]any{"supply": uint32(2000)},
				})
			},
			contains: []string{
				"AssertConstructorState",
				"was initialized with different state",
				"1 difference",
				"~ supply",
				"want  u32(2000)",
				"got   u32(1000)",
				"instance storage",
				"state written by the constructor (10 entries)",
			},
		},
		{
			name: "missing entry says it is missing",
			call: func(tb focus.TB) bool {
				return focus.AssertConstructorState(tb, meta, focus.ConstructorState{
					ContractID: contract,
					Entries:    map[string]any{"minted": true},
				})
			},
			contains: []string{"~ minted", "want  true", "(missing)"},
		},
		{
			name: "wrong owner names both addresses",
			call: func(tb focus.TB) bool {
				return focus.AssertConstructorState(tb, meta, focus.ConstructorState{
					ContractID: contract,
					Owner:      testDest,
				})
			},
			contains: []string{"~ owner", "want  address GAPB7…WYXZ", "got   address GCJNH…ZST6"},
		},
		{
			name: "several differences are reported at once",
			call: func(tb focus.TB) bool {
				return focus.AssertConstructorState(tb, meta, focus.ConstructorState{
					ContractID: contract,
					Owner:      testDest,
					Entries:    map[string]any{"supply": uint32(2000), "missing": uint32(1)},
				})
			},
			contains: []string{"3 differences", "~ owner", "~ supply", "~ missing"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
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

// TestAssertConstructorStateLastWriteWins checks that a key written more than
// once in a transaction is read as it ended up, not as it was first set.
func TestAssertConstructorStateLastWriteWins(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	instance := contractInstanceEntry(t, contract)
	first := contractDataEntry(t, contract,
		scSymbol("supply"), scU32(1000), xdr.ContractDataDurabilityPersistent)
	second := contractDataEntry(t, contract,
		scSymbol("supply"), scU32(2000), xdr.ContractDataDurabilityPersistent)
	meta := deploymentMeta(t, createdChange(instance), createdChange(first), updatedChange(second))

	tb := faketb.New()
	if !focus.AssertConstructorState(tb, meta, focus.ConstructorState{
		ContractID: contract,
		Entries:    map[string]any{"supply": uint32(2000)},
	}) {
		t.Errorf("the final value was not the one read:\n%s", tb.Output())
	}

	tb = faketb.New()
	if focus.AssertConstructorState(tb, meta, focus.ConstructorState{
		ContractID: contract,
		Entries:    map[string]any{"supply": uint32(1000)},
	}) {
		t.Error("the overwritten value was accepted")
	}
}

// TestAssertConstructorStateIgnoresBeforeImages checks that a state change —
// the value before an update — is not mistaken for something the transaction
// wrote.
func TestAssertConstructorStateIgnoresBeforeImages(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	instance := contractInstanceEntry(t, contract)
	stale := contractDataEntry(t, contract,
		scSymbol("stale"), scU32(1), xdr.ContractDataDurabilityPersistent)
	current := contractDataEntry(t, contract,
		scSymbol("current"), scU32(2), xdr.ContractDataDurabilityPersistent)
	meta := deploymentMeta(t, createdChange(instance), stateChange(stale), createdChange(current))

	tb := faketb.New()
	if !focus.AssertConstructorState(tb, meta, focus.ConstructorState{
		ContractID: contract,
		Entries:    map[string]any{"current": uint32(2)},
	}) {
		t.Errorf("the written entry was not read:\n%s", tb.Output())
	}

	tb = faketb.New()
	if focus.AssertConstructorState(tb, meta, focus.ConstructorState{
		ContractID: contract,
		Entries:    map[string]any{"stale": uint32(1)},
	}) {
		t.Error("a before-image was accepted as constructor state")
	}
}

// TestAssertConstructorStateDiagnosesBadInput checks that every input mistake
// is reported as the mistake it is, rather than as a missing entry.
func TestAssertConstructorStateDiagnosesBadInput(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	other := constructorContractID(t, 0x40)
	instance := contractInstanceEntry(t, contract,
		xdr.ScMapEntry{Key: scSymbol("owner"), Val: scAddress(t, testSource)})
	valid := deploymentMeta(t, createdChange(instance))
	classic := mustMarshal(t, xdr.TransactionMeta{V: 0, Operations: &[]xdr.OperationMeta{}})
	otherData := contractDataEntry(t, other,
		scSymbol("owner"), scAddress(t, testSource), xdr.ContractDataDurabilityPersistent)
	noInstance := contractDataEntry(t, contract,
		scSymbol("owner"), scAddress(t, testSource), xdr.ContractDataDurabilityPersistent)

	tests := []struct {
		name     string
		meta     focus.Value
		want     focus.ConstructorState
		contains []string
	}{
		{
			name:     "empty contract id",
			meta:     valid,
			want:     focus.ConstructorState{},
			contains: []string{"contractID is empty"},
		},
		{
			name:     "account instead of contract",
			meta:     valid,
			want:     focus.ConstructorState{ContractID: testSource},
			contains: []string{"is not a contract address", "strkey C…"},
		},
		{
			name:     "invalid contract strkey",
			meta:     valid,
			want:     focus.ConstructorState{ContractID: "CAAAA"},
			contains: []string{"not a valid strkey contract address"},
		},
		{
			name:     "nil meta",
			meta:     nil,
			want:     focus.ConstructorState{ContractID: contract},
			contains: []string{"could not be read"},
		},
		{
			name:     "classic meta",
			meta:     classic,
			want:     focus.ConstructorState{ContractID: contract},
			contains: []string{"no contract data for contract", "carries no contract data at all", "deployment transaction"},
		},

		{
			name:     "wrong contract id lists what was written",
			meta:     deploymentMeta(t, createdChange(otherData)),
			want:     focus.ConstructorState{ContractID: contract},
			contains: []string{"no contract data for contract", "contract data present", shortenForTest(other)},
		},
		{
			name:     "state without an instance entry",
			meta:     deploymentMeta(t, createdChange(noInstance)),
			want:     focus.ConstructorState{ContractID: contract},
			contains: []string{"no contract instance entry", "deployment did not create it"},
		},
		{
			name:     "invalid owner",
			meta:     valid,
			want:     focus.ConstructorState{ContractID: contract, Owner: "not-an-address"},
			contains: []string{"Owner", "is not a strkey account"},
		},
		{
			name: "owner also listed in entries",
			meta: valid,
			want: focus.ConstructorState{
				ContractID: contract,
				Owner:      testSource,
				Entries:    map[string]any{"OWNER": testSource},
			},
			contains: []string{"also lists the owner key"},
		},
		{
			name:     "empty entry key",
			meta:     valid,
			want:     focus.ConstructorState{ContractID: contract, Entries: map[string]any{"": true}},
			contains: []string{"empty storage key"},
		},
		{
			name:     "unsupported entry value",
			meta:     valid,
			want:     focus.ConstructorState{ContractID: contract, Entries: map[string]any{"supply": 1.5}},
			contains: []string{`the Entries value for "supply"`, "unsupported type"},
		},
		{
			name:     "nil entry value",
			meta:     valid,
			want:     focus.ConstructorState{ContractID: contract, Entries: map[string]any{"supply": nil}},
			contains: []string{`the Entries value for "supply"`, "value is nil"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertConstructorState(tb, tc.meta, tc.want) {
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
			if strings.Contains(msg, "was initialized with different state") {
				t.Errorf("an input error was reported as a state difference:\n%s", msg)
			}
		})
	}
}

// shortenForTest mirrors the short form the failure messages use.
func shortenForTest(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:5] + "…" + s[len(s)-4:]
}

func TestAssertContractOwnerPasses(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	ownerContract := constructorContractID(t, 0x20)

	tests := []struct {
		name string
		meta string
		key  string
	}{
		{
			name: "account owner in instance storage",
			meta: deploymentMeta(t, createdChange(contractInstanceEntry(t, contract,
				xdr.ScMapEntry{Key: scSymbol("Owner"), Val: scAddress(t, testSource)}))),
		},
		{
			name: "contract owner in instance storage",
			meta: deploymentMeta(t, createdChange(contractInstanceEntry(t, contract,
				xdr.ScMapEntry{Key: scSymbol("owner"), Val: scAddress(t, ownerContract)}))),
		},
		{
			name: "owner in a persistent entry",
			meta: deploymentMeta(t,
				createdChange(contractInstanceEntry(t, contract)),
				createdChange(contractDataEntry(t, contract, scSymbol("owner"),
					scAddress(t, testSource), xdr.ContractDataDurabilityPersistent))),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want := testSource
			if strings.Contains(tc.name, "contract owner") {
				want = ownerContract
			}
			tb := faketb.New()
			if !focus.AssertContractOwner(tb, tc.meta, contract, want) {
				t.Errorf("owner was not matched:\n%s", tb.Output())
			}
		})
	}
}

func TestAssertContractOwnerFailureMessages(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	wrongOwner := deploymentMeta(t, createdChange(contractInstanceEntry(t, contract,
		xdr.ScMapEntry{Key: scSymbol("owner"), Val: scAddress(t, testDest)})))
	noOwner := deploymentMeta(t, createdChange(contractInstanceEntry(t, contract,
		xdr.ScMapEntry{Key: scSymbol("supply"), Val: scU32(1)})))
	notAddress := deploymentMeta(t, createdChange(contractInstanceEntry(t, contract,
		xdr.ScMapEntry{Key: scSymbol("owner"), Val: scSymbol("alice")})))

	tests := []struct {
		name     string
		meta     focus.Value
		want     string
		contains []string
	}{
		{
			name:     "wrong owner shows both",
			meta:     wrongOwner,
			want:     testSource,
			contains: []string{"binds owner to address GAPB7…WYXZ", "want address GCJNH…ZST6", "state written by the constructor"},
		},
		{
			name:     "missing owner lists what was written",
			meta:     noOwner,
			want:     testSource,
			contains: []string{"has no owner binding", `"supply"`, "u32(1)"},
		},
		{
			name:     "non-address owner is called out",
			meta:     notAddress,
			want:     testSource,
			contains: []string{"is not an address", `"alice"`, "strkey account"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertContractOwner(tb, tc.meta, contract, tc.want) {
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

func TestAssertContractOwnerDiagnosesBadInput(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	meta := deploymentMeta(t, createdChange(contractInstanceEntry(t, contract,
		xdr.ScMapEntry{Key: scSymbol("owner"), Val: scAddress(t, testSource)})))

	tests := []struct {
		name     string
		contract string
		owner    string
		contains []string
	}{
		{"empty contract id", "", testSource, []string{"contractID is empty"}},
		{"invalid contract id", contract + "X", testSource, []string{"not a valid strkey contract address"}},
		{"empty owner", contract, "", []string{"wantOwner is empty"}},
		{"invalid owner", contract, "alice", []string{"is not a strkey account"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if focus.AssertContractOwner(tb, meta, tc.contract, tc.owner) {
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
		})
	}
}

// TestAssertConstructorStateEmptyStorageMessage covers the deployment that ran
// but wrote nothing: a real possibility for a constructor with no state, and
// one that must be reported as a missing entry rather than a broken assertion.
func TestAssertConstructorStateEmptyStorageMessage(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	meta := deploymentMeta(t, createdChange(contractInstanceEntry(t, contract)))

	tb := faketb.New()
	if focus.AssertConstructorState(tb, meta, focus.ConstructorState{
		ContractID: contract,
		Owner:      testSource,
	}) {
		t.Fatal("assertion passed for a missing entry")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"~ owner", "wrote no storage entries"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

// TestConstructorStateRendersSorobanValues checks that the failure listing
// renders the value types a constructor actually writes, instead of falling
// back to a bare type name.
func TestConstructorStateRendersSorobanValues(t *testing.T) {
	t.Parallel()

	contract := constructorContractID(t, 0x10)
	big128 := xdr.ScVal{
		Type: xdr.ScValTypeScvI128,
		I128: &xdr.Int128Parts{Hi: xdr.Int64(1), Lo: xdr.Uint64(0)},
	}
	u128 := xdr.ScVal{
		Type: xdr.ScValTypeScvU128,
		U128: &xdr.UInt128Parts{Hi: xdr.Uint64(1), Lo: xdr.Uint64(0)},
	}
	bytes := xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: bytesPtr(make([]byte, 20))}
	vec := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: vecPtr([]xdr.ScVal{scU32(1), scU32(2), scU32(3), scU32(4)})}
	m := xdr.ScMap{
		{Key: scSymbol("a"), Val: scU32(1)},
		{Key: scSymbol("b"), Val: scU32(2)},
		{Key: scSymbol("c"), Val: scU32(3)},
		{Key: scSymbol("d"), Val: scU32(4)},
	}
	scMap := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: mapPtr(m)}

	instance := contractInstanceEntry(t, contract,
		xdr.ScMapEntry{Key: scSymbol("big"), Val: big128},
		xdr.ScMapEntry{Key: scSymbol("ubig"), Val: u128},
		xdr.ScMapEntry{Key: scSymbol("blob"), Val: bytes},
		xdr.ScMapEntry{Key: scSymbol("list"), Val: vec},
		xdr.ScMapEntry{Key: scSymbol("record"), Val: scMap},
		xdr.ScMapEntry{Key: scU32(7), Val: scU32(1)},
	)
	meta := deploymentMeta(t, createdChange(instance))

	want := focus.ConstructorState{
		ContractID: contract,
		Entries: map[string]any{
			"big":    uint32(0),
			"ubig":   uint32(0),
			"blob":   uint32(0),
			"list":   uint32(0),
			"record": uint32(0),
		},
	}

	tb := faketb.New()
	if focus.AssertConstructorState(tb, meta, want) {
		t.Fatal("assertion passed with mismatched values")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{
		"i128(18446744073709551616)",
		"u128(18446744073709551616)",
		"bytes(0x0000000000000000…, 20 bytes)",
		"vec[u32(1), u32(2), u32(3), … +1]",
		"map{\"a\": u32(1), \"b\": u32(2), \"c\": u32(3), … +1}",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not render %q:\n%s", want, msg)
		}
	}
}

func bytesPtr(b []byte) *xdr.ScBytes {
	v := xdr.ScBytes(b)
	return &v
}

func vecPtr(v []xdr.ScVal) **xdr.ScVec {
	scVec := xdr.ScVec(v)
	ptr := &scVec
	return &ptr
}

func mapPtr(v xdr.ScMap) **xdr.ScMap {
	ptr := &v
	return &ptr
}

// TestRequireConstructorMirrors checks that both new assertions have a fatal
// mirror and produce the same message, only with different severity.
func TestRequireConstructorMirrors(t *testing.T) {
	t.Parallel()

	contract, meta := richContract(t)

	t.Run("constructor state passes", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		require.ConstructorState(tb, meta, focus.ConstructorState{
			ContractID: contract,
			Owner:      testSource,
		})
		if tb.Failed() {
			t.Errorf("require reported a failure for matching state:\n%s", tb.Output())
		}
	})

	t.Run("constructor state is fatal", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		require.ConstructorState(tb, meta, focus.ConstructorState{
			ContractID: contract,
			Entries:    map[string]any{"missing": true},
		})
		if len(tb.Errors) != 0 || len(tb.Fatals) != 1 {
			t.Errorf("require used Errorf instead of Fatalf (errors=%d fatals=%d)",
				len(tb.Errors), len(tb.Fatals))
		}
		if !strings.Contains(tb.Output(), "was initialized with different state") {
			t.Errorf("require lost the message:\n%s", tb.Output())
		}
	})

	t.Run("contract owner is fatal", func(t *testing.T) {
		t.Parallel()

		tb := faketb.New()
		require.ContractOwner(tb, meta, contract, testDest)
		if len(tb.Errors) != 0 || len(tb.Fatals) != 1 {
			t.Errorf("require used Errorf instead of Fatalf (errors=%d fatals=%d)",
				len(tb.Errors), len(tb.Fatals))
		}
		if !strings.Contains(tb.Output(), "binds owner to") {
			t.Errorf("require lost the message:\n%s", tb.Output())
		}
	})
}
