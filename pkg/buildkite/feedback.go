package buildkite

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const reportIssuePromptName = "report_issue"

const reportIssuePromptDescription = "Generate a structured, copy-pasteable report of a problem with this MCP server (failed tool calls, unexpected results, or missing capabilities) that the user can submit as a GitHub issue"

const reportIssuePromptTemplate = `You are helping the user report a problem with the Buildkite MCP server (buildkite-mcp-server).

Review this conversation for Buildkite MCP tool calls that failed, returned errors, or behaved differently than the user expected, then produce an issue report the user can copy and paste.

Rules:
- Redact all secrets before including anything in the report: API tokens, credentials, environment variable values, and signed or private URLs. Replace each with [REDACTED].
- Do not include full log output. Quote only short excerpts (a few lines) directly relevant to the problem.
- Only include information visible in this conversation. Do not guess or invent details; write "unknown" where information is missing.

Output the report inside a single fenced markdown code block using this template:

## Summary
One sentence describing what went wrong.

## Environment
- buildkite-mcp-server version: %s
- MCP client: %s

## Tool calls
For each problematic tool call:
- Tool: the tool name
- Arguments: the arguments as JSON, redacted per the rules above
- Actual behavior: the error message or unexpected result
- Expected behavior: what should have happened

## What I was trying to do
The user's goal, in one or two sentences.

## Additional context
Anything else relevant, or "none".

After the report, tell the user they can submit it by opening an issue at https://github.com/buildkite/buildkite-mcp-server/issues/new and pasting the report into the body, or by sharing it with Buildkite support.`

// NewReportIssuePrompt returns the report_issue prompt and its handler. The
// handler embeds the server version and, when available, the connected MCP
// client's name and version so reports carry environment details the model
// cannot reliably know itself.
func NewReportIssuePrompt(version string) (*mcp.Prompt, mcp.PromptHandler) {
	prompt := &mcp.Prompt{
		Name:        reportIssuePromptName,
		Description: reportIssuePromptDescription,
	}

	handler := func(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		client := "unknown"
		if request != nil && request.Session != nil {
			if ip := request.Session.InitializeParams(); ip != nil && ip.ClientInfo != nil && ip.ClientInfo.Name != "" {
				if ip.ClientInfo.Version != "" {
					client = fmt.Sprintf("%s %s", ip.ClientInfo.Name, ip.ClientInfo.Version)
				} else {
					client = ip.ClientInfo.Name
				}
			}
		}

		text := fmt.Sprintf(reportIssuePromptTemplate, version, client)

		return &mcp.GetPromptResult{
			Description: reportIssuePromptDescription,
			Messages: []*mcp.PromptMessage{
				{
					Role:    mcp.Role("user"),
					Content: &mcp.TextContent{Text: text},
				},
			},
		}, nil
	}

	return prompt, handler
}
