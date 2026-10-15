package recording_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/stretchr/testify/require"
)

type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
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
		entries = append(entries, e)
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
