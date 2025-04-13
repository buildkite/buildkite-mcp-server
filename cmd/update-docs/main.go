package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/internal/buildkite"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	readmePath = "README.md"
	// Markers for the tools section in the README
	toolsSectionStart = "# Tools"
	toolsSectionEnd   = "Example of the `get_pipeline` tool in action."
)

func main() {
	// Create a dummy client to initialize tools
	client := &gobuildkite.Client{}
	ctx := context.Background()

	// Collect all tools
	var tools []mcp.Tool

	// Get all tools, similar to how they're registered in stdio.go
	tool, _ := buildkite.GetPipeline(ctx, client)
	tools = append(tools, tool)

	tool, _ = buildkite.ListPipeline(ctx, client)
	tools = append(tools, tool)

	tool, _ = buildkite.ListBuilds(ctx, client)
	tools = append(tools, tool)

	tool, _ = buildkite.GetBuild(ctx, client)
	tools = append(tools, tool)
	
	tool, _ = buildkite.GetJobLogs(ctx, client)
	tools = append(tools, tool)

	// We can't easily initialize these because they need specific client interfaces
	// but we can add them manually
	tools = append(tools, mcp.NewTool("current_user", mcp.WithDescription("Get the current user")))
	tools = append(tools, mcp.NewTool("access_token", mcp.WithDescription("Get the details for the API access token that was used to authenticate the request")))

	// Generate markdown documentation for the tools
	toolsDocs := generateToolsDocs(tools)

	// Update the README
	updateReadme(toolsDocs)
}

func generateToolsDocs(tools []mcp.Tool) string {
	var buffer strings.Builder

	buffer.WriteString(toolsSectionStart + "\n\n")
	
	for _, tool := range tools {
		buffer.WriteString(fmt.Sprintf("* `%s` - %s\n", tool.Name, tool.Description))
	}
	
	buffer.WriteString("\n")

	return buffer.String()
}

func updateReadme(toolsDocs string) {
	// Read the current README
	content, err := os.ReadFile(readmePath)
	if err != nil {
		log.Fatalf("Error reading README: %v", err)
	}

	contentStr := string(content)

	// Define the regular expression to find the tools section
	re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(toolsSectionStart) + `.*?` + regexp.QuoteMeta(toolsSectionEnd))

	// Replace the tools section with the new content plus the example line
	newContent := re.ReplaceAllString(contentStr, toolsDocs+toolsSectionEnd)

	// Write the updated README
	err = os.WriteFile(readmePath, []byte(newContent), 0644)
	if err != nil {
		log.Fatalf("Error writing README: %v", err)
	}

	fmt.Println("README updated successfully!")
}
