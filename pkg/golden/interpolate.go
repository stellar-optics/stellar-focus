package golden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"text/template"
	"time"
)

// toInt converts various numeric types or numeric strings to int for template arithmetic.
func toInt(v any) (int, error) {
	switch val := v.(type) {
	case int:
		return val,
			nil
	case int64:
		return int(val),
			nil
	case int32:
		return int(val),
			nil
	case uint:
		return int(val),
			nil
	case uint64:
		return int(val),
			nil
	case uint32:
		return int(val),
			nil
	case float64:
		return int(val),
			nil
	case float32:
		return int(val),
			nil
	default:
		vVal := reflect.ValueOf(v)
		switch vVal.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return int(vVal.Int()), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return int(vVal.Uint()), nil
		case reflect.Float32, reflect.Float64:
			return int(vVal.Float()), nil
		}
		return 0, fmt.Errorf("cannot convert %T (%v) to int", v, v)
	}
}

// add adds two numbers.
func add(a, b any) (int, error) {
	aInt, err := toInt(a)
	if err != nil {
		return 0, fmt.Errorf("add: first argument: %w", err)
	}
	bInt, err := toInt(b)
	if err != nil {
		return 0, fmt.Errorf("add: second argument: %w", err)
	}
	return aInt + bInt,
		nil
}

// sub subtracts b from a.
func sub(a, b any) (int, error) {
	aInt, err := toInt(a)
	if err != nil {
		return 0, fmt.Errorf("sub: first argument: %w", err)
	}
	bInt, err := toInt(b)
	if err != nil {
		return 0, fmt.Errorf("sub: second argument: %w", err)
	}
	return aInt - bInt,
		nil
}

// timeOffset parses a duration string (or takes a time.Duration) and offsets the base time or current time.
func timeOffset(args ...any) (string, error) {
	var durationStr string
	var base time.Time = time.Unix(0, 0).UTC() // Deterministic base time if not provided

	if len(args) == 1 {
		switch v := args[0].(type) {
		case string:
			durationStr = v
		case time.Duration:
			return base.Add(v).Format(time.RFC3339),
			nil
		default:
			arrayVal, err := toInt(v)
			if err == nil {
				durationStr = fmt.Sprintf("%ds", arrayVal)
			} else {
				return "", fmt.Errorf("invalid timeOffset argument %T", args[0])
			}
		}
	} else if len(args) >= 2 {
		switch v := args[0].(type) {
		case time.Time:
			base = v
		case string:
			t, err := time.Parse(time.RFC3339, v)
			if err == nil {
				base = t
			} else {
				durationStr = v
			}
		}
		switch v := args[1].(type) {
		case string:
			durationStr = v
		case time.Duration:
			return base.Add(v).Format(time.RFC3339),
			nil
		}
	}

	if durationStr == "" {
		return base.Format(time.RFC3339), nil
	}

	d, err := time.ParseDuration(durationStr)
	if err != nil {
		return "", fmt.Errorf("invalid duration %q: %w", durationStr, err)
	}
	return base.Add(d).Format(time.RFC3339),
		nil
}

// accountID generates a mock account ID or returns a indexed one if variables provide it.
func accountID(args ...any) string {
	idx := 0
	if len(args) > 0 {
		if n, err := toInt(args[0]); err == nil {
			idx = n
		}
	}
	// Generate a deterministic Stellar public key-like string based on index
	return fmt.Sprintf("GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF%04d", idx)
}

// templateFuncs provides the expanded helper function map for golden file interpolation.
var templateFuncs = template.FuncMap{
	"add":        add,
	"sub":        sub,
	"timeOffset": timeOffset,
	"AccountID":  accountID,
}

// Interpolate applies Go template syntax to text using data as its context.
func Interpolate(text string, data any) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	parsed, err := template.New("golden").Funcs(templateFuncs).Option("missingkey=error").Parse(text)
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
