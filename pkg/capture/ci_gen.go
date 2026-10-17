package capture

import (
	"fmt"
	"strconv"
	"strings"
)

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