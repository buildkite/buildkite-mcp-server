package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type OrganizationsClient interface {
	List(ctx context.Context, options *buildkite.OrganizationListOptions) ([]buildkite.Organization, *buildkite.Response, error)
}

func UserTokenOrganization(client OrganizationsClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("user_token_organization",
			mcp.WithDescription("Get the organization associated with the user token used for this request"),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Organization for User Token",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.UserTokenOrganization")
			defer span.End()

			orgs, _, err := client.List(ctx, &buildkite.OrganizationListOptions{})
			if err != nil {
				return handleAPIError(err), nil
			}

			if len(orgs) == 0 {
				return mcp.NewToolResultError("no organization found for the current user token"), nil
			}

			return mcpTextResult(span, &orgs[0])
		}, []string{"read_organizations"}
}

func HandleUserTokenOrganizationPrompt(
	ctx context.Context,
	request mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: "When asked for detail of a users pipelines start by looking up the user's token organization",
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: "When asked for detail of a users pipelines start by looking up the user's token organization",
				},
			},
		},
	}, nil
}
