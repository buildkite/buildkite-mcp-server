package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/internal/commands"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/server"
)

const (
	readmePath = "README.md"
	// Markers for the tools section in the README
	toolsSectionStart = "# Tools"
	toolsSectionEnd   = "Example of the `get_pipeline` tool in action."
)

func main() {
	ctx := context.Background()

	// Create a dummy client to initialize tools
	client := &gobuildkite.Client{}

	// Collect all tools
	tools := commands.BuildkiteTools(ctx, client)

	// Generate markdown documentation for the tools
	toolsDocs := generateToolsDocs(tools)

	// Update the README
	updateReadme(toolsDocs)
}

func generateToolsDocs(tools []server.ServerTool) string {
	var buffer strings.Builder

	buffer.WriteString(toolsSectionStart + "\n\n")

	for _, st := range tools {
		buffer.WriteString(fmt.Sprintf("* `%s` - %s\n", st.Tool.Name, st.Tool.Description))
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
