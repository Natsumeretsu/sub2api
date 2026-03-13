package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type failingOpenAIResponsesTurnRepo struct{}

func (failingOpenAIResponsesTurnRepo) CreateProcessing(context.Context, *IdempotencyRecord) (bool, error) {
	return false, errors.New("primary unavailable")
}

func (failingOpenAIResponsesTurnRepo) GetByScopeAndKeyHash(context.Context, string, string) (*IdempotencyRecord, error) {
	return nil, errors.New("primary unavailable")
}

func (failingOpenAIResponsesTurnRepo) TryReclaim(context.Context, int64, string, time.Time, time.Time, time.Time) (bool, error) {
	return false, errors.New("primary unavailable")
}

func (failingOpenAIResponsesTurnRepo) ExtendProcessingLock(context.Context, int64, string, time.Time, time.Time) (bool, error) {
	return false, errors.New("primary unavailable")
}

func (failingOpenAIResponsesTurnRepo) MarkSucceeded(context.Context, int64, int, string, time.Time) error {
	return errors.New("primary unavailable")
}

func (failingOpenAIResponsesTurnRepo) MarkFailedRetryable(context.Context, int64, string, time.Time, time.Time) error {
	return errors.New("primary unavailable")
}

func (failingOpenAIResponsesTurnRepo) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, errors.New("primary unavailable")
}

func TestOpenAIResponsesTurnCoordinator_DuplicateLifecycleAndReclaim(t *testing.T) {
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	SetDefaultIdempotencyCoordinator(coordinator)
	t.Cleanup(func() {
		SetDefaultIdempotencyCoordinator(nil)
	})

	svc := &OpenAIGatewayService{}
	ctx := context.Background()
	desc := OpenAIResponsesTurnDescriptor{
		SessionHash:        "session-hash-1",
		PromptCacheKey:     "prompt-cache-key-1",
		Model:              "gpt-5.4",
		RequestedTransport: string(OpenAIUpstreamTransportResponsesWebsocketV2),
		RequestedCohort:    string(OpenAIContinuationCohortStrong),
		PayloadFingerprint: BuildOpenAIResponsesTurnPayloadFingerprint([]byte(`{"input":"hello"}`)),
		Stream:             true,
		TurnOrdinal:        1,
	}

	firstTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-1", desc)
	require.NoError(t, err)
	require.NotNil(t, firstTicket)
	require.Nil(t, duplicate)

	secondTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-1", desc)
	require.NoError(t, err)
	require.Nil(t, secondTicket)
	require.NotNil(t, duplicate)
	require.Equal(t, string(OpenAIResponsesTurnPhaseProcessing), duplicate.Phase)

	require.NoError(t, svc.MarkOpenAIResponsesTurnRetryable(ctx, firstTicket, "RETRYABLE"))

	reclaimedTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-1", desc)
	require.NoError(t, err)
	require.NotNil(t, reclaimedTicket)
	require.Nil(t, duplicate)

	require.NoError(t, svc.MarkOpenAIResponsesTurnEmitted(ctx, reclaimedTicket, OpenAIResponsesTurnStoredState{
		ResponseID:      "resp_emitted_1",
		AccountID:       1001,
		Cohort:          string(OpenAIContinuationCohortStrong),
		Transport:       string(OpenAIUpstreamTransportResponsesWebsocketV2),
		EmittedBytes:    true,
		ClientRequestID: "client-req-1",
	}))

	emittedTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-1", desc)
	require.NoError(t, err)
	require.Nil(t, emittedTicket)
	require.NotNil(t, duplicate)
	require.Equal(t, string(OpenAIResponsesTurnPhaseEmitted), duplicate.Phase)
	require.Equal(t, "resp_emitted_1", duplicate.State.ResponseID)

	completedDesc := desc
	completedDesc.TurnOrdinal = 2
	completedTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-2", completedDesc)
	require.NoError(t, err)
	require.NotNil(t, completedTicket)
	require.Nil(t, duplicate)

	require.NoError(t, svc.MarkOpenAIResponsesTurnCompleted(ctx, completedTicket, OpenAIResponsesTurnStoredState{
		ResponseID:      "resp_completed_1",
		AccountID:       1002,
		Cohort:          string(OpenAIContinuationCohortStrong),
		Transport:       string(OpenAIUpstreamTransportResponsesWebsocketV2),
		ClientRequestID: "client-req-2",
	}))

	completedRetryTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-2", completedDesc)
	require.NoError(t, err)
	require.Nil(t, completedRetryTicket)
	require.NotNil(t, duplicate)
	require.Equal(t, string(OpenAIResponsesTurnPhaseCompleted), duplicate.Phase)
	require.Equal(t, "resp_completed_1", duplicate.State.ResponseID)
}

func TestOpenAIResponsesTurnCoordinator_IgnoresDescriptorDriftInFingerprint(t *testing.T) {
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	SetDefaultIdempotencyCoordinator(coordinator)
	t.Cleanup(func() {
		SetDefaultIdempotencyCoordinator(nil)
	})

	svc := &OpenAIGatewayService{}
	ctx := context.Background()
	desc := OpenAIResponsesTurnDescriptor{
		SessionHash:        "session-hash-transport-drift",
		PromptCacheKey:     "prompt-cache-key-transport-drift",
		Model:              "gpt-5.4",
		RequestedTransport: string(OpenAIUpstreamTransportAny),
		RequestedCohort:    string(OpenAIContinuationCohortDegraded),
		PayloadFingerprint: BuildOpenAIResponsesTurnPayloadFingerprint([]byte(`{"input":"hello"}`)),
		Stream:             true,
		TurnOrdinal:        1,
	}

	firstTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-drift", desc)
	require.NoError(t, err)
	require.NotNil(t, firstTicket)
	require.Nil(t, duplicate)

	strongDesc := desc
	strongDesc.PromptCacheKey = "prompt-cache-key-transport-drift-v2"
	strongDesc.Model = "gpt-5.4-codex"
	strongDesc.Stream = false
	strongDesc.RequestedTransport = string(OpenAIUpstreamTransportResponsesWebsocketV2)
	strongDesc.RequestedCohort = string(OpenAIContinuationCohortStrong)
	secondTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 12, "turn-key-drift", strongDesc)
	require.NoError(t, err)
	require.Nil(t, secondTicket)
	require.NotNil(t, duplicate)
	require.Equal(t, string(OpenAIResponsesTurnPhaseProcessing), duplicate.Phase)
}

func TestOpenAIResponsesTurnCoordinator_FallsBackToMemoryStoreWhenPrimaryUnavailable(t *testing.T) {
	resetOpenAIWSContinuationStatsForTest()
	coordinator := NewIdempotencyCoordinator(failingOpenAIResponsesTurnRepo{}, DefaultIdempotencyConfig())
	SetDefaultIdempotencyCoordinator(coordinator)
	t.Cleanup(func() {
		SetDefaultIdempotencyCoordinator(nil)
		resetOpenAIWSContinuationStatsForTest()
	})

	svc := &OpenAIGatewayService{}
	ctx := context.Background()
	desc := OpenAIResponsesTurnDescriptor{
		SessionHash:        "session-fallback-1",
		PromptCacheKey:     "prompt-cache-fallback-1",
		Model:              "gpt-5.4",
		ClientRequestID:    "client-fallback-1",
		PayloadFingerprint: BuildOpenAIResponsesTurnPayloadFingerprint([]byte(`{"input":"hello fallback"}`)),
		Stream:             true,
		TurnOrdinal:        1,
	}

	ticket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 21, "turn-key-fallback-1", desc)
	require.NoError(t, err)
	require.NotNil(t, ticket)
	require.Nil(t, duplicate)
	require.Equal(t, int64(1), OpenAIWSContinuationStats().TurnStoreFallbackTotal)

	require.NoError(t, svc.MarkOpenAIResponsesTurnCompleted(ctx, ticket, OpenAIResponsesTurnStoredState{
		Phase:           string(OpenAIResponsesTurnPhaseCompleted),
		ResponseID:      "resp_fallback_1",
		AccountID:       201,
		Cohort:          string(OpenAIContinuationCohortStrong),
		Transport:       string(OpenAIUpstreamTransportResponsesWebsocketV2),
		ClientRequestID: "client-fallback-1",
	}))

	retryTicket, duplicate, err := svc.BeginOpenAIResponsesTurn(ctx, 21, "turn-key-fallback-1", desc)
	require.NoError(t, err)
	require.Nil(t, retryTicket)
	require.NotNil(t, duplicate)
	require.Equal(t, string(OpenAIResponsesTurnPhaseCompleted), duplicate.Phase)
	require.Equal(t, "resp_fallback_1", duplicate.State.ResponseID)
}
