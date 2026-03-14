package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIStructuredUpstreamError_PreviousResponseNotFound(t *testing.T) {
	status, errType, message, ok := ResolveOpenAIStructuredUpstreamError(
		http.StatusBadRequest,
		"Previous response with id 'resp_probe_dummy' not found.",
		`{"error":{"type":"invalid_request_error","param":"previous_response_id","code":"previous_response_not_found"}}`,
	)

	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", errType)
	require.Equal(t, "Previous response with id 'resp_probe_dummy' not found.", message)
}

func TestResolveOpenAIStructuredUpstreamError_UnsupportedPreviousResponseIDSoftInterrupt(t *testing.T) {
	status, errType, message, ok := ResolveOpenAIStructuredUpstreamError(
		http.StatusBadRequest,
		"Unsupported parameter: previous_response_id",
		`{"error":{"type":"invalid_request_error","param":"previous_response_id"}}`,
	)

	require.True(t, ok)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "api_error", errType)
	require.Equal(t, OpenAIHTTPPreviousResponseUnsupportedMessage(), message)
}

func TestResolveOpenAIStructuredUpstreamError_UpstreamAuthFailureKeepsLegacyPath(t *testing.T) {
	status, errType, message, ok := ResolveOpenAIStructuredUpstreamError(
		http.StatusUnauthorized,
		"Unauthorized",
		"",
	)

	require.False(t, ok)
	require.Zero(t, status)
	require.Empty(t, errType)
	require.Empty(t, message)
}
