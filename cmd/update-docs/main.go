package main

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	readmePath = "README.md"
	// Markers for the tools section in the README
	toolsSectionStart = "## 🛠️ Tools & Features"
	toolsSectionEnd   = "## 📸 Screenshots"
)

func main() {
	// Create a dummy client to initialize tools
	client := &gobuildkite.Client{}

	// Collect all tools (pass nil for ParquetClient since this is just for docs)
	tools := server.BuildkiteTools(client, nil)

	// Generate markdown documentation for the tools
	toolsDocs := generateToolsDocs(tools)

	// Update the README
	updateReadme(toolsDocs)
}

func generateToolsDocs(tools []mcpserver.ServerTool) string {
	var buffer strings.Builder

	buffer.WriteString(toolsSectionStart + "\n\n| Tool | Description |\n|------|-------------|\n")

	for _, st := range tools {
		buffer.WriteString(fmt.Sprintf("| `%s` | %s |\n", st.Tool.Name, st.Tool.Description))
	}

	buffer.WriteString("\n---\n\n")

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
	err = os.WriteFile(readmePath, []byte(newContent), 0o600)
	if err != nil {
		log.Fatalf("Error writing README: %v", err)
	}

	fmt.Println("README updated successfully!")
}
