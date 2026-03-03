package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockAnnotationsClient struct {
	ListByBuildFunc func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error)
	GetFunc         func(ctx context.Context, org, pipelineSlug, buildNumber, id string) (buildkite.Annotation, *buildkite.Response, error)
}

func (m *MockAnnotationsClient) ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
	if m.ListByBuildFunc != nil {
		return m.ListByBuildFunc(ctx, org, pipelineSlug, buildNumber, opts)
	}
	return nil, nil, nil
}

var _ AnnotationsClient = (*MockAnnotationsClient)(nil)

func TestListAnnotations(t *testing.T) {
	assert := require.New(t)

	client := &MockAnnotationsClient{
		ListByBuildFunc: func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
			return []buildkite.Annotation{
					{
						ID:       "1",
						BodyHTML: "Test annotation 1",
					},
					{
						ID:       "2",
						BodyHTML: "Test annotation 2",
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AnnotationsClient: client})

	tool, handler, _ := ListAnnotations()
	assert.NotNil(tool)
	assert.NotNil(handler)
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListAnnotationsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})
	assert.NoError(err)
	textContent := getTextResult(t, result)

	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"1","body_html":"Test annotation 1"},{"id":"2","body_html":"Test annotation 2"}]}`, textContent.Text)
}
