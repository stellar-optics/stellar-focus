package golden

import (
	"bytes"
	"fmt"
	"text/template"
)

// Interpolate expands Go template placeholders in a golden fixture or fixture
// source text. Placeholders like {{ .AccountID }} and {{ .Sequence }} are
// resolved using the provided data, and a missing key is treated as an error so
// the template cannot silently drift.
func Interpolate(text string, data any) (string, error) {
	if text == "" {
		return "", nil
	}
	tmpl, err := template.New("golden").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return out.String(), nil
}

// ExpandTemplate is an alias for Interpolate, useful when a caller wants a more
// descriptive name for a fixture template.
func ExpandTemplate(text string, data any) (string, error) {
	return Interpolate(text, data)
}

// RenderTemplate is another alias for callers that prefer the verb form.
func RenderTemplate(text string, data any) (string, error) {
	return Interpolate(text, data)
}
