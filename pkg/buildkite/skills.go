package buildkite

import (
	"context"
	"fmt"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type skill struct {
	Name        string
	Description string
	Path        string // path within resourcesFS
}

var skillRegistry = []skill{
	{
		Name:        "debug-logs-guide",
		Description: "How to debug Buildkite build failures using tail_logs, search_logs, and read_logs — including failed vs broken job semantics and token-efficient investigation workflow.",
		Path:        "resources/debug-logs-guide.md",
	},
}

type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ListSkillsArgs struct {
	Query string `json:"query,omitempty" jsonschema:"Optional case-insensitive filter over skill name and description"`
}

func ListSkills() (mcp.Tool, mcp.ToolHandlerFor[ListSkillsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_skills",
			Description: "List available skill guides that document usage patterns, pitfalls, and workflows for Buildkite MCP tools",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Skill Guides",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListSkillsArgs) (*mcp.CallToolResult, any, error) {
			_, span := trace.Start(ctx, "buildkite.ListSkills")
			defer span.End()

			results := []skillSummary{}
			query := strings.ToLower(args.Query)
			for _, s := range skillRegistry {
				if query == "" || strings.Contains(strings.ToLower(s.Name), query) || strings.Contains(strings.ToLower(s.Description), query) {
					results = append(results, skillSummary{Name: s.Name, Description: s.Description})
				}
			}

			return mcpTextResult(span, results)
		}, []string{}
}

type LoadSkillArgs struct {
	SkillName string `json:"skill_name" jsonschema:"Name of the skill to load, as returned by list_skills"`
}

func LoadSkill() (mcp.Tool, mcp.ToolHandlerFor[LoadSkillArgs, any], []string) {
	return mcp.Tool{
			Name:        "load_skill",
			Description: "Load the full content of a skill guide by name",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Load Skill Guide",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args LoadSkillArgs) (*mcp.CallToolResult, any, error) {
			_, span := trace.Start(ctx, "buildkite.LoadSkill")
			defer span.End()

			var match *skill
			for i := range skillRegistry {
				if skillRegistry[i].Name == args.SkillName {
					match = &skillRegistry[i]
					break
				}
			}

			if match == nil {
				names := make([]string, len(skillRegistry))
				for i := range skillRegistry {
					names[i] = skillRegistry[i].Name
				}
				return utils.NewToolResultError(fmt.Sprintf("unknown skill %q, valid skills are: %s", args.SkillName, strings.Join(names, ", "))), nil, nil
			}

			content, err := resourcesFS.ReadFile(match.Path)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to read skill %q: %v", args.SkillName, err)), nil, nil
			}

			return utils.NewToolResultText(string(content)), nil, nil
		}, []string{}
}
