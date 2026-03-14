package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

type OpenAITurnTokenAttribution struct {
	BridgeUsed           bool   `json:"bridge_used,omitempty"`
	BridgeMode           string `json:"bridge_mode,omitempty"`
	BridgeSource         string `json:"bridge_source,omitempty"`
	ReplayInputItems     int    `json:"replay_input_items,omitempty"`
	ReplayInputBytes     int    `json:"replay_input_bytes,omitempty"`
	ReplayInputApplied   bool   `json:"replay_input_applied,omitempty"`
	PromptCacheKeySource string `json:"prompt_cache_key_source,omitempty"`
	PromptCacheKeyUsed   bool   `json:"prompt_cache_key_used,omitempty"`
	CompactRequest       bool   `json:"compact_request,omitempty"`
	CompactOutcome       string `json:"compact_outcome,omitempty"`
	UpstreamInputTokens  int    `json:"upstream_input_tokens,omitempty"`
	BillableInputTokens  int    `json:"billable_input_tokens,omitempty"`
	CacheReadTokens      int    `json:"cache_read_tokens,omitempty"`
}

func cloneOpenAITurnTokenAttribution(input *OpenAITurnTokenAttribution) *OpenAITurnTokenAttribution {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func mergeOpenAITurnTokenAttribution(base *OpenAITurnTokenAttribution, overlay *OpenAITurnTokenAttribution) *OpenAITurnTokenAttribution {
	if base == nil {
		base = &OpenAITurnTokenAttribution{}
	}
	if overlay == nil {
		return base
	}
	if overlay.BridgeUsed {
		base.BridgeUsed = true
	}
	if strings.TrimSpace(overlay.BridgeMode) != "" {
		base.BridgeMode = strings.TrimSpace(overlay.BridgeMode)
	}
	if strings.TrimSpace(overlay.BridgeSource) != "" {
		base.BridgeSource = strings.TrimSpace(overlay.BridgeSource)
	}
	if overlay.ReplayInputItems > 0 {
		base.ReplayInputItems = overlay.ReplayInputItems
	}
	if overlay.ReplayInputBytes > 0 {
		base.ReplayInputBytes = overlay.ReplayInputBytes
	}
	if overlay.ReplayInputApplied {
		base.ReplayInputApplied = true
	}
	if strings.TrimSpace(overlay.PromptCacheKeySource) != "" {
		base.PromptCacheKeySource = strings.TrimSpace(overlay.PromptCacheKeySource)
	}
	if overlay.PromptCacheKeyUsed {
		base.PromptCacheKeyUsed = true
	}
	if overlay.CompactRequest {
		base.CompactRequest = true
	}
	if strings.TrimSpace(overlay.CompactOutcome) != "" {
		base.CompactOutcome = strings.TrimSpace(overlay.CompactOutcome)
	}
	if overlay.UpstreamInputTokens > 0 {
		base.UpstreamInputTokens = overlay.UpstreamInputTokens
	}
	if overlay.BillableInputTokens > 0 {
		base.BillableInputTokens = overlay.BillableInputTokens
	}
	if overlay.CacheReadTokens > 0 {
		base.CacheReadTokens = overlay.CacheReadTokens
	}
	return base
}

func measureOpenAIReplayInput(replayInput []json.RawMessage, replayInputExists bool) (items int, bytes int) {
	if !replayInputExists || len(replayInput) == 0 {
		return 0, 0
	}
	items = len(replayInput)
	bytes = 2
	for idx, item := range replayInput {
		bytes += len(item)
		if idx > 0 {
			bytes++
		}
	}
	return items, bytes
}

func hasOpenAITurnTokenAttributionData(input *OpenAITurnTokenAttribution) bool {
	if input == nil {
		return false
	}
	return input.BridgeUsed ||
		strings.TrimSpace(input.BridgeMode) != "" ||
		strings.TrimSpace(input.BridgeSource) != "" ||
		input.ReplayInputItems > 0 ||
		input.ReplayInputBytes > 0 ||
		input.ReplayInputApplied ||
		strings.TrimSpace(input.PromptCacheKeySource) != "" ||
		input.PromptCacheKeyUsed ||
		input.CompactRequest ||
		strings.TrimSpace(input.CompactOutcome) != "" ||
		input.UpstreamInputTokens > 0 ||
		input.BillableInputTokens > 0 ||
		input.CacheReadTokens > 0
}

func DecodeOpenAITurnTokenAttributionJSON(raw string) *OpenAITurnTokenAttribution {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var decoded OpenAITurnTokenAttribution
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	if !hasOpenAITurnTokenAttributionData(&decoded) {
		return nil
	}
	return &decoded
}

func emitOpenAITurnTokenAttributionLog(
	ctx context.Context,
	input *OpenAIRecordUsageInput,
	actualInputTokens int,
) {
	if input == nil || input.Result == nil || input.APIKey == nil || input.User == nil || input.Account == nil {
		return
	}

	attribution := mergeOpenAITurnTokenAttribution(
		cloneOpenAITurnTokenAttribution(input.Result.TokenAttribution),
		&OpenAITurnTokenAttribution{
			UpstreamInputTokens: input.Result.Usage.InputTokens,
			BillableInputTokens: actualInputTokens,
			CacheReadTokens:     input.Result.Usage.CacheReadInputTokens,
		},
	)

	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.String("request_id", strings.TrimSpace(input.Result.RequestID)),
		zap.String("client_request_id", strings.TrimSpace(input.ClientRequestID)),
		zap.String("platform", strings.TrimSpace(input.Account.Platform)),
		zap.String("model", strings.TrimSpace(input.Result.Model)),
		zap.Int64("user_id", input.User.ID),
		zap.Int64("api_key_id", input.APIKey.ID),
		zap.Int64("account_id", input.Account.ID),
		zap.Bool("stream", input.Result.Stream),
		zap.Bool("openai_ws_mode", input.Result.OpenAIWSMode),
		zap.Int("upstream_input_tokens", attribution.UpstreamInputTokens),
		zap.Int("billable_input_tokens", attribution.BillableInputTokens),
		zap.Int("cache_read_tokens", attribution.CacheReadTokens),
		zap.Bool("bridge_used", attribution.BridgeUsed),
	}
	if input.APIKey.GroupID != nil && *input.APIKey.GroupID > 0 {
		fields = append(fields, zap.Int64("group_id", *input.APIKey.GroupID))
	}
	if strings.TrimSpace(attribution.BridgeMode) != "" {
		fields = append(fields, zap.String("bridge_mode", attribution.BridgeMode))
	}
	if strings.TrimSpace(attribution.BridgeSource) != "" {
		fields = append(fields, zap.String("bridge_source", attribution.BridgeSource))
	}
	if attribution.ReplayInputItems > 0 {
		fields = append(fields, zap.Int("replay_input_items", attribution.ReplayInputItems))
	}
	if attribution.ReplayInputBytes > 0 {
		fields = append(fields, zap.Int("replay_input_bytes", attribution.ReplayInputBytes))
	}
	if attribution.ReplayInputApplied {
		fields = append(fields, zap.Bool("replay_input_applied", true))
	}
	if attribution.PromptCacheKeyUsed {
		fields = append(fields, zap.Bool("prompt_cache_key_used", true))
	}
	if strings.TrimSpace(attribution.PromptCacheKeySource) != "" {
		fields = append(fields, zap.String("prompt_cache_key_source", attribution.PromptCacheKeySource))
	}
	if attribution.CompactRequest {
		fields = append(fields, zap.Bool("compact_request", true))
	}
	if strings.TrimSpace(attribution.CompactOutcome) != "" {
		fields = append(fields, zap.String("compact_outcome", attribution.CompactOutcome))
	}

	logger.FromContext(ctx).With(fields...).Info("openai.turn_token_attribution")
}
