package focus

import (
	"encoding/base64"
	"fmt"
	"strings"

	rpcproto "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/simulate"
)

// AssertNoFootprintConflicts asserts that the given Soroban transactions have no resource
// footprint collisions (ReadWrite or WriteWrite) that would prevent concurrent execution in the same ledger.
func AssertNoFootprintConflicts(t TB, txs []xdr.TransactionEnvelope) bool {
	t.Helper()
	conflicts := simulate.DetectFootprintConflicts(txs)
	if len(conflicts) == 0 {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "AssertNoFootprintConflicts: found %d footprint conflict(s) across %d transaction(s):\n\n",
		len(conflicts), len(txs))
	for i, c := range conflicts {
		raw, err := c.Key.MarshalBinary()
		var keyB64 string
		if err != nil {
			keyB64 = "<invalid-key>"
		} else {
			keyB64 = base64.StdEncoding.EncodeToString(raw)
		}
		fmt.Fprintf(&b, "  [%d] tx %d and tx %d: %s conflict on key %s (%s)\n",
			i, c.TxAIndex, c.TxBIndex, c.ConflictType, describeLedgerKey(c.Key), keyB64)
	}
	t.Errorf("%s", strings.TrimRight(b.String(), "\n"))
	return false
}

// AssertSimulationResourcesWithin checks that the resources a Soroban
// simulation reported stay within the given budgets.
//
// For a comprehensive guide on simulation fixtures and footprint assertions,
// see docs/guides/simulation_testing.md.
//
// simResponse accepts either the transaction data a simulation produced — an
// xdr.SorobanTransactionData in any documented Value form — or the SDK's
// protocol.SimulateTransactionResponse (or its pointer), whose
// TransactionDataXDR is read. A simulation that reported an error, or that
// produced no transaction data, is a failure: there is nothing to check, and
// silently passing would make the assertion worthless. Nothing here touches
// the network; the response is data the caller already holds.
//
// maxCPU bounds the CPU instructions the invocation declared. maxMem bounds
// the total ledger bytes it may read from and write to; Soroban's memory
// metering is not part of the transaction data, so ledger I/O is the memory
// budget a submitted transaction actually commits to. Both budgets are checked
// independently, and a failure reports each side and the footprint, so an
// overrun says which resource grew and what the transaction touches.
func AssertSimulationResourcesWithin(t TB, simResponse Value, maxCPU, maxMem uint32) bool {
	t.Helper()

	resources, ok := simulationResources(t, "AssertSimulationResourcesWithin", simResponse)
	if !ok {
		return false
	}

	cpu := uint32(resources.Instructions)
	ledgerBytes := uint64(resources.DiskReadBytes) + uint64(resources.WriteBytes)
	cpuOver := cpu > maxCPU
	memOver := ledgerBytes > uint64(maxMem)

	if !cpuOver && !memOver {
		return true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "AssertSimulationResourcesWithin: simulated resources exceed the budget\n\n")
	fmt.Fprintf(&b, "  CPU instructions  %10d   budget %10d   %s\n",
		cpu, maxCPU, verdict(cpuOver, uint64(cpu)-uint64(maxCPU)))
	fmt.Fprintf(&b, "  ledger bytes      %10d   budget %10d   %s\n",
		ledgerBytes, maxMem, verdict(memOver, ledgerBytes-uint64(maxMem)))
	fmt.Fprintf(&b, "\n  footprint: %d read-only key(s), %d read-write key(s)",
		len(resources.Footprint.ReadOnly), len(resources.Footprint.ReadWrite))
	t.Errorf("%s", b.String())
	return false
}

// verdict reports one budget check, naming the overrun so the reader does not
// have to subtract the two numbers themselves.
func verdict(over bool, excess uint64) string {
	if !over {
		return "ok"
	}
	return fmt.Sprintf("OVER by %d", excess)
}

// simulationResources extracts the declared resources from a simulation
// response or from transaction data directly.
func simulationResources(t TB, assertion string, v Value) (xdr.SorobanResources, bool) {
	switch resp := v.(type) {
	case rpcproto.SimulateTransactionResponse:
		return resourcesFromSimulation(t, assertion, &resp)

	case *rpcproto.SimulateTransactionResponse:
		if resp == nil {
			t.Errorf("%s: simResponse is a nil *protocol.SimulateTransactionResponse", assertion)
			return xdr.SorobanResources{}, false
		}
		return resourcesFromSimulation(t, assertion, resp)

	default:
		return resourcesFromValue(t, assertion, v)
	}
}

// resourcesFromSimulation reads the transaction data out of an RPC response.
func resourcesFromSimulation(t TB, assertion string, resp *rpcproto.SimulateTransactionResponse) (xdr.SorobanResources, bool) {
	if resp.Error != "" {
		t.Errorf("%s: the simulation failed, so there are no resources to check:\n  %s",
			assertion, resp.Error)
		return xdr.SorobanResources{}, false
	}
	if resp.TransactionDataXDR == "" {
		t.Errorf("%s: the simulation response carries no transaction data; "+
			"a successful simulation returns the resources the invocation needs", assertion)
		return xdr.SorobanResources{}, false
	}
	return resourcesFromValue(t, assertion, resp.TransactionDataXDR)
}

// resourcesFromValue coerces transaction data and reads its resources.
func resourcesFromValue(t TB, assertion string, v Value) (xdr.SorobanResources, bool) {
	decoded, ok := resolve(t, assertion, "simResponse", v, "SorobanTransactionData")
	if !ok {
		return xdr.SorobanResources{}, false
	}

	data, ok := decoded.Raw.(*xdr.SorobanTransactionData)
	if !ok {
		t.Errorf("%s: simResponse claims to be a %s but carries no Soroban transaction data",
			assertion, decoded.Type)
		return xdr.SorobanResources{}, false
	}
	return data.Resources, true
}
