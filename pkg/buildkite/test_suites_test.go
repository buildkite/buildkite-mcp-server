package buildkite

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type MockTestSuitesClient struct {
	ListByPipelineFunc func(ctx context.Context, org, pipelineSlug string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error)
}

func (m *MockTestSuitesClient) ListByPipeline(ctx context.Context, org, pipelineSlug string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error) {
	if m.ListByPipelineFunc != nil {
		return m.ListByPipelineFunc(ctx, org, pipelineSlug, opt)
	}
	return nil, nil, nil
}

var _ TestSuitesClient = (*MockTestSuitesClient)(nil)

func TestListTestSuitesForPipeline(t *testing.T) {
	assert := require.New(t)

	testSuites := []buildkite.TestSuite{
		{
			ID:            "suite-uuid-1",
			Slug:          "suite1",
			Name:          "Suite One",
			URL:           "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1",
			WebURL:        "https://buildkite.com/organizations/org/analytics/suites/suite1",
			DefaultBranch: "main",
		},
		{
			ID:            "suite-uuid-2",
			Slug:          "suite2",
			Name:          "Suite Two",
			URL:           "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite2",
			WebURL:        "https://buildkite.com/organizations/org/analytics/suites/suite2",
			DefaultBranch: "main",
		},
	}

	var gotOrg, gotPipeline string
	var gotOpts *buildkite.TestSuiteListOptions

	mockClient := &MockTestSuitesClient{
		ListByPipelineFunc: func(ctx context.Context, org, pipelineSlug string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error) {
			gotOrg, gotPipeline, gotOpts = org, pipelineSlug, opt
			return testSuites, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Link": []string{"<https://api.buildkite.com/v2/analytics/organizations/org/pipelines/pipeline1/suites?page=2>; rel=\"next\""}},
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: mockClient})

	tool, handler, scopes := ListTestSuitesForPipeline()

	assert.Equal("list_test_suites_for_pipeline", tool.Name)
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Equal([]string{"read_suites"}, scopes)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListTestSuitesForPipelineArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline1",
		Page:         2,
		PerPage:      30,
	})
	assert.NoError(err)
	assert.NotNil(result)

	assert.Equal("org", gotOrg)
	assert.Equal("pipeline1", gotPipeline)
	assert.Equal(2, gotOpts.Page)
	assert.Equal(30, gotOpts.PerPage)

	textContent := result.Content[0].(*mcp.TextContent)
	assert.Contains(textContent.Text, "suite1")
	assert.Contains(textContent.Text, "suite2")
	assert.Contains(textContent.Text, "Suite One")
	assert.Contains(textContent.Text, "https://api.buildkite.com/v2/analytics/organizations/org/pipelines/pipeline1/suites?page=2")
}

func TestListTestSuitesForPipelineDefaultsPagination(t *testing.T) {
	assert := require.New(t)

	var gotOpts *buildkite.TestSuiteListOptions

	mockClient := &MockTestSuitesClient{
		ListByPipelineFunc: func(ctx context.Context, org, pipelineSlug string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error) {
			gotOpts = opt
			return []buildkite.TestSuite{}, &buildkite.Response{Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: mockClient})

	_, handler, _ := ListTestSuitesForPipeline()

	request := createMCPRequest(t, map[string]any{})
	_, _, err := handler(ctx, request, ListTestSuitesForPipelineArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline1",
	})
	assert.NoError(err)
	assert.Equal(1, gotOpts.Page)
	assert.Equal(100, gotOpts.PerPage)
}

func TestListTestSuitesForPipelineWithError(t *testing.T) {
	assert := require.New(t)

	mockClient := &MockTestSuitesClient{
		ListByPipelineFunc: func(ctx context.Context, org, pipelineSlug string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error) {
			return nil, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: mockClient})

	_, handler, _ := ListTestSuitesForPipeline()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListTestSuitesForPipelineArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline1",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}
