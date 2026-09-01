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
	GetFunc  func(ctx context.Context, org, slug, testID string, opt *buildkite.TestsGetOptions) (buildkite.TestWithMetrics, *buildkite.Response, error)
	ListFunc func(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error)
}

func (m *MockTestsClient) Get(ctx context.Context, org, slug, testID string, opt *buildkite.TestsGetOptions) (buildkite.TestWithMetrics, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, slug, testID, opt)
	}
	return buildkite.TestWithMetrics{}, nil, nil
}

func (m *MockTestsClient) List(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, slug, opt)
	}
	return nil, nil, nil
}

var _ TestsClient = (*MockTestsClient)(nil)

type MockBuildTestsClient struct {
	ListFunc func(ctx context.Context, org, buildUUID string, opt *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error)
}

func (m *MockBuildTestsClient) List(ctx context.Context, org, buildUUID string, opt *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
	return m.ListFunc(ctx, org, buildUUID, opt)
}

var _ BuildTestsClient = (*MockBuildTestsClient)(nil)

func TestListTestsForBuild(t *testing.T) {
	client := &MockBuildTestsClient{
		ListFunc: func(ctx context.Context, org, buildUUID string, opt *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "019d66fb-e8db-47eb-866c-94b85d42b9a1", buildUUID)
			require.Equal(t, buildkite.ListOptions{Page: 2, PerPage: 50}, opt.ListOptions)
			require.Equal(t, "flaky,!slow", opt.Labels)
			require.Equal(t, "main*", opt.Branch)
			require.Equal(t, "payments,!platform", opt.Owners)
			require.Equal(t, "enabled", opt.State)
			require.Equal(t, "framework:rspec,result:^failed", opt.Tags)
			require.Equal(t, "reliability", opt.SortBy)
			require.Equal(t, "asc", opt.Order)

			reliability := 0.97
			return []buildkite.TestWithMetrics{{
					Test:            buildkite.Test{ID: "test-123", Name: "Example Test"},
					Reliability:     &reliability,
					ExecutionsCount: 100,
				}}, &buildkite.Response{Response: &http.Response{Header: http.Header{
					"Link": []string{`<https://api.buildkite.com/v2/analytics/organizations/org/builds/019d66fb-e8db-47eb-866c-94b85d42b9a1/tests?page=3>; rel="next"`},
				}}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildTestsClient: client})
	tool, handler, scopes := ListTestsForBuild()

	require.Equal(t, "list_tests_for_build", tool.Name)
	require.Equal(t, "List Tests for Build", tool.Annotations.Title)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Contains(t, tool.Description, "build UUID")
	require.Equal(t, []string{"read_suites"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTestsForBuildArgs{
		OrgSlug:   "org",
		BuildUUID: "019d66fb-e8db-47eb-866c-94b85d42b9a1",
		Page:      2,
		PerPage:   50,
		Labels:    "flaky,!slow",
		Branch:    "main*",
		Owners:    "payments,!platform",
		State:     "enabled",
		Tags:      "framework:rspec,result:^failed",
		SortBy:    "reliability",
		Order:     "asc",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var response PaginatedResult[buildkite.TestWithMetrics]
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &response))
	require.Equal(t, `<https://api.buildkite.com/v2/analytics/organizations/org/builds/019d66fb-e8db-47eb-866c-94b85d42b9a1/tests?page=3>; rel="next"`, response.Headers["Link"])
	require.Len(t, response.Items, 1)
	require.Equal(t, "test-123", response.Items[0].ID)
	require.Equal(t, 100, response.Items[0].ExecutionsCount)
}

func TestListTests(t *testing.T) {
	minTimestamp := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	maxTimestamp := time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC)
	reliability := 0.97

	client := &MockTestsClient{
		ListFunc: func(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "suite1", slug)
			require.Equal(t, buildkite.ListOptions{Page: 2, PerPage: 50}, opt.ListOptions)
			require.Equal(t, minTimestamp, opt.MinTimestamp)
			require.Equal(t, maxTimestamp, opt.MaxTimestamp)
			require.Equal(t, 5, opt.MinExecutions)
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
		MinTimestamp:  minTimestamp,
		MaxTimestamp:  maxTimestamp,
		MinExecutions: 5,
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
			require.Equal(t, "7days", opt.Period)
			require.True(t, opt.MinTimestamp.IsZero())
			require.True(t, opt.MaxTimestamp.IsZero())
			require.Zero(t, opt.MinExecutions)
			return []buildkite.TestWithMetrics{}, &buildkite.Response{Response: &http.Response{Header: http.Header{}}}, nil
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
		MinTimestamp:  time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "period cannot be combined with min_timestamp")
}

func TestGetTest(t *testing.T) {
	assert := require.New(t)

	reliability := 0.97

	client := &MockTestsClient{
		GetFunc: func(ctx context.Context, org, slug, testID string, opt *buildkite.TestsGetOptions) (buildkite.TestWithMetrics, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("suite1", slug)
			assert.Equal("test-123", testID)
			assert.Equal(&buildkite.TestsGetOptions{}, opt)

			return buildkite.TestWithMetrics{
					Test: buildkite.Test{
						ID:       "test-123",
						Name:     "Example Test",
						Location: "spec/example_test.rb",
					},
					Reliability:             &reliability,
					DurationAverage:         1.5,
					ExecutionsCount:         100,
					ExecutionsCountByResult: map[string]int{"passed": 97, "failed": 3},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(`{"id": "test-123"}`)),
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})

	tool, handler, scopes := GetTest()
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test the tool definition
	assert.Equal("get_test", tool.Name)
	assert.Contains(tool.Description, "specific test")
	assert.Contains(tool.Description, "metrics")
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Equal([]string{"read_suites"}, scopes)

	// Test successful request
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetTestArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		TestID:        "test-123",
	})
	assert.NoError(err)
	assert.NotNil(result)

	var test buildkite.TestWithMetrics
	assert.NoError(json.Unmarshal([]byte(getTextResult(t, result).Text), &test))
	assert.Equal("test-123", test.ID)
	assert.Equal("Example Test", test.Name)
	assert.NotNil(test.Reliability)
	assert.InDelta(reliability, *test.Reliability, 0.0001)
	assert.InDelta(1.5, test.DurationAverage, 0.0001)
	assert.Equal(100, test.ExecutionsCount)
	assert.Equal(map[string]int{"passed": 97, "failed": 3}, test.ExecutionsCountByResult)
}

func TestGetTestWithTimeWindow(t *testing.T) {
	minTimestamp := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	maxTimestamp := time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC)

	client := &MockTestsClient{
		GetFunc: func(ctx context.Context, org, slug, testID string, opt *buildkite.TestsGetOptions) (buildkite.TestWithMetrics, *buildkite.Response, error) {
			require.Equal(t, &buildkite.TestsGetOptions{
				MinTimestamp: minTimestamp,
				MaxTimestamp: maxTimestamp,
			}, opt)

			return buildkite.TestWithMetrics{Test: buildkite.Test{ID: "test-123"}}, &buildkite.Response{
				Response: &http.Response{StatusCode: 200},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})
	_, handler, _ := GetTest()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetTestArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		TestID:        "test-123",
		MinTimestamp:  minTimestamp,
		MaxTimestamp:  maxTimestamp,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "test-123")
}

func TestGetTestWithPeriod(t *testing.T) {
	client := &MockTestsClient{
		GetFunc: func(ctx context.Context, org, slug, testID string, opt *buildkite.TestsGetOptions) (buildkite.TestWithMetrics, *buildkite.Response, error) {
			require.Equal(t, &buildkite.TestsGetOptions{Period: "28days"}, opt)

			return buildkite.TestWithMetrics{Test: buildkite.Test{ID: "test-123"}}, &buildkite.Response{
				Response: &http.Response{StatusCode: 200},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})
	_, handler, _ := GetTest()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetTestArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		TestID:        "test-123",
		Period:        "28days",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "test-123")
}

func TestGetTestPropagatesAPIError(t *testing.T) {
	resp := &http.Response{
		StatusCode: 422,
		Body:       io.NopCloser(strings.NewReader(`{"message":"period cannot be combined with min_timestamp"}`)),
	}

	client := &MockTestsClient{
		GetFunc: func(ctx context.Context, org, slug, testID string, opt *buildkite.TestsGetOptions) (buildkite.TestWithMetrics, *buildkite.Response, error) {
			return buildkite.TestWithMetrics{}, &buildkite.Response{Response: resp}, &buildkite.ErrorResponse{
				Response: resp,
				Message:  "period cannot be combined with min_timestamp",
			}
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TestsClient: client})
	_, handler, _ := GetTest()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetTestArgs{
		OrgSlug:       "org",
		TestSuiteSlug: "suite1",
		TestID:        "test-123",
		Period:        "7days",
		MinTimestamp:  time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "period cannot be combined with min_timestamp")
}
