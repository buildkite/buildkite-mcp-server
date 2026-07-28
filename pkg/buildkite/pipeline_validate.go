package buildkite

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
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

// Input limits enforced before a document is decoded or validated. The YAML
// node graph represents aliases as references, so the expansion budgets are
// enforced by walking the graph and charging each alias the full cost of its
// target — before any expansion is materialized. The raw source cap alone is
// not enough: aliases let a kilobytes-sized document decode into an
// arbitrarily large value (billion-laughs), and the schema validator builds
// its complete error tree in memory before it can be truncated, so oversized
// documents must be rejected up front.
const (
	// maxPipelineYAMLBytes bounds the raw YAML source accepted.
	maxPipelineYAMLBytes = 1 << 20 // 1 MiB

	// maxExpandedYAMLNodes bounds how many values the document may
	// materialize once anchors and aliases are resolved. Generous for real
	// pipelines: a maximal 500-step pipeline with dense per-step
	// configuration is on the order of 50-100 values per step.
	maxExpandedYAMLNodes = 50_000

	// maxExpandedYAMLScalarBytes bounds the total scalar bytes the document
	// may materialize once anchors and aliases are resolved, so a large
	// anchored scalar cannot be multiplied by aliasing.
	maxExpandedYAMLScalarBytes = 4 << 20 // 4 MiB

	// maxExpandedYAMLDepth bounds nesting depth (following aliases), keeping
	// the complexity walk and YAML decode recursion shallow.
	maxExpandedYAMLDepth = 100

	// maxPipelineSteps mirrors Buildkite's default service quota of 500 jobs
	// created per pipeline upload. Larger pipelines get a warning (the quota
	// can be raised per organization), not a validation failure.
	maxPipelineSteps = 500

	// maxValidationUnitValues bounds how many values a single
	// schema-validation call may cover: one step, one step nested in a
	// group, or the pipeline's configuration outside the steps list. The
	// validator builds its complete error tree in memory before it can be
	// truncated, and the pipeline schema's anyOf fan-out multiplies that
	// tree for every invalid value, so each validated unit must be small.
	// Real steps are two orders of magnitude below this limit.
	maxValidationUnitValues = 2_000
)

// validationCaveats is returned with every result so an agent does not
// over-trust a passing schema check.
const validationCaveats = "Schema validation only: a valid result does not check runtime interpolation of environment variables, plugin configuration correctness, step key/dependency references, or pipeline steps generated dynamically at run time."

// pipelineSchemas holds the compiled root schema plus the step subschemas
// used to validate steps one at a time.
type pipelineSchemas struct {
	// root validates a complete pipeline document.
	root *jsonschema.Schema
	// step validates a single entry of the top-level steps list.
	step *jsonschema.Schema
	// groupChild validates a single step nested inside a group step.
	groupChild *jsonschema.Schema
}

// compilePipelineSchemas compiles the embedded schema exactly once.
var compilePipelineSchemas = sync.OnceValues(func() (*pipelineSchemas, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(pipelineSchemaJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to parse embedded pipeline schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("pipeline-schema.json", doc); err != nil {
		return nil, fmt.Errorf("failed to load embedded pipeline schema: %w", err)
	}

	schemas := &pipelineSchemas{}
	for _, s := range []struct {
		target  **jsonschema.Schema
		pointer string
	}{
		{&schemas.root, "pipeline-schema.json"},
		{&schemas.step, "pipeline-schema.json#/definitions/pipelineSteps/items"},
		{&schemas.groupChild, "pipeline-schema.json#/definitions/groupSteps/items"},
	} {
		schema, err := compiler.Compile(s.pointer)
		if err != nil {
			return nil, fmt.Errorf("failed to compile embedded pipeline schema %q: %w", s.pointer, err)
		}
		*s.target = schema
	}
	return schemas, nil
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
	Warnings   []string                  `json:"warnings,omitempty"`
	Caveats    string                    `json:"caveats"`
}

// collectLeafErrors walks the validation error tree and appends leaf causes,
// which carry the most specific violation messages, rebasing each instance
// location onto prefix. Collection stops once maxCollectedValidationErrors
// have been gathered.
func collectLeafErrors(ve *jsonschema.ValidationError, prefix []string, printer *message.Printer, out []PipelineValidationError) []PipelineValidationError {
	if len(out) >= maxCollectedValidationErrors {
		return out
	}
	if len(ve.Causes) == 0 {
		location := make([]string, 0, len(prefix)+len(ve.InstanceLocation))
		location = append(location, prefix...)
		location = append(location, ve.InstanceLocation...)
		out = append(out, PipelineValidationError{
			Path:    "/" + strings.Join(location, "/"),
			Message: ve.ErrorKind.LocalizedString(printer),
		})
		return out
	}
	for _, cause := range ve.Causes {
		out = collectLeafErrors(cause, prefix, printer, out)
	}
	return out
}

// exceedsValueBudget reports whether the decoded JSON value contains more
// than *budget values, decrementing the budget as it walks and returning as
// soon as the budget is exhausted.
func exceedsValueBudget(v any, budget *int) bool {
	*budget--
	if *budget < 0 {
		return true
	}
	switch t := v.(type) {
	case map[string]any:
		for _, child := range t {
			if exceedsValueBudget(child, budget) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if exceedsValueBudget(child, budget) {
				return true
			}
		}
	}
	return false
}

// validateUnit validates one bounded portion of the pipeline document
// against a schema, appending its leaf errors to out with paths rebased onto
// prefix. The validator builds a unit's complete error tree in memory before
// it can be truncated, so units larger than maxValidationUnitValues are not
// validated at all: they fail with an explicit local-safety-limit message
// instead. Once maxCollectedValidationErrors have been gathered, remaining
// units are skipped entirely.
func validateUnit(schema *jsonschema.Schema, instance any, prefix []string, printer *message.Printer, out []PipelineValidationError) ([]PipelineValidationError, error) {
	if len(out) >= maxCollectedValidationErrors {
		return out, nil
	}
	budget := maxValidationUnitValues
	if exceedsValueBudget(instance, &budget) {
		return append(out, PipelineValidationError{
			Path:    "/" + strings.Join(prefix, "/"),
			Message: fmt.Sprintf("this section contains more than %d values and was not validated; this is a local safety limit of the validation tool, not a Buildkite limit", maxValidationUnitValues),
		}), nil
	}
	err := schema.Validate(instance)
	if err == nil {
		return out, nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return out, fmt.Errorf("unexpected validation failure: %w", err)
	}
	return collectLeafErrors(ve, prefix, printer, out), nil
}

// placeholderSteps returns a minimal valid steps list, substituted for the
// real steps when validating a pipeline's (or group's) own configuration so
// that each validated unit stays within the per-unit budget. "wait" is valid
// in both the top-level steps list and a group's steps list.
func placeholderSteps() []any { return []any{"wait"} }

// validatePipelineInstance validates the pipeline in bounded units: first
// the top-level configuration with the steps list replaced by a placeholder,
// then each step on its own, splitting group steps into their shell and
// individual children. Validating unit by unit keeps every schema.Validate
// error tree small even when the document as a whole holds tens of thousands
// of invalid values, and lets collection stop early once enough errors have
// been gathered.
func validatePipelineInstance(schemas *pipelineSchemas, instance any, printer *message.Printer) ([]PipelineValidationError, error) {
	root, isMap := instance.(map[string]any)
	steps, hasSteps := root["steps"].([]any)
	if !isMap || !hasSteps {
		// Malformed at the top level (not a map, or steps is missing or not
		// a list); validate as a single unit.
		return validateUnit(schemas.root, instance, nil, printer, nil)
	}

	shell := make(map[string]any, len(root))
	for k, v := range root {
		shell[k] = v
	}
	shell["steps"] = placeholderSteps()
	out, err := validateUnit(schemas.root, shell, nil, printer, nil)
	if err != nil {
		return nil, err
	}

	for i, step := range steps {
		prefix := []string{"steps", strconv.Itoa(i)}
		group, isGroupLike := step.(map[string]any)
		children, hasChildren := group["steps"].([]any)
		if !isGroupLike || !hasChildren {
			out, err = validateUnit(schemas.step, step, prefix, printer, out)
			if err != nil {
				return nil, err
			}
			continue
		}

		groupShell := make(map[string]any, len(group))
		for k, v := range group {
			groupShell[k] = v
		}
		groupShell["steps"] = placeholderSteps()
		out, err = validateUnit(schemas.step, groupShell, prefix, printer, out)
		if err != nil {
			return nil, err
		}
		for j, child := range children {
			childPrefix := []string{"steps", strconv.Itoa(i), "steps", strconv.Itoa(j)}
			out, err = validateUnit(schemas.groupChild, child, childPrefix, printer, out)
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// yamlComplexity accumulates the cost of a YAML document as if every alias
// were expanded, without materializing the expansion. Costs of completed
// subtrees are memoized, so the walk is linear in the size of the source
// document and fails fast once a budget is exceeded.
type yamlComplexity struct {
	nodes       int
	scalarBytes int
	memo        map[*yaml.Node]yamlCost
	active      map[*yaml.Node]bool
}

type yamlCost struct {
	nodes       int
	scalarBytes int
}

// checkYAMLComplexity rejects documents whose alias-expanded form exceeds
// the expansion budgets, before the document is decoded.
func checkYAMLComplexity(root *yaml.Node) error {
	c := &yamlComplexity{
		memo:   make(map[*yaml.Node]yamlCost),
		active: make(map[*yaml.Node]bool),
	}
	return c.walk(root, 0)
}

func (c *yamlComplexity) walk(n *yaml.Node, depth int) error {
	if depth > maxExpandedYAMLDepth {
		return fmt.Errorf("pipeline YAML is nested more than %d levels deep", maxExpandedYAMLDepth)
	}
	if cost, ok := c.memo[n]; ok {
		c.nodes += cost.nodes
		c.scalarBytes += cost.scalarBytes
		return c.checkBudgets()
	}
	if c.active[n] {
		return fmt.Errorf("pipeline YAML contains a self-referential alias")
	}
	c.active[n] = true
	defer delete(c.active, n)

	startNodes, startBytes := c.nodes, c.scalarBytes
	c.nodes++
	switch n.Kind {
	case yaml.ScalarNode:
		c.scalarBytes += len(n.Value)
	case yaml.AliasNode:
		if n.Alias != nil {
			if err := c.walk(n.Alias, depth+1); err != nil {
				return err
			}
		}
	default:
		for _, child := range n.Content {
			if err := c.walk(child, depth+1); err != nil {
				return err
			}
		}
	}
	if err := c.checkBudgets(); err != nil {
		return err
	}
	c.memo[n] = yamlCost{nodes: c.nodes - startNodes, scalarBytes: c.scalarBytes - startBytes}
	return nil
}

func (c *yamlComplexity) checkBudgets() error {
	if c.nodes > maxExpandedYAMLNodes {
		return fmt.Errorf("pipeline YAML is too complex to validate: it expands to more than %d values once anchors and aliases are resolved", maxExpandedYAMLNodes)
	}
	if c.scalarBytes > maxExpandedYAMLScalarBytes {
		return fmt.Errorf("pipeline YAML is too complex to validate: it expands to more than %d bytes of scalar data once anchors and aliases are resolved", maxExpandedYAMLScalarBytes)
	}
	return nil
}

// countPipelineSteps counts entries in the pipeline's steps list, including
// steps nested inside group steps. Counting stops as soon as the total
// exceeds maxPipelineSteps.
func countPipelineSteps(doc any) int {
	root, ok := doc.(map[string]any)
	if !ok {
		return 0
	}
	steps, ok := root["steps"].([]any)
	if !ok {
		return 0
	}
	return countStepEntries(steps, 0)
}

func countStepEntries(steps []any, count int) int {
	for _, step := range steps {
		count++
		if count > maxPipelineSteps {
			return count
		}
		if m, ok := step.(map[string]any); ok {
			if nested, ok := m["steps"].([]any); ok {
				count = countStepEntries(nested, count)
				if count > maxPipelineSteps {
					return count
				}
			}
		}
	}
	return count
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
	if strings.TrimSpace(pipelineYAML) == "" {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: "pipeline is empty; expected a YAML document with a top-level steps list"},
		}), nil
	}

	if len(pipelineYAML) > maxPipelineYAMLBytes {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("pipeline YAML is too large (%d bytes); maximum is %d bytes", len(pipelineYAML), maxPipelineYAMLBytes)},
		}), nil
	}

	// Parse into a node graph first: aliases stay as references there, so
	// the graph stays proportional to the source and the document's
	// expanded size can be bounded before anything is materialized.
	decoder := yaml.NewDecoder(strings.NewReader(pipelineYAML))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return invalidPipelineResult([]PipelineValidationError{
				{Path: "/", Message: "pipeline is empty; expected a YAML document with a top-level steps list"},
			}), nil
		}
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("invalid YAML syntax: %v", err)},
		}), nil
	}
	if err := decoder.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: "pipeline contains multiple YAML documents; expected a single document"},
		}), nil
	}

	if err := checkYAMLComplexity(&root); err != nil {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: err.Error()},
		}), nil
	}

	var doc any
	if err := root.Decode(&doc); err != nil {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("invalid YAML: %v", err)},
		}), nil
	}

	// Exceeding the default upload quota is a warning, not a schema
	// violation: the quota can be raised per organization, so the pipeline
	// may still be accepted by Buildkite.
	var warnings []string
	if count := countPipelineSteps(doc); count > maxPipelineSteps {
		warnings = append(warnings, fmt.Sprintf("pipeline defines more than %d steps (including steps nested in groups); Buildkite's default service quota is %d jobs per pipeline upload, so uploading this pipeline may be rejected unless the organization's quota has been raised", maxPipelineSteps, maxPipelineSteps))
	}

	// Round-trip through JSON to normalize YAML-specific types (integers,
	// timestamps, binary) into the JSON value types the validator expects.
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("pipeline is not representable as JSON: %v", err)},
		}), nil
	}

	schemas, err := compilePipelineSchemas()
	if err != nil {
		return ValidatePipelineResult{}, err
	}

	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonBytes))
	if err != nil {
		return invalidPipelineResult([]PipelineValidationError{
			{Path: "/", Message: fmt.Sprintf("invalid pipeline document: %v", err)},
		}), nil
	}

	printer := message.NewPrinter(language.English)
	validationErrors, err := validatePipelineInstance(schemas, instance, printer)
	if err != nil {
		return ValidatePipelineResult{}, err
	}
	if len(validationErrors) > 0 {
		result := invalidPipelineResult(validationErrors)
		result.Warnings = warnings
		return result, nil
	}

	return ValidatePipelineResult{Valid: true, Warnings: warnings, Caveats: validationCaveats}, nil
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
