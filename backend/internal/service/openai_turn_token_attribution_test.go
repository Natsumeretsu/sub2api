package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
}
