package buildkite

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockJobsClient for testing unblock functionality
type MockJobsClient struct {
	UnblockJobFunc func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
}

func (m *MockJobsClient) UnblockJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error) {
	if m.UnblockJobFunc != nil {
		return m.UnblockJobFunc(ctx, org, pipeline, buildNumber, jobID, opt)
	}
	return buildkite.Job{}, &buildkite.Response{}, nil
}

func TestUnblockJob(t *testing.T) {
	// Test tool definition
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, _, _ := UnblockJob()
		assert.Equal(t, "unblock_job", tool.Name)
		assert.Contains(t, tool.Description, "Unblock a blocked job")
	})

	// Test successful unblock
	t.Run("SuccessfulUnblock", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			UnblockJobFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error) {
				assert.Equal(t, "test-org", org)
				assert.Equal(t, "test-pipeline", pipeline)
				assert.Equal(t, "123", buildNumber)
				assert.Equal(t, "job-123", jobID)

				return buildkite.Job{
						ID:    jobID,
						State: "unblocked",
					}, &buildkite.Response{
						Response: &http.Response{
							StatusCode: 200,
						},
					}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})

		_, handler, _ := UnblockJob()

		req := createMCPRequest(t, map[string]any{})
		args := UnblockJobArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-123",
		}

		result, _, err := handler(ctx, req, args)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"id":"job-123"`)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"state":"unblocked"`)
	})

	// Test with fields
	t.Run("UnblockWithFields", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			UnblockJobFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error) {
				// Verify fields were passed correctly
				require.NotNil(t, opt)
				assert.Equal(t, "v1.0.0", opt.Fields["version"])
				assert.Equal(t, "prod", opt.Fields["environment"])

				return buildkite.Job{
						ID:    jobID,
						State: "unblocked",
					}, &buildkite.Response{
						Response: &http.Response{
							StatusCode: 200,
						},
					}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})

		_, handler, _ := UnblockJob()

		req := createMCPRequest(t, map[string]any{})
		args := UnblockJobArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-123",
			Fields:       map[string]string{"version": "v1.0.0", "environment": "prod"},
		}

		result, _, err := handler(ctx, req, args)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	// Test client error
	t.Run("ClientError", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			UnblockJobFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error) {
				return buildkite.Job{}, nil, errors.New("API connection failed")
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})

		_, handler, _ := UnblockJob()

		req := createMCPRequest(t, map[string]any{})
		args := UnblockJobArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-123",
		}

		result, _, err := handler(ctx, req, args)
		require.NoError(t, err)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "API connection failed")
	})

}
