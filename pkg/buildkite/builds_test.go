package buildkite

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
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

func TestGetBuild(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, handler, scopes := GetBuild()
		require.Equal(t, "get_build", tool.Name)
		require.True(t, tool.Annotations.ReadOnlyHint)
		require.Equal(t, []string{"read_builds"}, scopes)
		require.NotNil(t, handler)
	})

	t.Run("ReturnsBuildAndExcludesJobs", func(t *testing.T) {
		assert := require.New(t)

		var capturedOptions *buildkite.BuildGetOptions
		client := &MockBuildsClient{
			GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
				capturedOptions = opt
				assert.Equal("org", org)
				assert.Equal("pipeline", pipeline)
				assert.Equal("1", id)
				return buildkite.Build{
						ID:     "123",
						Number: 1,
						State:  "passed",
						Branch: "main",
						Env: map[string]any{
							"SECRET_TOKEN": "redacted",
						},
						Jobs: []buildkite.Job{{
							ID: "job-1",
						}},
						Pipeline: &buildkite.Pipeline{
							ID:            "pipeline-1",
							Configuration: "steps:\n  - command: echo secret",
							Env:           map[string]any{"PIPELINE_SECRET": "redacted"},
						},
						CreatedAt: &buildkite.Timestamp{},
					}, &buildkite.Response{
						Response: &http.Response{StatusCode: 200},
					}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := GetBuild()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			BuildNumber:  "1",
		})
		assert.NoError(err)

		text := getTextResult(t, result).Text
		assert.Contains(text, `"id":"123"`)
		assert.Contains(text, `"number":1`)
		assert.Contains(text, `"state":"passed"`)
		// Job detail lives in list_jobs/get_job, not here.
		assert.NotContains(text, `"jobs":`)
		// Build env and pipeline config are intentionally omitted from read_builds.
		assert.NotContains(text, `"env":`)
		assert.NotContains(text, `"pipeline":`)
		assert.NotContains(text, "SECRET_TOKEN")
		assert.NotContains(text, "PIPELINE_SECRET")
		assert.NotContains(text, "steps:")

		// The handler must exclude jobs and pipeline detail from the API request.
		require.NotNil(t, capturedOptions)
		assert.True(capturedOptions.ExcludeJobs)
		assert.True(capturedOptions.ExcludePipeline)
		assert.True(capturedOptions.IncludeTestEngine)
	})

	t.Run("APIError", func(t *testing.T) {
		assert := require.New(t)

		client := &MockBuildsClient{
			GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
				return buildkite.Build{}, nil, &buildkite.ErrorResponse{
					RawBody:  []byte("build not found"),
					Response: &http.Response{StatusCode: 404},
				}
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := GetBuild()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			BuildNumber:  "1",
		})
		assert.NoError(err)
		assert.True(result.IsError)
		assert.Contains(getTextResult(t, result).Text, "build not found")
	})
}

func TestListBuilds(t *testing.T) {
	t.Run("ToolDefinition", func(t *testing.T) {
		tool, handler, scopes := ListBuilds()
		require.Equal(t, "list_builds", tool.Name)
		require.True(t, tool.Annotations.ReadOnlyHint)
		require.Equal(t, []string{"read_builds"}, scopes)
		require.NotNil(t, handler)
	})

	t.Run("DefaultsAndSummaryOutput", func(t *testing.T) {
		assert := require.New(t)

		var capturedOptions *buildkite.BuildsListOptions
		client := &MockBuildsClient{
			ListByPipelineFunc: func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
				capturedOptions = opt
				return []buildkite.Build{
						{ID: "123", Number: 1, State: "running", CreatedAt: &buildkite.Timestamp{}},
					}, &buildkite.Response{
						Response: &http.Response{StatusCode: 200},
					}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := ListBuilds()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListBuildsArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
		})
		assert.NoError(err)

		text := getTextResult(t, result).Text
		assert.Contains(text, `"headers":{"Link":""}`)
		assert.Contains(text, `"items":[`)
		assert.Contains(text, `"id":"123"`)
		assert.Contains(text, `"state":"running"`)
		assert.NotContains(text, `"job_summary"`)
		assert.NotContains(text, `"jobs":`)

		require.NotNil(t, capturedOptions)
		assert.Equal(1, capturedOptions.Page)
		assert.Equal(30, capturedOptions.PerPage)
		assert.True(capturedOptions.ExcludeJobs)
		assert.True(capturedOptions.ExcludePipeline)
		assert.Nil(capturedOptions.Branch)
	})

	t.Run("CustomPaginationAndFilters", func(t *testing.T) {
		assert := require.New(t)

		var capturedOptions *buildkite.BuildsListOptions
		client := &MockBuildsClient{
			ListByPipelineFunc: func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
				capturedOptions = opt
				return []buildkite.Build{}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := ListBuilds()

		_, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListBuildsArgs{
			OrgSlug:      "org",
			PipelineSlug: "pipeline",
			Branch:       "feature",
			State:        "failed",
			Commit:       "abc123",
			Creator:      "user-1",
			Page:         3,
			PerPage:      50,
		})
		assert.NoError(err)

		require.NotNil(t, capturedOptions)
		assert.Equal(3, capturedOptions.Page)
		assert.Equal(50, capturedOptions.PerPage)
		assert.Equal([]string{"feature"}, capturedOptions.Branch)
		assert.Equal([]string{"failed"}, capturedOptions.State)
		assert.Equal("abc123", capturedOptions.Commit)
		assert.Equal("user-1", capturedOptions.Creator)
	})

	t.Run("OrgScopedWhenPipelineOmitted", func(t *testing.T) {
		assert := require.New(t)

		called := false
		client := &MockBuildsClient{
			ListByOrgFunc: func(ctx context.Context, org string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
				called = true
				assert.Equal("org", org)
				return []buildkite.Build{
						{ID: "123", Number: 1, State: "passed", CreatedAt: &buildkite.Timestamp{}},
					}, &buildkite.Response{
						Response: &http.Response{StatusCode: 200},
					}, nil
			},
			ListByPipelineFunc: func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
				t.Fatal("ListByPipeline should not be called when pipeline_slug is omitted")
				return nil, nil, nil
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := ListBuilds()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListBuildsArgs{
			OrgSlug: "org",
		})
		assert.NoError(err)
		assert.True(called)
		assert.Contains(getTextResult(t, result).Text, `"id":"123"`)
	})

	t.Run("APIError", func(t *testing.T) {
		assert := require.New(t)

		client := &MockBuildsClient{
			ListByOrgFunc: func(ctx context.Context, org string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
				return nil, nil, &buildkite.ErrorResponse{
					RawBody:  []byte("organization not found"),
					Response: &http.Response{StatusCode: 404},
				}
			},
		}

		ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client})
		_, handler, _ := ListBuilds()

		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListBuildsArgs{
			OrgSlug: "bad-org",
		})
		assert.NoError(err)
		assert.True(result.IsError)
		assert.Contains(getTextResult(t, result).Text, "organization not found")
	})
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
		require.Equal(t, boolPtr(true), tool.Annotations.DestructiveHint)
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
