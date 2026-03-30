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

// MockJobsClient for testing job functionality
type MockJobsClient struct {
	UnblockJobFunc                 func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
	RetryJobFunc                   func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error)
	GetJobEnvironmentVariablesFunc func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.JobEnvs, *buildkite.Response, error)
}

func (m *MockJobsClient) UnblockJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error) {
	if m.UnblockJobFunc != nil {
		return m.UnblockJobFunc(ctx, org, pipeline, buildNumber, jobID, opt)
	}
	return buildkite.Job{}, &buildkite.Response{}, nil
}

func (m *MockJobsClient) RetryJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error) {
	if m.RetryJobFunc != nil {
		return m.RetryJobFunc(ctx, org, pipeline, buildNumber, jobID)
	}
	return buildkite.Job{}, &buildkite.Response{}, nil
}

func (m *MockJobsClient) GetJobEnvironmentVariables(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.JobEnvs, *buildkite.Response, error) {
	if m.GetJobEnvironmentVariablesFunc != nil {
		return m.GetJobEnvironmentVariablesFunc(ctx, org, pipeline, buildNumber, jobID)
	}
	return buildkite.JobEnvs{}, &buildkite.Response{}, nil
}

var _ JobsClient = (*MockJobsClient)(nil)

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

func TestRetryJob(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, _, _ := RetryJob()
		assert.Equal(t, "retry_job", tool.Name)
		assert.Contains(t, tool.Description, "Retry")
	})

	t.Run("Success", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			RetryJobFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				assert.Equal(t, "test-org", org)
				assert.Equal(t, "test-pipeline", pipeline)
				assert.Equal(t, "123", buildNumber)
				assert.Equal(t, "job-456", jobID)

				return buildkite.Job{
						ID:    "job-789",
						State: "scheduled",
					}, &buildkite.Response{
						Response: &http.Response{StatusCode: 200},
					}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), RetryJobArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"id":"job-789"`)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"state":"scheduled"`)
	})

	t.Run("Error", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			RetryJobFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				return buildkite.Job{}, nil, errors.New("job not retryable")
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), RetryJobArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		})
		require.NoError(t, err)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "job not retryable")
	})
}

func TestGetJobEnvironmentVariables(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, _, _ := GetJobEnvironmentVariables()
		assert.Equal(t, "get_job_env", tool.Name)
		assert.Contains(t, tool.Description, "environment variables")
	})

	t.Run("Success", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			GetJobEnvironmentVariablesFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.JobEnvs, *buildkite.Response, error) {
				assert.Equal(t, "test-org", org)
				assert.Equal(t, "test-pipeline", pipeline)
				assert.Equal(t, "123", buildNumber)
				assert.Equal(t, "job-456", jobID)

				return buildkite.JobEnvs{
						EnvironmentVariables: map[string]string{
							"BUILDKITE_BRANCH": "main",
							"CI":               "true",
						},
					}, &buildkite.Response{
						Response: &http.Response{StatusCode: 200},
					}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := GetJobEnvironmentVariables()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobEnvironmentVariablesArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"BUILDKITE_BRANCH":"main"`)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"CI":"true"`)
	})

	t.Run("Error", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			GetJobEnvironmentVariablesFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.JobEnvs, *buildkite.Response, error) {
				return buildkite.JobEnvs{}, nil, errors.New("access denied")
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := GetJobEnvironmentVariables()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobEnvironmentVariablesArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		})
		require.NoError(t, err)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "access denied")
	})
}
