package service

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

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
	Counters   OpenAIWSContinuationStatsSnapshot      `json:"counters"`
	Config     OpenAIWSContinuationConfigSnapshot     `json:"config"`
	State      OpenAIWSStateStoreDebugSnapshot        `json:"state"`
	Capability OpenAIWSContinuationCapabilitySnapshot `json:"capability"`
}

type OpenAIWSContinuationCapabilitySnapshot struct {
	GlobalTotalOpenAIAccounts      int64                                   `json:"global_total_openai_accounts"`
	GlobalCompactCapableAccounts   int64                                   `json:"global_compact_capable_accounts"`
	GlobalStrongCohortAccounts     int64                                   `json:"global_strong_cohort_accounts"`
	GlobalDegradedOnlyAccounts     int64                                   `json:"global_degraded_only_accounts"`
	GlobalCompactIncapableAccounts int64                                   `json:"global_compact_incapable_accounts"`
	HasAnyCompactCapableAccount    bool                                    `json:"has_any_compact_capable_account"`
	HasAnyStrongCohortAccount      bool                                    `json:"has_any_strong_cohort_account"`
	HasAnyDegradedOnlyAccount      bool                                    `json:"has_any_degraded_only_account"`
	Groups                         []OpenAIWSContinuationGroupAvailability `json:"groups"`
}

type OpenAIWSContinuationGroupAvailability struct {
	GroupID                        int64    `json:"group_id"`
	GroupName                      string   `json:"group_name"`
	TotalSchedulableOpenAIAccounts int64    `json:"total_schedulable_openai_accounts"`
	OAuthSchedulableAccounts       int64    `json:"oauth_schedulable_accounts"`
	APIKeySchedulableAccounts      int64    `json:"apikey_schedulable_accounts"`
	CompactCapableAccounts         int64    `json:"compact_capable_accounts"`
	CompactIncapableAccounts       int64    `json:"compact_incapable_accounts"`
	StrongCohortAccounts           int64    `json:"strong_cohort_accounts"`
	DegradedOnlyAccounts           int64    `json:"degraded_only_accounts"`
	CompactCapableAccountNames     []string `json:"compact_capable_account_names,omitempty"`
	CompactIncapableAccountNames   []string `json:"compact_incapable_account_names,omitempty"`
	StrongCohortAccountNames       []string `json:"strong_cohort_account_names,omitempty"`
	DegradedOnlyAccountNames       []string `json:"degraded_only_account_names,omitempty"`
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
	snapshot.Capability = s.buildOpenAIWSContinuationCapabilitySnapshot(context.Background())
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

func (s *OpenAIGatewayService) buildOpenAIWSContinuationCapabilitySnapshot(ctx context.Context) OpenAIWSContinuationCapabilitySnapshot {
	if s == nil || s.accountRepo == nil {
		return OpenAIWSContinuationCapabilitySnapshot{}
	}

	accounts, err := s.accountRepo.ListSchedulableByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return OpenAIWSContinuationCapabilitySnapshot{}
	}
	if len(accounts) == 0 {
		fallbackAccounts, fallbackErr := s.accountRepo.ListSchedulable(ctx)
		if fallbackErr == nil {
			for i := range fallbackAccounts {
				if fallbackAccounts[i].IsOpenAI() {
					accounts = append(accounts, fallbackAccounts[i])
				}
			}
		}
	}
	if len(accounts) == 0 {
		return OpenAIWSContinuationCapabilitySnapshot{}
	}

	type groupAccumulator struct {
		snapshot OpenAIWSContinuationGroupAvailability
	}

	groups := make(map[int64]*groupAccumulator)
	addGroupAccount := func(groupID int64, groupName string, account Account) {
		acc := groups[groupID]
		if acc == nil {
			acc = &groupAccumulator{
				snapshot: OpenAIWSContinuationGroupAvailability{
					GroupID:   groupID,
					GroupName: normalizedOpenAIContinuationGroupName(groupID, groupName),
				},
			}
			groups[groupID] = acc
		}
		if acc.snapshot.GroupName == "" {
			acc.snapshot.GroupName = normalizedOpenAIContinuationGroupName(groupID, groupName)
		}
		acc.snapshot.TotalSchedulableOpenAIAccounts++
		if account.IsOAuth() {
			acc.snapshot.OAuthSchedulableAccounts++
		}
		if account.IsOpenAIApiKey() {
			acc.snapshot.APIKeySchedulableAccounts++
		}
		if s.SupportsOpenAIResponsesCompactForRuntime(&account) {
			acc.snapshot.CompactCapableAccounts++
			acc.snapshot.CompactCapableAccountNames = append(acc.snapshot.CompactCapableAccountNames, account.Name)
		} else {
			acc.snapshot.CompactIncapableAccounts++
			acc.snapshot.CompactIncapableAccountNames = append(acc.snapshot.CompactIncapableAccountNames, account.Name)
		}
		if s.resolveOpenAIContinuationCohortForRuntime(&account) == OpenAIContinuationCohortStrong {
			acc.snapshot.StrongCohortAccounts++
			acc.snapshot.StrongCohortAccountNames = append(acc.snapshot.StrongCohortAccountNames, account.Name)
		} else {
			acc.snapshot.DegradedOnlyAccounts++
			acc.snapshot.DegradedOnlyAccountNames = append(acc.snapshot.DegradedOnlyAccountNames, account.Name)
		}
	}

	snapshot := OpenAIWSContinuationCapabilitySnapshot{}
	for i := range accounts {
		account := accounts[i]
		if !account.IsOpenAI() {
			continue
		}
		snapshot.GlobalTotalOpenAIAccounts++
		if s.SupportsOpenAIResponsesCompactForRuntime(&account) {
			snapshot.GlobalCompactCapableAccounts++
		} else {
			snapshot.GlobalCompactIncapableAccounts++
		}
		if s.resolveOpenAIContinuationCohortForRuntime(&account) == OpenAIContinuationCohortStrong {
			snapshot.GlobalStrongCohortAccounts++
		} else {
			snapshot.GlobalDegradedOnlyAccounts++
		}

		groupNames := make(map[int64]string)
		for _, group := range account.Groups {
			if group == nil {
				continue
			}
			groupNames[group.ID] = group.Name
		}
		if len(account.GroupIDs) == 0 {
			addGroupAccount(0, "", account)
			continue
		}
		for _, groupID := range account.GroupIDs {
			addGroupAccount(groupID, groupNames[groupID], account)
		}
	}

	snapshot.HasAnyCompactCapableAccount = snapshot.GlobalCompactCapableAccounts > 0
	snapshot.HasAnyStrongCohortAccount = snapshot.GlobalStrongCohortAccounts > 0
	snapshot.HasAnyDegradedOnlyAccount = snapshot.GlobalDegradedOnlyAccounts > 0

	if len(groups) == 0 {
		return snapshot
	}

	snapshot.Groups = make([]OpenAIWSContinuationGroupAvailability, 0, len(groups))
	for _, acc := range groups {
		groupSnapshot := acc.snapshot
		sort.Strings(groupSnapshot.CompactCapableAccountNames)
		sort.Strings(groupSnapshot.CompactIncapableAccountNames)
		sort.Strings(groupSnapshot.StrongCohortAccountNames)
		sort.Strings(groupSnapshot.DegradedOnlyAccountNames)
		snapshot.Groups = append(snapshot.Groups, groupSnapshot)
	}
	sort.Slice(snapshot.Groups, func(i, j int) bool {
		if snapshot.Groups[i].GroupName == snapshot.Groups[j].GroupName {
			return snapshot.Groups[i].GroupID < snapshot.Groups[j].GroupID
		}
		return snapshot.Groups[i].GroupName < snapshot.Groups[j].GroupName
	})
	return snapshot
}

func (s *OpenAIGatewayService) resolveOpenAIContinuationCohortForRuntime(account *Account) OpenAIContinuationCohort {
	if s == nil || account == nil {
		return OpenAIContinuationCohortDegraded
	}
	if s.getOpenAIWSProtocolResolver().Resolve(account).Transport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		return OpenAIContinuationCohortStrong
	}
	return OpenAIContinuationCohortDegraded
}

func normalizedOpenAIContinuationGroupName(groupID int64, groupName string) string {
	groupName = strings.TrimSpace(groupName)
	if groupName != "" {
		return groupName
	}
	if groupID <= 0 {
		return "Ungrouped"
	}
	return "group#" + strconv.FormatInt(groupID, 10)
}
