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
			assert.True(opt.IncludeFailureExpanded)
			assert.Equal(0, opt.Page)
			assert.Equal(0, opt.PerPage)

			return failedExecutions, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Link": []string{`<https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs/run1/failed_executions?page=2>; rel="next"`},
					},
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
	assert.Contains(textContent.Text, `"headers":{"Link":"\u003c`)
	assert.Contains(textContent.Text, `failed_executions?page=2\u003e; rel=\"next\"`)
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

	failedExecutions := []buildkite.FailedExecution{
		{
			ExecutionID:   "exec-1",
			RunID:         "run-123",
			TestID:        "test-456",
			TestName:      "Test Case 1",
			FailureReason: "Assertion failed",
			Duration:      1.5,
		},
	}

	var gotOptions *buildkite.FailedExecutionsOptions
	mockClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			gotOptions = opt

			return failedExecutions, &buildkite.Response{
				Response: &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Link": []string{`<https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/runs/run1/failed_executions?page=3>; rel="next"`},
					},
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestExecutionsClient: mockClient})

	tool, handler, _ := GetFailedTestExecutions()
	assert.NotNil(tool)
	assert.NotNil(handler)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetFailedTestExecutionsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		RunID:         "run1",
		Page:          2,
		PerPage:       50,
	})
	assert.NoError(err)

	assert.Equal(&buildkite.FailedExecutionsOptions{
		Page:    2,
		PerPage: 50,
	}, gotOptions)

	textContent := result.Content[0].(*mcp.TextContent)
	assert.Contains(textContent.Text, "exec-1")
	assert.Contains(textContent.Text, `"headers":{"Link":"\u003c`)
	assert.Contains(textContent.Text, `failed_executions?page=3\u003e; rel=\"next\"`)
	assert.NotContains(textContent.Text, `"page":`)
	assert.NotContains(textContent.Text, `"per_page":`)
}
