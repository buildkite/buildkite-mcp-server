package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type PackageRegistriesClient interface {
	UpdatePackageRegistry(ctx context.Context, orgSlug, registrySlug string, patch map[string]json.RawMessage) (json.RawMessage, error)
}

func (a *BuildkiteClientAdapter) UpdatePackageRegistry(ctx context.Context, orgSlug, registrySlug string, patch map[string]json.RawMessage) (json.RawMessage, error) {
	// The SDK's update input has no visibility field, and its response type omits public:false.
	path := fmt.Sprintf("v2/packages/organizations/%s/registries/%s", orgSlug, registrySlug)
	req, err := a.NewRequest(ctx, http.MethodPatch, path, patch)
	if err != nil {
		return nil, err
	}

	var registry json.RawMessage
	if _, err := a.Do(req, &registry); err != nil {
		return nil, err
	}
	return registry, nil
}

type UpdatePackageRegistryArgs struct {
	ToolInput
	OrgSlug      string          `json:"org_slug"`
	RegistrySlug string          `json:"registry_slug"`
	Visibility   string          `json:"visibility,omitempty" jsonschema:"Only source registries support changing visibility"`
	Name         string          `json:"name,omitempty" jsonschema:"Renaming can change the registry slug"`
	Description  *string         `json:"description,omitempty" jsonschema:"Use null to clear"`
	Emoji        *string         `json:"emoji,omitempty" jsonschema:"Buildkite emoji shortcode or a single Unicode emoji; use null to clear"`
	Color        *string         `json:"color,omitempty" jsonschema:"Hex color; use null to clear"`
	OIDCPolicy   json.RawMessage `json:"oidc_policy,omitempty" jsonschema:"YAML policy string or JSON array of policy statements; replaces the policy, or clears it with null"`
}

func UpdatePackageRegistry() (mcp.Tool, mcp.ToolHandlerFor[UpdatePackageRegistryArgs, any], []string) {
	schema, err := jsonschema.For[UpdatePackageRegistryArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate package registry input schema: %v", err))
	}
	// Require single path segments, not URLs, for both slugs.
	const slugPattern = `^[A-Za-z0-9_-]+$`
	schema.Properties["org_slug"].Pattern = slugPattern
	schema.Properties["registry_slug"].Pattern = slugPattern
	schema.Properties["visibility"].Enum = []any{"public", "private"}
	policy := schema.Properties["oidc_policy"]
	*policy = jsonschema.Schema{
		Description: policy.Description,
		Types:       []string{"string", "array", "null"},
		Items:       &jsonschema.Schema{Type: "object"},
	}

	return mcp.Tool{
		Name:        "update_package_registry",
		Description: "Update an existing Buildkite Packages registry's visibility, metadata, or OIDC policy. Supply at least one setting. Omitted settings stay unchanged, including the OIDC policy unless explicitly requested. Making a registry public can also make the organization public. Requires the write_registries scope and permission to edit the registry. Returns the updated registry with its public boolean.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:           "Update Package Registry",
			DestructiveHint: new(true),
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdatePackageRegistryArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.UpdatePackageRegistry")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("registry_slug", args.RegistrySlug),
			attribute.String("visibility", args.Visibility),
		)

		fields, err := toolRawFields(request)
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		// Forward only validated settings, preserving explicit nulls and omitted keys.
		patch := make(map[string]json.RawMessage)
		for _, key := range []string{"visibility", "name", "description", "emoji", "color", "oidc_policy"} {
			if value, ok := fields[key]; ok {
				patch[key] = value
			}
		}
		if len(patch) == 0 {
			return utils.NewToolResultError("supply at least one registry setting to update: visibility, name, description, emoji, color, or oidc_policy"), nil, nil
		}

		registry, err := DepsFromContext(ctx).PackageRegistriesClient.UpdatePackageRegistry(ctx, args.OrgSlug, args.RegistrySlug, patch)
		if err != nil {
			return handleBuildkiteError(err)
		}
		if args.Visibility != "" {
			var updated struct {
				Public *bool `json:"public"`
			}
			if err := json.Unmarshal(registry, &updated); err != nil || updated.Public == nil || *updated.Public != (args.Visibility == "public") {
				return utils.NewToolResultError(fmt.Sprintf("The API did not confirm %s registry visibility. It may not support visibility updates. Other supplied settings may already have been applied.", args.Visibility)), nil, nil
			}
		}
		return mcpTextResult(span, registry)
	}, []string{"write_registries"}
}
