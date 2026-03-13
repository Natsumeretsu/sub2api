package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSStateStore_BindGetDeleteResponseAccount(t *testing.T) {
	cache := &stubGatewayCache{}
	store := NewOpenAIWSStateStore(cache)
	ctx := context.Background()
	groupID := int64(7)

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_abc", 101, time.Minute))

	accountID, err := store.GetResponseAccount(ctx, groupID, "resp_abc")
	require.NoError(t, err)
	require.Equal(t, int64(101), accountID)

	require.NoError(t, store.DeleteResponseAccount(ctx, groupID, "resp_abc"))
	accountID, err = store.GetResponseAccount(ctx, groupID, "resp_abc")
	require.NoError(t, err)
	require.Zero(t, accountID)
}

func TestOpenAIWSStateStore_ResponseConnTTL(t *testing.T) {
	store := NewOpenAIWSStateStore(nil)
	store.BindResponseConn("resp_conn", "conn_1", 30*time.Millisecond)

	connID, ok := store.GetResponseConn("resp_conn")
	require.True(t, ok)
	require.Equal(t, "conn_1", connID)

	time.Sleep(60 * time.Millisecond)
	_, ok = store.GetResponseConn("resp_conn")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_SessionTurnStateTTL(t *testing.T) {
	store := NewOpenAIWSStateStore(nil)
	store.BindSessionTurnState(9, "session_hash_1", "turn_state_1", 30*time.Millisecond)

	state, ok := store.GetSessionTurnState(9, "session_hash_1")
	require.True(t, ok)
	require.Equal(t, "turn_state_1", state)

	// group 隔离
	_, ok = store.GetSessionTurnState(10, "session_hash_1")
	require.False(t, ok)

	time.Sleep(60 * time.Millisecond)
	_, ok = store.GetSessionTurnState(9, "session_hash_1")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_SessionTurnStateFallsBackToCache(t *testing.T) {
	cache := &stubGatewayCache{}
	store1 := NewOpenAIWSStateStore(cache)
	store2 := NewOpenAIWSStateStore(cache)

	store1.BindSessionTurnState(9, "session_hash_turn_cache", "turn_state_cache", time.Minute)

	state, ok := store2.GetSessionTurnState(9, "session_hash_turn_cache")
	require.True(t, ok)
	require.Equal(t, "turn_state_cache", state)

	store1.DeleteSessionTurnState(9, "session_hash_turn_cache")
	store3 := NewOpenAIWSStateStore(cache)
	_, ok = store3.GetSessionTurnState(9, "session_hash_turn_cache")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_SessionTurnStateFallbackDoesNotOutliveSharedTTL(t *testing.T) {
	cache := newTTLAwareOpenAIWSCacheStub()
	store1 := NewOpenAIWSStateStore(cache)
	store2 := NewOpenAIWSStateStore(cache)

	store1.BindSessionTurnState(9, "session_hash_turn_ttl", "turn_state_ttl", 40*time.Millisecond)

	state, ok := store2.GetSessionTurnState(9, "session_hash_turn_ttl")
	require.True(t, ok)
	require.Equal(t, "turn_state_ttl", state)

	time.Sleep(80 * time.Millisecond)
	_, ok = store2.GetSessionTurnState(9, "session_hash_turn_ttl")
	require.False(t, ok, "从共享缓存回填的本地 turn_state 不应活过共享 TTL")
}

func TestOpenAIWSStateStore_SessionLastResponseTTL(t *testing.T) {
	store := NewOpenAIWSStateStore(nil)
	store.BindSessionLastResponse(9, "session_hash_resp_1", "resp_last_1", 30*time.Millisecond)

	responseID, ok := store.GetSessionLastResponse(9, "session_hash_resp_1")
	require.True(t, ok)
	require.Equal(t, "resp_last_1", responseID)

	_, ok = store.GetSessionLastResponse(10, "session_hash_resp_1")
	require.False(t, ok)

	time.Sleep(60 * time.Millisecond)
	_, ok = store.GetSessionLastResponse(9, "session_hash_resp_1")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_SessionLastResponseFallsBackToCache(t *testing.T) {
	cache := &stubGatewayCache{}
	store1 := NewOpenAIWSStateStore(cache)
	store2 := NewOpenAIWSStateStore(cache)

	store1.BindSessionLastResponse(9, "session_hash_resp_cache", "resp_last_cache", time.Minute)

	responseID, ok := store2.GetSessionLastResponse(9, "session_hash_resp_cache")
	require.True(t, ok)
	require.Equal(t, "resp_last_cache", responseID)

	store1.DeleteSessionLastResponse(9, "session_hash_resp_cache")
	store3 := NewOpenAIWSStateStore(cache)
	_, ok = store3.GetSessionLastResponse(9, "session_hash_resp_cache")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_SessionLastResponseFallbackDoesNotOutliveSharedTTL(t *testing.T) {
	cache := newTTLAwareOpenAIWSCacheStub()
	store1 := NewOpenAIWSStateStore(cache)
	store2 := NewOpenAIWSStateStore(cache)

	store1.BindSessionLastResponse(9, "session_hash_resp_ttl", "resp_last_ttl", 40*time.Millisecond)

	responseID, ok := store2.GetSessionLastResponse(9, "session_hash_resp_ttl")
	require.True(t, ok)
	require.Equal(t, "resp_last_ttl", responseID)

	time.Sleep(80 * time.Millisecond)
	_, ok = store2.GetSessionLastResponse(9, "session_hash_resp_ttl")
	require.False(t, ok, "从共享缓存回填的本地 last_response 不应活过共享 TTL")
}

func TestOpenAIWSStateStore_SessionConnTTL(t *testing.T) {
	store := NewOpenAIWSStateStore(nil)
	store.BindSessionConn(9, "session_hash_conn_1", "conn_1", 30*time.Millisecond)

	connID, ok := store.GetSessionConn(9, "session_hash_conn_1")
	require.True(t, ok)
	require.Equal(t, "conn_1", connID)

	// group 隔离
	_, ok = store.GetSessionConn(10, "session_hash_conn_1")
	require.False(t, ok)

	time.Sleep(60 * time.Millisecond)
	_, ok = store.GetSessionConn(9, "session_hash_conn_1")
	require.False(t, ok)
}

func TestOpenAIWSStateStore_DebugSnapshot(t *testing.T) {
	raw := NewOpenAIWSStateStore(nil)
	store, ok := raw.(*defaultOpenAIWSStateStore)
	require.True(t, ok)

	ctx := context.Background()
	require.NoError(t, store.BindResponseAccount(ctx, 9, "resp_debug_1", 101, time.Minute))
	store.BindResponseConn("resp_debug_1", "conn_debug_1", time.Minute)
	store.BindSessionTurnState(9, "session_hash_debug", "turn_debug", time.Minute)
	store.BindSessionLastResponse(9, "session_hash_debug", "resp_debug_1", time.Minute)
	store.BindSessionConn(9, "session_hash_debug", "conn_debug_1", time.Minute)
	store.DeleteSessionConn(9, "session_hash_debug")

	snapshot := store.DebugSnapshot()
	require.Equal(t, 1, snapshot.ResponseAccountLocalEntries)
	require.Equal(t, 1, snapshot.ResponseConnEntries)
	require.Equal(t, 1, snapshot.SessionTurnStateEntries)
	require.Equal(t, 1, snapshot.SessionLastResponseEntries)
	require.Equal(t, 0, snapshot.SessionConnEntries)
	require.Equal(t, int64(1), snapshot.ResponseAccountBindTotal)
	require.Equal(t, int64(1), snapshot.ResponseConnBindTotal)
	require.Equal(t, int64(1), snapshot.SessionTurnStateBindTotal)
	require.Equal(t, int64(1), snapshot.SessionLastResponseBindTotal)
	require.Equal(t, int64(1), snapshot.SessionConnBindTotal)
	require.Equal(t, int64(1), snapshot.SessionConnDeleteTotal)
	require.False(t, snapshot.SessionTurnStatePersistent)
	require.False(t, snapshot.SessionLastResponsePersistent)
	require.False(t, snapshot.ResponseAccountPersistent)
	require.Positive(t, snapshot.LocalCleanupIntervalSeconds)
	require.Positive(t, snapshot.LocalCleanupMaxPerMap)
	require.Positive(t, snapshot.LocalMaxEntriesPerMap)
	require.Positive(t, snapshot.RedisTimeoutMillis)
}

func TestOpenAIWSStateStore_GetResponseAccount_NoStaleAfterCacheMiss(t *testing.T) {
	cache := &stubGatewayCache{sessionBindings: map[string]int64{}}
	store := NewOpenAIWSStateStore(cache)
	ctx := context.Background()
	groupID := int64(17)
	responseID := "resp_cache_stale"
	cacheKey := openAIWSResponseAccountCacheKey(responseID)

	cache.sessionBindings[cacheKey] = 501
	accountID, err := store.GetResponseAccount(ctx, groupID, responseID)
	require.NoError(t, err)
	require.Equal(t, int64(501), accountID)

	delete(cache.sessionBindings, cacheKey)
	accountID, err = store.GetResponseAccount(ctx, groupID, responseID)
	require.NoError(t, err)
	require.Zero(t, accountID, "上游缓存失效后不应继续命中本地陈旧映射")
}

func TestOpenAIWSStateStore_MaybeCleanupRemovesExpiredIncrementally(t *testing.T) {
	raw := NewOpenAIWSStateStore(nil)
	store, ok := raw.(*defaultOpenAIWSStateStore)
	require.True(t, ok)

	expiredAt := time.Now().Add(-time.Minute)
	total := 2048
	store.responseToConnMu.Lock()
	for i := 0; i < total; i++ {
		store.responseToConn[fmt.Sprintf("resp_%d", i)] = openAIWSConnBinding{
			connID:    "conn_incremental",
			expiresAt: expiredAt,
		}
	}
	store.responseToConnMu.Unlock()

	store.lastCleanupUnixNano.Store(time.Now().Add(-2 * openAIWSStateStoreCleanupInterval).UnixNano())
	store.maybeCleanup()

	store.responseToConnMu.RLock()
	remainingAfterFirst := len(store.responseToConn)
	store.responseToConnMu.RUnlock()
	require.Less(t, remainingAfterFirst, total, "单轮 cleanup 应至少有进展")
	require.Greater(t, remainingAfterFirst, 0, "增量清理不要求单轮清空全部键")

	for i := 0; i < 8; i++ {
		store.lastCleanupUnixNano.Store(time.Now().Add(-2 * openAIWSStateStoreCleanupInterval).UnixNano())
		store.maybeCleanup()
	}

	store.responseToConnMu.RLock()
	remaining := len(store.responseToConn)
	store.responseToConnMu.RUnlock()
	require.Zero(t, remaining, "多轮 cleanup 后应逐步清空全部过期键")
}

func TestEnsureBindingCapacity_EvictsOneWhenMapIsFull(t *testing.T) {
	bindings := map[string]int{
		"a": 1,
		"b": 2,
	}

	ensureBindingCapacity(bindings, "c", 2)
	bindings["c"] = 3

	require.Len(t, bindings, 2)
	require.Equal(t, 3, bindings["c"])
}

func TestEnsureBindingCapacity_DoesNotEvictWhenUpdatingExistingKey(t *testing.T) {
	bindings := map[string]int{
		"a": 1,
		"b": 2,
	}

	ensureBindingCapacity(bindings, "a", 2)
	bindings["a"] = 9

	require.Len(t, bindings, 2)
	require.Equal(t, 9, bindings["a"])
}

type openAIWSStateStoreTimeoutProbeCache struct {
	setHasDeadline             bool
	getHasDeadline             bool
	deleteHasDeadline          bool
	setTurnStateHasDeadline    bool
	getTurnStateHasDeadline    bool
	delTurnStateHasDeadline    bool
	setLastResponseHasDeadline bool
	getLastResponseHasDeadline bool
	delLastResponseHasDeadline bool
	setDeadlineDelta           time.Duration
	getDeadlineDelta           time.Duration
	delDeadlineDelta           time.Duration
	setTurnStateDeadline       time.Duration
	getTurnStateDeadline       time.Duration
	delTurnStateDeadline       time.Duration
	setLastResponseDeadline    time.Duration
	getLastResponseDeadline    time.Duration
	delLastResponseDeadline    time.Duration
	turnStateValue             string
	lastResponseValue          string
}

type ttlAwareOpenAIWSCacheStub struct {
	turnStateBindings    map[string]ttlAwareOpenAIWSStringBinding
	lastResponseBindings map[string]ttlAwareOpenAIWSStringBinding
}

type ttlAwareOpenAIWSStringBinding struct {
	value     string
	expiresAt time.Time
}

func newTTLAwareOpenAIWSCacheStub() *ttlAwareOpenAIWSCacheStub {
	return &ttlAwareOpenAIWSCacheStub{
		turnStateBindings:    make(map[string]ttlAwareOpenAIWSStringBinding),
		lastResponseBindings: make(map[string]ttlAwareOpenAIWSStringBinding),
	}
}

func (c *ttlAwareOpenAIWSCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, errors.New("not found")
}

func (c *ttlAwareOpenAIWSCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (c *ttlAwareOpenAIWSCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *ttlAwareOpenAIWSCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (c *ttlAwareOpenAIWSCacheStub) GetOpenAIWSSessionTurnState(ctx context.Context, groupID int64, sessionHash string) (string, error) {
	state, _, err := c.GetOpenAIWSSessionTurnStateWithTTL(ctx, groupID, sessionHash)
	return state, err
}

func (c *ttlAwareOpenAIWSCacheStub) GetOpenAIWSSessionTurnStateWithTTL(_ context.Context, groupID int64, sessionHash string) (string, time.Duration, error) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	binding, ok := c.turnStateBindings[key]
	if !ok {
		return "", 0, errors.New("not found")
	}
	ttl := time.Until(binding.expiresAt)
	if ttl <= 0 || strings.TrimSpace(binding.value) == "" {
		delete(c.turnStateBindings, key)
		return "", 0, errors.New("not found")
	}
	return binding.value, ttl, nil
}

func (c *ttlAwareOpenAIWSCacheStub) SetOpenAIWSSessionTurnState(_ context.Context, groupID int64, sessionHash, turnState string, ttl time.Duration) error {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	c.turnStateBindings[key] = ttlAwareOpenAIWSStringBinding{
		value:     strings.TrimSpace(turnState),
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (c *ttlAwareOpenAIWSCacheStub) DeleteOpenAIWSSessionTurnState(_ context.Context, groupID int64, sessionHash string) error {
	delete(c.turnStateBindings, openAIWSSessionScopedKey(groupID, sessionHash))
	return nil
}

func (c *ttlAwareOpenAIWSCacheStub) GetOpenAIWSSessionLastResponse(ctx context.Context, groupID int64, sessionHash string) (string, error) {
	responseID, _, err := c.GetOpenAIWSSessionLastResponseWithTTL(ctx, groupID, sessionHash)
	return responseID, err
}

func (c *ttlAwareOpenAIWSCacheStub) GetOpenAIWSSessionLastResponseWithTTL(_ context.Context, groupID int64, sessionHash string) (string, time.Duration, error) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	binding, ok := c.lastResponseBindings[key]
	if !ok {
		return "", 0, errors.New("not found")
	}
	ttl := time.Until(binding.expiresAt)
	if ttl <= 0 || strings.TrimSpace(binding.value) == "" {
		delete(c.lastResponseBindings, key)
		return "", 0, errors.New("not found")
	}
	return binding.value, ttl, nil
}

func (c *ttlAwareOpenAIWSCacheStub) SetOpenAIWSSessionLastResponse(_ context.Context, groupID int64, sessionHash, responseID string, ttl time.Duration) error {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	c.lastResponseBindings[key] = ttlAwareOpenAIWSStringBinding{
		value:     strings.TrimSpace(responseID),
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (c *ttlAwareOpenAIWSCacheStub) DeleteOpenAIWSSessionLastResponse(_ context.Context, groupID int64, sessionHash string) error {
	delete(c.lastResponseBindings, openAIWSSessionScopedKey(groupID, sessionHash))
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) GetSessionAccountID(ctx context.Context, _ int64, _ string) (int64, error) {
	if deadline, ok := ctx.Deadline(); ok {
		c.getHasDeadline = true
		c.getDeadlineDelta = time.Until(deadline)
	}
	return 123, nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) SetSessionAccountID(ctx context.Context, _ int64, _ string, _ int64, _ time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.setHasDeadline = true
		c.setDeadlineDelta = time.Until(deadline)
	}
	return errors.New("set failed")
}

func (c *openAIWSStateStoreTimeoutProbeCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) DeleteSessionAccountID(ctx context.Context, _ int64, _ string) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.deleteHasDeadline = true
		c.delDeadlineDelta = time.Until(deadline)
	}
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) GetOpenAIWSSessionTurnState(ctx context.Context, _ int64, _ string) (string, error) {
	if deadline, ok := ctx.Deadline(); ok {
		c.getTurnStateHasDeadline = true
		c.getTurnStateDeadline = time.Until(deadline)
	}
	if c.turnStateValue == "" {
		return "", errors.New("not found")
	}
	return c.turnStateValue, nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) SetOpenAIWSSessionTurnState(ctx context.Context, _ int64, _ string, turnState string, _ time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.setTurnStateHasDeadline = true
		c.setTurnStateDeadline = time.Until(deadline)
	}
	c.turnStateValue = turnState
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) DeleteOpenAIWSSessionTurnState(ctx context.Context, _ int64, _ string) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.delTurnStateHasDeadline = true
		c.delTurnStateDeadline = time.Until(deadline)
	}
	c.turnStateValue = ""
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) GetOpenAIWSSessionLastResponse(ctx context.Context, _ int64, _ string) (string, error) {
	if deadline, ok := ctx.Deadline(); ok {
		c.getLastResponseHasDeadline = true
		c.getLastResponseDeadline = time.Until(deadline)
	}
	if c.lastResponseValue == "" {
		return "", errors.New("not found")
	}
	return c.lastResponseValue, nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) SetOpenAIWSSessionLastResponse(ctx context.Context, _ int64, _ string, responseID string, _ time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.setLastResponseHasDeadline = true
		c.setLastResponseDeadline = time.Until(deadline)
	}
	c.lastResponseValue = responseID
	return nil
}

func (c *openAIWSStateStoreTimeoutProbeCache) DeleteOpenAIWSSessionLastResponse(ctx context.Context, _ int64, _ string) error {
	if deadline, ok := ctx.Deadline(); ok {
		c.delLastResponseHasDeadline = true
		c.delLastResponseDeadline = time.Until(deadline)
	}
	c.lastResponseValue = ""
	return nil
}

func TestOpenAIWSStateStore_RedisOpsUseShortTimeout(t *testing.T) {
	probe := &openAIWSStateStoreTimeoutProbeCache{}
	store := NewOpenAIWSStateStore(probe)
	ctx := context.Background()
	groupID := int64(5)

	err := store.BindResponseAccount(ctx, groupID, "resp_timeout_probe", 11, time.Minute)
	require.Error(t, err)

	accountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_timeout_probe")
	require.NoError(t, getErr)
	require.Equal(t, int64(11), accountID, "本地缓存命中应优先返回已绑定账号")

	require.NoError(t, store.DeleteResponseAccount(ctx, groupID, "resp_timeout_probe"))

	require.True(t, probe.setHasDeadline, "SetSessionAccountID 应携带独立超时上下文")
	require.True(t, probe.deleteHasDeadline, "DeleteSessionAccountID 应携带独立超时上下文")
	require.False(t, probe.getHasDeadline, "GetSessionAccountID 本用例应由本地缓存命中，不触发 Redis 读取")
	require.Greater(t, probe.setDeadlineDelta, 2*time.Second)
	require.LessOrEqual(t, probe.setDeadlineDelta, 3*time.Second)
	require.Greater(t, probe.delDeadlineDelta, 2*time.Second)
	require.LessOrEqual(t, probe.delDeadlineDelta, 3*time.Second)

	probe2 := &openAIWSStateStoreTimeoutProbeCache{}
	store2 := NewOpenAIWSStateStore(probe2)
	accountID2, err2 := store2.GetResponseAccount(ctx, groupID, "resp_cache_only")
	require.NoError(t, err2)
	require.Equal(t, int64(123), accountID2)
	require.True(t, probe2.getHasDeadline, "GetSessionAccountID 在缓存未命中时应携带独立超时上下文")
	require.Greater(t, probe2.getDeadlineDelta, 2*time.Second)
	require.LessOrEqual(t, probe2.getDeadlineDelta, 3*time.Second)

	probe3 := &openAIWSStateStoreTimeoutProbeCache{}
	store3 := NewOpenAIWSStateStore(probe3)
	store3.BindSessionTurnState(groupID, "session_hash_turn_timeout", "turn_timeout", time.Minute)
	turnState, ok := store3.GetSessionTurnState(groupID, "session_hash_turn_timeout")
	require.True(t, ok)
	require.Equal(t, "turn_timeout", turnState)
	store3.DeleteSessionTurnState(groupID, "session_hash_turn_timeout")
	_, ok = store3.GetSessionTurnState(groupID, "session_hash_turn_timeout")
	require.False(t, ok)
	require.True(t, probe3.setTurnStateHasDeadline, "SetOpenAIWSSessionTurnState 应携带独立超时上下文")
	require.True(t, probe3.delTurnStateHasDeadline, "DeleteOpenAIWSSessionTurnState 应携带独立超时上下文")
	require.True(t, probe3.getTurnStateHasDeadline, "GetOpenAIWSSessionTurnState 在本地未命中时应携带独立超时上下文")
	require.Greater(t, probe3.setTurnStateDeadline, 2*time.Second)
	require.LessOrEqual(t, probe3.setTurnStateDeadline, 3*time.Second)
	require.Greater(t, probe3.delTurnStateDeadline, 2*time.Second)
	require.LessOrEqual(t, probe3.delTurnStateDeadline, 3*time.Second)
	require.Greater(t, probe3.getTurnStateDeadline, 2*time.Second)
	require.LessOrEqual(t, probe3.getTurnStateDeadline, 3*time.Second)

	probe4 := &openAIWSStateStoreTimeoutProbeCache{}
	store4 := NewOpenAIWSStateStore(probe4)
	store4.BindSessionLastResponse(groupID, "session_hash_resp_timeout", "resp_timeout", time.Minute)
	responseID, ok := store4.GetSessionLastResponse(groupID, "session_hash_resp_timeout")
	require.True(t, ok)
	require.Equal(t, "resp_timeout", responseID)
	store4.DeleteSessionLastResponse(groupID, "session_hash_resp_timeout")
	_, ok = store4.GetSessionLastResponse(groupID, "session_hash_resp_timeout")
	require.False(t, ok)
	require.True(t, probe4.setLastResponseHasDeadline, "SetOpenAIWSSessionLastResponse 应携带独立超时上下文")
	require.True(t, probe4.delLastResponseHasDeadline, "DeleteOpenAIWSSessionLastResponse 应携带独立超时上下文")
	require.True(t, probe4.getLastResponseHasDeadline, "GetOpenAIWSSessionLastResponse 在本地未命中时应携带独立超时上下文")
	require.Greater(t, probe4.setLastResponseDeadline, 2*time.Second)
	require.LessOrEqual(t, probe4.setLastResponseDeadline, 3*time.Second)
	require.Greater(t, probe4.delLastResponseDeadline, 2*time.Second)
	require.LessOrEqual(t, probe4.delLastResponseDeadline, 3*time.Second)
	require.Greater(t, probe4.getLastResponseDeadline, 2*time.Second)
	require.LessOrEqual(t, probe4.getLastResponseDeadline, 3*time.Second)
}

func TestWithOpenAIWSStateStoreRedisTimeout_WithParentContext(t *testing.T) {
	ctx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
	defer cancel()
	require.NotNil(t, ctx)
	_, ok := ctx.Deadline()
	require.True(t, ok, "应附加短超时")
}
