// Package golden — options for Golden test assertions.
package golden

import "github.com/stellar-optics/stellar-focus/pkg/focus"

// Option configures a Golden instance. It is an alias for Opt.
type Option = Opt

// NormalizerFunc rewrites a decoded value at its path before comparison and
// when a golden fixture is recorded.
type NormalizerFunc func(path string, value any) any

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

// WithCustomNormalizer applies fn to decoded values by path when comparing
// values and writing the fixture's decoded section.
func WithCustomNormalizer(fn NormalizerFunc) Option {
	return func(g *Golden) {
		g.customNormalizers = append(g.customNormalizers, fn)
		g.opts = append(g.opts, focus.NormalizeEachPath(fn))
	}
}
