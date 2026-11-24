// Package golden_test benchmarks recording and comparing fixtures, and proves
// the bytes a fixture is written with do not depend on the platform running
// the test.
package golden_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// BenchmarkGolden_Assert measures the comparison path end to end: loading and
// unmarshalling the fixture, decoding the actual value and comparing them.
func BenchmarkGolden_Assert(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "envelope.golden.json")
	xdr := envelopeXDR(b)
	saveFixture(b, path, xdr)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g := golden.New(b, golden.WithDir(dir), golden.WithType("TransactionEnvelope"))
		g.Assert("envelope", xdr)
	}
}

// BenchmarkGolden_Load measures fixture loading and JSON unmarshalling without
// the comparison, so the file and parser overhead is visible on its own.
func BenchmarkGolden_Load(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "envelope.golden.json")
	saveFixture(b, path, envelopeXDR(b))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := golden.Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGolden_Save measures serializing and writing a fixture.
func BenchmarkGolden_Save(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "envelope.golden.json")
	fixture, err := golden.Build(envelopeXDR(b), "TransactionEnvelope", "", nil)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := golden.Save(path, fixture); err != nil {
			b.Fatal(err)
		}
	}
}

// TestSavedFixturesAreByteIdentical checks the property the multi-platform CI
// job depends on: a fixture written here is the same bytes a fixture written
// on any other platform, with LF line endings and a single trailing newline.
// Without it a Windows checkout would produce a diff that is only a line
// ending, which is exactly the noise the normalization exists to prevent.
func TestSavedFixturesAreByteIdentical(t *testing.T) {
	dir := t.TempDir()
	xdr := envelopeXDR(t)
	first := filepath.Join(dir, "first.golden.json")
	second := filepath.Join(dir, "second.golden.json")
	saveFixture(t, first, xdr)
	saveFixture(t, second, xdr)

	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("two recordings of the same value produced different bytes")
	}
	if bytes.Contains(firstBytes, []byte("\r")) {
		t.Fatal("the fixture contains a carriage return, so it would differ on Windows")
	}
	if !bytes.HasSuffix(firstBytes, []byte("\n")) || bytes.HasSuffix(firstBytes, []byte("\n\n")) {
		t.Fatal("the fixture does not end with exactly one newline")
	}

	loaded, err := golden.Load(first)
	if err != nil {
		t.Fatalf("reading back the fixture: %v", err)
	}
	roundTripped := filepath.Join(dir, "round-trip.golden.json")
	if err := golden.Save(roundTripped, loaded); err != nil {
		t.Fatalf("saving the loaded fixture: %v", err)
	}
	roundTrippedBytes, err := os.ReadFile(roundTripped)
	if err != nil {
		t.Fatalf("reading the round-tripped fixture: %v", err)
	}
	if !bytes.Equal(firstBytes, roundTrippedBytes) {
		t.Fatal("loading and saving a fixture changed its bytes")
	}
}

// saveFixture records xdr as a fixture at path.
func saveFixture(tb testing.TB, path, xdr string) {
	tb.Helper()
	fixture, err := golden.Build(xdr, "TransactionEnvelope", "", nil)
	if err != nil {
		tb.Fatalf("building the fixture: %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		tb.Fatalf("saving the fixture: %v", err)
	}
}

// envelopeXDR returns the base64 transaction envelope the repository's own
// testdata carries, so the benchmark measures a real payload.
func envelopeXDR(tb testing.TB) string {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("..", "fuzz", "testdata", "tx_failed.env.txt"))
	if err != nil {
		tb.Fatalf("reading the test envelope: %v", err)
	}
	return strings.TrimSpace(string(data))
}
