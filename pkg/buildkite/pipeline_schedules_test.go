package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

type MockPipelineSchedulesClient struct {
	ListFunc   func(ctx context.Context, org, pipelineSlug string, opt *buildkite.PipelineScheduleListOptions) ([]buildkite.PipelineSchedule, *buildkite.Response, error)
	GetFunc    func(ctx context.Context, org, pipelineSlug, id string) (buildkite.PipelineSchedule, *buildkite.Response, error)
	CreateFunc func(ctx context.Context, org, pipelineSlug string, in buildkite.CreatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error)
	UpdateFunc func(ctx context.Context, org, pipelineSlug, id string, in buildkite.UpdatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error)
}

func (m *MockPipelineSchedulesClient) List(ctx context.Context, org, pipelineSlug string, opt *buildkite.PipelineScheduleListOptions) ([]buildkite.PipelineSchedule, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, pipelineSlug, opt)
	}
	return nil, nil, nil
}

func (m *MockPipelineSchedulesClient) Get(ctx context.Context, org, pipelineSlug, id string) (buildkite.PipelineSchedule, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, pipelineSlug, id)
	}
	return buildkite.PipelineSchedule{}, nil, nil
}

func (m *MockPipelineSchedulesClient) Create(ctx context.Context, org, pipelineSlug string, in buildkite.CreatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, org, pipelineSlug, in)
	}
	return buildkite.PipelineSchedule{}, nil, nil
}

func (m *MockPipelineSchedulesClient) Update(ctx context.Context, org, pipelineSlug, id string, in buildkite.UpdatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error) {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, org, pipelineSlug, id, in)
	}
	return buildkite.PipelineSchedule{}, nil, nil
}

var _ PipelineSchedulesClient = (*MockPipelineSchedulesClient)(nil)

func TestListPipelineSchedules(t *testing.T) {
	assert := require.New(t)

	client := &MockPipelineSchedulesClient{
		ListFunc: func(ctx context.Context, org, pipelineSlug string, opt *buildkite.PipelineScheduleListOptions) ([]buildkite.PipelineSchedule, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("pipeline", pipelineSlug)
			assert.Equal(1, opt.Page)
			assert.Equal(100, opt.PerPage)
			return []buildkite.PipelineSchedule{
					{
						ID:       "abc",
						Label:    "Nightly build",
						Cronline: "@daily",
						Enabled:  true,
					},
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelineSchedulesClient: client})

	tool, handler, _ := ListPipelineSchedules()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListPipelineSchedulesArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"abc","label":"Nightly build","cronline":"@daily","enabled":true}]}`, textContent.Text)
}

func TestGetPipelineSchedule(t *testing.T) {
	assert := require.New(t)

	client := &MockPipelineSchedulesClient{
		GetFunc: func(ctx context.Context, org, pipelineSlug, id string) (buildkite.PipelineSchedule, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("pipeline", pipelineSlug)
			assert.Equal("abc", id)
			return buildkite.PipelineSchedule{
					ID:       "abc",
					Label:    "Nightly build",
					Cronline: "@daily",
					Branch:   "main",
					Enabled:  true,
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelineSchedulesClient: client})

	tool, handler, _ := GetPipelineSchedule()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetPipelineScheduleArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		ScheduleID:   "abc",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"abc","label":"Nightly build","cronline":"@daily","branch":"main","enabled":true}`, textContent.Text)
}

func TestCreatePipelineSchedule(t *testing.T) {
	assert := require.New(t)

	enabled := false
	client := &MockPipelineSchedulesClient{
		CreateFunc: func(ctx context.Context, org, pipelineSlug string, in buildkite.CreatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("pipeline", pipelineSlug)
			assert.Equal("@daily", in.Cronline)
			assert.Equal("Nightly build", in.Label)
			assert.Equal(map[string]string{"FOO": "bar"}, in.Env)
			assert.NotNil(in.Enabled)
			assert.False(*in.Enabled)
			return buildkite.PipelineSchedule{
					ID:       "new-id",
					Cronline: "@daily",
					Label:    "Nightly build",
					Enabled:  false,
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 201},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelineSchedulesClient: client})

	tool, handler, _ := CreatePipelineSchedule()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, CreatePipelineScheduleArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		Cronline:     "@daily",
		Label:        "Nightly build",
		Env:          map[string]string{"FOO": "bar"},
		Enabled:      &enabled,
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"new-id","label":"Nightly build","cronline":"@daily","enabled":false}`, textContent.Text)
}

func TestUpdatePipelineSchedule(t *testing.T) {
	assert := require.New(t)

	enabled := true
	label := "Updated label"
	client := &MockPipelineSchedulesClient{
		UpdateFunc: func(ctx context.Context, org, pipelineSlug, id string, in buildkite.UpdatePipelineSchedule) (buildkite.PipelineSchedule, *buildkite.Response, error) {
			assert.Equal("org", org)
			assert.Equal("pipeline", pipelineSlug)
			assert.Equal("abc", id)
			gotLabel, ok := in.Label.Value()
			assert.True(ok)
			assert.Equal("Updated label", gotLabel)
			gotEnabled, ok := in.Enabled.Value()
			assert.True(ok)
			assert.True(gotEnabled)
			assert.True(in.Cronline.IsZero())
			assert.True(in.Message.IsZero())
			assert.True(in.Commit.IsZero())
			assert.True(in.Branch.IsZero())
			assert.True(in.Env.IsZero())
			return buildkite.PipelineSchedule{
					ID:      "abc",
					Label:   "Updated label",
					Enabled: true,
				}, &buildkite.Response{
					Response: &http.Response{StatusCode: 200},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{PipelineSchedulesClient: client})

	tool, handler, _ := UpdatePipelineSchedule()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, UpdatePipelineScheduleArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		ScheduleID:   "abc",
		Label:        &label,
		Enabled:      &enabled,
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"abc","label":"Updated label","enabled":true}`, textContent.Text)
}
