package golden

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// TrackedIgnoreRule tracks an ignore pattern along with the source location
// where it was registered and how many times it was matched.
type TrackedIgnoreRule struct {
	Pattern  string
	Location string
	Hits     int
}

// TrackIgnore creates a new TrackedIgnoreRule with the given pattern and location.
func TrackIgnore(pattern, location string) *TrackedIgnoreRule {
	return &TrackedIgnoreRule{
		Pattern:  pattern,
		Location: location,
		Hits:     0,
	}
}

// WithIgnorePaths registers ignore rules with caller file and line tracking,
// so unused rules can be detected and pruned with -focus.prune-ignores.
func WithIgnorePaths(patterns ...string) Opt {
	loc := callerLocation(1)
	return func(g *Golden) {
		for _, pat := range patterns {
			g.trackIgnoreRule(pat, loc)
		}
		g.opts = append(g.opts, focus.IgnorePaths(patterns...))
	}
}

// callerLocation returns the filename and line number of the caller, e.g. "payment_test.go:42".
func callerLocation(skip int) string {
	_, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return "unknown:0"
	}
	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
}
