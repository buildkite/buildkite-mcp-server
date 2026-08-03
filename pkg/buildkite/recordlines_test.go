package buildkite

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatJSONRecordsPerLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "array of objects gets one record per line",
			input: `{"items":[{"id":1},{"id":2},{"id":3}]}`,
			want:  "{\"items\":[\n{\"id\":1},\n{\"id\":2},\n{\"id\":3}\n]}",
		},
		{
			name:  "nested record arrays each break independently",
			input: `{"jobs":[{"name":"a","log_tail":[{"c":"x"},{"c":"y"}]},{"name":"b"}]}`,
			want:  "{\"jobs\":[\n{\"name\":\"a\",\"log_tail\":[\n{\"c\":\"x\"},\n{\"c\":\"y\"}\n]},\n{\"name\":\"b\"}\n]}",
		},
		{
			name:  "scalar arrays stay inline",
			input: `{"tags":["a","b","c"],"nums":[1,2,3]}`,
			want:  `{"tags":["a","b","c"],"nums":[1,2,3]}`,
		},
		{
			name:  "commas inside inline scalar arrays nested in records do not break",
			input: `{"items":[{"backtrace":["l1","l2"]},{"backtrace":[]}]}`,
			want:  "{\"items\":[\n{\"backtrace\":[\"l1\",\"l2\"]},\n{\"backtrace\":[]}\n]}",
		},
		{
			name:  "structural characters inside strings are ignored",
			input: `{"items":[{"body":"<ul>[{,]}\"</ul>"},{"body":"a\\\\"}]}`,
			want:  "{\"items\":[\n{\"body\":\"<ul>[{,]}\\\"</ul>\"},\n{\"body\":\"a\\\\\\\\\"}\n]}",
		},
		{
			name:  "top level array of objects",
			input: `[{"id":1},{"id":2}]`,
			want:  "[\n{\"id\":1},\n{\"id\":2}\n]",
		},
		{
			name:  "empty containers unchanged",
			input: `{"items":[],"meta":{}}`,
			want:  `{"items":[],"meta":{}}`,
		},
		{
			name:  "single object element still breaks",
			input: `{"items":[{"id":1}]}`,
			want:  "{\"items\":[\n{\"id\":1}\n]}",
		},
		{
			name:  "array of arrays is not a record array",
			input: `{"grid":[[1,2],[3,4]]}`,
			want:  `{"grid":[[1,2],[3,4]]}`,
		},
		{
			name:  "bare string result unchanged",
			input: `"Cluster queue dispatch resumed successfully"`,
			want:  `"Cluster queue dispatch resumed successfully"`,
		},
		{
			name:  "null unchanged",
			input: `null`,
			want:  `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatJSONRecordsPerLine([]byte(tt.input))
			require.Equal(t, tt.want, string(got))

			// Output must be the same JSON document.
			if tt.input != "null" {
				require.JSONEq(t, tt.input, string(got))
			}

			// Output length = input length + inserted newlines.
			require.Len(t, got, len(tt.input)+strings.Count(string(got), "\n"))

			// Idempotent: a second pass changes nothing.
			require.Equal(t, string(got), string(formatJSONRecordsPerLine(got)))
		})
	}
}

func TestFormatJSONRecordsPerLineRoundTripsRealPayload(t *testing.T) {
	// A shape representative of tool results: paginated items with nested
	// record arrays and content that embeds structural characters.
	payload := PaginatedResult[map[string]any]{
		Headers: map[string]string{"Link": "<https://api.buildkite.com/v2?page=2>; rel=\"next\""},
		Items: []map[string]any{
			{"name": ":yarnpkg: Yarn Audit", "soft_failed": true, "body_html": `<details>{"a":[1,2]}</details>`},
			{"name": ":rspec: Run", "log_tail": []map[string]any{{"c": "expected \"x\", got \"y\""}}},
		},
	}
	compact, err := json.Marshal(payload)
	require.NoError(t, err)

	formatted := formatJSONRecordsPerLine(compact)

	require.JSONEq(t, string(compact), string(formatted))
	require.True(t, bytes.Contains(formatted, []byte("},\n{")),
		"each item should start its own line")

	var roundTrip PaginatedResult[map[string]any]
	require.NoError(t, json.Unmarshal(formatted, &roundTrip))
	require.Equal(t, payload.Headers, roundTrip.Headers)
}
