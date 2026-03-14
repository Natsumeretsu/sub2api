package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestMeasureOpenAIReplayInput(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"input_text","text":"hello"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"ok"}`),
	}

	count, bytes := measureOpenAIReplayInput(items, true)

	require.Equal(t, 2, count)
	require.Equal(t, len(items[0])+len(items[1])+3, bytes)
}

func TestDecodeOpenAITurnTokenAttributionJSON(t *testing.T) {
	raw := `{"bridge_used":true,"bridge_mode":"ws_http_bridge","replay_input_items":2,"replay_input_bytes":123,"cache_read_tokens":456}`

	decoded := DecodeOpenAITurnTokenAttributionJSON(raw)

	require.NotNil(t, decoded)
	require.True(t, decoded.BridgeUsed)
	require.Equal(t, "ws_http_bridge", decoded.BridgeMode)
	require.Equal(t, 2, decoded.ReplayInputItems)
	require.Equal(t, 123, decoded.ReplayInputBytes)
	require.Equal(t, 456, decoded.CacheReadTokens)
}

func TestBuildOpenAICompactWindowAttribution(t *testing.T) {
	currentAttr := &OpenAITurnTokenAttribution{
		UpstreamInputTokens:  8321,
		BillableInputTokens:  2817,
		CacheReadTokens:      5504,
		PromptCacheKeyUsed:   true,
		PromptCacheKeySource: "payload",
	}
	previousCompactAttr := &OpenAITurnTokenAttribution{
		CompactRequest:      true,
		CompactOutcome:      "succeeded",
		UpstreamInputTokens: 9100,
		BillableInputTokens: 3200,
		CacheReadTokens:     5900,
	}

	window := BuildOpenAICompactWindowAttribution(
		2817,
		5504,
		currentAttr,
		"req-compact-1",
		4200,
		previousCompactAttr,
	)

	require.NotNil(t, window)
	require.Equal(t, "req-compact-1", window.PreviousCompactRequestID)
	require.Equal(t, "succeeded", window.PreviousCompactOutcome)
	require.EqualValues(t, 4200, window.PreviousCompactAgeMs)
	require.True(t, window.DeltaAvailable)
	require.Equal(t, -383, window.BillableInputDelta)
	require.Equal(t, -396, window.CacheReadDelta)
	require.Equal(t, -779, window.UpstreamInputDelta)
}

func TestBuildOpenAICompactWindowAttribution_IgnoresCurrentCompactRequest(t *testing.T) {
	currentAttr := &OpenAITurnTokenAttribution{CompactRequest: true}
	previousCompactAttr := &OpenAITurnTokenAttribution{CompactRequest: true, CompactOutcome: "succeeded"}

	window := BuildOpenAICompactWindowAttribution(0, 0, currentAttr, "req-compact-2", 1000, previousCompactAttr)

	require.Nil(t, window)
}

func TestOpenAIGatewayServiceRecordUsage_EmitsTurnTokenAttributionLog(t *testing.T) {
	sink, restore := captureStructuredLog(t)
	defer restore()

	groupID := int64(21)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_turn_attr_1",
			Usage: OpenAIUsage{
				InputTokens:          300,
				OutputTokens:         40,
				CacheReadInputTokens: 120,
			},
			Model: "gpt-5.4",
			TokenAttribution: &OpenAITurnTokenAttribution{
				BridgeUsed:         true,
				BridgeMode:         "ws_http_bridge",
				ReplayInputItems:   3,
				ReplayInputBytes:   512,
				ReplayInputApplied: true,
				CompactRequest:     true,
			},
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:      10021,
			GroupID: &groupID,
			Group: &Group{
				ID:             groupID,
				RateMultiplier: 1.0,
			},
		},
		User:            &User{ID: 20021},
		Account:         &Account{ID: 30021},
		SessionHash:     "sess-hash-21",
		ClientRequestID: "creq-turn-attr-1",
	})

	require.NoError(t, err)
	require.True(t, sink.ContainsMessageAtLevel("openai.turn_token_attribution", "info"))
	require.True(t, sink.ContainsFieldValue("request_id", "resp_turn_attr_1"))
	require.True(t, sink.ContainsFieldValue("client_request_id", "creq-turn-attr-1"))
	require.True(t, sink.ContainsFieldValue("bridge_mode", "ws_http_bridge"))
	require.True(t, sink.ContainsFieldValue("replay_input_bytes", "512"))
	require.True(t, sink.ContainsFieldValue("cache_read_tokens", "120"))
	require.True(t, sink.ContainsFieldValue("billable_input_tokens", "180"))
	require.True(t, sink.ContainsFieldValue("session_hash", "sess-hash-21"))
	require.True(t, sink.ContainsFieldValue("compact_outcome", "succeeded"))
}

func TestOpenAIGatewayServiceRecordUsage_FallsBackToContextRequestID(t *testing.T) {
	sink, restore := captureStructuredLog(t)
	defer restore()

	groupID := int64(31)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-turn-attr-fallback")
	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			Usage: OpenAIUsage{
				InputTokens:  12,
				OutputTokens: 3,
			},
			Model: "gpt-5.4",
		},
		APIKey: &APIKey{
			ID:      10031,
			GroupID: &groupID,
			Group: &Group{
				ID:             groupID,
				RateMultiplier: 1.0,
			},
		},
		User:            &User{ID: 20031},
		Account:         &Account{ID: 30031},
		ClientRequestID: "creq-turn-attr-fallback",
	})

	require.NoError(t, err)
	require.True(t, sink.ContainsFieldValue("request_id", "req-turn-attr-fallback"))
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "req-turn-attr-fallback", usageRepo.lastLog.RequestID)
}
