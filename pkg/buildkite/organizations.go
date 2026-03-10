package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type OrganizationsClient interface {
	List(ctx context.Context, options *buildkite.OrganizationListOptions) ([]buildkite.Organization, *buildkite.Response, error)
}

type UserTokenOrganizationArgs struct{}

func UserTokenOrganization() (mcp.Tool, mcp.ToolHandlerFor[UserTokenOrganizationArgs, any], []string) {
	return mcp.Tool{
			Name:        "user_token_organization",
			Description: "Get the organization associated with the user token used for this request",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Organization for User Token",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args UserTokenOrganizationArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.UserTokenOrganization")
			defer span.End()

			deps := DepsFromContext(ctx)
			orgs, _, err := deps.OrganizationsClient.List(ctx, &buildkite.OrganizationListOptions{})
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			if len(orgs) == 0 {
				return utils.NewToolResultError("no organization found for the current user token"), nil, nil
			}

			return mcpTextResult(span, &orgs[0])
		}, []string{"read_organizations"}
}

func HandleUserTokenOrganizationPrompt(
	ctx context.Context,
	request *mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: "When asked for detail of a users pipelines start by looking up the user's token organization",
		Messages: []*mcp.PromptMessage{
			{
				Role: mcp.Role("user"),
				Content: &mcp.TextContent{
					Text: "When asked for detail of a users pipelines start by looking up the user's token organization",
				},
			},
		},
	}, nil
}
