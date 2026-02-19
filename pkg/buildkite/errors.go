package buildkite

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
)

// handleAPIError converts a Buildkite API error into an MCP tool result error
// with user-friendly messages for common error cases like authentication failures.
func handleAPIError(err error) *mcp.CallToolResult {
	if err == nil {
		return nil
	}

	var errResp *buildkite.ErrorResponse
	if errors.As(err, &errResp) {
		// Check for authentication/authorization errors
		if errResp.Response != nil {
			statusCode := errResp.Response.StatusCode

			switch statusCode {
			case http.StatusUnauthorized:
				return mcp.NewToolResultError(
					"Authentication failed: Your API token is invalid or has expired. " +
						"Please check your BUILDKITE_API_TOKEN and ensure it's still valid.",
				)
			case http.StatusForbidden:
				// Try to get detailed error from RawBody or Message
				detailedMsg := getDetailedErrorMessage(errResp)
				return mcp.NewToolResultError(
					fmt.Sprintf(
						"Permission denied: Your API token doesn't have the required permissions for this operation. %s",
						detailedMsg,
					),
				)
			}
		}

		// For other errors, return the raw body if available (usually has detailed error info)
		if errResp.RawBody != nil {
			return mcp.NewToolResultError(string(errResp.RawBody))
		}

		// Fall back to the message field
		if errResp.Message != "" {
			return mcp.NewToolResultError(errResp.Message)
		}
	}

	// Default: return the error string
	return mcp.NewToolResultError(err.Error())
}

// getDetailedErrorMessage extracts a detailed error message from ErrorResponse
func getDetailedErrorMessage(errResp *buildkite.ErrorResponse) string {
	if errResp.RawBody != nil {
		return string(errResp.RawBody)
	}
	if errResp.Message != "" {
		return errResp.Message
	}
	return ""
}
