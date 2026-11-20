package buildkite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestGetRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "distinct exhausted caller buckets",
			status: http.StatusOK,
			body: `{"scopes":{
				"rest":{"limit":200,"current":17,"reset":12,"reset_at":"2026-10-23T01:02:12Z","enforced":true},
				"graphql":{"limit":20000,"current":300,"reset":120,"reset_at":"2026-10-23T01:04:00Z","enforced":false},
				"rest_user":{"limit":50,"current":50,"reset":38,"reset_at":"2026-10-23T01:02:38Z","enforced":true},
				"mcp":{"limit":50,"current":51,"reset":49,"reset_at":"2026-10-23T01:02:49Z","enforced":true}
			}}`,
		},
		{
			name:   "compute caller buckets remain null",
			status: http.StatusOK,
			body: `{"scopes":{
				"rest":{"limit":200,"current":9,"reset":20,"reset_at":"2026-10-23T01:02:20Z","enforced":true},
				"graphql":{"limit":20000,"current":0,"reset":0,"reset_at":"2026-10-23T01:02:00Z","enforced":true},
				"rest_user":null,"mcp":null
			}}`,
		},
		{
			name:   "missing scope",
			status: http.StatusForbidden,
			body:   `{"message":"Your access token doesn't have the read_organizations scope"}`,
		},
		{
			name:   "unauthorized",
			status: http.StatusUnauthorized,
			body:   `{"message":"Unauthorized"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.EscapedPath() != "/proxy/v2/organizations/acme%2Fteam/rate_limit" || r.Header.Get("Authorization") != "Bearer fake-token" {
					t.Errorf("unexpected request: %s %s (authorization present: %t)", r.Method, r.URL, r.Header.Get("Authorization") != "")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			client, err := buildkite.NewOpts(
				buildkite.WithTokenAuth("fake-token"),
				buildkite.WithBaseURL(srv.URL+"/proxy/"),
				buildkite.WithMaxRetries(0),
			)
			require.NoError(t, err)
			ctx := ContextWithDeps(context.Background(), ToolDependencies{
				RateLimitClient: &BuildkiteClientAdapter{Client: client},
			})

			_, handler, _ := GetRateLimit()
			result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetRateLimitArgs{OrgSlug: "acme/team"})
			if tc.status == http.StatusUnauthorized {
				require.ErrorIs(t, err, ErrUnauthorized)
				require.Nil(t, result)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.status != http.StatusOK, result.IsError)
			require.JSONEq(t, tc.body, getTextResult(t, result).Text)
		})
	}
}
