package cli

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/fakesource"
	"github.com/stellar-optics/stellar-focus/pkg/capture"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// base64Decode decodes a base64 payload, for tests that need its bytes.
func base64Decode(payload string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(payload)
}

// TestEncodeWritesAFixture is the ordinary case: a payload becomes a
// fixture file.
func TestEncodeWritesAFixture(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")
	dir := t.TempDir()
	path := filepath.Join(dir, "payment.golden.json")

	stdout, _, err := run(t, "encode", xdr, "--type", "TransactionEnvelope", "--out", path)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout, "wrote") {
		t.Errorf("encode printed nothing useful:\n%s", stdout)
	}

	fixture, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if fixture.XDR != xdr {
		t.Errorf("the stored XDR does not match the payload")
	}
	if fixture.Type != "TransactionEnvelope" {
		t.Errorf("Type = %q, want TransactionEnvelope", fixture.Type)
	}
}

// TestEncodeDetectsTheType checks that the type is worked out when it is not
// named.
func TestEncodeDetectsTheType(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")
	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	if _, _, err := run(t, "encode", xdr, "--out", path); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	fixture, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if fixture.Type != "TransactionEnvelope" {
		t.Errorf("Type = %q, want TransactionEnvelope", fixture.Type)
	}
}

// TestEncodeReadsStandardInput checks that a payload can be piped in.
func TestEncodeReadsStandardInput(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")
	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	original := stdin
	stdin = strings.NewReader(xdr + "\n")
	t.Cleanup(func() { stdin = original })

	if _, _, err := run(t, "encode", "--type", "TransactionEnvelope", "--out", path); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	fixture, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if fixture.XDR != xdr {
		t.Error("the stored XDR does not match the payload")
	}
}

// TestEncodeAcceptsHex checks that a hex payload works, because hex turns up
// in logs as often as base64 does.
func TestEncodeAcceptsHex(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")
	raw, err := base64Decode(xdr)
	if err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	if _, _, err := run(t, "encode", hex.EncodeToString(raw),
		"--type", "TransactionEnvelope", "--out", path); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	fixture, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if fixture.XDR != xdr {
		t.Error("the hex payload was not stored as the same XDR")
	}
}

// TestEncodePrintsToStandardOutput checks that a fixture can be written to
// standard output instead of a file.
func TestEncodePrintsToStandardOutput(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")

	stdout, _, err := run(t, "encode", xdr, "--type", "TransactionEnvelope")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var fixture golden.Fixture
	if err := json.Unmarshal([]byte(stdout), &fixture); err != nil {
		t.Fatalf("the output is not a fixture: %v\n%s", err, stdout)
	}
	if fixture.XDR != xdr {
		t.Error("the printed fixture does not carry the payload")
	}
}

// TestEncodeRecordsTheDescription checks that the note is stored in the
// fixture.
func TestEncodeRecordsTheDescription(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")
	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	if _, _, err := run(t, "encode", xdr, "--type", "TransactionEnvelope",
		"--out", path, "--description", "a payment fixture"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	fixture, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if fixture.Description != "a payment fixture" {
		t.Errorf("Description = %q, want %q", fixture.Description, "a payment fixture")
	}
}

// TestEncodeWritesTheFormatItsNameImplies checks that a YAML fixture is
// written when the name says so.
func TestEncodeWritesTheFormatItsNameImplies(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")
	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.yaml")

	if _, _, err := run(t, "encode", xdr, "--type", "TransactionEnvelope", "--out", path); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	fixture, err := golden.Load(path)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if fixture.XDR != xdr {
		t.Error("the stored XDR does not match the payload")
	}
}

// TestEncodeRejectsAnEmptyPayload checks that an empty payload is an error
// rather than an empty fixture.
func TestEncodeRejectsAnEmptyPayload(t *testing.T) {
	_, _, err := run(t, "encode", "   ", "--type", "TransactionEnvelope")
	if err == nil {
		t.Fatal("an empty payload was accepted")
	}
	if !strings.Contains(err.Error(), "no XDR payload") {
		t.Errorf("error does not explain the problem: %v", err)
	}
}

// TestEncodeRejectsAGarbagePayload checks that a payload that is not XDR at
// all is reported rather than written.
func TestEncodeRejectsAGarbagePayload(t *testing.T) {
	_, _, err := run(t, "encode", "not a payload!", "--type", "TransactionEnvelope")
	if err == nil {
		t.Fatal("a payload that is not XDR was accepted")
	}
	if !strings.Contains(err.Error(), "neither base64 nor hex") {
		t.Errorf("error does not explain the problem: %v", err)
	}
}

// TestEncodeRejectsAnUnknownType checks that a type that does not exist is
// reported.
func TestEncodeRejectsAnUnknownType(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")

	_, _, err := run(t, "encode", xdr, "--type", "NotAType", "--out", filepath.Join(t.TempDir(), "x.json"))
	if err == nil {
		t.Fatal("an unknown type was accepted")
	}
}

// TestEncodeDoesNotTouchTheNetwork checks that encoding is offline: it turns
// a payload into a file and never asks an endpoint anything.
func TestEncodeDoesNotTouchTheNetwork(t *testing.T) {
	src := fakesource.New(capture.NetworkTestnet, "https://fake.example")
	withFake(t, src)

	xdr := fixtureXDR(t, "tx_failed.env.txt")
	path := filepath.Join(t.TempDir(), "env.golden.json")

	if _, _, err := run(t, "encode", xdr, "--type", "TransactionEnvelope", "--out", path); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if src.Calls != 0 {
		t.Errorf("the network was contacted %d time(s)", src.Calls)
	}
}

// TestEncodeRejectsTooManyArguments checks that the command says what it
// accepts.
func TestEncodeRejectsTooManyArguments(t *testing.T) {
	if _, _, err := run(t, "encode", "one", "two"); err == nil {
		t.Fatal("two payloads were accepted")
	}
}

// TestEncodeWritesIntoAMissingDirectory checks that the directory of the
// output file is created, rather than the command failing over it.
func TestEncodeWritesIntoAMissingDirectory(t *testing.T) {
	xdr := fixtureXDR(t, "tx_failed.env.txt")
	path := filepath.Join(t.TempDir(), "nested", "deeper", "env.golden.json")

	if _, _, err := run(t, "encode", xdr, "--type", "TransactionEnvelope", "--out", path); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the fixture was not written: %v", err)
	}
}
