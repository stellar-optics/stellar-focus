// TAP (Test Anything Protocol) output formatter for focus verify.
//
// TAP version 13: https://testanything.org/tap-specification-130.html
//
// Standard TAP output format used by most CI systems.
// Outputs: 1..N at the top, then ok/not ok lines for each test.
//
// Example output:
//   1..5
//   ok 1 - fixture1.golden.json
//   not ok 2 - fixture2.golden.json
//   ok 3 - fixture3.golden.json
//   # looks like 5 tests
//   # passed: 3
//   # failed: 2
package cli

import (
	"fmt"
	"os"
)

// TapWriter writes TAP (Test Anything Protocol) output to a writer.
type TapWriter struct {
	file   *os.File
	closed bool
}

// NewTapWriter creates a new TapWriter that writes to the given file.
// If file is nil, output goes to stderr.
func NewTapWriter(file *os.File) *TapWriter {
	if file == nil {
		return &TapWriter{file: os.Stderr}
	}
	return &TapWriter{file: file}
}

// WritePlan writes the TAP plan header "1..N" where N is the total number of tests.
// This should be called once at the start of TAP output.
func (w *TapWriter) WritePlan(totalTests int) error {
	_, err := fmt.Fprintf(w.file, "1..%d\n", totalTests)
	return err
}

//WriteOK writes "ok N - description" for a passing test.
func (w *TapWriter) WriteOK(testNum int, description string) error {
	_, err := fmt.Fprintf(w.file, "ok %d - %s\n", testNum, description)
	return err
}

// WriteNotOK writes "not ok N - description" for a failing test.
func (w *TapWriter) WriteNotOK(testNum int, description string) error {
	_, err := fmt.Fprintf(w.file, "not ok %d - %s\n", testNum, description)
	return err
}

// WriteSummary writes the TAP summary lines showing passed/failed counts.
// This should be called after all test results have been written.
func (w *TapWriter) WriteSummary(passed, failed int) error {
	_, err := fmt.Fprintf(w.file, "# passed: %d\n", passed)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w.file, "# failed: %d\n", failed)
	return err
}

// Close closes the underlying file (if not stderr).
func (w *TapWriter) Close() error {
	if w.file == nil || w.file == os.Stderr {
		return nil
	}
	return w.file.Close()
}