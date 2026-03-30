package buildkite

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockBuildsClient struct {
	ListByOrgFunc      func(ctx context.Context, org string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
	ListByPipelineFunc func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
	GetFunc            func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error)
	CreateFunc         func(ctx context.Context, org string, pipeline string, b buildkite.CreateBuild) (buildkite.Build, *buildkite.Response, error)
	CancelFunc         func(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error)
	RebuildFunc        func(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error)
}

func (m *MockBuildsClient) Get(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, pipeline, id, opt)
	}
	return buildkite.Build{}, nil, nil
}

func (m *MockBuildsClient) ListByOrg(ctx context.Context, org string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
	if m.ListByOrgFunc != nil {
		return m.ListByOrgFunc(ctx, org, opt)
	}
	return nil, nil, nil
}

func (m *MockBuildsClient) ListByPipeline(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
	if m.ListByPipelineFunc != nil {
		return m.ListByPipelineFunc(ctx, org, pipeline, opt)
	}
	return nil, nil, nil
}

func (m *MockBuildsClient) Create(ctx context.Context, org string, pipeline string, b buildkite.CreateBuild) (buildkite.Build, *buildkite.Response, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, org, pipeline, b)
	}
	return buildkite.Build{}, nil, nil
}

func (m *MockBuildsClient) Cancel(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error) {
	if m.CancelFunc != nil {
		return m.CancelFunc(ctx, org, pipeline, buildNumber)
	}
	return buildkite.Build{}, nil
}

func (m *MockBuildsClient) Rebuild(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error) {
	if m.RebuildFunc != nil {
		return m.RebuildFunc(ctx, org, pipeline, buildNumber)
	}
	return buildkite.Build{}, nil
}

var _ BuildsClient = (*MockBuildsClient)(nil)

func TestGetBuildDefault(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			// Return build without jobs
			return buildkite.Build{
					ID:        "123",
					Number:    1,
					State:     "running",
					CreatedAt: &buildkite.Timestamp{},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := GetBuild()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test default behavior - jobs always excluded, summary always included
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetBuildArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// New format returns BuildDetail (detailed level by default)
	assert.Contains(textContent.Text, `"id":"123"`)
	assert.Contains(textContent.Text, `"number":1`)
	assert.Contains(textContent.Text, `"state":"running"`)
	assert.Contains(textContent.Text, `"total":0`)
	assert.Contains(textContent.Text, `"by_state":{}`)
	assert.Contains(textContent.Text, `"jobs_total":0`)
}

func TestGetBuildWithJobSummary(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			// Create a build with some jobs to test summary functionality
			return buildkite.Build{
					ID:        "123",
					Number:    1,
					State:     "finished",
					CreatedAt: &buildkite.Timestamp{},
					Jobs: []buildkite.Job{
						{ID: "job1", State: "passed"}, // API already coerced
						{ID: "job2", State: "failed"}, // API already coerced
						{ID: "job3", State: "running"},
						{ID: "job4", State: "waiting"},
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := GetBuild()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test behavior - jobs always excluded, summary always shown
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetBuildArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"job_summary"`)
	assert.Contains(textContent.Text, `"total":4`)
	assert.Contains(textContent.Text, `"by_state":{"failed":1,"passed":1,"running":1,"waiting":1}`)
	// Lightweight job entries included with correct id, name, and state
	assert.Contains(textContent.Text, `"jobs":[{"id":"job1"`)
	assert.Contains(textContent.Text, `{"id":"job1","name":"","state":"passed"}`)
	assert.Contains(textContent.Text, `{"id":"job2","name":"","state":"failed"}`)
	assert.Contains(textContent.Text, `{"id":"job3","name":"","state":"running"}`)
	assert.Contains(textContent.Text, `{"id":"job4","name":"","state":"waiting"}`)
	// Full job objects should NOT be present (no agent, command, etc.)
	assert.NotContains(textContent.Text, `"agent"`)
}

func TestListBuilds(t *testing.T) {
	assert := require.New(t)

	var capturedOptions *buildkite.BuildsListOptions
	client := &MockBuildsClient{
		ListByPipelineFunc: func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
			capturedOptions = opt
			return []buildkite.Build{
					{
						ID:        "123",
						Number:    1,
						State:     "running",
						CreatedAt: &buildkite.Timestamp{},
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := ListBuilds()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListBuildsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)

	// New format returns BuildSummary (summary level by default)
	assert.Contains(textContent.Text, `"headers":{"Link":""}`)
	assert.Contains(textContent.Text, `"items":[`)
	assert.Contains(textContent.Text, `"id":"123"`)
	assert.Contains(textContent.Text, `"number":1`)
	assert.Contains(textContent.Text, `"state":"running"`)
	assert.Contains(textContent.Text, `"jobs_total":0`)

	// Verify default pagination parameters - new defaults
	assert.NotNil(capturedOptions)
	assert.Equal(1, capturedOptions.Page)
	assert.Equal(30, capturedOptions.PerPage) // New default
	assert.Nil(capturedOptions.Branch)        // Branch should be nil when not specified
}

func TestListBuildsWithCustomPagination(t *testing.T) {
	assert := require.New(t)

	var capturedOptions *buildkite.BuildsListOptions
	client := &MockBuildsClient{
		ListByPipelineFunc: func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
			capturedOptions = opt
			return []buildkite.Build{
					{
						ID:        "123",
						Number:    1,
						State:     "running",
						CreatedAt: &buildkite.Timestamp{},
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := ListBuilds()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with custom pagination parameters
	request := createMCPRequest(t, map[string]any{})
	_, _, err := handler(ctx, request, ListBuildsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		Page:         3,
		PerPage:      50,
	})
	assert.NoError(err)

	// Verify custom pagination parameters were used
	assert.NotNil(capturedOptions)
	assert.Equal(3, capturedOptions.Page)
	assert.Equal(50, capturedOptions.PerPage)
	assert.Nil(capturedOptions.Branch) // Branch should be nil when not specified
}

func TestListBuildsWithBranchFilter(t *testing.T) {
	assert := require.New(t)

	var capturedOptions *buildkite.BuildsListOptions
	client := &MockBuildsClient{
		ListByPipelineFunc: func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
			capturedOptions = opt
			return []buildkite.Build{
					{
						ID:        "123",
						Number:    1,
						State:     "running",
						CreatedAt: &buildkite.Timestamp{},
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := ListBuilds()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with branch filter
	request := createMCPRequest(t, map[string]any{})
	_, _, err := handler(ctx, request, ListBuildsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		Branch:       "main",
	})
	assert.NoError(err)

	// Verify branch filter was applied
	assert.NotNil(capturedOptions)
	assert.Equal([]string{"main"}, capturedOptions.Branch)
	assert.Equal(1, capturedOptions.Page)
	assert.Equal(30, capturedOptions.PerPage) // New default
}

func TestListBuildsByOrg(t *testing.T) {
	assert := require.New(t)

	var capturedOrg string
	var capturedOptions *buildkite.BuildsListOptions
	client := &MockBuildsClient{
		ListByOrgFunc: func(ctx context.Context, org string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
			capturedOrg = org
			capturedOptions = opt
			return []buildkite.Build{
					{
						ID:        "build-1",
						Number:    10,
						State:     "running",
						CreatedAt: &buildkite.Timestamp{},
						Pipeline:  &buildkite.Pipeline{Slug: "pipeline-a"},
					},
					{
						ID:        "build-2",
						Number:    20,
						State:     "scheduled",
						CreatedAt: &buildkite.Timestamp{},
						Pipeline:  &buildkite.Pipeline{Slug: "pipeline-b"},
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
		ListByPipelineFunc: func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
			t.Fatal("ListByPipeline should not be called when pipeline_slug is empty")
			return nil, nil, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := ListBuilds()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test without pipeline_slug — should use ListByOrg
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListBuildsArgs{
		OrgSlug: "my-org",
		State:   "running",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"id":"build-1"`)
	assert.Contains(textContent.Text, `"id":"build-2"`)
	assert.Equal("my-org", capturedOrg)
	assert.NotNil(capturedOptions)
	assert.Equal([]string{"running"}, capturedOptions.State)
	assert.Equal(1, capturedOptions.Page)
	assert.Equal(30, capturedOptions.PerPage)
	// Pipeline info should be included for org-wide queries
	assert.False(capturedOptions.ExcludePipeline)
}

func TestListBuildsByOrgError(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		ListByOrgFunc: func(ctx context.Context, org string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
			return nil, nil, &buildkite.ErrorResponse{
				RawBody: []byte("organization not found"),
				Response: &http.Response{
					StatusCode: 404,
				},
			}
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	_, handler, _ := ListBuilds()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListBuildsArgs{
		OrgSlug: "bad-org",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, "organization not found")
	assert.True(result.IsError)
}

func TestGetBuildTestEngineRuns(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			// Return build with test engine data
			return buildkite.Build{
					ID:     "123",
					Number: 1,
					TestEngine: &buildkite.TestEngineProperty{
						Runs: []buildkite.TestEngineRun{
							{
								ID: "run-1",
								Suite: buildkite.TestEngineSuite{
									ID:   "suite-1",
									Slug: "my-test-suite",
								},
							},
							{
								ID: "run-2",
								Suite: buildkite.TestEngineSuite{
									ID:   "suite-2",
									Slug: "another-test-suite",
								},
							},
						},
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := GetBuildTestEngineRuns()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test tool properties
	assert.Equal("get_build_test_engine_runs", tool.Name)
	assert.Contains(tool.Description, "test engine runs")

	// Test successful request
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetBuildTestEngineRunsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, "run-1")
	assert.Contains(textContent.Text, "run-2")
	assert.Contains(textContent.Text, "my-test-suite")
	assert.Contains(textContent.Text, "another-test-suite")
}

func TestGetBuildTestEngineRunsNoBuildTestEngine(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			// Return build without test engine data
			return buildkite.Build{
					ID:         "123",
					Number:     1,
					TestEngine: nil,
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	_, handler, _ := GetBuildTestEngineRuns()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetBuildTestEngineRunsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// Should return empty array when no test engine data
	assert.Equal("null", textContent.Text)
}

func TestCreateBuild(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		CreateFunc: func(ctx context.Context, org string, pipeline string, b buildkite.CreateBuild) (buildkite.Build, *buildkite.Response, error) {
			// Validate required fields
			assert.Equal("org", org)
			assert.Equal("pipeline", pipeline)
			assert.Equal("abc123", b.Commit)
			assert.Equal("Test build", b.Message)
			assert.True(b.IgnorePipelineBranchFilters)

			// Return created build
			return buildkite.Build{
					ID:        "123",
					Number:    1,
					State:     "created",
					CreatedAt: &buildkite.Timestamp{},
					Env: map[string]any{
						"ENV_VAR": "value",
					},
					MetaData: map[string]string{
						"meta_key": "meta_value",
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 201,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := CreateBuild()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := CreateBuildArgs{
		OrgSlug:             "org",
		PipelineSlug:        "pipeline",
		Commit:              "abc123",
		Message:             "Test build",
		Branch:              "main",
		IgnoreBranchFilters: true,
		Environment: []Entry{
			{Key: "ENV_VAR", Value: "value"},
		},
		MetaData: []Entry{
			{Key: "meta_key", Value: "meta_value"},
		},
	}

	result, _, err := handler(ctx, request, args)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"123","number":1,"state":"created","blocked":false,"author":{},"env":{"ENV_VAR":"value"},"created_at":"0001-01-01T00:00:00Z","meta_data":{"meta_key":"meta_value"},"creator":{"avatar_url":"","created_at":null,"email":"","id":"","name":""}}`, textContent.Text)
}

func TestGetBuildWithJobStateFilter(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
					ID:     "123",
					Number: 1,
					State:  "failed",
					Jobs: []buildkite.Job{
						{ID: "job1", Name: "Test 1", State: "passed", Agent: buildkite.Agent{ID: "agent1", Name: "Agent 1"}},
						{ID: "job2", Name: "Test 2", State: "failed", Agent: buildkite.Agent{ID: "agent2", Name: "Agent 2"}},
						{ID: "job3", Name: "Test 3", State: "failed", Agent: buildkite.Agent{ID: "agent3", Name: "Agent 3"}},
						{ID: "job4", Name: "Test 4", State: "broken", Agent: buildkite.Agent{ID: "agent4", Name: "Agent 4"}},
					},
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := GetBuild()
	assert.NotNil(tool)
	assert.NotNil(handler)

	t.Run("filter by single state", func(t *testing.T) {
		request := createMCPRequest(t, map[string]any{})
		result, _, err := handler(ctx, request, GetBuildArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			BuildNumber:  "1",
			DetailLevel:  "full",
			JobState:     "failed",
		})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"job2"`)
		assert.Contains(textContent.Text, `"id":"job3"`)
		assert.NotContains(textContent.Text, `"id":"job1"`)
		assert.NotContains(textContent.Text, `"id":"job4"`)
	})

	t.Run("filter by multiple states", func(t *testing.T) {
		request := createMCPRequest(t, map[string]any{})
		result, _, err := handler(ctx, request, GetBuildArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			BuildNumber:  "1",
			DetailLevel:  "full",
			JobState:     "failed,broken",
		})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"job2"`)
		assert.Contains(textContent.Text, `"id":"job3"`)
		assert.Contains(textContent.Text, `"id":"job4"`)
		assert.NotContains(textContent.Text, `"id":"job1"`)
	})

	t.Run("filter with whitespace handling", func(t *testing.T) {
		request := createMCPRequest(t, map[string]any{})
		result, _, err := handler(ctx, request, GetBuildArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			BuildNumber:  "1",
			DetailLevel:  "full",
			JobState:     "failed, broken",
		})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"job2"`)
		assert.Contains(textContent.Text, `"id":"job3"`)
		assert.Contains(textContent.Text, `"id":"job4"`)
	})
}

func TestGetBuildWithAgentStripping(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
					ID:     "123",
					Number: 1,
					State:  "passed",
					Jobs: []buildkite.Job{
						{ID: "job1", Name: "Test 1", State: "passed", Agent: buildkite.Agent{ID: "agent1", Name: "Agent 1", Hostname: "host1"}},
					},
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := GetBuild()
	assert.NotNil(tool)
	assert.NotNil(handler)

	t.Run("include_agent false strips details", func(t *testing.T) {
		request := createMCPRequest(t, map[string]any{})
		result, _, err := handler(ctx, request, GetBuildArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			BuildNumber:  "1",
			DetailLevel:  "full",
			IncludeAgent: false,
		})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"agent1"`)
		assert.NotContains(textContent.Text, `"Agent 1"`)
		assert.NotContains(textContent.Text, `"host1"`)
	})

	t.Run("include_agent true keeps details", func(t *testing.T) {
		request := createMCPRequest(t, map[string]any{})
		result, _, err := handler(ctx, request, GetBuildArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			BuildNumber:  "1",
			DetailLevel:  "full",
			IncludeAgent: true,
		})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"agent1"`)
		assert.Contains(textContent.Text, `"Agent 1"`)
		assert.Contains(textContent.Text, `"host1"`)
	})
}

func TestGetBuildDetailedWithJobStateFilter(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
					ID:     "123",
					Number: 1,
					State:  "failed",
					Jobs: []buildkite.Job{
						{ID: "job1", Name: "Test 1", State: "passed"},
						{ID: "job2", Name: "Test 2", State: "failed"},
						{ID: "job3", Name: "Test 3", State: "failed"},
						{ID: "job4", Name: "Test 4", State: "broken"},
					},
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	tool, handler, _ := GetBuild()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetBuildArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
		DetailLevel:  "detailed",
		JobState:     "failed",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// jobs_total should reflect ALL jobs (4)
	assert.Contains(textContent.Text, `"jobs_total":4`)
	// job_summary.total should reflect filtered jobs (2)
	assert.Contains(textContent.Text, `"total":2`)
	// job_summary.by_state should only show failed count
	assert.Contains(textContent.Text, `"failed":2`)
	// jobs array should contain only the filtered entries
	assert.Contains(textContent.Text, `{"id":"job2","name":"Test 2","state":"failed"}`)
	assert.Contains(textContent.Text, `{"id":"job3","name":"Test 3","state":"failed"}`)
	assert.NotContains(textContent.Text, `{"id":"job1"`)
	assert.NotContains(textContent.Text, `{"id":"job4"`)
}

func TestGetBuildInvalidDetailLevel(t *testing.T) {
	assert := require.New(t)

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
					ID:     "123",
					Number: 1,
					State:  "failed",
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})

	_, handler, _ := GetBuild()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetBuildArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
		DetailLevel:  "summary",
	})
	assert.NoError(err)
	assert.True(result.IsError)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, "detail_level must be 'detailed' or 'full'")
}

func TestCancelBuild(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, _, _ := CancelBuild()
		require.Equal(t, "cancel_build", tool.Name)
		require.Contains(t, tool.Description, "Cancel")
	})

	t.Run("Success", func(t *testing.T) {
		assert := require.New(t)

		client := &MockBuildsClient{
			CancelFunc: func(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error) {
				assert.Equal("test-org", org)
				assert.Equal("test-pipeline", pipeline)
				assert.Equal("42", buildNumber)
				return buildkite.Build{
					ID:     "123",
					Number: 42,
					State:  "canceling",
				}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := CancelBuild()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CancelBuildArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "42",
		})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"123"`)
		assert.Contains(textContent.Text, `"state":"canceling"`)
	})

	t.Run("Error", func(t *testing.T) {
		assert := require.New(t)

		client := &MockBuildsClient{
			CancelFunc: func(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error) {
				return buildkite.Build{}, errors.New("build not found")
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := CancelBuild()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CancelBuildArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "42",
		})
		assert.NoError(err)
		assert.True(result.IsError)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, "build not found")
	})
}

func TestRebuildBuild(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, _, _ := RebuildBuild()
		require.Equal(t, "rebuild_build", tool.Name)
		require.Contains(t, tool.Description, "Rebuild")
	})

	t.Run("Success", func(t *testing.T) {
		assert := require.New(t)

		client := &MockBuildsClient{
			RebuildFunc: func(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error) {
				assert.Equal("test-org", org)
				assert.Equal("test-pipeline", pipeline)
				assert.Equal("42", buildNumber)
				return buildkite.Build{
					ID:     "456",
					Number: 43,
					State:  "scheduled",
				}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := RebuildBuild()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), RebuildBuildArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "42",
		})
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"456"`)
		assert.Contains(textContent.Text, `"state":"scheduled"`)
	})

	t.Run("Error", func(t *testing.T) {
		assert := require.New(t)

		client := &MockBuildsClient{
			RebuildFunc: func(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error) {
				return buildkite.Build{}, errors.New("build not found")
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := RebuildBuild()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), RebuildBuildArgs{
			OrgSlug:      "test-org",
			PipelineSlug: "test-pipeline",
			BuildNumber:  "42",
		})
		assert.NoError(err)
		assert.True(result.IsError)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, "build not found")
	})
}
