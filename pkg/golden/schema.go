package golden

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

//go:embed fixture.schema.json
var fixtureSchemaJSON []byte

type schemaNode struct {
	Type                 string                `json:"type"`
	Required             []string              `json:"required"`
	Properties           map[string]schemaNode `json:"properties"`
	AdditionalProperties *bool                 `json:"additionalProperties"`
	MinLength            int                   `json:"minLength"`
	Format               string                `json:"format"`
	Minimum              *json.Number          `json:"minimum"`
	Maximum              *json.Number          `json:"maximum"`
}

// Validate checks a fixture against the package's embedded JSON Schema.
func Validate(f *Fixture) error {
	return ValidateFixtureSchema(f, false)
}

// ValidateFixtureSchema validates raw or struct fixture data against the embedded
// fixture.schema.json, supporting strict mode where unknown fields are disallowed
// and required top-level attributes (version, type, payload/xdr, metadata, etc.) are verified.
func ValidateFixtureSchema(input any, strict bool) error {
	var data []byte
	var err error
	switch v := input.(type) {
	case []byte:
		data = v
	case *Fixture:
		if v == nil {
			return fmt.Errorf("fixture is nil")
		}
		if v.Format != "" && v.Format != FormatVersion {
			return fmt.Errorf("fixture was written in format %q, but this version reads %q", v.Format, FormatVersion)
		}
		data, err = json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encoding fixture for schema validation: %w", err)
		}
	case Fixture:
		if v.Format != "" && v.Format != FormatVersion {
			return fmt.Errorf("fixture was written in format %q, but this version reads %q", v.Format, FormatVersion)
		}
		data, err = json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encoding fixture for schema validation: %w", err)
		}
	default:
		data, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encoding input for schema validation: %w", err)
		}
	}

	var schema schemaNode
	if err := json.Unmarshal(fixtureSchemaJSON, &schema);
		err != nil {
			return fmt.Errorf("loading embedded fixture schema: %w", err)
		}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid JSON: multiple values")
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}

	return validateSchemaNode(value, schema, "fixture", strict)
}

func validateSchemaNode(value any, schema schemaNode, path string, strict bool) error {
	if !matchesSchemaType(value, schema.Type) {
		return fmt.Errorf("%s must be %s", path, schema.Type)
	}
	if schema.Type == "object" {
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}

		// Validate required properties
		for _, key := range schema.Required {
			if _, exists := object[key]; !exists {
				if path == "fixture" && key == "xdr" {
					return fmt.Errorf("fixture has no xdr field")
				}
				return fmt.Errorf("%s.%s is required", path, key)
			}
		}

		// Enforce strict unknown field check if requested or specified by schema
		disallowAdditional := strict
		if schema.AdditionalProperties != nil && !*schema.AdditionalProperties {
			disallowAdditional = true
		}

		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			property, exists := schema.Properties[key]
			if !exists {
				if disallowAdditional {
					return fmt.Errorf("%s.%s is not allowed by the fixture schema", path, key)
				}
				continue
			}
			if err := validateSchemaNode(object[key], property, path+"."+key, strict); err != nil {
				return err
			}
		}
	}
	if text, ok := value.(string); ok {
		if schema.MinLength > 0 && len([]rune(text)) < schema.MinLength {
			if path == "fixture.xdr" {
				return fmt.Errorf("fixture has no xdr field")
			}
			return fmt.Errorf("%s must not be empty", path)
		}
		if schema.Format == "date-time" && text != "" {
			if _, err := time.Parse(time.RFC3339, text); err != nil {
				return fmt.Errorf("%s must be an RFC3339 date-time", path)
			}
		}
	}
	if schema.Type == "integer" {
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be an integer", path)
		}
		integer, err := number.Int64()
		if err != nil {
			return fmt.Errorf("%s must be an integer", path)
		}
		if schema.Minimum != nil {
			minimum, err := schema.Minimum.Int64()
			if err != nil {
				return fmt.Errorf("loading fixture schema minimum for %s: %w", path, err)
			}
			if integer < minimum {
				return fmt.Errorf("%s must be at least %s", path, *schema.Minimum)
			}
		}
		if schema.Maximum != nil {
			maximum, err := schema.Maximum.Int64()
			if err != nil {
				return fmt.Errorf("loading fixture schema maximum for %s: %w", path, err)
			}
			if integer > maximum {
				return fmt.Errorf("%s must be at most %s", path, *schema.Maximum)
			}
		}
	}
	return nil
}

func matchesSchemaType(value any, schemaType string) bool {
	switch schemaType {
	case "":
		return true
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "number":
		_, ok := value.(json.Number)
		return ok
	default:
		return false
	}
}
