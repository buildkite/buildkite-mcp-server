package buildkite

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

type MockArtifactsClient struct {
	ListByBuildFunc      func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	ListByJobFunc        func(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	DownloadArtifactFunc func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error)
}

func (m *MockArtifactsClient) ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
	if m.ListByBuildFunc != nil {
		return m.ListByBuildFunc(ctx, org, pipelineSlug, buildNumber, opts)
	}
	return nil, nil, nil
}

func (m *MockArtifactsClient) ListByJob(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
	if m.ListByJobFunc != nil {
		return m.ListByJobFunc(ctx, org, pipelineSlug, buildNumber, jobID, opts)
	}
	return nil, nil, nil
}

func (m *MockArtifactsClient) DownloadArtifact(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
	if m.DownloadArtifactFunc != nil {
		return m.DownloadArtifactFunc(ctx, org, pipelineSlug, buildNumber, jobID, artifactID, writer)
	}
	return nil, nil
}

// Ensure MockArtifactsClient implements ArtifactsClient interface
var _ ArtifactsClient = (*MockArtifactsClient)(nil)

func TestListArtifactsForBuild(t *testing.T) {
	assert := require.New(t)

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

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: mockArtifactsClient})

	tool, handler, _ := ListArtifactsForBuild()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListArtifactsForBuildArgs{
		OrgSlug:      "test-org",
		PipelineSlug: "test-pipeline",
		BuildNumber:  "123",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"id":"abc123"`)
	assert.Contains(textContent.Text, `"filename":"test-artifact.txt"`)
	assert.Contains(textContent.Text, `"state":"finished"`)
	assert.Contains(textContent.Text, `"download_url":"https://example.com/artifact"`)
}

func TestListArtifactsForJob(t *testing.T) {
	assert := require.New(t)

	mockArtifactsClient := &MockArtifactsClient{
		ListByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
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

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: mockArtifactsClient})

	tool, handler, _ := ListArtifactsForJob()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListArtifactsForJobArgs{
		OrgSlug:      "test-org",
		PipelineSlug: "test-pipeline",
		BuildNumber:  "123",
		JobID:        "123456-abcdef-123abc-456def",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"id":"abc123"`)
	assert.Contains(textContent.Text, `"filename":"test-artifact.txt"`)
	assert.Contains(textContent.Text, `"state":"finished"`)
	assert.Contains(textContent.Text, `"download_url":"https://example.com/artifact"`)
}

func TestGetArtifact(t *testing.T) {
	assert := require.New(t)

	var gotArgs []string
	client := &MockArtifactsClient{
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			gotArgs = []string{org, pipelineSlug, buildNumber, jobID, artifactID}

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

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})

	tool, handler, _ := GetArtifact()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)
	assert.Equal([]string{"myorg", "my-pipeline", "123", "abc", "def"}, gotArgs)

	textContent := getTextResult(t, result)

	// Check the structure of the response
	assert.Contains(textContent.Text, `"status":"200 OK"`)
	assert.Contains(textContent.Text, `"statusCode":200`)
	assert.Contains(textContent.Text, `"encoding":"base64"`)

	// The base64 encoded "This is test artifact content"
	assert.Contains(textContent.Text, `"data":"VGhpcyBpcyB0ZXN0IGFydGlmYWN0IGNvbnRlbnQ="`)
}

func TestGetArtifact_JSONContent(t *testing.T) {
	assert := require.New(t)

	client := &MockArtifactsClient{
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			_, err := writer.Write([]byte(`{"passed":true,"failures":[{"name":"test one"}]}`))
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

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})

	_, handler, _ := GetArtifact()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, `"status":"200 OK"`)
	assert.Contains(textContent.Text, `"statusCode":200`)
	assert.Contains(textContent.Text, `"encoding":"json"`)
	assert.NotContains(textContent.Text, `"encoding":"base64"`)

	var response struct {
		Data struct {
			Passed   bool `json:"passed"`
			Failures []struct {
				Name string `json:"name"`
			} `json:"failures"`
		} `json:"data"`
	}
	assert.NoError(json.Unmarshal([]byte(textContent.Text), &response))
	assert.True(response.Data.Passed)
	assert.Equal("test one", response.Data.Failures[0].Name)
}

func TestGetArtifact_ErrorResponse(t *testing.T) {
	assert := require.New(t)

	client := &MockArtifactsClient{
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			resp := &http.Response{
				Request:    &http.Request{Method: "GET"},
				StatusCode: 404,
				Status:     "404 Not Found",
				Body:       io.NopCloser(bytes.NewBufferString(`{"message":"Artifact not found"}`)),
			}
			return &buildkite.Response{
				Response: resp,
			}, &buildkite.ErrorResponse{Response: resp, Message: `{"message":"Artifact not found"}`}
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})

	_, handler, _ := GetArtifact()

	req := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, req, GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "missing",
	})
	assert.NoError(err)
	assert.NotNil(result)
	assert.Contains(getTextResult(t, result).Text, `{"message":"Artifact not found"}`)
}

func TestArtifactDownloadPath(t *testing.T) {
	assert := require.New(t)

	assert.Equal(
		"v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download",
		artifactDownloadPath("myorg", "my-pipeline", "123", "abc", "def"),
	)

	// Path-unsafe characters in an identifier are escaped so the value cannot
	// inject extra path segments and alter which endpoint is addressed.
	assert.Equal(
		"v2/organizations/o/pipelines/p/builds/b/jobs/j/artifacts/a%2F..%2Faccess-token/download",
		artifactDownloadPath("o", "p", "b", "j", "a/../access-token"),
	)
}

func TestBuildkiteClientAdapter_DownloadArtifact(t *testing.T) {
	assert := require.New(t)

	const wantSuffix = "/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download"

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("artifact-bytes"))
	}))
	defer srv.Close()

	tests := []struct {
		name     string
		basePath string // appended to the test server URL to form the base URL
		wantPath string
	}{
		{
			// The common production shape: host with a root path and trailing
			// slash. No prefix is added and none is dropped.
			name:     "default root base url",
			basePath: "/",
			wantPath: wantSuffix,
		},
		{
			// A proxied installation serving the REST API under a path prefix;
			// the prefix must be preserved on the resolved request.
			name:     "proxy base url with trailing slash",
			basePath: "/rest/",
			wantPath: "/rest" + wantSuffix,
		},
		{
			// Same proxy prefix without a trailing slash — a realistic
			// misconfiguration the client must normalise to the same request.
			name:     "proxy base url without trailing slash",
			basePath: "/rest",
			wantPath: "/rest" + wantSuffix,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath = ""

			client, err := buildkite.NewOpts(
				buildkite.WithTokenAuth("fake-token"),
				buildkite.WithBaseURL(srv.URL+tt.basePath),
			)
			assert.NoError(err)

			adapter := &BuildkiteClientAdapter{Client: client}

			var buf bytes.Buffer
			resp, err := adapter.DownloadArtifact(context.Background(), "myorg", "my-pipeline", "123", "abc", "def", &buf)
			assert.NoError(err)
			assert.Equal(200, resp.StatusCode)
			assert.Equal("artifact-bytes", buf.String())
			assert.Equal(tt.wantPath, gotPath)
		})
	}
}
