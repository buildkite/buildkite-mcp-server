package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockPipelinesClient struct {
	GetFunc        func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error)
	ListFunc       func(ctx context.Context, org string, opt *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error)
	CreateFunc     func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	UpdateFunc     func(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	AddWebhookFunc func(ctx context.Context, org string, slug string) (*buildkite.Response, error)
}

func (m *MockPipelinesClient) Get(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, pipeline)
	}
	return buildkite.Pipeline{}, nil, nil
}

func (m *MockPipelinesClient) List(ctx context.Context, org string, opt *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, opt)
	}
	return nil, nil, nil
}

func (m *MockPipelinesClient) Create(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, org, p)
	}
	return buildkite.Pipeline{}, nil, nil
}

func (m *MockPipelinesClient) Update(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, org, pipeline, p)
	}
	return buildkite.Pipeline{}, nil, nil
}

func (m *MockPipelinesClient) AddWebhook(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
	if m.AddWebhookFunc != nil {
		return m.AddWebhookFunc(ctx, org, slug)
	}
	return &buildkite.Response{Response: &http.Response{StatusCode: 201}}, nil
}

func (m *MockPipelinesClient) GetGitHubWebhookProcessing(context.Context, string, string) (buildkite.GitHubWebhookProcessing, *buildkite.Response, error) {
	return buildkite.GitHubWebhookProcessing{}, nil, errors.New("unexpected processing state call")
}

func (m *MockPipelinesClient) EnableGitHubWebhookProcessing(context.Context, string, string) (buildkite.GitHubWebhookProcessing, *buildkite.Response, error) {
	return buildkite.GitHubWebhookProcessing{}, nil, errors.New("unexpected processing enable call")
}

func (m *MockPipelinesClient) DisableGitHubWebhookProcessing(context.Context, string, string) (buildkite.GitHubWebhookProcessing, *buildkite.Response, error) {
	return buildkite.GitHubWebhookProcessing{}, nil, errors.New("unexpected processing disable call")
}

var _ PipelinesClient = (*MockPipelinesClient)(nil)

func TestListPipelines(t *testing.T) {
	assert := require.New(t)

	client := &MockPipelinesClient{
		ListFunc: func(ctx context.Context, org string, opt *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error) {
			return []buildkite.Pipeline{
				{
					ID:        "123",
					Slug:      "test-pipeline",
					Name:      "Test Pipeline",
					CreatedAt: &buildkite.Timestamp{},
				},
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	tool, handler, _ := ListPipelines()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := ListPipelinesArgs{
		OrgSlug: "org",
	}

	result, _, err := handler(ctx, request, args)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"123","name":"Test Pipeline","slug":"test-pipeline","repository":"","default_branch":"","web_url":"","visibility":"","created_at":"0001-01-01T00:00:00Z"}]}`, textContent.Text)
}

func TestGetPipeline(t *testing.T) {
	assert := require.New(t)

	client := &MockPipelinesClient{
		GetFunc: func(ctx context.Context, org string, pipeline string) (buildkite.Pipeline, *buildkite.Response, error) {
			return buildkite.Pipeline{
				ID:        "123",
				Slug:      "test-pipeline",
				Name:      "Test Pipeline",
				CreatedAt: &buildkite.Timestamp{},
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	tool, handler, _ := GetPipeline()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := GetPipelineArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
	}

	result, _, err := handler(ctx, request, args)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.JSONEq(`{"id":"123","name":"Test Pipeline","slug":"test-pipeline","created_at":"0001-01-01T00:00:00Z","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"provider":{"id":"","webhook_url":"","settings":null}}`, textContent.Text)
}

func TestPipelineProviderReadbackThroughMCP(t *testing.T) {
	for _, tt := range []struct{ name, settings, template string }{
		{"asymmetric", `{"github_actions_mode":true,"github_workflow_access_tokens_enabled":false,"ignore_default_branch_pull_requests":true,"skip_builds_for_closed_pull_requests":false,"skip_builds_for_existing_commits":true,"publish_commit_status":false,"build_branches":true,"build_pull_requests":false,"build_tags":true,"filter_enabled":false,"filter_condition":"build.branch != 'release'","pull_request_branch_filter_enabled":true,"pull_request_branch_filter_configuration":"main release/*","skip_pull_request_builds_for_existing_commits":false,"trigger_mode":"none"}`, `"00000000-0000-4000-8000-000000000001"`},
		{"inverse", `{"github_actions_mode":false,"github_workflow_access_tokens_enabled":true,"ignore_default_branch_pull_requests":false,"skip_builds_for_closed_pull_requests":true,"skip_builds_for_existing_commits":false,"publish_commit_status":true,"trigger_mode":"code"}`, `null`},
		{"ordinary", `{"publish_commit_status":true,"trigger_mode":"code"}`, ""},
	} {
		for _, toolName := range []string{"get_pipeline", "list_pipelines"} {
			t.Run(toolName+"/"+tt.name, func(t *testing.T) {
				template := ""
				if tt.template != "" {
					template = `"pipeline_template_uuid":` + tt.template + `,`
				}
				pipeline := `{` + template + `"slug":"synthetic","branch_configuration":"main","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"provider":{"id":"github","settings":` + tt.settings + `}}`
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodGet, r.Method)
					w.Header().Set("Content-Type", "application/json")
					if toolName == "get_pipeline" {
						assert.Equal(t, "/v2/organizations/acme/pipelines/synthetic", r.URL.Path)
						_, _ = w.Write([]byte(pipeline))
					} else {
						assert.Equal(t, "/v2/organizations/acme/pipelines", r.URL.Path)
						_, _ = w.Write([]byte("[" + pipeline + "]"))
					}
				}))
				t.Cleanup(srv.Close)
				client := pipelineMCPClient(t, srv.URL)
				args := map[string]any{"org_slug": "acme", "detail_level": "full"}
				if toolName == "get_pipeline" {
					args["pipeline_slug"] = "synthetic"
				}
				result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: toolName, Arguments: args})
				require.NoError(t, err)
				require.False(t, result.IsError)
				var output map[string]any
				require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &output))
				if toolName == "list_pipelines" {
					output = output["items"].([]any)[0].(map[string]any)
				}
				require.Equal(t, "main", output["branch_configuration"])
				if tt.name == "asymmetric" {
					require.Equal(t, "00000000-0000-4000-8000-000000000001", output["pipeline_template_uuid"])
				} else {
					require.NotContains(t, output, "pipeline_template_uuid", "go-buildkite collapses null and absent template assignments")
				}
				require.Equal(t, false, output["skip_queued_branch_builds"])
				require.Equal(t, false, output["cancel_running_branch_builds"])
				actual := output["provider"].(map[string]any)["settings"].(map[string]any)
				var expected map[string]any
				require.NoError(t, json.Unmarshal([]byte(tt.settings), &expected))
				for key, value := range expected {
					require.Equal(t, value, actual[key], key)
				}
				for _, key := range []string{"github_actions_mode", "github_workflow_access_tokens_enabled", "ignore_default_branch_pull_requests", "skip_builds_for_closed_pull_requests", "skip_builds_for_existing_commits"} {
					if _, present := expected[key]; !present {
						require.NotContains(t, actual, key)
					}
				}
			})
		}
	}
}

func pipelineMCPClient(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	api, err := buildkite.NewOpts(buildkite.WithBaseURL(baseURL + "/"))
	require.NoError(t, err)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	server.AddReceivingMiddleware(InjectDepsMiddleware(ToolDependencies{PipelinesClient: api.Pipelines}))
	getTool, getHandler, _ := GetPipeline()
	mcp.AddTool(server, &getTool, getHandler)
	listTool, listHandler, _ := ListPipelines()
	mcp.AddTool(server, &listTool, listHandler)
	createTool, createHandler, _ := CreatePipeline()
	mcp.AddTool(server, &createTool, createHandler)
	updateTool, updateHandler, _ := UpdatePipeline()
	mcp.AddTool(server, &updateTool, updateHandler)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestPipelineSettingsWritesThroughMCP(t *testing.T) {
	for _, tt := range []struct{ name, fields string }{
		{"asymmetric", `{"branch_configuration":"main release/*","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"provider_settings":{"github_actions_mode":true,"github_workflow_access_tokens_enabled":false,"ignore_default_branch_pull_requests":true,"skip_builds_for_closed_pull_requests":false,"skip_builds_for_existing_commits":true,"trigger_mode":"none","build_pull_requests":false,"build_branches":true,"pull_request_branch_filter_enabled":false,"pull_request_branch_filter_configuration":"main","skip_pull_request_builds_for_existing_commits":true,"build_pull_request_forks":false,"prefix_pull_request_fork_branch_names":true,"build_tags":false,"publish_commit_status":true,"publish_commit_status_per_step":false,"filter_enabled":true,"filter_condition":"build.branch != 'release'","separate_pull_request_statuses":false,"publish_blocked_as_pending":true}}`},
		{"partial false", `{"provider_settings":{"github_actions_mode":false,"publish_commit_status":false}}`},
		{"clear filters", `{"branch_configuration":null,"skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"provider_settings":{"filter_condition":null,"pull_request_branch_filter_configuration":null}}`},
		{"empty filters", `{"branch_configuration":"","provider_settings":{"filter_condition":"","pull_request_branch_filter_configuration":""}}`},
		{"empty settings", `{"provider_settings":{}}`},
		{"ordinary", `{"description":"ordinary pipeline","skip_queued_branch_builds":true,"cancel_running_branch_builds":false}`},
	} {
		for _, toolName := range []string{"create_pipeline", "update_pipeline"} {
			t.Run(toolName+"/"+tt.name, func(t *testing.T) {
				var fields map[string]any
				require.NoError(t, json.Unmarshal([]byte(tt.fields), &fields))
				expected := make(map[string]any)
				args := map[string]any{"org_slug": "acme"}
				for key, value := range fields {
					args[key], expected[key] = value, value
				}
				if toolName == "create_pipeline" {
					args["name"], args["repository_url"], args["cluster_id"], args["configuration"], args["create_webhook"] = "Synthetic", "https://github.com/acme/example.git", "cluster-uuid", "steps: []", false
					expected["name"], expected["repository"], expected["cluster_id"], expected["configuration"] = "Synthetic", "https://github.com/acme/example.git", "cluster-uuid", "steps: []"
					if _, ok := expected["skip_queued_branch_builds"]; !ok {
						expected["skip_queued_branch_builds"] = false
					}
					if _, ok := expected["cancel_running_branch_builds"]; !ok {
						expected["cancel_running_branch_builds"] = false
					}
					// go-buildkite create uses the API's null default for an empty branch filter.
					if expected["branch_configuration"] == nil || expected["branch_configuration"] == "" {
						delete(expected, "branch_configuration")
					}
				} else {
					args["pipeline_slug"] = "synthetic"
				}
				requests := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if toolName == "create_pipeline" {
						assert.Equal(t, http.MethodPost, r.Method)
						assert.Equal(t, "/v2/organizations/acme/pipelines", r.URL.Path)
					} else {
						assert.Equal(t, http.MethodPatch, r.Method)
						assert.Equal(t, "/v2/organizations/acme/pipelines/synthetic", r.URL.Path)
					}
					var body map[string]any
					if assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
						assert.Equal(t, expected, body)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"slug":"synthetic","provider":{"id":"github","settings":{"github_actions_mode":true,"github_workflow_access_tokens_enabled":false}}}`))
				}))
				t.Cleanup(srv.Close)
				result, err := pipelineMCPClient(t, srv.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: toolName, Arguments: args})
				require.NoError(t, err)
				require.False(t, result.IsError, getTextResult(t, result).Text)
				require.Equal(t, 1, requests, "must not create a traditional webhook")
				var output map[string]any
				require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &output))
				if toolName == "create_pipeline" {
					output = output["pipeline"].(map[string]any)
				}
				settings := output["provider"].(map[string]any)["settings"].(map[string]any)
				require.Equal(t, true, settings["github_actions_mode"])
				require.Equal(t, false, settings["github_workflow_access_tokens_enabled"])
			})
		}
	}
}

func TestPipelineSettingsRejectInvalidInputsThroughMCP(t *testing.T) {
	for _, input := range []string{
		`{"provider_settings":null}`, `{"provider_settings":{"github_actions_mode":null}}`,
		`{"provider_settings":{"trigger_mode":null}}`, `{"provider_settings":{"publish_commit_status":"false"}}`,
		`{"provider_settings":{"unknown_setting":true}}`, `{"branch_configuration":false}`,
	} {
		for _, name := range []string{"create_pipeline", "update_pipeline"} {
			t.Run(name+"/"+input, func(t *testing.T) {
				requests := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests++
					w.WriteHeader(http.StatusInternalServerError)
				}))
				t.Cleanup(srv.Close)
				args := map[string]any{"org_slug": "acme", "pipeline_slug": "synthetic"}
				if name == "create_pipeline" {
					args = map[string]any{"org_slug": "acme", "name": "Synthetic", "repository_url": "https://github.com/acme/example.git", "cluster_id": "cluster-uuid", "configuration": "steps: []", "create_webhook": false}
				}
				var fields map[string]any
				require.NoError(t, json.Unmarshal([]byte(input), &fields))
				maps.Copy(args, fields)
				result, err := pipelineMCPClient(t, srv.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
				require.True(t, err != nil || result.IsError)
				require.Zero(t, requests)
			})
		}
	}
}

func TestPipelineSettingsPreserveAPIErrorsThroughMCP(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		for _, tt := range []struct{ tool, fields string }{
			{"create_pipeline", `{"configuration":"steps: []"}`},
			{"update_pipeline", `{}`},
			{"create_pipeline", `{"pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`},
			{"update_pipeline", `{"pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`},
			{"update_pipeline", `{"pipeline_template_uuid":null}`},
		} {
			t.Run(fmt.Sprintf("%s/%d/%s", tt.tool, status, tt.fields), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"synthetic permission or availability error"}`))
				}))
				t.Cleanup(srv.Close)
				args := map[string]any{"org_slug": "acme", "provider_settings": map[string]any{"github_actions_mode": true}}
				if tt.tool == "create_pipeline" {
					args["name"], args["repository_url"], args["cluster_id"], args["create_webhook"] = "Synthetic", "https://github.com/acme/example.git", "cluster-uuid", false
				} else {
					args["pipeline_slug"] = "synthetic"
				}
				var fields map[string]any
				require.NoError(t, json.Unmarshal([]byte(tt.fields), &fields))
				maps.Copy(args, fields)
				result, err := pipelineMCPClient(t, srv.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: args})
				if status == http.StatusUnauthorized {
					require.ErrorIs(t, err, ErrUnauthorized)
				} else {
					require.NoError(t, err)
					require.True(t, result.IsError)
					require.JSONEq(t, `{"message":"synthetic permission or availability error"}`, getTextResult(t, result).Text)
				}
			})
		}
	}
}

func TestPipelineTemplatesThroughMCP(t *testing.T) {
	for _, tt := range []struct{ name, tool, fields, body, responseTemplate string }{
		{"create from template", "create_pipeline", `{"pipeline_template_uuid":"00000000-0000-4000-8000-000000000001","provider_settings":{"github_actions_mode":true,"publish_commit_status":false}}`, `{"name":"Synthetic","repository":"https://github.com/acme/example.git","cluster_id":"cluster-uuid","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"pipeline_template_uuid":"00000000-0000-4000-8000-000000000001","provider_settings":{"github_actions_mode":true,"publish_commit_status":false}}`, `"00000000-0000-4000-8000-000000000001"`},
		{"assign template", "update_pipeline", `{"pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`, `{"pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`, `"00000000-0000-4000-8000-000000000001"`},
		{"retain template", "update_pipeline", `{"description":"retained"}`, `{"description":"retained"}`, `"00000000-0000-4000-8000-000000000001"`},
		{"remove with replacement", "update_pipeline", `{"pipeline_template_uuid":null,"configuration":"steps: []"}`, `{"pipeline_template_uuid":null,"configuration":"steps: []"}`, `null`},
		{"remove without replacement", "update_pipeline", `{"pipeline_template_uuid":null}`, `{"pipeline_template_uuid":null}`, `null`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if tt.tool == "create_pipeline" {
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "/v2/organizations/acme/pipelines", r.URL.Path)
				} else {
					assert.Equal(t, http.MethodPatch, r.Method)
					assert.Equal(t, "/v2/organizations/acme/pipelines/synthetic", r.URL.Path)
				}
				var body json.RawMessage
				if assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
					assert.JSONEq(t, tt.body, string(body))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"slug":"synthetic","pipeline_template_uuid":` + tt.responseTemplate + `}`))
			}))
			t.Cleanup(srv.Close)
			args := map[string]any{"org_slug": "acme"}
			if tt.tool == "create_pipeline" {
				args["name"], args["repository_url"], args["cluster_id"], args["create_webhook"] = "Synthetic", "https://github.com/acme/example.git", "cluster-uuid", false
			} else {
				args["pipeline_slug"] = "synthetic"
			}
			var fields map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.fields), &fields))
			maps.Copy(args, fields)
			result, err := pipelineMCPClient(t, srv.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError, getTextResult(t, result).Text)
			require.Equal(t, 1, requests)
			var output map[string]any
			require.NoError(t, json.Unmarshal([]byte(getTextResult(t, result).Text), &output))
			if tt.tool == "create_pipeline" {
				output = output["pipeline"].(map[string]any)
			}
			if tt.responseTemplate == "null" {
				require.NotContains(t, output, "pipeline_template_uuid")
			} else {
				require.Equal(t, "00000000-0000-4000-8000-000000000001", output["pipeline_template_uuid"])
			}
		})
	}
}

func TestPipelineTemplateValidationThroughMCP(t *testing.T) {
	for _, tt := range []struct{ name, tool, fields string }{
		{"missing alternatives", "create_pipeline", `{}`},
		{"blank configuration", "create_pipeline", `{"configuration":"  "}`},
		{"blank template", "create_pipeline", `{"pipeline_template_uuid":""}`},
		{"null template on create", "create_pipeline", `{"pipeline_template_uuid":null}`},
		{"conflicting alternatives", "create_pipeline", `{"configuration":"steps: []","pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`},
		{"template with empty configuration", "create_pipeline", `{"configuration":"","pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`},
		{"configuration with empty template", "create_pipeline", `{"configuration":"steps: []","pipeline_template_uuid":""}`},
		{"empty update is not removal", "update_pipeline", `{"pipeline_template_uuid":""}`},
		{"blank update is not removal", "update_pipeline", `{"pipeline_template_uuid":"  "}`},
		{"update conflicts", "update_pipeline", `{"configuration":"steps: []","pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`},
		{"null configuration conflicts", "update_pipeline", `{"configuration":null,"pipeline_template_uuid":"00000000-0000-4000-8000-000000000001"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(srv.Close)
			args := map[string]any{"org_slug": "acme"}
			if tt.tool == "create_pipeline" {
				args["name"], args["repository_url"], args["cluster_id"], args["create_webhook"] = "Synthetic", "https://github.com/acme/example.git", "cluster-uuid", false
			} else {
				args["pipeline_slug"] = "synthetic"
			}
			var fields map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.fields), &fields))
			maps.Copy(args, fields)
			result, err := pipelineMCPClient(t, srv.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: args})
			require.True(t, err != nil || result.IsError)
			require.Zero(t, requests)
		})
	}
}

func TestCreatePipeline(t *testing.T) {
	assert := require.New(t)

	testPipelineDefinition := `
agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
    key: "hello_step"
    label: "Hello Step"
`

	webhookCalled := false
	client := &MockPipelinesClient{
		CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
			// validate required fields
			assert.Equal("org", org)
			assert.Equal("cluster-123", p.ClusterID)
			assert.Equal("Test Pipeline", p.Name)
			assert.Equal("https://example.com/repo.git", p.Repository)
			assert.Equal(testPipelineDefinition, p.Configuration)
			assert.Equal(map[string]string{"team-uuid-1": "build_and_read"}, p.Teams)

			return buildkite.Pipeline{
				ID:        "123",
				Slug:      "test-pipeline",
				Name:      "Test Pipeline",
				ClusterID: "cluster-123",
				CreatedAt: &buildkite.Timestamp{},
				Tags:      []string{"tag1", "tag2"},
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("test-pipeline", slug)
			webhookCalled = true
			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 201,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	tool, handler, _ := CreatePipeline()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := CreatePipelineArgs{
		OrgSlug:       "org",
		Name:          "Test Pipeline",
		ClusterID:     "cluster-123",
		RepositoryURL: "https://example.com/repo.git",
		Description:   "A test pipeline",
		Configuration: testPipelineDefinition,
		Tags:          []string{"tag1", "tag2"},
		Teams:         map[string]string{"team-uuid-1": "build_and_read"},
		CreateWebhook: true,
	}

	result, _, err := handler(ctx, request, args)
	assert.NoError(err)
	assert.True(webhookCalled, "AddWebhook should have been called when CreateWebhook is true")

	textContent := getTextResult(t, result)
	requireJSONPathEqual(t, textContent.Text, true, "webhook", "created")
	requireJSONPathEqual(t, textContent.Text, "Pipeline and webhook created successfully.", "webhook", "note")
	requireJSONPathEqual(t, textContent.Text, "123", "pipeline", "id")
	requireJSONPathEqual(t, textContent.Text, "Test Pipeline", "pipeline", "name")
	requireJSONPathEqual(t, textContent.Text, "test-pipeline", "pipeline", "slug")
}

func TestCreatePipelineWithWebhook(t *testing.T) {
	assert := require.New(t)

	testPipelineDefinition := `
agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
    key: "hello_step"
    label: "Hello Step"
`

	webhookCalled := false
	client := &MockPipelinesClient{
		CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
			// validate required fields
			assert.Equal("org", org)
			assert.Equal("Test Pipeline", p.Name)
			assert.Equal("https://github.com/example/repo.git", p.Repository)
			assert.Equal("cluster-123", p.ClusterID)
			assert.Equal(testPipelineDefinition, p.Configuration)

			return buildkite.Pipeline{
				ID:        "123",
				Slug:      "test-pipeline",
				Name:      "Test Pipeline",
				ClusterID: "cluster-123",
				CreatedAt: &buildkite.Timestamp{},
				Tags:      []string{"tag1", "tag2"},
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 201,
				},
			}, nil
		},
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			// validate required fields
			assert.Equal("org", org)
			assert.Equal("test-pipeline", slug)

			webhookCalled = true
			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 201,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	tool, handler, _ := CreatePipeline()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := CreatePipelineArgs{
		OrgSlug:       "org",
		Name:          "Test Pipeline",
		ClusterID:     "cluster-123",
		RepositoryURL: "https://github.com/example/repo.git",
		Description:   "A test pipeline",
		Configuration: testPipelineDefinition,
		Tags:          []string{"tag1", "tag2"},
		CreateWebhook: true,
	}

	result, _, err := handler(ctx, request, args)
	assert.NoError(err)
	assert.True(webhookCalled, "AddWebhook should have been called")

	textContent := getTextResult(t, result)
	requireJSONPathEqual(t, textContent.Text, true, "webhook", "created")
	requireJSONPathEqual(t, textContent.Text, "Pipeline and webhook created successfully.", "webhook", "note")
	requireJSONPathEqual(t, textContent.Text, "123", "pipeline", "id")
	requireJSONPathEqual(t, textContent.Text, "Test Pipeline", "pipeline", "name")
	requireJSONPathEqual(t, textContent.Text, "test-pipeline", "pipeline", "slug")
}

func TestCreatePipelineWithWebhookError(t *testing.T) {
	assert := require.New(t)
	webhookErr := &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Request:    httptest.NewRequest(http.MethodPost, "https://api.buildkite.com/v2/organizations/org%3Fnext%23section/pipelines/test-pipeline/webhook", nil),
		},
		RawBody: []byte(`{"message":"Auto-creating webhooks requires a GitHub App to be installed and configured with access to the repository \"example/repo\". Please check that the repository URL is correct and that the GitHub App has been granted access to the repository.","code":"github_app_required"}`),
	}
	assert.NoError(json.Unmarshal(webhookErr.RawBody, webhookErr))

	testPipelineDefinition := `
agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
    key: "hello_step"
    label: "Hello Step"
`

	webhookCalled := false
	client := &MockPipelinesClient{
		CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
			// validate required fields
			assert.Equal("org?next#section", org)
			assert.Equal("Test Pipeline", p.Name)
			assert.Equal("https://github.com/example/repo.git", p.Repository)
			assert.Equal("cluster-123", p.ClusterID)
			assert.Equal(testPipelineDefinition, p.Configuration)

			return buildkite.Pipeline{
				ID:        "123",
				Slug:      "test-pipeline",
				Name:      "Test Pipeline",
				WebURL:    "https://buildkite.example.com/org/test-pipeline",
				ClusterID: "cluster-123",
				CreatedAt: &buildkite.Timestamp{},
				Tags:      []string{"tag1", "tag2"},
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 201,
				},
			}, nil
		},
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			webhookCalled = true
			return nil, webhookErr
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	tool, handler, _ := CreatePipeline()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := CreatePipelineArgs{
		OrgSlug:       "org?next#section",
		Name:          "Test Pipeline",
		ClusterID:     "cluster-123",
		RepositoryURL: "https://github.com/example/repo.git",
		Description:   "A test pipeline",
		Configuration: testPipelineDefinition,
		Tags:          []string{"tag1", "tag2"},
		CreateWebhook: true,
	}

	result, _, err := handler(ctx, request, args)
	assert.NoError(err)
	assert.True(webhookCalled, "AddWebhook should have been called")

	textContent := getTextResult(t, result)
	requireJSONPathEqual(t, textContent.Text, false, "webhook", "created")
	requireJSONPathEqual(t, textContent.Text, webhookErr.Error(), "webhook", "error")
	requireJSONPathEqual(t, textContent.Text, "Pipeline created successfully, but its GitHub webhook could not be created automatically. Do not recreate the pipeline.", "webhook", "note")
	requireJSONPathEqual(t, textContent.Text, "https://buildkite.example.com/organizations/org%3Fnext%23section/repository-providers", "webhook", "setup_url")
	requireJSONPathEqual(t, textContent.Text, "Open setup_url and connect a compatible Buildkite GitHub App, or grant the existing app access to this repository.", "webhook", "next_steps", 0)
	requireJSONPathEqual(t, textContent.Text, "Open the existing pipeline's repository settings at https://buildkite.example.com/org/test-pipeline/settings/repository.", "webhook", "next_steps", 1)
	requireJSONPathEqual(t, textContent.Text, "Do not create another pipeline; the pipeline in this result was created successfully.", "webhook", "next_steps", 3)
	requireJSONPathEqual(t, textContent.Text, "123", "pipeline", "id")
}

func TestCreatePipelineWithoutWebhook(t *testing.T) {
	client := &MockPipelinesClient{
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			t.Fatal("AddWebhook should not be called when CreateWebhook is false")
			return nil, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
	_, handler, _ := CreatePipeline()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineArgs{Configuration: "steps: []", CreateWebhook: false})
	require.NoError(t, err)
	require.NotContains(t, getTextResult(t, result).Text, `"webhook"`)
}

func TestCreatePipelineWithGenericWebhookError(t *testing.T) {
	client := &MockPipelinesClient{
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			return nil, errors.New("request timed out")
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
	_, handler, _ := CreatePipeline()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineArgs{Configuration: "steps: []", CreateWebhook: true})
	require.NoError(t, err)

	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, false, "webhook", "created")
	requireJSONPathEqual(t, text, "request timed out", "webhook", "error")
	requireJSONPathEqual(t, text, "Pipeline created successfully, but webhook creation failed.", "webhook", "note")
	require.NotContains(t, text, `"setup_url"`)
	require.NotContains(t, text, `"next_steps"`)
}

func TestCreatePipelineWithOtherWebhook422(t *testing.T) {
	// These messages come from Project::Webhook::Creator in buildkite/buildkite;
	// only the machine-readable code determines the recovery action.
	tests := []struct {
		name string
		body string
		note string
	}{
		{
			name: "unsupported provider",
			body: `{"message":"Auto-creating webhooks is only supported for GitHub and GitHub Enterprise repositories. The pipeline's repository provider was detected as \"Bitbucket\". If this is a GitHub repository, please verify the repository URL is correct.","code":"unsupported_repository_provider"}`,
			note: "Pipeline created successfully, but automatic webhook creation is unsupported for this repository provider. Do not recreate the pipeline.",
		},
		{
			name: "uncoded webhook failure",
			body: `{"message":"Unable to create webhook due to GitHub rate limit"}`,
			note: "Pipeline created successfully, but webhook creation failed.",
		},
		{
			name: "unknown code",
			body: `{"message":"Another failure","code":"other_failure"}`,
			note: "Pipeline created successfully, but webhook creation failed.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhookErr := &buildkite.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusUnprocessableEntity,
					Request:    httptest.NewRequest(http.MethodPost, "https://api.buildkite.com/v2/organizations/org/pipelines/test-pipeline/webhook", nil),
				},
				RawBody: []byte(tt.body),
			}
			require.NoError(t, json.Unmarshal(webhookErr.RawBody, webhookErr))
			client := &MockPipelinesClient{
				CreateFunc: func(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
					return buildkite.Pipeline{ID: "123", WebURL: "https://buildkite.example.com/org/test-pipeline"}, nil, nil
				},
				AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
					return nil, webhookErr
				},
			}
			ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
			_, handler, _ := CreatePipeline()
			result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineArgs{OrgSlug: "org", Configuration: "steps: []", CreateWebhook: true})
			require.NoError(t, err)

			text := getTextResult(t, result).Text
			requireJSONPathEqual(t, text, "123", "pipeline", "id")
			requireJSONPathEqual(t, text, false, "webhook", "created")
			requireJSONPathEqual(t, text, tt.note, "webhook", "note")
			require.NotContains(t, text, `"setup_url"`)
			require.NotContains(t, text, `"next_steps"`)
		})
	}
}

func TestCreatePipelineWebhook(t *testing.T) {
	client := &MockPipelinesClient{
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "pipeline", slug)
			return &buildkite.Response{Response: &http.Response{StatusCode: http.StatusCreated}}, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
	tool, handler, scopes := CreatePipelineWebhook()

	require.Equal(t, "create_pipeline_webhook", tool.Name)
	require.Equal(t, []string{"write_pipelines"}, scopes)
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineWebhookArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, true, "created")
	requireJSONPathEqual(t, text, "Webhook created successfully.", "note")
}

func TestCreatePipelineWebhookRejectsInvalidSlugs(t *testing.T) {
	tests := []struct {
		name         string
		orgSlug      string
		pipelineSlug string
		errorMessage string
	}{
		{
			name:         "organization path traversal",
			orgSlug:      "org/pipelines/victim",
			pipelineSlug: "pipeline",
			errorMessage: "org_slug must be 1-100 characters, start with a letter or number, and contain only letters, numbers, or hyphens",
		},
		{
			name:         "pipeline endpoint injection",
			orgSlug:      "org",
			pipelineSlug: "victim/archive?ignored=",
			errorMessage: "pipeline_slug must be 1-100 characters, start with a letter or number, and contain only letters, numbers, or hyphens",
		},
		{
			name:         "pipeline slug too long",
			orgSlug:      "org",
			pipelineSlug: strings.Repeat("a", 101),
			errorMessage: "pipeline_slug must be 1-100 characters, start with a letter or number, and contain only letters, numbers, or hyphens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockPipelinesClient{
				AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
					t.Fatal("AddWebhook must not be called with an invalid slug")
					return nil, nil
				},
			}
			ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
			_, handler, _ := CreatePipelineWebhook()

			result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineWebhookArgs{
				OrgSlug:      tt.orgSlug,
				PipelineSlug: tt.pipelineSlug,
			})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Equal(t, tt.errorMessage, getTextResult(t, result).Text)
		})
	}
}

func TestCreatePipelineWebhookWithCodedError(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		note     string
		setupURL string
	}{
		{
			name:     "GitHub App required",
			body:     `{"message":"Auto-creating webhooks requires a GitHub App to be installed and configured with access to the repository \"example/repo\".","code":"github_app_required"}`,
			note:     "The webhook could not be created automatically because the pipeline is not connected through a compatible Buildkite GitHub App.",
			setupURL: "https://buildkite.com/organizations/org/repository-providers",
		},
		{
			name: "unsupported provider",
			body: `{"message":"Auto-creating webhooks is only supported for GitHub and GitHub Enterprise repositories. The pipeline's repository provider was detected as \"Bitbucket\".","code":"unsupported_repository_provider"}`,
			note: "The webhook could not be created automatically because automatic webhook creation is unsupported for this repository provider.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhookErr := &buildkite.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusUnprocessableEntity,
					Request:    httptest.NewRequest(http.MethodPost, "https://api.buildkite.com/v2/organizations/org/pipelines/pipeline/webhook", nil),
				},
				RawBody: []byte(tt.body),
			}
			require.NoError(t, json.Unmarshal(webhookErr.RawBody, webhookErr))

			client := &MockPipelinesClient{
				AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
					return nil, webhookErr
				},
			}
			ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
			_, handler, _ := CreatePipelineWebhook()

			result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineWebhookArgs{
				OrgSlug:      "org",
				PipelineSlug: "pipeline",
			})
			require.NoError(t, err)
			require.False(t, result.IsError)

			text := getTextResult(t, result).Text
			requireJSONPathEqual(t, text, false, "created")
			requireJSONPathEqual(t, text, tt.note, "note")
			if tt.setupURL != "" {
				requireJSONPathEqual(t, text, tt.setupURL, "setup_url")
				requireJSONPathEqual(t, text, "Open setup_url and connect a compatible Buildkite GitHub App, or grant the existing app access to this repository.", "next_steps", 0)
				requireJSONPathEqual(t, text, "Open https://buildkite.com/org/pipeline, then open Settings > GitHub > Setup Instructions.", "next_steps", 1)
			} else {
				require.NotContains(t, text, `"setup_url"`)
				require.NotContains(t, text, `"next_steps"`)
			}
		})
	}
}

func TestCreatePipelineWebhookWithUnrecognized422(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "uncoded", body: `{"message":"Webhook could not be created for your repository."}`},
		{name: "unknown code", body: `{"message":"Another failure","code":"other_failure"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhookErr := &buildkite.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusUnprocessableEntity,
					Request:    httptest.NewRequest(http.MethodPost, "https://api.buildkite.com/v2/organizations/org/pipelines/pipeline/webhook", nil),
				},
				RawBody: []byte(tt.body),
			}
			require.NoError(t, json.Unmarshal(webhookErr.RawBody, webhookErr))
			client := &MockPipelinesClient{
				AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
					return nil, webhookErr
				},
			}
			ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
			_, handler, _ := CreatePipelineWebhook()

			result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineWebhookArgs{
				OrgSlug:      "org",
				PipelineSlug: "pipeline",
			})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.JSONEq(t, tt.body, getTextResult(t, result).Text)
		})
	}
}

func TestCreatePipelineWebhookWithForbiddenError(t *testing.T) {
	client := &MockPipelinesClient{
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			return nil, &buildkite.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusForbidden},
				Message:  "Forbidden",
			}
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
	_, handler, _ := CreatePipelineWebhook()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineWebhookArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "Forbidden", getTextResult(t, result).Text)
}

func TestCreatePipelineWebhookWithGenericError(t *testing.T) {
	client := &MockPipelinesClient{
		AddWebhookFunc: func(ctx context.Context, org string, slug string) (*buildkite.Response, error) {
			return nil, errors.New("request timed out")
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})
	_, handler, _ := CreatePipelineWebhook()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), CreatePipelineWebhookArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "request timed out", getTextResult(t, result).Text)
}

func TestUpdatePipeline(t *testing.T) {
	assert := require.New(t)

	testPipelineDefinition := `agents:
  queue: "something"
env:
  TEST_ENV_VAR: "value"
steps:
  - command: "echo Hello World"
	key: "hello_step"
	label: "Hello Step"
`
	client := &MockPipelinesClient{
		UpdateFunc: func(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
			// validate required fields
			assert.Equal("org", org)
			assert.Equal("test-pipeline", pipeline)

			configuration, ok := p.Configuration.Value()
			assert.True(ok)
			assert.Equal(testPipelineDefinition, configuration)

			name, ok := p.Name.Value()
			assert.True(ok)
			assert.Equal("Test Pipeline", name)

			skipQueuedBranchBuilds, ok := p.SkipQueuedBranchBuilds.Value()
			assert.True(ok)
			assert.False(skipQueuedBranchBuilds)

			tags, ok := p.Tags.Value()
			assert.True(ok)
			assert.Equal([]string{"tag1", "tag2"}, tags)

			return buildkite.Pipeline{
				ID:        "123",
				Slug:      "test-pipeline",
				Name:      "Test Pipeline",
				ClusterID: "abc-123",
				CreatedAt: &buildkite.Timestamp{},
				Tags:      []string{"tag1", "tag2"},
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	tool, handler, _ := UpdatePipeline()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})

	args := UpdatePipelineArgs{
		OrgSlug:                "org",
		PipelineSlug:           "test-pipeline",
		Name:                   testPtr("Test Pipeline"),
		ClusterID:              testPtr("abc-123"),
		Description:            testPtr("A test pipeline"),
		Configuration:          testPtr(testPipelineDefinition),
		RepositoryURL:          testPtr("https://example.com/repo.git"),
		SkipQueuedBranchBuilds: testPtr(false),
		Tags:                   []string{"tag1", "tag2"},
	}
	result, _, err := handler(ctx, request, args)
	assert.NoError(err)
	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"123","name":"Test Pipeline","slug":"test-pipeline","created_at":"0001-01-01T00:00:00Z","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"cluster_id":"abc-123","tags":["tag1","tag2"],"provider":{"id":"","webhook_url":"","settings":null}}`, textContent.Text)
}

func TestUpdatePipelineOmittedFieldsAndEmptyTags(t *testing.T) {
	assert := require.New(t)

	client := &MockPipelinesClient{
		UpdateFunc: func(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
			assert.True(p.Name.IsZero())
			assert.True(p.Configuration.IsZero())

			cancelRunningBranchBuilds, ok := p.CancelRunningBranchBuilds.Value()
			assert.True(ok)
			assert.False(cancelRunningBranchBuilds)

			tags, ok := p.Tags.Value()
			assert.True(ok)
			assert.Empty(tags)

			return buildkite.Pipeline{
				ID: "123",
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	_, handler, _ := UpdatePipeline()

	request := createMCPRequest(t, map[string]any{})
	_, _, err := handler(ctx, request, UpdatePipelineArgs{
		OrgSlug:                   "org",
		PipelineSlug:              "test-pipeline",
		CancelRunningBranchBuilds: testPtr(false),
		Tags:                      []string{},
	})
	assert.NoError(err)
}

func TestUpdatePipelineSendsEmptyTags(t *testing.T) {
	assert := require.New(t)

	client := &MockPipelinesClient{
		UpdateFunc: func(ctx context.Context, org string, pipeline string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("test-pipeline", pipeline)

			tags, ok := p.Tags.Value()
			assert.True(ok)
			assert.Equal([]string{}, tags)

			return buildkite.Pipeline{
				ID:   "123",
				Slug: "test-pipeline",
				Tags: []string{},
			}, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelinesClient: client})

	_, handler, _ := UpdatePipeline()

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), UpdatePipelineArgs{
		OrgSlug:      "org",
		PipelineSlug: "test-pipeline",
		Tags:         []string{},
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"123","slug":"test-pipeline","skip_queued_branch_builds":false,"cancel_running_branch_builds":false,"provider":{"id":"","webhook_url":"","settings":null}}`, textContent.Text)
}
