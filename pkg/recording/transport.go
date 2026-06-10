package recording

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// RecordingTransport wraps an http.RoundTripper and appends each request/response to a HAR file.
type RecordingTransport struct {
	wrapped http.RoundTripper
	harPath string
	har     *HAR
	mu      sync.Mutex
}

// NewRecordingTransport creates a RecordingTransport that writes to harPath.
// The file is created (or truncated) immediately to validate the path.
func NewRecordingTransport(wrapped http.RoundTripper, harPath, version string) (*RecordingTransport, error) {
	h := newHAR(version)
	if err := h.save(harPath); err != nil {
		return nil, fmt.Errorf("recording: cannot write to %s: %w", harPath, err)
	}
	return &RecordingTransport{
		wrapped: wrapped,
		harPath: harPath,
		har:     h,
	}, nil
}

// RoundTrip forwards the request to the wrapped transport, records the response, and returns it.
func (t *RecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.wrapped.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil {
		return resp, nil
	}

	entry := buildHAREntry(req, resp, body)

	t.mu.Lock()
	t.har.Log.Entries = append(t.har.Log.Entries, entry)
	_ = t.har.save(t.harPath)
	t.mu.Unlock()

	return resp, nil
}

// ReplayTransport serves recorded responses from a HAR file without making real network calls.
// Requests are matched by method + full URL. Repeated calls to the same URL are served in recorded order.
type ReplayTransport struct {
	mu      sync.Mutex
	entries map[string][]HAREntry
}

// NewReplayTransport loads a HAR file and prepares it for replay.
func NewReplayTransport(harPath string) (*ReplayTransport, error) {
	har, err := LoadHAR(harPath)
	if err != nil {
		return nil, fmt.Errorf("replay: failed to load %s: %w", harPath, err)
	}
	entries := make(map[string][]HAREntry, len(har.Log.Entries))
	for _, e := range har.Log.Entries {
		key := e.Request.Method + " " + e.Request.URL
		entries[key] = append(entries[key], e)
	}
	return &ReplayTransport{entries: entries}, nil
}

// RoundTrip returns the next recorded response matching the request's method and URL.
func (t *ReplayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()

	t.mu.Lock()
	list := t.entries[key]
	if len(list) == 0 {
		t.mu.Unlock()
		return nil, fmt.Errorf("replay: no recorded entry for %s %s", req.Method, req.URL)
	}
	entry := list[0]
	t.entries[key] = list[1:]
	t.mu.Unlock()

	return harEntryToResponse(req, entry), nil
}

func buildHAREntry(req *http.Request, resp *http.Response, body []byte) HAREntry {
	var reqHeaders []HARNameValue
	for k, vs := range req.Header {
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		for _, v := range vs {
			reqHeaders = append(reqHeaders, HARNameValue{Name: k, Value: v})
		}
	}

	var queryString []HARNameValue
	for k, vs := range req.URL.Query() {
		for _, v := range vs {
			queryString = append(queryString, HARNameValue{Name: k, Value: v})
		}
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	var respHeaders []HARNameValue
	for k, vs := range resp.Header {
		for _, v := range vs {
			respHeaders = append(respHeaders, HARNameValue{Name: k, Value: v})
		}
	}

	return HAREntry{
		Request: HARRequest{
			Method:      req.Method,
			URL:         req.URL.String(),
			HTTPVersion: "HTTP/1.1",
			Headers:     reqHeaders,
			QueryString: queryString,
			BodySize:    -1,
			HeadersSize: -1,
		},
		Response: HARResponse{
			Status:      resp.StatusCode,
			StatusText:  http.StatusText(resp.StatusCode),
			HTTPVersion: "HTTP/1.1",
			Headers:     respHeaders,
			Content: HARContent{
				Size:     len(body),
				MimeType: mimeType,
				Text:     string(body),
			},
			RedirectURL: "",
			BodySize:    len(body),
			HeadersSize: -1,
		},
	}
}

func harEntryToResponse(req *http.Request, entry HAREntry) *http.Response {
	header := make(http.Header, len(entry.Response.Headers))
	for _, h := range entry.Response.Headers {
		header.Add(h.Name, h.Value)
	}
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", entry.Response.Status, entry.Response.StatusText),
		StatusCode: entry.Response.Status,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(entry.Response.Content.Text)),
		Request:    req,
	}
}
