package service

import (
	"strings"
	"sync/atomic"
)

// OpenAIWSContinuationStatsSnapshot 提供 websocket continuation 热路径的轻量快照。
// 该统计用于本地诊断与回归验证，不承担账单或强一致监控语义。
type OpenAIWSContinuationStatsSnapshot struct {
	ValidationRejectMissingCallIDTotal        int64 `json:"validation_reject_missing_call_id_total"`
	ValidationRejectMissingItemReferenceTotal int64 `json:"validation_reject_missing_item_reference_total"`
	TurnStoreFallbackTotal                    int64 `json:"turn_store_fallback_total"`
	WSToHTTPMidSessionTotal                   int64 `json:"ws_to_http_mid_session_total"`
	PreviousResponseRecoveredFromSessionTotal int64 `json:"previous_response_recovered_from_session_total"`
	PreviousResponseStrippedMidSessionTotal   int64 `json:"previous_response_stripped_mid_session_total"`
	AccountSwitchWithCacheDropTotal           int64 `json:"account_switch_with_cache_drop_total"`
	AnchoredCrossAccountSwitchBlockedTotal    int64 `json:"anchored_cross_account_switch_blocked_total"`
	StrongCohortFallbackTotal                 int64 `json:"strong_cohort_fallback_total"`
	StrongCohortDegradeBlockedTotal           int64 `json:"strong_cohort_degrade_blocked_total"`
	CacheAffinitySelectionTotal               int64 `json:"cache_affinity_selection_total"`
	DuplicateTurnRetryBlockedAfterEmitTotal   int64 `json:"duplicate_turn_retry_blocked_after_emit_total"`
	EmittedBytesBeforeRetryTotal              int64 `json:"emitted_bytes_before_retry_total"`
	TurnReuseProcessingConflictTotal          int64 `json:"turn_reuse_processing_conflict_total"`
	TurnReuseEmittedConflictTotal             int64 `json:"turn_reuse_emitted_conflict_total"`
	TurnReuseCompletedConflictTotal           int64 `json:"turn_reuse_completed_conflict_total"`
	SessionStickyRebindFromLastResponseTotal  int64 `json:"session_sticky_rebind_from_last_response_total"`
	PrevNotFoundAlignRetryTotal               int64 `json:"prev_not_found_align_retry_total"`
	PrevNotFoundDropRetryTotal                int64 `json:"prev_not_found_drop_retry_total"`
	PrevNotFoundDropSelfContainedRetryTotal   int64 `json:"prev_not_found_drop_self_contained_retry_total"`
	PrevNotFoundFailClosedMissingAnchorTotal  int64 `json:"prev_not_found_fail_closed_missing_anchor_total"`
	PrevNotFoundFailClosedStaleAnchorTotal    int64 `json:"prev_not_found_fail_closed_stale_anchor_total"`
	PreflightPingAlignRetryTotal              int64 `json:"preflight_ping_align_retry_total"`
	PreflightPingDropRetryTotal               int64 `json:"preflight_ping_drop_retry_total"`
	PreflightPingDropSelfContainedRetryTotal  int64 `json:"preflight_ping_drop_self_contained_retry_total"`
	PreflightPingFailClosedMissingAnchorTotal int64 `json:"preflight_ping_fail_closed_missing_anchor_total"`
	PreflightPingFailClosedStaleAnchorTotal   int64 `json:"preflight_ping_fail_closed_stale_anchor_total"`
}

var (
	openAIWSContinuationValidationRejectMissingCallIDTotal        atomic.Int64
	openAIWSContinuationValidationRejectMissingItemReferenceTotal atomic.Int64
	openAIWSContinuationTurnStoreFallbackTotal                    atomic.Int64
	openAIWSContinuationWSToHTTPMidSessionTotal                   atomic.Int64
	openAIWSContinuationPreviousResponseRecoveredFromSessionTotal atomic.Int64
	openAIWSContinuationPreviousResponseStrippedMidSessionTotal   atomic.Int64
	openAIWSContinuationAccountSwitchWithCacheDropTotal           atomic.Int64
	openAIWSContinuationAnchoredCrossAccountSwitchBlockedTotal    atomic.Int64
	openAIWSContinuationStrongCohortFallbackTotal                 atomic.Int64
	openAIWSContinuationStrongCohortDegradeBlockedTotal           atomic.Int64
	openAIWSContinuationCacheAffinitySelectionTotal               atomic.Int64
	openAIWSContinuationDuplicateTurnRetryBlockedAfterEmitTotal   atomic.Int64
	openAIWSContinuationEmittedBytesBeforeRetryTotal              atomic.Int64
	openAIWSContinuationTurnReuseProcessingConflictTotal          atomic.Int64
	openAIWSContinuationTurnReuseEmittedConflictTotal             atomic.Int64
	openAIWSContinuationTurnReuseCompletedConflictTotal           atomic.Int64
	openAIWSContinuationSessionStickyRebindFromLastResponseTotal  atomic.Int64
	openAIWSContinuationPrevNotFoundAlignRetryTotal               atomic.Int64
	openAIWSContinuationPrevNotFoundDropRetryTotal                atomic.Int64
	openAIWSContinuationPrevNotFoundDropSelfContainedRetryTotal   atomic.Int64
	openAIWSContinuationPrevNotFoundFailClosedMissingAnchorTotal  atomic.Int64
	openAIWSContinuationPrevNotFoundFailClosedStaleAnchorTotal    atomic.Int64
	openAIWSContinuationPreflightPingAlignRetryTotal              atomic.Int64
	openAIWSContinuationPreflightPingDropRetryTotal               atomic.Int64
	openAIWSContinuationPreflightPingDropSelfContainedRetryTotal  atomic.Int64
	openAIWSContinuationPreflightPingFailClosedMissingAnchorTotal atomic.Int64
	openAIWSContinuationPreflightPingFailClosedStaleAnchorTotal   atomic.Int64
)

func OpenAIWSContinuationStats() OpenAIWSContinuationStatsSnapshot {
	return OpenAIWSContinuationStatsSnapshot{
		ValidationRejectMissingCallIDTotal:        openAIWSContinuationValidationRejectMissingCallIDTotal.Load(),
		ValidationRejectMissingItemReferenceTotal: openAIWSContinuationValidationRejectMissingItemReferenceTotal.Load(),
		TurnStoreFallbackTotal:                    openAIWSContinuationTurnStoreFallbackTotal.Load(),
		WSToHTTPMidSessionTotal:                   openAIWSContinuationWSToHTTPMidSessionTotal.Load(),
		PreviousResponseRecoveredFromSessionTotal: openAIWSContinuationPreviousResponseRecoveredFromSessionTotal.Load(),
		PreviousResponseStrippedMidSessionTotal:   openAIWSContinuationPreviousResponseStrippedMidSessionTotal.Load(),
		AccountSwitchWithCacheDropTotal:           openAIWSContinuationAccountSwitchWithCacheDropTotal.Load(),
		AnchoredCrossAccountSwitchBlockedTotal:    openAIWSContinuationAnchoredCrossAccountSwitchBlockedTotal.Load(),
		StrongCohortFallbackTotal:                 openAIWSContinuationStrongCohortFallbackTotal.Load(),
		StrongCohortDegradeBlockedTotal:           openAIWSContinuationStrongCohortDegradeBlockedTotal.Load(),
		CacheAffinitySelectionTotal:               openAIWSContinuationCacheAffinitySelectionTotal.Load(),
		DuplicateTurnRetryBlockedAfterEmitTotal:   openAIWSContinuationDuplicateTurnRetryBlockedAfterEmitTotal.Load(),
		EmittedBytesBeforeRetryTotal:              openAIWSContinuationEmittedBytesBeforeRetryTotal.Load(),
		TurnReuseProcessingConflictTotal:          openAIWSContinuationTurnReuseProcessingConflictTotal.Load(),
		TurnReuseEmittedConflictTotal:             openAIWSContinuationTurnReuseEmittedConflictTotal.Load(),
		TurnReuseCompletedConflictTotal:           openAIWSContinuationTurnReuseCompletedConflictTotal.Load(),
		SessionStickyRebindFromLastResponseTotal:  openAIWSContinuationSessionStickyRebindFromLastResponseTotal.Load(),
		PrevNotFoundAlignRetryTotal:               openAIWSContinuationPrevNotFoundAlignRetryTotal.Load(),
		PrevNotFoundDropRetryTotal:                openAIWSContinuationPrevNotFoundDropRetryTotal.Load(),
		PrevNotFoundDropSelfContainedRetryTotal:   openAIWSContinuationPrevNotFoundDropSelfContainedRetryTotal.Load(),
		PrevNotFoundFailClosedMissingAnchorTotal:  openAIWSContinuationPrevNotFoundFailClosedMissingAnchorTotal.Load(),
		PrevNotFoundFailClosedStaleAnchorTotal:    openAIWSContinuationPrevNotFoundFailClosedStaleAnchorTotal.Load(),
		PreflightPingAlignRetryTotal:              openAIWSContinuationPreflightPingAlignRetryTotal.Load(),
		PreflightPingDropRetryTotal:               openAIWSContinuationPreflightPingDropRetryTotal.Load(),
		PreflightPingDropSelfContainedRetryTotal:  openAIWSContinuationPreflightPingDropSelfContainedRetryTotal.Load(),
		PreflightPingFailClosedMissingAnchorTotal: openAIWSContinuationPreflightPingFailClosedMissingAnchorTotal.Load(),
		PreflightPingFailClosedStaleAnchorTotal:   openAIWSContinuationPreflightPingFailClosedStaleAnchorTotal.Load(),
	}
}

func recordOpenAIWSContinuationValidationRejectMissingCallID() {
	openAIWSContinuationValidationRejectMissingCallIDTotal.Add(1)
}

func recordOpenAIWSContinuationValidationRejectMissingItemReference() {
	openAIWSContinuationValidationRejectMissingItemReferenceTotal.Add(1)
}

func RecordOpenAIWSContinuationValidationReject(reason string) {
	switch reason {
	case "function_call_output_missing_call_id":
		recordOpenAIWSContinuationValidationRejectMissingCallID()
	case "function_call_output_missing_item_reference":
		recordOpenAIWSContinuationValidationRejectMissingItemReference()
	}
}

func RecordOpenAIWSContinuationWSToHTTPMidSession() {
	openAIWSContinuationWSToHTTPMidSessionTotal.Add(1)
}

func RecordOpenAIWSContinuationTurnStoreFallback() {
	openAIWSContinuationTurnStoreFallbackTotal.Add(1)
}

func RecordOpenAIWSContinuationPreviousResponseRecoveredFromSession() {
	openAIWSContinuationPreviousResponseRecoveredFromSessionTotal.Add(1)
}

func RecordOpenAIWSContinuationPreviousResponseStrippedMidSession() {
	openAIWSContinuationPreviousResponseStrippedMidSessionTotal.Add(1)
}

func RecordOpenAIWSContinuationAccountSwitchWithCacheDrop() {
	openAIWSContinuationAccountSwitchWithCacheDropTotal.Add(1)
}

func RecordOpenAIWSContinuationAnchoredCrossAccountSwitchBlocked() {
	openAIWSContinuationAnchoredCrossAccountSwitchBlockedTotal.Add(1)
}

func RecordOpenAIWSContinuationStrongCohortFallback() {
	openAIWSContinuationStrongCohortFallbackTotal.Add(1)
}

func RecordOpenAIWSContinuationStrongCohortDegradeBlocked() {
	openAIWSContinuationStrongCohortDegradeBlockedTotal.Add(1)
}

func RecordOpenAIWSContinuationCacheAffinitySelection() {
	openAIWSContinuationCacheAffinitySelectionTotal.Add(1)
}

func RecordOpenAIWSContinuationDuplicateTurnRetryBlockedAfterEmit() {
	openAIWSContinuationDuplicateTurnRetryBlockedAfterEmitTotal.Add(1)
}

func RecordOpenAIWSContinuationEmittedBytesBeforeRetry() {
	openAIWSContinuationEmittedBytesBeforeRetryTotal.Add(1)
}

func RecordOpenAIWSContinuationTurnReuseConflict(phase string) {
	switch strings.TrimSpace(phase) {
	case "emitted":
		openAIWSContinuationTurnReuseEmittedConflictTotal.Add(1)
	case "completed":
		openAIWSContinuationTurnReuseCompletedConflictTotal.Add(1)
	default:
		openAIWSContinuationTurnReuseProcessingConflictTotal.Add(1)
	}
}

func recordOpenAIWSContinuationSessionStickyRebindFromLastResponse() {
	openAIWSContinuationSessionStickyRebindFromLastResponseTotal.Add(1)
}

func recordOpenAIWSContinuationPrevNotFoundAlignRetry() {
	openAIWSContinuationPrevNotFoundAlignRetryTotal.Add(1)
}

func recordOpenAIWSContinuationPrevNotFoundDropRetry(selfContained bool) {
	openAIWSContinuationPrevNotFoundDropRetryTotal.Add(1)
	if selfContained {
		openAIWSContinuationPrevNotFoundDropSelfContainedRetryTotal.Add(1)
	}
}

func recordOpenAIWSContinuationPrevNotFoundFailClosed(reason string) {
	switch strings.TrimSpace(reason) {
	case "stale_local_anchor":
		openAIWSContinuationPrevNotFoundFailClosedStaleAnchorTotal.Add(1)
	default:
		openAIWSContinuationPrevNotFoundFailClosedMissingAnchorTotal.Add(1)
	}
}

func recordOpenAIWSContinuationPreflightPingAlignRetry() {
	openAIWSContinuationPreflightPingAlignRetryTotal.Add(1)
}

func recordOpenAIWSContinuationPreflightPingDropRetry(selfContained bool) {
	openAIWSContinuationPreflightPingDropRetryTotal.Add(1)
	if selfContained {
		openAIWSContinuationPreflightPingDropSelfContainedRetryTotal.Add(1)
	}
}

func recordOpenAIWSContinuationPreflightPingFailClosed(reason string) {
	switch strings.TrimSpace(reason) {
	case "stale_local_anchor":
		openAIWSContinuationPreflightPingFailClosedStaleAnchorTotal.Add(1)
	default:
		openAIWSContinuationPreflightPingFailClosedMissingAnchorTotal.Add(1)
	}
}

func resetOpenAIWSContinuationStatsForTest() {
	openAIWSContinuationValidationRejectMissingCallIDTotal.Store(0)
	openAIWSContinuationValidationRejectMissingItemReferenceTotal.Store(0)
	openAIWSContinuationTurnStoreFallbackTotal.Store(0)
	openAIWSContinuationWSToHTTPMidSessionTotal.Store(0)
	openAIWSContinuationPreviousResponseRecoveredFromSessionTotal.Store(0)
	openAIWSContinuationPreviousResponseStrippedMidSessionTotal.Store(0)
	openAIWSContinuationAccountSwitchWithCacheDropTotal.Store(0)
	openAIWSContinuationAnchoredCrossAccountSwitchBlockedTotal.Store(0)
	openAIWSContinuationStrongCohortFallbackTotal.Store(0)
	openAIWSContinuationStrongCohortDegradeBlockedTotal.Store(0)
	openAIWSContinuationCacheAffinitySelectionTotal.Store(0)
	openAIWSContinuationDuplicateTurnRetryBlockedAfterEmitTotal.Store(0)
	openAIWSContinuationEmittedBytesBeforeRetryTotal.Store(0)
	openAIWSContinuationTurnReuseProcessingConflictTotal.Store(0)
	openAIWSContinuationTurnReuseEmittedConflictTotal.Store(0)
	openAIWSContinuationTurnReuseCompletedConflictTotal.Store(0)
	openAIWSContinuationSessionStickyRebindFromLastResponseTotal.Store(0)
	openAIWSContinuationPrevNotFoundAlignRetryTotal.Store(0)
	openAIWSContinuationPrevNotFoundDropRetryTotal.Store(0)
	openAIWSContinuationPrevNotFoundDropSelfContainedRetryTotal.Store(0)
	openAIWSContinuationPrevNotFoundFailClosedMissingAnchorTotal.Store(0)
	openAIWSContinuationPrevNotFoundFailClosedStaleAnchorTotal.Store(0)
	openAIWSContinuationPreflightPingAlignRetryTotal.Store(0)
	openAIWSContinuationPreflightPingDropRetryTotal.Store(0)
	openAIWSContinuationPreflightPingDropSelfContainedRetryTotal.Store(0)
	openAIWSContinuationPreflightPingFailClosedMissingAnchorTotal.Store(0)
	openAIWSContinuationPreflightPingFailClosedStaleAnchorTotal.Store(0)
}

// ResetOpenAIWSContinuationStatsForTest 重置 continuation 统计，供跨包测试复位使用。
func ResetOpenAIWSContinuationStatsForTest() {
	resetOpenAIWSContinuationStatsForTest()
}
