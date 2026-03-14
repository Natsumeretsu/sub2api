//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildOpenAIWSHTTPBridgeRequestBody_RemovesTypeAndForcesStream(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"gpt-5.4","stream":false,"input":[]}`)

	body, err := buildOpenAIWSHTTPBridgeRequestBody(payload)
	require.NoError(t, err)
	require.True(t, gjson.ValidBytes(body))
	require.False(t, gjson.GetBytes(body, "type").Exists())
	require.True(t, gjson.GetBytes(body, "stream").Bool())
	require.Equal(t, "gpt-5.4", gjson.GetBytes(body, "model").String())
}

func TestRelayOpenAIWSHTTPBridgeStream_SynthesizesFailedTerminalWhenStreamEndsEarly(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newOpenAIWSV2TestConfig()}
	account := &Account{
		ID:       16,
		Name:     "RightCode",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_bridge_1"}}`,
			`data: {"type":"response.output_text.delta","response_id":"resp_bridge_1","delta":"hi"}`,
		}, "\n"))),
	}
	writes := make([][]byte, 0, 4)

	result, err := svc.relayOpenAIWSHTTPBridgeStream(
		context.Background(),
		resp,
		account,
		[]byte(`{"model":"gpt-5.4","stream":true}`),
		"gpt-5.4",
		time.Now(),
		func(message []byte) error {
			writes = append(writes, append([]byte(nil), message...))
			return nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_bridge_1", result.RequestID)
	require.GreaterOrEqual(t, len(writes), 2)
	require.Equal(t, "response.failed", gjson.GetBytes(writes[len(writes)-1], "type").String())
	require.Contains(t, gjson.GetBytes(writes[len(writes)-1], "error.message").String(), "terminal")
}

func TestRelayOpenAIWSHTTPBridgeStream_HTTPErrorBecomesResponseFailedEvent(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newOpenAIWSV2TestConfig()}
	account := &Account{
		ID:       16,
		Name:     "RightCode",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Unsupported parameter: previous_response_id"}}`)),
	}
	writes := make([][]byte, 0, 2)

	result, err := svc.relayOpenAIWSHTTPBridgeStream(
		context.Background(),
		resp,
		account,
		[]byte(`{"model":"gpt-5.4","stream":true}`),
		"gpt-5.4",
		time.Now(),
		func(message []byte) error {
			writes = append(writes, append([]byte(nil), message...))
			return nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, writes, 1)
	require.Equal(t, "response.failed", gjson.GetBytes(writes[0], "type").String())
	require.Equal(t, OpenAIHTTPPreviousResponseUnsupportedMessage(), gjson.GetBytes(writes[0], "error.message").String())
}

func TestRelayOpenAIWSHTTPBridgeStream_HTMLChallengeErrorBecomesStructuredFailedEvent(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newOpenAIWSV2TestConfig()}
	account := &Account{
		ID:       16,
		Name:     "RightCode",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header: http.Header{
			"Content-Type": []string{"text/html; charset=utf-8"},
			"cf-ray":       []string{"9dc2bc984a926ad4"},
		},
		Body: io.NopCloser(strings.NewReader(`<!doctype html><html><head><title>HTTP Status 400 – Bad Request</title></head><body><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script></body></html>`)),
	}
	writes := make([][]byte, 0, 2)

	result, err := svc.relayOpenAIWSHTTPBridgeStream(
		context.Background(),
		resp,
		account,
		[]byte(`{"model":"gpt-5.4","stream":true}`),
		"gpt-5.4",
		time.Now(),
		func(message []byte) error {
			writes = append(writes, append([]byte(nil), message...))
			return nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, writes, 1)
	require.Equal(t, "response.failed", gjson.GetBytes(writes[0], "type").String())
	require.NotContains(t, string(writes[0]), "<!doctype html>")
	require.Contains(t, gjson.GetBytes(writes[0], "error.message").String(), "Cloudflare challenge page")
}

func TestExtractOpenAIWSReplayInputFromCompletedEvent_MessageAndPromptCacheKey(t *testing.T) {
	payload := []byte(`{"type":"response.completed","response":{"prompt_cache_key":"pcache-123","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK","annotations":[],"logprobs":[]}]},{"type":"reasoning","summary":[]} ]}}`)

	replayInput, ok, promptCacheKey, err := extractOpenAIWSReplayInputFromCompletedEvent(payload)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "pcache-123", promptCacheKey)
	require.Len(t, replayInput, 1)
	require.Equal(t, "message", gjson.GetBytes(replayInput[0], "type").String())
	require.Equal(t, "assistant", gjson.GetBytes(replayInput[0], "role").String())
	require.Equal(t, "OK", gjson.GetBytes(replayInput[0], "content.0.text").String())
}

func TestPrepareOpenAIWSHTTPBridgePayload_ReplaysWhenPreviousResponseUnsupported(t *testing.T) {
	payload := []byte(`{"model":"gpt-5.4","stream":true,"previous_response_id":"resp_123","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"What is the code word?"}]}]}`)
	replayInput := []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"Remember the code word BANANA."}]}`),
		json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"remembered."}]}`),
		json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"What is the code word?"}]}`),
	}

	updated, promptInjected, replayApplied, err := prepareOpenAIWSHTTPBridgePayload(payload, replayInput, true, "pcache-xyz", false)
	require.NoError(t, err)
	require.True(t, promptInjected)
	require.True(t, replayApplied)
	require.False(t, gjson.GetBytes(updated, "previous_response_id").Exists())
	require.Equal(t, "pcache-xyz", gjson.GetBytes(updated, "prompt_cache_key").String())
	require.Equal(t, "Remember the code word BANANA.", gjson.GetBytes(updated, "input.0.content.0.text").String())
	require.Equal(t, "remembered.", gjson.GetBytes(updated, "input.1.content.0.text").String())
	require.Equal(t, "What is the code word?", gjson.GetBytes(updated, "input.2.content.0.text").String())
}

func TestBuildUpstreamRequestOpenAIPassthrough_APIKeyPromptCacheKeySetsSessionHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","prompt_cache_key":"pcache-bridge","input":[]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	svc := &OpenAIGatewayService{}
	account := &Account{
		ID:       16,
		Name:     "RightCode",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://right.codes/codex/v1",
		},
	}

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(
		context.Background(),
		c,
		account,
		[]byte(`{"model":"gpt-5.4","prompt_cache_key":"pcache-bridge","input":[]}`),
		"sk-upstream",
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "pcache-bridge", req.Header.Get("session_id"))
	require.Equal(t, "pcache-bridge", req.Header.Get("conversation_id"))
}
