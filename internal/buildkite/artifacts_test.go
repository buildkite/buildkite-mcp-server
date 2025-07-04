package buildkite

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockArtifactsClient struct {
	ListByBuildFunc           func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	DownloadArtifactByURLFunc func(ctx context.Context, url string, writer io.Writer) (*buildkite.Response, error)
}

func (m *MockArtifactsClient) ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
	if m.ListByBuildFunc != nil {
		return m.ListByBuildFunc(ctx, org, pipelineSlug, buildNumber, opts)
	}
	return nil, nil, nil
}

func (m *MockArtifactsClient) DownloadArtifactByURL(ctx context.Context, url string, writer io.Writer) (*buildkite.Response, error) {
	if m.DownloadArtifactByURLFunc != nil {
		return m.DownloadArtifactByURLFunc(ctx, url, writer)
	}
	return nil, nil
}

// Ensure MockArtifactsClient implements ArtifactsClient interface
var _ ArtifactsClient = (*MockArtifactsClient)(nil)

func TestListArtifacts(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	mockArtifactsClient := &MockArtifactsClient{
		ListByBuildFunc: func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			return []buildkite.Artifact{
					{
						ID:          "abc123",
						Filename:    "test-artifact.txt",
						State:       "finished",
						DownloadURL: "https://example.com/artifact",
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler := ListArtifacts(ctx, mockArtifactsClient)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org":           "test-org",
		"pipeline_slug": "test-pipeline",
		"build_number":  "123",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"id":"abc123"`)
	assert.Contains(textContent.Text, `"filename":"test-artifact.txt"`)
	assert.Contains(textContent.Text, `"state":"finished"`)
	assert.Contains(textContent.Text, `"download_url":"https://example.com/artifact"`)
}

func TestGetArtifact(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockArtifactsClient{
		DownloadArtifactByURLFunc: func(ctx context.Context, url string, writer io.Writer) (*buildkite.Response, error) {
			// Simulate writing artifact content to the provided writer
			_, err := writer.Write([]byte("This is test artifact content"))
			if err != nil {
				return nil, err
			}

			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
					Status:     "200 OK",
				},
			}, nil
		},
	}

	tool, handler := GetArtifact(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"url": "https://example.com/artifact",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	// Check the structure of the response
	assert.Contains(textContent.Text, `"status":"200 OK"`)
	assert.Contains(textContent.Text, `"statusCode":200`)
	assert.Contains(textContent.Text, `"encoding":"base64"`)

	// The base64 encoded "This is test artifact content"
	assert.Contains(textContent.Text, `"data":"VGhpcyBpcyB0ZXN0IGFydGlmYWN0IGNvbnRlbnQ="`)
}

func TestListArtifacts_MissingParameters(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockArtifactsClient{}

	_, handler := ListArtifacts(ctx, client)

	// Test missing org parameter
	req := createMCPRequest(t, map[string]any{
		"pipeline_slug": "test-pipeline",
		"build_number":  "123",
	})
	result, err := handler(ctx, req)
	assert.NoError(err)
	assert.NotNil(result)
	assert.Contains(getTextResult(t, result).Text, "required argument \"org\" not found")

	// Test missing pipeline_slug parameter
	req = createMCPRequest(t, map[string]any{
		"org":          "test-org",
		"build_number": "123",
	})
	result, err = handler(ctx, req)
	assert.NoError(err)
	assert.NotNil(result)
	assert.Contains(getTextResult(t, result).Text, "required argument \"pipeline_slug\" not found")

	// Test missing build_number parameter
	req = createMCPRequest(t, map[string]any{
		"org":           "test-org",
		"pipeline_slug": "test-pipeline",
	})
	result, err = handler(ctx, req)
	assert.NoError(err)
	assert.NotNil(result)
	assert.Contains(getTextResult(t, result).Text, "required argument \"build_number\" not found")
}

func TestGetArtifact_MissingParameters(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockArtifactsClient{}

	_, handler := GetArtifact(ctx, client)

	// Test missing url parameter
	req := createMCPRequest(t, map[string]any{})
	result, err := handler(ctx, req)
	assert.NoError(err)
	assert.NotNil(result)
	assert.Contains(getTextResult(t, result).Text, "required argument \"url\" not found")
}

func TestGetArtifact_ErrorResponse(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockArtifactsClient{
		DownloadArtifactByURLFunc: func(ctx context.Context, url string, writer io.Writer) (*buildkite.Response, error) {
			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 404,
					Status:     "404 Not Found",
					Body:       io.NopCloser(bytes.NewBufferString(`{"message":"Artifact not found"}`)),
				},
			}, nil
		},
	}

	_, handler := GetArtifact(ctx, client)

	req := createMCPRequest(t, map[string]any{
		"url": "https://example.com/nonexistent-artifact",
	})
	result, err := handler(ctx, req)
	assert.NoError(err)
	assert.NotNil(result)
	assert.Contains(getTextResult(t, result).Text, "failed to get artifact")
}

func TestBuildkiteClientAdapter_URLRewriting(t *testing.T) {
	assert := require.New(t)

	// Test rewriteArtifactURL method
	tests := []struct {
		name        string
		baseURL     string
		inputURL    string
		expectedURL string
	}{
		{
			name:        "should rewrite URLs when base URL has different host",
			baseURL:     "https://buildkite.proxy.com/rest/",
			inputURL:    "https://api.buildkite.com/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download",
			expectedURL: "https://buildkite.proxy.com/rest/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download",
		},
		{
			name:        "should not rewrite URLs when base URL matches input URL host and scheme",
			baseURL:     "https://api.buildkite.com/",
			inputURL:    "https://api.buildkite.com/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download",
			expectedURL: "https://api.buildkite.com/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download",
		},
		{
			name:        "should rewrite URLs when base URL has different host (any domain)",
			baseURL:     "https://buildkite.proxy.com/rest/",
			inputURL:    "https://example.com/some/other/url",
			expectedURL: "https://buildkite.proxy.com/rest/some/other/url",
		},
		{
			name:        "should handle base URL without trailing slash",
			baseURL:     "https://buildkite.proxy.com/rest",
			inputURL:    "https://api.buildkite.com/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download",
			expectedURL: "https://buildkite.proxy.com/rest/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download",
		},
		{
			name:        "should handle scheme differences",
			baseURL:     "http://buildkite.proxy.com/",
			inputURL:    "https://api.buildkite.com/v2/test",
			expectedURL: "http://buildkite.proxy.com/v2/test",
		},
		{
			name:        "should not rewrite when hosts and schemes match exactly",
			baseURL:     "https://api.buildkite.com/",
			inputURL:    "https://api.buildkite.com/v2/test",
			expectedURL: "https://api.buildkite.com/v2/test",
		},
		{
			name:        "should handle base URL with complex path prefix",
			baseURL:     "https://proxy.example.com/buildkite/api/",
			inputURL:    "https://api.buildkite.com/v2/orgs/test",
			expectedURL: "https://proxy.example.com/buildkite/api/v2/orgs/test",
		},
		{
			name:        "should return original URL when input URL is malformed",
			baseURL:     "https://buildkite.proxy.com/",
			inputURL:    "://malformed-url",
			expectedURL: "://malformed-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock buildkite client with the desired base URL
			client, err := buildkite.NewOpts(
				buildkite.WithTokenAuth("fake-token"),
				buildkite.WithBaseURL(tt.baseURL),
			)
			assert.NoError(err)

			adapter := &BuildkiteClientAdapter{Client: client}
			result := adapter.rewriteArtifactURL(tt.inputURL)
			assert.Equal(tt.expectedURL, result)
		})
	}
}

func TestBuildkiteClientAdapter_URLRewritingEdgeCases(t *testing.T) {
	assert := require.New(t)

	// Test edge cases
	t.Run("should handle nil base URL", func(t *testing.T) {
		adapter := &BuildkiteClientAdapter{
			Client: &buildkite.Client{},
		}
		result := adapter.rewriteArtifactURL("https://api.buildkite.com/test")
		assert.Equal("https://api.buildkite.com/test", result)
	})

	t.Run("should handle empty base URL", func(t *testing.T) {
		client, err := buildkite.NewOpts(
			buildkite.WithTokenAuth("fake-token"),
			buildkite.WithBaseURL(""),
		)
		assert.NoError(err)

		adapter := &BuildkiteClientAdapter{Client: client}
		result := adapter.rewriteArtifactURL("https://api.buildkite.com/test")
		assert.Equal("https://api.buildkite.com/test", result)
	})

	t.Run("should handle base URL with only root path", func(t *testing.T) {
		client, err := buildkite.NewOpts(
			buildkite.WithTokenAuth("fake-token"),
			buildkite.WithBaseURL("https://proxy.example.com/"),
		)
		assert.NoError(err)

		adapter := &BuildkiteClientAdapter{Client: client}
		result := adapter.rewriteArtifactURL("https://api.buildkite.com/v2/test")
		assert.Equal("https://proxy.example.com/v2/test", result)
	})
}
