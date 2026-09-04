package recording

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
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
	// Capture request body before forwarding so it can be recorded and restored.
	var reqBody []byte
	if req.Body != nil && req.Body != http.NoBody {
		var err error
		reqBody, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(reqBody))
		}
	}

	resp, err := t.wrapped.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	respBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	if readErr != nil {
		return resp, nil
	}

	entry := buildHAREntry(req, reqBody, resp, respBody)

	t.mu.Lock()
	t.har.Log.Entries = append(t.har.Log.Entries, entry)
	if saveErr := t.har.save(t.harPath); saveErr != nil {
		log.Warn().Err(saveErr).Str("path", t.harPath).Msg("recording: failed to save HAR file")
	}
	t.mu.Unlock()

	return resp, nil
}

// ReplayTransport serves recorded responses from a HAR file without making real network calls.
// Requests are matched by method + full URL (+ request body for write methods).
// Repeated calls to the same key are served in recorded order.
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
		key := entryKey(e.Request.Method, e.Request.URL, e.Request.PostData)
		entries[key] = append(entries[key], e)
	}
	return &ReplayTransport{entries: entries}, nil
}

// RoundTrip returns the next recorded response matching the request's method, URL, and body.
func (t *ReplayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var postData *HARPostData
	if req.Body != nil && req.Body != http.NoBody {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			if len(body) > 0 {
				postData = &HARPostData{
					MimeType: req.Header.Get("Content-Type"),
					Text:     string(body),
				}
			}
		}
	}

	key := entryKey(req.Method, req.URL.String(), postData)

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

// entryKey returns the lookup key for a HAR entry.
// Only the path and query string are used, not the scheme or host, so a recording made against
// one base URL (e.g. https://api.buildkite.com) can be replayed against another (e.g. a local stub).
// For requests with a body the body text is appended so that distinct POSTs to the same path are
// matched correctly.
func entryKey(method, rawURL string, postData *HARPostData) string {
	path := requestURI(rawURL)
	if postData != nil && postData.Text != "" {
		return method + " " + path + "\n" + postData.Text
	}
	return method + " " + path
}

// requestURI returns the path and query string of rawURL, falling back to the full string on parse error.
func requestURI(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.RequestURI()
}

func buildHAREntry(req *http.Request, reqBody []byte, resp *http.Response, respBody []byte) HAREntry {
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

	var postData *HARPostData
	if len(reqBody) > 0 {
		postData = &HARPostData{
			MimeType: req.Header.Get("Content-Type"),
			Text:     string(reqBody),
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

	content := harContent(respBody, mimeType)

	return HAREntry{
		Request: HARRequest{
			Method:      req.Method,
			URL:         req.URL.String(),
			HTTPVersion: "HTTP/1.1",
			Headers:     reqHeaders,
			QueryString: queryString,
			PostData:    postData,
			BodySize:    len(reqBody),
			HeadersSize: -1,
		},
		Response: HARResponse{
			Status:      resp.StatusCode,
			StatusText:  http.StatusText(resp.StatusCode),
			HTTPVersion: "HTTP/1.1",
			Headers:     respHeaders,
			Content:     content,
			RedirectURL: "",
			BodySize:    len(respBody),
			HeadersSize: -1,
		},
	}
}

// harContent encodes the response body as plain text or base64 depending on whether it is valid UTF-8.
func harContent(body []byte, mimeType string) HARContent {
	if utf8.Valid(body) {
		return HARContent{
			Size:     len(body),
			MimeType: mimeType,
			Text:     string(body),
		}
	}
	return HARContent{
		Size:     len(body),
		MimeType: mimeType,
		Text:     base64.StdEncoding.EncodeToString(body),
		Encoding: "base64",
	}
}

func harEntryToResponse(req *http.Request, entry HAREntry) *http.Response {
	header := make(http.Header, len(entry.Response.Headers))
	for _, h := range entry.Response.Headers {
		header.Add(h.Name, h.Value)
	}

	body := decodeHARContent(entry.Response.Content)

	return &http.Response{
		Status:     fmt.Sprintf("%d %s", entry.Response.Status, entry.Response.StatusText),
		StatusCode: entry.Response.Status,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     header,
		Body:       io.NopCloser(body),
		Request:    req,
	}
}

func decodeHARContent(content HARContent) *bytes.Reader {
	if content.Encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(content.Text)
		if err == nil {
			return bytes.NewReader(decoded)
		}
	}
	return bytes.NewReader([]byte(content.Text))
}
