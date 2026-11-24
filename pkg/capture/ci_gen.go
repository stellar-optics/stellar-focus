package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// GeneratedReplayTest is one replay test produced by batch code generation.
type GeneratedReplayTest struct {
	// FixturePath is the fixture the test replays, as given.
	FixturePath string
	// OutPath is the file the generated test is written to.
	OutPath string
	// Source is the formatted Go source.
	Source string
}

// GenerateReplayTestBatch renders replay tests for many captured fixtures.
//
// Every fixture is checked before any source is generated, so a bad entry
// fails the batch rather than leaving half a directory of tests behind. The
// fixture path written into each test is relative to outputDir, which is where
// the generated files will live.
func GenerateReplayTestBatch(fixtures []*CaptureFixture, outputDir string) ([]GeneratedReplayTest, error) {
	if outputDir == "" {
		return nil, fmt.Errorf("batch code generation: an output directory is required")
	}

	out := make([]GeneratedReplayTest, 0, len(fixtures))
	for i, fixture := range fixtures {
		if fixture == nil {
			return nil, fmt.Errorf("batch code generation: fixture %d is nil", i)
		}

		fixturePath := fixture.FixturePath
		if fixturePath == "" {
			return nil, fmt.Errorf("batch code generation: fixture %d has no path", i)
		}
		rel, err := filepath.Rel(outputDir, fixturePath)
		if err != nil {
			return nil, fmt.Errorf("batch code generation: relating %s to %s: %w", fixturePath, outputDir, err)
		}

		generated := *fixture
		generated.FixturePath = rel
		source, err := GenerateReplayTest(&generated)
		if err != nil {
			return nil, fmt.Errorf("batch code generation for %s: %w", fixturePath, err)
		}

		name := generated.Name
		if name == "" {
			name = fixtureNameFromPath(fixturePath)
		}
		outPath := filepath.Join(outputDir, name+"_test.go")
		out = append(out, GeneratedReplayTest{
			FixturePath: fixturePath,
			OutPath:     outPath,
			Source:      source,
		})
	}
	return out, nil
}

// WriteReplayTests generates replay tests for many fixtures and writes them to
// outputDir, creating the directory if needed. It returns the paths written, in
// the same order as the fixtures.
func WriteReplayTests(fixtures []*CaptureFixture, outputDir string) ([]string, error) {
	generated, err := GenerateReplayTestBatch(fixtures, outputDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", outputDir, err)
	}

	paths := make([]string, len(generated))
	for i, test := range generated {
		if err := os.WriteFile(test.OutPath, []byte(test.Source), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", test.OutPath, err)
		}
		paths[i] = test.OutPath
	}
	return paths, nil
}

// CIWorkflowOptions configures a generated GitHub Actions verification job.
type CIWorkflowOptions struct {
	// Schedule is a GitHub Actions cron expression. Empty uses every six hours.
	Schedule string
	// FixturePath is a file or directory accepted by focus verify. Empty uses testdata.
	FixturePath string
}

// GenerateCIWorkflow returns a deterministic GitHub Actions workflow that
// builds focus from the checked-out repository and verifies fixtures on testnet.
func GenerateCIWorkflow(options CIWorkflowOptions) ([]byte, error) {
	schedule := options.Schedule
	if schedule == "" {
		schedule = "17 */6 * * *"
	}
	fixturePath := options.FixturePath
	if fixturePath == "" {
		fixturePath = "testdata"
	}
	if strings.ContainsAny(schedule+fixturePath, "\r\n\x00") {
		return nil, fmt.Errorf("schedule and fixture path must not contain line breaks or NUL bytes")
	}
	return []byte(fmt.Sprintf(`name: Stellar fixture verification

on:
  schedule:
    - cron: %s
  workflow_dispatch:

permissions:
  contents: read

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Build focus
        run: go build -o "$RUNNER_TEMP/focus" ./cmd/focus
      - name: Verify testnet fixtures
        run: "$RUNNER_TEMP/focus" verify --network testnet %s
`, strconv.Quote(schedule), strconv.Quote(fixturePath))), nil
}

// defaultGoVersions are the Go versions a generated workflow matrices over.
// The matrix catches regressions introduced by a new Go release as well as
// by a new protocol release.
var defaultGoVersions = []string{"1.25", "1.26"}

// defaultNetworks are the networks a generated workflow verifies against.
var defaultNetworks = []string{NetworkTestnet, NetworkMainnet}

// quotedStrings renders a string list as a YAML flow sequence of quoted
// strings, safe for the workflow's matrix.
func quotedStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// hasString reports whether values contains v.
func hasString(values []string, v string) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}

// GenerateGitHubActionWorkflow returns a complete GitHub Actions workflow
// that verifies a captured bundle's fixtures on every push and on a
// schedule, across a matrix of Go versions and networks.
//
// The workflow checks the repository out, builds focus from it, caches the
// Go module cache between runs, and runs `focus verify --drifted-only`, so
// a regression in a new release fails the run instead of silently drifting
// fixtures. A non-zero --rate or an explicit --rpc-url can be added through
// the FOCUS_* environment variables the command reads.
func GenerateGitHubActionWorkflow(bundle *Bundle) string {
	networks := append([]string{}, defaultNetworks...)
	if bundle != nil && bundle.Network != "" && !hasString(networks, bundle.Network) {
		networks = append(networks, bundle.Network)
	}

	return fmt.Sprintf(`name: Verify Stellar fixtures

on:
  push:
    branches: ["main"]
  pull_request:
  schedule:
    - cron: "17 */6 * * *"
  workflow_dispatch:

permissions:
  contents: read

jobs:
  focus-verify:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        go-version: %s
        network: %s
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go-version }}
          cache: true

      - name: Install focus
        run: go install ./cmd/focus

      - name: Verify bundle fixtures against ${{ matrix.network }}
        run: focus verify --network ${{ matrix.network }} --drifted-only testdata/
        env:
          FOCUS_NETWORK: ${{ matrix.network }}
`, quotedStrings(defaultGoVersions), quotedStrings(networks))
}
