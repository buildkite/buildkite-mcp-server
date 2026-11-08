package buildkite

import (
	"context"
	"errors"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ReleasesClient interface {
	ListInstallations(context.Context, string, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseInstallation], *buildkite.Response, error)
	GetInstallation(context.Context, string, buildkite.ReleaseInstallationRef) (buildkite.ReleaseInstallation, *buildkite.Response, error)
	ListTargets(context.Context, string, buildkite.ReleaseInstallationRef, *buildkite.ReleasesListOptions) (buildkite.ReleasesPage[buildkite.ReleaseTarget], *buildkite.Response, error)
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

func ListInstallations() (mcp.Tool, mcp.ToolHandlerFor[ReleasesListArgs, any], []string) {
	return mcp.Tool{
		Name: "list_installations", Description: "List Releases installations, including archived registrations. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Installations", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ReleasesListArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListInstallations")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListInstallations(ctx, args.OrgSlug, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetInstallationArgs struct {
	ToolInput
	OrgSlug      string                           `json:"org_slug"`
	Installation buildkite.ReleaseInstallationRef `json:"installation" jsonschema:"Exactly one of name or id (installation UUID); names are matched literally"`
}

func GetInstallation() (mcp.Tool, mcp.ToolHandlerFor[GetInstallationArgs, any], []string) {
	return mcp.Tool{
		Name: "get_installation", Description: "Get a Releases installation by name or UUID, including archive state",
		Annotations: &mcp.ToolAnnotations{Title: "Get Installation", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetInstallationArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetInstallation")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetInstallation(ctx, args.OrgSlug, args.Installation)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ListInstallationTargetsArgs struct {
	ReleasesListArgs
	Installation buildkite.ReleaseInstallationRef `json:"installation" jsonschema:"Exactly one of name or id (installation UUID)"`
}

func ListInstallationTargets() (mcp.Tool, mcp.ToolHandlerFor[ListInstallationTargetsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_installation_targets", Description: "List targets in a Releases installation, including archived targets. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Installation Targets", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListInstallationTargetsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListInstallationTargets")
		defer span.End()
		opt, err := args.options()
		if err != nil {
			return handleBuildkiteError(err)
		}
		result, _, err := DepsFromContext(ctx).ReleasesClient.ListTargets(ctx, args.OrgSlug, args.Installation, opt)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type GetInstallationTargetArgs struct {
	ToolInput
	OrgSlug string                     `json:"org_slug"`
	Target  buildkite.ReleaseTargetRef `json:"target" jsonschema:"Either id (target UUID), or name plus installation with exactly one of name or id; do not combine forms"`
}

func GetInstallationTarget() (mcp.Tool, mcp.ToolHandlerFor[GetInstallationTargetArgs, any], []string) {
	return mcp.Tool{
		Name: "get_installation_target", Description: "Get a Releases target by UUID or installation and target name, including labels and archive state",
		Annotations: &mcp.ToolAnnotations{Title: "Get Installation Target", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetInstallationTargetArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetInstallationTarget")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetTarget(ctx, args.OrgSlug, args.Target)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ListInstallationComponentsArgs struct {
	ReleasesListArgs
	Target buildkite.ReleaseTargetRef `json:"target" jsonschema:"Either id (target UUID), or name plus installation with exactly one of name or id; do not combine forms"`
}

func ListInstallationComponents() (mcp.Tool, mcp.ToolHandlerFor[ListInstallationComponentsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_installation_components", Description: "List component registrations on a Releases target, including archived components and optional deployment-artifact bindings. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Installation Components", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListInstallationComponentsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListInstallationComponents")
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
	}, []string{"read_deployment_artifacts"}
}

type GetInstallationComponentArgs struct {
	ToolInput
	OrgSlug   string                        `json:"org_slug"`
	Component buildkite.ReleaseComponentRef `json:"component" jsonschema:"Either id (component UUID), or name plus target selector; do not combine forms. A target selector is id or name plus installation (name or id)"`
}

func GetInstallationComponent() (mcp.Tool, mcp.ToolHandlerFor[GetInstallationComponentArgs, any], []string) {
	return mcp.Tool{
		Name: "get_installation_component", Description: "Get a Releases component registration and optional artifact binding, not its observed runtime status",
		Annotations: &mcp.ToolAnnotations{Title: "Get Installation Component", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetInstallationComponentArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetInstallationComponent")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetComponent(ctx, args.OrgSlug, args.Component)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

func ListInstallationComponentStatuses() (mcp.Tool, mcp.ToolHandlerFor[ListInstallationComponentsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_installation_component_statuses", Description: "List observed component state and catalog resolution on a Releases target. Null state means never observed, not absent. Returns items and cursor pagination links",
		Annotations: &mcp.ToolAnnotations{Title: "List Installation Component Statuses", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListInstallationComponentsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListInstallationComponentStatuses")
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
	}, []string{"read_deployment_artifacts"}
}

type GetInstallationComponentStatusArgs struct {
	ToolInput
	OrgSlug   string                     `json:"org_slug"`
	Target    buildkite.ReleaseTargetRef `json:"target" jsonschema:"Either id (target UUID), or name plus installation with exactly one of name or id; do not combine forms"`
	Component string                     `json:"component" jsonschema:"Component name within the target, not its UUID"`
}

func GetInstallationComponentStatus() (mcp.Tool, mcp.ToolHandlerFor[GetInstallationComponentStatusArgs, any], []string) {
	return mcp.Tool{
		Name: "get_installation_component_status", Description: "Get a component's observed runtime state and catalog resolution. Null state means never observed; this does not report desired state or deployment safety",
		Annotations: &mcp.ToolAnnotations{Title: "Get Installation Component Status", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetInstallationComponentStatusArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetInstallationComponentStatus")
		defer span.End()
		result, _, err := DepsFromContext(ctx).ReleasesClient.GetComponentStatus(ctx, args.OrgSlug, args.Target, args.Component)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &result)
	}, []string{"read_deployment_artifacts"}
}

type ListInstallationObservationsArgs struct {
	ListInstallationComponentsArgs
	Component string `json:"component,omitempty" jsonschema:"Exact component name, not UUID"`
	Type      string `json:"type,omitempty" jsonschema:"Comma-separated event types: TargetAppeared, ComponentAppeared, ComponentDisappeared, ComponentVersionObserved"`
}

func ListInstallationObservations() (mcp.Tool, mcp.ToolHandlerFor[ListInstallationObservationsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_installation_observations", Description: "Browse a Releases target's observation history newest sequence first. Returns items and cursor pagination links; not a polling feed or evidence of desired state",
		Annotations: &mcp.ToolAnnotations{Title: "List Installation Observations", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListInstallationObservationsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListInstallationObservations")
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
