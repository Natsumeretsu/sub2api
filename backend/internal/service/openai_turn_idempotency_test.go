package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

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
