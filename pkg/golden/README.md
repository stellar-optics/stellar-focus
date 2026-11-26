# `pkg/golden`

`pkg/golden` stores readable XDR fixtures and compares test values against
them. Use `Golden.Assert` for one fixture or `Golden.AssertAll` /
`Golden.AssertAllParallel` for a table of cases.

For table layout, per-case options, sequential and concurrent execution, and
failure summaries, see the
[table-driven golden testing guide](../../docs/guides/table_driven_golden.md).
For fixture format, updates, and CI integration, see the
[golden fixtures guide](../../docs/golden_fixtures_guide.md).
