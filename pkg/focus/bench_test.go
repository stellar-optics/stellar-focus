// Package focus_test benchmarks the assertion hot paths: decoding, ignore-rule
// matching and diffing. The numbers these produce are recorded in
// docs/benchmarks.md so a regression is visible in review.
package focus_test

import (
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// BenchmarkAssertEnvelopeEqual_Match measures the passing case: two identical
// envelopes, where no diff is computed.
func BenchmarkAssertEnvelopeEqual_Match(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := want

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		focus.AssertEnvelopeEqual(faketb.New(), want, got)
	}
}

// BenchmarkAssertEnvelopeEqual_Mismatch measures the failing case, where the
// diff is calculated and formatted for the failure message.
func BenchmarkAssertEnvelopeEqual_Mismatch(b *testing.B) {
	want := buildEnvelope(b, paymentOp(b, 100_0000000))
	got := buildEnvelope(b, paymentOp(b, 200_0000000))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		focus.AssertEnvelopeEqual(faketb.New(), want, got)
	}
}

// BenchmarkIgnorePaths_Filter measures pattern matching alone: the envelopes
// differ only in the fields these deep wildcard patterns ignore, so the work
// per iteration is the ignore-rule filtering rather than a reported diff.
func BenchmarkIgnorePaths_Filter(b *testing.B) {
	want := buildEnvelopeSeq(b, 12345, paymentOp(b, 100_0000000))
	got := buildEnvelopeSeq(b, 54321, paymentOp(b, 200_0000000))
	patterns := []string{
		"**.SeqNum",
		"**.Signatures",
		"V1.Tx.Operations[*].Body.PaymentOp.Amount",
		"**.TimeBounds.*",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		focus.AssertEnvelopeEqual(faketb.New(), want, got, focus.IgnorePaths(patterns...))
	}
}
