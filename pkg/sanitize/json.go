package sanitize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// SanitizeJSONBytes sanitizes JSON string values and re-marshals the result.
// Named plainTextFields use SanitizePlainText to preserve unrendered content;
// other strings use Sanitize. Keys, numbers, booleans, and nulls pass through unchanged.
func SanitizeJSONBytes(data []byte, plainTextFields ...string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode JSON for sanitization: %w", err)
	}

	sanitized := sanitizeValue(raw, plainTextFields)

	result, err := json.Marshal(sanitized)
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal sanitized JSON: %w", err)
	}

	return result, nil
}

// sanitizeValue recursively walks and sanitizes a JSON value.
func sanitizeValue(v any, plainTextFields []string) any {
	switch val := v.(type) {
	case string:
		return Sanitize(val)
	case map[string]any:
		for k, v := range val {
			if text, ok := v.(string); ok && slices.Contains(plainTextFields, k) {
				val[k] = SanitizePlainText(text)
				continue
			}
			val[k] = sanitizeValue(v, plainTextFields)
		}
		return val
	case []any:
		for i, v := range val {
			val[i] = sanitizeValue(v, plainTextFields)
		}
		return val
	default:
		// json.Number, bool, nil — pass through unchanged
		return v
	}
}
