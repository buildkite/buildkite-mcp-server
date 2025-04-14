package joblogs

import (
	"fmt"
	"strings"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/buildkite/terminal-to-html/v3"
	"github.com/huantt/plaintext-extractor"
)

// Process accepts job logs from the Buildkite API and strips out formatting
// to reduce the number of tokens sent to the LLM
func Process(jobLog buildkite.JobLog) (string, error) {
	screen, err := terminal.NewScreen()
	if err != nil {
		return "", fmt.Errorf("failed to create terminal screen: %w", err)
	}

	_, err = screen.Write([]byte(jobLog.Content))
	if err != nil {
		return "", fmt.Errorf("failed to write to terminal screen: %w", err)
	}
	html := screen.AsHTML()

	output := strings.Builder{}

	extractor := plaintext.NewHtmlExtractor()
	for line := range strings.Lines(html) {
		plainText, err := extractor.PlainText(line)
		if err != nil {
			return "", fmt.Errorf("failed to extract plain text: %w", err)
		}
		output.WriteString(*plainText + "\n")
	}
	return output.String(), nil
}
