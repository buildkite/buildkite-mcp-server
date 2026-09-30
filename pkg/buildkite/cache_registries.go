package buildkite

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type CacheRegistriesClient interface {
	List(ctx context.Context, org, clusterID string, opt *buildkite.CacheRegistriesListOptions) (buildkite.CacheRegistriesList, *buildkite.Response, error)
	Get(ctx context.Context, org, clusterID, registryUUID string) (buildkite.CacheRegistry, *buildkite.Response, error)
	Create(ctx context.Context, org, clusterID string, input buildkite.CacheRegistryCreate) (buildkite.CacheRegistry, *buildkite.Response, error)
	Update(ctx context.Context, org, clusterID, registryUUID string, input buildkite.CacheRegistryUpdate) (buildkite.CacheRegistry, *buildkite.Response, error)
	Delete(ctx context.Context, org, clusterID, registryUUID string) (*buildkite.Response, error)
}

type ListCacheRegistriesArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	After     string `json:"after,omitempty" jsonschema:"Cursor from links.next in a previous response; mutually exclusive with before"`
	Before    string `json:"before,omitempty" jsonschema:"Cursor from links.prev in a previous response; mutually exclusive with after"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Results per page (max 100; API default 30)"`
}

func ListCacheRegistries() (mcp.Tool, mcp.ToolHandlerFor[ListCacheRegistriesArgs, any], []string) {
	return mcp.Tool{
		Name:        "list_cache_registries",
		Description: "List cache registries in a cluster, ordered by slug. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Cache Registries",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListCacheRegistriesArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListCacheRegistries")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("cluster_id", args.ClusterID),
			attribute.Int("per_page", args.PerPage),
		)

		if args.After != "" && args.Before != "" {
			return utils.NewToolResultError("'after' and 'before' are mutually exclusive; provide at most one"), nil, nil
		}

		deps := DepsFromContext(ctx)
		registries, _, err := deps.CacheRegistriesClient.List(ctx, args.OrgSlug, args.ClusterID, &buildkite.CacheRegistriesListOptions{
			After:   args.After,
			Before:  args.Before,
			PerPage: args.PerPage,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		span.SetAttributes(attribute.Int("item_count", len(registries.Items)))
		return mcpTextResult(span, &registries)
	}, []string{"read_clusters"}
}

type GetCacheRegistryArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	ClusterID    string `json:"cluster_id"`
	RegistryUUID string `json:"registry_uuid" jsonschema:"Cache registry UUID; the registry slug is not accepted"`
}

func GetCacheRegistry() (mcp.Tool, mcp.ToolHandlerFor[GetCacheRegistryArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_cache_registry",
		Description: "Get a cache registry by UUID, including its structured access policy",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Cache Registry",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetCacheRegistryArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetCacheRegistry")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("cluster_id", args.ClusterID),
			attribute.String("registry_uuid", args.RegistryUUID),
		)

		deps := DepsFromContext(ctx)
		registry, _, err := deps.CacheRegistriesClient.Get(ctx, args.OrgSlug, args.ClusterID, args.RegistryUUID)
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &registry)
	}, []string{"read_clusters"}
}

type CreateCacheRegistryArgs struct {
	ToolInput
	OrgSlug     string                         `json:"org_slug"`
	ClusterID   string                         `json:"cluster_id"`
	Name        string                         `json:"name"`
	Description *string                        `json:"description,omitempty" jsonschema:"Description to set; use null to clear it; omission uses the API default"`
	Emoji       *string                        `json:"emoji,omitempty" jsonschema:"Emoji to set; use null to clear it; omission uses the API default"`
	Color       *string                        `json:"color,omitempty" jsonschema:"Hex color to set; use null to clear it; omission uses the API default"`
	Policy      *buildkite.CacheRegistryPolicy `json:"policy,omitempty" jsonschema:"Structured cache registry policy JSON object; use null to clear it; omission uses the API default"`
}

func CreateCacheRegistry() (mcp.Tool, mcp.ToolHandlerFor[CreateCacheRegistryArgs, any], []string) {
	return mcp.Tool{
		Name:        "create_cache_registry",
		Description: "Create a cache registry in a cluster. Optional metadata and policy may be omitted, set, or explicitly cleared",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create Cache Registry",
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args CreateCacheRegistryArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.CreateCacheRegistry")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("cluster_id", args.ClusterID),
		)

		rawFields, err := cacheRegistryRawFields(request)
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		input := buildkite.CacheRegistryCreate{Name: args.Name}
		setCacheRegistryCreateFields(&input, args, rawFields)

		deps := DepsFromContext(ctx)
		registry, _, err := deps.CacheRegistriesClient.Create(ctx, args.OrgSlug, args.ClusterID, input)
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &registry)
	}, []string{"write_clusters"}
}

type UpdateCacheRegistryArgs struct {
	ToolInput
	OrgSlug      string                         `json:"org_slug"`
	ClusterID    string                         `json:"cluster_id"`
	RegistryUUID string                         `json:"registry_uuid" jsonschema:"Cache registry UUID; the registry slug is not accepted"`
	Name         string                         `json:"name,omitempty" jsonschema:"Name to set; omission leaves it unchanged and an empty string is preserved"`
	Description  *string                        `json:"description,omitempty" jsonschema:"Description to set; use null to clear it; omission leaves it unchanged"`
	Emoji        *string                        `json:"emoji,omitempty" jsonschema:"Emoji to set; use null to clear it; omission leaves it unchanged"`
	Color        *string                        `json:"color,omitempty" jsonschema:"Hex color to set; use null to clear it; omission leaves it unchanged"`
	Policy       *buildkite.CacheRegistryPolicy `json:"policy,omitempty" jsonschema:"Structured cache registry policy JSON object; use null to clear it; omission leaves it unchanged"`
}

func UpdateCacheRegistry() (mcp.Tool, mcp.ToolHandlerFor[UpdateCacheRegistryArgs, any], []string) {
	return mcp.Tool{
		Name:        "update_cache_registry",
		Description: "Update a cache registry by UUID. Omitted fields remain unchanged; nullable metadata and policy may be set or explicitly cleared",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Update Cache Registry",
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdateCacheRegistryArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.UpdateCacheRegistry")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("cluster_id", args.ClusterID),
			attribute.String("registry_uuid", args.RegistryUUID),
		)

		rawFields, err := cacheRegistryRawFields(request)
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		input := buildkite.CacheRegistryUpdate{}
		if _, ok := rawFields["name"]; ok {
			input.Name = buildkite.Some(args.Name)
		}
		setCacheRegistryUpdateFields(&input, args, rawFields)

		deps := DepsFromContext(ctx)
		registry, _, err := deps.CacheRegistriesClient.Update(ctx, args.OrgSlug, args.ClusterID, args.RegistryUUID, input)
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &registry)
	}, []string{"write_clusters"}
}

type SetDefaultCacheRegistryArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	ClusterID    string `json:"cluster_id"`
	RegistryUUID string `json:"registry_uuid" jsonschema:"UUID of a cache registry in this cluster; the registry slug is not accepted"`
}

func SetDefaultCacheRegistry() (mcp.Tool, mcp.ToolHandlerFor[SetDefaultCacheRegistryArgs, any], []string) {
	return mcp.Tool{
		Name:        "set_default_cache_registry",
		Description: "Set a cluster's default cache registry by UUID. A cluster always has a default, so this switches it to another registry in the same cluster rather than clearing it. Returns the updated cluster",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Set Default Cache Registry",
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args SetDefaultCacheRegistryArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.SetDefaultCacheRegistry")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("cluster_id", args.ClusterID),
			attribute.String("registry_uuid", args.RegistryUUID),
		)

		deps := DepsFromContext(ctx)
		cluster, _, err := deps.ClustersClient.Update(ctx, args.OrgSlug, args.ClusterID, buildkite.ClusterUpdate{
			DefaultCacheRegistryUUID: buildkite.Some(args.RegistryUUID),
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &cluster)
	}, []string{"write_clusters"}
}

type DeleteCacheRegistryArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	ClusterID    string `json:"cluster_id"`
	RegistryUUID string `json:"registry_uuid" jsonschema:"Cache registry UUID; the registry slug is not accepted"`
}

type deleteCacheRegistryResult struct {
	Deleted      bool   `json:"deleted"`
	RegistryUUID string `json:"registry_uuid"`
}

func DeleteCacheRegistry() (mcp.Tool, mcp.ToolHandlerFor[DeleteCacheRegistryArgs, any], []string) {
	return mcp.Tool{
		Name:        "delete_cache_registry",
		Description: "Delete a cache registry by UUID, including its cache metadata. The cluster's default cache registry can't be deleted; set another registry as the default first",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Cache Registry",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args DeleteCacheRegistryArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.DeleteCacheRegistry")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("cluster_id", args.ClusterID),
			attribute.String("registry_uuid", args.RegistryUUID),
		)

		deps := DepsFromContext(ctx)
		// The API responds 204 No Content on success, so there's no body to decode.
		if _, err := deps.CacheRegistriesClient.Delete(ctx, args.OrgSlug, args.ClusterID, args.RegistryUUID); err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &deleteCacheRegistryResult{Deleted: true, RegistryUUID: args.RegistryUUID})
	}, []string{"write_clusters"}
}

func setNullableCacheRegistryMetadata(field *buildkite.Optional[*string], value *string, clear bool) {
	if value != nil {
		*field = buildkite.Some(value)
	} else if clear {
		*field = buildkite.Some[*string](nil)
	}
}

func setCacheRegistryCreateFields(input *buildkite.CacheRegistryCreate, args CreateCacheRegistryArgs, rawFields map[string]json.RawMessage) {
	setNullableCacheRegistryMetadata(&input.Description, args.Description, isJSONNull(rawFields["description"]))
	setNullableCacheRegistryMetadata(&input.Emoji, args.Emoji, isJSONNull(rawFields["emoji"]))
	setNullableCacheRegistryMetadata(&input.Color, args.Color, isJSONNull(rawFields["color"]))
	if args.Policy != nil {
		input.Policy = buildkite.Some(*args.Policy)
	} else if isJSONNull(rawFields["policy"]) {
		input.Policy = buildkite.Some[buildkite.CacheRegistryPolicy](nil)
	}
}

func setCacheRegistryUpdateFields(input *buildkite.CacheRegistryUpdate, args UpdateCacheRegistryArgs, rawFields map[string]json.RawMessage) {
	setNullableCacheRegistryMetadata(&input.Description, args.Description, isJSONNull(rawFields["description"]))
	setNullableCacheRegistryMetadata(&input.Emoji, args.Emoji, isJSONNull(rawFields["emoji"]))
	setNullableCacheRegistryMetadata(&input.Color, args.Color, isJSONNull(rawFields["color"]))
	if args.Policy != nil {
		input.Policy = buildkite.Some(*args.Policy)
	} else if isJSONNull(rawFields["policy"]) {
		input.Policy = buildkite.Some[buildkite.CacheRegistryPolicy](nil)
	}
}

func cacheRegistryRawFields(request *mcp.CallToolRequest) (map[string]json.RawMessage, error) {
	var arguments map[string]json.RawMessage
	if request == nil || request.Params == nil {
		return arguments, nil
	}
	if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
		return nil, err
	}
	return arguments, nil
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}
