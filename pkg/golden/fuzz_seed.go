package golden

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// fuzzSeedHeader is the first line Go's fuzzing engine writes in a corpus
// file. A file without it is rejected as an unrecognised seed.
const fuzzSeedHeader = "go test fuzz v1"

// ExportToFuzzCorpus writes the raw XDR of every fixture under goldenDir as a
// Go fuzz seed file in fuzzTargetDir, returning how many seeds were written.
//
// It is the bridge between the two halves of this package: golden fixtures are
// real, reviewed chain data, and a Go 1.18+ fuzzer that starts from them gets
// realistic input instead of random bytes. Point fuzzTargetDir at the corpus
// directory a fuzz target reads, e.g. testdata/fuzz/FuzzDecode.
//
// Seed names are the SHA-256 of the payload, so re-running the export over an
// unchanged fixture set writes the same filenames and leaves the directory
// untouched. Two fixtures with identical XDR share a seed rather than
// producing duplicates.
func ExportToFuzzCorpus(goldenDir, fuzzTargetDir string) (int, error) {
	names, err := Discover(goldenDir)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(fuzzTargetDir, 0o755); err != nil {
		return 0, fmt.Errorf("creating fuzz corpus directory %s: %w", fuzzTargetDir, err)
	}

	written := 0
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		path := filepath.Join(goldenDir, name+jsonGoldenSuffix)
		fixture, err := Load(path)
		if err != nil {
			return written, fmt.Errorf("loading fixture %s: %w", path, err)
		}

		payload, err := base64.StdEncoding.DecodeString(fixture.XDR)
		if err != nil {
			return written, fmt.Errorf("fixture %s: XDR is not base64: %w", path, err)
		}

		sum := sha256.Sum256(payload)
		seedName := hex.EncodeToString(sum[:])
		if seen[seedName] {
			continue
		}
		seen[seedName] = true

		seed := fmt.Appendf(nil, "%s\n[]byte(%s)\n", fuzzSeedHeader, strconv.Quote(string(payload)))
		if err := os.WriteFile(filepath.Join(fuzzTargetDir, seedName), seed, 0o600); err != nil {
			return written, fmt.Errorf("writing fuzz seed %s: %w", seedName, err)
		}
		written++
	}
	return written, nil
}

// ExportFuzzSeeds is retained for callers written before the corpus format
// was named; it is ExportToFuzzCorpus with the count discarded.
//
// Deprecated: use ExportToFuzzCorpus, which reports how many seeds were
// written.
func ExportFuzzSeeds(fixtureDir, corpusDir string) error {
	_, err := ExportToFuzzCorpus(fixtureDir, corpusDir)
	return err
}
