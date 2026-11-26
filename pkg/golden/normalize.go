package golden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func normalizeDecoded(raw json.RawMessage, normalizers []NormalizerFunc) (json.RawMessage, error) {
	if len(normalizers) == 0 || len(raw) == 0 {
		return raw, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	value = normalizeNode("", value, normalizers)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func normalizeNode(path string, value any, normalizers []NormalizerFunc) any {
	for _, normalize := range normalizers {
		value = normalize(path, value)
	}
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			node[key] = normalizeNode(joinNormalizePath(path, key), child, normalizers)
		}
	case []any:
		for i, child := range node {
			node[i] = normalizeNode(fmt.Sprintf("%s[%d]", path, i), child, normalizers)
		}
	}
	return value
}

func joinNormalizePath(path, segment string) string {
	if path == "" {
		return segment
	}
	return strings.Join([]string{path, segment}, ".")
}
