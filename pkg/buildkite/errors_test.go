package buildkite

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestHandleBuildkiteError_Unauthorized(t *testing.T) {
	errResp := &buildkite.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusUnauthorized},
	}

	result, data, err := handleBuildkiteError(errResp)

	require.Nil(t, result)
	require.Nil(t, data)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestHandleBuildkiteError_Forbidden(t *testing.T) {
	errResp := &buildkite.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusForbidden},
		Message:  "Your access token doesn't have the read_suites scope",
	}

	result, data, err := handleBuildkiteError(errResp)

	require.NoError(t, err)
	require.Nil(t, data)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	textContent := getTextResult(t, result)
	require.Equal(t, "Your access token doesn't have the read_suites scope", textContent.Text)
}

func TestHandleBuildkiteError_WithRawBody(t *testing.T) {
	errResp := &buildkite.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusUnprocessableEntity},
		RawBody:  []byte(`{"message":"validation failed"}`),
	}

	result, data, err := handleBuildkiteError(errResp)

	require.NoError(t, err)
	require.Nil(t, data)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	textContent := getTextResult(t, result)
	require.JSONEq(t, `{"message":"validation failed"}`, textContent.Text)
}

func TestHandleBuildkiteError_RateLimited(t *testing.T) {
	userLimit := "You have exceeded your rest_user API rate limit. Please wait 60 seconds before making more requests."
	for name, tc := range map[string]struct {
		header  http.Header
		message string
		want    string
	}{
		// When the per-user limit is exceeded, RateLimit-Reset still reports
		// the organization's window, so the API's message must win.
		"UserLimitMessage":         {http.Header{"Ratelimit-Reset": []string{"35"}, "Ratelimit-User-Reset": []string{"60"}}, userLimit, "Please wait 60 seconds"},
		"NoMessageReset":           {http.Header{"Ratelimit-Reset": []string{"42"}}, "", "The limit resets in 42 seconds"},
		"NoMessageUserResetLonger": {http.Header{"Ratelimit-Reset": []string{"3"}, "Ratelimit-User-Reset": []string{"60"}}, "", "The limit resets in 60 seconds"},
		"NoMessageOrgResetLonger":  {http.Header{"Ratelimit-Reset": []string{"42"}, "Ratelimit-User-Reset": []string{"3"}}, "", "The limit resets in 42 seconds"},
		"NoMessageInvalidOrgReset": {http.Header{"Ratelimit-Reset": []string{"invalid"}, "Ratelimit-User-Reset": []string{"60"}}, "", "The limit resets in 60 seconds"},
		"NoMessageNoReset":         {http.Header{}, "", "Retry in a minute"},
	} {
		t.Run(name, func(t *testing.T) {
			errResp := &buildkite.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusTooManyRequests, Header: tc.header},
				Message:  tc.message,
			}

			result, _, err := handleBuildkiteError(fmt.Errorf("failed to fetch logs from API: %w", errResp))

			require.NoError(t, err)
			require.True(t, result.IsError)
			text := getTextResult(t, result).Text
			require.Contains(t, text, "Buildkite API rate limit exceeded (HTTP 429)")
			require.Contains(t, text, tc.want)
			require.Contains(t, text, "If available, use get_rate_limit with the affected org_slug")
			require.NotContains(t, text, "35 seconds")
		})
	}
}

func TestHandleBuildkiteError_GenericError(t *testing.T) {
	genericErr := fmt.Errorf("connection refused")

	result, data, err := handleBuildkiteError(genericErr)

	require.NoError(t, err)
	require.Nil(t, data)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	textContent := getTextResult(t, result)
	require.Equal(t, "connection refused", textContent.Text)
}

func TestHandleBuildkiteError_NonUnauthorizedWithMessage(t *testing.T) {
	errResp := &buildkite.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusNotFound},
		Message:  "pipeline not found",
	}

	result, data, err := handleBuildkiteError(errResp)

	require.NoError(t, err)
	require.Nil(t, data)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	textContent := getTextResult(t, result)
	require.Equal(t, "pipeline not found", textContent.Text)
}

func TestHandleBuildkiteError_NilResponse(t *testing.T) {
	// ErrorResponse with no underlying HTTP response (e.g. a network-level failure
	// that still produces an ErrorResponse). Must not be treated as a 401.
	errResp := &buildkite.ErrorResponse{
		Response: nil,
		Message:  "connection reset",
	}

	result, data, err := handleBuildkiteError(errResp)

	require.NoError(t, err)
	require.Nil(t, data)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	textContent := getTextResult(t, result)
	require.Equal(t, "connection reset", textContent.Text)
}

func TestErrUnauthorized_IsWrappable(t *testing.T) {
	wrapped := fmt.Errorf("wrapped: %w", ErrUnauthorized)
	require.ErrorIs(t, wrapped, ErrUnauthorized)
}
