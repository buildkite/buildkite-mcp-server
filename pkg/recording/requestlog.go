package recording

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/rs/zerolog/log"
)

// RequestLogEntry is one line of a request log: a single outbound API request
// and, when it was made on behalf of an MCP tool call, that call's identity.
// It deliberately holds no headers or bodies (no credentials, small lines).
type RequestLogEntry struct {
	Source     string  `json:"source"`
	Time       string  `json:"time"`
	DurationMS float64 `json:"duration_ms"`
	Method     string  `json:"method"`
	URL        string  `json:"url"`
	Status     int     `json:"status,omitempty"`
	Error      string  `json:"error,omitempty"`
	Bytes      int64   `json:"bytes,omitempty"`
	Tool       string  `json:"tool,omitempty"`
	ToolCallID string  `json:"tool_call_id,omitempty"`
}

// RequestLogTransport wraps an http.RoundTripper and appends one JSON line per
// request to a file. Unlike RecordingTransport it keeps no bodies and only
// appends, so it stays cheap for long sessions and log-heavy tools.
type RequestLogTransport struct {
	wrapped http.RoundTripper
	mu      sync.Mutex
	file    *os.File
}

// NewRequestLogTransport creates a RequestLogTransport appending to path. The
// file is opened (and created if missing) immediately to validate the path;
// it is appended to, not truncated, so a restarted server keeps earlier lines.
// A started record distinguishes an active logger with no requests from a
// placeholder file left by a harness running an older server.
func NewRequestLogTransport(wrapped http.RoundTripper, path string) (*RequestLogTransport, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path is a user-supplied output file
	if err != nil {
		return nil, fmt.Errorf("request log: cannot open %s: %w", path, err)
	}
	if _, err := f.WriteString("{\"source\":\"mcp\",\"type\":\"started\"}\n"); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("request log: cannot write startup record: %w", err)
	}
	return &RequestLogTransport{wrapped: wrapped, file: f}, nil
}

// RoundTrip forwards the request and logs it, whether or not it succeeded.
func (t *RequestLogTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.wrapped.RoundTrip(req)

	// Redirects may target signed storage URLs. Keep only endpoint identity,
	// without modifying the request that carries the download credentials.
	loggedURL := *req.URL
	loggedURL.User = nil
	loggedURL.RawQuery = ""
	loggedURL.ForceQuery = false
	loggedURL.Fragment = ""
	loggedURL.RawFragment = ""
	entry := RequestLogEntry{
		Source:     "mcp",
		Time:       start.UTC().Format(time.RFC3339Nano),
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Method:     req.Method,
		URL:        loggedURL.String(),
	}
	if err != nil {
		entry.Error = strings.ReplaceAll(err.Error(), req.URL.String(), loggedURL.String())
	} else {
		entry.Status = resp.StatusCode
		if resp.ContentLength > 0 {
			entry.Bytes = resp.ContentLength
		}
	}
	if tc, ok := trace.ToolCallFromContext(req.Context()); ok {
		entry.Tool = tc.Name
		entry.ToolCallID = tc.ID
	}

	line, marshalErr := json.Marshal(entry)
	if marshalErr == nil {
		t.mu.Lock()
		_, writeErr := t.file.Write(append(line, '\n'))
		t.mu.Unlock()
		if writeErr != nil {
			log.Warn().Err(writeErr).Msg("request log: failed to write entry")
		}
	}

	return resp, err
}
