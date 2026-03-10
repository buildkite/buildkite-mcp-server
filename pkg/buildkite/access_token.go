package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AccessTokenClient interface {
	Get(ctx context.Context) (buildkite.AccessToken, *buildkite.Response, error)
}

type AccessTokenArgs struct{}

func AccessToken() (mcp.Tool, mcp.ToolHandlerFor[AccessTokenArgs, any], []string) {
	return mcp.Tool{
			Name:        "access_token",
			Description: "Get information about the current API access token including its scopes and UUID",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Access Token",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args AccessTokenArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.AccessToken")
			defer span.End()

			deps := DepsFromContext(ctx)
			token, _, err := deps.AccessTokensClient.Get(ctx)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			return mcpTextResult(span, &token)
		}, []string{"read_user"}
}
