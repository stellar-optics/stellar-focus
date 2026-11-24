# Examples

This directory contains runnable examples demonstrating Stellar and Soroban workflows.

## Lifecycle Example (`lifecycle.ts`)

A complete TypeScript example showing the full lifecycle of a Stellar transaction flow on testnet:

1. **FUND** - Create a claimable balance (funds locked with claim predicates)
2. **CLAIM** - Claim the balance by the designated claimant
3. **ATTEST** - Invoke a Soroban contract to attest/record the claim
4. **SETTLE** - Finalize/settle via contract

### Prerequisites

- Node.js 18+
- Testnet accounts with XLM for transaction fees
  - Get testnet XLM: https://laboratory.stellar.org/#account-creator?network=testnet

### Setup

```bash
cd examples
npm install
```

### Configuration

Set the following environment variables:

```bash
# Required: Funder and claimant secret keys (testnet)
export FUNDER_SECRET="SXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
export CLAIMANT_SECRET="SXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"

# Optional: Attestor for contract interactions
export ATTESTOR_SECRET="SXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
export ATTESTATION_CONTRACT_ID="CXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
```

Generate testnet keys with:
```bash
# Using stellar-cli
stellar keys generate --network testnet

# Or using the SDK programmatically
node -e "const {Keypair} = require('@stellar/stellar-sdk'); const k = Keypair.random(); console.log('Public:', k.publicKey()); console.log('Secret:', k.secret())"
```

### Run

```bash
# Type-check only (runs in CI)
npm run typecheck

# Build
npm run build

# Run directly with ts-node
npm run dev

# Or run compiled JS
npm run build && npm run start
```

### Expected Output

```
╔══════════════════════════════════════════════════════════════╗
║     Stellar Full Lifecycle Example (Testnet)                 ║
║     Fund → Claim → Attest → Settle                           ║
╚══════════════════════════════════════════════════════════════╝

📋 Configuration:
   Funder:    GABC...
   Claimant:  GDEF...
   Attestor:  GHIJ...
   RPC:       https://soroban-testnet.stellar.org
   Contract:  CABC...

🔹 STEP 1: FUND
   Creating claimable balance...
   ✅ FUND confirmed: 1a2b3c...
   Balance ID: 1a2b3c-0
   Amount: 10 XLM
   Claimants: GDEF... (immediate), GABC... (after 24h)

🔹 STEP 2: CLAIM
   Claiming the balance...
   ✅ CLAIM confirmed: 4d5e6f...
   Claimed by: GDEF...
   Balance ID: 1a2b3c-0

🔹 STEP 3: ATTEST
   Recording claim on Soroban contract...
   ✅ ATTEST confirmed: 7g8h9i...
   Contract: CABC...
   Attested claim: 4d5e6f...

🔹 STEP 4: SETTLE
   Settling via contract...
   ✅ SETTLE confirmed: 0j1k2l...
   Contract: CABC...
   Reference: 7g8h9i...

╔══════════════════════════════════════════════════════════════╗
║                    LIFECYCLE COMPLETE                         ║
╚══════════════════════════════════════════════════════════════╝

📝 Transaction Hashes (save for verification):
   FUND:   1a2b3c...
   CLAIM:  4d5e6f...
   ATTEST: 7g8h9i...
   SETTLE: 0j1k2l...

🔗 View on Stellar Expert:
   fund: https://stellar.expert/explorer/testnet/tx/1a2b3c...
   claim: https://stellar.expert/explorer/testnet/tx/4d5e6f...
   attest: https://stellar.expert/explorer/testnet/tx/7g8h9i...
   settle: https://stellar.expert/explorer/testnet/tx/0j1k2l...
```

### CI Integration

The example includes a type-check step that runs in CI:

```yaml
# .github/workflows/ci.yml
- name: Type-check TypeScript examples
  run: |
    cd examples
    npm ci
    npm run typecheck
```

### Contract Interface (for Attest/Settle)

If you want to deploy your own attestation contract, here's the Rust interface:

```rust
// soroban contract
#[contract]
pub struct AttestationContract;

#[contractimpl]
impl AttestationContract {
    /// Record that a claim occurred
    pub fn attest(env: Env, claimer: Address, claim_tx_hash: BytesN<32>) {
        claimer.require_auth();
        // Store attestation: claim_tx_hash -> claimer
        env.storage().instance().set(&claim_tx_hash, &claimer);
    }

    /// Finalize settlement
    pub fn settle(env: Env, attestation_id: BytesN<32>) {
        // Verify attestation exists
        let claimer: Address = env.storage().instance().get(&attestation_id).unwrap();
        claimer.require_auth();
        // Perform settlement logic (mint tokens, release escrow, etc.)
    }
}
```

Deploy to testnet and set `ATTESTATION_CONTRACT_ID` to the deployed contract address.