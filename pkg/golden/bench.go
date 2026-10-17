package golden

import (
	"os"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// benchmarkTB is a tiny no-op implementation of focus.TB for benches that just
// need to pass a TB through the assertion helpers without failing the run.
type benchmarkTB struct{}

func (benchmarkTB) Helper()               {}
func (benchmarkTB) Errorf(string, ...any) {}
func (benchmarkTB) Fatalf(string, ...any) {}
func (benchmarkTB) Name() string          { return "golden-benchmark" }

// BenchmarkFixture runs a one-shot assertion comparison repeatedly so changes in
// the comparison path show up in the benchmark output.
func BenchmarkFixture(b *testing.B, want, got focus.Value, typeName string, opts ...focus.Option) {
	b.Helper()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		focus.AssertEqualAs(benchmarkTB{}, typeName, want, got, opts...)
	}
}

// BenchmarkGolden measures the structural comparison used by Golden.Assert.
func BenchmarkGolden(b *testing.B) {
	data, err := os.ReadFile("../../testdata/tx_failed.env.txt")
	if err != nil {
		b.Fatal(err)
	}
	xdr := strings.TrimSpace(string(data))
	BenchmarkFixture(b, xdr, xdr, "TransactionEnvelope")
}
