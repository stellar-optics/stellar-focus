package capture_test

import (
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/pkg/capture"
)

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