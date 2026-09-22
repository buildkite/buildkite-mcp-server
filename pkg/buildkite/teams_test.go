package buildkite

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

type mockTeamsClient struct {
	ListFunc func(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error)
}

func (m *mockTeamsClient) List(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, opt)
	}
	return nil, nil, nil
}

var _ TeamsClient = (*mockTeamsClient)(nil)

type mockTeamPipelinesClient struct {
	ListFunc func(ctx context.Context, org, teamID string, opt *buildkite.TeamPipelinesListOptions) ([]buildkite.TeamPipeline, *buildkite.Response, error)
	GetFunc  func(ctx context.Context, org, teamID, pipelineID string) (buildkite.TeamPipeline, *buildkite.Response, error)
}

func (m *mockTeamPipelinesClient) List(ctx context.Context, org, teamID string, opt *buildkite.TeamPipelinesListOptions) ([]buildkite.TeamPipeline, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, teamID, opt)
	}
	return nil, nil, nil
}

func (m *mockTeamPipelinesClient) Get(ctx context.Context, org, teamID, pipelineID string) (buildkite.TeamPipeline, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, teamID, pipelineID)
	}
	return buildkite.TeamPipeline{}, nil, nil
}

var _ TeamPipelinesClient = (*mockTeamPipelinesClient)(nil)

func okResponse() *buildkite.Response {
	return &buildkite.Response{Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}}
}

func apiError(status int, message string) error {
	return &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: status,
			Request:    &http.Request{Method: http.MethodGet},
		},
		Message: message,
	}
}

func TestListTeams(t *testing.T) {
	assert := require.New(t)

	var gotOrg string
	var gotOpts *buildkite.TeamsListOptions
	client := &mockTeamsClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error) {
			gotOrg = org
			gotOpts = opt
			resp := okResponse()
			resp.Header.Set("Link", `<https://api.buildkite.com/v2/organizations/org/teams?page=2>; rel="next"`)
			return []buildkite.Team{
				{ID: "team-uuid", GraphQLID: "VGVhbS0tLXRlYW0tdXVpZA==", Name: "Backend", Slug: "backend", Privacy: "visible"},
			}, resp, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TeamsClient: client})

	tool, handler, scopes := ListTeams()
	assert.Equal("list_teams", tool.Name)
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Equal([]string{"read_teams"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTeamsArgs{OrgSlug: "org", Page: 2, PerPage: 50})
	assert.NoError(err)
	assert.Equal("org", gotOrg)
	assert.Equal(2, gotOpts.Page)
	assert.Equal(50, gotOpts.PerPage)

	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, `<https://api.buildkite.com/v2/organizations/org/teams?page=2>; rel="next"`, "headers", "Link")
	requireJSONPathEqual(t, text, "team-uuid", "items", 0, "id")
	requireJSONPathEqual(t, text, "VGVhbS0tLXRlYW0tdXVpZA==", "items", 0, "graphql_id")
	requireJSONPathEqual(t, text, "backend", "items", 0, "slug")
}

func TestListTeamsError(t *testing.T) {
	assert := require.New(t)

	client := &mockTeamsClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error) {
			return nil, nil, apiError(http.StatusForbidden, "missing read_teams scope")
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{TeamsClient: client})

	_, handler, _ := ListTeams()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTeamsArgs{OrgSlug: "org"})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(getTextResult(t, result).Text, "missing read_teams scope")
}

func TestListTeamPipelines(t *testing.T) {
	assert := require.New(t)

	var gotTeamID string
	client := &mockTeamPipelinesClient{
		ListFunc: func(ctx context.Context, org, teamID string, opt *buildkite.TeamPipelinesListOptions) ([]buildkite.TeamPipeline, *buildkite.Response, error) {
			gotTeamID = teamID
			return []buildkite.TeamPipeline{
				{
					ID:          "pipeline-uuid",
					URL:         "https://api.buildkite.com/v2/organizations/org/pipelines/shardnado",
					AccessLevel: "build_and_read",
				},
			}, okResponse(), nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{TeamPipelinesClient: client})

	tool, handler, scopes := ListTeamPipelines()
	assert.Equal("list_team_pipelines", tool.Name)
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Equal([]string{"read_teams"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListTeamPipelinesArgs{OrgSlug: "org", TeamID: "team-uuid"})
	assert.NoError(err)
	assert.Equal("team-uuid", gotTeamID)

	assert.JSONEq(`{"headers":{"Link":""},"items":[{"pipeline_id":"pipeline-uuid","pipeline_url":"https://api.buildkite.com/v2/organizations/org/pipelines/shardnado","access_level":"build_and_read"}]}`, getTextResult(t, result).Text)
}

func TestListPipelineTeams(t *testing.T) {
	assert := require.New(t)

	pipelines := &MockPipelinesClient{
		GetFunc: func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("shardnado", pipeline)
			return buildkite.Pipeline{ID: "pipeline-uuid", GraphQLID: "UGlwZWxpbmUtLS1waXBlbGluZS11dWlk", Slug: "shardnado"}, okResponse(), nil
		},
	}

	// Two pages of teams so the handler has to follow NextPage.
	teams := &mockTeamsClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error) {
			assert.Equal(100, opt.PerPage)
			switch opt.Page {
			case 1:
				resp := okResponse()
				resp.NextPage = 2
				return []buildkite.Team{
					{ID: "team-platform", GraphQLID: "gql-platform", Slug: "platform", Name: "Platform"},
					{ID: "team-nobody", GraphQLID: "gql-nobody", Slug: "nobody", Name: "Nobody"},
				}, resp, nil
			case 2:
				return []buildkite.Team{
					{ID: "team-backend", GraphQLID: "gql-backend", Slug: "backend", Name: "Backend"},
				}, okResponse(), nil
			default:
				t.Fatalf("unexpected teams page %d", opt.Page)
				return nil, nil, nil
			}
		},
	}

	var checked []string
	associations := &mockTeamPipelinesClient{
		GetFunc: func(ctx context.Context, org, teamID, pipelineID string) (buildkite.TeamPipeline, *buildkite.Response, error) {
			assert.Equal("pipeline-uuid", pipelineID, "association lookups must use the pipeline UUID, not the slug")
			checked = append(checked, teamID)
			switch teamID {
			case "team-platform":
				return buildkite.TeamPipeline{ID: pipelineID, AccessLevel: "manage_build_and_read"}, okResponse(), nil
			case "team-backend":
				return buildkite.TeamPipeline{ID: pipelineID, AccessLevel: "build_and_read"}, okResponse(), nil
			default:
				return buildkite.TeamPipeline{}, nil, apiError(http.StatusNotFound, "No team pipeline found")
			}
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		PipelinesClient:     pipelines,
		TeamsClient:         teams,
		TeamPipelinesClient: associations,
	})

	tool, handler, scopes := ListPipelineTeams()
	assert.Equal("list_pipeline_teams", tool.Name)
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Equal([]string{"read_pipelines", "read_teams"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListPipelineTeamsArgs{OrgSlug: "org", PipelineSlug: "shardnado"})
	assert.NoError(err)
	assert.False(result.IsError)
	assert.Equal([]string{"team-platform", "team-nobody", "team-backend"}, checked)

	var got PipelineTeamsResult
	assert.NoError(json.Unmarshal([]byte(getTextResult(t, result).Text), &got))
	assert.Equal("pipeline-uuid", got.PipelineID)
	assert.Equal("UGlwZWxpbmUtLS1waXBlbGluZS11dWlk", got.PipelineGraphQLID)
	assert.Equal("shardnado", got.PipelineSlug)
	assert.Equal(3, got.TeamsChecked)

	// Sorted by team slug; the 404 team is left out.
	assert.Equal([]PipelineTeam{
		{TeamID: "team-backend", TeamGraphQLID: "gql-backend", TeamSlug: "backend", TeamName: "Backend", AccessLevel: "build_and_read"},
		{TeamID: "team-platform", TeamGraphQLID: "gql-platform", TeamSlug: "platform", TeamName: "Platform", AccessLevel: "manage_build_and_read"},
	}, got.Teams)
}

func TestListPipelineTeamsNoTeamsHaveAccess(t *testing.T) {
	assert := require.New(t)

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		PipelinesClient: &MockPipelinesClient{
			GetFunc: func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
				return buildkite.Pipeline{ID: "pipeline-uuid", Slug: pipeline}, okResponse(), nil
			},
		},
		TeamsClient: &mockTeamsClient{
			ListFunc: func(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error) {
				return []buildkite.Team{{ID: "team-a", Slug: "a"}}, okResponse(), nil
			},
		},
		TeamPipelinesClient: &mockTeamPipelinesClient{
			GetFunc: func(ctx context.Context, org, teamID, pipelineID string) (buildkite.TeamPipeline, *buildkite.Response, error) {
				return buildkite.TeamPipeline{}, nil, apiError(http.StatusNotFound, "No team pipeline found")
			},
		},
	})

	_, handler, _ := ListPipelineTeams()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListPipelineTeamsArgs{OrgSlug: "org", PipelineSlug: "shardnado"})
	assert.NoError(err)
	assert.False(result.IsError)

	// teams is an empty array, not null, so callers can tell "checked and found none" apart from an error.
	assert.JSONEq(`{"pipeline_id":"pipeline-uuid","pipeline_slug":"shardnado","teams_checked":1,"teams":[]}`, getTextResult(t, result).Text)
}

func TestListPipelineTeamsErrors(t *testing.T) {
	pipelineOK := &MockPipelinesClient{
		GetFunc: func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
			return buildkite.Pipeline{ID: "pipeline-uuid", Slug: pipeline}, okResponse(), nil
		},
	}
	oneTeam := &mockTeamsClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error) {
			return []buildkite.Team{{ID: "team-a", Slug: "a"}}, okResponse(), nil
		},
	}

	t.Run("pipeline not found is a tool error", func(t *testing.T) {
		assert := require.New(t)
		ctx := ContextWithDeps(context.Background(), ToolDependencies{
			PipelinesClient: &MockPipelinesClient{
				GetFunc: func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
					return buildkite.Pipeline{}, nil, apiError(http.StatusNotFound, "Not Found")
				},
			},
		})
		_, handler, _ := ListPipelineTeams()
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListPipelineTeamsArgs{OrgSlug: "org", PipelineSlug: "missing"})
		assert.NoError(err)
		assert.True(result.IsError)
		assert.Contains(getTextResult(t, result).Text, "Not Found")
	})

	t.Run("non-404 association error is a tool error", func(t *testing.T) {
		assert := require.New(t)
		ctx := ContextWithDeps(context.Background(), ToolDependencies{
			PipelinesClient: pipelineOK,
			TeamsClient:     oneTeam,
			TeamPipelinesClient: &mockTeamPipelinesClient{
				GetFunc: func(ctx context.Context, org, teamID, pipelineID string) (buildkite.TeamPipeline, *buildkite.Response, error) {
					return buildkite.TeamPipeline{}, nil, apiError(http.StatusForbidden, "missing read_teams scope")
				},
			},
		})
		_, handler, _ := ListPipelineTeams()
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListPipelineTeamsArgs{OrgSlug: "org", PipelineSlug: "shardnado"})
		assert.NoError(err)
		assert.True(result.IsError)
		assert.Contains(getTextResult(t, result).Text, "missing read_teams scope")
	})

	t.Run("401 from teams list propagates as ErrUnauthorized", func(t *testing.T) {
		assert := require.New(t)
		ctx := ContextWithDeps(context.Background(), ToolDependencies{
			PipelinesClient: pipelineOK,
			TeamsClient: &mockTeamsClient{
				ListFunc: func(ctx context.Context, org string, opt *buildkite.TeamsListOptions) ([]buildkite.Team, *buildkite.Response, error) {
					return nil, nil, apiError(http.StatusUnauthorized, "Unauthorized")
				},
			},
		})
		_, handler, _ := ListPipelineTeams()
		result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListPipelineTeamsArgs{OrgSlug: "org", PipelineSlug: "shardnado"})
		assert.Nil(result)
		assert.ErrorIs(err, ErrUnauthorized)
	})
}

func TestIsBuildkiteNotFound(t *testing.T) {
	assert := require.New(t)
	assert.True(isBuildkiteNotFound(apiError(http.StatusNotFound, "nope")))
	assert.False(isBuildkiteNotFound(apiError(http.StatusForbidden, "nope")))
	assert.False(isBuildkiteNotFound(nil))
	assert.False(isBuildkiteNotFound(context.Canceled))
}
