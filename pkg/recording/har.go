package recording

import (
	"encoding/json"
	"os"
)

// HAR is the top-level structure of an HTTP Archive (HAR 1.2) file.
type HAR struct {
	Log HARLog `json:"log"`
}

// HARLog is the log section of a HAR file.
type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Entries []HAREntry `json:"entries"`
}

// HARCreator identifies the tool that produced the HAR file.
type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// HAREntry is a single request/response pair.
type HAREntry struct {
	Request  HARRequest  `json:"request"`
	Response HARResponse `json:"response"`
}

// HARRequest captures the HTTP request fields we use.
type HARRequest struct {
	Method      string         `json:"method"`
	URL         string         `json:"url"`
	HTTPVersion string         `json:"httpVersion"`
	Headers     []HARNameValue `json:"headers"`
	QueryString []HARNameValue `json:"queryString"`
	PostData    *HARPostData   `json:"postData,omitempty"`
	BodySize    int            `json:"bodySize"`
	HeadersSize int            `json:"headersSize"`
}

// HARPostData holds a recorded request body.
type HARPostData struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

// HARResponse captures the HTTP response fields we use.
type HARResponse struct {
	Status      int            `json:"status"`
	StatusText  string         `json:"statusText"`
	HTTPVersion string         `json:"httpVersion"`
	Headers     []HARNameValue `json:"headers"`
	Content     HARContent     `json:"content"`
	RedirectURL string         `json:"redirectURL"`
	BodySize    int            `json:"bodySize"`
	HeadersSize int            `json:"headersSize"`
}

// HARContent holds the response body and its metadata.
// When the body is not valid UTF-8, Text contains a base64-encoded copy and Encoding is "base64".
type HARContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

// HARNameValue is a key-value pair used for headers and query string parameters.
type HARNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// LoadHAR reads and parses a HAR file from disk.
func LoadHAR(path string) (*HAR, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var h HAR
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func newHAR(version string) *HAR {
	return &HAR{
		Log: HARLog{
			Version: "1.2",
			Creator: HARCreator{
				Name:    "buildkite-mcp-server",
				Version: version,
			},
			Entries: []HAREntry{},
		},
	}
}

func (h *HAR) save(path string) error {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
