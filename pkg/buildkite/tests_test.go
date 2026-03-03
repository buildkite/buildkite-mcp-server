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
