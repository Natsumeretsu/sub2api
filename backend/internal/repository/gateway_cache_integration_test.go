//go:build integration

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type gatewayCacheOpenAIWSSessionLastResponseCapability interface {
	GetOpenAIWSSessionLastResponse(ctx context.Context, groupID int64, sessionHash string) (string, error)
	SetOpenAIWSSessionLastResponse(ctx context.Context, groupID int64, sessionHash, responseID string, ttl time.Duration) error
	DeleteOpenAIWSSessionLastResponse(ctx context.Context, groupID int64, sessionHash string) error
}

type gatewayCacheOpenAIWSSessionLastResponseTTLCapability interface {
	GetOpenAIWSSessionLastResponseWithTTL(ctx context.Context, groupID int64, sessionHash string) (string, time.Duration, error)
}

type gatewayCacheOpenAIWSSessionTurnStateCapability interface {
	GetOpenAIWSSessionTurnState(ctx context.Context, groupID int64, sessionHash string) (string, error)
	SetOpenAIWSSessionTurnState(ctx context.Context, groupID int64, sessionHash, turnState string, ttl time.Duration) error
	DeleteOpenAIWSSessionTurnState(ctx context.Context, groupID int64, sessionHash string) error
}

type gatewayCacheOpenAIWSSessionTurnStateTTLCapability interface {
	GetOpenAIWSSessionTurnStateWithTTL(ctx context.Context, groupID int64, sessionHash string) (string, time.Duration, error)
}

type GatewayCacheSuite struct {
	IntegrationRedisSuite
	cache service.GatewayCache
}

func (s *GatewayCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.cache = NewGatewayCache(s.rdb)
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_Missing() {
	_, err := s.cache.GetSessionAccountID(s.ctx, 1, "nonexistent")
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing session")
}

func (s *GatewayCacheSuite) TestSetAndGetSessionAccountID() {
	sessionID := "s1"
	accountID := int64(99)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sid, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err, "GetSessionAccountID")
	require.Equal(s.T(), accountID, sid, "session id mismatch")
}

func (s *GatewayCacheSuite) TestSessionAccountID_TTL() {
	sessionID := "s2"
	accountID := int64(100)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL sessionKey after Set")
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL() {
	sessionID := "s3"
	accountID := int64(101)
	groupID := int64(1)
	initialTTL := 1 * time.Minute
	refreshTTL := 3 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, initialTTL), "SetSessionAccountID")

	require.NoError(s.T(), s.cache.RefreshSessionTTL(s.ctx, groupID, sessionID, refreshTTL), "RefreshSessionTTL")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL after Refresh")
	s.AssertTTLWithin(ttl, 1*time.Second, refreshTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL_MissingKey() {
	// RefreshSessionTTL on a missing key should not error (no-op)
	err := s.cache.RefreshSessionTTL(s.ctx, 1, "missing-session", 1*time.Minute)
	require.NoError(s.T(), err, "RefreshSessionTTL on missing key should not error")
}

func (s *GatewayCacheSuite) TestDeleteSessionAccountID() {
	sessionID := "openai:s4"
	accountID := int64(102)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")
	require.NoError(s.T(), s.cache.DeleteSessionAccountID(s.ctx, groupID, sessionID), "DeleteSessionAccountID")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_CorruptedValue() {
	sessionID := "corrupted"
	groupID := int64(1)
	sessionKey := buildSessionKey(groupID, sessionID)

	// Set a non-integer value
	require.NoError(s.T(), s.rdb.Set(s.ctx, sessionKey, "not-a-number", 1*time.Minute).Err(), "Set invalid value")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.Error(s.T(), err, "expected error for corrupted value")
	require.False(s.T(), errors.Is(err, redis.Nil), "expected parsing error, not redis.Nil")
}

func (s *GatewayCacheSuite) TestOpenAIWSSessionLastResponseLifecycle() {
	cache, ok := s.cache.(gatewayCacheOpenAIWSSessionLastResponseCapability)
	require.True(s.T(), ok, "gateway cache should expose optional openai ws session last response capability")

	sessionID := "ws_session_1"
	groupID := int64(1)
	responseID := "resp_123"
	sessionTTL := 1 * time.Minute

	_, err := cache.GetOpenAIWSSessionLastResponse(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing ws session last response")

	require.NoError(s.T(), cache.SetOpenAIWSSessionLastResponse(s.ctx, groupID, sessionID, responseID, sessionTTL))

	gotResponseID, err := cache.GetOpenAIWSSessionLastResponse(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), responseID, gotResponseID)

	sessionKey := buildOpenAIWSSessionLastResponseKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL ws session last response after Set")
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)

	require.NoError(s.T(), cache.DeleteOpenAIWSSessionLastResponse(s.ctx, groupID, sessionID))

	_, err = cache.GetOpenAIWSSessionLastResponse(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *GatewayCacheSuite) TestOpenAIWSSessionLastResponseGetWithTTL() {
	cache, ok := s.cache.(gatewayCacheOpenAIWSSessionLastResponseCapability)
	require.True(s.T(), ok, "gateway cache should expose optional openai ws session last response capability")
	ttlCache, ok := s.cache.(gatewayCacheOpenAIWSSessionLastResponseTTLCapability)
	require.True(s.T(), ok, "gateway cache should expose optional openai ws session last response ttl capability")

	sessionID := "ws_session_ttl_resp_1"
	groupID := int64(1)
	responseID := "resp_ttl_123"
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), cache.SetOpenAIWSSessionLastResponse(s.ctx, groupID, sessionID, responseID, sessionTTL))

	gotResponseID, ttl, err := ttlCache.GetOpenAIWSSessionLastResponseWithTTL(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), responseID, gotResponseID)
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)
}

func (s *GatewayCacheSuite) TestOpenAIWSSessionTurnStateLifecycle() {
	cache, ok := s.cache.(gatewayCacheOpenAIWSSessionTurnStateCapability)
	require.True(s.T(), ok, "gateway cache should expose optional openai ws session turn state capability")

	sessionID := "ws_session_turn_1"
	groupID := int64(1)
	turnState := "turn_state_123"
	sessionTTL := 1 * time.Minute

	_, err := cache.GetOpenAIWSSessionTurnState(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing ws session turn state")

	require.NoError(s.T(), cache.SetOpenAIWSSessionTurnState(s.ctx, groupID, sessionID, turnState, sessionTTL))

	gotTurnState, err := cache.GetOpenAIWSSessionTurnState(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), turnState, gotTurnState)

	sessionKey := buildOpenAIWSSessionTurnStateKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL ws session turn state after Set")
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)

	require.NoError(s.T(), cache.DeleteOpenAIWSSessionTurnState(s.ctx, groupID, sessionID))

	_, err = cache.GetOpenAIWSSessionTurnState(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *GatewayCacheSuite) TestOpenAIWSSessionTurnStateGetWithTTL() {
	cache, ok := s.cache.(gatewayCacheOpenAIWSSessionTurnStateCapability)
	require.True(s.T(), ok, "gateway cache should expose optional openai ws session turn state capability")
	ttlCache, ok := s.cache.(gatewayCacheOpenAIWSSessionTurnStateTTLCapability)
	require.True(s.T(), ok, "gateway cache should expose optional openai ws session turn state ttl capability")

	sessionID := "ws_session_ttl_turn_1"
	groupID := int64(1)
	turnState := "turn_state_ttl_123"
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), cache.SetOpenAIWSSessionTurnState(s.ctx, groupID, sessionID, turnState, sessionTTL))

	gotTurnState, ttl, err := ttlCache.GetOpenAIWSSessionTurnStateWithTTL(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), turnState, gotTurnState)
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)
}

func TestGatewayCacheSuite(t *testing.T) {
	suite.Run(t, new(GatewayCacheSuite))
}
