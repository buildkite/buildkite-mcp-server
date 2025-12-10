package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type MockBuildsClient struct {
	ListByPipelineFunc func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
	GetFunc            func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error)
	CreateFunc         func(ctx context.Context, org string, pipeline string, b buildkite.CreateBuild) (buildkite.Build, *buildkite.Response, error)
}

func (m *MockBuildsClient) Get(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, pipeline, id, opt)
	}
	return buildkite.Build{}, nil, nil
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

var _ BuildsClient = (*MockBuildsClient)(nil)

func TestWaitForBuildCompletes(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()

	// Track call count to simulate state changes
	callCount := 0
	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			callCount++

			// First call returns running build, second call returns finished build
			state := "running"
			if callCount >= 2 {
				state = "finished"
			}

			return buildkite.Build{
					ID:        "123",
					Number:    1,
					State:     state,
					CreatedAt: &buildkite.Timestamp{},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, typedHandler, _ := WaitForBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with short timeout to make test fast
	request := mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Arguments: map[string]any{
				"org_slug":      "org",
				"pipeline_slug": "pipeline",
				"build_number":  "1",
				"wait_timeout":  10,
			},
			Meta: &mcp.Meta{}, // Add empty Meta to avoid nil pointer
		},
	}
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"id":"123"`)
	assert.Contains(textContent.Text, `"number":1`)
	assert.Contains(textContent.Text, `"state":"finished"`)

	// Should have been called at least twice (initial + polling)
	assert.GreaterOrEqual(callCount, 2)
}

func TestWaitForBuildTimeout(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			// Always return running to trigger timeout
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

	tool, typedHandler, _ := WaitForBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with very short timeout (1 second)
	request := mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Arguments: map[string]any{
				"org_slug":      "org",
				"pipeline_slug": "pipeline",
				"build_number":  "1",
				"wait_timeout":  1,
			},
			Meta: &mcp.Meta{}, // Add empty Meta to avoid nil pointer
		},
	}
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// Should still return the build even if timeout occurs
	assert.Contains(textContent.Text, `"id":"123"`)
	assert.Contains(textContent.Text, `"state":"running"`)
}

func TestWaitForBuildMissingParameters(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockBuildsClient{}

	tool, typedHandler, _ := WaitForBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test missing org_slug
	request := createMCPRequest(t, map[string]any{
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "org_slug parameter is required")

	// Test missing pipeline_slug
	request = createMCPRequest(t, map[string]any{
		"org_slug":     "org",
		"build_number": "1",
	})
	result, err = handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "pipeline_slug parameter is required")

	// Test missing build_number
	request = createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
	})
	result, err = handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "build_number parameter is required")
}

func TestGetBuildDefault(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
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

	tool, typedHandler, _ := GetBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test default behavior - jobs always excluded, summary always included
	request := createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// New format returns BuildDetail (detailed level by default)
	assert.Contains(textContent.Text, `"id":"123"`)
	assert.Contains(textContent.Text, `"number":1`)
	assert.Contains(textContent.Text, `"state":"running"`)
	assert.Contains(textContent.Text, `"job_summary":{"total":0,"by_state":{}}`)
	assert.Contains(textContent.Text, `"jobs_total":0`)
}

func TestGetBuildWithJobSummary(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
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

	tool, typedHandler, _ := GetBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test behavior - jobs always excluded, summary always shown
	request := createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"job_summary"`)
	assert.Contains(textContent.Text, `"total":4`)
	assert.Contains(textContent.Text, `"by_state":{"failed":1,"passed":1,"running":1,"waiting":1}`)
	assert.NotContains(textContent.Text, `"jobs"`) // Jobs always excluded
}

func TestListBuilds(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
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

	tool, typedHandler, _ := ListBuilds(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
	})
	result, err := handler(ctx, request)
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

	ctx := context.Background()
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

	tool, typedHandler, _ := ListBuilds(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with custom pagination parameters
	request := createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
		"page":          float64(3),
		"per_page":      float64(50),
	})
	_, err := handler(ctx, request)
	assert.NoError(err)

	// Verify custom pagination parameters were used
	assert.NotNil(capturedOptions)
	assert.Equal(3, capturedOptions.Page)
	assert.Equal(50, capturedOptions.PerPage)
	assert.Nil(capturedOptions.Branch) // Branch should be nil when not specified
}

func TestListBuildsWithBranchFilter(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
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

	tool, typedHandler, _ := ListBuilds(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with branch filter
	request := createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
		"branch":        "main",
	})
	_, err := handler(ctx, request)
	assert.NoError(err)

	// Verify branch filter was applied
	assert.NotNil(capturedOptions)
	assert.Equal([]string{"main"}, capturedOptions.Branch)
	assert.Equal(1, capturedOptions.Page)
	assert.Equal(30, capturedOptions.PerPage) // New default
}

func TestGetBuildTestEngineRuns(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
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

	tool, typedHandler, _ := GetBuildTestEngineRuns(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test tool properties
	assert.Equal("get_build_test_engine_runs", tool.Name)
	assert.Contains(tool.Description, "test engine runs")

	// Test successful request
	request := createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, "run-1")
	assert.Contains(textContent.Text, "run-2")
	assert.Contains(textContent.Text, "my-test-suite")
	assert.Contains(textContent.Text, "another-test-suite")
}

func TestGetBuildTestEngineRunsNoBuildTestEngine(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
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

	_, typedHandler, _ := GetBuildTestEngineRuns(client)
	handler := mcp.NewTypedToolHandler(typedHandler)

	request := createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// Should return empty array when no test engine data
	assert.Equal("null", textContent.Text)
}

func TestGetBuildTestEngineRunsMissingParameters(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockBuildsClient{}

	_, typedHandler, _ := GetBuildTestEngineRuns(client)
	handler := mcp.NewTypedToolHandler(typedHandler)

	// Test missing org parameter
	request := createMCPRequest(t, map[string]any{
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "org_slug")

	// Test missing pipeline_slug parameter
	request = createMCPRequest(t, map[string]any{
		"org_slug":     "org",
		"build_number": "1",
	})
	result, err = handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "pipeline_slug")

	// Test missing build_number parameter
	request = createMCPRequest(t, map[string]any{
		"org_slug":      "org",
		"pipeline_slug": "pipeline",
	})
	result, err = handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "build_number")
}

func TestCreateBuild(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
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

	tool, handler, _ := CreateBuild(client)
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

	result, err := handler(ctx, request, args)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"123","number":1,"state":"created","blocked":false,"author":{},"env":{"ENV_VAR":"value"},"created_at":"0001-01-01T00:00:00Z","meta_data":{"meta_key":"meta_value"},"creator":{"avatar_url":"","created_at":null,"email":"","id":"","name":""}}`, textContent.Text)
}

func TestCalculatePercentage(t *testing.T) {
	assert := require.New(t)

	// Test zero division case - should return 0 instead of panicking
	result := calculatePercentage(0, 0)
	assert.Equal(0, result)

	// Test normal completion percentage calculations
	result = calculatePercentage(10, 3)
	assert.Equal(70, result) // (10-3)*100/10 = 70%

	// Test 100% completion
	result = calculatePercentage(5, 0)
	assert.Equal(100, result) // (5-0)*100/5 = 100%

	// Test 0% completion
	result = calculatePercentage(8, 8)
	assert.Equal(0, result) // (8-8)*100/8 = 0%

	// Test with single item
	result = calculatePercentage(1, 0)
	assert.Equal(100, result) // (1-0)*100/1 = 100%
}

func TestGetBuildWithJobStateFilter(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

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

	tool, typedHandler, _ := GetBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	t.Run("filter by single state", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Arguments: map[string]any{
					"org_slug":      "org",
					"pipeline_slug": "pipeline",
					"build_number":  "1",
					"detail_level":  "full",
					"job_state":     "failed",
				},
			},
		}
		result, err := handler(ctx, request)
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"job2"`)
		assert.Contains(textContent.Text, `"id":"job3"`)
		assert.NotContains(textContent.Text, `"id":"job1"`)
		assert.NotContains(textContent.Text, `"id":"job4"`)
	})

	t.Run("filter by multiple states", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Arguments: map[string]any{
					"org_slug":      "org",
					"pipeline_slug": "pipeline",
					"build_number":  "1",
					"detail_level":  "full",
					"job_state":     "failed,broken",
				},
			},
		}
		result, err := handler(ctx, request)
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"job2"`)
		assert.Contains(textContent.Text, `"id":"job3"`)
		assert.Contains(textContent.Text, `"id":"job4"`)
		assert.NotContains(textContent.Text, `"id":"job1"`)
	})

	t.Run("filter with whitespace handling", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Arguments: map[string]any{
					"org_slug":      "org",
					"pipeline_slug": "pipeline",
					"build_number":  "1",
					"detail_level":  "full",
					"job_state":     "failed, broken",
				},
			},
		}
		result, err := handler(ctx, request)
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"id":"job2"`)
		assert.Contains(textContent.Text, `"id":"job3"`)
		assert.Contains(textContent.Text, `"id":"job4"`)
	})
}

func TestGetBuildWithAgentStripping(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

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

	tool, typedHandler, _ := GetBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	t.Run("include_agent false strips details", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Arguments: map[string]any{
					"org_slug":      "org",
					"pipeline_slug": "pipeline",
					"build_number":  "1",
					"detail_level":  "full",
					"include_agent": false,
				},
			},
		}
		result, err := handler(ctx, request)
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"agent1"`)
		assert.NotContains(textContent.Text, `"Agent 1"`)
		assert.NotContains(textContent.Text, `"host1"`)
	})

	t.Run("include_agent true keeps details", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Arguments: map[string]any{
					"org_slug":      "org",
					"pipeline_slug": "pipeline",
					"build_number":  "1",
					"detail_level":  "full",
					"include_agent": true,
				},
			},
		}
		result, err := handler(ctx, request)
		assert.NoError(err)

		textContent := getTextResult(t, result)
		assert.Contains(textContent.Text, `"agent1"`)
		assert.Contains(textContent.Text, `"Agent 1"`)
		assert.Contains(textContent.Text, `"host1"`)
	})
}

func TestGetBuildDetailedWithJobStateFilter(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

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

	tool, typedHandler, _ := GetBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Arguments: map[string]any{
				"org_slug":      "org",
				"pipeline_slug": "pipeline",
				"build_number":  "1",
				"detail_level":  "detailed",
				"job_state":     "failed",
			},
		},
	}
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// jobs_total should reflect ALL jobs (4)
	assert.Contains(textContent.Text, `"jobs_total":4`)
	// job_summary.total should reflect filtered jobs (2)
	assert.Contains(textContent.Text, `"total":2`)
	// job_summary.by_state should only show failed count
	assert.Contains(textContent.Text, `"failed":2`)
}

func TestGetBuildSummaryIgnoresJobFiltering(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
					ID:     "123",
					Number: 1,
					State:  "failed",
					Jobs: []buildkite.Job{
						{ID: "job1", Name: "Test 1", State: "passed"},
						{ID: "job2", Name: "Test 2", State: "failed"},
					},
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	tool, typedHandler, _ := GetBuild(client)
	handler := mcp.NewTypedToolHandler(typedHandler)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Arguments: map[string]any{
				"org_slug":      "org",
				"pipeline_slug": "pipeline",
				"build_number":  "1",
				"detail_level":  "summary",
				"job_state":     "failed", // should be ignored
			},
		},
	}
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	// jobs_total should reflect ALL jobs (2)
	assert.Contains(textContent.Text, `"jobs_total":2`)
	// No job_summary in summary level
	assert.NotContains(textContent.Text, `"job_summary"`)
}
