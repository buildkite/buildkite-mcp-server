package buildkite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type MockTestSuitesClient struct {
	ListFunc func(ctx context.Context, org string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error)
	GetFunc  func(ctx context.Context, org, slug string) (buildkite.TestSuite, *buildkite.Response, error)
}

func (m *MockTestSuitesClient) List(ctx context.Context, org string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error) {
	return m.ListFunc(ctx, org, opt)
}

func (m *MockTestSuitesClient) Get(ctx context.Context, org, slug string) (buildkite.TestSuite, *buildkite.Response, error) {
	return m.GetFunc(ctx, org, slug)
}

var _ TestSuitesClient = (*MockTestSuitesClient)(nil)

func TestListTestSuites(t *testing.T) {
	var receivedOptions *buildkite.TestSuiteListOptions
	client := &MockTestSuitesClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			receivedOptions = opt
			return []buildkite.TestSuite{
					{ID: "suite-id-1", Slug: "rspec", Name: "RSpec"},
					{ID: "suite-id-2", Slug: "jest", Name: "Jest"},
				}, &buildkite.Response{Response: &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Link": []string{"<https://api.buildkite.com/v2/analytics/organizations/org/suites?page=2>; rel=\"next\""},
					},
				}}, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: client})
	tool, handler, scopes := ListTestSuites()

	require.Equal(t, "list_test_suites", tool.Name)
	require.Equal(t, "List all test suites for an organization in Buildkite Test Engine", tool.Description)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_suites"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTestSuitesArgs{
		OrgSlug: "org",
		Page:    1,
		PerPage: 30,
	})
	require.NoError(t, err)
	require.Equal(t, 1, receivedOptions.Page)
	require.Equal(t, 30, receivedOptions.PerPage)
	text := result.Content[0].(*mcp.TextContent).Text
	require.Contains(t, text, "rspec")
	require.Contains(t, text, "Jest")
	require.Contains(t, text, "suites?page=2")
}

func TestListTestSuitesWithError(t *testing.T) {
	client := &MockTestSuitesClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error) {
			return nil, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: client})
	_, handler, _ := ListTestSuites()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTestSuitesArgs{OrgSlug: "org"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestGetTestSuite(t *testing.T) {
	client := &MockTestSuitesClient{
		GetFunc: func(ctx context.Context, org, slug string) (buildkite.TestSuite, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "rspec", slug)
			return buildkite.TestSuite{
				ID: "suite-id", Slug: "rspec", Name: "RSpec", DefaultBranch: "main",
			}, &buildkite.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: client})
	tool, handler, scopes := GetTestSuite()

	require.Equal(t, "get_test_suite", tool.Name)
	require.Equal(t, "Get a specific test suite in Buildkite Test Engine", tool.Description)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_suites"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetTestSuiteArgs{
		OrgSlug: "org", TestSuiteSlug: "rspec",
	})
	require.NoError(t, err)
	text := result.Content[0].(*mcp.TextContent).Text
	require.Contains(t, text, "suite-id")
	require.Contains(t, text, "rspec")
	require.Contains(t, text, "main")
}

func TestGetTestSuiteWithError(t *testing.T) {
	client := &MockTestSuitesClient{
		GetFunc: func(ctx context.Context, org, slug string) (buildkite.TestSuite, *buildkite.Response, error) {
			return buildkite.TestSuite{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: client})
	_, handler, _ := GetTestSuite()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetTestSuiteArgs{
		OrgSlug: "org", TestSuiteSlug: "rspec",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestGetTestSuiteHTTPError(t *testing.T) {
	client := &MockTestSuitesClient{
		GetFunc: func(ctx context.Context, org, slug string) (buildkite.TestSuite, *buildkite.Response, error) {
			return buildkite.TestSuite{}, &buildkite.Response{Response: &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("Test suite not found")),
			}}, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestSuitesClient: client})
	_, handler, _ := GetTestSuite()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetTestSuiteArgs{
		OrgSlug: "org", TestSuiteSlug: "missing",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "Test suite not found")
}
