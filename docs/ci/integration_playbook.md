# CI/CD Integration Playbook for `focus verify`

This playbook provides battle-tested pipeline configurations for running `focus verify` in CI, publishing PR comment summaries, and capturing artifacts. It covers GitHub Actions and GitLab CI, with best practices for caching and exit-code handling.

## GitHub Actions Workflow

Add this workflow to `.github/workflows/focus-verify.yml` in your repository:

```yaml
name: Focus Fixture Verification

on:
  push:
    branches: [main, release/*]
  pull_request:
    branches: [main]

jobs:
  verify-fixtures:
    runs-on: ubuntu-latest
    timeout-minutes: 30

    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25'

      - name: Install focus CLI
        run: go install github.com/stellar-optics/stellar-focus/cmd/focus@latest

      - name: Focus Verify (with drift detection)
        id: verify
        run: |
          focus verify testdata/ --format json --output report.json
          # Exit code 0 = all matched, 2 = drift found, 1 = check could not run

      - name: Upload verification report
        uses: actions/upload-artifact@v4
        with:
          name: focus-verify-report
          path: report.json

      - name: Comment on PR (drift only)
        if: github.event_name == 'pull_request' && steps.verify.outcome == 'failure'
        uses: peter-evans/create-or-update-comment@v4
        with:
          token: ${{ github.token }}
          issue-number: ${{ github.event.pull_request.number }}
          body: |
            ### Focus Fixture Verification
            
            The `focus verify` step completed with drift detected. See the full report in the artifacts.
            
            ```json
            $(cat report.json | jq .)
            ```
            
            To update the golden fixtures, run `go test ./... -focus.update` locally and commit the changes.

      - name: Comment on PR (no drift)
        if: github.event_name == 'pull_request' && steps.verify.outcome == 'success'
        uses: peter-evans/create-or-update-comment@v4
        with:
          token: ${{ github.token }}
          issue-number: ${{ github.event.pull_request.number }}
          body: |
            ### Focus Fixture Verification
            
            All golden fixtures match. No drift detected.
            
            The verification report is available in the workflow artifacts.

      - name: Cache .focus directory
        uses: actions/cache@v4
        with:
          path: ~/.focus
          key: focus-cache-${{ runner.os }}-${{ hashFiles('go.sum') }}
          restore-keys: |
            focus-cache-${{ runner.os }}-
```

### Exit Code Handling

| Exit Code | Meaning | CI Action |
|---|---|---|
| `0` | All fixtures matched | Treat as success; report "all matched" |
| `1` | A fixture could not be checked (e.g., network error, timeout) | Treat as failure or warning; report "1 errored"; consider retry |
| `2` | At least one fixture drifted | Treat as failure; show the diff report in PR comment; block merge if desired |

**Important**: Since v0.2, `focus verify` exits `2` for drift and `1` for check errors. A CI pipeline that previously treated `1` as "drift" should test for `2` instead.

### Why `--drifted-only`?

The `--drifted-only` flag re-checks only fixtures that previously drifted, using the cache in `.focus/verify-cache.json`. This:

- Keeps CI fast for repositories with hundreds of fixtures
- StayS within Soroban RPC rate limits
- Reports filtered fixtures with a `FILTERED` status and reason

For first runs or when fixtures are frequently updated, omit `--drifted-only` to check everything.

### Caching the `.focus` Directory

The `.focus` directory (created by `focus capture` and used by `focus verify`) contains the verification cache. Cache it across CI runs:

```yaml
- uses: actions/cache@v4
  with:
    path: ~/.focus
    key: focus-cache-${{ runner.os }}-${{ hashFiles('go.sum') }}
    restore-keys: |
      focus-cache-${{ runner.os }}-
```

This avoids re-fetching the same transactions on every run. Note: the cache is a speed-up, not a source of truth; delete it (`rm -rf ~/.focus`) if you suspect it's stale.

---

## GitLab CI Configuration

Add this pipeline snippet to `.gitlab-ci.yml`:

```yaml
focus_verify:
  stage: test
  image: golang:1.25
  variables:
    GOPATH: /go
    GOFLAGS: "-mod=mod"
  script:
    - go install github.com/stellar-optics/stellar-focus/cmd/focus@latest
    
    # Run focus verify and capture JSON report
    - focus verify testdata/ --format json --output report.json || true
    
    # Upload artifact for download
    - artifacts:
        paths:
          - report.json
        name: focus-verify-report
    
    # Publish JUnit XML for test result tracking
    - name: Convert to JUnit XML
      run: |
        # focus verify JSON -> JUnit XML conversion
        # The JSON has: total, matched, drifted, skipped, errored, results[]
        # We'll generate a simple XML summary
        python3 - <<'PY'
import json, sys, xml.etree.ElementTree as ET

with open('report.json') as f:
    report = json.load(f)

root = ET.Element("testsuite", name="focus-verify", tests=str(report["total"]))

results = report["results"]
drifted = 0
matched = 0
skipped = 0
errored = 0

for r in results:
    status = r["status"]
    if status == "drifted":
        drifted += 1
    elif status == "matched":
        matched += 1
    elif status == "skipped":
        skipped += 1
    elif status == "errored":
        errored += 1

# Add test cases for drifted fixtures
for r in results:
    if r["status"] == "drifted":
        tc = ET.SubElement(root, "testcase", name=r["path"])
        # Add failure info for drifted fixtures
        ET.SubElement(tc, "failure", message=r.get("diff_paths", ["unknown"])[0])
        if drifted == 1:
            drifted_flag = "drifted"

ET.SubElement(root, "system-out").text = json.dumps(report, indent=2)

tree = ET.ElementTree(root)
with open("focus-verify-junit.xml", "wb") as f:
    tree.write(f, encoding="unicode", xml_method=" PY
    
    # Publish test results
    - name: Publish test results
      uses: mikepenz/action-junit-report@v2
      with:
        junit: focus-verify-junit.xml
        fail-on-error: false
        fail-on-warning: false
```

### Exit Code Handling in GitLab CI

GitLab CI treats a job as failed if the exit code is non-zero. Since `focus verify` exits `2` on drift, the job will fail. To handle this gracefully:

1. **`|| true`** after the command (as shown above) to prevent the job from failing immediately.
2. **Parse the report.json** to determine the actual outcome.
3. **Use `allow_failure: true`** if you want the pipeline to continue even on drift, but still publish the report.

Example with conditional failure:

```yaml
focus_verify:
  stage: test
  image: golang:1.25
  script:
    - focus verify testdata/ --format json --output report.json || true
    
    # Fail the job only if drift was detected
    - name: Check for drift
      run: |
        drifted=$(python3 -c "import json; r=json.load(open('report.json')); print(any(x['status']=='drifted' for x in r['results']))")
        if [ "$drifted" = "True" ]; then
          echo "DRIFT_DETECTED=1" >> $GITHUB_ENV
          exit 1
        fi
      shell: bash
```

---

## Best Practices

### 1. Always Use `--format json` in CI

The JSON format is machine-readable and carries the same information as the terminal report, plus the counts and per-fixture status. This allows scripts to branch on the exit code and read the document for details.

### 2. Cache `.focus/` Across Runs

The `.focus` directory contains the verification cache used by `--drifted-only`. Cache it to avoid re-fetching the same transactions on every CI run.

### 3. Use `--drifted-only` for Recurring CI

For PR checks and cron jobs, `--drifted-only` is the default recommended mode. It only re-checks fixtures that previously drifted, keeping CI fast and within API rate limits.

### 4. Publish a PR Comment on Drift

When drift is detected, publish a comment on the PR with the JSON report. This gives reviewers the exact field paths that changed, making it easy to decide whether the change is intentional (update the fixture) or accidental.

### 5. Handle the Exit Code Correctly

- Exit `0`: All matched — pipeline can continue.
- Exit `1`: Check could not run — investigate; may warrant a retry.
- Exit `2`: Drift detected — this is the expected "failure" case that indicates a real change.

### 6. Include the Report in Artifacts

Always upload `report.json` as a CI artifact so reviewers can inspect the details without running the command locally.

### 7. For GitLab CI: Use JUnit XML for Test Tracking

Convert the JSON report to JUnit XML format so GitLab's test result UI shows the verification summary. The snippet above demonstrates a simple conversion.

### 8. Version-Pin the focus CLI

Use `go install github.com/stellar-optics/stellar-focus/cmd/focus@latest` or pin to a specific version/version range in your CI configuration to ensure consistent behavior across runs.

### 9. Handle Network Errors Gracefully

If a fixture cannot be fetched (network timeout, endpoint unreachable), `focus verify` exits `1`. In CI, this is often transient; consider adding retry logic or marking the run as "pending" rather than failing the pipeline.

### 10. Document the Workflow

Add a brief README section or `CI.md` file explaining:
- How to run `focus verify` locally
- What the exit codes mean
- How to update fixtures with `-focus.update`
- Where to find the verification report in CI

---

## Related Documentation

- [Golden Fixtures Guide & CI Integration](docs/golden_fixtures_guide.md) — fixture anatomy, `-focus.update`, `focus golden resolve`
- [Reference Glossary](docs/reference/glossary.md) — terminology used throughout this playbook
- `focus verify --help` — CLI help for all available flags
- `focus verify --format json --help` — JSON output format details