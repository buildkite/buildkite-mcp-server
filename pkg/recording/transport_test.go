package recording_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
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
	r.Equal(`{"id":"org1"}`, string(body1))

	req2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines", nil)
	resp2, err := transport.RoundTrip(req2)
	r.NoError(err)
	body2, _ := io.ReadAll(resp2.Body)
	r.Equal(`[{"slug":"my-pipeline"}]`, string(body2))

	har, err := recording.LoadHAR(harPath)
	r.NoError(err)
	r.Len(har.Log.Entries, 2)

	e0 := har.Log.Entries[0]
	r.Equal("GET", e0.Request.Method)
	r.Equal("https://api.buildkite.com/v2/organizations", e0.Request.URL)
	r.Equal(200, e0.Response.Status)
	r.Equal(`{"id":"org1"}`, e0.Response.Content.Text)
	for _, h := range e0.Request.Headers {
		r.NotEqual("Authorization", h.Name)
	}

	e1 := har.Log.Entries[1]
	r.Equal("https://api.buildkite.com/v2/organizations/my-org/pipelines", e1.Request.URL)
	r.Equal(`[{"slug":"my-pipeline"}]`, e1.Response.Content.Text)
}

func TestReplayTransportAnyOrder(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "test.har")

	// Record two distinct URLs
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
	r.Equal(`[{"slug":"my-pipeline"}]`, string(body2))

	replayReq1, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	resp1, err := replay.RoundTrip(replayReq1)
	r.NoError(err)
	body1, _ := io.ReadAll(resp1.Body)
	r.Equal(`{"id":"org1"}`, string(body1))
}

func TestReplayTransportRepeatedURL(t *testing.T) {
	r := require.New(t)

	harPath := filepath.Join(t.TempDir(), "pages.har")

	// Record two calls to the same URL (pagination)
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
	r.Equal(`[{"slug":"page1"}]`, string(body))

	req2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations/my-org/pipelines?page=1", nil)
	resp2, err := replay.RoundTrip(req2)
	r.NoError(err)
	body2, _ := io.ReadAll(resp2.Body)
	r.Equal(`[{"slug":"page2"}]`, string(body2))
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

	// Consume the one entry then try again
	knownReq, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	_, err = replay.RoundTrip(knownReq)
	r.NoError(err)

	knownReq2, _ := http.NewRequest("GET", "https://api.buildkite.com/v2/organizations", nil)
	_, err = replay.RoundTrip(knownReq2)
	r.Error(err)
	r.Contains(err.Error(), "no recorded entry")
}
