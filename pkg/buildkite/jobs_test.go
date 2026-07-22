package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockJobsClient for testing job functionality
type MockJobsClient struct {
	ListByBuildFunc                func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error)
	GetJobFunc                     func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error)
	GetJobByOrgFunc                func(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error)
	UnblockJobFunc                 func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
	RetryJobFunc                   func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error)
	GetJobEnvironmentVariablesFunc func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.JobEnvs, *buildkite.Response, error)
}

func (m *MockJobsClient) ListByBuild(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
	if m.ListByBuildFunc != nil {
		return m.ListByBuildFunc(ctx, org, pipeline, buildNumber, opt)
	}
	return buildkite.JobsList{}, &buildkite.Response{}, nil
}

func (m *MockJobsClient) GetJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error) {
	if m.GetJobFunc != nil {
		return m.GetJobFunc(ctx, org, pipeline, buildNumber, jobID)
	}
	return buildkite.Job{}, &buildkite.Response{}, nil
}

func (m *MockJobsClient) GetJobByOrg(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error) {
	if m.GetJobByOrgFunc != nil {
		return m.GetJobByOrgFunc(ctx, org, jobID)
	}
	return buildkite.Job{}, &buildkite.Response{}, nil
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

func testJobAgent() buildkite.Agent {
	return buildkite.Agent{
		ID:       "agent-1",
		Name:     "agent-name",
		Hostname: "agent-host",
		Version:  "3.99.0",
	}
}

func intPtr(value int) *int {
	return &value
}

func TestUnblockJob(t *testing.T) {
	// Test tool definition
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, _, _ := UnblockJob()
		assert.Equal(t, "unblock_job", tool.Name)
		assert.Contains(t, tool.Description, "Unblock a blocked job")
		assert.Equal(t, boolPtr(true), tool.Annotations.DestructiveHint)
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
		assert.Equal(t, boolPtr(true), tool.Annotations.DestructiveHint)
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

func TestListJobs(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, handler, scopes := ListJobs()
		assert.Equal(t, "list_jobs", tool.Name)
		assert.Contains(t, tool.Description, "List jobs")
		assert.True(t, tool.Annotations.ReadOnlyHint)
		assert.Equal(t, []string{"read_builds"}, scopes)
		assert.NotNil(t, handler)
	})

	t.Run("SuccessfulListWithFiltersAndPagination", func(t *testing.T) {
		var captured *buildkite.JobsListOptions
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				captured = opt
				assert.Equal(t, "test-org", org)
				assert.Equal(t, "test-pipeline", pipeline)
				assert.Equal(t, "123", buildNumber)
				return buildkite.JobsList{
					Items: []buildkite.Job{{ID: "job-1", Name: "test", State: "passed", Command: "go test ./...", ExitStatus: intPtr(0)}},
					Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/v2/...?after=cursor2"},
				}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := ListJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug:            "test-org",
			PipelineSlug:       "test-pipeline",
			BuildNumber:        "123",
			State:              "passed, failed",
			StepKey:            "test-step",
			GroupKey:           "test-group",
			IncludeRetriedJobs: boolPtr(false),
			PerPage:            50,
			After:              "cursor1",
		})
		require.NoError(t, err)

		text := getTextResult(t, result).Text
		assert.Contains(t, text, `"items":[`)
		assert.Contains(t, text, `"name":"test"`)
		assert.Contains(t, text, `"command":"go test ./..."`)
		assert.Contains(t, text, `"id":"job-1"`)
		assert.Contains(t, text, `"next":"https://api.buildkite.com`)

		require.NotNil(t, captured)
		assert.Equal(t, []string{"passed", "failed"}, captured.State)
		assert.Equal(t, "test-step", captured.StepKey)
		assert.Equal(t, "test-group", captured.GroupKey)
		require.NotNil(t, captured.IncludeRetriedJobs)
		assert.False(t, *captured.IncludeRetriedJobs)
		assert.Equal(t, 50, captured.PerPage)
		assert.Equal(t, "cursor1", captured.After)
	})

	t.Run("SummaryContainsOnlyDiagnosticFields", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{Items: []buildkite.Job{{
					ID:              "job-1",
					Name:            "test",
					State:           "failed",
					Command:         "go test ./...",
					ExitStatus:      intPtr(1),
					SoftFailed:      true,
					SignalReason:    "agent_stop",
					StepKey:         "test",
					RetriesCount:    1,
					RetrySource:     &buildkite.JobRetrySource{JobID: "job-0", RetryType: "manual"},
					BuildURL:        "https://api.buildkite.com/v2/builds/123",
					ClusterID:       "cluster-1",
					ClusterQueueURL: "https://api.buildkite.com/v2/queues/queue-1",
				}}}, &buildkite.Response{}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := ListJobs()
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug: "test-org", PipelineSlug: "test-pipeline", BuildNumber: "123",
		})
		require.NoError(t, err)

		var response struct {
			Items []map[string]any `json:"items"`
		}
		require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &response))
		require.Len(t, response.Items, 1)
		assert.Equal(t, map[string]any{
			"id": "job-1", "name": "test", "state": "failed", "command": "go test ./...", "exit_status": float64(1),
			"soft_failed": true, "signal_reason": "agent_stop", "step_key": "test",
			"retries_count": float64(1), "retry_source": map[string]any{"job_id": "job-0", "retry_type": "manual"},
		}, response.Items[0])
	})

	t.Run("DetailedExcludesRepeatedInfrastructureFields", func(t *testing.T) {
		timestamp := buildkite.NewTimestamp(time.Date(2026, 7, 17, 6, 46, 58, 0, time.UTC))
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{
					Items: []buildkite.Job{{
						ID: "job-1", Name: "test", State: "failed", Command: "go test ./...",
						CreatedAt: timestamp, StartedAt: timestamp, FinishedAt: timestamp,
						Signal: intPtr(9), SignalReason: "agent_stop", Retried: true,
						RetriedInJobID: "job-2", RetriesCount: 1,
						RetrySource:        &buildkite.JobRetrySource{JobID: "job-0", RetryType: "manual"},
						SoftFailed:         true,
						Unblockable:        true,
						ParallelGroupIndex: intPtr(1), ParallelGroupTotal: intPtr(3),
						BuildURL: "https://api.buildkite.com/v2/builds/123", ClusterID: "cluster-1",
						ClusterQueueURL: "https://api.buildkite.com/v2/queues/queue-1", AgentQueryRules: []string{"queue=test"},
					}},
					Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/v2/jobs?after=next"},
				}, &buildkite.Response{}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := ListJobs()
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug: "test-org", PipelineSlug: "test-pipeline", BuildNumber: "123", DetailLevel: "detailed",
		})
		require.NoError(t, err)

		text := getTextResult(t, result).Text
		var jobs JobListResult[JobDetail]
		require.NoError(t, json.Unmarshal([]byte(text), &jobs))
		require.Len(t, jobs.Items, 1)
		job := jobs.Items[0]
		assert.Equal(t, "job-1", job.ID)
		assert.Equal(t, timestamp, job.CreatedAt)
		assert.Equal(t, timestamp, job.StartedAt)
		assert.Equal(t, timestamp, job.FinishedAt)
		assert.Equal(t, intPtr(9), job.Signal)
		assert.Equal(t, "agent_stop", job.SignalReason)
		assert.True(t, job.Retried)
		assert.Equal(t, "job-2", job.RetriedInJobID)
		assert.Equal(t, 1, job.RetriesCount)
		assert.Equal(t, &buildkite.JobRetrySource{JobID: "job-0", RetryType: "manual"}, job.RetrySource)
		assert.True(t, job.SoftFailed)
		assert.True(t, job.Unblockable)
		assert.Equal(t, intPtr(1), job.ParallelGroupIndex)
		assert.Equal(t, intPtr(3), job.ParallelGroupTotal)
		assert.Equal(t, buildkite.JobsListLink("https://api.buildkite.com/v2/jobs?after=next"), jobs.Links.Next)
		assert.NotContains(t, text, "build_url")
		assert.NotContains(t, text, "cluster_id")
		assert.NotContains(t, text, "cluster_queue_url")
		assert.NotContains(t, text, "agent_query_rules")
	})

	t.Run("DetailedAgentFields", func(t *testing.T) {
		for _, test := range []struct {
			name         string
			includeAgent bool
			want         buildkite.Agent
		}{
			{name: "IDOnlyByDefault", want: buildkite.Agent{ID: "agent-1"}},
			{name: "FullDetailsWhenRequested", includeAgent: true, want: testJobAgent()},
		} {
			t.Run(test.name, func(t *testing.T) {
				mockJobs := &MockJobsClient{
					ListByBuildFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
						return buildkite.JobsList{Items: []buildkite.Job{{ID: "job-1", Agent: testJobAgent()}}}, &buildkite.Response{}, nil
					},
				}

				ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
				_, handler, _ := ListJobs()
				result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
					OrgSlug: "test-org", PipelineSlug: "test-pipeline", BuildNumber: "123",
					DetailLevel: "detailed", IncludeAgent: test.includeAgent,
				})
				require.NoError(t, err)

				var jobs JobListResult[JobDetail]
				require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &jobs))
				require.Len(t, jobs.Items, 1)
				require.NotNil(t, jobs.Items[0].Agent)
				assert.Equal(t, test.want, *jobs.Items[0].Agent)
			})
		}
	})

	t.Run("RedactsUnusedJobFields", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{
					Items: []buildkite.Job{{
						ID:           "job-1",
						State:        "passed",
						WebURL:       "https://buildkite.com/test-org/test-pipeline/builds/123#job-1",
						RawLogsURL:   "https://api.buildkite.com/v2/logs/raw",
						ArtifactsURL: "https://api.buildkite.com/v2/artifacts",
						LogsURL:      "https://api.buildkite.com/v2/logs",
						GraphQLID:    "graphql-job-id",
					}},
					Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/v2/...?after=cursor2"},
				}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := ListJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			DetailLevel:  "full",
		})
		require.NoError(t, err)

		var jobs buildkite.JobsList
		require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &jobs))
		require.Len(t, jobs.Items, 1)
		assert.Equal(t, "job-1", jobs.Items[0].ID)
		assert.Equal(t, "passed", jobs.Items[0].State)
		assert.Equal(t, "https://api.buildkite.com/v2/...?after=cursor2", string(jobs.Links.Next))
		assert.Empty(t, jobs.Items[0].WebURL)
		assert.Empty(t, jobs.Items[0].RawLogsURL)
		assert.Empty(t, jobs.Items[0].ArtifactsURL)
		assert.Empty(t, jobs.Items[0].LogsURL)
		assert.Empty(t, jobs.Items[0].GraphQLID)
	})

	t.Run("DefaultsToAgentIDOnly", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{
					Items: []buildkite.Job{{
						ID:    "job-1",
						State: "passed",
						Agent: testJobAgent(),
					}},
				}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := ListJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			DetailLevel:  "full",
		})
		require.NoError(t, err)

		var jobs buildkite.JobsList
		require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &jobs))
		require.Len(t, jobs.Items, 1)
		assert.Equal(t, "agent-1", jobs.Items[0].Agent.ID)
		assert.Empty(t, jobs.Items[0].Agent.Name)
		assert.Empty(t, jobs.Items[0].Agent.Hostname)
		assert.Empty(t, jobs.Items[0].Agent.Version)
	})

	t.Run("IncludesAgentDetailsWhenRequested", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			ListByBuildFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{
					Items: []buildkite.Job{{
						ID:    "job-1",
						State: "passed",
						Agent: testJobAgent(),
					}},
				}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := ListJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			DetailLevel:  "full",
			IncludeAgent: true,
		})
		require.NoError(t, err)

		var jobs buildkite.JobsList
		require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &jobs))
		require.Len(t, jobs.Items, 1)
		assert.Equal(t, testJobAgent(), jobs.Items[0].Agent)
	})

	t.Run("InvalidDetailLevel", func(t *testing.T) {
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: &MockJobsClient{}})
		_, handler, _ := ListJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug: "test-org", PipelineSlug: "test-pipeline", BuildNumber: "123", DetailLevel: "verbose",
		})
		require.NoError(t, err)
		assert.Contains(t, getTextResult(t, result).Text, "detail_level must be 'summary', 'detailed', or 'full'")
	})

	t.Run("AfterAndBeforeMutuallyExclusive", func(t *testing.T) {
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: &MockJobsClient{}})
		_, handler, _ := ListJobs()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListJobsArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			After:        "a",
			Before:       "b",
		})
		require.NoError(t, err)
		assert.Contains(t, getTextResult(t, result).Text, "mutually exclusive")
	})
}

func TestGetJob(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, handler, scopes := GetJob()
		assert.Equal(t, "get_job", tool.Name)
		assert.Contains(t, tool.Description, "Get a single job")
		assert.True(t, tool.Annotations.ReadOnlyHint)
		assert.Equal(t, []string{"read_builds"}, scopes)
		assert.NotNil(t, handler)
	})

	t.Run("BuildScopedLookup", func(t *testing.T) {
		called := false
		mockJobs := &MockJobsClient{
			GetJobFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				called = true
				assert.Equal(t, "test-org", org)
				assert.Equal(t, "test-pipeline", pipeline)
				assert.Equal(t, "123", buildNumber)
				assert.Equal(t, "job-456", jobID)
				return buildkite.Job{ID: jobID, State: "passed"}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
			GetJobByOrgFunc: func(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				t.Fatal("GetJobByOrg should not be called for a build-scoped lookup")
				return buildkite.Job{}, nil, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := GetJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "123",
			JobID:        "job-456",
		})
		require.NoError(t, err)
		assert.True(t, called)
		assert.Contains(t, getTextResult(t, result).Text, `"id":"job-456"`)
	})

	t.Run("RedactsUnusedJobFields", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			GetJobByOrgFunc: func(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				assert.Equal(t, "test-org", org)
				assert.Equal(t, "job-456", jobID)
				return buildkite.Job{
					ID:           jobID,
					State:        "running",
					WebURL:       "https://buildkite.com/test-org/test-pipeline/builds/123#job-456",
					RawLogsURL:   "https://api.buildkite.com/v2/logs/raw",
					ArtifactsURL: "https://api.buildkite.com/v2/artifacts",
					LogsURL:      "https://api.buildkite.com/v2/logs",
					GraphQLID:    "graphql-job-id",
				}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := GetJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobArgs{
			OrgSlug: "test-org",
			JobID:   "job-456",
		})
		require.NoError(t, err)

		var job buildkite.Job
		require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &job))
		assert.Equal(t, "job-456", job.ID)
		assert.Equal(t, "running", job.State)
		assert.Empty(t, job.WebURL)
		assert.Empty(t, job.RawLogsURL)
		assert.Empty(t, job.ArtifactsURL)
		assert.Empty(t, job.LogsURL)
		assert.Empty(t, job.GraphQLID)
	})

	t.Run("DefaultsToAgentIDOnly", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			GetJobByOrgFunc: func(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				return buildkite.Job{
					ID:    jobID,
					State: "running",
					Agent: testJobAgent(),
				}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := GetJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobArgs{
			OrgSlug: "test-org",
			JobID:   "job-456",
		})
		require.NoError(t, err)

		var job buildkite.Job
		require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &job))
		assert.Equal(t, "agent-1", job.Agent.ID)
		assert.Empty(t, job.Agent.Name)
		assert.Empty(t, job.Agent.Hostname)
		assert.Empty(t, job.Agent.Version)
	})

	t.Run("IncludesAgentDetailsWhenRequested", func(t *testing.T) {
		mockJobs := &MockJobsClient{
			GetJobByOrgFunc: func(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				return buildkite.Job{
					ID:    jobID,
					State: "running",
					Agent: testJobAgent(),
				}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := GetJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobArgs{
			OrgSlug:      "test-org",
			JobID:        "job-456",
			IncludeAgent: true,
		})
		require.NoError(t, err)

		var job buildkite.Job
		require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &job))
		assert.Equal(t, testJobAgent(), job.Agent)
	})

	t.Run("OrgScopedLookup", func(t *testing.T) {
		called := false
		mockJobs := &MockJobsClient{
			GetJobByOrgFunc: func(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				called = true
				assert.Equal(t, "test-org", org)
				assert.Equal(t, "job-456", jobID)
				return buildkite.Job{ID: jobID, State: "running"}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
			},
			GetJobFunc: func(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error) {
				t.Fatal("GetJob should not be called for an org-scoped lookup")
				return buildkite.Job{}, nil, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: mockJobs})
		_, handler, _ := GetJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobArgs{
			OrgSlug: "test-org",
			JobID:   "job-456",
		})
		require.NoError(t, err)
		assert.True(t, called)
		assert.Contains(t, getTextResult(t, result).Text, `"state":"running"`)
	})

	t.Run("PartialBuildScopeIsRejected", func(t *testing.T) {
		ctx := ContextWithDeps(context.Background(), ToolDependencies{JobsClient: &MockJobsClient{}})
		_, handler, _ := GetJob()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobArgs{
			OrgSlug:      "test-org",
			JobID:        "job-456",
			PipelineSlug: "test-pipeline", // build_number missing
		})
		require.NoError(t, err)
		assert.Contains(t, getTextResult(t, result).Text, "provide both")
	})
}
