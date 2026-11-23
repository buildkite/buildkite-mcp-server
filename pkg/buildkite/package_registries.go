package buildkite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type PackageRegistriesClient interface {
	Update(ctx context.Context, orgSlug, registrySlug string, input buildkite.UpdatePackageRegistryInput) (buildkite.PackageRegistry, *buildkite.Response, error)
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
		input := buildkite.UpdatePackageRegistryInput{
			Description: packageRegistryMetadata(args.Description, fields["description"]),
			Emoji:       packageRegistryMetadata(args.Emoji, fields["emoji"]),
			Color:       packageRegistryMetadata(args.Color, fields["color"]),
		}
		if args.Visibility != "" {
			input.Visibility = buildkite.Some(args.Visibility)
		}
		if _, ok := fields["name"]; ok {
			input.Name = buildkite.Some(args.Name)
		}
		policy := bytes.TrimSpace(fields["oidc_policy"])
		if input.Visibility.IsZero() && input.Name.IsZero() && input.Description.IsZero() && input.Emoji.IsZero() && input.Color.IsZero() && len(policy) == 0 {
			return utils.NewToolResultError("supply at least one registry setting to update: visibility, name, description, emoji, color, or oidc_policy"), nil, nil
		}
		if len(policy) > 0 {
			switch {
			case isJSONNull(policy):
				input.OIDCPolicy = buildkite.Null[buildkite.PackageRegistryOIDCPolicy]()
			case policy[0] == '[':
				policyYAML, err := packageRegistryJSONPolicy(policy)
				if err != nil {
					return utils.NewToolResultError(fmt.Sprintf("invalid oidc_policy: %v", err)), nil, nil
				}
				input.OIDCPolicyYAML = &policyYAML
			default:
				var policyYAML string
				if err := json.Unmarshal(policy, &policyYAML); err != nil {
					return utils.NewToolResultError(fmt.Sprintf("invalid oidc_policy: %v", err)), nil, nil
				}
				input.OIDCPolicyYAML = &policyYAML
			}
		}

		registry, _, err := DepsFromContext(ctx).PackageRegistriesClient.Update(ctx, args.OrgSlug, args.RegistrySlug, input)
		if err != nil {
			return handleBuildkiteError(err)
		}
		if args.Visibility != "" && registry.Public != (args.Visibility == "public") {
			return utils.NewToolResultError(fmt.Sprintf("The API did not confirm %s registry visibility. It may not support visibility updates. Other supplied settings may already have been applied.", args.Visibility)), nil, nil
		}
		return mcpTextResult(span, &registry)
	}, []string{"write_registries"}
}

func packageRegistryMetadata(value *string, raw json.RawMessage) buildkite.Optional[string] {
	if value != nil {
		return buildkite.Some(*value)
	}
	if isJSONNull(raw) {
		return buildkite.Null[string]()
	}
	return buildkite.Optional[string]{}
}

// REST accepts JSON as YAML. Re-encode the policy without narrowing it to the
// SDK's statement types, which don't support every REST claim predicate form.
func packageRegistryJSONPolicy(raw []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var policy any
	if err := decoder.Decode(&policy); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(yamlCompatibleJSONNumbers(policy))
	return string(encoded), err
}

func yamlCompatibleJSONNumbers(value any) any {
	switch v := value.(type) {
	case []any:
		for i, item := range v {
			v[i] = yamlCompatibleJSONNumbers(item)
		}
	case map[string]any:
		for key, item := range v {
			v[key] = yamlCompatibleJSONNumbers(item)
		}
	case json.Number:
		if i := strings.IndexAny(v.String(), "eE"); i != -1 {
			// Psych recognizes scientific notation as a float only with a
			// decimal point and an explicit exponent sign.
			mantissa, exponent := v.String()[:i], v.String()[i+1:]
			if !strings.Contains(mantissa, ".") {
				mantissa += ".0"
			}
			if exponent[0] != '+' && exponent[0] != '-' {
				exponent = "+" + exponent
			}
			return json.Number(mantissa + "e" + exponent)
		}
	}
	return value
}
