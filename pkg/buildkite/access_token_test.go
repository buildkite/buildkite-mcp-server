package buildkite

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockAccessTokenClient struct {
	GetFunc func(ctx context.Context) (buildkite.AccessToken, *buildkite.Response, error)
}

func (m *MockAccessTokenClient) Get(ctx context.Context) (buildkite.AccessToken, *buildkite.Response, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx)
	}
	return buildkite.AccessToken{}, nil, nil
}

func TestAccessToken(t *testing.T) {
	assert := require.New(t)
	testTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	ctx := context.Background()
	client := &MockAccessTokenClient{
		GetFunc: func(ctx context.Context) (buildkite.AccessToken, *buildkite.Response, error) {
			return buildkite.AccessToken{
					UUID:        "123",
					Scopes:      []string{"read_build", "read_pipeline"},
					Description: "Test token",
					User: struct {
						Name  string `json:"name"`
						Email string `json:"email"`
					}{
						Name:  "Test User",
						Email: "test@example.com",
					},
					CreatedAt: &buildkite.Timestamp{Time: testTime},

					// Add other fields as needed
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(`{"id": "123"}`)),
					},
				}, nil
		},
	}

	tool, handler, _ := AccessToken(client)
	assert.NotNil(t, tool)
	assert.NotNil(t, handler)

	request := createMCPRequest(t, map[string]any{})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.JSONEq(`{"uuid":"123","scopes":["read_build","read_pipeline"],"description":"Test token","created_at":"2023-01-01T00:00:00Z","user":{"name":"Test User","email":"test@example.com"}}`, textContent.Text)
}
