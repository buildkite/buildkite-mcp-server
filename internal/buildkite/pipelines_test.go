package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockPipelinesClient struct {
	GetFunc    func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error)
	ListFunc   func(ctx context.Context, org string, opt *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error)
	CreateFunc func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
}

func (m *MockPipelinesClient) Get(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, pipeline)
	}
	return buildkite.Pipeline{}, nil, nil
}

func (m *MockPipelinesClient) List(ctx context.Context, org string, opt *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, opt)
	}
	return nil, nil, nil
}

func (m *MockPipelinesClient) Create(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, org, p)
	}
	return buildkite.Pipeline{}, nil, nil
}

var _ PipelinesClient = (*MockPipelinesClient)(nil)

func TestListPipelines(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockPipelinesClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error) {
			return []buildkite.Pipeline{
					{
						ID:        "123",
						Slug:      "test-pipeline",
						Name:      "Test Pipeline",
						CreatedAt: &buildkite.Timestamp{},
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler := ListPipelines(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org": "org",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.Equal(`[{"id":"123","name":"Test Pipeline","slug":"test-pipeline","created_at":"0001-01-01T00:00:00Z","provider":{"id":"","webhook_url":"","settings":null}}]`, textContent.Text)
}

func TestGetPipeline(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockPipelinesClient{
		GetFunc: func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
			return buildkite.Pipeline{
					ID:        "123",
					Slug:      "test-pipeline",
					Name:      "Test Pipeline",
					CreatedAt: &buildkite.Timestamp{},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler := GetPipeline(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org":           "org",
		"pipeline_slug": "pipeline",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.Equal(`{"id":"123","name":"Test Pipeline","slug":"test-pipeline","created_at":"0001-01-01T00:00:00Z","provider":{"id":"","webhook_url":"","settings":null}}`, textContent.Text)
}

func TestCreatePipeline(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockPipelinesClient{
		CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
			return buildkite.Pipeline{

					ID:        "123",
					Slug:      "test-pipeline",
					Name:      "Test Pipeline",
					CreatedAt: &buildkite.Timestamp{},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler := CreatePipeline(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org":            "org",
		"name":           "Test Pipeline",
		"repository_url": "https://example.com/repo.git",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)
	textContent := getTextResult(t, result)
	assert.Equal(`{"id":"123","name":"Test Pipeline","slug":"test-pipeline","created_at":"0001-01-01T00:00:00Z","provider":{"id":"","webhook_url":"","settings":null}}`, textContent.Text)
}
