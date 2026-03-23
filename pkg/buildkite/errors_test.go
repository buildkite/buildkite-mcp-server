package buildkite

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
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
