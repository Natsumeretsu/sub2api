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
	RecordOpenAIWSContinuationTurnStoreFallback()
	RecordOpenAIWSContinuationWSToHTTPMidSession()
	RecordOpenAIWSContinuationPreviousResponseRecoveredFromSession()
	RecordOpenAIWSContinuationHTTPPreviousResponseUnsupported()
	RecordOpenAIWSContinuationPreviousResponseStrippedMidSession()
	RecordOpenAIWSContinuationAccountSwitchWithCacheDrop()
	RecordOpenAIWSContinuationAnchoredCrossAccountSwitchBlocked()
	RecordOpenAIWSContinuationStrongCohortFallback()
	RecordOpenAIWSContinuationStrongCohortDegradeBlocked()
	RecordOpenAIWSContinuationDuplicateTurnRetryBlockedAfterEmit()
	RecordOpenAIWSContinuationEmittedBytesBeforeRetry()
	recordOpenAIWSContinuationSessionStickyRebindFromLastResponse()
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
	require.Equal(t, int64(1), stats.TurnStoreFallbackTotal)
	require.Equal(t, int64(1), stats.WSToHTTPMidSessionTotal)
	require.Equal(t, int64(1), stats.PreviousResponseRecoveredFromSessionTotal)
	require.Equal(t, int64(1), stats.HTTPPreviousResponseUnsupportedTotal)
	require.Equal(t, int64(1), stats.PreviousResponseStrippedMidSessionTotal)
	require.Equal(t, int64(1), stats.AccountSwitchWithCacheDropTotal)
	require.Equal(t, int64(1), stats.AnchoredCrossAccountSwitchBlockedTotal)
	require.Equal(t, int64(1), stats.StrongCohortFallbackTotal)
	require.Equal(t, int64(1), stats.StrongCohortDegradeBlockedTotal)
	require.Equal(t, int64(1), stats.DuplicateTurnRetryBlockedAfterEmitTotal)
	require.Equal(t, int64(1), stats.EmittedBytesBeforeRetryTotal)
	require.Equal(t, int64(1), stats.SessionStickyRebindFromLastResponseTotal)
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
	RecordOpenAIWSContinuationWSToHTTPMidSession()
	RecordOpenAIWSContinuationPreviousResponseRecoveredFromSession()
	RecordOpenAIWSContinuationStrongCohortFallback()
	RecordOpenAIWSContinuationStrongCohortDegradeBlocked()
	recordOpenAIWSContinuationSessionStickyRebindFromLastResponse()
	recordOpenAIWSContinuationPrevNotFoundAlignRetry()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			OpenAIWS: config.GatewayOpenAIWSConfig{
				Enabled:                                true,
				OAuthEnabled:                           true,
				APIKeyEnabled:                          true,
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
	teamGroup := &Group{ID: 2, Name: "Team号池"}
	privateGroup := &Group{ID: 3, Name: "Private"}
	accountRepo := &stubOpenAIAccountRepo{
		accounts: []Account{
			{
				ID:       12,
				Name:     "aak9e9qvfc@zhurunqi.love",
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Extra: map[string]any{
					"openai_passthrough":                           true,
					"openai_oauth_responses_websockets_v2_enabled": true,
					"openai_oauth_responses_websockets_v2_mode":    OpenAIWSIngressModePassthrough,
				},
				GroupIDs: []int64{2},
				Groups:   []*Group{teamGroup},
			},
			{
				ID:       14,
				Name:     "PackyCode",
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Extra: map[string]any{
					"openai_passthrough":                            true,
					"openai_apikey_responses_websockets_v2_enabled": true,
					"openai_apikey_responses_websockets_v2_mode":    OpenAIWSIngressModePassthrough,
				},
				GroupIDs: []int64{3},
				Groups:   []*Group{privateGroup},
			},
		},
	}
	openAIAccounts, err := accountRepo.ListSchedulableByPlatform(context.Background(), PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, openAIAccounts, 2)

	svc := NewOpenAIGatewayService(accountRepo, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, svc.accountRepo)
	serviceOpenAIAccounts, err := svc.accountRepo.ListSchedulableByPlatform(context.Background(), PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, serviceOpenAIAccounts, 2)
	store := svc.getOpenAIWSStateStore()
	require.NotNil(t, store)
	require.NoError(t, store.BindResponseAccount(context.Background(), 7, "resp_runtime_1", 101, time.Minute))
	store.BindSessionTurnState(7, "session_hash_runtime", "turn_runtime", time.Minute)
	store.BindSessionLastResponse(7, "session_hash_runtime", "resp_runtime_1", time.Minute)

	snapshot := svc.OpenAIWSContinuationRuntimeSnapshot()
	require.Equal(t, int64(1), snapshot.Counters.WSToHTTPMidSessionTotal)
	require.Equal(t, int64(1), snapshot.Counters.PreviousResponseRecoveredFromSessionTotal)
	require.Equal(t, int64(1), snapshot.Counters.StrongCohortFallbackTotal)
	require.Equal(t, int64(1), snapshot.Counters.StrongCohortDegradeBlockedTotal)
	require.Equal(t, int64(1), snapshot.Counters.SessionStickyRebindFromLastResponseTotal)
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
	require.Equal(t, int64(2), snapshot.Capability.GlobalTotalOpenAIAccounts)
	require.Equal(t, int64(1), snapshot.Capability.GlobalCompactCapableAccounts)
	require.Equal(t, int64(1), snapshot.Capability.GlobalStrongCohortAccounts)
	require.Equal(t, int64(1), snapshot.Capability.GlobalDegradedOnlyAccounts)
	require.Equal(t, int64(1), snapshot.Capability.GlobalCompactIncapableAccounts)
	require.Equal(t, int64(0), snapshot.Capability.GlobalHTTPStreamingCapableAccounts)
	require.Equal(t, int64(0), snapshot.Capability.GlobalHTTPStreamingIncapableAccounts)
	require.Equal(t, int64(2), snapshot.Capability.GlobalHTTPStreamingUnknownAccounts)
	require.True(t, snapshot.Capability.HasAnyCompactCapableAccount)
	require.True(t, snapshot.Capability.HasAnyStrongCohortAccount)
	require.True(t, snapshot.Capability.HasAnyDegradedOnlyAccount)
	require.False(t, snapshot.Capability.HasAnyHTTPStreamingCapableAccount)
	require.Len(t, snapshot.Capability.Groups, 2)

	var privateSnapshot OpenAIWSContinuationGroupAvailability
	var teamSnapshot OpenAIWSContinuationGroupAvailability
	for _, group := range snapshot.Capability.Groups {
		switch group.GroupID {
		case 2:
			teamSnapshot = group
		case 3:
			privateSnapshot = group
		}
	}

	require.Equal(t, "Team号池", teamSnapshot.GroupName)
	require.Equal(t, int64(1), teamSnapshot.TotalSchedulableOpenAIAccounts)
	require.Equal(t, int64(1), teamSnapshot.OAuthSchedulableAccounts)
	require.Equal(t, int64(1), teamSnapshot.CompactCapableAccounts)
	require.Equal(t, int64(1), teamSnapshot.StrongCohortAccounts)
	require.Equal(t, int64(1), teamSnapshot.HTTPStreamingUnknownAccounts)
	require.Equal(t, []string{"aak9e9qvfc@zhurunqi.love"}, teamSnapshot.CompactCapableAccountNames)
	require.Equal(t, []string{"aak9e9qvfc@zhurunqi.love"}, teamSnapshot.HTTPStreamingUnknownAccountNames)

	require.Equal(t, "Private", privateSnapshot.GroupName)
	require.Equal(t, int64(1), privateSnapshot.TotalSchedulableOpenAIAccounts)
	require.Equal(t, int64(1), privateSnapshot.APIKeySchedulableAccounts)
	require.Equal(t, int64(1), privateSnapshot.CompactIncapableAccounts)
	require.Equal(t, int64(0), privateSnapshot.StrongCohortAccounts)
	require.Equal(t, int64(1), privateSnapshot.DegradedOnlyAccounts)
	require.Equal(t, int64(1), privateSnapshot.HTTPStreamingUnknownAccounts)
	require.Equal(t, []string{"PackyCode"}, privateSnapshot.CompactIncapableAccountNames)
	require.Equal(t, []string{"PackyCode"}, privateSnapshot.DegradedOnlyAccountNames)
	require.Equal(t, []string{"PackyCode"}, privateSnapshot.HTTPStreamingUnknownAccountNames)
}
