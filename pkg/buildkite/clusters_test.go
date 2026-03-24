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

var _ ClustersClient = (*mockClustersClient)(nil)

type mockClustersClient struct {
	ListFunc   func(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error)
	GetFunc    func(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error)
	CreateFunc func(ctx context.Context, org string, cc buildkite.ClusterCreate) (buildkite.Cluster, *buildkite.Response, error)
	UpdateFunc func(ctx context.Context, org, id string, cu buildkite.ClusterUpdate) (buildkite.Cluster, *buildkite.Response, error)
	DeleteFunc func(ctx context.Context, org, id string) (*buildkite.Response, error)
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

func (m *mockClustersClient) Create(ctx context.Context, org string, cc buildkite.ClusterCreate) (buildkite.Cluster, *buildkite.Response, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, org, cc)
	}
	return buildkite.Cluster{}, nil, nil
}

func (m *mockClustersClient) Update(ctx context.Context, org, id string, cu buildkite.ClusterUpdate) (buildkite.Cluster, *buildkite.Response, error) {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, org, id, cu)
	}
	return buildkite.Cluster{}, nil, nil
}

func (m *mockClustersClient) Delete(ctx context.Context, org, id string) (*buildkite.Response, error) {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, org, id)
	}
	return nil, nil
}

func TestListClusters(t *testing.T) {
	assert := require.New(t)

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

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	tool, handler, _ := ListClusters()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, ListClustersArgs{
		OrgSlug: "org",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"headers":{"Link":""},"items":[{"id":"cluster-id","name":"cluster-name","created_by":{},"maintainers":{}}]}`, textContent.Text)
}

func TestGetCluster(t *testing.T) {
	assert := require.New(t)

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

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	tool, handler, _ := GetCluster()
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetClusterArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq("{\"id\":\"cluster-id\",\"name\":\"cluster-name\",\"created_by\":{},\"maintainers\":{}}", textContent.Text)
}

func TestCreateCluster(t *testing.T) {
	assert := require.New(t)

	client := &mockClustersClient{
		CreateFunc: func(ctx context.Context, org string, cc buildkite.ClusterCreate) (buildkite.Cluster, *buildkite.Response, error) {
			return buildkite.Cluster{
					ID:   "new-cluster-id",
					Name: cc.Name,
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 201,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	tool, handler, scopes := CreateCluster()
	assert.Equal("create_cluster", tool.Name)
	assert.Equal(boolPtr(false), tool.Annotations.DestructiveHint)
	assert.Contains(scopes, "write_clusters")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, CreateClusterArgs{
		OrgSlug: "org",
		Name:    "my-cluster",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"new-cluster-id","name":"my-cluster","created_by":{},"maintainers":{}}`, textContent.Text)
}

func TestUpdateCluster(t *testing.T) {
	assert := require.New(t)

	client := &mockClustersClient{
		UpdateFunc: func(ctx context.Context, org, id string, cu buildkite.ClusterUpdate) (buildkite.Cluster, *buildkite.Response, error) {
			return buildkite.Cluster{
					ID:   id,
					Name: cu.Name,
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	tool, handler, scopes := UpdateCluster()
	assert.Equal("update_cluster", tool.Name)
	assert.Equal(boolPtr(false), tool.Annotations.DestructiveHint)
	assert.Contains(scopes, "write_clusters")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, UpdateClusterArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		Name:      "updated-name",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.JSONEq(`{"id":"cluster-id","name":"updated-name","created_by":{},"maintainers":{}}`, textContent.Text)
}

func TestDeleteCluster(t *testing.T) {
	assert := require.New(t)

	client := &mockClustersClient{
		DeleteFunc: func(ctx context.Context, org, id string) (*buildkite.Response, error) {
			return &buildkite.Response{
				Response: &http.Response{
					StatusCode: 204,
				},
			}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	tool, handler, scopes := DeleteCluster()
	assert.Equal("delete_cluster", tool.Name)
	assert.Equal(boolPtr(true), tool.Annotations.DestructiveHint)
	assert.Contains(scopes, "write_clusters")

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, DeleteClusterArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
	})
	assert.NoError(err)

	textContent := getTextResult(t, result)
	assert.Contains(textContent.Text, "Cluster deleted successfully")
}

func TestCreateClusterWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClustersClient{
		CreateFunc: func(ctx context.Context, org string, cc buildkite.ClusterCreate) (buildkite.Cluster, *buildkite.Response, error) {
			return buildkite.Cluster{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	_, handler, _ := CreateCluster()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, CreateClusterArgs{
		OrgSlug: "org",
		Name:    "my-cluster",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestUpdateClusterWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClustersClient{
		UpdateFunc: func(ctx context.Context, org, id string, cu buildkite.ClusterUpdate) (buildkite.Cluster, *buildkite.Response, error) {
			return buildkite.Cluster{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	_, handler, _ := UpdateCluster()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, UpdateClusterArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		Name:      "updated-name",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestDeleteClusterWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClustersClient{
		DeleteFunc: func(ctx context.Context, org, id string) (*buildkite.Response, error) {
			return &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: client})

	_, handler, _ := DeleteCluster()

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, DeleteClusterArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
	})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}
