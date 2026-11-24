/**
 * Stellar Full Lifecycle Example
 *
 * This example demonstrates the complete lifecycle of a Stellar transaction flow:
 * 1. FUND   - Create a claimable balance (funds locked with claim predicates)
 * 2. CLAIM  - Claim the balance by the designated claimant
 * 3. ATTEST - Invoke a Soroban contract to attest/record the claim
 * 4. SETTLE - Finalize/settle via contract (e.g., release escrow, mint tokens)
 *
 * Run: npm run dev (after npm install)
 * Requires: Testnet accounts with XLM for fees
 */

import {
  Keypair,
  Networks,
  TransactionBuilder,
  Operation,
  Asset,
  Claimant,
  SorobanRpc,
  Contract,
  Address,
  nativeToScVal,
  xdr,
  BASE_FEE,
  Transaction,
} from "@stellar/stellar-sdk";

/* ============================================================================
 * CONFIGURATION - Replace with your testnet keys
 * ============================================================================ */

// Generate these with: npm run generate:keys
// Or use stellar-cli: stellar keys generate --network testnet
const FUNDER_SECRET = process.env["FUNDER_SECRET"] || "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWH";
const CLAIMANT_SECRET = process.env["CLAIMANT_SECRET"] || "SBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBWH";
const ATTESTOR_SECRET = process.env["ATTESTOR_SECRET"] || "SCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCWH";
const ATTESTATION_CONTRACT_ID = process.env["ATTESTATION_CONTRACT_ID"] || "";

// Testnet RPC endpoint
const RPC_URL = "https://soroban-testnet.stellar.org";
const NETWORK_PASSPHRASE = Networks.TESTNET;

/* ============================================================================
 * UTILITIES
 * ============================================================================ */

const funder = Keypair.fromSecret(FUNDER_SECRET);
const claimant = Keypair.fromSecret(CLAIMANT_SECRET);
const attestor = ATTESTOR_SECRET ? Keypair.fromSecret(ATTESTOR_SECRET) : null;

const server = new SorobanRpc.Server(RPC_URL, { allowHttp: true });

async function getAccount(accountId: string) {
  return server.getAccount(accountId);
}

async function submitTransaction(tx: Transaction, signerKeys: Keypair[]): Promise<string> {
  const prepared = await server.prepareTransaction(tx);
  prepared.sign(...signerKeys);
  const result = await server.sendTransaction(prepared);
  if (result.status === "ERROR") {
    throw new Error(`Transaction failed: ${result.errorResult}`);
  }
  return result.hash;
}

async function waitForTx(hash: string): Promise<void> {
  let attempts = 0;
  while (attempts < 30) {
    const status = await server.getTransaction(hash);
    if (status.status === "SUCCESS") return;
    if (status.status === "FAILED") throw new Error(`Transaction failed: ${status.resultXdr}`);
    await new Promise(r => setTimeout(r, 2000));
    attempts++;
  }
  throw new Error("Transaction confirmation timeout");
}

function logStep(step: string, detail: string) {
  console.log(`\n🔹 ${step}`);
  console.log(`   ${detail}`);
}

function logSuccess(step: string, hash: string) {
  console.log(`   ✅ ${step} confirmed: ${hash}`);
}

/* ============================================================================
 * STEP 1: FUND - Create a Claimable Balance
 * ============================================================================

 * A claimable balance locks XLM with predicates that determine who can claim
 * and when. This is the "funding" step - funds are committed but not yet
 * accessible to the recipient.
 */
async function step1_fund(): Promise<{ balanceId: string; txHash: string }> {
  logStep("STEP 1: FUND", "Creating claimable balance...");

  const funderAccount = await getAccount(funder.publicKey());
  const amount = "10"; // 10 XLM

  // Define claimants with predicates
  // Claimant 1: The designated recipient can claim anytime
  const claimantPredicate = Claimant.predicateUnconditional();
  const claimant1 = new Claimant(claimant.publicKey(), claimantPredicate);

  // Claimant 2: Funder can reclaim after 24 hours (clawback)
  const reclaimTime = Math.floor(Date.now() / 1000) + 86400; // 24 hours from now
  const reclaimPredicate = Claimant.predicateBeforeAbsoluteTime(reclaimTime.toString());
  const claimant2 = new Claimant(funder.publicKey(), reclaimPredicate);

  const tx = new TransactionBuilder(funderAccount, {
    fee: BASE_FEE,
    networkPassphrase: NETWORK_PASSPHRASE,
  })
    .addOperation(
      Operation.createClaimableBalance({
        asset: Asset.native(),
        amount: amount,
        claimants: [claimant1, claimant2],
      })
    )
    .setTimeout(300)
    .build();

  const hash = await submitTransaction(tx, [funder]);
  await waitForTx(hash);

  // The balance ID can be derived from the transaction hash and operation index
  // In practice, parse from transaction meta. Here we use a simplified form.
  const balanceId = `${hash}-0`;

  logSuccess("FUND", hash);
  console.log(`   Balance ID: ${balanceId}`);
  console.log(`   Amount: ${amount} XLM`);
  console.log(`   Claimants: ${claimant.publicKey()} (immediate), ${funder.publicKey()} (after 24h)`);

  return { balanceId, txHash: hash };
}

/* ============================================================================
 * STEP 2: CLAIM - Claim the Balance
 * ============================================================================

 * The designated claimant claims the balance. This transfers the XLM from
 * the claimable balance entry to the claimant's account.
 */
async function step2_claim(balanceId: string): Promise<string> {
  logStep("STEP 2: CLAIM", "Claiming the balance...");

  const claimantAccount = await getAccount(claimant.publicKey());

  // The balance ID format is "txHash-opIndex"
  // For the actual ClaimClaimableBalance operation, we need the raw balance ID
  // In practice, this would be extracted from the CreateClaimableBalance result
  // Here we use a deterministic ID derived from the transaction
  const txHashPart = balanceId.split("-")[0];
  if (!txHashPart) {
    throw new Error(`Invalid balance ID format: ${balanceId}`);
  }
  const balanceIdBuffer = Buffer.from(txHashPart, "hex").subarray(0, 32);
  const claimableBalanceId = xdr.ClaimableBalanceId.claimableBalanceIdTypeV0(balanceIdBuffer);

  const tx = new TransactionBuilder(claimantAccount, {
    fee: BASE_FEE,
    networkPassphrase: NETWORK_PASSPHRASE,
  })
    .addOperation(
      Operation.claimClaimableBalance({
        balanceId: claimableBalanceId.toXDR("hex"),
      })
    )
    .setTimeout(300)
    .build();

  const hash = await submitTransaction(tx, [claimant]);
  await waitForTx(hash);

  logSuccess("CLAIM", hash);
  console.log(`   Claimed by: ${claimant.publicKey()}`);
  console.log(`   Balance ID: ${balanceId}`);

  return hash;
}

/* ============================================================================
 * STEP 3: ATTEST - Record the Claim on a Soroban Contract
 * ============================================================================

 * An attestation contract can record that a claim occurred, providing
 * on-chain proof. This is useful for compliance, auditing, or triggering
 * downstream logic.
 */
async function step3_attest(claimTxHash: string): Promise<string | null> {
  if (!ATTESTATION_CONTRACT_ID) {
    logStep("STEP 3: ATTEST", "Skipped - no contract ID configured");
    console.log("   Set ATTESTATION_CONTRACT_ID to enable this step");
    return null;
  }

  logStep("STEP 3: ATTEST", "Recording claim on Soroban contract...");

  const attestorAccount = await getAccount(attestor!.publicKey());

  const contract = new Contract(ATTESTATION_CONTRACT_ID);

  // Build the attest call: attest(claimer: Address, claim_tx_hash: BytesN<32>)
  const claimTxHashBytes = Buffer.from(claimTxHash, "hex");
  const tx = new TransactionBuilder(attestorAccount, {
    fee: "1000000", // Higher fee for contract calls
    networkPassphrase: NETWORK_PASSPHRASE,
  })
    .addOperation(
      contract.call(
        "attest",
        nativeToScVal(claimant.publicKey(), { type: "address" }),
        nativeToScVal(claimTxHashBytes, { type: "bytes" })
      )
    )
    .setTimeout(300)
    .build();

  // Simulate first to get auth and resource costs
  const simulation = await server.simulateTransaction(tx);
  if (SorobanRpc.Api.isSimulationError(simulation)) {
    throw new Error(`Simulation failed: ${simulation.error}`);
  }

  const prepared = SorobanRpc.assembleTransaction(tx, simulation).build();
  prepared.sign(attestor!);

  const result = await server.sendTransaction(prepared);
  if (result.status === "ERROR") {
    throw new Error(`Attestation failed: ${result.errorResult}`);
  }

  await waitForTx(result.hash);

  logSuccess("ATTEST", result.hash);
  console.log(`   Contract: ${ATTESTATION_CONTRACT_ID}`);
  console.log(`   Attested claim: ${claimTxHash}`);

  return result.hash;
}

/* ============================================================================
 * STEP 4: SETTLE - Finalize via Contract
 * ============================================================================

 * Settlement could mean releasing escrowed funds, minting tokens, updating
 * state, or any final action that completes the business logic.
 */
async function step4_settle(attestTxHash: string | null): Promise<string | null> {
  if (!ATTESTATION_CONTRACT_ID) {
    logStep("STEP 4: SETTLE", "Skipped - no contract ID configured");
    return null;
  }

  logStep("STEP 4: SETTLE", "Settling via contract...");

  const attestorAccount = await getAccount(attestor!.publicKey());

  const contract = new Contract(ATTESTATION_CONTRACT_ID);

  // Build the settle call: settle(attestation_id: BytesN<32>)
  // In practice, this might reference the attestation or the original claim
  const referenceHash = attestTxHash || claimant.publicKey(); // fallback
  const referenceBytes = Buffer.from(referenceHash, "hex");

  const tx = new TransactionBuilder(attestorAccount, {
    fee: "1000000",
    networkPassphrase: NETWORK_PASSPHRASE,
  })
    .addOperation(
      contract.call(
        "settle",
        nativeToScVal(referenceBytes, { type: "bytes" })
      )
    )
    .setTimeout(300)
    .build();

  const simulation = await server.simulateTransaction(tx);
  if (SorobanRpc.Api.isSimulationError(simulation)) {
    throw new Error(`Simulation failed: ${simulation.error}`);
  }

  const prepared = SorobanRpc.assembleTransaction(tx, simulation).build();
  prepared.sign(attestor!);

  const result = await server.sendTransaction(prepared);
  if (result.status === "ERROR") {
    throw new Error(`Settlement failed: ${result.errorResult}`);
  }

  await waitForTx(result.hash);

  logSuccess("SETTLE", result.hash);
  console.log(`   Contract: ${ATTESTATION_CONTRACT_ID}`);
  console.log(`   Reference: ${referenceHash}`);

  return result.hash;
}

/* ============================================================================
 * MAIN - Run Full Lifecycle
 * ============================================================================ */

async function main() {
  console.log("╔══════════════════════════════════════════════════════════════╗");
  console.log("║     Stellar Full Lifecycle Example (Testnet)                 ║");
  console.log("║     Fund → Claim → Attest → Settle                           ║");
  console.log("╚══════════════════════════════════════════════════════════════╝");

  console.log(`\n📋 Configuration:`);
  console.log(`   Funder:    ${funder.publicKey()}`);
  console.log(`   Claimant:  ${claimant.publicKey()}`);
  console.log(`   Attestor:  ${attestor?.publicKey() || "not configured"}`);
  console.log(`   RPC:       ${RPC_URL}`);
  console.log(`   Contract:  ${ATTESTATION_CONTRACT_ID || "not configured"}`);

  // Verify accounts exist and have XLM
  try {
    await getAccount(funder.publicKey());
    await getAccount(claimant.publicKey());
    if (attestor) await getAccount(attestor.publicKey());
  } catch (e) {
    console.error("\n❌ Account not found or network error. Ensure accounts exist on testnet.");
    console.error("   Get testnet XLM: https://laboratory.stellar.org/#account-creator?network=testnet");
    process.exit(1);
  }

  const hashes: Record<string, string> = {};

  try {
    // Step 1: Fund
    const fundResult = await step1_fund();
    hashes["fund"] = fundResult.txHash;

    // Step 2: Claim
    const claimHash = await step2_claim(fundResult.balanceId);
    hashes["claim"] = claimHash;

    // Step 3: Attest
    const attestHash = await step3_attest(claimHash);
    if (attestHash) hashes["attest"] = attestHash;

    // Step 4: Settle
    const settleHash = await step4_settle(attestHash);
    if (settleHash) hashes["settle"] = settleHash;

    // Summary
    console.log("\n╔══════════════════════════════════════════════════════════════╗");
    console.log("║                    LIFECYCLE COMPLETE                         ║");
    console.log("╚══════════════════════════════════════════════════════════════╝");
    console.log("\n📝 Transaction Hashes (save for verification):");
    Object.entries(hashes).forEach(([step, hash]) => {
      console.log(`   ${step.toUpperCase()}: ${hash}`);
    });

    console.log("\n🔗 View on Stellar Expert:");
    Object.entries(hashes).forEach(([step, hash]) => {
      console.log(`   ${step}: https://stellar.expert/explorer/testnet/tx/${hash}`);
    });

  } catch (error) {
    console.error("\n❌ Lifecycle failed:", error);
    process.exit(1);
  }
}

main().catch(console.error);