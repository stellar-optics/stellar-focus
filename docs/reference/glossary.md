# Glossary of Stellar XDR and Soroban Testing Terminology

This glossary defines the core terms used throughout `stellar-focus` and Stellar/Soroban testing in general. Each term is defined in the context of testing, and cross-references point to the relevant assertion helpers.

## Core Terminology

### XDR
**External Data Representation** (RFC 4506). The binary format used by the Stellar protocol to serialize structured data. All transaction data, ledger entries, and contract values are transmitted as XDR. `stellar-focus` assertions operate on XDR values, decoding them through `stellar-xdr-lens` for human-readable diffs.

### Stroop
1 / 10,000,000 of an XLM. The smallest divisible unit of Stellar's native asset. Amounts in `stellar-focus` assertions are typically shown in both stroops and XLM (e.g., `1000000000 stroops = 100.0000000 XLM`).

### Envelope
The signed outer wrapper containing transaction data and signatures. In `stellar-focus`, the primary type is `TransactionEnvelope`, which wraps a `V1.Transaction` (or legacy `V0.Transaction`). Assertions such as `AssertEnvelopeEqual` compare two envelopes, optionally ignoring volatile fields like sequence numbers, signatures, and time bounds.

### TransactionMeta
Ledger state changes (Created, Modified, Deleted) produced by execution of a transaction. Available in `SorobanTransactionMeta` and used by event, trap, and resource-budget assertions.

### Footprint
The declared set of read-only and read-write ledger keys required by a Soroban contract invocation. Reported in `SorobanTransactionData` and checked via `AssertSorobanResources`.

### ScVal
**Soroban contract value** union type. The union form used by Soroban to represent all possible return values, event data, and storage entries. Types include `ScvU32`, `ScvU64`, `ScvI32`, `ScvI64`, `ScvBytes`, `ScvString`, `ScvMap`, `ScvVec`, and others. See `pkg/focus/spec.go` for typed matching with contract specifications.

### Golden Fixture
A stored, version-controlled reference serialization used for regression testing. Fixtures follow the `focus/v1` format and contain the base64-encoded XDR (source of truth) alongside a decoded lens tree for human review. Managed via `pkg/golden` with `-focus.update` for updates and `focus verify` for drift detection.

### TTL
**Time-To-Live** in ledger entries for contract storage. The number of ledger closes after which an entry expires. Assertions such as `AssertConstructorState` may check TTL values on stored entries.

### Assertion Helpers (by subject)

| Helper | Subject | Key Behavior |
|---|---|---|
| `AssertEnvelopeEqual` | Envelope | Names field paths that differ; supports `IgnoreVolatile()`, `NormalizeAddress()`, `WithLabel()` |
| `AssertOperationTypes` | Operation | Checks the discriminator; typed assertions `AssertPayment`, `AssertCreateAccount`, `AssertChangeTrust` |
| `AssertFee` | Fee | Shows both stroops and XLM; supports `IgnoreSequence()` |
| `AssertTimeBounds` / `AssertLedgerBounds` | Bounds | Compares V1 preconditions or V0 field; zero means "unbounded" |
| `AssertOperationFailed` | Result code | Reports Horizon short code + lens explanation + hint |
| `AssertEventEmitted` | Event | Matches one event; `AssertEventsEqual` matches ordered sequence |
| `AssertContractError` | Error | Reads `ScErrorType` + code from return value + diagnostic stream |
| `AssertSorobanResources` | Resource budget | Checks CPU instructions, ledger bytes read/written; zero = unchecked |
| `AssertResourceFee` | Resource fee | Pins refundable resource fee in stroops and XLM |
| `AssertLedgerKeyEqual` | Ledger key | Compares key built by `pkg/ledgerkey` against one from meta/fixture |
| `AssertXDRRoundTrip` | Round-trip | Re-encodes decoded value, decodes again, diffs the two trees |

### Ignore & Normalize Rules

- `IgnoreVolatile()`: Ignores sequence + signatures + time bounds (default for `AssertEnvelopeEqual`)
- `IgnoreSequence()`: Ignores only the sequence number
- `IgnoreSignatures()`: Ignores signature fields
- `IgnoreSorobanAuth()`: Ignores volatile Soroban authorization credentials (nonce + signature)
- `IgnorePaths("**.Signatures", "operations[*].sourceaccount")`: Ignore specific dotted paths
- `NormalizePath("**.SeqNum", func(v any) any { return v.(int64) / 100 * 100 })`: Map dynamic values to stable names
- `NormalizeAddress(map[string]string)`: Rewrite source accounts, destinations, contract IDs to stable aliases

### Soroban Specific

- **ConstructorState**: Storage entries written by `__constructor` (instance, persistent, temporary)
- **ReturnValue**: `SorobanTransactionMeta.ReturnValue` as `xdr.ScVal`
- **Event**: Emitted contract event with `Topics` and optional `Data`
- **DiagnosticEvent**: Host-recorded event from `fn_call`, `fn_return`, `log`, `error` in the diagnostic stream
- **ContractSpec**: `[]xdr.ScSpecEntry` describing a contract's storage and event fields; when supplied, assertions render field names instead of wire structure
- **Trap**: A contract panic recorded in the result metadata; `AssertContractTrapped` verifies the trap, `AssertContractTrappedWith` provides `TrapEvidence`
- **ScErrorType**: Error category (`SceContract`, `SceWasmVm`, etc.); paired with a numeric code

## Cross-References

Each term links to the assertion helper that most directly addresses it:

- **XDR** → `AssertEnvelopeEqual`, `AssertXDRRoundTrip`
- **Stroop** → `AssertFee`, `AssertPayment`, `AssertCreateAccount`, `AssertChangeTrust`
- **Envelope** → `AssertEnvelopeEqual`, `AssertFeeBump`, `InnerEnvelope`
- **TransactionMeta** → `AssertConstructorState`, `AssertEventEmitted`, `AssertReturnValue`
- **Footprint** → `AssertSorobanResources`, `AssertInstructionsBelow`
- **ScVal** → `AssertReturnValue`, `AssertReturnValueWithSpec`, `AssertEventEmitted` (with `Spec`)
- **Golden Fixture** → `golden.New`, `g.Assert`, `g.AssertAll`, `focus verify`, `focus lint`
- **TTL** → `AssertConstructorState` (entry TTL), ledger entry assertions
- **AssertEnvelopeEqual** → See full assertion reference
- **IgnoreVolatile** → `IgnoreVolatile`, `IgnoreSequence`, `IgnoreSignatures`, `IgnoreSorobanAuth`, `IgnorePaths`, `NormalizePath`, `NormalizeAddress`
- **AssertConstructorState** → `pkg/focus/constructor.go`
- **AssertEventEmitted** → `pkg/focus/soroban.go`
- **AssertContractError** → `pkg/focus/traps.go`
- **AssertSorobanResources** → `pkg/focus/soroban.go`
- **AssertResourceFee** → `pkg/focus/soroban.go`
- **AssertLedgerKeyEqual** → `pkg/ledgerkey`, `pkg/focus/ledger_entries.go`
- **AssertXDRRoundTrip** → `pkg/focus/equal.go`

## Terminology by Package

- `pkg/focus`: Assertions, options (ignore/normalize rules), round-trip, ledger keys, memos, signatures, resource budgets, Soroban events/traps/errors, table-driven golden (`golden.AssertAll`), require mirrors
- `pkg/golden`: Golden file management, schema validation, table-driven assertion (`AssertAll`), require mirrors, lint, refresh
- `pkg/ledgerkey`: Account, trustline, contract data, contract code key builders
- `pkg/fuzz`: Embedded fuzz corpus of real mainnet XDR seed values
- `internal/faketb`: Recording test harness for assertion message testing
- `internal/fakesource`: In-memory capture source for offline testing of capture/verify/simulate
- `cmd/focus`: CLI tool for capture, verify, encode, refresh, lint, golden resolve