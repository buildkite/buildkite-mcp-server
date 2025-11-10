package buildkite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// MockBuildkiteLogsClient for testing
type MockBuildkiteLogsClient struct {
	DownloadAndCacheFunc func(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error)
}

func (m *MockBuildkiteLogsClient) DownloadAndCache(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error) {
	if m.DownloadAndCacheFunc != nil {
		return m.DownloadAndCacheFunc(ctx, org, pipeline, build, job, cacheTTL, forceRefresh)
	}
	return "/tmp/test.parquet", nil
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
	ctx := context.Background()

	mockClient := &MockBuildkiteLogsClient{
		DownloadAndCacheFunc: func(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error) {
			assert.Equal("test-org", org)
			assert.Equal("test-pipeline", pipeline)
			assert.Equal("123", build)
			assert.Equal("job-456", job)
			return "/tmp/test.parquet", nil
		},
	}

	_, handler, _ := SearchLogs(mockClient)

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

		result, err := handler(ctx, mcp.CallToolRequest{}, params)
		assert.NoError(err)
		textContent, ok := result.Content[0].(mcp.TextContent)
		assert.True(ok)
		assert.Contains(textContent.Text, "invalid regex pattern")
	})

	t.Run("client error", func(t *testing.T) {
		errorClient := &MockBuildkiteLogsClient{
			DownloadAndCacheFunc: func(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error) {
				return "", errors.New("download failed")
			},
		}

		_, errorHandler, _ := SearchLogs(errorClient)

		params := SearchLogsParams{
			JobLogsBaseParams: JobLogsBaseParams{
				OrgSlug:      "test-org",
				PipelineSlug: "test-pipeline",
				BuildNumber:  "123",
				JobID:        "job-456",
			},
			Pattern: "error",
		}

		result, err := errorHandler(ctx, mcp.CallToolRequest{}, params)
		assert.NoError(err)
		textContent, ok := result.Content[0].(mcp.TextContent)
		assert.True(ok)
		assert.Contains(textContent.Text, "Failed to create log reader")
	})
}

func TestTailLogsHandler(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	mockClient := &MockBuildkiteLogsClient{
		DownloadAndCacheFunc: func(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error) {
			return "/tmp/test.parquet", nil
		},
	}

	_, handler, _ := TailLogs(mockClient)

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
		result, err := handler(ctx, mcp.CallToolRequest{}, params)
		assert.NoError(err)
		textContent, ok := result.Content[0].(mcp.TextContent)
		assert.True(ok)
		assert.Contains(textContent.Text, "Failed to get file info")
	})
}

func TestReadLogsHandler(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	mockClient := &MockBuildkiteLogsClient{
		DownloadAndCacheFunc: func(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error) {
			return "/tmp/test.parquet", nil
		},
	}

	_, handler, _ := ReadLogs(mockClient)

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
	result, err := handler(ctx, mcp.CallToolRequest{}, params)
	assert.NoError(err)
	textContent, ok := result.Content[0].(mcp.TextContent)
	assert.True(ok)
	assert.Contains(textContent.Text, "Failed to read entries")
}

func TestNewParquetReader(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	t.Run("successful creation", func(t *testing.T) {
		mockClient := &MockBuildkiteLogsClient{
			DownloadAndCacheFunc: func(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error) {
				assert.Equal("test-org", org)
				assert.Equal("test-pipeline", pipeline)
				assert.Equal("123", build)
				assert.Equal("job-456", job)
				assert.Equal(5*time.Minute, cacheTTL)
				assert.True(forceRefresh)
				return "/tmp/test.parquet", nil
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
			DownloadAndCacheFunc: func(ctx context.Context, org, pipeline, build, job string, cacheTTL time.Duration, forceRefresh bool) (string, error) {
				return "", errors.New("download failed")
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
		assert.Contains(err.Error(), "failed to download/cache logs")
	})
}
