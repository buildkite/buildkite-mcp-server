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

	tool, handler := GetBuild(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test default behavior - jobs always excluded, summary always included
	request := createMCPRequest(t, map[string]any{
		"org":           "org",
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Equal(`{"id":"123","number":1,"state":"running","blocked":false,"author":{},"created_at":"0001-01-01T00:00:00Z","creator":{"avatar_url":"","created_at":null,"email":"","id":"","name":""},"job_summary":{"total":0,"by_state":{}}}`, textContent.Text)
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

	tool, handler := GetBuild(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test behavior - jobs always excluded, summary always shown
	request := createMCPRequest(t, map[string]any{
		"org":           "org",
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

	tool, handler := ListBuilds(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org":           "org",
		"pipeline_slug": "pipeline",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.Equal(`{"headers":{"Link":""},"items":[{"id":"123","number":1,"state":"running","blocked":false,"author":{},"created_at":"0001-01-01T00:00:00Z","creator":{"avatar_url":"","created_at":null,"email":"","id":"","name":""}}]}`, textContent.Text)

	// Verify default pagination parameters - ensure they are set to 1 per page
	assert.NotNil(capturedOptions)
	assert.Equal(1, capturedOptions.Page)
	assert.Equal(1, capturedOptions.PerPage)
	assert.Nil(capturedOptions.Branch) // Branch should be nil when not specified
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

	tool, handler := ListBuilds(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with custom pagination parameters
	request := createMCPRequest(t, map[string]any{
		"org":           "org",
		"pipeline_slug": "pipeline",
		"page":          float64(3),
		"perPage":       float64(50),
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

	tool, handler := ListBuilds(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with branch filter
	request := createMCPRequest(t, map[string]any{
		"org":           "org",
		"pipeline_slug": "pipeline",
		"branch":        "main",
	})
	_, err := handler(ctx, request)
	assert.NoError(err)

	// Verify branch filter was applied
	assert.NotNil(capturedOptions)
	assert.Equal([]string{"main"}, capturedOptions.Branch)
	assert.Equal(1, capturedOptions.Page)
	assert.Equal(1, capturedOptions.PerPage)
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

	tool, handler := GetBuildTestEngineRuns(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test tool properties
	assert.Equal("get_build_test_engine_runs", tool.Name)
	assert.Contains(tool.Description, "test engine runs")

	// Test successful request
	request := createMCPRequest(t, map[string]any{
		"org":           "org",
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

	_, handler := GetBuildTestEngineRuns(ctx, client)

	request := createMCPRequest(t, map[string]any{
		"org":           "org",
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

	_, handler := GetBuildTestEngineRuns(ctx, client)

	// Test missing org parameter
	request := createMCPRequest(t, map[string]any{
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "org")

	// Test missing pipeline_slug parameter
	request = createMCPRequest(t, map[string]any{
		"org":          "org",
		"build_number": "1",
	})
	result, err = handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "pipeline_slug")

	// Test missing build_number parameter
	request = createMCPRequest(t, map[string]any{
		"org":           "org",
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

	tool, handler := CreateBuild(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org":           "org",
		"pipeline_slug": "pipeline",
	})

	args := CreateBuildArgs{
		Commit:  "abc123",
		Message: "Test build",
		Branch:  "main",
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
	assert.Equal(`{"id":"123","number":1,"state":"created","blocked":false,"author":{},"env":{"ENV_VAR":"value"},"created_at":"0001-01-01T00:00:00Z","meta_data":{"meta_key":"meta_value"},"creator":{"avatar_url":"","created_at":null,"email":"","id":"","name":""}}`, textContent.Text)
}
