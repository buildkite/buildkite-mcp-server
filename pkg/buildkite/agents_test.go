package buildkite

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	gobuildkite "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

var _ AgentsClient = (*mockAgentsClient)(nil)

type mockAgentsClient struct {
	ListFunc func(ctx context.Context, org string, opts *gobuildkite.AgentListOptions) ([]gobuildkite.Agent, *gobuildkite.Response, error)
	GetFunc  func(ctx context.Context, org, id string) (gobuildkite.Agent, *gobuildkite.Response, error)
}

func (m *mockAgentsClient) List(ctx context.Context, org string, opts *gobuildkite.AgentListOptions) ([]gobuildkite.Agent, *gobuildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, opts)
	}
	return nil, nil, nil
}

func (m *mockAgentsClient) Get(ctx context.Context, org, id string) (gobuildkite.Agent, *gobuildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, id)
	}
	return gobuildkite.Agent{}, nil, nil
}

func TestListAgents(t *testing.T) {
	assert := require.New(t)

	client := &mockAgentsClient{
		ListFunc: func(ctx context.Context, org string, opts *gobuildkite.AgentListOptions) ([]gobuildkite.Agent, *gobuildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("agent-name", opts.Name)
			assert.Equal("host-1", opts.Hostname)
			assert.Equal("3.90.0", opts.Version)
			assert.Equal(2, opts.Page)
			assert.Equal(50, opts.PerPage)

			return []gobuildkite.Agent{{
					ID:             "agent-id",
					Name:           "agent-name",
					ConnectedState: "connected",
					Hostname:       "host-1",
					Version:        "3.90.0",
				}}, &gobuildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
						Header:     http.Header{"Link": []string{"<next>"}},
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: client})

	tool, handler, scopes := ListAgents()
	assert.Equal("list_agents", tool.Name)
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Contains(scopes, "read_agents")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListAgentsArgs{
		OrgSlug:     "org",
		Name:        "agent-name",
		Hostname:    "host-1",
		Version:     "3.90.0",
		Page:        2,
		PerPage:     50,
		DetailLevel: "summary",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"agent-id","name":"agent-name","connection_state":"connected","hostname":"host-1","version":"3.90.0"}]}`, textContent.Text)
}

func TestGetAgent(t *testing.T) {
	assert := require.New(t)

	paused := true
	client := &mockAgentsClient{
		GetFunc: func(ctx context.Context, org, id string) (gobuildkite.Agent, *gobuildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("agent-id", id)

			return gobuildkite.Agent{
				ID:             "agent-id",
				Name:           "agent-name",
				ConnectedState: "connected",
				Hostname:       "host-1",
				Paused:         &paused,
			}, &gobuildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: client})

	tool, handler, scopes := GetAgent()
	assert.Equal("get_agent", tool.Name)
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Contains(scopes, "read_agents")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetAgentArgs{
		OrgSlug:     "org",
		AgentID:     "agent-id",
		DetailLevel: "summary",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"agent-id","name":"agent-name","connection_state":"connected","hostname":"host-1","paused":true}`, textContent.Text)
}

func TestListAgentsDetailed(t *testing.T) {
	assert := require.New(t)

	priority := 7
	paused := true
	client := &mockAgentsClient{
		ListFunc: func(ctx context.Context, org string, opts *gobuildkite.AgentListOptions) ([]gobuildkite.Agent, *gobuildkite.Response, error) {
			return []gobuildkite.Agent{{
				ID:             "agent-id",
				Name:           "agent-name",
				ConnectedState: "connected",
				Hostname:       "host-1",
				IPAddress:      "10.0.0.1",
				UserAgent:      "buildkite-agent/3.90.0",
				Version:        "3.90.0",
				OSID:           "ubuntu",
				Arch:           "amd64",
				Queue:          "default",
				Priority:       &priority,
				Metadata:       []string{"queue=default"},
				Paused:         &paused,
				Job:            &gobuildkite.Job{ID: "job-id", Name: "tests", State: "running"},
			}}, &gobuildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: client})

	_, handler, _ := ListAgents()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListAgentsArgs{OrgSlug: "org", DetailLevel: "detailed"})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"agent-id","name":"agent-name","connection_state":"connected","hostname":"host-1","ip_address":"10.0.0.1","user_agent":"buildkite-agent/3.90.0","version":"3.90.0","os_id":"ubuntu","arch":"amd64","queue":"default","priority":7,"meta_data":["queue=default"],"paused":true,"job":{"id":"job-id","name":"tests","state":"running"}}]}`, textContent.Text)
}

func TestGetAgentDetailed(t *testing.T) {
	assert := require.New(t)

	paused := true
	client := &mockAgentsClient{
		GetFunc: func(ctx context.Context, org, id string) (gobuildkite.Agent, *gobuildkite.Response, error) {
			return gobuildkite.Agent{
				ID:             "agent-id",
				Name:           "agent-name",
				ConnectedState: "connected",
				Hostname:       "host-1",
				OSID:           "ubuntu",
				Arch:           "amd64",
				Queue:          "default",
				Paused:         &paused,
				Job:            &gobuildkite.Job{ID: "job-id", Name: "tests", State: "running"},
			}, &gobuildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: client})

	_, handler, _ := GetAgent()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetAgentArgs{OrgSlug: "org", AgentID: "agent-id", DetailLevel: "detailed"})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"agent-id","name":"agent-name","connection_state":"connected","hostname":"host-1","os_id":"ubuntu","arch":"amd64","queue":"default","paused":true,"job":{"id":"job-id","name":"tests","state":"running"}}`, textContent.Text)
}

func TestGetAgentFullIncludesUpstreamAgentFields(t *testing.T) {
	assert := require.New(t)

	client := &mockAgentsClient{
		GetFunc: func(ctx context.Context, org, id string) (gobuildkite.Agent, *gobuildkite.Response, error) {
			return gobuildkite.Agent{
				ID:         "agent-id",
				Name:       "agent-name",
				AgentToken: "agent-token",
				Queue:      "default",
			}, &gobuildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: client})

	_, handler, _ := GetAgent()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetAgentArgs{OrgSlug: "org", AgentID: "agent-id"})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"agent-id","name":"agent-name","access_token":"agent-token","queue":"default"}`, textContent.Text)
}

func TestListAgentsInvalidDetailLevel(t *testing.T) {
	assert := require.New(t)

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: &mockAgentsClient{}})

	_, handler, _ := ListAgents()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListAgentsArgs{OrgSlug: "org", DetailLevel: "bad"})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "detail_level must be 'summary', 'detailed', or 'full'")
}

func TestGetAgentInvalidDetailLevel(t *testing.T) {
	assert := require.New(t)

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: &mockAgentsClient{}})

	_, handler, _ := GetAgent()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetAgentArgs{OrgSlug: "org", AgentID: "agent-id", DetailLevel: "bad"})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "detail_level must be 'summary', 'detailed', or 'full'")
}

func TestListAgentsWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockAgentsClient{
		ListFunc: func(ctx context.Context, org string, opts *gobuildkite.AgentListOptions) ([]gobuildkite.Agent, *gobuildkite.Response, error) {
			return nil, &gobuildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: client})

	_, handler, _ := ListAgents()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListAgentsArgs{OrgSlug: "org"})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestGetAgentWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockAgentsClient{
		GetFunc: func(ctx context.Context, org, id string) (gobuildkite.Agent, *gobuildkite.Response, error) {
			return gobuildkite.Agent{}, &gobuildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{AgentsClient: client})

	_, handler, _ := GetAgent()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetAgentArgs{OrgSlug: "org", AgentID: "agent-id"})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}
