package golden

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// ExportFuzzSeeds writes each fixture's XDR as a Go fuzz corpus seed. Existing
// files not named by this export are left untouched.
func ExportFuzzSeeds(fixtureDir, corpusDir string) error {
	names, err := Discover(fixtureDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		return fmt.Errorf("creating fuzz corpus directory %s: %w", corpusDir, err)
	}
	for index, name := range names {
		path := filepath.Join(fixtureDir, name+jsonGoldenSuffix)
		fixture, err := Load(path)
		if err != nil {
			return fmt.Errorf("loading fixture %s: %w", path, err)
		}
		seed := []byte("go test fuzz v1\nstring(" + strconv.Quote(fixture.XDR) + ")\n")
		seedPath := filepath.Join(corpusDir, fmt.Sprintf("seed_%06d", index))
		if err := os.WriteFile(seedPath, seed, 0o600); err != nil {
			return fmt.Errorf("writing fuzz seed %s: %w", seedPath, err)
		}
	}
	return nil
}
