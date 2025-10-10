package buildkite

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/stretchr/testify/require"
)

type MockOrganizationsClient struct {
	ListFunc func(ctx context.Context, options *buildkite.OrganizationListOptions) ([]buildkite.Organization, *buildkite.Response, error)
}

func (m *MockOrganizationsClient) List(ctx context.Context, options *buildkite.OrganizationListOptions) ([]buildkite.Organization, *buildkite.Response, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx, options)
	}
	return nil, nil, nil
}

func TestUserTokenOrganization(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockOrganizationsClient{
		ListFunc: func(ctx context.Context, options *buildkite.OrganizationListOptions) ([]buildkite.Organization, *buildkite.Response, error) {
			return []buildkite.Organization{
					{
						Slug: "test-org",
						Name: "Test Organization",
					},
				}, &buildkite.Response{
					Response: &http.Response{
						StatusCode: 200,
					},
				}, nil
		},
	}

	tool, handler, _ := UserTokenOrganization(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.JSONEq(`{"name":"Test Organization","slug":"test-org"}`, textContent.Text)
}

func TestUserTokenOrganizationError(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockOrganizationsClient{
		ListFunc: func(ctx context.Context, options *buildkite.OrganizationListOptions) ([]buildkite.Organization, *buildkite.Response, error) {
			resp := &http.Response{
				Request:    &http.Request{Method: "GET"},
				StatusCode: 500,
			}
			return nil, &buildkite.Response{
				Response: resp,
			}, &buildkite.ErrorResponse{Response: resp, Message: "Internal Server Error"}
		},
	}

	tool, handler, _ := UserTokenOrganization(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, err := handler(ctx, request)
	assert.NoError(err)
	assert.Contains(getTextResult(t, result).Text, "Internal Server Error")
}

func TestUserTokenOrganizationErrorNoOrganization(t *testing.T) {
	assert := require.New(t)

	ctx := context.Background()
	client := &MockOrganizationsClient{
		ListFunc: func(ctx context.Context, options *buildkite.OrganizationListOptions) ([]buildkite.Organization, *buildkite.Response, error) {
			return nil, &buildkite.Response{
				Response: &http.Response{
					StatusCode: 200,
				},
			}, nil
		},
	}

	tool, handler, _ := UserTokenOrganization(client)
	assert.NotNil(tool)
	assert.NotNil(handler)

	request := createMCPRequest(t, map[string]any{})
	result, err := handler(ctx, request)
	assert.NoError(err)

	textContent := getTextResult(t, result)

	assert.Equal("no organization found for the current user token", textContent.Text)
}
