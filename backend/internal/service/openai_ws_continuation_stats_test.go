package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSContinuationStatsSnapshot(t *testing.T) {
	resetOpenAIWSContinuationStatsForTest()
	RecordOpenAIWSContinuationValidationReject("function_call_output_missing_call_id")
	RecordOpenAIWSContinuationValidationReject("function_call_output_missing_item_reference")
	recordOpenAIWSContinuationPrevNotFoundAlignRetry()
	recordOpenAIWSContinuationPrevNotFoundDropRetry(true)
	recordOpenAIWSContinuationPrevNotFoundFailClosedMissingAnchor()
	recordOpenAIWSContinuationPreflightPingAlignRetry()
	recordOpenAIWSContinuationPreflightPingDropRetry(true)
	recordOpenAIWSContinuationPreflightPingFailClosedMissingAnchor()

	stats := OpenAIWSContinuationStats()
	require.Equal(t, int64(1), stats.ValidationRejectMissingCallIDTotal)
	require.Equal(t, int64(1), stats.ValidationRejectMissingItemReferenceTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundAlignRetryTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundDropRetryTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundDropSelfContainedRetryTotal)
	require.Equal(t, int64(1), stats.PrevNotFoundFailClosedMissingAnchorTotal)
	require.Equal(t, int64(1), stats.PreflightPingAlignRetryTotal)
	require.Equal(t, int64(1), stats.PreflightPingDropRetryTotal)
	require.Equal(t, int64(1), stats.PreflightPingDropSelfContainedRetryTotal)
	require.Equal(t, int64(1), stats.PreflightPingFailClosedMissingAnchorTotal)
}

func TestOpenAIWSContinuationStatsResetForTest(t *testing.T) {
	resetOpenAIWSContinuationStatsForTest()
	recordOpenAIWSContinuationPrevNotFoundDropRetry(false)
	recordOpenAIWSContinuationPreflightPingFailClosedMissingAnchor()

	resetOpenAIWSContinuationStatsForTest()
	stats := OpenAIWSContinuationStats()
	require.Equal(t, OpenAIWSContinuationStatsSnapshot{}, stats)
}
