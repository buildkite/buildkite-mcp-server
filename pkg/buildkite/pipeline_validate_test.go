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

	t.Run("more than 500 steps is a warning, not an error", func(t *testing.T) {
		assert := require.New(t)

		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < maxPipelineSteps; i++ {
			fmt.Fprintf(&sb, "  - command: echo %d\n", i)
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.True(result.Valid)
		assert.Empty(result.Warnings)

		// The default service quota is 500 jobs per upload, but it can be
		// raised per organization, so a larger pipeline is still valid.
		sb.WriteString("  - command: echo one too many\n")
		result, err = validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.True(result.Valid)
		assert.Empty(result.Errors)
		assert.Len(result.Warnings, 1)
		assert.Contains(result.Warnings[0], "default service quota")
	})

	t.Run("steps nested in groups count toward the step quota warning", func(t *testing.T) {
		assert := require.New(t)

		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&sb, "  - group: group-%d\n    steps:\n", i)
			fmt.Fprintf(&sb, "      - command: echo a\n      - command: echo b\n")
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.True(result.Valid)
		assert.Len(result.Warnings, 1)
		assert.Contains(result.Warnings[0], "more than 500 steps")
	})

	t.Run("mass invalid steps stop error collection early", func(t *testing.T) {
		assert := require.New(t)

		// 10,000 invalid steps previously allocated >100 MB inside the
		// schema validator building its error tree in a single Validate
		// call. Per-step validation stops after
		// maxCollectedValidationErrors leaf errors have been gathered.
		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < 10_000; i++ {
			fmt.Fprintf(&sb, "  - fake_property_%d: 1\n", i)
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.True(result.Truncated)
		assert.Len(result.Errors, maxValidationErrors)
		assert.Equal(maxCollectedValidationErrors, result.ErrorCount)
		assert.Len(result.Warnings, 1, "should still warn about the step quota")
	})

	t.Run("a single step with mass invalid entries hits the local validation limit", func(t *testing.T) {
		assert := require.New(t)

		// One step with 40,000 invalid notify entries previously reached
		// ~345 MB RSS inside a single schema.Validate call. The per-unit
		// value budget must refuse to validate the step instead.
		var sb strings.Builder
		sb.WriteString("steps:\n  - command: echo hello\n    notify:\n")
		for i := 0; i < 40_000; i++ {
			fmt.Fprintf(&sb, "      - bogus%d\n", i)
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Equal("/steps/0", result.Errors[0].Path)
		assert.Contains(result.Errors[0].Message, "local safety limit")
		assert.Contains(result.Errors[0].Message, "not a Buildkite limit")
	})

	t.Run("mass invalid pipeline-level entries hit the local validation limit", func(t *testing.T) {
		assert := require.New(t)

		var sb strings.Builder
		sb.WriteString("notify:\n")
		for i := 0; i < 40_000; i++ {
			fmt.Fprintf(&sb, "  - bogus%d\n", i)
		}
		sb.WriteString("steps:\n  - command: echo hello\n")
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Equal("/", result.Errors[0].Path)
		assert.Contains(result.Errors[0].Message, "local safety limit")
	})

	t.Run("a group child with mass invalid entries hits the local validation limit", func(t *testing.T) {
		assert := require.New(t)

		var sb strings.Builder
		sb.WriteString("steps:\n  - group: g\n    steps:\n      - command: echo hello\n        notify:\n")
		for i := 0; i < 40_000; i++ {
			fmt.Fprintf(&sb, "          - bogus%d\n", i)
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.False(result.Valid)
		assert.Len(result.Errors, 1)
		assert.Equal("/steps/0/steps/0", result.Errors[0].Path)
		assert.Contains(result.Errors[0].Message, "local safety limit")
	})

	t.Run("large valid groups are validated per child", func(t *testing.T) {
		assert := require.New(t)

		// A single group with many valid children must not trip the
		// per-unit budget, because each child is its own unit.
		var sb strings.Builder
		sb.WriteString("steps:\n  - group: big\n    steps:\n")
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&sb, "      - label: step-%d\n        command: echo %d\n        agents:\n          queue: default\n", i, i)
		}
		result, err := validatePipelineYAML(sb.String())
		assert.NoError(err)
		assert.True(result.Valid)
		assert.Empty(result.Warnings)
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

	// The maximum quota-sized pipeline with every step invalid: per-step
	// validation with early stop bounds the total error-tree allocation.
	maxInvalidSteps := func() string {
		var sb strings.Builder
		sb.WriteString("steps:\n")
		for i := 0; i < maxPipelineSteps; i++ {
			fmt.Fprintf(&sb, "  - fake_property_%d: 1\n", i)
		}
		return sb.String()
	}()

	// One step with 40,000 invalid notify entries: a ~520 KB document that
	// previously reached ~345 MB RSS (~484 MB allocated) inside a single
	// schema.Validate call. The per-unit value budget must refuse to
	// validate the step, keeping allocation at parse cost only.
	massNotifyStep := func() string {
		var sb strings.Builder
		sb.WriteString("steps:\n  - command: echo hello\n    notify:\n")
		for i := 0; i < 40_000; i++ {
			fmt.Fprintf(&sb, "      - bogus%d\n", i)
		}
		return sb.String()
	}()

	for name, doc := range map[string]string{
		"alias_bomb_rejected":          aliasBomb,
		"mass_invalid_steps_truncated": massInvalidSteps,
		"max_invalid_steps_validated":  maxInvalidSteps,
		"mass_notify_step_local_limit": massNotifyStep,
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
