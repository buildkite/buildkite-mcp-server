package buildkite

import (
	"context"
	"errors"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ReleasesClient interface {
	ListTargets(context.Context, string, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseTarget], *buildkite.Response, error)
	GetTarget(context.Context, string, buildkite.ReleaseTargetRef) (buildkite.ReleaseTarget, *buildkite.Response, error)
	ListComponents(context.Context, string, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseComponent], *buildkite.Response, error)
	GetComponent(context.Context, string, buildkite.ReleaseComponentRef) (buildkite.ReleaseComponent, *buildkite.Response, error)
	ListComponentInstances(context.Context, string, buildkite.ReleaseTargetRef, *buildkite.ReleasesComponentInstanceListOptions) (buildkite.ReleasesPage[buildkite.ReleaseComponentInstance], *buildkite.Response, error)
	ListOrganizationComponentInstances(context.Context, string, *buildkite.ReleasesComponentInstanceListOptions) (buildkite.ReleasesPage[buildkite.ReleaseComponentInstance], *buildkite.Response, error)
	GetComponentInstance(context.Context, string, buildkite.ReleaseComponentInstanceRef) (buildkite.ReleaseComponentInstance, *buildkite.Response, error)
	ListComponentInstanceStatuses(context.Context, string, buildkite.ReleaseTargetRef, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseComponentInstanceStatus], *buildkite.Response, error)
	GetComponentInstanceStatus(context.Context, string, buildkite.ReleaseTargetRef, string) (buildkite.ReleaseComponentInstanceStatus, *buildkite.Response, error)
	ListObservations(context.Context, string, buildkite.ReleaseTargetRef, *buildkite.ReleasesObservationListOptions) (buildkite.ReleasesPage[buildkite.ReleaseObservation], *buildkite.Response, error)
	ListArtifacts(context.Context, string, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseArtifact], *buildkite.Response, error)
	GetArtifact(context.Context, string, buildkite.ReleaseArtifactRef) (buildkite.ReleaseArtifact, *buildkite.Response, error)
	ListDigests(context.Context, string, buildkite.ReleaseArtifactRef, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseArtifactDigest], *buildkite.Response, error)
	GetDigest(context.Context, string, buildkite.ReleaseDigestRef) (buildkite.ReleaseArtifactDigest, *buildkite.Response, error)
}

type ReleasesListArgs struct {
	ToolInput
	OrgSlug string `json:"org_slug"`
	After   string `json:"after,omitempty" jsonschema:"Opaque after cursor from links.next; mutually exclusive with before"`
	Before  string `json:"before,omitempty" jsonschema:"Opaque before cursor from links.prev; mutually exclusive with after"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Results per page: 1–100; omission uses the API default of 30"`
}

func (a ReleasesListArgs) options() (*buildkite.ReleasesListOptions, error) {
	if a.After != "" && a.Before != "" {
		return nil, errors.New("after and before are mutually exclusive")
	}
	return &buildkite.ReleasesListOptions{After: a.After, Before: a.Before, PerPage: a.PerPage}, nil
}

func ListReleaseTargets() (mcp.Tool, mcp.ToolHandlerFor[ReleasesListArgs, any], []string) {
	return mcp.Tool{
		Name: "list_release_targets", Description: "List organization-owned Releases targets, including archived targets. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Release Targets", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ReleasesListArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListReleaseTargets")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListTargets(ctx, args.OrgSlug, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetReleaseTargetArgs struct {
	ToolInput
	OrgSlug string                     `json:"org_slug"`
	Target  buildkite.ReleaseTargetRef `json:"target" jsonschema:"Exactly one of name or id (target UUID); names are organization-wide and matched literally"`
}

func GetReleaseTarget() (mcp.Tool, mcp.ToolHandlerFor[GetReleaseTargetArgs, any], []string) {
	return mcp.Tool{
		Name: "get_release_target", Description: "Get an organization-owned Releases target by name or UUID, including labels and archive state",
		Annotations: &mcp.ToolAnnotations{Title: "Get Release Target", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetReleaseTargetArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetReleaseTarget")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetTarget(ctx, args.OrgSlug, args.Target)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

func ListReleaseComponents() (mcp.Tool, mcp.ToolHandlerFor[ReleasesListArgs, any], []string) {
	return mcp.Tool{
		Name: "list_release_components", Description: "List organization-owned catalog Components with their required deployment-artifact bindings, including archived Components. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Release Components", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ReleasesListArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListReleaseComponents")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListComponents(ctx, args.OrgSlug, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetReleaseComponentArgs struct {
	ToolInput
	OrgSlug   string                        `json:"org_slug"`
	Component buildkite.ReleaseComponentRef `json:"component" jsonschema:"Exactly one of name or id (catalog Component UUID)"`
}

func GetReleaseComponent() (mcp.Tool, mcp.ToolHandlerFor[GetReleaseComponentArgs, any], []string) {
	return mcp.Tool{
		Name: "get_release_component", Description: "Get an organization-owned catalog Component and its required deployment-artifact binding",
		Annotations: &mcp.ToolAnnotations{Title: "Get Release Component", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetReleaseComponentArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetReleaseComponent")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetComponent(ctx, args.OrgSlug, args.Component)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ListReleaseComponentInstancesArgs struct {
	ReleasesListArgs
	Target *buildkite.ReleaseTargetRef         `json:"target,omitempty" jsonschema:"Omit for organization-wide discovery; otherwise exactly one of name or id (Target UUID)"`
	Label  buildkite.ReleaseTargetLabelFilters `json:"label,omitempty" jsonschema:"Match all supplied label pairs on the Instance's Target"`
}

func ListReleaseComponentInstances() (mcp.Tool, mcp.ToolHandlerFor[ListReleaseComponentInstancesArgs, any], []string) {
	return mcp.Tool{
		Name: "list_release_component_instances", Description: "List Component Instances across the organization or on one Target, including archived registrations. Filter by Target labels. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Release Component Instances", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListReleaseComponentInstancesArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListReleaseComponentInstances")
		defer span.End()
		pagination, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		opt := &buildkite.ReleasesComponentInstanceListOptions{ReleasesListOptions: *pagination, Label: args.Label}
		api := DepsFromContext(ctx).ReleasesClient
		var result buildkite.ReleasesPage[buildkite.ReleaseComponentInstance]
		if args.Target == nil {
			result, _, err = api.ListOrganizationComponentInstances(ctx, args.OrgSlug, opt)
		} else {
			result, _, err = api.ListComponentInstances(ctx, args.OrgSlug, *args.Target, opt)
		}
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetReleaseComponentInstanceArgs struct {
	ToolInput
	OrgSlug  string                                `json:"org_slug"`
	Instance buildkite.ReleaseComponentInstanceRef `json:"instance" jsonschema:"Either id (Instance UUID), or name plus target with exactly one of name or id; do not combine forms"`
}

func GetReleaseComponentInstance() (mcp.Tool, mcp.ToolHandlerFor[GetReleaseComponentInstanceArgs, any], []string) {
	return mcp.Tool{
		Name: "get_release_component_instance", Description: "Get a Component Instance registration, including its Target and catalog Component IDs and archive state",
		Annotations: &mcp.ToolAnnotations{Title: "Get Release Component Instance", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetReleaseComponentInstanceArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetReleaseComponentInstance")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetComponentInstance(ctx, args.OrgSlug, args.Instance)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ReleasesTargetListArgs struct {
	ReleasesListArgs
	Target buildkite.ReleaseTargetRef `json:"target" jsonschema:"Exactly one of name or id (Target UUID)"`
}

func ListReleaseComponentInstanceStatuses() (mcp.Tool, mcp.ToolHandlerFor[ReleasesTargetListArgs, any], []string) {
	return mcp.Tool{
		Name: "list_release_component_instance_statuses", Description: "List observed Instance state and digest catalog resolution on a Target. Null state means never observed, not absent. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Release Component Instance Statuses", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ReleasesTargetListArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListReleaseComponentInstanceStatuses")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListComponentInstanceStatuses(ctx, args.OrgSlug, args.Target, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetReleaseComponentInstanceStatusArgs struct {
	ToolInput
	OrgSlug  string                     `json:"org_slug"`
	Target   buildkite.ReleaseTargetRef `json:"target" jsonschema:"Exactly one of name or id (Target UUID)"`
	Instance string                     `json:"instance" jsonschema:"Instance name within the Target"`
}

func GetReleaseComponentInstanceStatus() (mcp.Tool, mcp.ToolHandlerFor[GetReleaseComponentInstanceStatusArgs, any], []string) {
	return mcp.Tool{
		Name: "get_release_component_instance_status", Description: "Get an Instance's observed presence, active and last-active digests, and read-time catalog resolution. Null state means never observed",
		Annotations: &mcp.ToolAnnotations{Title: "Get Release Component Instance Status", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetReleaseComponentInstanceStatusArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetReleaseComponentInstanceStatus")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetComponentInstanceStatus(ctx, args.OrgSlug, args.Target, args.Instance)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ListReleaseObservationsArgs struct {
	ReleasesTargetListArgs
	Instance string `json:"instance,omitempty" jsonschema:"Exact Instance name"`
	Type     string `json:"type,omitempty" jsonschema:"Comma-separated event types: ComponentInstanceAppeared, ComponentInstanceDisappeared, ComponentInstanceDigestObserved"`
}

func ListReleaseObservations() (mcp.Tool, mcp.ToolHandlerFor[ListReleaseObservationsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_release_observations", Description: "Browse a Releases target's observation history newest sequence first. Returns items and cursor pagination links; not a polling feed or evidence of desired state",
		Annotations: &mcp.ToolAnnotations{Title: "List Release Observations", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListReleaseObservationsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListReleaseObservations")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListObservations(ctx, args.OrgSlug, args.Target, &buildkite.ReleasesObservationListOptions{
			ReleasesListOptions: *opt, Instance: args.Instance, Type: args.Type,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

func ListDeploymentArtifacts() (mcp.Tool, mcp.ToolHandlerFor[ReleasesListArgs, any], []string) {
	return mcp.Tool{
		Name: "list_deployment_artifacts", Description: "List the Releases deployment-artifact catalog, including archived entries. Not build-job artifact files. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Deployment Artifacts", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ReleasesListArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListDeploymentArtifacts")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListArtifacts(ctx, args.OrgSlug, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetDeploymentArtifactArgs struct {
	ToolInput
	OrgSlug  string                       `json:"org_slug"`
	Artifact buildkite.ReleaseArtifactRef `json:"artifact" jsonschema:"Exactly one of name or id (deployment-artifact UUID), not a build-job artifact ID"`
}

func GetDeploymentArtifact() (mcp.Tool, mcp.ToolHandlerFor[GetDeploymentArtifactArgs, any], []string) {
	return mcp.Tool{
		Name: "get_deployment_artifact", Description: "Get a Releases deployment-artifact catalog entry and archive state. Not a build-job artifact file",
		Annotations: &mcp.ToolAnnotations{Title: "Get Deployment Artifact", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetDeploymentArtifactArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetDeploymentArtifact")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetArtifact(ctx, args.OrgSlug, args.Artifact)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ListDeploymentArtifactDigestsArgs struct {
	ReleasesListArgs
	Artifact buildkite.ReleaseArtifactRef `json:"artifact" jsonschema:"Exactly one of name or id (deployment-artifact UUID)"`
}

func ListDeploymentArtifactDigests() (mcp.Tool, mcp.ToolHandlerFor[ListDeploymentArtifactDigestsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_deployment_artifact_digests", Description: "List published SHA-256 digests newest publication first, including provenance and yank state. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Deployment Artifact Digests", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListDeploymentArtifactDigestsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListDeploymentArtifactDigests")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListDigests(ctx, args.OrgSlug, args.Artifact, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetDeploymentArtifactDigestArgs struct {
	ToolInput
	OrgSlug string                     `json:"org_slug"`
	Digest  buildkite.ReleaseDigestRef `json:"digest" jsonschema:"Either id (catalog Digest UUID), or exact sha256 digest plus artifact with exactly one of name or id; do not combine forms"`
}

func GetDeploymentArtifactDigest() (mcp.Tool, mcp.ToolHandlerFor[GetDeploymentArtifactDigestArgs, any], []string) {
	return mcp.Tool{
		Name: "get_deployment_artifact_digest", Description: "Get an immutable published digest by UUID or Artifact and exact SHA-256 digest, including provenance and yank state",
		Annotations: &mcp.ToolAnnotations{Title: "Get Deployment Artifact Digest", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetDeploymentArtifactDigestArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetDeploymentArtifactDigest")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetDigest(ctx, args.OrgSlug, args.Digest)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}
