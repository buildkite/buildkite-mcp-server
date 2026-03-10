package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type UserClient interface {
	CurrentUser(ctx context.Context) (buildkite.User, *buildkite.Response, error)
}

type CurrentUserArgs struct{}

func CurrentUser() (mcp.Tool, mcp.ToolHandlerFor[CurrentUserArgs, any], []string) {
	tool := mcp.Tool{
		Name:        "current_user",
		Description: "Get details about the user account that owns the API token, including name, email, avatar, and account creation date",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Current User",
			ReadOnlyHint: true,
		},
	}
	handler := func(ctx context.Context, request *mcp.CallToolRequest, args CurrentUserArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.CurrentUser")
		defer span.End()

		deps := DepsFromContext(ctx)
		user, _, err := deps.UserClient.CurrentUser(ctx)
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		return mcpTextResult(span, &user)
	}
	scopes := []string{"read_user"}
	return tool, handler, scopes
}
