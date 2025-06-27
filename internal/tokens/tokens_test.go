package tokens

import (
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "empty string",
			input:    "",
			expected: 0,
		},
		{
			name:     "single short word",
			input:    "test",
			expected: 1,
		},
		{
			name:     "multiple short words",
			input:    "this is a test",
			expected: 4,
		},
		{
			name:     "medium length words",
			input:    "medium length",
			expected: 4, // "medium" (6 chars) = 2, "length" (6 chars) = 2
		},
		{
			name:     "long words",
			input:    "internationalization standardization",
			expected: 9, // "internationalization" (20 chars) = 5, "standardization" (14 chars) = 4
		},
		{
			name:     "mixed length words",
			input:    "this is a complicated test with internationalization",
			expected: 13, // 1+1+1+3+1+1+5 = 13
		},
		{
			name:     "multiple spaces",
			input:    "this   is  a   test",
			expected: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateTokens(tt.input)
			if result != tt.expected {
				t.Errorf("EstimateTokens(%q) = %d, expected %d", tt.input, result, tt.expected)
			}
		})
	}
}
