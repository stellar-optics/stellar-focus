# Multi-transaction capture bundles

A capture bundle groups the fixtures returned for several transactions in
their requested order. The current library API is in
[`pkg/capture/bundle.go`](../../pkg/capture/bundle.go). It can capture and
verify members through a `capture.Source`, but the current `focus` CLI does
not have a `focus capture bundle` subcommand.

## Manifest format

`focus/bundle/v1` is the `Bundle.Format` value. A bundle manifest records a
description, source network and endpoint, and an ordered `members` array.
Each transaction produces one member per captured record (normally envelope,
result, and meta). Member fields are `name`, `type`, `txHash`, optional
`ledger`, and RFC3339 `capturedAt`.

```json
{
  "format": "focus/bundle/v1",
  "description": "account setup followed by the first payment",
  "network": "testnet",
  "endpoint": "https://soroban-testnet.stellar.org",
  "members": [
    {
      "name": "setup_tx01_envelope.golden.json",
      "type": "TransactionEnvelope",
      "txHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "ledger": 12345678,
      "capturedAt": "2026-11-25T09:00:00Z"
    },
    {
      "name": "setup_tx01_result.golden.json",
      "type": "TransactionResult",
      "txHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "ledger": 12345678,
      "capturedAt": "2026-11-25T09:00:00Z"
    },
    {
      "name": "setup_tx02_envelope.golden.json",
      "type": "TransactionEnvelope",
      "txHash": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "ledger": 12345679,
      "capturedAt": "2026-11-25T09:00:00Z"
    }
  ]
}
```

The manifest does not currently store a network passphrase or a dependency
graph. Its member order preserves the order of requested transaction hashes
and the record order returned by the source; it does not claim or validate
causal dependencies. Passphrase provenance, when available, belongs to the
individual golden fixtures, not this `Bundle` schema.

## Capture with the library

`GatherBundle` accepts transaction hashes in the order to record them. It
fetches every requested transaction before writing fixture files, and writes
the manifest last:

```go
bundle, paths, err := capture.GatherBundle(
    ctx,
    src, // a configured capture.Source
    capture.BundleRequest{
        Hashes:      []string{setupHash, paymentHash},
        Description: "setup followed by payment",
        Prefix:      "account-flow",
    },
    "testdata/account-flow",
    time.Now(),
)
if err != nil {
    return err
}
_ = bundle
_ = paths
```

A fetch failure happens before any files are written. A later filesystem or
fixture-write error can still leave files already written, so this is not a
transactional filesystem operation. Reusing an existing prefix is rejected
unless `BundleRequest.Force` is true; a distinct prefix keeps captures
alongside one another.

The existing CLI supports `focus capture tx --batch hashes.txt` for batch
capture, but a failed transaction is reported while the batch carries on.
That command is not an all-or-nothing substitute for `GatherBundle`.

## Verify members in CI

`LoadBundle` checks that the manifest is readable and has the supported
`format`; it does not validate a dependency graph (there is none in this
schema). `VerifyBundle` loads each member fixture, re-fetches its recorded
transaction record, and returns one `VerifyResult` per member in manifest
order:

```go
bundle, err := capture.LoadBundle("testdata/account-flow/account-flow.bundle.json")
if err != nil {
    return err
}
results := capture.VerifyBundle(ctx, src, "testdata/account-flow", bundle)
for _, result := range results {
    if result.Err != nil {
        return fmt.Errorf("%s: could not verify: %w", result.Path, result.Err)
    }
    if !result.Match {
        return fmt.Errorf("%s: fixture drifted: %v", result.Path, result.Diff)
    }
}
```

Use the same source network and passphrase as the captured fixtures. The
standard `focus verify` command checks fixture paths against their provenance,
but currently does not load a bundle manifest or validate that its member set
is complete. A CI job that needs bundle-level checks should load the manifest
and call `VerifyBundle` (or otherwise check its member list) in addition to
verifying individual fixtures.
