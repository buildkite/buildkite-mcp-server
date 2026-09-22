package buildkite

import (
	"context"
	"sort"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// TeamsClient is the subset of go-buildkite's TeamsService used by the team tools.
type TeamsClient interface {
	List(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error)
}

// TeamPipelinesClient is the subset of go-buildkite's TeamPipelinesService used by the team tools.
type TeamPipelinesClient interface {
	List(ctx context.Context, org, teamID string, opt *buildkite.TeamPipelinesListOptions) ([]buildkite.TeamPipeline, *buildkite.Response, error)
	Get(ctx context.Context, org, teamID, pipelineID string) (buildkite.TeamPipeline, *buildkite.Response, error)
}

type ListTeamsArgs struct {
	ToolInput
	OrgSlug string `json:"org_slug"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

type ListTeamPipelinesArgs struct {
	ToolInput
	OrgSlug string `json:"org_slug"`
	TeamID  string `json:"team_id" jsonschema:"Team UUID, the id field returned by list_teams. The REST endpoint does not accept team slugs"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

type ListPipelineTeamsArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
}

// PipelineTeam describes one team's access to a pipeline.
type PipelineTeam struct {
	TeamID        string               `json:"team_id"`
	TeamGraphQLID string               `json:"team_graphql_id,omitempty"`
	TeamSlug      string               `json:"team_slug"`
	TeamName      string               `json:"team_name"`
	AccessLevel   string               `json:"access_level"`
	CreatedAt     *buildkite.Timestamp `json:"created_at,omitempty"`
}

// PipelineTeamsResult is the response body of list_pipeline_teams.
type PipelineTeamsResult struct {
	PipelineID        string         `json:"pipeline_id"`
	PipelineGraphQLID string         `json:"pipeline_graphql_id,omitempty"`
	PipelineSlug      string         `json:"pipeline_slug"`
	TeamsChecked      int            `json:"teams_checked"`
	Teams             []PipelineTeam `json:"teams"`
}

func ListTeams() (mcp.Tool, mcp.ToolHandlerFor[ListTeamsArgs, any], []string) {
	return mcp.Tool{
		Name:        "list_teams",
		Description: "List the teams in an organization with each team's UUID, GraphQL ID, slug, name, privacy and default settings. Use the id field as team_id when calling list_team_pipelines",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Teams",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListTeamsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListTeams")
		defer span.End()

		paginationParams := paginationFromArgs(args.Page, args.PerPage)

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.Int("page", paginationParams.Page),
			attribute.Int("per_page", paginationParams.PerPage),
		)

		deps := DepsFromContext(ctx)
		teams, resp, err := deps.TeamsClient.List(ctx, args.OrgSlug, &buildkite.TeamsListOptions{
			ListOptions: paginationParams,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		result := PaginatedResult[buildkite.Team]{
			Items: teams,
			Headers: map[string]string{
				"Link": linkHeader(resp),
			},
		}

		span.SetAttributes(attribute.Int("item_count", len(teams)))

		return mcpTextResult(span, &result)
	}, []string{"read_teams"}
}

func ListTeamPipelines() (mcp.Tool, mcp.ToolHandlerFor[ListTeamPipelinesArgs, any], []string) {
	return mcp.Tool{
		Name:        "list_team_pipelines",
		Description: "List the pipelines a team has access to, with each pipeline's UUID, API URL, and the team's access level (read_only, build_and_read or manage_build_and_read). Requires the team UUID from list_teams. To go the other way, from a pipeline to its teams, use list_pipeline_teams",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Team Pipelines",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListTeamPipelinesArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListTeamPipelines")
		defer span.End()

		paginationParams := paginationFromArgs(args.Page, args.PerPage)

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("team_id", args.TeamID),
			attribute.Int("page", paginationParams.Page),
			attribute.Int("per_page", paginationParams.PerPage),
		)

		deps := DepsFromContext(ctx)
		pipelines, resp, err := deps.TeamPipelinesClient.List(ctx, args.OrgSlug, args.TeamID, &buildkite.TeamPipelinesListOptions{
			ListOptions: paginationParams,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		result := PaginatedResult[buildkite.TeamPipeline]{
			Items: pipelines,
			Headers: map[string]string{
				"Link": linkHeader(resp),
			},
		}

		span.SetAttributes(attribute.Int("item_count", len(pipelines)))

		return mcpTextResult(span, &result)
	}, []string{"read_teams"}
}

func ListPipelineTeams() (mcp.Tool, mcp.ToolHandlerFor[ListPipelineTeamsArgs, any], []string) {
	return mcp.Tool{
		Name: "list_pipeline_teams",
		Description: "List the teams that have access to a pipeline, with each team's UUID, GraphQL ID, slug, name and access level (read_only, build_and_read or manage_build_and_read). " +
			"The REST API has no pipeline-to-teams endpoint, so this tool resolves the pipeline and then checks every team in the organization, making one request per team; expect it to be slower in organizations with many teams. " +
			"Limitation: the REST API does not expose the TeamPipeline association's own GraphQL node ID (the value Terraform's buildkite_pipeline_team import expects), so this tool cannot return it; only the team and pipeline GraphQL IDs are included",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Pipeline Teams",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListPipelineTeamsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListPipelineTeams")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
		)

		deps := DepsFromContext(ctx)

		pipeline, _, err := deps.PipelinesClient.Get(ctx, args.OrgSlug, args.PipelineSlug)
		if err != nil {
			return handleBuildkiteError(err)
		}

		result := PipelineTeamsResult{
			PipelineID:        pipeline.ID,
			PipelineGraphQLID: pipeline.GraphQLID,
			PipelineSlug:      pipeline.Slug,
			Teams:             []PipelineTeam{},
		}

		page := 1
		for {
			teams, resp, err := deps.TeamsClient.List(ctx, args.OrgSlug, &buildkite.TeamsListOptions{
				ListOptions: buildkite.ListOptions{Page: page, PerPage: 100},
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			for _, team := range teams {
				result.TeamsChecked++

				association, _, err := deps.TeamPipelinesClient.Get(ctx, args.OrgSlug, team.ID, pipeline.ID)
				if isBuildkiteNotFound(err) {
					// A 404 means this team has no access to the pipeline.
					continue
				}
				if err != nil {
					return handleBuildkiteError(err)
				}

				result.Teams = append(result.Teams, PipelineTeam{
					TeamID:        team.ID,
					TeamGraphQLID: team.GraphQLID,
					TeamSlug:      team.Slug,
					TeamName:      team.Name,
					AccessLevel:   association.AccessLevel,
					CreatedAt:     association.CreatedAt,
				})
			}

			// Stop when the Link header reports no further page. The guard against a
			// non-advancing NextPage prevents looping forever on odd responses.
			if resp == nil || resp.NextPage <= page {
				break
			}
			page = resp.NextPage
		}

		sort.Slice(result.Teams, func(i, j int) bool {
			return result.Teams[i].TeamSlug < result.Teams[j].TeamSlug
		})

		span.SetAttributes(
			attribute.Int("teams_checked", result.TeamsChecked),
			attribute.Int("item_count", len(result.Teams)),
		)

		return mcpTextResult(span, &result)
	}, []string{"read_pipelines", "read_teams"}
}

// linkHeader returns the Link header of a response, tolerating a nil response.
func linkHeader(resp *buildkite.Response) string {
	if resp == nil || resp.Response == nil {
		return ""
	}
	return resp.Header.Get("Link")
}
