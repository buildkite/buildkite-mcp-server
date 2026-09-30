package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type mockCacheRegistriesClient struct {
	list   func(context.Context, string, string, *buildkite.CacheRegistriesListOptions) (buildkite.CacheRegistriesList, *buildkite.Response, error)
	get    func(context.Context, string, string, string) (buildkite.CacheRegistry, *buildkite.Response, error)
	create func(context.Context, string, string, buildkite.CacheRegistryCreate) (buildkite.CacheRegistry, *buildkite.Response, error)
	update func(context.Context, string, string, string, buildkite.CacheRegistryUpdate) (buildkite.CacheRegistry, *buildkite.Response, error)
	delete func(context.Context, string, string, string) (*buildkite.Response, error)
}

func (m *mockCacheRegistriesClient) List(ctx context.Context, org, clusterID string, opt *buildkite.CacheRegistriesListOptions) (buildkite.CacheRegistriesList, *buildkite.Response, error) {
	return m.list(ctx, org, clusterID, opt)
}

func (m *mockCacheRegistriesClient) Get(ctx context.Context, org, clusterID, registryUUID string) (buildkite.CacheRegistry, *buildkite.Response, error) {
	return m.get(ctx, org, clusterID, registryUUID)
}

func (m *mockCacheRegistriesClient) Create(ctx context.Context, org, clusterID string, input buildkite.CacheRegistryCreate) (buildkite.CacheRegistry, *buildkite.Response, error) {
	return m.create(ctx, org, clusterID, input)
}

func (m *mockCacheRegistriesClient) Update(ctx context.Context, org, clusterID, registryUUID string, input buildkite.CacheRegistryUpdate) (buildkite.CacheRegistry, *buildkite.Response, error) {
	return m.update(ctx, org, clusterID, registryUUID, input)
}

func (m *mockCacheRegistriesClient) Delete(ctx context.Context, org, clusterID, registryUUID string) (*buildkite.Response, error) {
	return m.delete(ctx, org, clusterID, registryUUID)
}

var _ CacheRegistriesClient = (*mockCacheRegistriesClient)(nil)

func TestListCacheRegistries(t *testing.T) {
	client := &mockCacheRegistriesClient{
		list: func(_ context.Context, org, clusterID string, opt *buildkite.CacheRegistriesListOptions) (buildkite.CacheRegistriesList, *buildkite.Response, error) {
			require.Equal(t, "acme", org)
			require.Equal(t, "cluster-uuid", clusterID)
			require.Equal(t, "next-cursor", opt.After)
			require.Empty(t, opt.Before)
			require.Equal(t, 50, opt.PerPage)
			return buildkite.CacheRegistriesList{
				Items: []buildkite.CacheRegistry{{
					UUID:    "registry-uuid",
					Slug:    "registry-slug",
					Name:    "Registry",
					Policy:  buildkite.CacheRegistryPolicy{"pipelines": []any{"one", "two"}},
					Default: true,
				}, {
					UUID: "other-registry-uuid",
					Slug: "other-registry-slug",
					Name: "Other registry",
				}},
				Links: buildkite.CacheRegistriesListLinks{
					Next: "https://api.buildkite.com/v2/organizations/acme/clusters/cluster-uuid/cache-registries?after=another-cursor&per_page=50",
				},
			}, nil, nil
		},
	}

	tool, handler, scopes := ListCacheRegistries()
	require.Equal(t, "list_cache_registries", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_clusters"}, scopes)

	result, _, err := handler(cacheRegistriesContext(client), createMCPRequest(t, map[string]any{}), ListCacheRegistriesArgs{
		OrgSlug: "acme", ClusterID: "cluster-uuid", After: "next-cursor", PerPage: 50,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, "registry-uuid", "items", 0, "uuid")
	requireJSONPathEqual(t, text, []any{"one", "two"}, "items", 0, "policy", "pipelines")
	requireJSONPathEqual(t, text, true, "items", 0, "default")
	requireJSONPathEqual(t, text, false, "items", 1, "default")
	requireJSONPathEqual(t, text, "https://api.buildkite.com/v2/organizations/acme/clusters/cluster-uuid/cache-registries?after=another-cursor&per_page=50", "links", "next")
}

func TestListCacheRegistriesRejectsBothCursors(t *testing.T) {
	_, handler, _ := ListCacheRegistries()
	result, _, err := handler(context.Background(), createMCPRequest(t, map[string]any{}), ListCacheRegistriesArgs{After: "a", Before: "b"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "mutually exclusive")
}

func TestGetCacheRegistryUsesUUID(t *testing.T) {
	client := &mockCacheRegistriesClient{
		get: func(_ context.Context, org, clusterID, registryUUID string) (buildkite.CacheRegistry, *buildkite.Response, error) {
			require.Equal(t, "acme", org)
			require.Equal(t, "cluster-uuid", clusterID)
			require.Equal(t, "registry-uuid", registryUUID)
			return buildkite.CacheRegistry{UUID: registryUUID, Name: "Registry"}, nil, nil
		},
	}

	tool, handler, scopes := GetCacheRegistry()
	require.Equal(t, "get_cache_registry", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_clusters"}, scopes)

	result, _, err := handler(cacheRegistriesContext(client), createMCPRequest(t, map[string]any{}), GetCacheRegistryArgs{
		OrgSlug: "acme", ClusterID: "cluster-uuid", RegistryUUID: "registry-uuid",
	})
	require.NoError(t, err)
	requireJSONPathEqual(t, getTextResult(t, result).Text, "registry-uuid", "uuid")
}

func TestCreateCacheRegistryPreservesOmittedSetAndClearFields(t *testing.T) {
	empty := ""
	tests := []struct {
		name     string
		args     CreateCacheRegistryArgs
		wantJSON string
	}{
		{
			name:     "omit optional fields",
			args:     CreateCacheRegistryArgs{Name: "Registry"},
			wantJSON: `{"name":"Registry"}`,
		},
		{
			name: "set empty values and clear nullable values",
			args: CreateCacheRegistryArgs{
				Name: "Registry", Description: &empty,
				Policy: cacheRegistryPolicyPtr(buildkite.CacheRegistryPolicy{}),
			},
			wantJSON: `{"name":"Registry","description":"","emoji":null,"policy":{}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockCacheRegistriesClient{
				create: func(_ context.Context, org, clusterID string, input buildkite.CacheRegistryCreate) (buildkite.CacheRegistry, *buildkite.Response, error) {
					require.Equal(t, "acme", org)
					require.Equal(t, "cluster-uuid", clusterID)
					body, err := json.Marshal(input)
					require.NoError(t, err)
					require.JSONEq(t, tt.wantJSON, string(body))
					return buildkite.CacheRegistry{UUID: "registry-uuid", Name: input.Name}, nil, nil
				},
			}

			tt.args.OrgSlug = "acme"
			tt.args.ClusterID = "cluster-uuid"
			tool, handler, scopes := CreateCacheRegistry()
			require.Equal(t, "create_cache_registry", tool.Name)
			require.Equal(t, boolPtr(false), tool.Annotations.DestructiveHint)
			require.Equal(t, []string{"write_clusters"}, scopes)

			requestArgs := map[string]any{}
			if tt.name == "set empty values and clear nullable values" {
				requestArgs = map[string]any{"description": "", "emoji": nil, "policy": map[string]any{}}
			}
			result, _, err := handler(cacheRegistriesContext(client), createMCPRequest(t, requestArgs), tt.args)
			require.NoError(t, err)
			require.False(t, result.IsError)
		})
	}
}

func TestUpdateCacheRegistryPreservesOmittedSetAndClearFields(t *testing.T) {
	client := &mockCacheRegistriesClient{
		update: func(_ context.Context, org, clusterID, registryUUID string, input buildkite.CacheRegistryUpdate) (buildkite.CacheRegistry, *buildkite.Response, error) {
			require.Equal(t, "acme", org)
			require.Equal(t, "cluster-uuid", clusterID)
			require.Equal(t, "registry-uuid", registryUUID)
			body, err := json.Marshal(input)
			require.NoError(t, err)
			require.JSONEq(t, `{"name":"","description":null,"emoji":"","policy":null}`, string(body))
			return buildkite.CacheRegistry{UUID: registryUUID}, nil, nil
		},
	}

	tool, handler, scopes := UpdateCacheRegistry()
	require.Equal(t, "update_cache_registry", tool.Name)
	require.Equal(t, boolPtr(true), tool.Annotations.DestructiveHint)
	require.Equal(t, []string{"write_clusters"}, scopes)

	result, _, err := handler(cacheRegistriesContext(client), createMCPRequest(t, map[string]any{
		"name": "", "description": nil, "emoji": "", "policy": nil,
	}), UpdateCacheRegistryArgs{
		OrgSlug: "acme", ClusterID: "cluster-uuid", RegistryUUID: "registry-uuid",
		Name: "", Emoji: stringPtr(""),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func TestUpdateCacheRegistryPreservesNullsThroughMCP(t *testing.T) {
	client := &mockCacheRegistriesClient{
		update: func(_ context.Context, _, _, _ string, input buildkite.CacheRegistryUpdate) (buildkite.CacheRegistry, *buildkite.Response, error) {
			body, err := json.Marshal(input)
			require.NoError(t, err)
			require.JSONEq(t, `{"name":"","description":null,"policy":null}`, string(body))
			return buildkite.CacheRegistry{UUID: "registry-uuid"}, nil, nil
		},
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	server.AddReceivingMiddleware(InjectDepsMiddleware(ToolDependencies{CacheRegistriesClient: client}))
	tool, handler, _ := UpdateCacheRegistry()
	mcp.AddTool(server, &tool, handler)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "update_cache_registry",
		Arguments: map[string]any{
			"org_slug": "acme", "cluster_id": "cluster-uuid", "registry_uuid": "registry-uuid",
			"name": "", "description": nil, "policy": nil,
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func TestUpdateCacheRegistryHandlesAPIError(t *testing.T) {
	client := &mockCacheRegistriesClient{
		update: func(context.Context, string, string, string, buildkite.CacheRegistryUpdate) (buildkite.CacheRegistry, *buildkite.Response, error) {
			return buildkite.CacheRegistry{}, nil, errors.New("API error")
		},
	}

	_, handler, _ := UpdateCacheRegistry()
	result, _, err := handler(cacheRegistriesContext(client), createMCPRequest(t, map[string]any{}), UpdateCacheRegistryArgs{})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "API error")
}

func TestSetDefaultCacheRegistry(t *testing.T) {
	clusters := &mockClustersClient{
		UpdateFunc: func(_ context.Context, org, id string, cu buildkite.ClusterUpdate) (buildkite.Cluster, *buildkite.Response, error) {
			require.Equal(t, "acme", org)
			require.Equal(t, "cluster-uuid", id)
			body, err := json.Marshal(cu)
			require.NoError(t, err)
			require.JSONEq(t, `{"default_cache_registry_uuid":"registry-uuid"}`, string(body))
			return buildkite.Cluster{ID: id, DefaultCacheRegistryUUID: "registry-uuid"}, nil, nil
		},
	}

	tool, handler, scopes := SetDefaultCacheRegistry()
	require.Equal(t, "set_default_cache_registry", tool.Name)
	require.False(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, boolPtr(false), tool.Annotations.DestructiveHint)
	require.True(t, tool.Annotations.IdempotentHint)
	require.Equal(t, []string{"write_clusters"}, scopes)

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: clusters})
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), SetDefaultCacheRegistryArgs{
		OrgSlug: "acme", ClusterID: "cluster-uuid", RegistryUUID: "registry-uuid",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	requireJSONPathEqual(t, getTextResult(t, result).Text, "registry-uuid", "default_cache_registry_uuid")
}

func TestSetDefaultCacheRegistryReturnsAPIValidationError(t *testing.T) {
	message := `{"message":"default_cache_registry_uuid must be the UUID of a cache registry in this cluster"}`
	clusters := &mockClustersClient{
		UpdateFunc: func(context.Context, string, string, buildkite.ClusterUpdate) (buildkite.Cluster, *buildkite.Response, error) {
			return buildkite.Cluster{}, nil, &buildkite.ErrorResponse{RawBody: []byte(message)}
		},
	}

	_, handler, _ := SetDefaultCacheRegistry()
	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClustersClient: clusters})
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), SetDefaultCacheRegistryArgs{
		OrgSlug: "acme", ClusterID: "cluster-uuid", RegistryUUID: "ruby-gems",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "must be the UUID of a cache registry in this cluster")
}

func TestDeleteCacheRegistry(t *testing.T) {
	client := &mockCacheRegistriesClient{
		delete: func(_ context.Context, org, clusterID, registryUUID string) (*buildkite.Response, error) {
			require.Equal(t, "acme", org)
			require.Equal(t, "cluster-uuid", clusterID)
			require.Equal(t, "registry-uuid", registryUUID)
			return &buildkite.Response{Response: &http.Response{StatusCode: http.StatusNoContent}}, nil
		},
	}

	tool, handler, scopes := DeleteCacheRegistry()
	require.Equal(t, "delete_cache_registry", tool.Name)
	require.False(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, boolPtr(true), tool.Annotations.DestructiveHint)
	require.Contains(t, tool.Description, "cache metadata")
	require.Contains(t, tool.Description, "default cache registry can't be deleted")
	require.Equal(t, []string{"write_clusters"}, scopes)

	result, _, err := handler(cacheRegistriesContext(client), createMCPRequest(t, map[string]any{}), DeleteCacheRegistryArgs{
		OrgSlug: "acme", ClusterID: "cluster-uuid", RegistryUUID: "registry-uuid",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, true, "deleted")
	requireJSONPathEqual(t, text, "registry-uuid", "registry_uuid")
}

func TestDeleteCacheRegistryRejectsDefaultRegistry(t *testing.T) {
	client := &mockCacheRegistriesClient{
		delete: func(context.Context, string, string, string) (*buildkite.Response, error) {
			return nil, &buildkite.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusUnprocessableEntity},
				RawBody:  []byte(`{"message":"Unable to destroy cache registry. Cannot destroy default cache registry"}`),
			}
		},
	}

	_, handler, _ := DeleteCacheRegistry()
	result, _, err := handler(cacheRegistriesContext(client), createMCPRequest(t, map[string]any{}), DeleteCacheRegistryArgs{
		OrgSlug: "acme", ClusterID: "cluster-uuid", RegistryUUID: "registry-uuid",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "Cannot destroy default cache registry")
}

func cacheRegistriesContext(client CacheRegistriesClient) context.Context {
	return ContextWithDeps(context.Background(), ToolDependencies{CacheRegistriesClient: client})
}

func cacheRegistryPolicyPtr(policy buildkite.CacheRegistryPolicy) *buildkite.CacheRegistryPolicy {
	return &policy
}

func stringPtr(value string) *string {
	return &value
}
