package buildkite

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestHandleAPIError_Nil(t *testing.T) {
	result := handleAPIError(nil)
	require.Nil(t, result)
}

func TestHandleAPIError_Unauthorized(t *testing.T) {
	assert := require.New(t)

	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader("Unauthorized")),
	}
	err := &buildkite.ErrorResponse{
		Response: resp,
		Message:  "Unauthorized",
	}

	result := handleAPIError(err)
	assert.NotNil(result)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "Authentication failed")
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "API token is invalid or has expired")
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "BUILDKITE_API_TOKEN")
}

func TestHandleAPIError_Forbidden(t *testing.T) {
	assert := require.New(t)

	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader("Forbidden")),
	}
	err := &buildkite.ErrorResponse{
		Response: resp,
		Message:  "Insufficient permissions",
		RawBody:  []byte(`{"message":"Missing required scope: write_builds"}`),
	}

	result := handleAPIError(err)
	assert.NotNil(result)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "Permission denied")
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "required permissions")
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "write_builds")
}

func TestHandleAPIError_WithRawBody(t *testing.T) {
	assert := require.New(t)

	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("Not Found")),
	}
	err := &buildkite.ErrorResponse{
		Response: resp,
		Message:  "Not Found",
		RawBody:  []byte(`{"message":"Pipeline not found"}`),
	}

	result := handleAPIError(err)
	assert.NotNil(result)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "Pipeline not found")
}

func TestHandleAPIError_WithMessage(t *testing.T) {
	assert := require.New(t)

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("Internal Server Error")),
	}
	err := &buildkite.ErrorResponse{
		Response: resp,
		Message:  "Internal Server Error",
	}

	result := handleAPIError(err)
	assert.NotNil(result)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(mcp.TextContent).Text, "Internal Server Error")
}

func TestHandleAPIError_NonBuildkiteError(t *testing.T) {
	assert := require.New(t)

	err := errors.New("generic error message")

	result := handleAPIError(err)
	assert.NotNil(result)
	assert.True(result.IsError)
	assert.Equal("generic error message", result.Content[0].(mcp.TextContent).Text)
}

func TestGetDetailedErrorMessage_RawBody(t *testing.T) {
	assert := require.New(t)

	errResp := &buildkite.ErrorResponse{
		RawBody: []byte("detailed error from raw body"),
		Message: "should not use this",
	}

	msg := getDetailedErrorMessage(errResp)
	assert.Equal("detailed error from raw body", msg)
}

func TestGetDetailedErrorMessage_MessageOnly(t *testing.T) {
	assert := require.New(t)

	errResp := &buildkite.ErrorResponse{
		Message: "error message",
	}

	msg := getDetailedErrorMessage(errResp)
	assert.Equal("error message", msg)
}

func TestGetDetailedErrorMessage_Empty(t *testing.T) {
	assert := require.New(t)

	errResp := &buildkite.ErrorResponse{}

	msg := getDetailedErrorMessage(errResp)
	assert.Equal("", msg)
}
