package buildkite

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

// pipelineSchemaJSON is a pinned copy of the Buildkite pipeline JSON schema
// from https://github.com/buildkite/pipeline-schema. Embedding it keeps
// validation deterministic and fully offline; refresh it by re-downloading
// schema.json from that repository.
//
//go:embed resources/pipeline-schema.json
var pipelineSchemaJSON []byte

// maxValidationErrors bounds the number of schema errors returned so that a
// deeply broken pipeline (the schema makes heavy use of anyOf, which fans out
// into many leaf errors) cannot flood the agent's context.
const maxValidationErrors = 20

// maxCollectedValidationErrors bounds how many leaf errors are gathered while
// walking the validation error tree, so a pathological document cannot force
// a huge intermediate allocation before truncation. When collection stops at
// this cap, error_count reports the cap rather than the true total.
const maxCollectedValidationErrors = 200

// validationCaveats is returned with every result so an agent does not
// over-trust a passing schema check.
const validationCaveats = "Schema validation only: a valid result does not check runtime interpolation of environment variables, plugin configuration correctness, step key/dependency references, or pipeline steps generated dynamically at run time."

// compilePipelineSchema compiles the embedded schema exactly once.
var compilePipelineSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(pipelineSchemaJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to parse embedded pipeline schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("pipeline-schema.json", doc); err != nil {
		return nil, fmt.Errorf("failed to load embedded pipeline schema: %w", err)
	}

	schema, err := compiler.Compile("pipeline-schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to compile embedded pipeline schema: %w", err)
	}
	return schema, nil
})

type ValidatePipelineArgs struct {
	YAML string `json:"yaml" jsonschema:"The Buildkite pipeline YAML content to validate (e.g. the contents of .buildkite/pipeline.yml)"`
}

// PipelineValidationError is a single schema violation.
type PipelineValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidatePipelineResult is the structured result of a pipeline validation.
type ValidatePipelineResult struct {
	Valid      bool                      `json:"valid"`
	Errors     []PipelineValidationError `json:"errors,omitempty"`
	ErrorCount int                       `json:"error_count,omitempty"`
	Truncated  bool                      `json:"truncated,omitempty"`
	Caveats    string                    `json:"caveats"`
}

// collectLeafErrors walks the validation error tree and appends leaf causes,
// which carry the most specific violation messages. Collection stops once
// maxCollectedValidationErrors have been gathered.
func collectLeafErrors(ve *jsonschema.ValidationError, printer *message.Printer, out []PipelineValidationError) []PipelineValidationError {
	if len(out) >= maxCollectedValidationErrors {
		return out
	}
	if len(ve.Causes) == 0 {
		out = append(out, PipelineValidationError{
			Path:    "/" + strings.Join(ve.InstanceLocation, "/"),
			Message: ve.ErrorKind.LocalizedString(printer),
		})
		return out
	}
	for _, cause := range ve.Causes {
		out = collectLeafErrors(cause, printer, out)
	}
	return out
}

// invalidPipelineResult builds a failed validation result, truncating the
// error list to maxValidationErrors.
func invalidPipelineResult(errors []PipelineValidationError) ValidatePipelineResult {
	result := ValidatePipelineResult{
		Valid:      false,
		Errors:     errors,
		ErrorCount: len(errors),
		Caveats:    validationCaveats,
	}
	if len(errors) > maxValidationErrors {
		result.Errors = errors[:maxValidationErrors]
		result.Truncated = true
	}
	return result
}

// validatePipelineYAML validates pipeline YAML against the embedded schema.
// Syntax and schema violations are reported in the result; the error return
// is reserved for internal failures (e.g. a broken embedded schema).
func validatePipelineYAML(pipelineYAML string) (ValidatePipelineResult, error) {
	schema, err := compilePipelineSchema()
	if err != nil {
		return ValidatePipelineResult{}, err
	}

	if strings.TrimSpace(pipelineYAML) == "" {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: "pipeline is empty; expected a YAML document with a top-level steps list"},
		}), nil
	}

	const maxPipelineYAMLBytes = 1 << 20 // 1 MiB
	if len(pipelineYAML) > maxPipelineYAMLBytes {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("pipeline YAML is too large (%d bytes); maximum is %d bytes", len(pipelineYAML), maxPipelineYAMLBytes)},
		}), nil
	}
	// yaml.v3 bounds alias expansion during decode (it rejects documents
	// with excessive aliasing), so a small source document cannot
	// materialize an arbitrarily large structure (billion-laughs).
	var doc any
	if err := yaml.Unmarshal([]byte(pipelineYAML), &doc); err != nil {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("invalid YAML syntax: %v", err)},
		}), nil
	}

	// Round-trip through JSON to normalize YAML-specific types (integers,
	// timestamps, binary) into the JSON value types the validator expects.
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("pipeline is not representable as JSON: %v", err)},
		}), nil
	}

	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonBytes))
	if err != nil {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("invalid pipeline document: %v", err)},
		}), nil
	}

	if err := schema.Validate(instance); err != nil {
		ve, ok := err.(*jsonschema.ValidationError)
		if !ok {
			return ValidatePipelineResult{}, fmt.Errorf("unexpected validation failure: %w", err)
		}
		printer := message.NewPrinter(language.English)
		return invalidPipelineResult(collectLeafErrors(ve, printer, nil)), nil
	}

	return ValidatePipelineResult{Valid: true, Caveats: validationCaveats}, nil
}

func ValidatePipeline() (mcp.Tool, mcp.ToolHandlerFor[ValidatePipelineArgs, any], []string) {
	return mcp.Tool{
			Name:        "validate_pipeline",
			Description: "Validate Buildkite pipeline YAML against the official pipeline schema without calling the Buildkite API. Use this to check a pipeline definition (e.g. .buildkite/pipeline.yml) before creating or updating a pipeline, or before committing pipeline changes",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Validate Pipeline YAML",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ValidatePipelineArgs) (*mcp.CallToolResult, any, error) {
			_, span := trace.Start(ctx, "buildkite.ValidatePipeline")
			defer span.End()

			span.SetAttributes(attribute.Int("yaml_bytes", len(args.YAML)))

			result, err := validatePipelineYAML(args.YAML)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("pipeline validation failed: %v", err)), nil, nil
			}

			span.SetAttributes(
				attribute.Bool("valid", result.Valid),
				attribute.Int("error_count", result.ErrorCount),
			)

			return mcpTextResult(span, &result)
		}, []string{}
}
