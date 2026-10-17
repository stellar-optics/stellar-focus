package golden

import (
	"regexp"
	"strings"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

func noiseSuppressionOptions() []focus.Option {
	normalize := func(any) any { return "<normalized>" }
	return []focus.Option{
		focus.NormalizePath("**.SeqNum", normalize),
		focus.NormalizePath("**.MinTime", normalize),
		focus.NormalizePath("**.MaxTime", normalize),
		focus.NormalizePath("**.LedgerCloseTime", normalize),
		focus.NormalizePath("**.Timestamp", normalize),
		focus.NormalizePath("**.CreatedAt", normalize),
		focus.NormalizePath("**.UpdatedAt", normalize),
	}
}

// NormalizeNoise rewrites common dynamic values in a golden payload so fixture
// comparisons ignore volatile timestamps and sequence numbers while preserving the
// rest of the structure. The normalizer is deliberately conservative: it only
// replaces values that are well-known to change between runs or across networks.
func NormalizeNoise(data []byte) []byte {
	text := string(data)
	for _, re := range []string{
		`(?i)("(?:timestamp|time|created_at|closed_at|updated_at|at)"\s*:\s*")([^"]+)(")`,
		`(?i)("(?:sequence|seq_num|seqNum|ledger_seq|ledgerSequence)"\s*:\s*)(-?\d+)(\s*(?:,|\}))`,
	} {
		text = regexp.MustCompile(re).ReplaceAllString(text, `$1<normalized>$3`)
	}
	return []byte(text)
}

// StripNoise is a convenience wrapper around NormalizeNoise for text fixtures.
func StripNoise(data []byte) []byte { return NormalizeNoise(data) }

// SuppressNoise is a compatibility alias used by callers that prefer the verb.
func SuppressNoise(data []byte) []byte { return NormalizeNoise(data) }

// Noise is the string-oriented form of the normalizer.
func Noise(text string) string {
	return strings.TrimSuffix(string(NormalizeNoise([]byte(text))), "\n")
}
