package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-logs/logparser"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// MockBuildkiteLogsClient for testing
type MockBuildkiteLogsClient struct {
	NewReaderFunc func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error)
}

func (m *MockBuildkiteLogsClient) NewReader(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
	if m.NewReaderFunc != nil {
		return m.NewReaderFunc(ctx, org, pipeline, build, job, ttl, forceRefresh)
	}
	return buildkitelogs.NewParquetReader("/tmp/test.parquet"), nil
}

var _ BuildkiteLogsClient = (*MockBuildkiteLogsClient)(nil)

func TestParseCacheTTL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
	}{
		{
			name:     "empty string",
			input:    "",
			expected: 30 * time.Second,
		},
		{
			name:     "valid duration",
			input:    "5m",
			expected: 5 * time.Minute,
		},
		{
			name:     "invalid duration",
			input:    "invalid",
			expected: 30 * time.Second,
		},
		{
			name:     "seconds",
			input:    "45s",
			expected: 45 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCacheTTL(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateSearchPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{
			name:    "valid pattern",
			pattern: "error",
			wantErr: false,
		},
		{
			name:    "valid regex",
			pattern: "ERROR.*failed",
			wantErr: false,
		},
		{
			name:    "invalid regex",
			pattern: "[",
			wantErr: true,
		},
		{
			name:    "empty pattern",
			pattern: "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSearchPattern(tt.pattern)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSearchLogsHandler(t *testing.T) {
	assert := require.New(t)

	mockClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
			assert.Equal("test-org", org)
			assert.Equal("test-pipeline", pipeline)
			assert.Equal("123", build)
			assert.Equal("job-456", job)
			return buildkitelogs.NewParquetReader("/tmp/test.parquet"), nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildkiteLogsClient: mockClient})

	_, handler, _ := SearchLogs()

	t.Run("invalid regex pattern", func(t *testing.T) {
		params := SearchLogsParams{
			JobLogsBaseParams: JobLogsBaseParams{
				OrgSlug:      "test-org",
				PipelineSlug: "test-pipeline",
				BuildNumber:  "123",
				JobID:        "job-456",
			},
			Pattern: "[", // Invalid regex
		}

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), params)
		assert.NoError(err)
		textContent := result.Content[0].(*mcp.TextContent)
		assert.Contains(textContent.Text, "invalid regex pattern")
	})

	t.Run("client error", func(t *testing.T) {
		errorClient := &MockBuildkiteLogsClient{
			NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
				return nil, errors.New("download failed")
			},
		}

		errorCtx := ContextWithDeps(context.Background(), ToolDependencies{BuildkiteLogsClient: errorClient})
		_, errorHandler, _ := SearchLogs()

		params := SearchLogsParams{
			JobLogsBaseParams: JobLogsBaseParams{
				OrgSlug:      "test-org",
				PipelineSlug: "test-pipeline",
				BuildNumber:  "123",
				JobID:        "job-456",
			},
			Pattern: "error",
		}

		result, _, err := errorHandler(errorCtx, createMCPRequest(t, map[string]any{}), params)
		assert.NoError(err)
		textContent := result.Content[0].(*mcp.TextContent)
		assert.Contains(textContent.Text, "failed to create log reader")
	})
}

// writeTestParquetFile creates a parquet log file with the given entries, in
// row order, for use in tests that exercise real search/read behavior.
func writeTestParquetFile(t *testing.T, filename string, contents []string) {
	t.Helper()

	f, err := os.Create(filename)
	require.NoError(t, err)
	defer f.Close()

	writer, err := buildkitelogs.NewParquetWriter(f)
	require.NoError(t, err)
	defer writer.Close()

	baseTime := time.Date(2025, 4, 22, 21, 43, 29, 0, time.UTC)
	entries := make([]*logparser.Entry, len(contents))
	for i, content := range contents {
		entries[i] = &logparser.Entry{
			Timestamp: baseTime.Add(time.Duration(i) * 100 * time.Millisecond),
			Content:   content,
			RawLine:   []byte(content),
		}
	}

	require.NoError(t, writer.WriteBatch(entries))
}

// TestSearchLogsHandler_SeekStart is a regression test for the seek_start
// parameter being silently dropped: SearchLogsParams.SeekStart was accepted
// by the tool schema but never copied into buildkitelogs.SearchOptions, so
// every search behaved as if seek_start had been omitted.
func TestSearchLogsHandler_SeekStart(t *testing.T) {
	assert := require.New(t)

	testFile := t.TempDir() + "/seek_start.parquet"
	writeTestParquetFile(t, testFile, []string{
		"setup phase started",          // row 0
		"installing dependencies",      // row 1
		"test phase started",           // row 2
		"running unit tests",           // row 3
		"test failed: assertion error", // row 4
		"cleanup phase started",        // row 5
	})

	mockClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
			return buildkitelogs.NewParquetReader(testFile), nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildkiteLogsClient: mockClient})
	_, handler, _ := SearchLogs()

	baseParams := JobLogsBaseParams{
		OrgSlug:      "test-org",
		PipelineSlug: "test-pipeline",
		BuildNumber:  "123",
		JobID:        "job-456",
	}

	search := func(seekStart int) []TerseSearchResult {
		params := SearchLogsParams{
			JobLogsBaseParams: baseParams,
			Pattern:           "test.*",
			Reverse:           true,
			SeekStart:         seekStart,
		}

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), params)
		assert.NoError(err)

		textContent := result.Content[0].(*mcp.TextContent)
		var resp struct {
			Results []TerseSearchResult `json:"results"`
		}
		assert.NoError(json.Unmarshal([]byte(textContent.Text), &resp))
		return resp.Results
	}

	// Without seek_start, a reverse search matches all three "test.*" rows.
	all := search(0)
	assert.Len(all, 3)
	assert.Equal(int64(4), all[0].Match.RN)

	// With seek_start: 3, the search should start at row 3 and go backwards,
	// so the match at row 4 (after the seek point) must be excluded.
	seeked := search(3)
	assert.Len(seeked, 2)
	assert.Equal(int64(3), seeked[0].Match.RN)
	assert.Equal(int64(2), seeked[1].Match.RN)
}

// TestSearchLogsHandler_TerseFormat is a regression test for search_logs
// returning the library's raw SearchResult (content/row_number/timestamp,
// plus undocumented flags/group fields) instead of the {ts,c,rn} format
// documented in debug-logs-guide.md and in the tool's own description.
func TestSearchLogsHandler_TerseFormat(t *testing.T) {
	assert := require.New(t)

	testFile := t.TempDir() + "/terse_format.parquet"
	writeTestParquetFile(t, testFile, []string{
		"setup phase started",          // row 0
		"test failed: assertion error", // row 1
	})

	mockClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
			return buildkitelogs.NewParquetReader(testFile), nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildkiteLogsClient: mockClient})
	_, handler, _ := SearchLogs()

	params := SearchLogsParams{
		JobLogsBaseParams: JobLogsBaseParams{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		},
		Pattern: "test failed",
	}

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), params)
	assert.NoError(err)
	text := result.Content[0].(*mcp.TextContent).Text

	// The documented fields must be present under their terse names.
	assert.Contains(text, `"rn":1`)
	assert.Contains(text, `"c":"test failed: assertion error"`)

	// The library's raw field names, and its extra undocumented fields,
	// must not leak through.
	for _, undocumented := range []string{"row_number", "content", "timestamp", "flags", "group"} {
		assert.NotContains(text, `"`+undocumented+`"`)
	}
}

// TestToTerseEntry_RowZero is a regression test for `rn` being tagged
// json:"rn,omitempty": since row 0 is Go's zero value for int64, omitempty
// silently dropped `rn` from the very first log entry of any file, breaking
// the documented {ts,c,rn} contract for exactly the entries most likely to
// matter (e.g. tail_logs/read_logs starting from the top of a short log).
func TestToTerseEntry_RowZero(t *testing.T) {
	entry := buildkitelogs.ParquetLogEntry{
		RowNumber: 0,
		Timestamp: 1700000000000,
		Content:   "first line",
		Flags:     1, // HasTimestamp
	}

	b, err := json.Marshal(toTerseEntry(entry))
	require.NoError(t, err)
	require.Contains(t, string(b), `"rn":0`)
}

func TestTailLogsHandler(t *testing.T) {
	assert := require.New(t)

	mockClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
			return buildkitelogs.NewParquetReader("/tmp/test.parquet"), nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildkiteLogsClient: mockClient})

	_, handler, _ := TailLogs()

	t.Run("default tail value", func(t *testing.T) {
		params := TailLogsParams{
			JobLogsBaseParams: JobLogsBaseParams{
				OrgSlug:      "test-org",
				PipelineSlug: "test-pipeline",
				BuildNumber:  "123",
				JobID:        "job-456",
			},
			Tail: 0, // Should default to 10
		}

		// This will fail due to the parquet file not existing, but we can check the parameters
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), params)
		assert.NoError(err)
		textContent := result.Content[0].(*mcp.TextContent)
		assert.Contains(textContent.Text, "Failed to get file info")
	})
}

func TestReadLogsHandler(t *testing.T) {
	assert := require.New(t)

	mockClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
			return buildkitelogs.NewParquetReader("/tmp/test.parquet"), nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildkiteLogsClient: mockClient})

	_, handler, _ := ReadLogs()

	params := ReadLogsParams{
		JobLogsBaseParams: JobLogsBaseParams{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		},
		Seek:  0,
		Limit: 100,
	}

	// This will fail due to the parquet file not existing, but we can test the flow
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), params)
	assert.NoError(err)
	textContent := result.Content[0].(*mcp.TextContent)
	assert.Contains(textContent.Text, "Failed to read entries")
}

func TestNewParquetReader(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	t.Run("successful creation", func(t *testing.T) {
		mockClient := &MockBuildkiteLogsClient{
			NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
				assert.Equal("test-org", org)
				assert.Equal("test-pipeline", pipeline)
				assert.Equal("123", build)
				assert.Equal("job-456", job)
				assert.Equal(5*time.Minute, ttl)
				assert.True(forceRefresh)
				return buildkitelogs.NewParquetReader("/tmp/test.parquet"), nil
			},
		}

		params := JobLogsBaseParams{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
			CacheTTL:     "5m",
			ForceRefresh: true,
		}

		// This will succeed in creating the reader but fail later when trying to read
		// the non-existent parquet file, but we can verify the client was called correctly
		reader, err := newParquetReader(ctx, mockClient, params)
		assert.NoError(err)   // Creation succeeds
		assert.NotNil(reader) // Reader is created
	})

	t.Run("client error", func(t *testing.T) {
		mockClient := &MockBuildkiteLogsClient{
			NewReaderFunc: func(ctx context.Context, org, pipeline, build, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
				return nil, errors.New("download failed")
			},
		}

		params := JobLogsBaseParams{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		}

		reader, err := newParquetReader(ctx, mockClient, params)
		assert.Error(err)
		assert.Nil(reader)
		assert.Contains(err.Error(), "failed to create log reader")
	})
}
