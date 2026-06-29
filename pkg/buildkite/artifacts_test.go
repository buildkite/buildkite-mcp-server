package buildkite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type MockArtifactsClient struct {
	ListByBuildFunc        func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	ListByJobFunc          func(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	GetByJobFunc           func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error)
	DownloadArtifactFunc   func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error)
	ResolveDownloadURLFunc func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error)
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

func (m *MockArtifactsClient) GetByJob(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
	if m.GetByJobFunc != nil {
		return m.GetByJobFunc(ctx, org, pipelineSlug, buildNumber, jobID, artifactID)
	}
	return buildkite.Artifact{}, nil, nil
}

func (m *MockArtifactsClient) ResolveDownloadURL(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
	if m.ResolveDownloadURLFunc != nil {
		return m.ResolveDownloadURLFunc(ctx, org, pipelineSlug, buildNumber, jobID, artifactID)
	}
	return "", nil
}

// Ensure MockArtifactsClient implements ArtifactsClient interface
var _ ArtifactsClient = (*MockArtifactsClient)(nil)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

func TestGetArtifact_TextInline(t *testing.T) {
	assert := require.New(t)

	var gotArgs []string
	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			gotArgs = []string{org, pipelineSlug, buildNumber, jobID, artifactID}
			return buildkite.Artifact{
				Filename: "artifact.txt",
				MimeType: "text/plain",
				FileSize: 29,
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "https://example.com/resolved-artifact?X-Amz-Expires=600", nil
		},
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
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

	got := getJSONResult(t, result)
	assert.Equal("text", got["encoding"])
	assert.Equal("This is test artifact content", got["content"])
	assert.Equal("text/plain", got["mime_type"])
	assert.Equal(int64(29), got["file_size"])
	assert.Equal("artifact.txt", got["filename"])
	assert.Equal("https://example.com/resolved-artifact?X-Amz-Expires=600", got["download_url"])
	assert.Equal("none", got["download_url_auth"])
	assert.Equal(int64(600), got["download_url_expires_in_seconds"])
}

func TestGetArtifact_NonUTF8TextReturnsURL(t *testing.T) {
	assert := require.New(t)

	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename: "latin1.txt",
				MimeType: "text/plain; charset=iso-8859-1",
				FileSize: 4,
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "https://example.com/latin1.txt", nil
		},
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			_, err := writer.Write([]byte{0xff, 0xfe, 0xfd, '\n'})
			return nil, err
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)

	got := getJSONResult(t, result)
	assert.Equal("url", got["encoding"])
	assert.Equal("text/plain; charset=iso-8859-1", got["mime_type"])
	assert.Equal("https://example.com/latin1.txt", got["download_url"])
	assert.Contains(got["note"], "not valid UTF-8")
	assert.NotContains(got, "content")
}

func TestGetArtifact_StructuredJSONInline(t *testing.T) {
	assert := require.New(t)

	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename: "clippy.sarif",
				MimeType: "application/sarif+json",
				FileSize: 18,
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "https://example.com/clippy.sarif", nil
		},
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			_, err := writer.Write([]byte(`{"version":"2.1.0"}`))
			return nil, err
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)

	got := getJSONResult(t, result)
	assert.Equal("text", got["encoding"])
	assert.JSONEq(`{"version":"2.1.0"}`, got["content"].(string))
	assert.Equal("application/sarif+json", got["mime_type"])
}

func TestGetArtifact_MarkupReturnsURL(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		filename string
	}{
		{name: "xml", mimeType: "text/xml", filename: "junit.xml"},
		{name: "html", mimeType: "text/html", filename: "report.html"},
		{name: "structured xml", mimeType: "application/atom+xml", filename: "feed.xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)

			downloadCalled := false
			client := &MockArtifactsClient{
				GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
					return buildkite.Artifact{
						Filename: tt.filename,
						MimeType: tt.mimeType,
						FileSize: 42,
					}, nil, nil
				},
				ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
					return "https://example.com/artifact", nil
				},
				DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
					downloadCalled = true
					return nil, nil
				},
			}

			ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
			_, handler, _ := GetArtifact()

			result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
				OrgSlug:      "myorg",
				PipelineSlug: "my-pipeline",
				BuildNumber:  "123",
				JobID:        "abc",
				ArtifactID:   "def",
			})
			assert.NoError(err)
			assert.False(downloadCalled)

			got := getJSONResult(t, result)
			assert.Equal("url", got["encoding"])
			assert.Equal(tt.mimeType, got["mime_type"])
			assert.Equal("https://example.com/artifact", got["download_url"])
			assert.NotContains(got, "content")
		})
	}
}

func TestGetArtifact_TextTooLargeReturnsURL(t *testing.T) {
	assert := require.New(t)

	downloadCalled := false
	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename: "large.txt",
				MimeType: "text/plain",
				FileSize: textArtifactInlineLimit + 1,
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "https://example.com/large", nil
		},
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			downloadCalled = true
			return nil, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)
	assert.False(downloadCalled)

	got := getJSONResult(t, result)
	assert.Equal("url", got["encoding"])
	assert.Equal("https://example.com/large", got["download_url"])
	assert.NotContains(got, "content")
}

func TestGetArtifact_BinaryReturnsURL(t *testing.T) {
	assert := require.New(t)

	downloadCalled := false
	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename: "artifact.zip",
				MimeType: "application/zip",
				FileSize: 4096,
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "https://example.com/artifact.zip", nil
		},
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			downloadCalled = true
			return nil, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)
	assert.False(downloadCalled)

	got := getJSONResult(t, result)
	assert.Equal("url", got["encoding"])
	assert.Equal("application/zip", got["mime_type"])
	assert.Equal("https://example.com/artifact.zip", got["download_url"])
	assert.Equal("none", got["download_url_auth"])
	assert.NotContains(got, "content")
}

func TestGetArtifact_EmptyTextInline(t *testing.T) {
	assert := require.New(t)

	downloadCalled := false
	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename: "empty.txt",
				MimeType: "text/plain",
				FileSize: 0,
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "https://example.com/empty.txt", nil
		},
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			downloadCalled = true
			// Empty file: nothing is written to the buffer.
			return nil, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)
	assert.True(downloadCalled)

	got := getJSONResult(t, result)
	assert.Equal("text", got["encoding"])
	assert.Empty(got["content"])
	assert.Equal("https://example.com/empty.txt", got["download_url"])
}

func TestGetArtifact_OversizedTextReturnsURL(t *testing.T) {
	assert := require.New(t)

	downloadCalled := false
	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename: "liar.txt",
				MimeType: "text/plain",
				FileSize: 10, // metadata under-reports the real content
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "https://example.com/liar.txt", nil
		},
		DownloadArtifactFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
			downloadCalled = true
			_, err := writer.Write(bytes.Repeat([]byte("a"), int(textArtifactInlineLimit)+1))
			return nil, err
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)
	assert.True(downloadCalled)

	got := getJSONResult(t, result)
	assert.Equal("url", got["encoding"])
	assert.Equal("https://example.com/liar.txt", got["download_url"])
	assert.Contains(got["note"], "larger than expected")
	assert.NotContains(got, "content")
}

func TestGetArtifact_ResolveDownloadURLFailureFallsBack(t *testing.T) {
	assert := require.New(t)

	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename:    "artifact.zip",
				MimeType:    "application/zip",
				FileSize:    4096,
				DownloadURL: "https://api.buildkite.com/v2/download",
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "", errors.New("no redirect")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)

	got := getJSONResult(t, result)
	assert.Equal("url", got["encoding"])
	assert.Equal("https://api.buildkite.com/v2/download", got["download_url"])
	assert.Equal("requires Buildkite API authentication", got["download_url_auth"])
	assert.Equal(int64(0), got["download_url_expires_in_seconds"])
}

func TestGetArtifact_NoDownloadURLAvailable(t *testing.T) {
	assert := require.New(t)

	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			return buildkite.Artifact{
				Filename: "artifact.zip",
				MimeType: "application/zip",
				FileSize: 4096,
			}, nil, nil
		},
		ResolveDownloadURLFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
			return "", errors.New("no redirect")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ArtifactsClient: client})
	_, handler, _ := GetArtifact()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetArtifactArgs{
		OrgSlug:      "myorg",
		PipelineSlug: "my-pipeline",
		BuildNumber:  "123",
		JobID:        "abc",
		ArtifactID:   "def",
	})
	assert.NoError(err)

	got := getJSONResult(t, result)
	assert.Equal("url", got["encoding"])
	assert.NotContains(got, "download_url")
	assert.Contains(got["note"], "no download URL was available")
}

func TestGetArtifact_ErrorResponse(t *testing.T) {
	assert := require.New(t)

	client := &MockArtifactsClient{
		GetByJobFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
			resp := &http.Response{
				Request:    &http.Request{Method: "GET"},
				StatusCode: 404,
				Status:     "404 Not Found",
				Body:       io.NopCloser(bytes.NewBufferString(`{"message":"Artifact not found"}`)),
			}
			return buildkite.Artifact{}, &buildkite.Response{Response: resp}, &buildkite.ErrorResponse{Response: resp, Message: `{"message":"Artifact not found"}`}
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

func TestIsTextMIMEType(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		want     bool
	}{
		{name: "text plain", mimeType: "text/plain", want: true},
		{name: "text csv", mimeType: "text/csv", want: true},
		{name: "text html", mimeType: "text/html", want: false},
		{name: "uppercase text", mimeType: "TEXT/PLAIN", want: true},
		{name: "text with charset", mimeType: "text/plain; charset=utf-8", want: true},
		{name: "json", mimeType: "application/json", want: true},
		{name: "sarif json", mimeType: "application/sarif+json", want: true},
		{name: "text xml", mimeType: "text/xml", want: false},
		{name: "xml", mimeType: "application/xml", want: false},
		{name: "atom xml", mimeType: "application/atom+xml", want: false},
		{name: "xhtml", mimeType: "application/xhtml+xml", want: false},
		{name: "yaml", mimeType: "application/yaml", want: true},
		{name: "x yaml", mimeType: "application/x-yaml", want: true},
		{name: "javascript", mimeType: "application/javascript", want: true},
		{name: "ndjson", mimeType: "application/x-ndjson", want: true},
		{name: "image", mimeType: "image/png", want: false},
		{name: "zip", mimeType: "application/zip", want: false},
		{name: "empty", mimeType: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isTextMIMEType(tt.mimeType))
		})
	}
}

func TestDownloadURLExpiresInSeconds(t *testing.T) {
	tests := []struct {
		name        string
		downloadURL string
		want        int
	}{
		{name: "s3 expires", downloadURL: "https://example.com/artifact?X-Amz-Expires=600", want: 600},
		{name: "missing expires", downloadURL: "https://example.com/artifact", want: 60},
		{name: "invalid expires", downloadURL: "https://example.com/artifact?X-Amz-Expires=nope", want: 60},
		{name: "negative expires", downloadURL: "https://example.com/artifact?X-Amz-Expires=-1", want: 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, downloadURLExpiresInSeconds(tt.downloadURL))
		})
	}
}

func TestInlineLimitWriter(t *testing.T) {
	assert := require.New(t)

	// Writing exactly the limit buffers everything without flagging overflow.
	w := &inlineLimitWriter{limit: 4}
	n, err := w.Write([]byte("abcd"))
	assert.NoError(err)
	assert.Equal(4, n)
	assert.False(w.overflow)
	assert.Equal("abcd", w.buf.String())

	// A further write past the limit is discarded but still reports success so
	// the underlying download drains to completion.
	n, err = w.Write([]byte("e"))
	assert.NoError(err)
	assert.Equal(1, n)
	assert.True(w.overflow)
	assert.Equal("abcd", w.buf.String())

	// A single oversized write is truncated to the limit.
	w2 := &inlineLimitWriter{limit: 4}
	n, err = w2.Write([]byte("abcdef"))
	assert.NoError(err)
	assert.Equal(6, n)
	assert.True(w2.overflow)
	assert.Equal("abcd", w2.buf.String())
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

func TestArtifactMetadataPath(t *testing.T) {
	assert := require.New(t)

	assert.Equal(
		"v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def",
		artifactMetadataPath("myorg", "my-pipeline", "123", "abc", "def"),
	)

	assert.Equal(
		"v2/organizations/o/pipelines/p/builds/b/jobs/j/artifacts/a%2F..%2Faccess-token",
		artifactMetadataPath("o", "p", "b", "j", "a/../access-token"),
	)
}

func TestBuildkiteClientAdapter_GetByJob(t *testing.T) {
	assert := require.New(t)

	const wantSuffix = "/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def"

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"def","filename":"artifact.txt","mime_type":"text/plain","file_size":12}`))
	}))
	defer srv.Close()

	tests := []struct {
		name     string
		basePath string
		wantPath string
	}{
		{name: "default root base url", basePath: "/", wantPath: wantSuffix},
		{name: "proxy base url with trailing slash", basePath: "/rest/", wantPath: "/rest" + wantSuffix},
		{name: "proxy base url without trailing slash", basePath: "/rest", wantPath: "/rest" + wantSuffix},
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

			artifact, resp, err := adapter.GetByJob(context.Background(), "myorg", "my-pipeline", "123", "abc", "def")
			assert.NoError(err)
			assert.Equal(200, resp.StatusCode)
			assert.Equal("artifact.txt", artifact.Filename)
			assert.Equal(tt.wantPath, gotPath)
		})
	}
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

func TestBuildkiteClientAdapter_ResolveDownloadURL(t *testing.T) {
	assert := require.New(t)

	const wantSuffix = "/v2/organizations/myorg/pipelines/my-pipeline/builds/123/jobs/abc/artifacts/def/download"
	const resolvedURL = "https://storage.example.com/artifact"

	var gotPath string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, resolvedURL, http.StatusFound)
	}))
	defer srv.Close()

	tests := []struct {
		name     string
		basePath string
		wantPath string
	}{
		{name: "default root base url", basePath: "/", wantPath: wantSuffix},
		{name: "proxy base url with trailing slash", basePath: "/rest/", wantPath: "/rest" + wantSuffix},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath = ""
			gotAuth = ""

			client, err := buildkite.NewOpts(
				buildkite.WithTokenAuth("fake-token"),
				buildkite.WithBaseURL(srv.URL+tt.basePath),
			)
			assert.NoError(err)

			adapter := &BuildkiteClientAdapter{Client: client}

			gotURL, err := adapter.ResolveDownloadURL(context.Background(), "myorg", "my-pipeline", "123", "abc", "def")
			assert.NoError(err)
			assert.Equal(resolvedURL, gotURL)
			assert.Equal(tt.wantPath, gotPath)
			assert.Equal("Bearer fake-token", gotAuth)
		})
	}
}

func TestBuildkiteClientAdapter_ResolveDownloadURLUsesConfiguredHTTPClient(t *testing.T) {
	assert := require.New(t)

	const resolvedURL = "https://storage.example.com/artifact"

	var gotInjectedHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotInjectedHeader = r.Header.Get("X-Transport-Was-Here")
		http.Redirect(w, r, resolvedURL, http.StatusFound)
	}))
	defer srv.Close()

	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("X-Transport-Was-Here", "yes")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	client, err := buildkite.NewOpts(
		buildkite.WithTokenAuth("fake-token"),
		buildkite.WithBaseURL(srv.URL+"/"),
		buildkite.WithHTTPClient(httpClient),
	)
	assert.NoError(err)

	adapter := &BuildkiteClientAdapter{Client: client, HTTPClient: httpClient}

	gotURL, err := adapter.ResolveDownloadURL(context.Background(), "myorg", "my-pipeline", "123", "abc", "def")
	assert.NoError(err)
	assert.Equal(resolvedURL, gotURL)
	assert.Equal("yes", gotInjectedHeader)
	assert.Nil(httpClient.CheckRedirect)
}

func getJSONResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()

	text := getTextResult(t, result).Text
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()

	var got map[string]any
	require.NoError(t, decoder.Decode(&got))

	normalized := make(map[string]any, len(got))
	for key, value := range got {
		if n, ok := value.(json.Number); ok {
			i, err := n.Int64()
			require.NoError(t, err)
			normalized[key] = i
			continue
		}
		normalized[key] = value
	}
	return normalized
}
