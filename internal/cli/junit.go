package cli

// JUnit XML output for focus verify.
//
// CI systems parse JUnit XML to draw inline annotations and failure charts, so
// the document has to be schema-shaped rather than merely well-formed: a
// <testsuites> root, a <testsuite> per run and a <testcase> per fixture, with
// drift reported as a <failure>, an uncheckable fixture as an <error>, and a
// fixture that was not checked as <skipped>.

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// junitTestSuites is the document root.
type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

// junitTestSuite is one verification run.
type junitTestSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

// junitTestCase is one fixture.
type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Error     *junitMessage `xml:"error,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
}

// junitMessage is the body of a failure, error or skip.
type junitMessage struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// writeJUnit renders a verification report as JUnit XML.
func writeJUnit(w io.Writer, report *VerifyReport) error {
	suite := junitTestSuite{
		Name:  "focus-verify",
		Tests: len(report.Results),
		Time:  "0",
	}

	for _, item := range report.Results {
		testCase := junitTestCase{
			Name:      item.Path,
			Classname: "focus.verify",
			Time:      "0",
		}

		switch item.Status {
		case statusDrifted:
			suite.Failures++
			testCase.Failure = &junitMessage{
				Message: "fixture drifted from the chain",
				Text:    describeDiffs(item),
			}
		case statusErrored:
			suite.Errors++
			testCase.Error = &junitMessage{Message: item.Error}
		case statusSkipped, statusFiltered:
			suite.Skipped++
			testCase.Skipped = &junitMessage{Message: item.Error}
		}

		suite.Cases = append(suite.Cases, testCase)
	}

	document := junitTestSuites{Suites: []junitTestSuite{suite}}

	encoder := xml.NewEncoder(w)
	encoder.Indent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("rendering the JUnit report: %w", err)
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return nil
}

// describeDiffs renders the differences of a drifted fixture for the failure
// body, one line per changed field.
func describeDiffs(item VerifyItemResult) string {
	if len(item.Diffs) == 0 {
		return "the fixture no longer matches the chain"
	}

	var b strings.Builder
	b.WriteString("the fixture no longer matches the chain:\n")
	for _, diff := range item.Diffs {
		fmt.Fprintf(&b, "%s %s: %v -> %v\n", diff.Path, diff.Op, diff.Before, diff.After)
	}
	return b.String()
}
