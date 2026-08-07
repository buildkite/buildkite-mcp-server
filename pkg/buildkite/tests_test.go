package buildkite

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

type MockTestsClient struct {
	GetFunc  func(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error)
	ListFunc func(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error)
}

func (m *MockTestsClient) Get(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, slug, testID)
	}
	return buildkite.Test{}, nil, nil
}

func (m *MockTestsClient) List(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, slug, opt)
	}
	return nil, nil, nil
}

var _ TestsClient = (*MockTestsClient)(nil)

func TestListTests(t *testing.T) {
	minTimestamp := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	maxTimestamp := time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC)
	reliability := 0.97

	client := &MockTestsClient{
		ListFunc: func(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "suite1", slug)
			require.Equal(t, buildkite.ListOptions{Page: 2, PerPage: 50}, opt.ListOptions)
			require.Equal(t, "7days", opt.Period)
			require.Equal(t, minTimestamp, opt.MinTimestamp)
			require.Equal(t, maxTimestamp, opt.MaxTimestamp)
			require.Equal(t, "flaky,!slow", opt.Labels)
			require.Equal(t, "main*", opt.Branch)
			require.Equal(t, "payments,!platform", opt.Owners)
			require.Equal(t, "enabled", opt.State)
			require.Equal(t, "framework:rspec,result:^failed", opt.Tags)
			require.Equal(t, "reliability", opt.SortBy)
			require.Equal(t, "asc", opt.Order)

			return []buildkite.TestWithMetrics{
					{
						Test: buildkite.Test{
							ID:       "test-123",
							Name:     "Example Test",
							Location: "spec/example_test.rb:42",
							FileName: "spec/example_test.rb",
							Labels:   []string{"flaky"},
						},
						Reliability:     &reliability,
						DurationAverage: 1.4,
						DurationTotal:   140,
						DurationMinimum: 0.5,
						DurationMaximum: 4.2,
						ExecutionsCount: 100,
						ExecutionsCountByResult: map[string]int{
							"passed": 97,
							"failed": 3,
						},
					},
					{
						Test:            buildkite.Test{ID: "test-456", Name: "Skipped Test"},
						Reliability:     nil,
						ExecutionsCount: 1,
						ExecutionsCountByResult: map[string]int{
							"passed":  0,
							"failed":  0,
							"skipped": 1,
						},
					},
				}, &buildkite.Response{Response: &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Link": []string{`<https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/tests?page=3>; rel="next"`},
					},
				}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})
	tool, handler, scopes := ListTests()

	require.Equal(t, "list_tests", tool.Name)
	require.Equal(t, "List Tests", tool.Annotations.Title)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Contains(t, tool.Description, "execution metrics")
	require.Equal(t, []string{"read_suites"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTestsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		Page:          2,
		PerPage:       50,
		Period:        "7days",
		MinTimestamp:  minTimestamp,
		MaxTimestamp:  maxTimestamp,
		Labels:        "flaky,!slow",
		Branch:        "main*",
		Owners:        "payments,!platform",
		State:         "enabled",
		Tags:          "framework:rspec,result:^failed",
		SortBy:        "reliability",
		Order:         "asc",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	resultText := getTextResult(t, result).Text
	var rawResponse struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText), &rawResponse))
	reliabilityValue, reliabilityPresent := rawResponse.Items[1]["reliability"]
	require.True(t, reliabilityPresent)
	require.Nil(t, reliabilityValue)

	var response PaginatedResult[buildkite.TestWithMetrics]
	require.NoError(t, json.Unmarshal([]byte(resultText), &response))
	require.Equal(t, `<https://api.buildkite.com/v2/analytics/organizations/org/suites/suite1/tests?page=3>; rel="next"`, response.Headers["Link"])
	require.Len(t, response.Items, 2)
	require.Equal(t, "test-123", response.Items[0].ID)
	require.Equal(t, []string{"flaky"}, response.Items[0].Labels)
	require.NotNil(t, response.Items[0].Reliability)
	require.InDelta(t, reliability, *response.Items[0].Reliability, 0.0001)
	require.InDelta(t, 1.4, response.Items[0].DurationAverage, 0.0001)
	require.InDelta(t, 140.0, response.Items[0].DurationTotal, 0.0001)
	require.InDelta(t, 0.5, response.Items[0].DurationMinimum, 0.0001)
	require.InDelta(t, 4.2, response.Items[0].DurationMaximum, 0.0001)
	require.Equal(t, 100, response.Items[0].ExecutionsCount)
	require.Equal(t, map[string]int{"passed": 97, "failed": 3}, response.Items[0].ExecutionsCountByResult)
	require.Nil(t, response.Items[1].Reliability)
}

func TestListTestsUsesDefaultPagination(t *testing.T) {
	client := &MockTestsClient{
		ListFunc: func(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			require.Equal(t, buildkite.ListOptions{Page: 1, PerPage: 100}, opt.ListOptions)
			return []buildkite.TestWithMetrics{}, &buildkite.Response{Response: &http.Response{Header: http.Header{}}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})
	_, handler, _ := ListTests()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTestsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func TestListTestsReturnsAPIError(t *testing.T) {
	response := &http.Response{
		Request:    &http.Request{Method: http.MethodGet},
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(strings.NewReader(`{"message":"period cannot be combined with min_timestamp"}`)),
	}
	client := &MockTestsClient{
		ListFunc: func(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			return nil, &buildkite.Response{Response: response}, &buildkite.ErrorResponse{
				Response: response,
				Message:  "period cannot be combined with min_timestamp",
			}
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})
	_, handler, _ := ListTests()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTestsArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		Period:        "7days",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "period cannot be combined with min_timestamp")
}

func TestGetTest(t *testing.T) {
	assert := require.New(t)

	client := &MockTestsClient{
		GetFunc: func(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error) {
			return buildkite.Test{
					ID:       "test-123",
					Name:     "Example Test",
					Location: "spec/example_test.rb",
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(`{"id": "test-123"}`)),
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})

	tool, handler, _ := GetTest()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test the tool definition
	assert.Equal("get_test", tool.Name)
	assert.Contains(tool.Description, "specific test")
	assert.True(tool.Annotations.ReadOnlyHint)

	// Test successful request
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetTestArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		TestID:        "test-123",
	})
	assert.NoError(err)
	assert.NotNil(result)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, "test-123")
	assert.Contains(textContent.Text, "Example Test")
}
