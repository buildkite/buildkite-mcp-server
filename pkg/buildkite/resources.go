package buildkite

import (
	"context"
	"embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed resources/*.md
var resourcesFS embed.FS

func HandleDebugLogsGuideResource(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	content, err := resourcesFS.ReadFile("resources/debug-logs-guide.md")
	if err != nil {
		return nil, err
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      request.Params.URI,
				MIMEType: "text/markdown",
				Text:     string(content),
			},
		},
	}, nil
}
