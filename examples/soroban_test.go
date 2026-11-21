package examples_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// Helpers to construct realistic Soroban test fixtures.

func exampleContractAddress(seed byte) (string, xdr.ContractId) {
	var id xdr.ContractId
	for i := range id {
		id[i] = seed
	}
	addr, _ := strkey.Encode(strkey.VersionByteContract, id[:])
	return addr, id
}

func exampleAccountAddress(seed byte) string {
	var pub [32]byte
	for i := range pub {
		pub[i] = seed
	}
	addr, _ := strkey.Encode(strkey.VersionByteAccountID, pub[:])
	return addr
}

func scValSymbol(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func scValU32(n uint32) xdr.ScVal {
	val := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &val}
}

func scValU64(n uint64) xdr.ScVal {
	val := xdr.Uint64(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &val}
}

func scValAddress(addr string) xdr.ScVal {
	raw, _ := strkey.Decode(strkey.VersionByteAccountID, addr)
	var pubKey xdr.Uint256
	copy(pubKey[:], raw)
	return xdr.ScVal{
		Type: xdr.ScValTypeScvAddress,
		Address: &xdr.ScAddress{
			Type:      xdr.ScAddressTypeScAddressTypeAccount,
			AccountId: &xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &pubKey},
		},
	}
}

func exampleEnvelope(instructions, readBytes, writeBytes uint32, resourceFee int64) xdr.TransactionEnvelope {
	var pub xdr.Uint256
	pub[0] = 1
	pub[1] = 2
	pub[2] = 3
	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: xdr.MuxedAccount{
					Type:    xdr.CryptoKeyTypeKeyTypeEd25519,
					Ed25519: &pub,
				},
				Fee:    100,
				SeqNum: 10,
				Ext: xdr.TransactionExt{
					V: 1,
					SorobanData: &xdr.SorobanTransactionData{
						Resources: xdr.SorobanResources{
							Instructions:  xdr.Uint32(instructions),
							DiskReadBytes: xdr.Uint32(readBytes),
							WriteBytes:    xdr.Uint32(writeBytes),
						},
						ResourceFee: xdr.Int64(resourceFee),
					},
				},
			},
		},
	}
}

func marshalXDR(t *testing.T, v any) string {
	t.Helper()
	b, _, err := focus.Encode(v, "")
	if err != nil {
		t.Fatalf("marshalling XDR: %v", err)
	}
	return b
}

// TestSorobanDeploymentConstructorState demonstrates asserting on initial state
// set by a contract's constructor during deployment.
func TestSorobanDeploymentConstructorState(t *testing.T) {
	contractAddr, contractID := exampleContractAddress(0x42)
	adminAddr := exampleAccountAddress(0x01)

	var wasmHash xdr.Hash
	wasmHash[0] = 0x42
	// Create contract instance ledger entry with initial state
	instanceEntry := xdr.LedgerEntry{
		LastModifiedLedgerSeq: 100,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeContractData,
			ContractData: &xdr.ContractDataEntry{
				Contract: xdr.ScAddress{
					Type:       xdr.ScAddressTypeScAddressTypeContract,
					ContractId: &contractID,
				},
				Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
				Durability: xdr.ContractDataDurabilityPersistent,
				Val: xdr.ScVal{
					Type: xdr.ScValTypeScvContractInstance,
					Instance: &xdr.ScContractInstance{
						Executable: xdr.ContractExecutable{
							Type:     xdr.ContractExecutableTypeContractExecutableWasm,
							WasmHash: &wasmHash,
						},
						Storage: &xdr.ScMap{
							{Key: scValSymbol("admin"), Val: scValAddress(adminAddr)},
							{Key: scValSymbol("decimals"), Val: scValU32(7)},
							{Key: scValSymbol("name"), Val: scValSymbol("FocusToken")},
						},
					},
				},
			},
		},
	}

	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			TxChangesAfter: []xdr.LedgerEntryChange{
				{
					Type:    xdr.LedgerEntryChangeTypeLedgerEntryCreated,
					Created: &instanceEntry,
				},
			},
		},
	}
	metaBase64 := marshalXDR(t, meta)

	// Verify the contract constructor initialized the expected instance storage
	focus.AssertConstructorState(t, metaBase64, focus.ConstructorState{
		ContractID: contractAddr,
		Owner:      adminAddr,
		OwnerKey:   "admin",
		Entries: map[string]any{
			"decimals": uint32(7),
			"name":     "FocusToken",
		},
	})
}

// TestSorobanInvocationReturnValue demonstrates asserting on contract call return values.
func TestSorobanInvocationReturnValue(t *testing.T) {
	retVal := scValU64(100_0000000)
	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue: retVal,
			},
		},
	}
	metaBase64 := marshalXDR(t, meta)

	// Assert the contract function returned 100_0000000
	focus.AssertReturnValue(t, metaBase64, scValU64(100_0000000))
}

// TestSorobanContractEvents demonstrates matching sequences of emitted contract events.
func TestSorobanContractEvents(t *testing.T) {
	contractAddr, contractID := exampleContractAddress(0x11)
	userAddr := exampleAccountAddress(0x02)

	event := xdr.ContractEvent{
		ContractId: &contractID,
		Type:       xdr.ContractEventTypeContract,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: []xdr.ScVal{
					scValSymbol("transfer"),
					scValAddress(userAddr),
				},
				Data: scValU64(500),
			},
		},
	}

	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
				Events:      []xdr.ContractEvent{event},
			},
		},
	}
	metaBase64 := marshalXDR(t, meta)

	// Assert on the emitted event sequence
	focus.AssertEventsEqual(t, metaBase64, []focus.Event{
		{
			ContractID: contractAddr,
			Topics:     []any{"transfer", scValAddress(userAddr)},
			Data:       scValU64(500),
		},
	})
}

// TestSorobanContractPanic demonstrates testing contract traps and asserting panic error codes.
func TestSorobanContractPanic(t *testing.T) {
	symError := xdr.ScSymbol("error")
	symCode := xdr.ScSymbol("Error")
	symContract := xdr.ScSymbol("Contract")
	codeVal := xdr.Uint32(101)

	// Diagnostic events carrying the trap reason
	trapEvents := []xdr.DiagnosticEvent{
		{
			InSuccessfulContractCall: false,
			Event: xdr.ContractEvent{
				Type: xdr.ContractEventTypeDiagnostic,
				Body: xdr.ContractEventBody{
					V: 0,
					V0: &xdr.ContractEventV0{
						Topics: []xdr.ScVal{
							{Type: xdr.ScValTypeScvSymbol, Sym: &symError},
							{Type: xdr.ScValTypeScvSymbol, Sym: &symCode},
							{Type: xdr.ScValTypeScvSymbol, Sym: &symContract},
							{Type: xdr.ScValTypeScvU32, U32: &codeVal},
						},
						Data: xdr.ScVal{
							Type: xdr.ScValTypeScvError,
							Error: &xdr.ScError{
								Type:         xdr.ScErrorTypeSceContract,
								ContractCode: &codeVal,
							},
						},
					},
				},
			},
		},
	}

	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue:      xdr.ScVal{Type: xdr.ScValTypeScvVoid},
				DiagnosticEvents: trapEvents,
			},
		},
	}
	metaBase64 := marshalXDR(t, meta)

	// Assert the contract trapped with contract error code 101
	focus.AssertContractPanicReason(t, metaBase64, uint32(101))
	require.ContractPanicReason(t, metaBase64, uint32(101))
}

// TestSorobanContractDiagnosticLogs demonstrates asserting on formatted diagnostic log messages.
func TestSorobanContractDiagnosticLogs(t *testing.T) {
	symLog := xdr.ScSymbol("log")
	logText := xdr.ScString("deposit 500 stroops for account GCXYZ completed")

	diagEvents := []xdr.DiagnosticEvent{
		{
			InSuccessfulContractCall: true,
			Event: xdr.ContractEvent{
				Type: xdr.ContractEventTypeDiagnostic,
				Body: xdr.ContractEventBody{
					V: 0,
					V0: &xdr.ContractEventV0{
						Topics: []xdr.ScVal{
							{Type: xdr.ScValTypeScvSymbol, Sym: &symLog},
						},
						Data: xdr.ScVal{
							Type: xdr.ScValTypeScvString,
							Str:  &logText,
						},
					},
				},
			},
		},
	}

	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue:      xdr.ScVal{Type: xdr.ScValTypeScvVoid},
				DiagnosticEvents: diagEvents,
			},
		},
	}
	metaBase64 := marshalXDR(t, meta)

	// Literal substring matching
	focus.AssertContractLogMessage(t, metaBase64, "deposit 500")

	// Regular expression matching with /.../
	focus.AssertContractLogMessage(t, metaBase64, `/deposit \d+ stroops/`)
	require.ContractLogMessage(t, metaBase64, `/deposit \d+ stroops/`)
}

// TestSorobanResourceLimits demonstrates checking CPU instructions and footprint bounds.
func TestSorobanResourceLimits(t *testing.T) {
	env := exampleEnvelope(500_000, 1024, 2048, 25_000)
	envBase64 := marshalXDR(t, env)

	// Check instruction and storage footprint limits
	focus.AssertSorobanResources(t, envBase64, 500_000, 1024, 2048)

	// Check refundable resource fee
	focus.AssertResourceFee(t, envBase64, 25_000)
}
