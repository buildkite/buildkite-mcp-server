package buildkite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type MockTestRunsClient struct {
	GetFunc                 func(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error)
	ListFunc                func(ctx context.Context, org, slug string, opt *buildkite.TestRunsListOptions) ([]buildkite.TestRun, *buildkite.Response, error)
	GetFailedExecutionsFunc func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

func (m *MockTestRunsClient) Get(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, slug, runID)
	}
	return buildkite.TestRun{}, nil, nil
}

func (m *MockTestRunsClient) List(ctx context.Context, org, slug string, opt *buildkite.TestRunsListOptions) ([]buildkite.TestRun, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, slug, opt)
	}
	return nil, nil, nil
}

func (m *MockTestRunsClient) GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
	if m.GetFailedExecutionsFunc != nil {
		return m.GetFailedExecutionsFunc(ctx, org, slug, runID, opt)
	}
	return nil, nil, nil
}

var _ TestRunsClient = (*MockTestRunsClient)(nil)

func TestListTestRuns(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	testRuns := []buildkite.TestRun{
		{
			ID:        "run1",
			URL:       "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs/run1",
			WebURL:    "https://buildkite.com/org/analytics/suites/suite1/runs/run1",
			Branch:    "main",
			CommitSHA: "abc123",
		},
		{
			ID:        "run2",
			URL:       "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs/run2",
			WebURL:    "https://buildkite.com/org/analytics/suites/suite1/runs/run2",
			Branch:    "feature",
			CommitSHA: "def456",
		},
	}

	mockClient := &MockTestRunsClient{
		ListFunc: func(ctx context.Context, org, slug string, opt *buildkite.TestRunsListOptions) ([]buildkite.TestRun, *buildkite.Response, error) {
			return testRuns, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Link": []string{"<https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs?page=2>; rel=\"next\""}},
				},
			}, nil
		},
	}

	tool, handler, _ := ListTestRuns(mockClient)

	// Test tool properties
	assert.Equal("list_test_runs", tool.Name)
	assert.Equal("List all test runs for a test suite in Buildkite Test Engine", tool.Description)
	if tool.Annotations.ReadOnlyHint != nil {
		assert.True(*tool.Annotations.ReadOnlyHint)
	}

	// Test successful request
	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
		"page":            1,
		"perPage":         30,
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.NotNil(result)

	// Check the result contains paginated data
	textContent := result.Content[0].(mcp.TextContent)
	assert.Contains(textContent.Text, "run1")
	assert.Contains(textContent.Text, "run2")
	assert.Contains(textContent.Text, "abc123")
	assert.Contains(textContent.Text, "def456")
	assert.Contains(textContent.Text, "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs?page=2")
}

func TestListTestRunsWithError(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{
		ListFunc: func(ctx context.Context, org, slug string, opt *buildkite.TestRunsListOptions) ([]buildkite.TestRun, *buildkite.Response, error) {
			return []buildkite.TestRun{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	_, handler, _ := ListTestRuns(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "API error")
}

func TestListTestRunsMissingOrg(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{}

	_, handler, _ := ListTestRuns(mockClient)

	request := createMCPRequest(t, map[string]any{
		"test_suite_slug": "suite1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "org")
}

func TestListTestRunsMissingTestSuiteSlug(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{}

	_, handler, _ := ListTestRuns(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug": "org",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "test_suite_slug")
}

func TestGetTestRun(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	testRun := buildkite.TestRun{
		ID:        "run1",
		URL:       "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs/run1",
		WebURL:    "https://buildkite.com/org/analytics/suites/suite1/runs/run1",
		Branch:    "main",
		CommitSHA: "abc123",
	}

	mockClient := &MockTestRunsClient{
		GetFunc: func(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error) {
			return testRun, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
				},
			}, nil
		},
	}

	tool, handler, _ := GetTestRun(mockClient)

	// Test tool properties
	assert.Equal("get_test_run", tool.Name)
	assert.Equal("Get a specific test run in Buildkite Test Engine", tool.Description)
	if tool.Annotations.ReadOnlyHint != nil {
		assert.True(*tool.Annotations.ReadOnlyHint)
	}

	// Test successful request
	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
		"run_id":          "run1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.NotNil(result)

	// Check the result contains test run data
	textContent := result.Content[0].(mcp.TextContent)
	assert.Contains(textContent.Text, "run1")
	assert.Contains(textContent.Text, "abc123")
	assert.Contains(textContent.Text, "main")
}

func TestGetTestRunWithError(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{
		GetFunc: func(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error) {
			return buildkite.TestRun{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	_, handler, _ := GetTestRun(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
		"run_id":          "run1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "API error")
}

func TestGetTestRunMissingOrg(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{}

	_, handler, _ := GetTestRun(mockClient)

	request := createMCPRequest(t, map[string]any{
		"test_suite_slug": "suite1",
		"run_id":          "run1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "org")
}

func TestGetTestRunMissingTestSuiteSlug(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{}

	_, handler, _ := GetTestRun(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug": "org",
		"run_id":   "run1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "test_suite_slug")
}

func TestGetTestRunMissingRunID(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{}

	_, handler, _ := GetTestRun(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "run_id")
}

func TestGetTestRunReturnsBuildID(t *testing.T) {
	// This test documents the expected behavior that build_id should be returned
	// from the GetTestRun endpoint, as per the Buildkite API documentation:
	// https://buildkite.com/docs/apis/rest-api/test-engine/runs
	//
	// Currently fails because go-buildkite/v4 TestRun struct is missing the BuildID field.
	// See: https://github.com/buildkite/go-buildkite/issues/XXX
	assert := require.New(t)

	ctx := context.Background()
	testRun := buildkite.TestRun{
		ID:        "run1",
		URL:       "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs/run1",
		WebURL:    "https://buildkite.com/org/analytics/suites/suite1/runs/run1",
		Branch:    "main",
		CommitSHA: "abc123",
		// BuildID: "89c02425-7712-4ee5-a694-c94b56b4d54c", // Cannot set - field doesn't exist in go-buildkite/v4
	}

	mockClient := &MockTestRunsClient{
		GetFunc: func(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error) {
			return testRun, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
				},
			}, nil
		},
	}

	_, handler, _ := GetTestRun(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
		"run_id":          "run1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.NotNil(result)

	textContent := result.Content[0].(mcp.TextContent)
	// This assertion documents the expected behavior - build_id should be in the response
	// Will fail until go-buildkite adds BuildID to TestRun struct
	assert.Contains(textContent.Text, "build_id", "TestRun response should contain build_id field per Buildkite API spec")
}

func TestGetTestRunHTTPError(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{
		GetFunc: func(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error) {
			return buildkite.TestRun{}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("Test run not found")),
				},
			}, nil
		},
	}

	_, handler, _ := GetTestRun(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
		"run_id":          "run1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "Test run not found")
}

func TestListTestRunsHTTPError(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockClient := &MockTestRunsClient{
		ListFunc: func(ctx context.Context, org, slug string, opt *buildkite.TestRunsListOptions) ([]buildkite.TestRun, *buildkite.Response, error) {
			resp := &http.Response{
				Request:    &http.Request{Method: "GET"},
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader("Access denied")),
			}
			return []buildkite.TestRun{}, &buildkite.Response{
				Response: resp,
			}, &buildkite.ErrorResponse{Response: resp, Message: "Access denied"}
		},
	}

	_, handler, _ := ListTestRuns(mockClient)

	request := createMCPRequest(t, map[string]any{
		"org_slug":        "org",
		"test_suite_slug": "suite1",
	})

	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "Access denied")
}
