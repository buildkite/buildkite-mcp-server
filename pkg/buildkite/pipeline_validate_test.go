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

	t.Run("legitimate anchors and aliases are allowed", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML(`
steps:
  - label: test
    command: go test ./...
    plugins: &plugins
      - docker#v5.0.0:
          image: golang:1.24
  - label: lint
    command: make lint
    plugins: *plugins
`)
		assert.NoError(err)
		assert.True(result.Valid)
	})

	t.Run("merge keys are allowed", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML(`
defaults: &defaults
  agents:
    queue: default
  timeout_in_minutes: 10
steps:
  - <<: *defaults
    label: test
    command: go test ./...
`)
		assert.NoError(err)
		assert.True(result.Valid)
	})

	t.Run("nested alias expansion is rejected", func(t *testing.T) {
		assert := require.New(t)

		// Billion-laughs style document: ~200 bytes of source that would
		// materialize to over 10 MB if aliases expanded unboundedly, with
		// each additional level multiplying the size by 10. It must be
		// rejected by the pre-decode complexity check, before expansion.
		result, err := validatePipelineYAML(`
a: &a ["x","x","x","x","x","x","x","x","x","x"]
b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a,*a]
c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b,*b]
d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c,*c]
e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d,*d]
steps: [*e]
`)
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "too complex")
	})

	t.Run("wide alias expansion below the yaml.v3 heuristic is rejected", func(t *testing.T) {
		assert := require.New(t)

		// A ~7 KB document that yaml.v3's alias heuristic accepts but that
		// materializes ~180K values: an anchor holding 1,000 map entries,
		// referenced 90 times.
		var sb strings.Builder
		sb.WriteString("anchor: &big\n")
		for i := 0; i < 1000; i++ {
			fmt.Fprintf(&sb, "  k%d: v\n", i)
		}
		sb.WriteString("steps:\n")
		for i := 0; i < 90; i++ {
			sb.WriteString("  - *big\n")
		}

		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "too complex")
	})

	t.Run("aliased large scalar expansion is rejected", func(t *testing.T) {
		assert := require.New(t)

		// A 200 KB anchored script aliased 30 times materializes ~6 MB of
		// scalar data from a well-under-1-MiB source document.
		script := strings.Repeat("x", 200*1024)
		var sb strings.Builder
		fmt.Fprintf(&sb, "script: &script \"%s\"\nsteps:\n", script)
		for i := 0; i < 30; i++ {
			sb.WriteString("  - command: *script\n")
		}

		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "scalar data")
	})

	t.Run("self-referential alias is rejected", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML("a: &a [*a]\nsteps: []\n")
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "self-referential")
	})

	t.Run("excessive nesting depth is rejected", func(t *testing.T) {
		assert := require.New(t)

		doc := "steps: " + strings.Repeat("[", 150) + strings.Repeat("]", 150)
		result, err := validatePipelineYAML(doc)
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "levels deep")
	})

	t.Run("multiple YAML documents are rejected", func(t *testing.T) {
		assert := require.New(t)

		result, err := validatePipelineYAML("steps: []\n---\nsteps: []\n")
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Contains(result.Errors[0].Message, "multiple YAML documents")
	})

	t.Run("at most 500 steps are accepted", func(t *testing.T) {
		assert := require.New(t)

		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < maxPipelineSteps; i++ {
			fmt.Fprintf(&sb, "  - command: echo %d\n", i)
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.True(result.Valid)

		sb.WriteString("  - command: echo one too many\n")
		result, err = validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Equal("/steps", result.Errors[0].Path)
		assert.Contains(result.Errors[0].Message, "more than 500 steps")
	})

	t.Run("steps nested in groups count toward the step limit", func(t *testing.T) {
		assert := require.New(t)

		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&sb, "  - group: group-%d\n    steps:\n", i)
			fmt.Fprintf(&sb, "      - command: echo a\n      - command: echo b\n")
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Equal("/steps", result.Errors[0].Path)
	})

	t.Run("mass invalid steps are rejected before schema validation", func(t *testing.T) {
		assert := require.New(t)

		// 10,000 invalid steps previously allocated >100 MB inside the
		// schema validator building its error tree; the step-count guard
		// must reject the document before validation starts.
		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < 10_000; i++ {
			fmt.Fprintf(&sb, "  - fake_property_%d: 1\n", i)
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Equal("/steps", result.Errors[0].Path)
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

// BenchmarkValidatePipelineAdversarial documents the allocation profile of
// adversarial inputs. Run with -benchmem: the rejected documents must stay in
// the low-megabyte range because they are refused before decode/validation,
// and the worst accepted case bounds the validator's error fan-out.
func BenchmarkValidatePipelineAdversarial(b *testing.B) {
	aliasBomb := func() string {
		var sb strings.Builder
		sb.WriteString("anchor: &big\n")
		for i := 0; i < 1000; i++ {
			fmt.Fprintf(&sb, "  k%d: v\n", i)
		}
		sb.WriteString("steps:\n")
		for i := 0; i < 90; i++ {
			sb.WriteString("  - *big\n")
		}
		return sb.String()
	}()

	massInvalidSteps := func() string {
		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < 10_000; i++ {
			fmt.Fprintf(&sb, "  - fake_property_%d: 1\n", i)
		}
		return sb.String()
	}()

	// Worst accepted case: the maximum number of steps, all invalid, so the
	// schema validator builds its largest permissible error tree.
	maxInvalidSteps := func() string {
		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < maxPipelineSteps; i++ {
			fmt.Fprintf(&sb, "  - fake_property_%d: 1\n", i)
		}
		return sb.String()
	}()

	for name, doc := range map[string]string{
		"alias_bomb_rejected":         aliasBomb,
		"mass_invalid_steps_rejected": massInvalidSteps,
		"max_invalid_steps_validated": maxInvalidSteps,
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				result, err := validatePipelineYAML(doc)
				if err != nil {
					b.Fatal(err)
				}
				if result.Valid {
					b.Fatal("expected invalid result")
				}
			}
		})
	}
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
