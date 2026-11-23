// Package golden — options for Golden test assertions.
package golden

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
