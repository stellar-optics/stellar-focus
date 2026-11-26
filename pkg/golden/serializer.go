package golden

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fixture represents the persisted structure of a golden test asset.
type Fixture struct {
	Format      string      `json:"format"`
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Metadata    interface{} `json:"metadata,omitempty"`
	XDR         interface{} `json:"xdr"`
}

// Load reads and parses a golden fixture from disk, supporting transparent decompression.
func Load(path string) (*Fixture, error) {
	raw, err := ReadFile(path)
	if err != nil {
		return nil, err
	}

	var fixture Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return nil, fmt.Errorf("parsing golden file %s: %w", path, err)
	}

	return &fixture, nil
}

// Save marshals a golden fixture to JSON and writes it to disk, supporting transparent compression.
func Save(path string, fixture *Fixture) error {
	if fixture.Format == "" {
		fixture.Format = "focus/v1"
	}

	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling golden fixture: %w", err)
	}

	// Ensure trailing newline for POSIX compliance and clean git diffs
	data = append(data, '\n')

	return WriteFile(path, data)
}

// NormalizeLineEndings normalizes carriage returns to ensure cross-platform consistency.
func NormalizeLineEndings(b []byte) []byte {
	// Replace CRLF with LF
	if !strings.Contains(string(b), "\r\n") {
		return b
	}
	return []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
}
