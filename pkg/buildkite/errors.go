package buildkite

import (
	"errors"
	"net/http"

	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrUnauthorized is returned when the Buildkite API responds with HTTP 401.
// It is a *jsonrpc.Error so the MCP SDK passes it through the middleware chain
// rather than converting it to a tool result body. Library consumers can use
// errors.Is to detect this and trigger a reauth flow.
//
// The error code 401 is a positive, non-standard JSON-RPC code (conventional
// codes are negative). It is chosen deliberately for HTTP semantic alignment;
// jsonrpc.Error.Is compares by code value only, so detection via errors.Is is
// unaffected by the sign.
//
// Do not modify the fields of this value; treat it as a read-only sentinel.
var ErrUnauthorized = &jsonrpc.Error{
	Code:    http.StatusUnauthorized,
	Message: "buildkite: unauthorized",
}

func isBuildkiteUnauthorized(err error) bool {
	if errors.Is(err, ErrUnauthorized) {
		return true
	}

	var errResp *buildkite.ErrorResponse
	return errors.As(err, &errResp) && errResp.Response != nil && errResp.Response.StatusCode == http.StatusUnauthorized
}

// handleBuildkiteError converts a Buildkite API error into tool handler return values.
// On a 401 it returns (nil, nil, ErrUnauthorized) so the error propagates as a
// JSON-RPC error and can be intercepted by middleware. On other errors it returns
// a tool result error so the tool call succeeds at the JSON-RPC level but with an
// error body.
func handleBuildkiteError(err error) (*mcp.CallToolResult, any, error) {
	if isBuildkiteUnauthorized(err) {
		return nil, nil, ErrUnauthorized
	}

	var errResp *buildkite.ErrorResponse
	if errors.As(err, &errResp) {
		if errResp.RawBody != nil {
			return utils.NewToolResultError(string(errResp.RawBody)), nil, nil
		}
		if errResp.Message != "" {
			return utils.NewToolResultError(errResp.Message), nil, nil
		}
	}
	return utils.NewToolResultError(err.Error()), nil, nil
}
