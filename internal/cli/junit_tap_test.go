package cli

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyJUnitFormatWritesSchema checks the JUnit document a CI reporter
// would read: a testsuites root, one suite, and drift as a testcase failure.
func TestVerifyJUnitFormatWritesSchema(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)

	// The chain has moved on: same hash, different content.
	src.Transactions[testHash] = driftedRecords(t)

	out := filepath.Join(t.TempDir(), "report.xml")
	_, stderr, err := run(t, "verify", dir, "--format", "junit", "--output", out)
	if err == nil {
		t.Fatal("verify reported success despite drift")
	}
	if code, _ := ExitCode(err); code != ExitDrift {
		t.Errorf("exit code %d, want %d", code, ExitDrift)
	}
	if stderr != "" {
		t.Errorf("the JUnit run wrote to stderr:\n%s", stderr)
	}

	data, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("reading the report: %v", readErr)
	}

	var document junitTestSuites
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatalf("the report is not JUnit XML: %v\n%s", err, data)
	}
	if len(document.Suites) != 1 {
		t.Fatalf("%d test suites, want 1:\n%s", len(document.Suites), data)
	}
	suite := document.Suites[0]
	if suite.Name != "focus-verify" {
		t.Errorf("suite name %q, want focus-verify", suite.Name)
	}
	if suite.Tests < 1 || suite.Tests != len(suite.Cases) {
		t.Errorf("suite reports %d tests and %d testcases", suite.Tests, len(suite.Cases))
	}
	if suite.Failures != 1 {
		t.Errorf("suite reports %d failures, want 1:\n%s", suite.Failures, data)
	}
	if suite.Errors != 0 {
		t.Errorf("suite reports %d errors, want 0", suite.Errors)
	}

	var failed bool
	for _, testCase := range suite.Cases {
		if testCase.Name == "" || testCase.Classname == "" {
			t.Errorf("testcase is missing its name or classname: %+v", testCase)
		}
		if testCase.Failure != nil {
			failed = true
			if !strings.Contains(testCase.Failure.Text, "no longer matches") {
				t.Errorf("failure body does not describe drift: %q", testCase.Failure.Text)
			}
			if !strings.Contains(testCase.Failure.Text, "modified:") {
				t.Errorf("failure body does not name a changed field: %q", testCase.Failure.Text)
			}
		}
	}
	if !failed {
		t.Fatalf("no testcase carries a failure:\n%s", data)
	}
}

// TestVerifyTAPFormatWritesPlanAndResults checks the TAP version 13 shape: a
// version line, a plan, and a not-ok result with a diagnostic block.
func TestVerifyTAPFormatWritesPlanAndResults(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	dir := t.TempDir()
	captureIntoDir(t, dir)
	src.Transactions[testHash] = driftedRecords(t)

	out := filepath.Join(t.TempDir(), "report.tap")
	_, _, err := run(t, "verify", dir, "--format", "tap", "--output", out)
	if err == nil {
		t.Fatal("verify reported success despite drift")
	}
	if code, _ := ExitCode(err); code != ExitDrift {
		t.Errorf("exit code %d, want %d", code, ExitDrift)
	}

	data, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("reading the report: %v", readErr)
	}
	text := string(data)

	for _, want := range []string{"TAP version 13\n", "1..", "not ok 1 - ", "  ---\n", "  ...\n", "# failed: 1\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("TAP output is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ok 1 - ") && !strings.Contains(text, "not ok 1 - ") {
		t.Errorf("drifted fixture was reported as ok:\n%s", text)
	}
}

// TestWriteJUnitReportsSkippedAndErrored checks that a fixture that was not
// checked and one that could not be checked are distinguishable in the XML.
func TestWriteJUnitReportsSkippedAndErrored(t *testing.T) {
	report := &VerifyReport{Results: []VerifyItemResult{
		{Path: "matched.golden.json", Status: statusMatched},
		{Path: "skipped.golden.json", Status: statusSkipped, Error: "hand-written"},
		{Path: "errored.golden.json", Status: statusErrored, Error: "unreachable"},
	}}

	var b strings.Builder
	if err := writeJUnit(&b, report); err != nil {
		t.Fatalf("writing the report: %v", err)
	}

	var document junitTestSuites
	if err := xml.Unmarshal([]byte(b.String()), &document); err != nil {
		t.Fatalf("the report is not JUnit XML: %v\n%s", err, b.String())
	}
	suite := document.Suites[0]
	if suite.Skipped != 1 {
		t.Errorf("suite reports %d skipped, want 1:\n%s", suite.Skipped, b.String())
	}
	if suite.Errors != 1 {
		t.Errorf("suite reports %d errors, want 1:\n%s", suite.Errors, b.String())
	}
	for _, testCase := range suite.Cases {
		switch testCase.Name {
		case "skipped.golden.json":
			if testCase.Skipped == nil {
				t.Error("skipped fixture has no skipped element")
			}
		case "errored.golden.json":
			if testCase.Error == nil {
				t.Error("errored fixture has no error element")
			}
		}
	}
}

// TestWriteTAPReportsSkippedAndErrored checks the TAP directives for a
// fixture that was not checked and one that could not be checked.
func TestWriteTAPReportsSkippedAndErrored(t *testing.T) {
	report := &VerifyReport{Results: []VerifyItemResult{
		{Path: "matched.golden.json", Status: statusMatched},
		{Path: "skipped.golden.json", Status: statusSkipped, Error: "hand-written"},
		{Path: "errored.golden.json", Status: statusErrored, Error: "unreachable"},
	}}

	var b strings.Builder
	if err := writeTAP(&b, report); err != nil {
		t.Fatalf("writing the report: %v", err)
	}
	text := b.String()

	if !strings.Contains(text, "ok 2 - skipped.golden.json # SKIP hand-written") {
		t.Errorf("skipped fixture is not a SKIP directive:\n%s", text)
	}
	if !strings.Contains(text, "not ok 3 - errored.golden.json") {
		t.Errorf("errored fixture is not a failure:\n%s", text)
	}
	if !strings.Contains(text, "# tests: 3\n# passed: 1\n# failed: 1\n# skipped: 1\n") {
		t.Errorf("summary comments are wrong:\n%s", text)
	}
}

// TestVerifyOutputNeedsAMachineFormat checks that --output without a format
// that produces a report is refused, rather than writing an empty file.
func TestVerifyOutputNeedsAMachineFormat(t *testing.T) {
	src := sourceWithTx(t)
	withFake(t, src)

	out := filepath.Join(t.TempDir(), "report.txt")
	_, _, err := run(t, "verify", t.TempDir(), "--output", out)
	if err == nil {
		t.Fatal("--output with the text format was accepted")
	}
	if !IsUsage(err) {
		t.Errorf("error %v is not a usage error", err)
	}
}
