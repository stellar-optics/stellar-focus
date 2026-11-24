package capture_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

func TestGenerateGitHubActionWorkflowIsValidYAML(t *testing.T) {
	t.Parallel()

	bundle := &capture.Bundle{
		Format:   capture.BundleFormat,
		Network:  capture.NetworkTestnet,
		Endpoint: "https://mainnet.sorobanrpc.com",
		Members: []capture.BundleMember{
			{Name: "tx01_envelope.golden.json", Type: "TransactionEnvelope", TxHash: "2b2e276a", Ledger: 3966820,
				CapturedAt: "2026-08-05T12:00:00Z"},
		},
	}

	workflow := capture.GenerateGitHubActionWorkflow(bundle)

	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(workflow), &decoded); err != nil {
		t.Fatalf("generated workflow is not valid YAML: %v", err)
	}

	if name, _ := decoded["name"].(string); name != "Verify Stellar fixtures" {
		t.Errorf("workflow name = %q, want %q", name, "Verify Stellar fixtures")
	}

	on, ok := decoded["on"].(map[string]any)
	if !ok {
		t.Fatal("workflow has no `on` trigger block")
	}
	if _, ok := on["schedule"]; !ok {
		t.Error("workflow has no schedule trigger")
	}
	if _, ok := on["workflow_dispatch"]; !ok {
		t.Error("workflow cannot be triggered by hand")
	}

	jobs, ok := decoded["jobs"].(map[string]any)
	if !ok {
		t.Fatal("workflow has no jobs")
	}
	job, ok := jobs["focus-verify"].(map[string]any)
	if !ok {
		t.Fatal("workflow has no focus-verify job")
	}
	if runsOn, _ := job["runs-on"].(string); runsOn != "ubuntu-latest" {
		t.Errorf("job runs-on = %q, want %q", runsOn, "ubuntu-latest")
	}

	strategy, _ := job["strategy"].(map[string]any)
	matrix, _ := strategy["matrix"].(map[string]any)
	goVersions, ok := matrix["go-version"].([]any)
	if !ok || len(goVersions) < 2 {
		t.Errorf("matrix does not run multiple Go versions: %v", matrix["go-version"])
	}
	for _, want := range []string{"1.25", "1.26"} {
		if !stringInMatrix(goVersions, want) {
			t.Errorf("matrix go-version does not include %q: %v", want, matrix["go-version"])
		}
	}
	networks, ok := matrix["network"].([]any)
	if !ok || len(networks) == 0 {
		t.Fatalf("matrix has no networks: %v", matrix["network"])
	}
	for _, want := range []string{capture.NetworkTestnet, capture.NetworkMainnet} {
		if !stringInMatrix(networks, want) {
			t.Errorf("matrix network does not include %q: %v", want, matrix["network"])
		}
	}

	for _, want := range []string{
		"actions/checkout@v4",
		"actions/setup-go@v5",
		"cache: true",
		"go install ./cmd/focus",
		"focus verify --network ${{ matrix.network }} --drifted-only testdata/",
		"FOCUS_NETWORK: ${{ matrix.network }}",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("workflow does not contain %q:\n%s", want, workflow)
		}
	}
}

func stringInMatrix(values []any, want string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == want {
			return true
		}
	}
	return false
}

func TestGenerateGitHubActionWorkflowIncludesTheBundleNetwork(t *testing.T) {
	t.Parallel()

	bundle := &capture.Bundle{Format: capture.BundleFormat, Network: "futurenet"}
	workflow := capture.GenerateGitHubActionWorkflow(bundle)

	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(workflow), &decoded); err != nil {
		t.Fatalf("generated workflow is not valid YAML: %v", err)
	}
	matrix := decoded["jobs"].(map[string]any)["focus-verify"].(map[string]any)["strategy"].(map[string]any)["matrix"].(map[string]any)
	networks := matrix["network"].([]any)
	var got []string
	for _, value := range networks {
		got = append(got, value.(string))
	}
	for _, want := range []string{capture.NetworkTestnet, capture.NetworkMainnet, "futurenet"} {
		if !stringInMatrix(networks, want) {
			t.Errorf("matrix network = %v, want it to include %q", got, want)
		}
	}
}

func TestGenerateGitHubActionWorkflowIsDeterministic(t *testing.T) {
	t.Parallel()

	bundle := &capture.Bundle{Format: capture.BundleFormat, Network: capture.NetworkMainnet}
	first := capture.GenerateGitHubActionWorkflow(bundle)
	second := capture.GenerateGitHubActionWorkflow(bundle)
	if first != second {
		t.Fatal("identical bundles produced different workflows")
	}
	if strings.Count(first, "focus verify") != 1 || strings.Count(first, "--drifted-only") != 1 {
		t.Errorf("workflow does not run focus verify --drifted-only exactly once:\n%s", first)
	}
}

func TestGenerateCIWorkflowIsDeterministicAndConfigurable(t *testing.T) {
	t.Parallel()

	options := capture.CIWorkflowOptions{Schedule: "0 3 * * 1", FixturePath: "fixtures/network"}
	first, err := capture.GenerateCIWorkflow(options)
	if err != nil {
		t.Fatalf("GenerateCIWorkflow() error = %v", err)
	}
	second, err := capture.GenerateCIWorkflow(options)
	if err != nil {
		t.Fatalf("second GenerateCIWorkflow() error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("identical options produced different workflow content")
	}
	for _, want := range []string{"workflow_dispatch", "actions/checkout@v4", "focus\" verify --network testnet \"fixtures/network\"", "cron: \"0 3 * * 1\""} {
		if !strings.Contains(string(first), want) {
			t.Errorf("workflow does not contain %q", want)
		}
	}
}

func TestGenerateCIWorkflowRejectsLineBreakInjection(t *testing.T) {
	t.Parallel()

	if _, err := capture.GenerateCIWorkflow(capture.CIWorkflowOptions{FixturePath: "testdata\nrun: unsafe"}); err == nil {
		t.Fatal("GenerateCIWorkflow() accepted a multiline fixture path")
	}
}
