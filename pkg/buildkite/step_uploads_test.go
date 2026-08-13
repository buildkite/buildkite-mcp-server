package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type MockStepUploadsClient struct {
	ListByBuildFunc func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.StepUploadsListOptions) (buildkite.StepUploadsList, *buildkite.Response, error)
	GetFunc         func(ctx context.Context, org, pipelineSlug, buildNumber, uploadUUID string) (buildkite.StepUpload, *buildkite.Response, error)
}

func (m *MockStepUploadsClient) ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.StepUploadsListOptions) (buildkite.StepUploadsList, *buildkite.Response, error) {
	if m.ListByBuildFunc != nil {
		return m.ListByBuildFunc(ctx, org, pipelineSlug, buildNumber, opts)
	}
	return buildkite.StepUploadsList{}, nil, nil
}

func (m *MockStepUploadsClient) Get(ctx context.Context, org, pipelineSlug, buildNumber, uploadUUID string) (buildkite.StepUpload, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, pipelineSlug, buildNumber, uploadUUID)
	}
	return buildkite.StepUpload{}, nil, nil
}

var _ StepUploadsClient = (*MockStepUploadsClient)(nil)

func TestListStepUploads(t *testing.T) {
	assert := require.New(t)

	createdJobsCount := 2
	var gotOpts *buildkite.StepUploadsListOptions

	client := &MockStepUploadsClient{
		ListByBuildFunc: func(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.StepUploadsListOptions) (buildkite.StepUploadsList, *buildkite.Response, error) {
			gotOpts = opts
			return buildkite.StepUploadsList{
				Items: []buildkite.StepUpload{
					{
						UUID:             "upload-1",
						State:            "applied",
						Source:           "job",
						SourceJobID:      "48af33d8-2d4f-4f19-9a1c-e358c1e0f5ba",
						CreatedJobsCount: &createdJobsCount,
					},
				},
			}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{StepUploadsClient: client})

	tool, handler, scopes := ListStepUploads()
	assert.Equal("list_step_uploads", tool.Name)
	assert.Equal([]string{"read_builds"}, scopes)

	result, _, err := handler(ctx, &mcp.CallToolRequest{}, ListStepUploadsArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "42",
		SourceJobID:  "48af33d8-2d4f-4f19-9a1c-e358c1e0f5ba",
	})
	assert.NoError(err)

	assert.Equal("48af33d8-2d4f-4f19-9a1c-e358c1e0f5ba", gotOpts.SourceJobID)

	textResult := getTextResult(t, result)
	assert.Contains(textResult.Text, `"uuid": "upload-1"`)
	assert.Contains(textResult.Text, `"created_jobs_count": 2`)
	assert.NotContains(textResult.Text, "definition_yaml")
}

func TestGetStepUpload(t *testing.T) {
	assert := require.New(t)

	definitionYAML := "steps:\n- command: echo hello\n"
	definitionBytes := 4096
	omitted := false

	client := &MockStepUploadsClient{
		GetFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, uploadUUID string) (buildkite.StepUpload, *buildkite.Response, error) {
			return buildkite.StepUpload{
				UUID:                  uploadUUID,
				State:                 "applied",
				DefinitionBytes:       &definitionBytes,
				DefinitionYAML:        &definitionYAML,
				DefinitionYAMLOmitted: &omitted,
			}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{StepUploadsClient: client})

	tool, handler, scopes := GetStepUpload()
	assert.Equal("get_step_upload", tool.Name)
	assert.Equal([]string{"read_builds"}, scopes)

	result, _, err := handler(ctx, &mcp.CallToolRequest{}, GetStepUploadArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "42",
		UploadUUID:   "upload-1",
	})
	assert.NoError(err)

	textResult := getTextResult(t, result)
	assert.Contains(textResult.Text, "echo hello")
	assert.Contains(textResult.Text, `"definition_yaml_omitted": false`)
}

func TestGetStepUpload_OmittedDefinition(t *testing.T) {
	assert := require.New(t)

	definitionBytes := 38_000_000
	omitted := true

	client := &MockStepUploadsClient{
		GetFunc: func(ctx context.Context, org, pipelineSlug, buildNumber, uploadUUID string) (buildkite.StepUpload, *buildkite.Response, error) {
			return buildkite.StepUpload{
				UUID:                  uploadUUID,
				State:                 "applied",
				DefinitionBytes:       &definitionBytes,
				DefinitionYAMLOmitted: &omitted,
			}, &buildkite.Response{Response: &http.Response{StatusCode: 200}}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{StepUploadsClient: client})

	_, handler, _ := GetStepUpload()

	result, _, err := handler(ctx, &mcp.CallToolRequest{}, GetStepUploadArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "42",
		UploadUUID:   "upload-big",
	})
	assert.NoError(err)

	textResult := getTextResult(t, result)
	assert.Contains(textResult.Text, `"definition_yaml_omitted": true`)
	assert.Contains(textResult.Text, `"definition_bytes": 38000000`)
}
