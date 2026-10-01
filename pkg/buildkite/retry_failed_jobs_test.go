package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyRetry(t *testing.T) {
	tests := []struct {
		name   string
		job    buildkite.Job
		retry  bool
		reason string
	}{
		{"expired", buildkite.Job{Type: "script", State: "expired"}, true, "expired"},
		{"agent lost", buildkite.Job{Type: "script", State: "failed", ExitStatus: intPtr(-1)}, true, "agent_lost"},
		{"agent lost while timing out", buildkite.Job{Type: "script", State: "timed_out", ExitStatus: intPtr(-1)}, true, "agent_lost"},
		{"agent stop", buildkite.Job{Type: "script", State: "failed", ExitStatus: intPtr(255), SignalReason: "agent_stop"}, true, "agent_stop"},
		{"agent refused", buildkite.Job{Type: "script", State: "failed", SignalReason: "agent_refused"}, true, "agent_refused"},
		{"stack error", buildkite.Job{Type: "script", State: "failed", SignalReason: "stack_error"}, true, "stack_error"},
		{"command failure", buildkite.Job{Type: "script", State: "failed", ExitStatus: intPtr(1)}, false, "command_failed"},
		{"soft failure from infrastructure", buildkite.Job{Type: "script", State: "failed", ExitStatus: intPtr(-1), SoftFailed: true}, false, "soft_failed"},
		{"canceled", buildkite.Job{Type: "script", State: "canceled", SignalReason: "cancel"}, false, "canceled"},
		{"timed out", buildkite.Job{Type: "script", State: "timed_out", SignalReason: "cancel"}, false, "timed_out"},
		{"signature rejected", buildkite.Job{Type: "script", State: "failed", SignalReason: "signature_rejected"}, false, "signature_rejected"},
		{"promised failure still running", buildkite.Job{Type: "script", State: "running", PromisedExitStatus: intPtr(1)}, false, "not_finished"},
		{"trigger job", buildkite.Job{Type: "trigger", State: "failed"}, false, "not_command_job"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retry, reason := classifyRetry(tt.job)
			assert.Equal(t, tt.retry, retry)
			assert.Equal(t, tt.reason, reason)
		})
	}
}

func TestRetryFailedJobs(t *testing.T) {
	args := RetryFailedJobsArgs{OrgSlug: "test-org", PipelineSlug: "test-pipeline", BuildNumber: "123"}
	failedJobs := []buildkite.Job{
		{ID: "lost", Name: "tests", StepKey: "tests", Type: "script", State: "failed", ExitStatus: intPtr(-1)},
		{ID: "real", Name: "lint", Type: "script", State: "failed", ExitStatus: intPtr(1)},
		{ID: "stopped", Type: "script", State: "failed", ExitStatus: intPtr(255), SignalReason: "agent_stop"},
		{ID: "allowed", Type: "script", State: "failed", ExitStatus: intPtr(1), SoftFailed: true},
	}
	listJobs := func(t *testing.T) func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
		return func(_ context.Context, org, pipeline, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			assert.Equal(t, "test-org", org)
			assert.Equal(t, "test-pipeline", pipeline)
			assert.Equal(t, "123", buildNumber)
			assert.Equal(t, []string{"failed", "timed_out", "expired", "canceled"}, opt.State)
			require.NotNil(t, opt.IncludeRetriedJobs)
			assert.False(t, *opt.IncludeRetriedJobs)
			return buildkite.JobsList{Items: failedJobs}, &buildkite.Response{}, nil
		}
	}
	decode := func(t *testing.T, result *mcp.CallToolResult) RetryFailedJobsResult {
		t.Helper()
		require.False(t, result.IsError, result.Content[0].(*mcp.TextContent).Text)
		var decoded RetryFailedJobsResult
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &decoded))
		return decoded
	}
	ids := func(items []RetryFailedJobsItem) []string {
		out := make([]string, len(items))
		for i, item := range items {
			out[i] = item.ID + ":" + item.Reason
		}
		return out
	}

	t.Run("ToolDefinition", func(t *testing.T) {
		tool, _, scopes := RetryFailedJobs()
		assert.Equal(t, "retry_failed_jobs", tool.Name)
		assert.Equal(t, boolPtr(true), tool.Annotations.DestructiveHint)
		assert.Equal(t, []string{"write_builds"}, scopes)
	})

	t.Run("RetriesOnlySafeJobs", func(t *testing.T) {
		var retriedIDs []string
		mockJobs := &MockJobsClient{
			ListByBuildFunc: listJobs(t),
			RetryJobFunc: func(_ context.Context, org, pipeline, buildNumber, jobID string) (buildkite.Job, *buildkite.Response, error) {
				retriedIDs = append(retriedIDs, jobID)
				if jobID == "stopped" {
					return buildkite.Job{}, nil, errors.New("manual retries are not allowed")
				}
				return buildkite.Job{ID: jobID + "-retry"}, &buildkite.Response{}, nil
			},
		}
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryFailedJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), args)
		require.NoError(t, err)
		decoded := decode(t, result)

		assert.Equal(t, []string{"lost", "stopped"}, retriedIDs)
		assert.False(t, decoded.DryRun)
		assert.Equal(t, []string{"lost:agent_lost"}, ids(decoded.Retried))
		assert.Equal(t, "lost-retry", decoded.Retried[0].RetryJobID)
		assert.Equal(t, "tests", decoded.Retried[0].StepKey)
		assert.Equal(t, []string{"real:command_failed", "allowed:soft_failed"}, ids(decoded.Skipped))
		assert.Equal(t, []string{"stopped:agent_stop"}, ids(decoded.Errors))
		assert.Equal(t, "manual retries are not allowed", decoded.Errors[0].Error)
	})

	t.Run("DryRunDoesNotRetry", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			ListByBuildFunc: listJobs(t),
			RetryJobFunc: func(context.Context, string, string, string, string) (buildkite.Job, *buildkite.Response, error) {
				t.Fatal("dry run must not retry jobs")
				return buildkite.Job{}, nil, nil
			},
		}
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryFailedJobs()

		dryRunArgs := args
		dryRunArgs.DryRun = true
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), dryRunArgs)
		require.NoError(t, err)
		decoded := decode(t, result)

		assert.True(t, decoded.DryRun)
		assert.Equal(t, []string{"lost:agent_lost", "stopped:agent_stop"}, ids(decoded.Retried))
		assert.Empty(t, decoded.Retried[0].RetryJobID)
		assert.Equal(t, []string{"real:command_failed", "allowed:soft_failed"}, ids(decoded.Skipped))
	})

	t.Run("FollowsPagination", func(t *testing.T) {
		var afters []string
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(_ context.Context, _, _, _ string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				afters = append(afters, opt.After)
				assert.Equal(t, []string{"failed", "timed_out", "expired", "canceled"}, opt.State)
				if opt.After == "" {
					return buildkite.JobsList{
						Items: []buildkite.Job{{ID: "first", Type: "script", State: "expired"}},
						Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/v2/organizations/test-org/pipelines/test-pipeline/builds/123/jobs?after=cursor-2"},
					}, &buildkite.Response{}, nil
				}
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "second", Type: "script", State: "expired"}}}, &buildkite.Response{}, nil
			},
		}
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryFailedJobs()

		dryRunArgs := args
		dryRunArgs.DryRun = true
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), dryRunArgs)
		require.NoError(t, err)

		assert.Equal(t, []string{"", "cursor-2"}, afters)
		assert.Equal(t, []string{"first:expired", "second:expired"}, ids(decode(t, result).Retried))
	})

	t.Run("RefusesWhenScanLimitExceeded", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{
					Items: []buildkite.Job{{ID: "lost", Type: "script", State: "failed", ExitStatus: intPtr(-1)}},
					Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/v2/organizations/test-org/pipelines/test-pipeline/builds/123/jobs?after=more"},
				}, &buildkite.Response{}, nil
			},
			RetryJobFunc: func(context.Context, string, string, string, string) (buildkite.Job, *buildkite.Response, error) {
				t.Fatal("must not retry when the job list is incomplete")
				return buildkite.Job{}, nil, nil
			},
		}
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryFailedJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), args)
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "no jobs were retried")
	})

	t.Run("RetryUnauthorizedStops", func(t *testing.T) {
		var retriedIDs []string
		mockJobs := &MockJobsClient{
			ListByBuildFunc: listJobs(t),
			RetryJobFunc: func(_ context.Context, _, _, _, jobID string) (buildkite.Job, *buildkite.Response, error) {
				retriedIDs = append(retriedIDs, jobID)
				return buildkite.Job{}, nil, &buildkite.ErrorResponse{Response: &http.Response{StatusCode: http.StatusUnauthorized}}
			},
		}
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryFailedJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), args)
		require.ErrorIs(t, err, ErrUnauthorized)
		assert.Nil(t, result)
		assert.Equal(t, []string{"lost"}, retriedIDs)
	})

	t.Run("ListError", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{}, nil, errors.New("build not found")
			},
		}
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := RetryFailedJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), args)
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "build not found")
	})
}
