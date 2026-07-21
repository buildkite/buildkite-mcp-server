package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type PipelineSchedulesClient interface {
	List(ctx context.Context, org, pipelineSlug string, opt *buildkite.PipelineScheduleListOptions) ([]buildkite.PipelineSchedule, *buildkite.Response, error)
	Get(ctx context.Context, org, pipelineSlug, id string) (buildkite.PipelineSchedule, *buildkite.Response, error)
	Create(ctx context.Context, org, pipelineSlug string, in buildkite.CreatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error)
	Update(ctx context.Context, org, pipelineSlug, id string, in buildkite.UpdatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error)
}

type ListPipelineSchedulesArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

func ListPipelineSchedules() (mcp.Tool, mcp.ToolHandlerFor[ListPipelineSchedulesArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_pipeline_schedules",
			Description: "List the pipeline schedules for a pipeline, including cron expression, target branch, environment variables, enabled state, and next scheduled build time",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Pipeline Schedules",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListPipelineSchedulesArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListPipelineSchedules")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			schedules, resp, err := deps.PipelineSchedulesClient.List(ctx, args.OrgSlug, args.PipelineSlug, &buildkite.PipelineScheduleListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.PipelineSchedule]{
				Items: schedules,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(schedules)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_pipelines"}
}

type GetPipelineScheduleArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	ScheduleID   string `json:"schedule_id"`
}

func GetPipelineSchedule() (mcp.Tool, mcp.ToolHandlerFor[GetPipelineScheduleArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_pipeline_schedule",
			Description: "Get detailed information about a single pipeline schedule including its cron expression, target branch, environment variables, enabled state, last failure, and next build time",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Pipeline Schedule",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args GetPipelineScheduleArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetPipelineSchedule")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("schedule_id", args.ScheduleID),
			)

			deps := DepsFromContext(ctx)
			schedule, _, err := deps.PipelineSchedulesClient.Get(ctx, args.OrgSlug, args.PipelineSlug, args.ScheduleID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &schedule)
		}, []string{"read_pipelines"}
}

type CreatePipelineScheduleArgs struct {
	OrgSlug      string            `json:"org_slug"`
	PipelineSlug string            `json:"pipeline_slug"`
	Cronline     string            `json:"cronline" jsonschema:"Schedule interval as a crontab expression (e.g. '0 0 * * *') or predefined value (e.g. '@daily', '@hourly', '@weekly', '@monthly', '@yearly')"`
	Label        string            `json:"label,omitempty" jsonschema:"Descriptive label for the schedule"`
	Message      string            `json:"message,omitempty" jsonschema:"Message attached to triggered builds"`
	Commit       string            `json:"commit,omitempty" jsonschema:"Commit reference (defaults to HEAD)"`
	Branch       string            `json:"branch,omitempty" jsonschema:"Target branch (defaults to the pipeline default branch)"`
	Env          map[string]string `json:"env,omitempty" jsonschema:"Environment variables to set on triggered builds"`
	Enabled      *bool             `json:"enabled,omitempty" jsonschema:"Whether the schedule is active. Defaults to true if unset."`
}

func CreatePipelineSchedule() (mcp.Tool, mcp.ToolHandlerFor[CreatePipelineScheduleArgs, any], []string) {
	return mcp.Tool{
			Name:        "create_pipeline_schedule",
			Description: "Create a new pipeline schedule that triggers builds on a cron-driven interval",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Create Pipeline Schedule",
				DestructiveHint: boolPtr(false),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args CreatePipelineScheduleArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreatePipelineSchedule")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("cronline", args.Cronline),
			)

			create := buildkite.CreatePipelineSchedule{
				Cronline: args.Cronline,
				Label:    args.Label,
				Message:  args.Message,
				Commit:   args.Commit,
				Branch:   args.Branch,
				Env:      args.Env,
				Enabled:  args.Enabled,
			}

			deps := DepsFromContext(ctx)
			schedule, _, err := deps.PipelineSchedulesClient.Create(ctx, args.OrgSlug, args.PipelineSlug, create)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &schedule)
		}, []string{"write_pipelines"}
}

type UpdatePipelineScheduleArgs struct {
	OrgSlug      string            `json:"org_slug"`
	PipelineSlug string            `json:"pipeline_slug"`
	ScheduleID   string            `json:"schedule_id"`
	Cronline     *string           `json:"cronline,omitempty" jsonschema:"Schedule interval as a crontab expression or predefined value"`
	Label        *string           `json:"label,omitempty"`
	Message      *string           `json:"message,omitempty"`
	Commit       *string           `json:"commit,omitempty"`
	Branch       *string           `json:"branch,omitempty"`
	Env          map[string]string `json:"env,omitempty" jsonschema:"Environment variables to set on triggered builds. Providing this field REPLACES the existing env map entirely — include all keys you want to retain."`
	Enabled      *bool             `json:"enabled,omitempty" jsonschema:"Whether the schedule is active. Re-enabling clears previous failure data."`
}

func UpdatePipelineSchedule() (mcp.Tool, mcp.ToolHandlerFor[UpdatePipelineScheduleArgs, any], []string) {
	return mcp.Tool{
			Name:        "update_pipeline_schedule",
			Description: "Modify an existing pipeline schedule's cron expression, branch, environment variables, or enabled state",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Update Pipeline Schedule",
				DestructiveHint: boolPtr(true),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdatePipelineScheduleArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.UpdatePipelineSchedule")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("schedule_id", args.ScheduleID),
			)

			update := buildkite.UpdatePipelineSchedule{}
			if args.Cronline != nil {
				update.Cronline = buildkite.Some(*args.Cronline)
			}
			if args.Label != nil {
				update.Label = buildkite.Some(*args.Label)
			}
			if args.Message != nil {
				update.Message = buildkite.Some(*args.Message)
			}
			if args.Commit != nil {
				update.Commit = buildkite.Some(*args.Commit)
			}
			if args.Branch != nil {
				update.Branch = buildkite.Some(*args.Branch)
			}
			if args.Env != nil {
				update.Env = buildkite.Some(args.Env)
			}
			if args.Enabled != nil {
				update.Enabled = buildkite.Some(*args.Enabled)
			}

			deps := DepsFromContext(ctx)
			schedule, _, err := deps.PipelineSchedulesClient.Update(ctx, args.OrgSlug, args.PipelineSlug, args.ScheduleID, update)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &schedule)
		}, []string{"write_pipelines"}
}
