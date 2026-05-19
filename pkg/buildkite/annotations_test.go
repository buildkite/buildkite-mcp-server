package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockAnnotationsClient struct {
	ListByBuildFunc  func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error)
	CreateFunc       func(ctx context.Context, org, pipelineSlug, buildNumber string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error)
	ListByJobFunc    func(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error)
	CreateForJobFunc func(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error)
}

func (m *MockAnnotationsClient) ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
	if m.ListByBuildFunc != nil {
		return m.ListByBuildFunc(ctx, org, pipelineSlug, buildNumber, opts)
	}
	return nil, nil, nil
}

func (m *MockAnnotationsClient) Create(ctx context.Context, org, pipelineSlug, buildNumber string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, org, pipelineSlug, buildNumber, ac)
	}
	return buildkite.Annotation{}, nil, nil
}

func (m *MockAnnotationsClient) ListByJob(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
	if m.ListByJobFunc != nil {
		return m.ListByJobFunc(ctx, org, pipelineSlug, buildNumber, jobID, opts)
	}
	return nil, nil, nil
}

func (m *MockAnnotationsClient) CreateForJob(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error) {
	if m.CreateForJobFunc != nil {
		return m.CreateForJobFunc(ctx, org, pipelineSlug, buildNumber, jobID, ac)
	}
	return buildkite.Annotation{}, nil, nil
}

var _ AnnotationsClient = (*MockAnnotationsClient)(nil)

func TestListAnnotations(t *testing.T) {
	assert := require.New(t)

	client := &MockAnnotationsClient{
		ListByBuildFunc: func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
			return []buildkite.Annotation{
				{ID: "1", BodyHTML: "Test annotation 1"},
				{ID: "2", BodyHTML: "Test annotation 2"},
			}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AnnotationsClient: client})

	tool, handler, _ := ListAnnotations()
	assert.NotNil(tool)
	assert.NotNil(handler)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListAnnotationsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"1","body_html":"Test annotation 1"},{"id":"2","body_html":"Test annotation 2"}]}`, textContent.Text)
}

func TestListAnnotationsForJobScope(t *testing.T) {
	assert := require.New(t)

	client := &MockAnnotationsClient{
		ListByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
			assert.Equal("job-1", jobID)
			return []buildkite.Annotation{{ID: "1", Scope: "job", BodyHTML: "Job annotation"}}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AnnotationsClient: client})

	tool, handler, _ := ListAnnotations()
	assert.NotNil(tool)
	assert.NotNil(handler)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListAnnotationsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
		Scope:        "job",
		JobID:        "job-1",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"1","scope":"job","body_html":"Job annotation"}]}`, textContent.Text)
}

func TestListAnnotationsRequiresJobIDForJobScope(t *testing.T) {
	assert := require.New(t)

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AnnotationsClient: &MockAnnotationsClient{}})

	_, handler, _ := ListAnnotations()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListAnnotationsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
		Scope:        "job",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(getTextResult(t, result).Text, "job_id is required when scope is 'job'")
}

func TestCreateAnnotation(t *testing.T) {
	assert := require.New(t)

	client := &MockAnnotationsClient{
		CreateFunc: func(ctx context.Context, org, pipelineSlug, buildNumber string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("pipeline", pipelineSlug)
			assert.Equal("1", buildNumber)
			assert.Equal(buildkite.AnnotationCreate{
				Body:     "Hello world!",
				Context:  "greeting",
				Style:    "info",
				Priority: 5,
				Append:   true,
			}, ac)

			return buildkite.Annotation{
				ID:       "ann-1",
				Context:  "greeting",
				Style:    "info",
				Scope:    "build",
				Priority: 5,
				BodyHTML: "<p>Hello world!</p>",
			}, &buildkite.Response{Response: &http.Response{StatusCode: 201}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AnnotationsClient: client})

	tool, handler, _ := CreateAnnotation()
	assert.NotNil(tool)
	assert.NotNil(handler)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreateAnnotationArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
		Body:         "Hello world!",
		Context:      "greeting",
		Style:        "info",
		Priority:     5,
		Append:       true,
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"ann-1","context":"greeting","style":"info","scope":"build","priority":5,"body_html":"<p>Hello world!</p>"}`, textContent.Text)
}

func TestCreateAnnotationForJobScope(t *testing.T) {
	assert := require.New(t)

	client := &MockAnnotationsClient{
		CreateForJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error) {
			assert.Equal("job-1", jobID)
			assert.Equal(buildkite.AnnotationCreate{
				Body:     "Tests passed",
				Context:  "test-results",
				Style:    "success",
				Priority: 3,
				Append:   false,
			}, ac)

			return buildkite.Annotation{
				ID:       "ann-job-1",
				Context:  "test-results",
				Style:    "success",
				Scope:    "job",
				Priority: 3,
				BodyHTML: "<p>Tests passed</p>",
			}, &buildkite.Response{Response: &http.Response{StatusCode: 201}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AnnotationsClient: client})

	tool, handler, _ := CreateAnnotation()
	assert.NotNil(tool)
	assert.NotNil(handler)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreateAnnotationArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
		Scope:        "job",
		JobID:        "job-1",
		Body:         "Tests passed",
		Context:      "test-results",
		Style:        "success",
		Priority:     3,
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"ann-job-1","context":"test-results","style":"success","scope":"job","priority":3,"body_html":"<p>Tests passed</p>"}`, textContent.Text)
}

func TestCreateAnnotationRejectsInvalidScope(t *testing.T) {
	assert := require.New(t)

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AnnotationsClient: &MockAnnotationsClient{}})

	_, handler, _ := CreateAnnotation()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreateAnnotationArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
		Scope:        "wat",
		Body:         "Hello world!",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(getTextResult(t, result).Text, "scope must be 'build' or 'job'")
}
