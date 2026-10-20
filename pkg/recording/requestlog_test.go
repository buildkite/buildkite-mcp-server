package recording_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/stretchr/testify/require"
)

type errTransport struct{ err error }

func (t errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if t.err != nil {
		return nil, t.err
	}
	return nil, fmt.Errorf("boom")
}

func readRequestLog(t *testing.T, path string) []recording.RequestLogEntry {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var entries []recording.RequestLogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e recording.RequestLogEntry
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &e))
		if e.Method != "" { // Startup records are not HTTP requests.
			entries = append(entries, e)
		}
	}
	require.NoError(t, scanner.Err())
	return entries
}

func TestRequestLogTransport(t *testing.T) {
	r := require.New(t)
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")

	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(200, `{"id":"1"}`, "application/json"),
		newMockResponse(404, `{"message":"Not Found"}`, "application/json"),
	}}
	transport, err := recording.NewRequestLogTransport(mock, logPath)
	r.NoError(err)

	// One request made on behalf of a tool call, one with no tool call.
	ctx := trace.WithToolCall(context.Background(), trace.ToolCall{Name: "get_build", ID: "7"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.buildkite.com/v2/organizations/acme/pipelines/app/builds/1", nil)
	r.NoError(err)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := transport.RoundTrip(req)
	r.NoError(err)
	r.Equal(200, resp.StatusCode)

	req2, err := http.NewRequest(http.MethodGet, "https://api.buildkite.com/v2/missing", nil)
	r.NoError(err)
	_, err = transport.RoundTrip(req2)
	r.NoError(err)

	entries := readRequestLog(t, logPath)
	r.Len(entries, 2)
	r.Equal("mcp", entries[0].Source)
	r.Equal(http.MethodGet, entries[0].Method)
	r.Equal("https://api.buildkite.com/v2/organizations/acme/pipelines/app/builds/1", entries[0].URL)
	r.Equal(200, entries[0].Status)
	r.Equal("get_build", entries[0].Tool)
	r.Equal("7", entries[0].ToolCallID)
	r.NotEmpty(entries[0].Time)
	r.Equal(404, entries[1].Status)
	r.Empty(entries[1].Tool)

	raw, err := os.ReadFile(logPath)
	r.NoError(err)
	r.NotContains(string(raw), "secret-token")
}

func TestRequestLogTransportLogsErrors(t *testing.T) {
	r := require.New(t)
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")

	transport, err := recording.NewRequestLogTransport(errTransport{}, logPath)
	r.NoError(err)

	req, err := http.NewRequest(http.MethodGet, "https://api.buildkite.com/v2/user", nil)
	r.NoError(err)
	_, err = transport.RoundTrip(req)
	r.Error(err)

	entries := readRequestLog(t, logPath)
	r.Len(entries, 1)
	r.Equal("boom", entries[0].Error)
	r.Zero(entries[0].Status)
}

func TestRequestLogTransportAppends(t *testing.T) {
	r := require.New(t)
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")
	r.NoError(os.WriteFile(logPath, []byte(`{"source":"mcp","method":"GET","url":"earlier"}`+"\n"), 0o600))

	mock := &mockTransport{responses: []*http.Response{newMockResponse(200, `{}`, "application/json")}}
	transport, err := recording.NewRequestLogTransport(mock, logPath)
	r.NoError(err)
	req, err := http.NewRequest(http.MethodGet, "https://api.buildkite.com/v2/user", nil)
	r.NoError(err)
	_, err = transport.RoundTrip(req)
	r.NoError(err)

	entries := readRequestLog(t, logPath)
	r.Len(entries, 2)
	r.Equal("earlier", entries[0].URL)
}

func TestNewRequestLogTransportBadPath(t *testing.T) {
	_, err := recording.NewRequestLogTransport(http.DefaultTransport, filepath.Join(t.TempDir(), "missing-dir", "requests.jsonl"))
	require.Error(t, err)
}

func TestRequestLogTransportSignedRedirect(t *testing.T) {
	const query = "X-Amz-Signature=test-signature&X-Amz-Security-Token=test-security-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/download" {
			http.Redirect(w, req, "/artifact?"+query, http.StatusFound)
			return
		}
		// Logging must not remove credentials from the actual download.
		if req.URL.RawQuery != query {
			http.Error(w, "missing download credentials", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("artifact contents"))
	}))
	defer server.Close()
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")
	transport, err := recording.NewRequestLogTransport(http.DefaultTransport, logPath)
	require.NoError(t, err)
	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL + "/download")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "artifact contents", string(body))
	entries := readRequestLog(t, logPath)
	require.Len(t, entries, 2)
	require.Equal(t, server.URL+"/artifact", entries[1].URL)
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "test-signature")
	require.NotContains(t, string(raw), "test-security-token")
}

func TestRequestLogTransportStartsWithoutRequests(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")
	_, err := recording.NewRequestLogTransport(http.DefaultTransport, logPath)
	require.NoError(t, err)
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	var started map[string]any
	require.NoError(t, json.Unmarshal(raw, &started))
	require.Equal(t, "mcp", started["source"])
	require.Equal(t, "started", started["type"])
}

func TestRequestLogTransportRedactsErrorURL(t *testing.T) {
	const rawURL = "https://user:password@storage.example/artifact?signature=secret#fragment"
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")
	transport, err := recording.NewRequestLogTransport(errTransport{err: fmt.Errorf("download %s failed", rawURL)}, logPath)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.Error(t, err)
	entries := readRequestLog(t, logPath)
	require.Len(t, entries, 1)
	require.Equal(t, "https://storage.example/artifact", entries[0].URL)
	require.Equal(t, "download https://storage.example/artifact failed", entries[0].Error)
	require.Equal(t, rawURL, req.URL.String())
}
