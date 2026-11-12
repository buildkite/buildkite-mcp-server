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
	ListComponents(context.Context, string, buildkite.ReleaseTargetRef, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseComponent], *buildkite.Response, error)
	GetComponent(context.Context, string, buildkite.ReleaseComponentRef) (buildkite.ReleaseComponent, *buildkite.Response, error)
	ListComponentStatuses(context.Context, string, buildkite.ReleaseTargetRef, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseComponentStatus], *buildkite.Response, error)
	GetComponentStatus(context.Context, string, buildkite.ReleaseTargetRef, string) (buildkite.ReleaseComponentStatus, *buildkite.Response, error)
	ListObservations(context.Context, string, buildkite.ReleaseTargetRef, *buildkite.ReleasesObservationListOptions) (buildkite.ReleasesPage[buildkite.ReleaseObservation], *buildkite.Response, error)
	ListArtifacts(context.Context, string, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseArtifact], *buildkite.Response, error)
	GetArtifact(context.Context, string, buildkite.ReleaseArtifactRef) (buildkite.ReleaseArtifact, *buildkite.Response, error)
	ListVersions(context.Context, string, buildkite.ReleaseArtifactRef, *buildkite.ReleasesVersionListOptions) (buildkite.ReleasesPage[buildkite.ReleaseArtifactVersion], *buildkite.Response, error)
	GetVersion(context.Context, string, buildkite.ReleaseVersionRef) (buildkite.ReleaseArtifactVersion, *buildkite.Response, error)
	CompareVersions(context.Context, string, buildkite.ReleaseArtifactRef, *buildkite.ReleasesComparisonOptions) (buildkite.ReleaseVersionComparison, *buildkite.Response, error)
	ListComparisonPullRequests(context.Context, string, buildkite.ReleaseArtifactRef, *buildkite.ReleasesComparisonListOptions) (buildkite.ReleasesPage[buildkite.ReleasePullRequest], *buildkite.Response, error)
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
	}, []string{"read_release_targets"}
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
	}, []string{"read_release_targets"}
}

type ListReleaseComponentsArgs struct {
	ReleasesListArgs
	Target buildkite.ReleaseTargetRef `json:"target" jsonschema:"Exactly one of name or id (target UUID)"`
}

func ListReleaseComponents() (mcp.Tool, mcp.ToolHandlerFor[ListReleaseComponentsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_release_components", Description: "List component registrations on a Releases target, including archived components and optional deployment-artifact bindings. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Release Components", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListReleaseComponentsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListReleaseComponents")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListComponents(ctx, args.OrgSlug, args.Target, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_release_targets"}
}

type GetReleaseComponentArgs struct {
	ToolInput
	OrgSlug   string                        `json:"org_slug"`
	Component buildkite.ReleaseComponentRef `json:"component" jsonschema:"Either id (component UUID), or name plus target selector; do not combine forms. A target selector has exactly one of name or id"`
}

func GetReleaseComponent() (mcp.Tool, mcp.ToolHandlerFor[GetReleaseComponentArgs, any], []string) {
	return mcp.Tool{
		Name: "get_release_component", Description: "Get a Releases component registration and optional artifact binding, not its observed runtime status",
		Annotations: &mcp.ToolAnnotations{Title: "Get Release Component", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetReleaseComponentArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetReleaseComponent")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetComponent(ctx, args.OrgSlug, args.Component)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_release_targets"}
}

func ListReleaseComponentStatuses() (mcp.Tool, mcp.ToolHandlerFor[ListReleaseComponentsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_release_component_statuses", Description: "List observed component state and catalog resolution on a Releases target. Null state means never observed, not absent. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Release Component Statuses", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListReleaseComponentsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListReleaseComponentStatuses")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListComponentStatuses(ctx, args.OrgSlug, args.Target, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_release_targets"}
}

type GetReleaseComponentStatusArgs struct {
	ToolInput
	OrgSlug   string                     `json:"org_slug"`
	Target    buildkite.ReleaseTargetRef `json:"target" jsonschema:"Exactly one of name or id (target UUID)"`
	Component string                     `json:"component" jsonschema:"Component name within the target, not its UUID"`
}

func GetReleaseComponentStatus() (mcp.Tool, mcp.ToolHandlerFor[GetReleaseComponentStatusArgs, any], []string) {
	return mcp.Tool{
		Name: "get_release_component_status", Description: "Get a component's observed runtime state and catalog resolution. Null state means never observed; this does not report desired state or deployment safety",
		Annotations: &mcp.ToolAnnotations{Title: "Get Release Component Status", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetReleaseComponentStatusArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetReleaseComponentStatus")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetComponentStatus(ctx, args.OrgSlug, args.Target, args.Component)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_release_targets"}
}

type ListReleaseObservationsArgs struct {
	ListReleaseComponentsArgs
	Component string `json:"component,omitempty" jsonschema:"Exact component name, not UUID"`
	Type      string `json:"type,omitempty" jsonschema:"Comma-separated event types: TargetAppeared, ComponentAppeared, ComponentDisappeared, ComponentVersionObserved"`
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
			ReleasesListOptions: *opt, Component: args.Component, Type: args.Type,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_release_targets"}
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
		Name: "get_deployment_artifact", Description: "Get a Releases deployment-artifact catalog entry and version scheme. Not a build-job artifact file",
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

type ListDeploymentArtifactVersionsArgs struct {
	ReleasesListArgs
	Artifact  buildkite.ReleaseArtifactRef `json:"artifact" jsonschema:"Exactly one of name or id (deployment-artifact UUID)"`
	Order     string                       `json:"order,omitempty" jsonschema:"registered_at (default) or precedence; SHA artifacts do not support precedence"`
	Direction string                       `json:"direction,omitempty" jsonschema:"asc or desc (default); only valid with precedence ordering"`
	NewerThan string                       `json:"newer_than,omitempty" jsonschema:"Version UUID in this artifact; strict bound excluding ties, implies precedence ordering; mutually exclusive with older_than"`
	OlderThan string                       `json:"older_than,omitempty" jsonschema:"Version UUID in this artifact; strict bound excluding ties, implies precedence ordering; mutually exclusive with newer_than"`
	Suffix    *string                      `json:"suffix,omitempty" jsonschema:"Build-number schemes only: exact suffix; empty string selects unsuffixed versions; omission includes all variants"`
	Yanked    *bool                        `json:"yanked,omitempty" jsonschema:"Filter withdrawal state; omission includes both yanked and non-yanked versions"`
}

func ListDeploymentArtifactVersions() (mcp.Tool, mcp.ToolHandlerFor[ListDeploymentArtifactVersionsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_deployment_artifact_versions", Description: "List Releases artifact versions with provenance and yank state. Default order is registration time, not version precedence. Returns items, cursor links and optional ordering metadata",
		Annotations: &mcp.ToolAnnotations{Title: "List Deployment Artifact Versions", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListDeploymentArtifactVersionsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListDeploymentArtifactVersions")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListVersions(ctx, args.OrgSlug, args.Artifact, &buildkite.ReleasesVersionListOptions{
			ReleasesListOptions: *opt, Order: args.Order, Direction: args.Direction, NewerThan: args.NewerThan, OlderThan: args.OlderThan, Suffix: args.Suffix, Yanked: args.Yanked,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetDeploymentArtifactVersionArgs struct {
	ToolInput
	OrgSlug string                      `json:"org_slug"`
	Version buildkite.ReleaseVersionRef `json:"version" jsonschema:"Either id (version UUID), or exact version string plus artifact with exactly one of name or id; do not combine forms"`
}

func GetDeploymentArtifactVersion() (mcp.Tool, mcp.ToolHandlerFor[GetDeploymentArtifactVersionArgs, any], []string) {
	return mcp.Tool{
		Name: "get_deployment_artifact_version", Description: "Get an immutable Releases artifact version by UUID or artifact and exact version label, including digest, provenance and yank state",
		Annotations: &mcp.ToolAnnotations{Title: "Get Deployment Artifact Version", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetDeploymentArtifactVersionArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetDeploymentArtifactVersion")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetVersion(ctx, args.OrgSlug, args.Version)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type CompareDeploymentArtifactVersionsArgs struct {
	GetDeploymentArtifactArgs
	Left    string `json:"left,omitempty" jsonschema:"Exact version label; provide exactly one of left or left_id"`
	LeftID  string `json:"left_id,omitempty" jsonschema:"Version UUID in this artifact; provide exactly one of left or left_id"`
	Right   string `json:"right,omitempty" jsonschema:"Exact version label; provide exactly one of right or right_id"`
	RightID string `json:"right_id,omitempty" jsonschema:"Version UUID in this artifact; provide exactly one of right or right_id"`
}

func (a CompareDeploymentArtifactVersionsArgs) options() *buildkite.ReleasesComparisonOptions {
	return &buildkite.ReleasesComparisonOptions{Left: a.Left, LeftID: a.LeftID, Right: a.Right, RightID: a.RightID}
}

func CompareDeploymentArtifactVersions() (mcp.Tool, mcp.ToolHandlerFor[CompareDeploymentArtifactVersionsArgs, any], []string) {
	return mcp.Tool{
		Name: "compare_deployment_artifact_versions", Description: "Compare left relative to right by the Releases artifact's version scheme. Unknown precedence is a valid result; equal precedence, same version and same content are separate facts. Does not establish deployment safety",
		Annotations: &mcp.ToolAnnotations{Title: "Compare Deployment Artifact Versions", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CompareDeploymentArtifactVersionsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.CompareDeploymentArtifactVersions")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.CompareVersions(ctx, args.OrgSlug, args.Artifact, args.options())
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ListDeploymentArtifactComparisonPullRequestsArgs struct {
	CompareDeploymentArtifactVersionsArgs
	After   string `json:"after,omitempty" jsonschema:"Opaque after cursor from links.next; mutually exclusive with before"`
	Before  string `json:"before,omitempty" jsonschema:"Opaque before cursor from links.prev; mutually exclusive with after"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Results per page: 1–100; omission uses the API default of 30"`
}

func ListDeploymentArtifactComparisonPullRequests() (mcp.Tool, mcp.ToolHandlerFor[ListDeploymentArtifactComparisonPullRequestsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_deployment_artifact_comparison_pull_requests", Description: "List publisher-supplied pull requests in the range right < version <= left, newest version first, including yanked versions. Non-forward or unknown comparisons return an empty page. Returns items and cursor links; not a complete change inventory",
		Annotations: &mcp.ToolAnnotations{Title: "List Deployment Artifact Comparison Pull Requests", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListDeploymentArtifactComparisonPullRequestsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListDeploymentArtifactComparisonPullRequests")
		defer span.End()
		opt, err := (ReleasesListArgs{After: args.After, Before: args.Before, PerPage: args.PerPage}).options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListComparisonPullRequests(ctx, args.OrgSlug, args.Artifact, &buildkite.ReleasesComparisonListOptions{
			ReleasesListOptions: *opt, ReleasesComparisonOptions: *args.options(),
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}
