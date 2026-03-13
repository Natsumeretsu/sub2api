package service

import "github.com/Wei-Shaw/sub2api/internal/config"

type OpenAIWSContinuationConfigSnapshot struct {
	Enabled                                bool   `json:"enabled"`
	ForceHTTP                              bool   `json:"force_http"`
	ResponsesWebsockets                    bool   `json:"responses_websockets"`
	ResponsesWebsocketsV2                  bool   `json:"responses_websockets_v2"`
	AllowStoreRecovery                     bool   `json:"allow_store_recovery"`
	IngressPreviousResponseRecoveryEnabled bool   `json:"ingress_previous_response_recovery_enabled"`
	StoreDisabledConnMode                  string `json:"store_disabled_conn_mode"`
	StoreDisabledForceNewConn              bool   `json:"store_disabled_force_new_conn"`
	StickySessionTTLSeconds                int    `json:"sticky_session_ttl_seconds"`
	StickyResponseIDTTLSeconds             int    `json:"sticky_response_id_ttl_seconds"`
	StickyPreviousResponseTTLSeconds       int    `json:"sticky_previous_response_ttl_seconds"`
	MaxConnsPerAccount                     int    `json:"max_conns_per_account"`
	MinIdlePerAccount                      int    `json:"min_idle_per_account"`
	MaxIdlePerAccount                      int    `json:"max_idle_per_account"`
	FallbackCooldownSeconds                int    `json:"fallback_cooldown_seconds"`
	RetryTotalBudgetMS                     int    `json:"retry_total_budget_ms"`
}

type OpenAIWSContinuationRuntimeSnapshot struct {
	Counters OpenAIWSContinuationStatsSnapshot  `json:"counters"`
	Config   OpenAIWSContinuationConfigSnapshot `json:"config"`
	State    OpenAIWSStateStoreDebugSnapshot    `json:"state"`
}

func (s *OpenAIGatewayService) OpenAIWSContinuationRuntimeSnapshot() OpenAIWSContinuationRuntimeSnapshot {
	snapshot := OpenAIWSContinuationRuntimeSnapshot{
		Counters: OpenAIWSContinuationStats(),
	}
	if s == nil {
		return snapshot
	}
	snapshot.Config = buildOpenAIWSContinuationConfigSnapshot(s.cfg)
	if store := s.getOpenAIWSStateStore(); store != nil {
		snapshot.State = store.DebugSnapshot()
	}
	return snapshot
}

func buildOpenAIWSContinuationConfigSnapshot(cfg *config.Config) OpenAIWSContinuationConfigSnapshot {
	if cfg == nil {
		return OpenAIWSContinuationConfigSnapshot{}
	}
	wsCfg := cfg.Gateway.OpenAIWS
	return OpenAIWSContinuationConfigSnapshot{
		Enabled:                                wsCfg.Enabled,
		ForceHTTP:                              wsCfg.ForceHTTP,
		ResponsesWebsockets:                    wsCfg.ResponsesWebsockets,
		ResponsesWebsocketsV2:                  wsCfg.ResponsesWebsocketsV2,
		AllowStoreRecovery:                     wsCfg.AllowStoreRecovery,
		IngressPreviousResponseRecoveryEnabled: wsCfg.IngressPreviousResponseRecoveryEnabled,
		StoreDisabledConnMode:                  wsCfg.StoreDisabledConnMode,
		StoreDisabledForceNewConn:              wsCfg.StoreDisabledForceNewConn,
		StickySessionTTLSeconds:                wsCfg.StickySessionTTLSeconds,
		StickyResponseIDTTLSeconds:             wsCfg.StickyResponseIDTTLSeconds,
		StickyPreviousResponseTTLSeconds:       wsCfg.StickyPreviousResponseTTLSeconds,
		MaxConnsPerAccount:                     wsCfg.MaxConnsPerAccount,
		MinIdlePerAccount:                      wsCfg.MinIdlePerAccount,
		MaxIdlePerAccount:                      wsCfg.MaxIdlePerAccount,
		FallbackCooldownSeconds:                wsCfg.FallbackCooldownSeconds,
		RetryTotalBudgetMS:                     wsCfg.RetryTotalBudgetMS,
	}
}
