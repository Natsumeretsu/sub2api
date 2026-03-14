//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type openAIHTTPPreviousResponseProbeUpstreamStub struct {
	resp      *http.Response
	err       error
	callCount int
	lastReq   *http.Request
	lastBody  []byte
}

func (s *openAIHTTPPreviousResponseProbeUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.callCount++
	s.lastReq = req
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		s.lastBody = body
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	return s.resp, s.err
}

func (s *openAIHTTPPreviousResponseProbeUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ bool) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestResolveOpenAIHTTPPreviousResponseSupport_ProbeConfirmsPreviousResponseCapability(t *testing.T) {
	upstream := &openAIHTTPPreviousResponseProbeUpstreamStub{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{},
			Body: io.NopCloser(strings.NewReader(`{
				"error": {
					"message": "Previous response with id 'resp_http_previous_response_probe' not found.",
					"type": "invalid_request_error",
					"param": "previous_response_id",
					"code": "previous_response_not_found"
				}
			}`)),
		},
	}
	account := &Account{
		ID:          16,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://right.codes/codex/v1",
		},
	}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	supported, known, source, err := svc.ResolveOpenAIHTTPPreviousResponseSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_previous_response_supported", source)
	require.Equal(t, 1, upstream.callCount)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://right.codes/codex/v1/responses", upstream.lastReq.URL.String())
	require.Contains(t, string(upstream.lastBody), `"previous_response_id":"resp_http_previous_response_probe"`)

	supported, known, source, err = svc.ResolveOpenAIHTTPPreviousResponseSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_previous_response_supported", source)
	require.Equal(t, 1, upstream.callCount, "second lookup should hit observed cache")
}

func TestResolveOpenAIHTTPPreviousResponseSupport_ProbeRejectsUnsupportedPreviousResponseID(t *testing.T) {
	upstream := &openAIHTTPPreviousResponseProbeUpstreamStub{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"detail":"Unsupported parameter: previous_response_id"}`)),
		},
	}
	account := &Account{
		ID:          17,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.invalid/v1",
		},
	}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	supported, known, source, err := svc.ResolveOpenAIHTTPPreviousResponseSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.False(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_unsupported_previous_response_id", source)
}
