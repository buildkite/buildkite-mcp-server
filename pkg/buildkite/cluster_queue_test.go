package buildkite

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type mockClusterQueuesClient struct {
	ListFunc   func(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error)
	GetFunc    func(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error)
	CreateFunc func(ctx context.Context, org, clusterID string, qc buildkite.ClusterQueueCreate) (buildkite.ClusterQueue, *buildkite.Response, error)
	UpdateFunc func(ctx context.Context, org, clusterID, queueID string, qu buildkite.ClusterQueueUpdate) (buildkite.ClusterQueue, *buildkite.Response, error)
	PauseFunc  func(ctx context.Context, org, clusterID, queueID string, qp buildkite.ClusterQueuePause) (buildkite.ClusterQueue, *buildkite.Response, error)
	ResumeFunc func(ctx context.Context, org, clusterID, queueID string) (*buildkite.Response, error)
}

func (m *mockClusterQueuesClient) List(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, clusterID, opts)
	}
	return nil, nil, nil
}

func (m *mockClusterQueuesClient) Get(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, clusterID, queueID)
	}
	return buildkite.ClusterQueue{}, nil, nil
}

func (m *mockClusterQueuesClient) Create(ctx context.Context, org, clusterID string, qc buildkite.ClusterQueueCreate) (buildkite.ClusterQueue, *buildkite.Response, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, org, clusterID, qc)
	}
	return buildkite.ClusterQueue{}, nil, nil
}

func (m *mockClusterQueuesClient) Update(ctx context.Context, org, clusterID, queueID string, qu buildkite.ClusterQueueUpdate) (buildkite.ClusterQueue, *buildkite.Response, error) {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, org, clusterID, queueID, qu)
	}
	return buildkite.ClusterQueue{}, nil, nil
}

func (m *mockClusterQueuesClient) Pause(ctx context.Context, org, clusterID, queueID string, qp buildkite.ClusterQueuePause) (buildkite.ClusterQueue, *buildkite.Response, error) {
	if m.PauseFunc != nil {
		return m.PauseFunc(ctx, org, clusterID, queueID, qp)
	}
	return buildkite.ClusterQueue{}, nil, nil
}

func (m *mockClusterQueuesClient) Resume(ctx context.Context, org, clusterID, queueID string) (*buildkite.Response, error) {
	if m.ResumeFunc != nil {
		return m.ResumeFunc(ctx, org, clusterID, queueID)
	}
	return nil, nil
}

var _ ClusterQueuesClient = (*mockClusterQueuesClient)(nil)

func TestListClusterQueues(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		ListFunc: func(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error) {
			return []buildkite.ClusterQueue{
					{
						ID: "queue-id",
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	tool, handler, _ := ListClusterQueues()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListClusterQueuesArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"queue-id","dispatch_paused":false,"created_by":{}}]}`, textContent.Text)
}

func TestGetClusterQueue(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		GetFunc: func(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error) {
			return buildkite.ClusterQueue{
					ID: "queue-id",
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	tool, handler, _ := GetClusterQueue()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetClusterQueueArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		QueueID:   "queue-id",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq("{\"id\":\"queue-id\",\"dispatch_paused\":false,\"created_by\":{}}", textContent.Text)
}

func TestCreateClusterQueue(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		CreateFunc: func(ctx context.Context, org, clusterID string, qc buildkite.ClusterQueueCreate) (buildkite.ClusterQueue, *buildkite.Response, error) {
			return buildkite.ClusterQueue{
					ID:  "new-queue-id",
					Key: qc.Key,
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 201,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	tool, handler, scopes := CreateClusterQueue()
	assert.Equal("create_cluster_queue", tool.Name)
	assert.Equal(boolPtr(false), tool.Annotations.DestructiveHint)
	assert.Contains(scopes, "write_clusters")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, CreateClusterQueueArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		Key:       "default",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"new-queue-id","key":"default","dispatch_paused":false,"created_by":{}}`, textContent.Text)
}

func TestUpdateClusterQueue(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		UpdateFunc: func(ctx context.Context, org, clusterID, queueID string, qu buildkite.ClusterQueueUpdate) (buildkite.ClusterQueue, *buildkite.Response, error) {
			return buildkite.ClusterQueue{
					ID:          queueID,
					Description: qu.Description,
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	tool, handler, scopes := UpdateClusterQueue()
	assert.Equal("update_cluster_queue", tool.Name)
	assert.Equal(boolPtr(false), tool.Annotations.DestructiveHint)
	assert.Contains(scopes, "write_clusters")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, UpdateClusterQueueArgs{
		OrgSlug:     "org",
		ClusterID:   "cluster-id",
		QueueID:     "queue-id",
		Description: "updated description",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"queue-id","description":"updated description","dispatch_paused":false,"created_by":{}}`, textContent.Text)
}

func TestPauseClusterQueueDispatch(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		PauseFunc: func(ctx context.Context, org, clusterID, queueID string, qp buildkite.ClusterQueuePause) (buildkite.ClusterQueue, *buildkite.Response, error) {
			return buildkite.ClusterQueue{
					ID:             queueID,
					DispatchPaused: true,
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	tool, handler, scopes := PauseClusterQueueDispatch()
	assert.Equal("pause_cluster_queue_dispatch", tool.Name)
	assert.Equal(boolPtr(true), tool.Annotations.DestructiveHint)
	assert.Contains(scopes, "write_clusters")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, PauseClusterQueueDispatchArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		QueueID:   "queue-id",
		Note:      "maintenance",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"queue-id","dispatch_paused":true,"created_by":{}}`, textContent.Text)
}

func TestResumeClusterQueueDispatch(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		ResumeFunc: func(ctx context.Context, org, clusterID, queueID string) (*buildkite.Response, error) {
			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	tool, handler, scopes := ResumeClusterQueueDispatch()
	assert.Equal("resume_cluster_queue_dispatch", tool.Name)
	assert.Equal(boolPtr(false), tool.Annotations.DestructiveHint)
	assert.Contains(scopes, "write_clusters")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ResumeClusterQueueDispatchArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		QueueID:   "queue-id",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, "Cluster queue dispatch resumed successfully")
}

func TestCreateClusterQueueWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		CreateFunc: func(ctx context.Context, org, clusterID string, qc buildkite.ClusterQueueCreate) (buildkite.ClusterQueue, *buildkite.Response, error) {
			return buildkite.ClusterQueue{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	_, handler, _ := CreateClusterQueue()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, CreateClusterQueueArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		Key:       "default",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestUpdateClusterQueueWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		UpdateFunc: func(ctx context.Context, org, clusterID, queueID string, qu buildkite.ClusterQueueUpdate) (buildkite.ClusterQueue, *buildkite.Response, error) {
			return buildkite.ClusterQueue{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	_, handler, _ := UpdateClusterQueue()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, UpdateClusterQueueArgs{
		OrgSlug:     "org",
		ClusterID:   "cluster-id",
		QueueID:     "queue-id",
		Description: "updated",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestPauseClusterQueueDispatchWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		PauseFunc: func(ctx context.Context, org, clusterID, queueID string, qp buildkite.ClusterQueuePause) (buildkite.ClusterQueue, *buildkite.Response, error) {
			return buildkite.ClusterQueue{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	_, handler, _ := PauseClusterQueueDispatch()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, PauseClusterQueueDispatchArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		QueueID:   "queue-id",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestResumeClusterQueueDispatchWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterQueuesClient{
		ResumeFunc: func(ctx context.Context, org, clusterID, queueID string) (*buildkite.Response, error) {
			return &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterQueuesClient: client})

	_, handler, _ := ResumeClusterQueueDispatch()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ResumeClusterQueueDispatchArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		QueueID:   "queue-id",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}
