package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	openAIResponsesTurnIdempotencyScopePrefix = "gateway.openai.responses.turn:"
	openAIResponsesTurnRoute                  = "/openai/v1/responses"
	openAIResponsesTurnTTL                    = 30 * time.Minute
	openAIResponsesTurnProcessingTimeout      = 5 * time.Minute
)

type OpenAIResponsesTurnPhase string

const (
	OpenAIResponsesTurnPhaseProcessing OpenAIResponsesTurnPhase = "processing"
	OpenAIResponsesTurnPhaseEmitted    OpenAIResponsesTurnPhase = "emitted"
	OpenAIResponsesTurnPhaseCompleted  OpenAIResponsesTurnPhase = "completed"
)

type OpenAIResponsesTurnDescriptor struct {
	SessionHash        string `json:"session_hash,omitempty"`
	PromptCacheKey     string `json:"prompt_cache_key,omitempty"`
	Model              string `json:"model,omitempty"`
	ClientRequestID    string `json:"client_request_id,omitempty"`
	RequestedTransport string `json:"requested_transport,omitempty"`
	RequestedCohort    string `json:"requested_cohort,omitempty"`
	PayloadFingerprint string `json:"payload_fingerprint,omitempty"`
	Stream             bool   `json:"stream,omitempty"`
	TurnOrdinal        int    `json:"turn_ordinal,omitempty"`
}

type OpenAIResponsesTurnStoredState struct {
	Phase           string `json:"phase,omitempty"`
	ResponseID      string `json:"response_id,omitempty"`
	AccountID       int64  `json:"account_id,omitempty"`
	Cohort          string `json:"cohort,omitempty"`
	Transport       string `json:"transport,omitempty"`
	EmittedBytes    bool   `json:"emitted_bytes,omitempty"`
	ClientRequestID string `json:"client_request_id,omitempty"`
}

type OpenAIResponsesTurnDuplicate struct {
	Phase            string
	RetryAfterSecond int
	State            OpenAIResponsesTurnStoredState
}

type OpenAIResponsesTurnTicket struct {
	recordID int64
	groupID  int64
	turnKey  string
	ttl      time.Duration
}

type openAIResponsesTurnCoordinator struct {
	repo              IdempotencyRepository
	ttl               time.Duration
	processingTimeout time.Duration
}

func (s *OpenAIGatewayService) BeginOpenAIResponsesTurn(
	ctx context.Context,
	groupID int64,
	turnKey string,
	desc OpenAIResponsesTurnDescriptor,
) (*OpenAIResponsesTurnTicket, *OpenAIResponsesTurnDuplicate, error) {
	coordinator := newOpenAIResponsesTurnCoordinator()
	if coordinator == nil {
		return &OpenAIResponsesTurnTicket{}, nil, nil
	}
	return coordinator.begin(ctx, groupID, turnKey, desc)
}

func (s *OpenAIGatewayService) MarkOpenAIResponsesTurnRetryable(
	ctx context.Context,
	ticket *OpenAIResponsesTurnTicket,
	reason string,
) error {
	coordinator := newOpenAIResponsesTurnCoordinator()
	if coordinator == nil {
		return nil
	}
	return coordinator.markRetryable(ctx, ticket, reason)
}

func (s *OpenAIGatewayService) MarkOpenAIResponsesTurnEmitted(
	ctx context.Context,
	ticket *OpenAIResponsesTurnTicket,
	state OpenAIResponsesTurnStoredState,
) error {
	coordinator := newOpenAIResponsesTurnCoordinator()
	if coordinator == nil {
		return nil
	}
	if strings.TrimSpace(state.Phase) == "" {
		state.Phase = string(OpenAIResponsesTurnPhaseEmitted)
	}
	state.EmittedBytes = true
	return coordinator.markTerminal(ctx, ticket, state, httpStatusAccepted)
}

func (s *OpenAIGatewayService) MarkOpenAIResponsesTurnCompleted(
	ctx context.Context,
	ticket *OpenAIResponsesTurnTicket,
	state OpenAIResponsesTurnStoredState,
) error {
	coordinator := newOpenAIResponsesTurnCoordinator()
	if coordinator == nil {
		return nil
	}
	if strings.TrimSpace(state.Phase) == "" {
		state.Phase = string(OpenAIResponsesTurnPhaseCompleted)
	}
	return coordinator.markTerminal(ctx, ticket, state, httpStatusOK)
}

func BuildOpenAIResponsesTurnPayloadFingerprint(payload []byte) string {
	normalized := payload
	if trimmed := strings.TrimSpace(string(payload)); trimmed == "" {
		normalized = []byte("{}")
	} else {
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err == nil {
			delete(decoded, "previous_response_id")
			if marshaled, marshalErr := json.Marshal(decoded); marshalErr == nil {
				normalized = marshaled
			}
		}
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

const (
	httpStatusOK       = 200
	httpStatusAccepted = 202
)

func newOpenAIResponsesTurnCoordinator() *openAIResponsesTurnCoordinator {
	defaultCoordinator := DefaultIdempotencyCoordinator()
	if defaultCoordinator == nil || defaultCoordinator.repo == nil {
		return nil
	}
	return &openAIResponsesTurnCoordinator{
		repo:              defaultCoordinator.repo,
		ttl:               openAIResponsesTurnTTL,
		processingTimeout: maxDuration(defaultCoordinator.cfg.ProcessingTimeout, openAIResponsesTurnProcessingTimeout),
	}
}

func (c *openAIResponsesTurnCoordinator) begin(
	ctx context.Context,
	groupID int64,
	turnKey string,
	desc OpenAIResponsesTurnDescriptor,
) (*OpenAIResponsesTurnTicket, *OpenAIResponsesTurnDuplicate, error) {
	if c == nil || c.repo == nil {
		return &OpenAIResponsesTurnTicket{}, nil, nil
	}
	key, err := NormalizeIdempotencyKey(turnKey)
	if err != nil {
		return nil, nil, err
	}
	scope := fmt.Sprintf("%s%d", openAIResponsesTurnIdempotencyScopePrefix, groupID)
	now := time.Now()
	expiresAt := now.Add(c.ttl)
	lockedUntil := now.Add(c.processingTimeout)
	fingerprint, err := BuildIdempotencyFingerprint(http.MethodPost, openAIResponsesTurnRoute, buildOpenAIResponsesTurnActorScope(groupID, desc), desc)
	if err != nil {
		return nil, nil, err
	}
	keyHash := HashIdempotencyKey(key)
	record := &IdempotencyRecord{
		Scope:              scope,
		IdempotencyKeyHash: keyHash,
		RequestFingerprint: fingerprint,
		Status:             IdempotencyStatusProcessing,
		LockedUntil:        &lockedUntil,
		ExpiresAt:          expiresAt,
	}
	for attempt := 0; attempt < 2; attempt++ {
		owner, createErr := c.repo.CreateProcessing(ctx, record)
		if createErr != nil {
			return nil, nil, ErrIdempotencyStoreUnavail.WithCause(createErr)
		}
		if owner {
			return &OpenAIResponsesTurnTicket{
				recordID: record.ID,
				groupID:  groupID,
				turnKey:  key,
				ttl:      c.ttl,
			}, nil, nil
		}

		existing, getErr := c.repo.GetByScopeAndKeyHash(ctx, scope, keyHash)
		if getErr != nil {
			return nil, nil, ErrIdempotencyStoreUnavail.WithCause(getErr)
		}
		if existing == nil {
			continue
		}
		if existing.RequestFingerprint != fingerprint {
			return nil, nil, ErrIdempotencyKeyConflict
		}
		if !existing.ExpiresAt.After(now) {
			taken, reclaimErr := c.repo.TryReclaim(ctx, existing.ID, existing.Status, now, lockedUntil, expiresAt)
			if reclaimErr != nil {
				return nil, nil, ErrIdempotencyStoreUnavail.WithCause(reclaimErr)
			}
			if taken {
				record.ID = existing.ID
				return &OpenAIResponsesTurnTicket{
					recordID: existing.ID,
					groupID:  groupID,
					turnKey:  key,
					ttl:      c.ttl,
				}, nil, nil
			}
			continue
		}

		switch existing.Status {
		case IdempotencyStatusProcessing:
			if existing.LockedUntil == nil || existing.LockedUntil.After(now) {
				return nil, &OpenAIResponsesTurnDuplicate{
					Phase:            string(OpenAIResponsesTurnPhaseProcessing),
					RetryAfterSecond: retryAfterSeconds(existing.LockedUntil, now),
				}, nil
			}
			taken, reclaimErr := c.repo.TryReclaim(ctx, existing.ID, existing.Status, now, lockedUntil, expiresAt)
			if reclaimErr != nil {
				return nil, nil, ErrIdempotencyStoreUnavail.WithCause(reclaimErr)
			}
			if taken {
				record.ID = existing.ID
				return &OpenAIResponsesTurnTicket{
					recordID: existing.ID,
					groupID:  groupID,
					turnKey:  key,
					ttl:      c.ttl,
				}, nil, nil
			}
		case IdempotencyStatusFailedRetryable:
			if existing.LockedUntil != nil && existing.LockedUntil.After(now) {
				return nil, &OpenAIResponsesTurnDuplicate{
					Phase:            string(OpenAIResponsesTurnPhaseProcessing),
					RetryAfterSecond: retryAfterSeconds(existing.LockedUntil, now),
				}, nil
			}
			taken, reclaimErr := c.repo.TryReclaim(ctx, existing.ID, IdempotencyStatusFailedRetryable, now, lockedUntil, expiresAt)
			if reclaimErr != nil {
				return nil, nil, ErrIdempotencyStoreUnavail.WithCause(reclaimErr)
			}
			if taken {
				record.ID = existing.ID
				return &OpenAIResponsesTurnTicket{
					recordID: existing.ID,
					groupID:  groupID,
					turnKey:  key,
					ttl:      c.ttl,
				}, nil, nil
			}
		case IdempotencyStatusSucceeded:
			state, decodeErr := decodeOpenAIResponsesTurnStoredState(existing.ResponseBody)
			if decodeErr != nil {
				state = OpenAIResponsesTurnStoredState{Phase: string(OpenAIResponsesTurnPhaseCompleted)}
			}
			return nil, &OpenAIResponsesTurnDuplicate{
				Phase: state.Phase,
				State: state,
			}, nil
		default:
			return nil, nil, ErrIdempotencyKeyConflict
		}
	}
	return nil, nil, ErrIdempotencyInProgress
}

func (c *openAIResponsesTurnCoordinator) markRetryable(ctx context.Context, ticket *OpenAIResponsesTurnTicket, reason string) error {
	if c == nil || c.repo == nil || ticket == nil || ticket.recordID <= 0 {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "OPENAI_TURN_RETRYABLE"
	}
	expiresAt := time.Now().Add(ticket.ttl)
	return c.repo.MarkFailedRetryable(ctx, ticket.recordID, reason, time.Now(), expiresAt)
}

func (c *openAIResponsesTurnCoordinator) markTerminal(
	ctx context.Context,
	ticket *OpenAIResponsesTurnTicket,
	state OpenAIResponsesTurnStoredState,
	responseStatus int,
) error {
	if c == nil || c.repo == nil || ticket == nil || ticket.recordID <= 0 {
		return nil
	}
	if strings.TrimSpace(state.Phase) == "" {
		state.Phase = string(OpenAIResponsesTurnPhaseCompleted)
	}
	stored, err := json.Marshal(state)
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(ticket.ttl)
	return c.repo.MarkSucceeded(ctx, ticket.recordID, responseStatus, string(stored), expiresAt)
}

func decodeOpenAIResponsesTurnStoredState(stored *string) (OpenAIResponsesTurnStoredState, error) {
	if stored == nil || strings.TrimSpace(*stored) == "" {
		return OpenAIResponsesTurnStoredState{}, nil
	}
	var out OpenAIResponsesTurnStoredState
	if err := json.Unmarshal([]byte(*stored), &out); err != nil {
		return OpenAIResponsesTurnStoredState{}, err
	}
	out.Phase = strings.TrimSpace(out.Phase)
	return out, nil
}

func buildOpenAIResponsesTurnActorScope(groupID int64, desc OpenAIResponsesTurnDescriptor) string {
	parts := []string{fmt.Sprintf("group:%d", groupID)}
	if sessionHash := strings.TrimSpace(desc.SessionHash); sessionHash != "" {
		parts = append(parts, "session:"+sessionHash)
	}
	return strings.Join(parts, "|")
}

func retryAfterSeconds(lockedUntil *time.Time, now time.Time) int {
	if lockedUntil == nil {
		return 1
	}
	seconds := int(lockedUntil.Sub(now).Seconds())
	if seconds <= 0 {
		return 1
	}
	return seconds
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}
