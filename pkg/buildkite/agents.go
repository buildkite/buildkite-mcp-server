package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type AgentsClient interface {
	List(ctx context.Context, org string, opts *buildkite.AgentListOptions) ([]buildkite.Agent, *buildkite.Response, error)
	Get(ctx context.Context, org, id string) (buildkite.Agent, *buildkite.Response, error)
}

type ListAgentsArgs struct {
	OrgSlug     string `json:"org_slug"`
	Name        string `json:"name,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Version     string `json:"version,omitempty"`
	Page        int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage     int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
	DetailLevel string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary' (default), 'detailed', or 'full'"`
}

type GetAgentArgs struct {
	OrgSlug     string `json:"org_slug"`
	AgentID     string `json:"agent_id"`
	DetailLevel string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary', 'detailed', or 'full' (default)"`
}

type AgentJobSummary struct {
	ID    string `json:"id"`
	Type  string `json:"type,omitempty"`
	Name  string `json:"name,omitempty"`
	State string `json:"state,omitempty"`
}

type AgentSummary struct {
	ID              string           `json:"id"`
	Name            string           `json:"name,omitempty"`
	ConnectionState string           `json:"connection_state,omitempty"`
	Hostname        string           `json:"hostname,omitempty"`
	Version         string           `json:"version,omitempty"`
	Paused          *bool            `json:"paused,omitempty"`
	Job             *AgentJobSummary `json:"job,omitempty"`
}

type AgentDetail struct {
	ID                     string               `json:"id"`
	Name                   string               `json:"name,omitempty"`
	WebURL                 string               `json:"web_url,omitempty"`
	ConnectionState        string               `json:"connection_state,omitempty"`
	Hostname               string               `json:"hostname,omitempty"`
	IPAddress              string               `json:"ip_address,omitempty"`
	UserAgent              string               `json:"user_agent,omitempty"`
	Version                string               `json:"version,omitempty"`
	OSID                   string               `json:"os_id,omitempty"`
	Arch                   string               `json:"arch,omitempty"`
	Queue                  string               `json:"queue,omitempty"`
	Priority               *int                 `json:"priority,omitempty"`
	Metadata               []string             `json:"meta_data,omitempty"`
	CreatedAt              *buildkite.Timestamp `json:"created_at,omitempty"`
	LastJobFinishedAt      *buildkite.Timestamp `json:"last_job_finished_at,omitempty"`
	Paused                 *bool                `json:"paused,omitempty"`
	PausedAt               *buildkite.Timestamp `json:"paused_at,omitempty"`
	PausedNote             *string              `json:"paused_note,omitempty"`
	PausedTimeoutInMinutes *int                 `json:"paused_timeout_in_minutes,omitempty"`
	Job                    *AgentJobSummary     `json:"job,omitempty"`
}

func summarizeAgentJob(job *buildkite.Job) *AgentJobSummary {
	if job == nil {
		return nil
	}

	return &AgentJobSummary{
		ID:    job.ID,
		Type:  job.Type,
		Name:  job.Name,
		State: job.State,
	}
}

func summarizeAgent(agent buildkite.Agent) AgentSummary {
	return AgentSummary{
		ID:              agent.ID,
		Name:            agent.Name,
		ConnectionState: agent.ConnectedState,
		Hostname:        agent.Hostname,
		Version:         agent.Version,
		Paused:          agent.Paused,
		Job:             summarizeAgentJob(agent.Job),
	}
}

func detailAgent(agent buildkite.Agent) AgentDetail {
	return AgentDetail{
		ID:                     agent.ID,
		Name:                   agent.Name,
		WebURL:                 agent.WebURL,
		ConnectionState:        agent.ConnectedState,
		Hostname:               agent.Hostname,
		IPAddress:              agent.IPAddress,
		UserAgent:              agent.UserAgent,
		Version:                agent.Version,
		OSID:                   agent.OSID,
		Arch:                   agent.Arch,
		Queue:                  agent.Queue,
		Priority:               agent.Priority,
		Metadata:               agent.Metadata,
		CreatedAt:              agent.CreatedAt,
		LastJobFinishedAt:      agent.LastJobFinishedAt,
		Paused:                 agent.Paused,
		PausedAt:               agent.PausedAt,
		PausedNote:             agent.PausedNote,
		PausedTimeoutInMinutes: agent.PausedTimeoutInMinutes,
		Job:                    summarizeAgentJob(agent.Job),
	}
}

func createPaginatedAgentResult[T any](agents []buildkite.Agent, converter func(buildkite.Agent) T, headers map[string]string) PaginatedResult[T] {
	items := make([]T, len(agents))
	for i, agent := range agents {
		items[i] = converter(agent)
	}

	return PaginatedResult[T]{
		Items:   items,
		Headers: headers,
	}
}

func ListAgents() (mcp.Tool, mcp.ToolHandlerFor[ListAgentsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_agents",
			Description: "List agents in an organization with their connection state, host details, version, current job, and pause status",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Agents",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListAgentsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListAgents")
			defer span.End()

			if args.DetailLevel == "" {
				args.DetailLevel = "summary"
			}

			switch args.DetailLevel {
			case "summary", "detailed", "full":
			default:
				return utils.NewToolResultError("detail_level must be 'summary', 'detailed', or 'full'"), nil, nil
			}

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("name_filter", args.Name),
				attribute.String("hostname_filter", args.Hostname),
				attribute.String("version_filter", args.Version),
				attribute.String("detail_level", args.DetailLevel),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			agents, resp, err := deps.AgentsClient.List(ctx, args.OrgSlug, &buildkite.AgentListOptions{
				ListOptions: paginationParams,
				Name:        args.Name,
				Hostname:    args.Hostname,
				Version:     args.Version,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			headers := map[string]string{
				"Link": resp.Header.Get("Link"),
			}

			var result any
			switch args.DetailLevel {
			case "summary":
				result = createPaginatedAgentResult(agents, summarizeAgent, headers)
			case "detailed":
				result = createPaginatedAgentResult(agents, detailAgent, headers)
			default: // full
				result = createPaginatedAgentResult(agents, func(a buildkite.Agent) buildkite.Agent { return a }, headers)
			}

			span.SetAttributes(attribute.Int("item_count", len(agents)))

			return mcpTextResult(span, &result)
		}, []string{"read_agents"}
}

func GetAgent() (mcp.Tool, mcp.ToolHandlerFor[GetAgentArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_agent",
			Description: "Get detailed information about a specific agent including its connection state, host details, current job, metadata, and pause status",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Agent",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args GetAgentArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetAgent")
			defer span.End()

			if args.DetailLevel == "" {
				args.DetailLevel = "full"
			}

			switch args.DetailLevel {
			case "summary", "detailed", "full":
			default:
				return utils.NewToolResultError("detail_level must be 'summary', 'detailed', or 'full'"), nil, nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("agent_id", args.AgentID),
				attribute.String("detail_level", args.DetailLevel),
			)

			deps := DepsFromContext(ctx)
			agent, _, err := deps.AgentsClient.Get(ctx, args.OrgSlug, args.AgentID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			var result any
			switch args.DetailLevel {
			case "summary":
				result = summarizeAgent(agent)
			case "detailed":
				result = detailAgent(agent)
			default: // full
				result = agent
			}

			return mcpTextResult(span, &result)
		}, []string{"read_agents"}
}
