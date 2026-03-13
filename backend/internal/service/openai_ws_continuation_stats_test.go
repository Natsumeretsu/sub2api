package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSContinuationStatsSnapshot(t *testing.T) {
	resetOpenAIWSContinuationStatsForTest()
	RecordOpenAIWSContinuationValidationReject("function_call_output_missing_call_id")
	RecordOpenAIWSContinuationValidationReject("function_call_output_missing_item_reference")
	recordOpenAIWSContinuationPrevNotFoundAlignRetry()
	recordOpenAIWSContinuationPrevNotFoundDropRetry(true)
	recordOpenAIWSContinuationPrevNotFoundFailClosed("missing_local_anchor")
	recordOpenAIWSContinuationPrevNotFoundFailClosed("stale_local_anchor")
	recordOpenAIWSContinuationPreflightPingAlignRetry()
	recordOpenAIWSContinuationPreflightPingDropRetry(true)
	recordOpenAIWSContinuationPreflightPingFailClosed("missing_local_anchor")
	recordOpenAIWSContinuationPreflightPingFailClosed("stale_local_anchor")

	stats := OpenAIWSContinuationStats()
	require.Equal(t, int64(1), stats.ValidationRejectMissingCallIDTotal)
	require.Equal(t, int64(1), stats.ValidationRejectMissingItemReferenceTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundAlignRetryTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundDropRetryTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundDropSelfContainedRetryTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundFailClosedMissingAnchorTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundFailClosedStaleAnchorTotal)
	require.Equal(t, int64(1), stats.PreflightPingAlignRetryTotal)
	require.Equal(t, int64(1), stats.PreflightPingDropRetryTotal)
	require.Equal(t, int64(1), stats.PreflightPingDropSelfContainedRetryTotal)
	require.Equal(t, int64(1), stats.PreflightPingFailClosedMissingAnchorTotal)
	require.Equal(t, int64(1), stats.PreflightPingFailClosedStaleAnchorTotal)
}

func TestOpenAIWSContinuationStatsResetForTest(t *testing.T) {
	resetOpenAIWSContinuationStatsForTest()
	recordOpenAIWSContinuationPrevNotFoundDropRetry(false)
	recordOpenAIWSContinuationPreflightPingFailClosed("missing_local_anchor")

	resetOpenAIWSContinuationStatsForTest()
	stats := OpenAIWSContinuationStats()
	require.Equal(t, OpenAIWSContinuationStatsSnapshot{}, stats)
}

func TestOpenAIGatewayService_OpenAIWSContinuationRuntimeSnapshot(t *testing.T) {
	resetOpenAIWSContinuationStatsForTest()
	recordOpenAIWSContinuationPrevNotFoundAlignRetry()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			OpenAIWS: config.GatewayOpenAIWSConfig{
				Enabled:                                true,
				ResponsesWebsockets:                    true,
				ResponsesWebsocketsV2:                  true,
				IngressPreviousResponseRecoveryEnabled: true,
				StoreDisabledConnMode:                  "strict",
				StoreDisabledForceNewConn:              true,
				StickySessionTTLSeconds:                3600,
				StickyResponseIDTTLSeconds:             7200,
				StickyPreviousResponseTTLSeconds:       1800,
				MaxConnsPerAccount:                     4,
				MinIdlePerAccount:                      1,
				MaxIdlePerAccount:                      2,
				FallbackCooldownSeconds:                15,
				RetryTotalBudgetMS:                     2500,
			},
		},
	}
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	store := svc.getOpenAIWSStateStore()
	require.NotNil(t, store)
	require.NoError(t, store.BindResponseAccount(context.Background(), 7, "resp_runtime_1", 101, time.Minute))
	store.BindSessionTurnState(7, "session_hash_runtime", "turn_runtime", time.Minute)
	store.BindSessionLastResponse(7, "session_hash_runtime", "resp_runtime_1", time.Minute)

	snapshot := svc.OpenAIWSContinuationRuntimeSnapshot()
	require.Equal(t, int64(1), snapshot.Counters.PrevNotFoundAlignRetryTotal)
	require.True(t, snapshot.Config.Enabled)
	require.True(t, snapshot.Config.ResponsesWebsocketsV2)
	require.Equal(t, 3600, snapshot.Config.StickySessionTTLSeconds)
	require.Equal(t, 7200, snapshot.Config.StickyResponseIDTTLSeconds)
	require.Equal(t, "strict", snapshot.Config.StoreDisabledConnMode)
	require.Equal(t, 1, snapshot.State.ResponseAccountLocalEntries)
	require.Equal(t, 1, snapshot.State.SessionTurnStateEntries)
	require.Equal(t, 1, snapshot.State.SessionLastResponseEntries)
	require.Equal(t, int64(1), snapshot.State.ResponseAccountBindTotal)
}
