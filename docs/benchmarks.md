# Benchmarks

The assertion path is on the hot path of every test in a suite that uses
`stellar-focus`, so its cost is measured rather than assumed. The benchmarks
live next to the code they measure:

| Benchmark | Where |
| --- | --- |
| `BenchmarkAssertEnvelopeEqual_Match` / `_Mismatch` | `pkg/focus/bench_test.go` |
| `BenchmarkIgnorePaths_Filter` | `pkg/focus/bench_test.go` |
| `BenchmarkGolden_Assert` / `_Load` / `_Save` | `pkg/golden/bench_test.go` |

Run them with:

```sh
go test ./pkg/focus ./pkg/golden -run '^$' -bench . -benchmem
```

## Baseline

Recorded on linux/amd64, Go 1.25, Intel Core i5-6300HQ, with `-count=1`:

```
BenchmarkAssertEnvelopeEqual_Match-4       26229    44905 ns/op   13454 B/op    231 allocs/op
BenchmarkAssertEnvelopeEqual_Mismatch-4    22464    53319 ns/op   16657 B/op    273 allocs/op
BenchmarkIgnorePaths_Filter-4              14661    80947 ns/op   18522 B/op    504 allocs/op

BenchmarkGolden_Assert-4                    1136  1002625 ns/op  230561 B/op   3082 allocs/op
BenchmarkGolden_Load-4                      2025   623422 ns/op  150518 B/op   1343 allocs/op
BenchmarkGolden_Save-4                      3831   557423 ns/op  127825 B/op     29 allocs/op
```

What the numbers say:

- A passing envelope assertion costs ~45 µs, almost all of it decoding the
  two envelopes. A failing one costs ~9 µs more, which is the diff and the
  rendered message.
- Ignore rules are the most expensive passing case at ~81 µs: matching eight
  wildcard patterns against every path is real work, and it is where a
  regression would show up first.
- Golden assertions cost ~1 ms, dominated by reading and unmarshalling the
  fixture (~620 µs) rather than by the comparison. Saving a fixture is
  ~560 µs and allocates 29 times, so recording is never the bottleneck in a
  test run.

These are baselines, not budgets: the point is to notice a change, so compare
against the same machine and Go version.
