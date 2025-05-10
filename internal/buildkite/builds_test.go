package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockBuildsClient struct {
	ListByPipelineFunc func(ctx context.Context, org string, pipeline string, opt *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
	GetFunc            func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildsListOptions) (buildkite.Build, *buildkite.Response, error)
}

func (m *MockBuildsClient) Get(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildsListOptions) (buildkite.Build, *buildkite.Response, error) {
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

var _ BuildsClient = (*MockBuildsClient)(nil)

func TestGetBuild(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockBuildsClient{
		GetFunc: func(ctx context.Context, org string, pipeline string, id string, opt *buildkite.BuildsListOptions) (buildkite.Build, *buildkite.Response, error) {
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

	request := createMCPRequest(t, map[string]any{
		"org":           "org",
		"pipeline_slug": "pipeline",
		"build_number":  "1",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.Equal(`{"id":"123","number":1,"state":"running","author":{},"created_at":"0001-01-01T00:00:00Z","creator":{"avatar_url":"","created_at":null,"email":"","id":"","name":""}}`, textContent.Text)
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

	assert.Equal(`[{"id":"123","number":1,"state":"running","author":{},"created_at":"0001-01-01T00:00:00Z","creator":{"avatar_url":"","created_at":null,"email":"","id":"","name":""}}]`, textContent.Text)
	
	// Verify default pagination parameters - ensure they are set to 1 per page
	assert.NotNil(capturedOptions)
	assert.Equal(1, capturedOptions.Page)
	assert.Equal(1, capturedOptions.PerPage)
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
}