package service

import (
	"strings"
	"sync/atomic"
)

// OpenAIWSContinuationStatsSnapshot 提供 websocket continuation 热路径的轻量快照。
// 该统计用于本地诊断与回归验证，不承担账单或强一致监控语义。
type OpenAIWSContinuationStatsSnapshot struct {
	ValidationRejectMissingCallIDTotal        int64
	ValidationRejectMissingItemReferenceTotal int64
	SessionStickyRebindFromLastResponseTotal  int64
	PrevNotFoundAlignRetryTotal               int64
	PrevNotFoundDropRetryTotal                int64
	PrevNotFoundDropSelfContainedRetryTotal   int64
	PrevNotFoundFailClosedMissingAnchorTotal  int64
	PrevNotFoundFailClosedStaleAnchorTotal    int64
	PreflightPingAlignRetryTotal              int64
	PreflightPingDropRetryTotal               int64
	PreflightPingDropSelfContainedRetryTotal  int64
	PreflightPingFailClosedMissingAnchorTotal int64
	PreflightPingFailClosedStaleAnchorTotal   int64
}

var (
	openAIWSContinuationValidationRejectMissingCallIDTotal        atomic.Int64
	openAIWSContinuationValidationRejectMissingItemReferenceTotal atomic.Int64
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
