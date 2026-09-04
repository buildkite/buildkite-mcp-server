package recording_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/stretchr/testify/require"
)

type mockTransport struct {
	responses []*http.Response
	index     int
}

func (m *mockTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	if m.index >= len(m.responses) {
		return nil, fmt.Errorf("mock: no more responses")
	}
	resp := m.responses[m.index]
	m.index++
	return resp, nil
}

func newMockResponse(status int, body string, contentType string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", contentType)
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     h,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func newMockResponseBytes(status int, body []byte, contentType string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", contentType)
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func TestRecordingTransport(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "test.har")

	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(200, `{"id":"org1"}`, "application/json"),
		newMockResponse(200, `[{"slug":"my-pipeline"}]`, "application/json"),
	}}

	transport, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)

	req1, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	req1.Header.Set("Authorization", "Bearer secret-token")
	resp1, err := transport.RoundTrip(req1)
	r.NoError(err)
	body1, _ := io.ReadAll(resp1.Body)
	r.JSONEq(`{"id":"org1"}`, string(body1))

	req2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines", nil)
	resp2, err := transport.RoundTrip(req2)
	r.NoError(err)
	body2, _ := io.ReadAll(resp2.Body)
	r.JSONEq(`[{"slug":"my-pipeline"}]`, string(body2))

	har, err := recording.LoadHAR(harPath)
	r.NoError(err)
	r.Len(har.Log.Entries, 2)

	e0 := har.Log.Entries[0]
	r.Equal("GET", e0.Request.Method)
	r.Equal("https://api.buildkite.com/v2/organizations", e0.Request.URL)
	r.Equal(200, e0.Response.Status)
	r.JSONEq(`{"id":"org1"}`, e0.Response.Content.Text)
	// Authorization header must be stripped
	for _, h := range e0.Request.Headers {
		r.NotEqual("Authorization", h.Name)
	}

	e1 := har.Log.Entries[1]
	r.Equal("https://api.buildkite.com/v2/organizations/my-org/pipelines", e1.Request.URL)
	r.JSONEq(`[{"slug":"my-pipeline"}]`, e1.Response.Content.Text)
}

func TestRecordingTransportPostBody(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "post.har")

	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(201, `{"number":1}`, "application/json"),
		newMockResponse(201, `{"number":2}`, "application/json"),
	}}

	transport, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)

	body1 := `{"branch":"main","message":"build 1"}`
	req1, _ := http.NewRequest("POST", "https://api.buildkite.com/v2/organizations/my-org/pipelines/my-pipe/builds",
		strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	_, err = transport.RoundTrip(req1)
	r.NoError(err)

	body2 := `{"branch":"feat","message":"build 2"}`
	req2, _ := http.NewRequest("POST", "https://api.buildkite.com/v2/organizations/my-org/pipelines/my-pipe/builds",
		strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	_, err = transport.RoundTrip(req2)
	r.NoError(err)

	har, err := recording.LoadHAR(harPath)
	r.NoError(err)
	r.Len(har.Log.Entries, 2)
	r.NotNil(har.Log.Entries[0].Request.PostData)
	r.Equal(body1, har.Log.Entries[0].Request.PostData.Text)
	r.NotNil(har.Log.Entries[1].Request.PostData)
	r.Equal(body2, har.Log.Entries[1].Request.PostData.Text)
}

func TestRecordingTransportBinaryResponse(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "binary.har")

	// Bytes that are not valid UTF-8
	binaryBody := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00} // gzip magic bytes

	mock := &mockTransport{responses: []*http.Response{
		newMockResponseBytes(200, binaryBody, "application/gzip"),
	}}

	transport, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)

	req, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/jobs/123/log.gz", nil)
	resp, err := transport.RoundTrip(req)
	r.NoError(err)
	respBody, _ := io.ReadAll(resp.Body)
	r.Equal(binaryBody, respBody)

	har, err := recording.LoadHAR(harPath)
	r.NoError(err)
	r.Equal("base64", har.Log.Entries[0].Response.Content.Encoding)
	r.Equal(base64.StdEncoding.EncodeToString(binaryBody), har.Log.Entries[0].Response.Content.Text)
}

func TestReplayTransportAnyOrder(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "test.har")

	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(200, `{"id":"org1"}`, "application/json"),
		newMockResponse(200, `[{"slug":"my-pipeline"}]`, "application/json"),
	}}
	rec, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)
	req1, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	_, _ = rec.RoundTrip(req1)
	req2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines", nil)
	_, _ = rec.RoundTrip(req2)

	// Replay in reverse order — should still work
	replay, err := recording.NewReplayTransport(harPath)
	r.NoError(err)

	replayReq2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines", nil)
	resp2, err := replay.RoundTrip(replayReq2)
	r.NoError(err)
	body2, _ := io.ReadAll(resp2.Body)
	r.JSONEq(`[{"slug":"my-pipeline"}]`, string(body2))

	replayReq1, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	resp1, err := replay.RoundTrip(replayReq1)
	r.NoError(err)
	body1, _ := io.ReadAll(resp1.Body)
	r.JSONEq(`{"id":"org1"}`, string(body1))
}

func TestReplayTransportPostBodyMatching(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "post.har")

	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(201, `{"number":1}`, "application/json"),
		newMockResponse(201, `{"number":2}`, "application/json"),
	}}
	rec, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)

	url := "https://api.buildkite.com/v2/organizations/my-org/pipelines/my-pipe/builds"
	body1 := `{"branch":"main"}`
	body2 := `{"branch":"feat"}`
	req1, _ := http.NewRequest("POST", url, strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	_, _ = rec.RoundTrip(req1)
	req2, _ := http.NewRequest("POST", url, strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	_, _ = rec.RoundTrip(req2)

	replay, err := recording.NewReplayTransport(harPath)
	r.NoError(err)

	// Request body2 first — should get response 2
	replayReq2, _ := http.NewRequest("POST", url, strings.NewReader(body2))
	replayReq2.Header.Set("Content-Type", "application/json")
	resp2, err := replay.RoundTrip(replayReq2)
	r.NoError(err)
	got2, _ := io.ReadAll(resp2.Body)
	r.JSONEq(`{"number":2}`, string(got2))

	// Request body1 — should get response 1
	replayReq1, _ := http.NewRequest("POST", url, strings.NewReader(body1))
	replayReq1.Header.Set("Content-Type", "application/json")
	resp1, err := replay.RoundTrip(replayReq1)
	r.NoError(err)
	got1, _ := io.ReadAll(resp1.Body)
	r.JSONEq(`{"number":1}`, string(got1))
}

func TestReplayTransportBinaryResponse(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "binary.har")

	binaryBody := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}
	mock := &mockTransport{responses: []*http.Response{
		newMockResponseBytes(200, binaryBody, "application/gzip"),
	}}
	rec, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)
	req, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/log.gz", nil)
	_, _ = rec.RoundTrip(req)

	replay, err := recording.NewReplayTransport(harPath)
	r.NoError(err)

	replayReq, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/log.gz", nil)
	resp, err := replay.RoundTrip(replayReq)
	r.NoError(err)
	got, _ := io.ReadAll(resp.Body)
	r.Equal(binaryBody, got)
}

func TestReplayTransportRepeatedURL(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "pages.har")

	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(200, `[{"slug":"page1"}]`, "application/json"),
		newMockResponse(200, `[{"slug":"page2"}]`, "application/json"),
	}}
	rec, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)
	for range 2 {
		req, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines?page=1", nil)
		_, _ = rec.RoundTrip(req)
	}

	replay, err := recording.NewReplayTransport(harPath)
	r.NoError(err)

	req, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines?page=1", nil)
	resp, err := replay.RoundTrip(req)
	r.NoError(err)
	body, _ := io.ReadAll(resp.Body)
	r.JSONEq(`[{"slug":"page1"}]`, string(body))

	req2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines?page=1", nil)
	resp2, err := replay.RoundTrip(req2)
	r.NoError(err)
	body2, _ := io.ReadAll(resp2.Body)
	r.JSONEq(`[{"slug":"page2"}]`, string(body2))
}

func TestReplayTransportErrors(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "test.har")

	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(200, `{}`, "application/json"),
	}}
	rec, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)
	req, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	_, _ = rec.RoundTrip(req)

	replay, err := recording.NewReplayTransport(harPath)
	r.NoError(err)

	// Unknown URL
	unknownReq, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/unknown", nil)
	_, err = replay.RoundTrip(unknownReq)
	r.Error(err)
	r.Contains(err.Error(), "no recorded entry")

	// Consume the entry then verify exhaustion error
	knownReq, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	_, err = replay.RoundTrip(knownReq)
	r.NoError(err)

	knownReq2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	_, err = replay.RoundTrip(knownReq2)
	r.Error(err)
	r.Contains(err.Error(), "no recorded entry")
}

// TestBinaryBodyRoundTrip verifies that binary (non-UTF-8) response bodies survive a full
// record → HAR file → replay cycle with bytes identical to the original.
func TestBinaryBodyRoundTrip(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "binary-rt.har")
	// Gzip magic bytes — definitely not valid UTF-8.
	original := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff}

	// Record
	mock := &mockTransport{responses: []*http.Response{
		newMockResponseBytes(200, original, "application/gzip"),
	}}
	rec, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)
	recReq, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/jobs/abc/log.gz", nil)
	recResp, err := rec.RoundTrip(recReq)
	r.NoError(err)
	recBody, _ := io.ReadAll(recResp.Body)
	r.Equal(original, recBody, "recording should return the original bytes to the caller")

	// Verify the HAR stored it as base64
	har, err := recording.LoadHAR(harPath)
	r.NoError(err)
	r.Equal("base64", har.Log.Entries[0].Response.Content.Encoding)
	r.Equal(base64.StdEncoding.EncodeToString(original), har.Log.Entries[0].Response.Content.Text)

	// Replay
	replay, err := recording.NewReplayTransport(harPath)
	r.NoError(err)
	replayReq, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/jobs/abc/log.gz", nil)
	replayResp, err := replay.RoundTrip(replayReq)
	r.NoError(err)
	replayBody, _ := io.ReadAll(replayResp.Body)
	r.Equal(original, replayBody, "replay should return the original bytes unchanged")
}

// TestReplayTransportBaseURLMismatch verifies that a HAR recorded against one host can be
// replayed when requests arrive with a different host (e.g. a local stub server).
// Matching is keyed on path+query only, not on scheme or host.
func TestReplayTransportBaseURLMismatch(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "mismatch.har")

	// Record against the real base URL
	mock := &mockTransport{responses: []*http.Response{
		newMockResponse(200, `[{"slug":"my-pipeline"}]`, "application/json"),
	}}
	rec, err := recording.NewRecordingTransport(mock, harPath, "test")
	r.NoError(err)
	recReq, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines", nil)
	_, err = rec.RoundTrip(recReq)
	r.NoError(err)

	// Replay — request arrives with a different host (local stub)
	replay, err := recording.NewReplayTransport(harPath)
	r.NoError(err)
	replayReq, _ := http.NewRequest("GET", "http://localhost:9999/v2/organizations/my-org/pipelines", nil)
	resp, err := replay.RoundTrip(replayReq)
	r.NoError(err)
	body, _ := io.ReadAll(resp.Body)
	r.JSONEq(`[{"slug":"my-pipeline"}]`, string(body))
}

// TestFullClientStackIntegration exercises the recording and replay transports wired into the
// same HTTP client stack used in production: header injection → otelhttp → recording/replay.
// This guards against the OTEL instrumentation or header injection interfering with body handling.
func TestFullClientStackIntegration(t *testing.T) {
	r := require.New(t)

	// Fake API server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v2/organizations":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"slug":"my-org"}]`)
		case "/v2/organizations/my-org/pipelines":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"slug":"my-pipe"}]`)
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	harPath := filepath.Join(t.TempDir(), "stack.har")

	// --- Record phase ---
	recTransport, err := recording.NewRecordingTransport(http.DefaultTransport, harPath, "test")
	r.NoError(err)
	recClient := trace.NewHTTPClientWithHeadersAndTransport(
		map[string]string{"X-Test": "recording"},
		recTransport,
	)

	for _, path := range []string{"/v2/organizations", "/v2/organizations/my-org/pipelines"} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		resp, doErr := recClient.Do(req)
		r.NoError(doErr)
		resp.Body.Close()
	}

	har, err := recording.LoadHAR(harPath)
	r.NoError(err)
	r.Len(har.Log.Entries, 2)

	// --- Replay phase (no server needed) ---
	replayTransport, err := recording.NewReplayTransport(harPath)
	r.NoError(err)
	replayClient := trace.NewHTTPClientWithHeadersAndTransport(
		map[string]string{"X-Test": "replay"},
		replayTransport,
	)

	// Requests can arrive in any order
	req2, _ := http.NewRequest("GET", srv.URL+"/v2/organizations/my-org/pipelines", nil)
	resp2, err := replayClient.Do(req2)
	r.NoError(err)
	body2, _ := io.ReadAll(resp2.Body)
	r.JSONEq(`[{"slug":"my-pipe"}]`, string(body2))

	req1, _ := http.NewRequest("GET", srv.URL+"/v2/organizations", nil)
	resp1, err := replayClient.Do(req1)
	r.NoError(err)
	body1, _ := io.ReadAll(resp1.Body)
	r.JSONEq(`[{"slug":"my-org"}]`, string(body1))
}
