package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockUserClient struct {
	CurrentUserFunc func(ctx context.Context) (buildkite.User, *buildkite.Response, error)
}

func (m *MockUserClient) CurrentUser(ctx context.Context) (buildkite.User, *buildkite.Response, error) {
	if m.CurrentUserFunc != nil {
		return m.CurrentUserFunc(ctx)
	}
	return buildkite.User{}, nil, nil
}

func TestCurrentUser(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockUserClient{
		CurrentUserFunc: func(ctx context.Context) (buildkite.User, *buildkite.Response, error) {
			return buildkite.User{
					ID:        "123",
					Name:      "Test User",
					Email:     "user@example.com",
					CreatedAt: &buildkite.Timestamp{},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler, scopes := CurrentUser(client)
	assert.Equal([]string{"read_user"}, scopes)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.JSONEq(`{"id":"123","name":"Test User","email":"user@example.com","created_at":"0001-01-01T00:00:00Z"}`, textContent.Text)
}
