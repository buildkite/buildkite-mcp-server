package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type mockClusterQueuesClient struct {
	ListFunc func(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error)
	GetFunc  func(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error)
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
