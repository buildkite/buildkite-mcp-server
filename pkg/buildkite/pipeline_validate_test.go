package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePipelineToolDefinition(t *testing.T) {
	assert := require.New(t)

	tool, handler, scopes := ValidatePipeline()
	assert.NotNil(handler)

	assert.Equal("validate_pipeline", tool.Name)
	assert.Contains(tool.Description, "pipeline schema")
	assert.True(tool.Annotations.ReadOnlyHint)
	assert.Empty(scopes, "validate_pipeline is local and must not require API scopes")
}

func TestValidatePipelineYAML(t *testing.T) {
	t.Run("valid pipeline", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML(`
steps:
  - label: ":go: test"
    command: go test ./...
    agents:
      queue: default
  - wait
  - label: deploy
    command: ./deploy.sh
    branches: main
`)
		assert.NoError(err)
		assert.True(result.Valid)
		assert.Empty(result.Errors)
		assert.NotEmpty(result.Caveats)
	})

	t.Run("valid pipeline with env and group", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML(`
env:
  FOO: bar
steps:
  - group: ":test_tube: tests"
    steps:
      - label: unit
        command: make test
`)
		assert.NoError(err)
		assert.True(result.Valid)
	})

	t.Run("missing steps", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML(`
env:
  FOO: bar
`)
		assert.NoError(err)
		assert.False(result.Valid)
		assert.NotEmpty(result.Errors)
	})

	t.Run("steps has wrong type", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML(`steps: "not a list"`)
		assert.NoError(err)
		assert.False(result.Valid)
		assert.NotEmpty(result.Errors)
		assert.Equal("/steps", result.Errors[0].Path)
	})

	t.Run("invalid step property", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML(`
steps:
  - label: test
    command: make test
    retry:
      automatic:
        limit: "not a number"
`)
		assert.NoError(err)
		assert.False(result.Valid)
		assert.NotEmpty(result.Errors)
	})

	t.Run("empty input", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML("   \n")
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "empty")
	})

	t.Run("invalid YAML syntax", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML("steps:\n  - label: [unclosed")
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "YAML")
	})

	t.Run("error list is truncated", func(t *testing.T) {
		assert := require.New(t)

		// Build a pipeline with many invalid steps to fan out leaf errors
		// past maxValidationErrors.
		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < 30; i++ {
			fmt.Fprintf(&sb, "  - not_a_real_property_%d: true\n", i)
		}

		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.True(result.Truncated)
		assert.Len(result.Errors, maxValidationErrors)
		assert.Greater(result.ErrorCount, maxValidationErrors)
	})
}

func TestValidatePipelineHandler(t *testing.T) {
	assert := require.New(t)

	_, handler, _ := ValidatePipeline()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(context.Background(), request, ValidatePipelineArgs{
		YAML: "steps:\n  - command: echo hello\n",
	})
	assert.NoError(err)
	assert.False(result.IsError)

	textContent := getTextResult(t, result)

	var parsed ValidatePipelineResult
	assert.NoError(json.Unmarshal([]byte(textContent.Text), &parsed))
	assert.True(parsed.Valid)
	assert.NotEmpty(parsed.Caveats)
}
