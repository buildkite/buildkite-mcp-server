package sanitize

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SanitizeJSONBytes unmarshals JSON data, recursively applies Sanitize() to all
// string values, and re-marshals the result. JSON keys are not sanitized as they
// are structural. Numbers, booleans, and nulls pass through unchanged.
func SanitizeJSONBytes(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode JSON for sanitization: %w", err)
	}

	sanitized := sanitizeValue(raw)

	result, err := json.Marshal(sanitized)
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal sanitized JSON: %w", err)
	}

	return result, nil
}

// sanitizeValue recursively walks a JSON value and applies Sanitize() to strings.
func sanitizeValue(v any) any {
	switch val := v.(type) {
	case string:
		return Sanitize(val)
	case map[string]any:
		for k, v := range val {
			val[k] = sanitizeValue(v)
		}
		return val
	case []any:
		for i, v := range val {
			val[i] = sanitizeValue(v)
		}
		return val
	default:
		// json.Number, bool, nil — pass through unchanged
		return v
	}
}
