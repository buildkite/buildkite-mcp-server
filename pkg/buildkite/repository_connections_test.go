package buildkite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

type mockRepositoryConnectionsClient struct {
	list func(context.Context, string) ([]buildkite.RepositoryConnection, *buildkite.Response, error)
	get  func(context.Context, string, string) (buildkite.RepositoryConnection, *buildkite.Response, error)
}

func (m *mockRepositoryConnectionsClient) List(ctx context.Context, org string) ([]buildkite.RepositoryConnection, *buildkite.Response, error) {
	return m.list(ctx, org)
}

func (m *mockRepositoryConnectionsClient) Get(ctx context.Context, org, id string) (buildkite.RepositoryConnection, *buildkite.Response, error) {
	return m.get(ctx, org, id)
}

var _ RepositoryConnectionsClient = (*mockRepositoryConnectionsClient)(nil)

func TestListRepositoryConnections(t *testing.T) {
	client := &mockRepositoryConnectionsClient{
		list: func(_ context.Context, org string) ([]buildkite.RepositoryConnection, *buildkite.Response, error) {
			require.Equal(t, "acme", org)
			return []buildkite.RepositoryConnection{
				{ID: "github-uuid", Type: "github_code_access_app", DisplayName: "GitHub (acme)"},
				{ID: "gitlab-uuid", Type: "gitlab_self_managed", DisplayName: "GitLab Self-Managed"},
			}, nil, nil
		},
	}

	tool, handler, scopes := ListRepositoryConnections()
	require.Equal(t, "list_repository_connections", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_organization_repository_connections"}, scopes)

	result, _, err := handler(repositoryConnectionsContext(client), createMCPRequest(t, map[string]any{}), ListRepositoryConnectionsArgs{OrgSlug: "acme"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, "github-uuid", 0, "id")
	requireJSONPathEqual(t, text, "gitlab_self_managed", 1, "type")
}

func TestGetRepositoryConnectionReturnsProviderRateLimit(t *testing.T) {
	client := &mockRepositoryConnectionsClient{
		get: func(_ context.Context, org, id string) (buildkite.RepositoryConnection, *buildkite.Response, error) {
			require.Equal(t, "acme", org)
			require.Equal(t, "github-uuid", id)
			return buildkite.RepositoryConnection{
				ID:             id,
				Type:           "github_code_access_app",
				ServiceAccount: &buildkite.RepositoryConnectionServiceAccount{Login: "acme"},
				Host:           &buildkite.RepositoryConnectionHost{Type: "github", URL: "https://github.com"},
				RateLimit: &buildkite.RepositoryConnectionRateLimit{
					Limit:     5000,
					Used:      42,
					Remaining: 4958,
					ResetAt:   time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC),
				},
			}, nil, nil
		},
	}

	tool, handler, scopes := GetRepositoryConnection()
	require.Equal(t, "get_repository_connection", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Contains(t, tool.Description, "not the Buildkite API rate limit")
	require.Equal(t, []string{"read_organization_repository_connections"}, scopes)

	result, _, err := handler(repositoryConnectionsContext(client), createMCPRequest(t, map[string]any{}), GetRepositoryConnectionArgs{
		OrgSlug: "acme", ConnectionID: "github-uuid",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, "github-uuid", "id")
	requireJSONPathEqual(t, text, "acme", "service_account", "login")
	requireJSONPathEqual(t, text, float64(5000), "rate_limit", "limit")
	requireJSONPathEqual(t, text, float64(42), "rate_limit", "used")
	requireJSONPathEqual(t, text, float64(4958), "rate_limit", "remaining")
	requireJSONPathEqual(t, text, "2026-07-16T06:00:00Z", "rate_limit", "reset_at")
}

func TestGetRepositoryConnectionReportsUnavailableRateLimitAsNull(t *testing.T) {
	client := &mockRepositoryConnectionsClient{
		get: func(_ context.Context, _, id string) (buildkite.RepositoryConnection, *buildkite.Response, error) {
			return buildkite.RepositoryConnection{ID: id, Type: "gitlab_self_managed"}, nil, nil
		},
	}

	_, handler, _ := GetRepositoryConnection()
	result, _, err := handler(repositoryConnectionsContext(client), createMCPRequest(t, map[string]any{}), GetRepositoryConnectionArgs{
		OrgSlug: "acme", ConnectionID: "gitlab-uuid",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	requireJSONPathEqual(t, getTextResult(t, result).Text, nil, "rate_limit")
}

func TestGetRepositoryConnectionHandlesAPIError(t *testing.T) {
	client := &mockRepositoryConnectionsClient{
		get: func(context.Context, string, string) (buildkite.RepositoryConnection, *buildkite.Response, error) {
			return buildkite.RepositoryConnection{}, nil, errors.New("API error")
		},
	}

	_, handler, _ := GetRepositoryConnection()
	result, _, err := handler(repositoryConnectionsContext(client), createMCPRequest(t, map[string]any{}), GetRepositoryConnectionArgs{})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "API error")
}

func repositoryConnectionsContext(client RepositoryConnectionsClient) context.Context {
	return ContextWithDeps(context.Background(), ToolDependencies{RepositoryConnectionsClient: client})
}
