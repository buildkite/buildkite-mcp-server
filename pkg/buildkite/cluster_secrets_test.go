package buildkite

import (
	"context"
	"fmt"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type mockClusterSecretsClient struct {
	GetFunc func(
		ctx context.Context,
		org string,
		clusterID string,
		secretID string,
	) (buildkite.ClusterSecret, *buildkite.Response, error)
}

func (m *mockClusterSecretsClient) Get(
	ctx context.Context,
	org string,
	clusterID string,
	secretID string,
) (buildkite.ClusterSecret, *buildkite.Response, error) {
	return m.GetFunc(ctx, org, clusterID, secretID)
}

var _ ClusterSecretsClient = (*mockClusterSecretsClient)(nil)

func TestGetClusterSecret(t *testing.T) {
	client := &mockClusterSecretsClient{
		GetFunc: func(
			ctx context.Context,
			org string,
			clusterID string,
			secretID string,
		) (buildkite.ClusterSecret, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "cluster-id", clusterID)
			require.Equal(t, "secret-id", secretID)

			return buildkite.ClusterSecret{
				ID:          "secret-id",
				Key:         "DATABASE_PASSWORD",
				Description: "Database password",
				Policy:      "- pipeline_slug: example",
			}, nil, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		ClusterSecretsClient: client,
	})

	tool, handler, scopes := GetClusterSecret()

	require.Equal(t, "get_cluster_secret", tool.Name)
	require.NotNil(t, tool.Annotations)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_secrets_details"}, scopes)

	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetClusterSecretArgs{
		OrgSlug:   "org",
		ClusterID: "cluster-id",
		SecretID:  "secret-id",
	})
	require.NoError(t, err)

	textContent := getTextResult(t, result)
	require.JSONEq(t, `{
		"id": "secret-id",
		"key": "DATABASE_PASSWORD",
		"description": "Database password",
		"policy": "- pipeline_slug: example",
		"created_by": {},
		"organization": {}
	}`, textContent.Text)
	require.NotContains(t, textContent.Text, `"value"`)
}

func TestGetClusterSecretWithError(t *testing.T) {
	assert := require.New(t)

	client := &mockClusterSecretsClient{
		GetFunc: func(
			ctx context.Context,
			org string,
			clusterID string,
			secretID string,
		) (buildkite.ClusterSecret, *buildkite.Response, error) {
			return buildkite.ClusterSecret{}, &buildkite.Response{}, fmt.Errorf("API error")
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{ClusterSecretsClient: client})

	_, handler, _ := GetClusterSecret()
	request := createMCPRequest(t, map[string]any{})
	result, _, err := handler(ctx, request, GetClusterSecretArgs{OrgSlug: "org", ClusterID: "cluster-id", SecretID: "secret-id"})
	assert.NoError(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*mcp.TextContent).Text, "API error")
}
