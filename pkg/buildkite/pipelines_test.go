package buildkite

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockPipelinesClient struct {
	GetFunc        func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error)
	ListFunc       func(ctx context.Context, org string, opt *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error)
	CreateFunc     func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	UpdateFunc     func(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	AddWebhookFunc func(ctx context.Context, org string, slug string) (*buildkite.Response, error)
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

func (m *MockPipelinesClient) Update(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, org, pipeline, p)
	}
	return buildkite.Pipeline{}, nil, nil
}

func (m *MockPipelinesClient) AddWebhook(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
	if m.AddWebhookFunc != nil {
		return m.AddWebhookFunc(ctx, org, slug)
	}
	return &buildkite.Response{Response: &http.Response{StatusCode: 201}}, nil
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

	tool, handler, _ := ListPipelines(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := ListPipelinesArgs{
		OrgSlug: "org",
	}

	result, err := handler(ctx, request, args)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.Equal(`{"headers":{"Link":""},"items":[{"id":"123","name":"Test Pipeline","slug":"test-pipeline","repository":"","default_branch":"","web_url":"","visibility":"","created_at":"0001-01-01T00:00:00Z"}]}`, textContent.Text)
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

	tool, handler, _ := GetPipeline(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := GetPipelineArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
	}

	result, err := handler(ctx, request, args)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.Equal(`{"id":"123","name":"Test Pipeline","slug":"test-pipeline","created_at":"0001-01-01T00:00:00Z","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"provider":{"id":"","webhook_url":"","settings":null}}`, textContent.Text)
}

func TestCreatePipeline(t *testing.T) {
	assert := require.New(t)

	testPipelineDefinition := `
agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
    key: "hello_step"
    label: "Hello Step"
`

	ctx := context.Background()
	webhookCalled := false
	client := &MockPipelinesClient{
		CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {

			// validate required fields
			assert.Equal("org", org)
			assert.Equal("cluster-123", p.ClusterID)
			assert.Equal("Test Pipeline", p.Name)
			assert.Equal("https://example.com/repo.git", p.Repository)
			assert.Equal(testPipelineDefinition, p.Configuration)

			return buildkite.Pipeline{
					ID:        "123",
					Slug:      "test-pipeline",
					Name:      "Test Pipeline",
					ClusterID: "cluster-123",
					CreatedAt: &buildkite.Timestamp{},
					Tags:      []string{"tag1", "tag2"},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("test-pipeline", slug)
			webhookCalled = true
			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 201,
				},
			}, nil
		},
	}

	tool, handler, _ := CreatePipeline(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := CreatePipelineArgs{
		OrgSlug:       "org",
		Name:          "Test Pipeline",
		ClusterID:     "cluster-123",
		RepositoryURL: "https://example.com/repo.git",
		Description:   "A test pipeline",
		Configuration: testPipelineDefinition,
		Tags:          []string{"tag1", "tag2"},
		CreateWebhook: nil,
	}

	result, err := handler(ctx, request, args)
	assert.NoError(err)
	assert.True(webhookCalled, "AddWebhook should have been called when CreateWebhook is nil")

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"webhook":{"created":true,"note":"Pipeline and webhook created successfully."}`)
	assert.Contains(textContent.Text, `"pipeline":{"id":"123","name":"Test Pipeline","slug":"test-pipeline"`)
}

func TestCreatePipelineWithWebhook(t *testing.T) {
	assert := require.New(t)

	testPipelineDefinition := `
agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
    key: "hello_step"
    label: "Hello Step"
`

	ctx := context.Background()
	webhookCalled := false
	createWebhook := true
	client := &MockPipelinesClient{
		CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {

			// validate required fields
			assert.Equal("org", org)
			assert.Equal("Test Pipeline", p.Name)
			assert.Equal("https://github.com/example/repo.git", p.Repository)
			assert.Equal("cluster-123", p.ClusterID)
			assert.Equal(testPipelineDefinition, p.Configuration)

			return buildkite.Pipeline{
					ID:        "123",
					Slug:      "test-pipeline",
					Name:      "Test Pipeline",
					ClusterID: "cluster-123",
					CreatedAt: &buildkite.Timestamp{},
					Tags:      []string{"tag1", "tag2"},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 201,
					},
				}, nil
		},
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {

			// validate required fields
			assert.Equal("org", org)
			assert.Equal("test-pipeline", slug)

			webhookCalled = true
			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 201,
				},
			}, nil
		},
	}

	tool, handler, _ := CreatePipeline(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := CreatePipelineArgs{
		OrgSlug:       "org",
		Name:          "Test Pipeline",
		ClusterID:     "cluster-123",
		RepositoryURL: "https://github.com/example/repo.git",
		Description:   "A test pipeline",
		Configuration: testPipelineDefinition,
		Tags:          []string{"tag1", "tag2"},
		CreateWebhook: &createWebhook,
	}

	result, err := handler(ctx, request, args)
	assert.NoError(err)
	assert.True(webhookCalled, "AddWebhook should have been called")

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"webhook":{"created":true,"note":"Pipeline and webhook created successfully."}`)
	assert.Contains(textContent.Text, `"pipeline":{"id":"123","name":"Test Pipeline","slug":"test-pipeline"`)
}

func TestCreatePipelineWithWebhookError(t *testing.T) {
	assert := require.New(t)

	testPipelineDefinition := `
agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
    key: "hello_step"
    label: "Hello Step"
`

	ctx := context.Background()
	webhookCalled := false
	createWebhook := true
	client := &MockPipelinesClient{
		CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {

			// validate required fields
			assert.Equal("org", org)
			assert.Equal("Test Pipeline", p.Name)
			assert.Equal("https://github.com/example/repo.git", p.Repository)
			assert.Equal("cluster-123", p.ClusterID)
			assert.Equal(testPipelineDefinition, p.Configuration)

			return buildkite.Pipeline{
					ID:        "123",
					Slug:      "test-pipeline",
					Name:      "Test Pipeline",
					ClusterID: "cluster-123",
					CreatedAt: &buildkite.Timestamp{},
					Tags:      []string{"tag1", "tag2"},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 201,
					},
				}, nil
		},
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			webhookCalled = true
			return nil, errors.New("Auto-creating webhooks is not supported for your repository.")
		},
	}

	tool, handler, _ := CreatePipeline(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := CreatePipelineArgs{
		OrgSlug:       "org",
		Name:          "Test Pipeline",
		ClusterID:     "cluster-123",
		RepositoryURL: "https://github.com/example/repo.git",
		Description:   "A test pipeline",
		Configuration: testPipelineDefinition,
		Tags:          []string{"tag1", "tag2"},
		CreateWebhook: &createWebhook,
	}

	result, err := handler(ctx, request, args)
	assert.NoError(err)
	assert.True(webhookCalled, "AddWebhook should have been called")

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"webhook":{"created":false,`)
	assert.Contains(textContent.Text, `"error":"Auto-creating webhooks is not supported for your repository."`)
	assert.Contains(textContent.Text, `"note":"Pipeline created successfully, but webhook creation failed.`)
}

func TestUpdatePipeline(t *testing.T) {
	assert := require.New(t)

	testPipelineDefinition := `agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
	key: "hello_step"
	label: "Hello Step"
`
	ctx := context.Background()
	client := &MockPipelinesClient{
		UpdateFunc: func(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {

			// validate required fields
			assert.Equal("org", org)
			assert.Equal("test-pipeline", pipeline)

			assert.Equal(testPipelineDefinition, p.Configuration)

			return buildkite.Pipeline{
					ID:        "123",
					Slug:      "test-pipeline",
					Name:      "Test Pipeline",
					ClusterID: "abc-123",
					CreatedAt: &buildkite.Timestamp{},
					Tags:      []string{"tag1", "tag2"},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler, _ := UpdatePipeline(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := UpdatePipelineArgs{
		OrgSlug:       "org",
		PipelineSlug:  "test-pipeline",
		Name:          "Test Pipeline",
		ClusterID:     "abc-123",
		Description:   "A test pipeline",
		Configuration: testPipelineDefinition,
		RepositoryURL: "https://example.com/repo.git",
		Tags:          []string{"tag1", "tag2"},
	}
	result, err := handler(ctx, request, args)
	assert.NoError(err)
	textContent := getTextResult(t, result)
	assert.Equal(`{"id":"123","name":"Test Pipeline","slug":"test-pipeline","created_at":"0001-01-01T00:00:00Z","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"cluster_id":"abc-123","tags":["tag1","tag2"],"provider":{"id":"","webhook_url":"","settings":null}}`, textContent.Text)
}
