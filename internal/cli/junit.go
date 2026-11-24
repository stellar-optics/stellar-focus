// JUnit XML output formatter for focus verify.
//
// Generates JUnit XML compatible with CI/test result parsers.
// See: https://testng.org/doc/jet.html#writingtestngxml
package cli

import (
	"fmt"
	"os"
)

// JUnitWriter writes JUnit XML output to a writer.
type JUnitWriter struct {
	file *os.File
}

// NewJUnitWriter creates a new JUnitWriter that writes to the given file.
// If file is nil, output goes to stderr.
func NewJUnitWriter(file *os.File) *JUnitWriter {
	if file == nil {
		return &JUnitWriter{file: nil}
	}
	return &JUnitWriter{file: file}
}

// WriteTestSuite writes a complete JUnit testsuite element.
//
// Parameters:
//   - name: the test suite name
//   - tests: total number of tests
//   - failures: total number of failures
//   - testsRun: number of tests that were actually run/executed
func (w *JUnitWriter) WriteTestSuite(name string, tests, failures, testsRun int) error {
	var s string
	if w.file == nil {
		s = fmt.Sprintf("<testsuite name=\"%s\" tests=\"%d\" failures=\"%d\" tests-run=\"%d\">\n", name, tests, failures, testsRun)
	} else {
		s = fmt.Sprintf("<testsuite name=\"%s\" tests=\"%d\" failures=\"%d\" tests-run=\"%d\">\n", name, tests, failures, testsRun)
	}
	if w.file == nil {
		_, _ = fmt.Fprint(os.Stderr, s)
	} else {
		_, _ = fmt.Fprint(w.file, s)
	}
	return nil
}

// WriteTestCase writes a single JUnit testcase element.
//
// Parameters:
//   - name: the test case name
//   - classname: the class name (typically the fixture path or test identifier)
//   - time: the execution time in seconds (can be "0" if unknown)
//   - failureMessage: optional failure message; empty if test passed
func (w *JUnitWriter) WriteTestCase(name, classname, time, failureMessage string) error {
	var s string
	if w.file == nil {
		if failureMessage == "" {
			s = fmt.Sprintf("<testcase name=\"%s\" classname=\"%s\" time=\"%s\"/>\n", name, classname, time)
		} else {
			s = fmt.Sprintf("<testcase name=\"%s\" classname=\"%s\" time=\"%s\"><failure message=\"%s\"/></testcase>\n", name, classname, time, failureMessage)
		}
	} else {
		if failureMessage == "" {
			s = fmt.Sprintf("<testcase name=\"%s\" classname=\"%s\" time=\"%s\"/>\n", name, classname, time)
		} else {
			s = fmt.Sprintf("<testcase name=\"%s\" classname=\"%s\" time=\"%s\"><failure message=\"%s\"/></testcase>\n", name, classname, time, failureMessage)
		}
	}
	if w.file == nil {
		_, _ = fmt.Fprint(os.Stderr, s)
	} else {
		_, _ = fmt.Fprint(w.file, s)
	}
	return nil
}

// WriteEndTestSuite writes the closing testsuite element.
func (w *JUnitWriter) WriteEndTestSuite() error {
	var s string
	if w.file == nil {
		s = "</testsuite>\n"
	} else {
		s = "</testsuite>\n"
	}
	if w.file == nil {
		_, _ = fmt.Fprint(os.Stderr, s)
	} else {
		_, _ = fmt.Fprint(w.file, s)
	}
	return nil
}

// Close closes the underlying file if one was provided.
func (w *JUnitWriter) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// WriteSummary writes a complete JUnit XML report for focus verify results.
// This is a convenience method that writes the full XML structure.
func WriteSummary(w *JUnitWriter, checked, matched, drifted, skipped int) error {
	if err := w.WriteTestSuite("focus-verify", checked, drifted, checked); err != nil {
		return err
	}

	// Write test cases
	for i := 0; i < checked; i++ {
		// We don't have individual test case names here, so we'll write a placeholder
		// In a full implementation, each fixture would be a test case
		if err := w.WriteTestCase("fixture-"+fmt.Sprintf("%d", i), "focus-verify", "0", ""); err != nil {
			return err
		}
	}

	if err := w.WriteEndTestSuite(); err != nil {
		return err
	}

	return nil
}

// WriteReport writes a complete JUnit XML report to the given writer/file.
// This is the main entry point for generating JUnit XML output from focus verify.
func WriteReport(w *JUnitWriter, checked, matched, drifted, skipped int) error {
	if err := w.WriteTestSuite("focus-verify", checked, drifted, checked); err != nil {
		return err
	}

	// Write test cases - each checked fixture becomes a test case
	for i := 0; i < checked; i++ {
		failureMsg := ""
		if drifted > 0 && i < drifted {
			failureMsg = "Fixture drifted from chain"
		}
		if err := w.WriteTestCase("fixture-"+fmt.Sprintf("%d", i), "focus-verify", "0", failureMsg); err != nil {
			return err
		}
	}

	if err := w.WriteEndTestSuite(); err != nil {
		return err
	}

	return nil
}