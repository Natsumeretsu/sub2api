package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
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

type OpenAICompactWindowAttribution struct {
	PreviousCompactRequestID           string `json:"previous_compact_request_id,omitempty"`
	PreviousCompactOutcome             string `json:"previous_compact_outcome,omitempty"`
	PreviousCompactAgeMs               int64  `json:"previous_compact_age_ms"`
	PreviousCompactInputTokens         int    `json:"previous_compact_input_tokens"`
	PreviousCompactCacheReadTokens     int    `json:"previous_compact_cache_read_tokens"`
	PreviousCompactUpstreamInputTokens int    `json:"previous_compact_upstream_input_tokens"`
	DeltaAvailable                     bool   `json:"delta_available"`
	BillableInputDelta                 int    `json:"billable_input_delta"`
	CacheReadDelta                     int    `json:"cache_read_delta"`
	UpstreamInputDelta                 int    `json:"upstream_input_delta"`
	WindowTotalsAvailable              bool   `json:"window_totals_available"`
	WindowTurnCount                    int    `json:"window_turn_count"`
	WindowBridgeTurnCount              int    `json:"window_bridge_turn_count"`
	WindowReplayInputItems             int    `json:"window_replay_input_items"`
	WindowReplayInputBytes             int    `json:"window_replay_input_bytes"`
	WindowBillableInputTokens          int    `json:"window_billable_input_tokens"`
	WindowCacheReadTokens              int    `json:"window_cache_read_tokens"`
	WindowUpstreamInputTokens          int    `json:"window_upstream_input_tokens"`
}

type OpenAICompactWindowRollup struct {
	TurnCount           int
	BridgeTurnCount     int
	ReplayInputItems    int
	ReplayInputBytes    int
	BillableInputTokens int
	CacheReadTokens     int
	UpstreamInputTokens int
}

type OpenAICompactChainSegment struct {
	CompactRequestID       string `json:"compact_request_id,omitempty"`
	CompactOutcome         string `json:"compact_outcome,omitempty"`
	CompactAgeMs           int64  `json:"compact_age_ms"`
	WindowTurnCount        int    `json:"window_turn_count"`
	WindowBridgeTurnCount  int    `json:"window_bridge_turn_count"`
	WindowReplayInputItems int    `json:"window_replay_input_items"`
	WindowReplayInputBytes int    `json:"window_replay_input_bytes"`
	WindowBillableInput    int    `json:"window_billable_input_tokens"`
	WindowCacheReadTokens  int    `json:"window_cache_read_tokens"`
	WindowUpstreamInput    int    `json:"window_upstream_input_tokens"`
}

type OpenAICompactChainAttribution struct {
	TotalsAvailable          bool                        `json:"totals_available"`
	SegmentCount             int                         `json:"segment_count"`
	SuccessfulCompactCount   int                         `json:"successful_compact_count"`
	TotalTurnCount           int                         `json:"total_turn_count"`
	TotalBridgeTurnCount     int                         `json:"total_bridge_turn_count"`
	TotalReplayInputItems    int                         `json:"total_replay_input_items"`
	TotalReplayInputBytes    int                         `json:"total_replay_input_bytes"`
	TotalBillableInputTokens int                         `json:"total_billable_input_tokens"`
	TotalCacheReadTokens     int                         `json:"total_cache_read_tokens"`
	TotalUpstreamInputTokens int                         `json:"total_upstream_input_tokens"`
	Segments                 []OpenAICompactChainSegment `json:"segments,omitempty"`
}

type OpenAICompactChainEvent struct {
	RequestID   string
	CreatedAtMs int64
	Attribution *OpenAITurnTokenAttribution
}

func hasOpenAICompactWindowRollupData(input *OpenAICompactWindowRollup) bool {
	if input == nil {
		return false
	}
	return input.TurnCount > 0 ||
		input.BridgeTurnCount > 0 ||
		input.ReplayInputItems > 0 ||
		input.ReplayInputBytes > 0 ||
		input.BillableInputTokens > 0 ||
		input.CacheReadTokens > 0 ||
		input.UpstreamInputTokens > 0
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

func DecodeOpenAICompactChainEventsJSON(raw string) []OpenAICompactChainEvent {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	type wireEvent struct {
		RequestID   string          `json:"request_id"`
		CreatedAtMs int64           `json:"created_at_ms"`
		Extra       json.RawMessage `json:"extra"`
	}
	var payload []wireEvent
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	out := make([]OpenAICompactChainEvent, 0, len(payload))
	for _, item := range payload {
		attr := DecodeOpenAITurnTokenAttributionJSON(strings.TrimSpace(string(item.Extra)))
		if attr == nil {
			continue
		}
		out = append(out, OpenAICompactChainEvent{
			RequestID:   strings.TrimSpace(item.RequestID),
			CreatedAtMs: item.CreatedAtMs,
			Attribution: attr,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func BuildOpenAICompactWindowAttribution(
	currentInputTokens int,
	currentCacheReadTokens int,
	currentAttr *OpenAITurnTokenAttribution,
	previousCompactRequestID string,
	previousCompactAgeMs int64,
	previousCompactAttr *OpenAITurnTokenAttribution,
	windowRollup *OpenAICompactWindowRollup,
) *OpenAICompactWindowAttribution {
	if previousCompactAttr == nil || !previousCompactAttr.CompactRequest {
		return nil
	}
	if currentAttr != nil && currentAttr.CompactRequest {
		return nil
	}

	outcome := strings.TrimSpace(previousCompactAttr.CompactOutcome)
	window := &OpenAICompactWindowAttribution{
		PreviousCompactRequestID:           strings.TrimSpace(previousCompactRequestID),
		PreviousCompactOutcome:             outcome,
		PreviousCompactAgeMs:               previousCompactAgeMs,
		PreviousCompactInputTokens:         previousCompactAttr.BillableInputTokens,
		PreviousCompactCacheReadTokens:     previousCompactAttr.CacheReadTokens,
		PreviousCompactUpstreamInputTokens: previousCompactAttr.UpstreamInputTokens,
	}

	if currentAttr != nil && strings.EqualFold(outcome, "succeeded") {
		window.DeltaAvailable = true
		window.BillableInputDelta = currentInputTokens - previousCompactAttr.BillableInputTokens
		window.CacheReadDelta = currentCacheReadTokens - previousCompactAttr.CacheReadTokens
		window.UpstreamInputDelta = currentAttr.UpstreamInputTokens - previousCompactAttr.UpstreamInputTokens
	}
	if hasOpenAICompactWindowRollupData(windowRollup) && strings.EqualFold(outcome, "succeeded") {
		window.WindowTotalsAvailable = true
		window.WindowTurnCount = windowRollup.TurnCount
		window.WindowBridgeTurnCount = windowRollup.BridgeTurnCount
		window.WindowReplayInputItems = windowRollup.ReplayInputItems
		window.WindowReplayInputBytes = windowRollup.ReplayInputBytes
		window.WindowBillableInputTokens = windowRollup.BillableInputTokens
		window.WindowCacheReadTokens = windowRollup.CacheReadTokens
		window.WindowUpstreamInputTokens = windowRollup.UpstreamInputTokens
	}

	if window.PreviousCompactRequestID == "" &&
		window.PreviousCompactOutcome == "" &&
		window.PreviousCompactAgeMs == 0 &&
		window.PreviousCompactInputTokens == 0 &&
		window.PreviousCompactCacheReadTokens == 0 &&
		window.PreviousCompactUpstreamInputTokens == 0 &&
		!window.WindowTotalsAvailable &&
		!window.DeltaAvailable {
		return nil
	}

	return window
}

func BuildOpenAICompactChainAttribution(
	currentCreatedAt time.Time,
	events []OpenAICompactChainEvent,
) *OpenAICompactChainAttribution {
	if len(events) == 0 {
		return nil
	}
	currentMs := currentCreatedAt.UnixMilli()
	chain := &OpenAICompactChainAttribution{}
	var currentSegment *OpenAICompactChainSegment
	for _, event := range events {
		attr := event.Attribution
		if attr == nil {
			continue
		}
		if attr.CompactRequest && strings.EqualFold(strings.TrimSpace(attr.CompactOutcome), "succeeded") {
			segment := OpenAICompactChainSegment{
				CompactRequestID: strings.TrimSpace(event.RequestID),
				CompactOutcome:   "succeeded",
			}
			if currentMs > 0 && event.CreatedAtMs > 0 && currentMs >= event.CreatedAtMs {
				segment.CompactAgeMs = currentMs - event.CreatedAtMs
			}
			chain.Segments = append(chain.Segments, segment)
			currentSegment = &chain.Segments[len(chain.Segments)-1]
			chain.SegmentCount++
			chain.SuccessfulCompactCount++
			continue
		}
		if currentSegment == nil {
			continue
		}
		currentSegment.WindowTurnCount++
		if attr.BridgeUsed {
			currentSegment.WindowBridgeTurnCount++
			chain.TotalBridgeTurnCount++
		}
		currentSegment.WindowReplayInputItems += attr.ReplayInputItems
		currentSegment.WindowReplayInputBytes += attr.ReplayInputBytes
		currentSegment.WindowBillableInput += attr.BillableInputTokens
		currentSegment.WindowCacheReadTokens += attr.CacheReadTokens
		currentSegment.WindowUpstreamInput += attr.UpstreamInputTokens
		chain.TotalTurnCount++
		chain.TotalReplayInputItems += attr.ReplayInputItems
		chain.TotalReplayInputBytes += attr.ReplayInputBytes
		chain.TotalBillableInputTokens += attr.BillableInputTokens
		chain.TotalCacheReadTokens += attr.CacheReadTokens
		chain.TotalUpstreamInputTokens += attr.UpstreamInputTokens
	}
	if chain.SegmentCount == 0 {
		return nil
	}
	chain.TotalsAvailable = true
	return chain
}

func resolveOpenAITurnRequestID(ctx context.Context, result *OpenAIForwardResult) string {
	if result != nil {
		if requestID := strings.TrimSpace(result.RequestID); requestID != "" {
			return requestID
		}
	}
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(ctxkey.RequestID).(string)
	return strings.TrimSpace(requestID)
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
	if attribution != nil && attribution.CompactRequest && strings.TrimSpace(attribution.CompactOutcome) == "" {
		attribution.CompactOutcome = "succeeded"
	}
	requestID := resolveOpenAITurnRequestID(ctx, input.Result)

	fields := []zap.Field{
		zap.String("component", "audit.openai_turn_token_attribution"),
		zap.String("source_component", "service.openai_gateway"),
		zap.String("request_id", requestID),
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
	if sessionHash := strings.TrimSpace(input.SessionHash); sessionHash != "" {
		fields = append(fields, zap.String("session_hash", sessionHash))
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
