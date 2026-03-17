package buildkite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type MockTestExecutionsClient struct {
	GetFailedExecutionsFunc func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

func (m *MockTestExecutionsClient) GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
	if m.GetFailedExecutionsFunc != nil {
		return m.GetFailedExecutionsFunc(ctx, org, slug, runID, opt)
	}
	return nil, nil, nil
}

var _ TestExecutionsClient = (*MockTestExecutionsClient)(nil)

func TestGetFailedExecutions(t *testing.T) {
	assert := require.New(t)

	failedExecutions := []buildkite.FailedExecution{
		{
			ExecutionID:   "exec-1",
			RunID:         "run-123",
			TestID:        "test-456",
			TestName:      "Test Case 1",
			FailureReason: "Assertion failed",
			Duration:      1.5,
		},
		{
			ExecutionID:   "exec-2",
			RunID:         "run-123",
			TestID:        "test-789",
			TestName:      "Test Case 2",
			FailureReason: "Timeout",
			Duration:      30.0,
		},
	}

	mockClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			return failedExecutions, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestExecutionsClient: mockClient})

	tool, handler, _ := GetFailedTestExecutions()

	// Test tool properties
	assert.Equal("get_failed_executions", tool.Name)
	assert.Equal("Get failed test executions for a specific test run in Buildkite Test Engine. Optionally get the expanded failure details such as full error messages and stack traces.", tool.Description)
	assert.True(tool.Annotations.ReadOnlyHint)

	// Test successful request
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetFailedTestExecutionsArgs{
		OrgSlug:                "org",
		TestSuiteSlug:          "suite1",
		RunID:                  "run1",
		IncludeFailureExpanded: true,
	})
	assert.NoError(err)
	assert.NotNil(result)

	// Check the result contains failed execution data
	textContent := result.Content[0].(*mcp.TextContent)
	assert.Contains(textContent.Text, "exec-1")
	assert.Contains(textContent.Text, "exec-2")
	assert.Contains(textContent.Text, "Test Case 1")
	assert.Contains(textContent.Text, "Assertion failed")
	assert.Contains(textContent.Text, "Timeout")
	// Should always have pagination metadata (defaults: page=1, per_page=25)
	assert.Contains(textContent.Text, `"page":1`)
	assert.Contains(textContent.Text, `"per_page":25`)
	assert.Contains(textContent.Text, `"total":2`)
}

func TestGetFailedExecutionsWithError(t *testing.T) {
	assert := require.New(t)

	mockClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			return []buildkite.FailedExecution{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestExecutionsClient: mockClient})

	_, handler, _ := GetFailedTestExecutions()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestGetFailedExecutionsHTTPError(t *testing.T) {
	assert := require.New(t)

	mockClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			resp := &http.Response{
				Request: &http.Request{
					Method: "GET",
					URL:    nil,
				},
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("Failed executions not found")),
			}

			return []buildkite.FailedExecution{}, &buildkite.Response{
				Response: resp,
			}, &buildkite.ErrorResponse{Response: resp, Message: "Failed executions not found"}
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestExecutionsClient: mockClient})

	_, handler, _ := GetFailedTestExecutions()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "Failed executions not found")
}

func TestGetFailedExecutionsPagination(t *testing.T) {
	assert := require.New(t)

	// Create 6 failed executions to test pagination
	failedExecutions := []buildkite.FailedExecution{
		{
			ExecutionID:   "exec-1",
			RunID:         "run-123",
			TestID:        "test-456",
			TestName:      "Test Case 1",
			FailureReason: "Assertion failed",
			Duration:      1.5,
		},
		{
			ExecutionID:   "exec-2",
			RunID:         "run-123",
			TestID:        "test-789",
			TestName:      "Test Case 2",
			FailureReason: "Timeout",
			Duration:      30.0,
		},
		{
			ExecutionID:   "exec-3",
			RunID:         "run-123",
			TestID:        "test-101",
			TestName:      "Test Case 3",
			FailureReason: "Network error",
			Duration:      5.0,
		},
		{
			ExecutionID:   "exec-4",
			RunID:         "run-123",
			TestID:        "test-102",
			TestName:      "Test Case 4",
			FailureReason: "Database error",
			Duration:      2.5,
		},
		{
			ExecutionID:   "exec-5",
			RunID:         "run-123",
			TestID:        "test-103",
			TestName:      "Test Case 5",
			FailureReason: "Memory leak",
			Duration:      10.0,
		},
		{
			ExecutionID:   "exec-6",
			RunID:         "run-123",
			TestID:        "test-104",
			TestName:      "Test Case 6",
			FailureReason: "Segmentation fault",
			Duration:      0.1,
		},
	}

	mockClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			return failedExecutions, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestExecutionsClient: mockClient})

	tool, handler, _ := GetFailedTestExecutions()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test first page with page size of 2
	resultFirstPage, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
		Page:          1,
		PerPage:       2,
	})
	assert.NoError(err)

	textContentFirstPage := resultFirstPage.Content[0].(*mcp.TextContent)
	// Should contain first 2 executions
	assert.Contains(textContentFirstPage.Text, "exec-1")
	assert.Contains(textContentFirstPage.Text, "exec-2")
	assert.NotContains(textContentFirstPage.Text, "exec-3")
	assert.NotContains(textContentFirstPage.Text, "exec-4")
	// Should have pagination metadata
	assert.Contains(textContentFirstPage.Text, `"page":1`)
	assert.Contains(textContentFirstPage.Text, `"per_page":2`)
	assert.Contains(textContentFirstPage.Text, `"total":6`)
	assert.Contains(textContentFirstPage.Text, `"has_next":true`)
	assert.Contains(textContentFirstPage.Text, `"has_prev":false`)

	// Test second page with page size of 2
	resultSecondPage, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
		Page:          2,
		PerPage:       2,
	})
	assert.NoError(err)

	textContentSecondPage := resultSecondPage.Content[0].(*mcp.TextContent)
	// Should contain next 2 executions
	assert.NotContains(textContentSecondPage.Text, "exec-1")
	assert.NotContains(textContentSecondPage.Text, "exec-2")
	assert.Contains(textContentSecondPage.Text, "exec-3")
	assert.Contains(textContentSecondPage.Text, "exec-4")
	// Should have pagination metadata
	assert.Contains(textContentSecondPage.Text, `"page":2`)
	assert.Contains(textContentSecondPage.Text, `"per_page":2`)
	assert.Contains(textContentSecondPage.Text, `"total":6`)
	assert.Contains(textContentSecondPage.Text, `"has_next":true`)
	assert.Contains(textContentSecondPage.Text, `"has_prev":true`)

	// Test last page
	resultLastPage, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
		Page:          3,
		PerPage:       2,
	})
	assert.NoError(err)

	textContentLastPage := resultLastPage.Content[0].(*mcp.TextContent)
	// Should contain last 2 executions
	assert.Contains(textContentLastPage.Text, "exec-5")
	assert.Contains(textContentLastPage.Text, "exec-6")
	// Should have pagination metadata
	assert.Contains(textContentLastPage.Text, `"page":3`)
	assert.Contains(textContentLastPage.Text, `"per_page":2`)
	assert.Contains(textContentLastPage.Text, `"total":6`)
	assert.Contains(textContentLastPage.Text, `"has_next":false`)
	assert.Contains(textContentLastPage.Text, `"has_prev":true`)

	// Test page beyond available data
	resultBeyond, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
		Page:          5,
		PerPage:       2,
	})
	assert.NoError(err)

	textContentBeyond := resultBeyond.Content[0].(*mcp.TextContent)
	// Should contain empty items array
	assert.Contains(textContentBeyond.Text, `"items":[]`)
}

func TestGetFailedExecutionsLargePage(t *testing.T) {
	assert := require.New(t)

	failedExecutions := []buildkite.FailedExecution{
		{
			ExecutionID:   "exec-1",
			RunID:         "run-123",
			TestID:        "test-456",
			TestName:      "Test Case 1",
			FailureReason: "Assertion failed",
			Duration:      1.5,
		},
		{
			ExecutionID:   "exec-2",
			RunID:         "run-123",
			TestID:        "test-789",
			TestName:      "Test Case 2",
			FailureReason: "Timeout",
			Duration:      30.0,
		},
	}

	mockClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			return failedExecutions, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestExecutionsClient: mockClient})

	tool, handler, _ := GetFailedTestExecutions()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test with page size larger than available data
	resultLargePage, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
		Page:          1,
		PerPage:       10,
	})
	assert.NoError(err)

	textContentLargePage := resultLargePage.Content[0].(*mcp.TextContent)
	// Should contain all executions
	assert.Contains(textContentLargePage.Text, "exec-1")
	assert.Contains(textContentLargePage.Text, "exec-2")
	// Should have pagination metadata
	assert.Contains(textContentLargePage.Text, `"page":1`)
	assert.Contains(textContentLargePage.Text, `"per_page":10`)
	assert.Contains(textContentLargePage.Text, `"total":2`)
	assert.Contains(textContentLargePage.Text, `"has_next":false`)
	assert.Contains(textContentLargePage.Text, `"has_prev":false`)
}
