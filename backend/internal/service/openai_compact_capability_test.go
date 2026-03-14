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

type openAICompactProbeUpstreamStub struct {
	resp      *http.Response
	err       error
	callCount int
	lastReq   *http.Request
	lastBody  []byte
}

func (s *openAICompactProbeUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.callCount++
	s.lastReq = req
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		s.lastBody = body
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	return s.resp, s.err
}

func (s *openAICompactProbeUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ bool) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestResolveOpenAIResponsesCompactSupport_ProbeConfirmsPreviousResponseCapability(t *testing.T) {
	upstream := &openAICompactProbeUpstreamStub{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{},
			Body: io.NopCloser(strings.NewReader(`{
				"error": {
					"message": "Previous response with id 'resp_compact_capability_probe' not found.",
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

	supported, known, source, err := svc.ResolveOpenAIResponsesCompactSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_previous_response_supported", source)
	require.Equal(t, 1, upstream.callCount)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://right.codes/codex/v1/responses/compact", upstream.lastReq.URL.String())
	require.Contains(t, string(upstream.lastBody), `"instructions":"compact capability probe"`)
	require.Contains(t, string(upstream.lastBody), `"previous_response_id":"resp_compact_capability_probe"`)

	supported, known, source, err = svc.ResolveOpenAIResponsesCompactSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_previous_response_supported", source)
	require.Equal(t, 1, upstream.callCount, "second lookup should hit observed cache")
}

func TestResolveOpenAIResponsesCompactSupport_ProbeRejectsUnsupportedPreviousResponseID(t *testing.T) {
	upstream := &openAICompactProbeUpstreamStub{
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

	supported, known, source, err := svc.ResolveOpenAIResponsesCompactSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.False(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_unsupported_previous_response_id", source)
}

func TestResolveOpenAIResponsesCompactSupport_ProbeRejectsGenericHTTP4xx(t *testing.T) {
	upstream := &openAICompactProbeUpstreamStub{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"detail":"The 'gpt-5.4' model is not supported when using Codex with a ChatGPT account."}`)),
		},
	}
	account := &Account{
		ID:          18,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.invalid/v1",
		},
	}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	supported, known, source, err := svc.ResolveOpenAIResponsesCompactSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.False(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_http_rejected", source)
}

func TestOpenAIWSContinuationRuntimeSnapshot_UsesObservedCompactCapability(t *testing.T) {
	account := Account{
		ID:          18,
		Name:        "RightCode",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Schedulable: true,
		Status:      StatusActive,
		GroupIDs:    []int64{2},
	}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
	}
	svc.setObservedOpenAIResponsesCompactCapability(&account, true, "probe_previous_response_supported")

	snapshot := svc.buildOpenAIWSContinuationCapabilitySnapshot(context.Background())
	require.Equal(t, int64(1), snapshot.GlobalCompactCapableAccounts)
	require.Equal(t, int64(0), snapshot.GlobalCompactIncapableAccounts)
	require.Len(t, snapshot.Groups, 1)
	require.Equal(t, int64(1), snapshot.Groups[0].CompactCapableAccounts)
	require.Equal(t, []string{"RightCode"}, snapshot.Groups[0].CompactCapableAccountNames)
}
