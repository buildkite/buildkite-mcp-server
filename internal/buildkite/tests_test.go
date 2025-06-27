package buildkite

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockTestsClient struct {
	GetFunc func(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error)
}

func (m *MockTestsClient) Get(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, slug, testID)
	}
	return buildkite.Test{}, nil, nil
}

var _ TestsClient = (*MockTestsClient)(nil)

func TestGetTest(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()

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

	tool, handler := GetTest(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	// Test the tool schema
	assert.Equal("get_test", tool.Name)
	assert.Contains(tool.Description, "specific test")

	// Test required parameters
	params := tool.InputSchema.Properties
	assert.Contains(params, "org")
	assert.Contains(params, "test_suite_slug")
	assert.Contains(params, "test_id")

	// Verify org is required
	orgParam := params["org"].(map[string]interface{})
	assert.Equal("string", orgParam["type"])

	// Verify test_suite_slug is required
	testSuiteParam := params["test_suite_slug"].(map[string]interface{})
	assert.Equal("string", testSuiteParam["type"])

	// Verify test_id is required
	testIDParam := params["test_id"].(map[string]interface{})
	assert.Equal("string", testIDParam["type"])
}