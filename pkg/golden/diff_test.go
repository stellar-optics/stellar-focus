package golden_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// TestDiffIdentical verifies that Diff returns false on identical strings.
func TestDiffIdentical(t *testing.T) {
	t.Parallel()

	differed, out := golden.Diff("hello\nworld\n", "hello\nworld\n")
	if differed {
		t.Errorf("Diff reported differed=true for identical strings; out:\n%s", out)
	}
	if out != "" {
		t.Errorf("Diff produced non-empty output for identical strings:\n%s", out)
	}
}

// TestDiffMismatch verifies that Diff reports differing lines correctly.
func TestDiffMismatch(t *testing.T) {
	t.Parallel()

	differed, out := golden.Diff("alpha\nbravo\ncharlie\n", "alpha\nBRAVO\ncharlie\n")
	if !differed {
		t.Fatal("Diff reported differed=false for differing strings")
	}
	if !strings.Contains(out, "bravo") || !strings.Contains(out, "BRAVO") {
		t.Errorf("Diff output missing diff details:\n%s", out)
	}
}

// TestStreamDiffIdenticalInputsNoDiff verifies that two identical readers
// produce no diff and differed=false.
func TestStreamDiffIdenticalInputsNoDiff(t *testing.T) {
	t.Parallel()

	content := "line one\nline two\nline three\n"
	var out bytes.Buffer
	differed, err := golden.StreamDiff(
		strings.NewReader(content),
		strings.NewReader(content),
		&out,
		golden.DefaultMaxStreamDifferences,
	)
	if err != nil {
		t.Fatalf("StreamDiff() error = %v", err)
	}
	if differed {
		t.Errorf("identical inputs reported differed=true; output:\n%s", out.String())
	}
	if out.Len() != 0 {
		t.Errorf("identical inputs produced non-empty diff output:\n%s", out.String())
	}
}

// TestStreamDiffDetectsDifferences verifies that a changed line is reported.
func TestStreamDiffDetectsDifferences(t *testing.T) {
	t.Parallel()

	r1 := strings.NewReader("alpha\nbeta\ngamma\n")
	r2 := strings.NewReader("alpha\nBETA\ngamma\n")

	var out bytes.Buffer
	differed, err := golden.StreamDiff(r1, r2, &out, golden.DefaultMaxStreamDifferences)
	if err != nil {
		t.Fatalf("StreamDiff() error = %v", err)
	}
	if !differed {
		t.Error("StreamDiff() differed=false, want true")
	}
	got := out.String()
	if !strings.Contains(got, "beta") {
		t.Errorf("diff output does not name the old value:\n%s", got)
	}
	if !strings.Contains(got, "BETA") {
		t.Errorf("diff output does not name the new value:\n%s", got)
	}
}

// TestStreamDiffLimitsMaxDifferences verifies that StreamDiff halts and limits
// output to maxDifferences lines.
func TestStreamDiffLimitsMaxDifferences(t *testing.T) {
	t.Parallel()

	const maxDiffs = 5
	var r1, r2 strings.Builder
	for i := 0; i < 50; i++ {
		r1.WriteString("line from r1\n")
		r2.WriteString("line from r2\n")
	}

	var out bytes.Buffer
	differed, err := golden.StreamDiff(
		strings.NewReader(r1.String()),
		strings.NewReader(r2.String()),
		&out,
		maxDiffs,
	)
	if err != nil {
		t.Fatalf("StreamDiff error: %v", err)
	}
	if !differed {
		t.Fatal("StreamDiff reported differed=false")
	}

	hunks := strings.Count(out.String(), "@line")
	if hunks != maxDiffs {
		t.Errorf("expected %d hunk headers, got %d:\n%s", maxDiffs, hunks, out.String())
	}
	if !strings.Contains(out.String(), "truncated") {
		t.Errorf("expected truncation warning in output:\n%s", out.String())
	}
}

// TestStreamDiffTrailingWhitespaceIgnored verifies that trailing spaces do not
// count as a difference (NormalizeWhitespace contract).
func TestStreamDiffTrailingWhitespaceIgnored(t *testing.T) {
	t.Parallel()

	r1 := strings.NewReader("hello   \n")
	r2 := strings.NewReader("hello\n")

	var out bytes.Buffer
	differed, err := golden.StreamDiff(r1, r2, &out, golden.DefaultMaxStreamDifferences)
	if err != nil {
		t.Fatalf("StreamDiff() error = %v", err)
	}
	if differed {
		t.Errorf("trailing whitespace counted as a difference:\n%s", out.String())
	}
}

// TestStreamDiffR1Longer verifies that extra lines in r1 are reported as
// deletions ("eof" on r2 side).
func TestStreamDiffR1Longer(t *testing.T) {
	t.Parallel()

	r1 := strings.NewReader("line1\nline2\nextra\n")
	r2 := strings.NewReader("line1\nline2\n")

	var out bytes.Buffer
	differed, err := golden.StreamDiff(r1, r2, &out, golden.DefaultMaxStreamDifferences)
	if err != nil {
		t.Fatalf("StreamDiff() error = %v", err)
	}
	if !differed {
		t.Error("StreamDiff() differed=false, want true")
	}
	if !strings.Contains(out.String(), "(eof)") {
		t.Errorf("diff output does not name eof for shorter side:\n%s", out.String())
	}
}

// TestStreamDiffZeroMaxUsesDefault verifies that maxDifferences ≤ 0 applies
// DefaultMaxStreamDifferences instead of looping forever.
func TestStreamDiffZeroMaxUsesDefault(t *testing.T) {
	t.Parallel()

	var sb1, sb2 strings.Builder
	for i := 0; i < 100; i++ {
		sb1.WriteString("a\n")
		sb2.WriteString("b\n")
	}

	var out bytes.Buffer
	differed, err := golden.StreamDiff(strings.NewReader(sb1.String()), strings.NewReader(sb2.String()), &out, 0)
	if err != nil {
		t.Fatalf("StreamDiff() error = %v", err)
	}
	if !differed {
		t.Error("StreamDiff() differed=false, want true")
	}
	hunks := strings.Count(out.String(), "@line")
	if hunks != golden.DefaultMaxStreamDifferences {
		t.Errorf("expected %d hunk headers with zero max, got %d",
			golden.DefaultMaxStreamDifferences, hunks)
	}
}

// TestGoldenAssertEmitsStreamDiffForLargeFixture verifies that golden.Assert
// triggers a streaming diff when the fixture on disk exceeds 1 MB (issue #349).
func TestGoldenAssertEmitsStreamDiffForLargeFixture(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "large.golden.json")

	// Create a fixture file > 1MB (1 << 20 bytes).
	pad := strings.Repeat("X", 1100*1024)
	fixture, err := golden.Build(buildEnvelope(t, 100), "TransactionEnvelope", pad, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil || fi.Size() < 1024*1024 {
		t.Fatalf("fixture file size %d, want > 1MB", fi.Size())
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if g.Assert("large", buildEnvelope(t, 200)) {
		t.Fatal("Assert() passed; want failure due to diff")
	}

	out := tb.Output()
	if !strings.Contains(out, "golden: streaming diff for") {
		t.Errorf("Assert() did not emit streaming diff for >1MB fixture; output:\n%s", out)
	}
	if !strings.Contains(out, "fixture >1 MB") {
		t.Errorf("Assert() did not mention fixture >1 MB; output:\n%s", out)
	}
}

// TestStreamDiffMemoryAllocationBenchmark demonstrates <10% memory usage
// compared to in-memory diff for 20MB fixture as required by issue #349.
func TestStreamDiffMemoryAllocationBenchmark(t *testing.T) {
	// Build 20 MB payloads.
	const targetBytes = 20 * 1024 * 1024
	line := strings.Repeat("A", 100) + "\n"
	linesCount := targetBytes / len(line)

	var sb strings.Builder
	sb.Grow(targetBytes)
	for i := 0; i < linesCount; i++ {
		sb.WriteString(line)
	}
	s1 := sb.String()

	// s2 differs at line 10.
	s2 := s1[:1000] + "DIFF\n" + s1[1005:]

	// Measure in-memory Diff allocations.
	runtime.GC()
	var mBefore, mAfter runtime.MemStats
	runtime.ReadMemStats(&mBefore)
	differed1, _ := golden.Diff(s1, s2)
	runtime.ReadMemStats(&mAfter)
	inMemoryAlloc := mAfter.TotalAlloc - mBefore.TotalAlloc
	if !differed1 {
		t.Fatal("Diff failed to detect difference")
	}

	// Measure StreamDiff allocations.
	runtime.GC()
	runtime.ReadMemStats(&mBefore)
	var out bytes.Buffer
	differed2, err := golden.StreamDiff(
		strings.NewReader(s1),
		strings.NewReader(s2),
		&out,
		20,
	)
	runtime.ReadMemStats(&mAfter)
	streamAlloc := mAfter.TotalAlloc - mBefore.TotalAlloc

	if err != nil {
		t.Fatalf("StreamDiff error: %v", err)
	}
	if !differed2 {
		t.Fatal("StreamDiff failed to detect difference")
	}

	t.Logf("20MB Fixture Diff Memory: InMemory=%d bytes (%.2f MB), StreamDiff=%d bytes (%.2f MB)",
		inMemoryAlloc, float64(inMemoryAlloc)/(1024*1024),
		streamAlloc, float64(streamAlloc)/(1024*1024),
	)

	// StreamDiff must use <10% of in-memory diff memory.
	ratio := float64(streamAlloc) / float64(inMemoryAlloc)
	t.Logf("StreamDiff memory ratio: %.2f%% of in-memory", ratio*100)
	if ratio >= 0.10 {
		t.Errorf("StreamDiff memory usage (%.2f%%) exceeded 10%% threshold of in-memory diff", ratio*100)
	}
}

// BenchmarkInMemoryDiff benchmarks in-memory diff on a 1MB payload.
func BenchmarkInMemoryDiff(b *testing.B) {
	line := strings.Repeat("X", 80) + "\n"
	s1 := strings.Repeat(line, 12500) // ~1MB
	s2 := s1[:500] + "DIFF\n" + s1[505:]

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = golden.Diff(s1, s2)
	}
}

// BenchmarkStreamDiff benchmarks streaming diff on a 1MB payload.
func BenchmarkStreamDiff(b *testing.B) {
	line := strings.Repeat("X", 80) + "\n"
	s1 := strings.Repeat(line, 12500) // ~1MB
	s2 := s1[:500] + "DIFF\n" + s1[505:]

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = golden.StreamDiff(
			strings.NewReader(s1),
			strings.NewReader(s2),
			io.Discard,
			20,
		)
	}
}
