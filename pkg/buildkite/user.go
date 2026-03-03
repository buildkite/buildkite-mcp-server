package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type UserClient interface {
	CurrentUser(ctx context.Context) (buildkite.User, *buildkite.Response, error)
}

func CurrentUser(client UserClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	tool = mcp.NewTool("current_user",
		mcp.WithDescription("Get details about the user account that owns the API token, including name, email, avatar, and account creation date"),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:        "Get Current User",
			ReadOnlyHint: mcp.ToBoolPtr(true),
		}),
	)
	handler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, span := trace.Start(ctx, "buildkite.CurrentUser")
		defer span.End()

		user, _, err := client.CurrentUser(ctx)
		if err != nil {
			return handleAPIError(err), nil
		}

		return mcpTextResult(span, &user)
	}
	scopes = []string{"read_user"}
	return
}
