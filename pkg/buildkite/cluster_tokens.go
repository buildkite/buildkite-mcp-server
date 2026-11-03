package buildkite

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ClusterTokensClient interface {
	List(ctx context.Context, org, clusterID string, opts *buildkite.ClusterTokensListOptions) ([]buildkite.ClusterToken, *buildkite.Response, error)
	Get(ctx context.Context, org, clusterID, tokenID string) (buildkite.ClusterToken, *buildkite.Response, error)
	Create(ctx context.Context, org, clusterID string, input buildkite.ClusterTokenCreate) (buildkite.ClusterToken, *buildkite.Response, error)
	Update(ctx context.Context, org, clusterID, tokenID string, input buildkite.ClusterTokenUpdate) (buildkite.ClusterToken, *buildkite.Response, error)
}

type ListClusterTokensArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

type GetClusterTokenArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	TokenID   string `json:"token_id"`
}

type CreateClusterTokenArgs struct {
	ToolInput
	OrgSlug            string     `json:"org_slug"`
	ClusterID          string     `json:"cluster_id"`
	Description        string     `json:"description"`
	AllowedIPAddresses string     `json:"allowed_ip_addresses,omitempty" jsonschema:"Space-separated IP addresses or CIDR ranges; omission allows any address"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty" jsonschema:"RFC3339 expiry for new agent registrations, at least 10 minutes in the future; omitted means no expiry; cannot be updated"`
	Permissions        []string   `json:"permissions,omitzero" jsonschema:"Replacement permission allowlist; omitted defaults to full permissions; [] grants no actions"`
	AllowAllQueues     *bool      `json:"allow_all_queues,omitempty" jsonschema:"Omitted defaults to all queues; false requires an explicit allowed_queue_ids list; true cannot accompany nonempty IDs"`
	AllowedQueueIDs    []string   `json:"allowed_queue_ids,omitzero" jsonschema:"Queue UUIDs, not keys; with allow_all_queues false, [] allows no queues"`
}

type UpdateClusterTokenArgs struct {
	ToolInput
	OrgSlug            string   `json:"org_slug"`
	ClusterID          string   `json:"cluster_id"`
	TokenID            string   `json:"token_id"`
	Description        *string  `json:"description,omitempty"`
	AllowedIPAddresses *string  `json:"allowed_ip_addresses,omitempty" jsonschema:"Space-separated IP addresses or CIDR ranges; empty string clears the restriction"`
	Permissions        []string `json:"permissions,omitzero" jsonschema:"Replacement permission allowlist; omitted preserves permissions; [] grants no actions"`
	AllowAllQueues     *bool    `json:"allow_all_queues,omitempty" jsonschema:"Omitted preserves the flag; to switch from restricted to all queues send true and allowed_queue_ids []"`
	AllowedQueueIDs    []string `json:"allowed_queue_ids,omitzero" jsonschema:"Replacement queue UUID list, not keys; omitted preserves bindings; [] clears bindings without changing allow_all_queues; nonempty IDs conflict with all queues"`
}

func ListClusterTokens() (mcp.Tool, mcp.ToolHandlerFor[ListClusterTokensArgs, any], []string) {
	return mcp.Tool{
		Name:        "list_cluster_tokens",
		Description: "List a cluster's agent token metadata, permissions, and queue access when available; token secrets are not returned",
		Annotations: &mcp.ToolAnnotations{Title: "List Cluster Tokens", ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListClusterTokensArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListClusterTokens")
		defer span.End()
		pagination := paginationFromArgs(args.Page, args.PerPage)
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("cluster_id", args.ClusterID),
			attribute.Int("page", pagination.Page), attribute.Int("per_page", pagination.PerPage))
		tokens, resp, err := DepsFromContext(ctx).ClusterTokensClient.List(ctx, args.OrgSlug, args.ClusterID, &buildkite.ClusterTokensListOptions{ListOptions: pagination})
		if err != nil {
			return handleBuildkiteError(err)
		}
		result := PaginatedResult[buildkite.ClusterToken]{Items: tokens, Headers: map[string]string{"Link": resp.Header.Get("Link")}}
		span.SetAttributes(attribute.Int("item_count", len(tokens)))
		return mcpTextResult(span, &result)
	}, []string{"read_clusters"}
}

func GetClusterToken() (mcp.Tool, mcp.ToolHandlerFor[GetClusterTokenArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_cluster_token",
		Description: "Get a cluster agent token's metadata, permissions, and queue access when available; the token secret is not returned",
		Annotations: &mcp.ToolAnnotations{Title: "Get Cluster Token", ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetClusterTokenArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetClusterToken")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("cluster_id", args.ClusterID), attribute.String("token_id", args.TokenID))
		token, _, err := DepsFromContext(ctx).ClusterTokensClient.Get(ctx, args.OrgSlug, args.ClusterID, args.TokenID)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &token)
	}, []string{"read_clusters"}
}

func CreateClusterToken() (mcp.Tool, mcp.ToolHandlerFor[CreateClusterTokenArgs, any], []string) {
	return mcp.Tool{
		Name:        "create_cluster_token",
		Description: "Create a cluster agent token with optional permissions and queue restrictions. The token secret is returned only on creation. Null fields are not accepted. Feature availability and validation are enforced by the API",
		InputSchema: clusterTokenInputSchema[CreateClusterTokenArgs](),
		Annotations: &mcp.ToolAnnotations{Title: "Create Cluster Token", DestructiveHint: new(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args CreateClusterTokenArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.CreateClusterToken")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("cluster_id", args.ClusterID))
		input := buildkite.ClusterTokenCreate{Description: args.Description, AllowedIPAddresses: args.AllowedIPAddresses}
		if args.ExpiresAt != nil {
			input.ExpiresAt = &buildkite.Timestamp{Time: *args.ExpiresAt}
		}
		if args.Permissions != nil {
			input.Permissions = buildkite.Some(args.Permissions)
		}
		if args.AllowAllQueues != nil {
			input.AllowAllQueues = buildkite.Some(*args.AllowAllQueues)
		}
		if args.AllowedQueueIDs != nil {
			input.AllowedQueueIDs = buildkite.Some(args.AllowedQueueIDs)
		}
		token, _, err := DepsFromContext(ctx).ClusterTokensClient.Create(ctx, args.OrgSlug, args.ClusterID, input)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &token)
	}, []string{"write_clusters"}
}

func UpdateClusterToken() (mcp.Tool, mcp.ToolHandlerFor[UpdateClusterTokenArgs, any], []string) {
	return mcp.Tool{
		Name:        "update_cluster_token",
		Description: "Update a cluster agent token's metadata, permission allowlist, or queue access; omitted fields remain unchanged and null fields are not accepted. Queue changes affect new agent registrations, not existing agents or jobs. API validation and feature availability apply",
		InputSchema: clusterTokenInputSchema[UpdateClusterTokenArgs](),
		Annotations: &mcp.ToolAnnotations{Title: "Update Cluster Token", DestructiveHint: new(true)},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdateClusterTokenArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.UpdateClusterToken")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("cluster_id", args.ClusterID), attribute.String("token_id", args.TokenID))
		input := buildkite.ClusterTokenUpdate{}
		if args.Description != nil {
			input.Description = buildkite.Some(*args.Description)
		}
		if args.AllowedIPAddresses != nil {
			input.AllowedIPAddresses = buildkite.Some(*args.AllowedIPAddresses)
		}
		if args.Permissions != nil {
			input.Permissions = buildkite.Some(args.Permissions)
		}
		if args.AllowAllQueues != nil {
			input.AllowAllQueues = buildkite.Some(*args.AllowAllQueues)
		}
		if args.AllowedQueueIDs != nil {
			input.AllowedQueueIDs = buildkite.Some(args.AllowedQueueIDs)
		}
		token, _, err := DepsFromContext(ctx).ClusterTokensClient.Update(ctx, args.OrgSlug, args.ClusterID, args.TokenID, input)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &token)
	}, []string{"write_clusters"}
}

// The inferred schema permits null for Go pointers and slices, but the API does
// not. Reject null before decoding can collapse it into an omitted field.
func clusterTokenInputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("generate cluster token input schema: %v", err))
	}
	for _, field := range schema.Properties {
		field.Types = slices.DeleteFunc(field.Types, func(t string) bool { return t == "null" })
	}
	return schema
}
