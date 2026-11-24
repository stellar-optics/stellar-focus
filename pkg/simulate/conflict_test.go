package simulate_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/simulate"
)

func makeTestEnvelope(readOnly, readWrite []xdr.LedgerKey) xdr.TransactionEnvelope {
	return xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				Ext: xdr.TransactionExt{
					V: 1,
					SorobanData: &xdr.SorobanTransactionData{
						Resources: xdr.SorobanResources{
							Footprint: xdr.LedgerFootprint{
								ReadOnly:  readOnly,
								ReadWrite: readWrite,
							},
						},
					},
				},
			},
		},
	}
}

func testKey(id byte) xdr.LedgerKey {
	var hash xdr.Hash
	hash[0] = id
	return xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeContractCode,
		ContractCode: &xdr.LedgerKeyContractCode{
			Hash: hash,
		},
	}
}

func TestDetectFootprintConflicts(t *testing.T) {
	t.Run("No conflicts on disjoint footprints", func(t *testing.T) {
		tx1 := makeTestEnvelope([]xdr.LedgerKey{testKey(1)}, []xdr.LedgerKey{testKey(2)})
		tx2 := makeTestEnvelope([]xdr.LedgerKey{testKey(3)}, []xdr.LedgerKey{testKey(4)})

		conflicts := simulate.DetectFootprintConflicts([]xdr.TransactionEnvelope{tx1, tx2})
		if len(conflicts) != 0 {
			t.Fatalf("expected 0 conflicts, got %d: %v", len(conflicts), conflicts)
		}
	})

	t.Run("No conflicts on read-only overlap", func(t *testing.T) {
		tx1 := makeTestEnvelope([]xdr.LedgerKey{testKey(1), testKey(2)}, nil)
		tx2 := makeTestEnvelope([]xdr.LedgerKey{testKey(2), testKey(3)}, nil)

		conflicts := simulate.DetectFootprintConflicts([]xdr.TransactionEnvelope{tx1, tx2})
		if len(conflicts) != 0 {
			t.Fatalf("expected 0 conflicts on read-read overlap, got %d: %v", len(conflicts), conflicts)
		}
	})

	t.Run("Write-Write conflict", func(t *testing.T) {
		tx1 := makeTestEnvelope(nil, []xdr.LedgerKey{testKey(1), testKey(2)})
		tx2 := makeTestEnvelope(nil, []xdr.LedgerKey{testKey(2), testKey(3)})

		conflicts := simulate.DetectFootprintConflicts([]xdr.TransactionEnvelope{tx1, tx2})
		if len(conflicts) != 1 {
			t.Fatalf("expected 1 conflict, got %d: %v", len(conflicts), conflicts)
		}
		c := conflicts[0]
		if c.ConflictType != simulate.ConflictTypeWriteWrite {
			t.Errorf("conflict type = %v, want WriteWrite", c.ConflictType)
		}
		if c.TxAIndex != 0 || c.TxBIndex != 1 {
			t.Errorf("tx indices = (%d, %d), want (0, 1)", c.TxAIndex, c.TxBIndex)
		}
		if c.Key.ContractCode.Hash[0] != 2 {
			t.Errorf("conflicting key = %v, want key 2", c.Key)
		}
	})

	t.Run("Read-Write conflict", func(t *testing.T) {
		tx1 := makeTestEnvelope([]xdr.LedgerKey{testKey(5)}, nil)
		tx2 := makeTestEnvelope(nil, []xdr.LedgerKey{testKey(5)})

		conflicts := simulate.DetectFootprintConflicts([]xdr.TransactionEnvelope{tx1, tx2})
		if len(conflicts) != 1 {
			t.Fatalf("expected 1 conflict, got %d: %v", len(conflicts), conflicts)
		}
		c := conflicts[0]
		if c.ConflictType != simulate.ConflictTypeReadWrite {
			t.Errorf("conflict type = %v, want ReadWrite", c.ConflictType)
		}
	})

	t.Run("Non-Soroban envelopes ignored safely", func(t *testing.T) {
		classicTx := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTxV0}
		sorobanTx := makeTestEnvelope(nil, []xdr.LedgerKey{testKey(1)})

		conflicts := simulate.DetectFootprintConflicts([]xdr.TransactionEnvelope{classicTx, sorobanTx})
		if len(conflicts) != 0 {
			t.Fatalf("expected 0 conflicts, got %d", len(conflicts))
		}
	})
}
