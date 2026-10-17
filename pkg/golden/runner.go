package golden

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

const jsonGoldenSuffix = ".golden.json"

// Discover returns sorted fixture names relative to dir, without the JSON
// golden suffix. Nested directories are included.
func Discover(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walking golden directory %s: %w", path, walkErr)
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), jsonGoldenSuffix) {
			return nil
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("making fixture path relative: %w", err)
		}
		names = append(names, strings.TrimSuffix(filepath.ToSlash(name), jsonGoldenSuffix))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// ActualFunc produces the value compared against a fixture. name is relative
// to the fixture directory and omits .golden.json.
type ActualFunc func(t *testing.T, name string) focus.Value

// Run discovers JSON golden files and runs one named subtest per fixture.
func Run(t *testing.T, dir string, actual ActualFunc, opts ...Opt) {
	t.Helper()
	if actual == nil {
		t.Fatal("golden: runner requires an actual-value function")
		return
	}
	names, err := Discover(dir)
	if err != nil {
		t.Fatalf("golden: discovering fixtures: %v", err)
		return
	}
	goldenOpts := append([]Opt{WithDir(dir)}, opts...)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			value := actual(t, name)
			New(t, goldenOpts...).Assert(name, value)
		})
	}
}
