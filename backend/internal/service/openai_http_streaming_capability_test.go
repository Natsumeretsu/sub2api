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

type openAIHTTPStreamingProbeUpstreamStub struct {
	resp      *http.Response
	err       error
	callCount int
	lastReq   *http.Request
	lastBody  []byte
}

func (s *openAIHTTPStreamingProbeUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.callCount++
	s.lastReq = req
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		s.lastBody = body
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	return s.resp, s.err
}

func (s *openAIHTTPStreamingProbeUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ bool) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestResolveOpenAIHTTPStreamingSupportForRequest_ProbeConfirmsStreamingCapability(t *testing.T) {
	upstream := &openAIHTTPStreamingProbeUpstreamStub{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_probe_1"}}`,
				`data: {"type":"response.output_text.delta","delta":"ok"}`,
				`data: {"type":"response.completed","response":{"id":"resp_probe_1"}}`,
				`data: [DONE]`,
			}, "\n"))),
		},
	}
	account := &Account{
		ID:          16,
		Name:        "RightCode",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://right.codes/codex/v1",
		},
	}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	supported, known, source, err := svc.ResolveOpenAIHTTPStreamingSupportForRequest(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_stream_done_supported", source)
	require.Equal(t, 1, upstream.callCount)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://right.codes/codex/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("accept"))
	require.Contains(t, string(upstream.lastBody), `"stream":true`)

	supported, known, source, err = svc.ResolveOpenAIHTTPStreamingSupportForRequest(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_stream_done_supported", source)
	require.Equal(t, 1, upstream.callCount, "second lookup should hit observed cache")
}

func TestResolveOpenAIHTTPStreamingSupportForRequest_ProbeRejectsMissingDone(t *testing.T) {
	upstream := &openAIHTTPStreamingProbeUpstreamStub{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_probe_1"}}`,
				`data: {"type":"response.output_text.delta","delta":"partial"}`,
				`data: {"type":"response.completed","response":{"id":"resp_probe_1"}}`,
			}, "\n"))),
		},
	}
	account := &Account{
		ID:          17,
		Name:        "PackyCode",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://packy.codes/v1",
		},
	}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	supported, known, source, err := svc.ResolveOpenAIHTTPStreamingSupportForRequest(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.False(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_stream_missing_done", source)
}
