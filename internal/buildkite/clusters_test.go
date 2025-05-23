package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

var _ ClustersClient = (*mockClustersClient)(nil)

type mockClustersClient struct {
	ListFunc func(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error)
	GetFunc  func(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error)
}

func (m *mockClustersClient) List(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, org, opts)
	}
	return nil, nil, nil
}
func (m *mockClustersClient) Get(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, org, id)
	}
	return buildkite.Cluster{}, nil, nil
}

func TestListClusters(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &mockClustersClient{
		ListFunc: func(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error) {
			return []buildkite.Cluster{
					{
						ID:   "cluster-id",
						Name: "cluster-name",
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler := ListClusters(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org": "org",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Equal("[{\"id\":\"cluster-id\",\"name\":\"cluster-name\",\"created_by\":{}}]", textContent.Text)
}

func TestGetCluster(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &mockClustersClient{
		GetFunc: func(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error) {
			return buildkite.Cluster{
					ID:   "cluster-id",
					Name: "cluster-name",
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler := GetCluster(ctx, client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{
		"org":        "org",
		"cluster_id": "cluster-id",
	})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Equal("{\"id\":\"cluster-id\",\"name\":\"cluster-name\",\"created_by\":{}}", textContent.Text)
}
