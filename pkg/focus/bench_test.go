// Package focus_test benchmarks the assertion hot paths: decoding, ignore-rule
// matching and diffing. The numbers these produce are recorded in
// docs/benchmarks.md so a regression is visible in review.
package focus_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

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

// BenchmarkAssertLedgerEntries_HighVolume measures high-volume ledger change assertions
// with cached decoded keys and fast discriminant switches.
func BenchmarkAssertLedgerEntries_HighVolume(b *testing.B) {
	entry := accountEntryValue(b, testSource, 100_0000000, xdr.Thresholds{1, 0, 0, 0})
	key, err := entry.LedgerKey()
	if err != nil {
		b.Fatalf("getting ledger key: %v", err)
	}
	keyB64, err := key.MarshalBinary()
	if err != nil {
		b.Fatalf("marshaling ledger key: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		focus.AssertLedgerEntriesEqual(faketb.New(), entry, entry)
		focus.AssertLedgerKeyEqual(faketb.New(), key, keyB64)
	}
}

// BenchmarkAssertOperations_HighVolume measures hot-path assertion loops over operations
// using static discriminant dispatch.
func BenchmarkAssertOperations_HighVolume(b *testing.B) {
	op := paymentOp(b, 100_0000000)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		focus.AssertOperationsEqual(faketb.New(), op, op)
	}
}
