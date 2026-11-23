# Capture, Anonymize and Replay

How to turn a real transaction from the Stellar network into a reproducible,
offline test fixture — and then make the test that reads it.

The pipeline this guide covers:

```
focus capture tx <hash>          capture envelope, result and meta
focus capture tx --batch …       capture many at once
focus capture tx … --sanitize   mask memos, data values and secret keys
focus capture tx … --mask-addresses                 mask real accounts → mock accounts
capture.GenerateReplayTest …      turn a capture into a _test.go
go test ./...                      run it, offline, against the fixture
```

The motivation is narrow and practical: a production failure is the best test
case there is — someone really paid someone, on mainnet, and it broke. But the
transaction that broke carries live data that does not belong in a repository:
real accounts, real memos, real payloads. Capture stores the XDR; this guide
shows how to sanitize it, and how to generate the replay test that keeps it
honest.

---

## Step 1: Capture a transaction by hash

```sh
focus capture tx 2b2e276a8f99a2927580eca2c8d665c0adaef9526476f5549269798380a66648 \
  --out testdata/
```

The command fetches the transaction and writes its envelope, result and meta as
separate fixture files, each stamped with the network, ledger and capture time so
a later run can re-check them:

```console
$ focus capture tx 2b2e276a… --out testdata/
wrote testdata/2b2e276a_envelope.golden.json
wrote testdata/2b2e276a_result.golden.json
wrote testdata/2b2e276a_meta.golden.json
```

`--prefix failed_payment` names the fixtures instead of using the hash:

```sh
focus capture tx 2b2e276a… --out testdata/ --prefix failed_payment
```

### Source and reach

- `--source rpc` (default) or `horizon` — Horizon reaches further back, so it
  can capture transactions Soroban RPC no longer keeps.
- `--archive-url <url>` with `--ledger <n>` for transactions older than the
  source's history; an archive holds envelopes and results but not meta, so the
  ledger the transaction closed in is what `--ledger` names.
- `--network mainnet` (or `testnet`, `futurenet`, …) and `--passphrase` for
  networks focus cannot name.

## Step 2: Capture in bulk

Twenty transactions one command at a time is twenty processes and twenty
connections. `--batch` reads hashes from a file instead:

```sh
$ cat hashes.txt
# the transactions of the fixture suite
2b2e276a8f99a2927580eca2c8d665c0adaef9526476f5549269798380a66648
failed_payment 3c3f387b9f0ab3a38690fdb3d9d776d1dbebf063758a66583a6799491b77759

$ focus capture tx --batch hashes.txt --out testdata/ --concurrency 4
[1/2] captured tx 2b2e276a -> testdata/2b2e276a_envelope.golden.json
[2/2] captured tx 3c3f387b -> testdata/failed_payment_3c3f387b_envelope.golden.json

2 transaction(s): 2 captured, 0 not found, 0 failed
```

A line is a hash, optionally preceded by a prefix for the fixtures it produces;
blank lines and `#` comments are ignored, and `-` reads hashes from standard
input.

## Step 3: Sanitize before committing

A capture from a private or staging network can carry data that must not be
committed. Three families of masking exist:

```sh
# all three, at once
focus capture tx <hash> --out testdata/ --sanitize

# each on its own
focus capture tx <hash> --out testdata/ --mask-memo        # text memos -> [REDACTED_MEMO]
focus capture tx <hash> --out testdata/ --sanitize-memos # alias of --mask-memo
focus capture tx <hash> --out testdata/ --scrub-data     # data entry values -> [REDACTED_DATA]
focus capture tx <hash> --out testdata/ --scrub-keys    # secret-shaped strings -> [REDACTED_SECRET_KEY]

# real accounts -> deterministic mock accounts (the anonymize step)
focus capture tx <hash> --out testdata/ --mask-addresses
```

`--mask-addresses` replaces public account addresses with deterministic mock
accounts — derived from a seed mixed with the original address, so the same real
account always maps to the same mock and equal addresses map to equal mocks
across records. It is the library-level `capture.Anonymize` /
`capture.AnonymizeWithSeed` behind the flag.

`--sanitize` is all three memo/data/key masks; the individual flags pick some.
The decoded section of the fixture is rendered from the masked XDR, so it is
clean too.

A sanitized fixture no longer matches the chain, so `focus verify` skips it
rather than reporting drift.

## Step 4: Generate a replay test from the capture

`pkg/capture/codegen.go` turns a capture into a runnable Go test:

```go
import (
	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

fixture, err := golden.Load("testdata/failed_payment.golden.json")
if err != nil {
	panic(err)
}

src, err := capture.GenerateReplayTest(&capture.CaptureFixture{
	Name:        "failed_payment",
	Fixture:     fixture,
	FixturePath: "testdata/failed_payment.golden.json",
})
// src is gofmt-formatted, deterministic, timestamp-free Go test source.
```

The two entry points:

- `capture.GenerateReplayTest(&capture.CaptureFixture{...})` renders a
  table-driven replay test that loads the fixture and asserts the captured XDR
  against it, plus the operation types that were captured;
- `capture.GenerateTest(name string, f *golden.Fixture, opts capture.CodegenOptions) ([]byte, error)`
  renders a single-test variant, and `capture.GenerateTestFile(fixturePath, outPath string, opts capture.CodegenOptions) error`
  writes the generated source to a `_test.go` file.

Options:

```go
opts := capture.CodegenOptions{
	FixturePath: "testdata/failed_payment.golden.json", // how the test loads its fixture
	Package:      "fixtures_test",                     // generated package clause; "" -> "fixtures_test"
	FuncName:     "TestReplayFailedPayment",         // overrides the derived name
}
```

Generated tests are deterministic — the same fixture and options always produce
byte-identical output, with no timestamps and no map iteration — and gofmt
formats the source, so a regenerated test shows up in review as no diff at all.

### What the generated test looks like

```go
// Code generated by focus capture; DO NOT EDIT.

// Replay of the TransactionEnvelope captured as "failed_payment".

package fixtures_test

import (
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// TestReplay_FailedPayment replays the captured transaction and checks that the stored fixture still matches it.
//
// A failure here means one of two things: the fixture file was edited
// without re-capturing, or a decoder changed underneath it. Either way
// the diff names the field.
func TestReplay_FailedPayment(t *testing.T) {
	const capturedXDR = "AAAAAgAAA..."

	fixture, err := golden.Load("testdata/failed_payment.golden.json")
	if err != nil {
		t.Fatalf("loading fixture: %v", err)
	}

	wantOps := []string{"payment"}

	tests := []struct {
		name string
		xdr  string
	}{
		{name: "captured", xdr: capturedXDR},
		{name: "fixture", xdr: fixture.XDR},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			focus.AssertEnvelopeEqual(t, capturedXDR, tt.xdr)
			focus.AssertOperationTypes(t, tt.xdr, wantOps...)
		})
	}
}
```

## The full workflow

Putting it together, from a hash on a dashboard to a red-green offline test:

```sh
# 1. Capture the production failure you are chasing.
focus capture tx <hash> --out testdata/ --prefix failed_payment

# 2. If the data must not be committed, mask before writing.
focus capture tx <hash> --out testdata/ --prefix failed_payment --sanitize

# 3. Check it still holds together against the source you captured from.
focus verify testdata/

# 4. Generate the replay test and commit it.
#      capture.GenerateReplayTest(&capture.CaptureFixture{...})
#      capture.GenerateTestFile("testdata/failed_payment.golden.json", "pkg/yourexamples/replay_failed_payment_test.go", opts)

# 5. Run the suite offline.
go test ./...
```

`focus verify` re-checks the stored fixtures against the network: `0` when
everything checked still matches, `2` on drift (with the structural diff, field
by field), and `1` when a fixture could not be checked at all. A sanitized
fixture is skipped.

## Next steps

- What to assert about the captured data: the [Assertions
  Cookbook](assertions_cookbook.md).
- How ledger keys and state assertions fit in: the [Ledger Keys & State
  Snapshot Inspection](ledger_keys_and_state.md) guide.
- How volatile fields stay out of the way: the [Ignore Rules
  Reference](ignore_rules_reference.md).
