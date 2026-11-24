package golden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

// Interpolate applies Go template syntax to text using data as its context.
func Interpolate(text string, data any) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	parsed, err := template.New("golden").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parsing fixture template: %w", err)
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, data); err != nil {
		return "", fmt.Errorf("rendering fixture template: %w", err)
	}
	return output.String(), nil
}

// InterpolateFixture returns a rendered copy of f without mutating the input.
func InterpolateFixture(f *Fixture, data any) (*Fixture, error) {
	if f == nil {
		return nil, fmt.Errorf("fixture is nil")
	}
	result := *f
	fields := []struct {
		name  string
		value *string
	}{
		{name: "type", value: &result.Type},
		{name: "description", value: &result.Description},
		{name: "xdr", value: &result.XDR},
	}
	for _, field := range fields {
		value, err := Interpolate(*field.value, data)
		if err != nil {
			return nil, fmt.Errorf("interpolating fixture %s: %w", field.name, err)
		}
		*field.value = value
	}
	if f.Captured != nil {
		captured := *f.Captured
		fields := []struct {
			name  string
			value *string
		}{
			{name: "network", value: &captured.Network},
			{name: "txHash", value: &captured.TxHash},
			{name: "at", value: &captured.At},
			{name: "endpoint", value: &captured.Endpoint},
		}
		for _, field := range fields {
			value, err := Interpolate(*field.value, data)
			if err != nil {
				return nil, fmt.Errorf("interpolating fixture captured.%s: %w", field.name, err)
			}
			*field.value = value
		}
		result.Captured = &captured
	}
	if len(f.Decoded) > 0 {
		var tree any
		if err := json.Unmarshal(f.Decoded, &tree); err != nil {
			return nil, fmt.Errorf("decoding fixture decoded section: %w", err)
		}
		tree, err := interpolateTree(tree, data)
		if err != nil {
			return nil, fmt.Errorf("interpolating fixture decoded section: %w", err)
		}
		result.Decoded, err = json.Marshal(tree)
		if err != nil {
			return nil, fmt.Errorf("encoding interpolated decoded section: %w", err)
		}
	}
	if err := Validate(&result); err != nil {
		return nil, fmt.Errorf("validating interpolated fixture: %w", err)
	}
	return &result, nil
}

func interpolateTree(value any, data any) (any, error) {
	switch typed := value.(type) {
	case string:
		return Interpolate(typed, data)
	case []any:
		for index := range typed {
			child, err := interpolateTree(typed[index], data)
			if err != nil {
				return nil, err
			}
			typed[index] = child
		}
		return typed, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			renderedKey, err := Interpolate(key, data)
			if err != nil {
				return nil, err
			}
			if _, exists := result[renderedKey]; exists {
				return nil, fmt.Errorf("interpolated object keys collide at %q", renderedKey)
			}
			renderedChild, err := interpolateTree(child, data)
			if err != nil {
				return nil, err
			}
			result[renderedKey] = renderedChild
		}
		return result, nil
	default:
		return value, nil
	}
}
