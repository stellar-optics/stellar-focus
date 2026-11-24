// Package focus provides XDR-aware assertions for Go tests.
package focus_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// BenchmarkAssertEnvelopeEqual_Identical benchmarks AssertEnvelopeEqual with
// identical envelopes (best case - no diff needed).
func BenchmarkAssertEnvelopeEqual_Identical(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := want

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got)
	}
}

// BenchmarkAssertEnvelopeEqual_Different benchmarks AssertEnvelopeEqual with
// differing envelopes (worst case - full diff computation).
func BenchmarkAssertEnvelopeEqual_Different(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := buildEnvelope(b, paymentOp(b, 200_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got)
	}
}

// BenchmarkAssertEnvelopeEqual_IgnoreVolatile benchmarks AssertEnvelopeEqual
// with IgnoreVolatile option (common production use case).
func BenchmarkAssertEnvelopeEqual_IgnoreVolatile(b *testing.B) {
	want := buildEnvelopeSeq(b, 12345, paymentOp(b, 100_0000000))
	got := buildEnvelopeSeq(b, 54321, paymentOp(b, 100_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got, focus.IgnoreVolatile())
	}
}

// BenchmarkAssertEnvelopeEqual_MultiOp benchmarks AssertEnvelopeEqual with
// multiple operations (10 payments).
func BenchmarkAssertEnvelopeEqual_MultiOp(b *testing.B) {
	ops := make([]xdr.Operation, 10)
	for i := range ops {
		ops[i] = paymentOp(b, int64((i+1)*100_0000000))
	}
	want := buildEnvelope(b, ops...)
	got := buildEnvelope(b, ops...)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got)
	}
}

// BenchmarkAssertEnvelopeEqual_MultiOpDiff benchmarks AssertEnvelopeEqual with
// multiple operations where one differs.
func BenchmarkAssertEnvelopeEqual_MultiOpDiff(b *testing.B) {
	wantOps := make([]xdr.Operation, 10)
	gotOps := make([]xdr.Operation, 10)
	for i := range wantOps {
		wantOps[i] = paymentOp(b, int64((i+1)*100_0000000))
		gotOps[i] = paymentOp(b, int64((i+1)*100_0000000))
	}
	// Make the 5th operation different
	gotOps[4] = paymentOp(b, 999_0000000)

	want := buildEnvelope(b, wantOps...)
	got := buildEnvelope(b, gotOps...)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got)
	}
}

// BenchmarkAssertResultEqual_Identical benchmarks AssertResultEqual with
// identical results.
func BenchmarkAssertResultEqual_Identical(b *testing.B) {
	want := buildResult(b, xdr.TransactionResultCodeTxSuccess, paymentResult(xdr.PaymentResultCodePaymentSuccess))
	got := want

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertResultEqual(tb, want, got)
	}
}

// BenchmarkAssertResultEqual_Different benchmarks AssertResultEqual with
// differing results.
func BenchmarkAssertResultEqual_Different(b *testing.B) {
	want := buildResult(b, xdr.TransactionResultCodeTxSuccess, paymentResult(xdr.PaymentResultCodePaymentSuccess))
	got := buildResult(b, xdr.TransactionResultCodeTxFailed, paymentResult(xdr.PaymentResultCodePaymentUnderfunded))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertResultEqual(tb, want, got)
	}
}

// BenchmarkAssertMetaEqual_Identical benchmarks AssertMetaEqual with
// identical metas.
func BenchmarkAssertMetaEqual_Identical(b *testing.B) {
	meta := metaV2(b, []xdr.LedgerEntryChange{})
	want := meta
	got := meta

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertMetaEqual(tb, want, got)
	}
}

// BenchmarkAssertMetaEqual_Different benchmarks AssertMetaEqual with
// differing metas (different ledger entry changes).
func BenchmarkAssertMetaEqual_Different(b *testing.B) {
	want := metaV2(b, []xdr.LedgerEntryChange{})
	got := metaV2(b, []xdr.LedgerEntryChange{
		createdChange(accountEntryValue(b, testSource, 1_000000000, xdr.Thresholds{})),
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertMetaEqual(tb, want, got)
	}
}

// BenchmarkAssertEqual_AutoDetect benchmarks AssertEqual with auto type
// detection.
func BenchmarkAssertEqual_AutoDetect(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := want

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEqual(tb, want, got)
	}
}

// BenchmarkAssertEnvelopeEqual_WithCustomOptions benchmarks AssertEnvelopeEqual
// with custom ignore and normalize options.
func BenchmarkAssertEnvelopeEqual_WithCustomOptions(b *testing.B) {
	want := buildEnvelopeSeq(b, 12345, paymentOp(b, 100_0000000))
	got := buildEnvelopeSeq(b, 54321, paymentOp(b, 200_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got,
			focus.IgnorePaths("**.SeqNum", "**.Signatures"),
			focus.NormalizePath("**.Fee", func(v any) any {
				if fee, ok := v.(int64); ok {
					return fee / 100 * 100
				}
				return v
			}),
		)
	}
}

// BenchmarkCoerce benchmarks the internal coerce function directly.
func BenchmarkCoerce(b *testing.B) {
	env := buildEnvelope(b, paymentOp(b, 100_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = focus.CoerceForBenchmark(env, "")
	}
}

// BenchmarkCoerceWithType benchmarks the coerce function with explicit type.
func BenchmarkCoerceWithType(b *testing.B) {
	env := buildEnvelope(b, paymentOp(b, 100_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = focus.CoerceForBenchmark(env, "TransactionEnvelope")
	}
}

// BenchmarkLensDecode benchmarks the lens.Decode function directly.
func BenchmarkLensDecode(b *testing.B) {
	env := buildEnvelope(b, paymentOp(b, 100_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = focus.LensDecodeForBenchmark(env)
	}
}

// BenchmarkLensDiff benchmarks the lens.Diff function directly.
func BenchmarkLensDiff(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := buildEnvelope(b, paymentOp(b, 200_0000000))

	wantDec, _ := focus.LensDecodeForBenchmark(want)
	gotDec, _ := focus.LensDecodeForBenchmark(got)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = focus.LensDiffForBenchmark(wantDec, gotDec)
	}
}

// BenchmarkAssertEnvelopeEqual_10kIterations runs AssertEnvelopeEqual 10,000
// times to measure sustained performance and allocation patterns.
func BenchmarkAssertEnvelopeEqual_10kIterations(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := buildEnvelope(b, paymentOp(b, 200_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(len(want) + len(got)))
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got)
	}
}

// BenchmarkAssertEnvelopeEqual_Identical_10kIterations runs AssertEnvelopeEqual
// with identical envelopes 10,000 times.
func BenchmarkAssertEnvelopeEqual_Identical_10kIterations(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := want

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(len(want) + len(got)))
	for i := 0; i < b.N; i++ {
		tb := faketb.New()
		focus.AssertEnvelopeEqual(tb, want, got)
	}
}
