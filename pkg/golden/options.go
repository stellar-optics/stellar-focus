// Package golden — options for Golden test assertions.
package golden

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// Option configures a Golden instance. It is an alias for Opt.
type Option = Opt

// WithCompact sets the golden file format to Compact JSON (single-line
// .min.json or .compact.json).
//
// When compact mode is enabled, fixtures are written as single-line JSON with
// canonical sorted keys, saving repository disk space for large fixture suites.
func WithCompact(compact bool) Option {
	return func(g *Golden) {
		if compact {
			g.format = Compact
			g.suffix = Compact.DefaultSuffix()
		} else if g.format == Compact {
			g.format = JSON
			g.suffix = JSON.DefaultSuffix()
		}
	}
}

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
