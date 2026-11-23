// Package simulate provides Soroban simulation and offline analysis tooling.
package simulate

import (
	"encoding/base64"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ConflictType names the category of footprint conflict between two transactions.
type ConflictType string

const (
	// ConflictTypeReadWrite occurs when one transaction reads a key that another writes.
	ConflictTypeReadWrite ConflictType = "ReadWrite"
	// ConflictTypeWriteWrite occurs when both transactions write to the same key.
	ConflictTypeWriteWrite ConflictType = "WriteWrite"

	// ReadWrite is an alias for ConflictTypeReadWrite.
	ReadWrite ConflictType = "ReadWrite"
	// WriteWrite is an alias for ConflictTypeWriteWrite.
	WriteWrite ConflictType = "WriteWrite"
)

// FootprintConflict describes a concurrency collision on a ledger entry between two transactions.
type FootprintConflict struct {
	Key          xdr.LedgerKey
	TxAIndex     int
	TxBIndex     int
	ConflictType ConflictType
}

// String returns a human-readable description of the conflict.
func (c FootprintConflict) String() string {
	raw, _ := c.Key.MarshalBinary()
	keyB64 := base64.StdEncoding.EncodeToString(raw)
	return fmt.Sprintf("tx %d and tx %d have %s conflict on ledger key %s",
		c.TxAIndex, c.TxBIndex, c.ConflictType, keyB64)
}

// ExtractFootprint extracts the Soroban ledger footprint from a transaction envelope, if present.
func ExtractFootprint(env xdr.TransactionEnvelope) (*xdr.LedgerFootprint, bool) {
	var sorobanData *xdr.SorobanTransactionData
	switch env.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		if env.V1 != nil && env.V1.Tx.Ext.V == 1 && env.V1.Tx.Ext.SorobanData != nil {
			sorobanData = env.V1.Tx.Ext.SorobanData
		}
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		if env.FeeBump != nil && env.FeeBump.Tx.InnerTx.Type == xdr.EnvelopeTypeEnvelopeTypeTx {
			inner := env.FeeBump.Tx.InnerTx.V1
			if inner != nil && inner.Tx.Ext.V == 1 && inner.Tx.Ext.SorobanData != nil {
				sorobanData = inner.Tx.Ext.SorobanData
			}
		}
	}
	if sorobanData == nil {
		return nil, false
	}
	return &sorobanData.Resources.Footprint, true
}

// DetectFootprintConflicts analyzes a batch of transaction envelopes and detects all resource
// footprint collisions between pairs of transactions that would prevent concurrent execution.
func DetectFootprintConflicts(txs []xdr.TransactionEnvelope) []FootprintConflict {
	var conflicts []FootprintConflict
	if len(txs) < 2 {
		return conflicts
	}

	type txFootprint struct {
		hasFootprint bool
		readOnly     map[string]xdr.LedgerKey
		readWrite    map[string]xdr.LedgerKey
	}

	fps := make([]txFootprint, len(txs))
	for i, env := range txs {
		fp, ok := ExtractFootprint(env)
		if !ok || fp == nil {
			continue
		}
		fps[i].hasFootprint = true
		fps[i].readOnly = make(map[string]xdr.LedgerKey, len(fp.ReadOnly))
		fps[i].readWrite = make(map[string]xdr.LedgerKey, len(fp.ReadWrite))

		for _, k := range fp.ReadOnly {
			raw, err := k.MarshalBinary()
			if err == nil {
				fps[i].readOnly[string(raw)] = k
			}
		}
		for _, k := range fp.ReadWrite {
			raw, err := k.MarshalBinary()
			if err == nil {
				fps[i].readWrite[string(raw)] = k
			}
		}
	}

	for i := 0; i < len(txs); i++ {
		if !fps[i].hasFootprint {
			continue
		}
		for j := i + 1; j < len(txs); j++ {
			if !fps[j].hasFootprint {
				continue
			}

			seenKeys := make(map[string]bool)

			// 1. Write-Write conflicts: key in both txA.readWrite and txB.readWrite
			for keyID, key := range fps[i].readWrite {
				if _, ok := fps[j].readWrite[keyID]; ok {
					conflicts = append(conflicts, FootprintConflict{
						Key:          key,
						TxAIndex:     i,
						TxBIndex:     j,
						ConflictType: ConflictTypeWriteWrite,
					})
					seenKeys[keyID] = true
				}
			}

			// 2. Read-Write conflicts: key in txA.readWrite and txB.readOnly
			for keyID, key := range fps[i].readWrite {
				if seenKeys[keyID] {
					continue
				}
				if _, ok := fps[j].readOnly[keyID]; ok {
					conflicts = append(conflicts, FootprintConflict{
						Key:          key,
						TxAIndex:     i,
						TxBIndex:     j,
						ConflictType: ConflictTypeReadWrite,
					})
					seenKeys[keyID] = true
				}
			}

			// 3. Write-Read conflicts: key in txA.readOnly and txB.readWrite
			for keyID, key := range fps[i].readOnly {
				if seenKeys[keyID] {
					continue
				}
				if _, ok := fps[j].readWrite[keyID]; ok {
					conflicts = append(conflicts, FootprintConflict{
						Key:          key,
						TxAIndex:     i,
						TxBIndex:     j,
						ConflictType: ConflictTypeReadWrite,
					})
					seenKeys[keyID] = true
				}
			}
		}
	}

	return conflicts
}
