package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	openAIWSResponseAccountCachePrefix = "openai:response:"
	openAIWSStateStoreCleanupInterval  = time.Minute
	openAIWSStateStoreCleanupMaxPerMap = 512
	openAIWSStateStoreMaxEntriesPerMap = 65536
	openAIWSStateStoreRedisTimeout     = 3 * time.Second
)

type openAIWSAccountBinding struct {
	accountID int64
	expiresAt time.Time
}

type openAIWSConnBinding struct {
	connID    string
	expiresAt time.Time
}

type openAIWSTurnStateBinding struct {
	turnState string
	expiresAt time.Time
}

type openAIWSLastResponseBinding struct {
	responseID string
	expiresAt  time.Time
}

type openAIWSSessionConnBinding struct {
	connID    string
	expiresAt time.Time
}

type openAIWSSessionLastResponseCache interface {
	GetOpenAIWSSessionLastResponse(ctx context.Context, groupID int64, sessionHash string) (string, error)
	SetOpenAIWSSessionLastResponse(ctx context.Context, groupID int64, sessionHash, responseID string, ttl time.Duration) error
	DeleteOpenAIWSSessionLastResponse(ctx context.Context, groupID int64, sessionHash string) error
}

type openAIWSSessionTurnStateCache interface {
	GetOpenAIWSSessionTurnState(ctx context.Context, groupID int64, sessionHash string) (string, error)
	SetOpenAIWSSessionTurnState(ctx context.Context, groupID int64, sessionHash, turnState string, ttl time.Duration) error
	DeleteOpenAIWSSessionTurnState(ctx context.Context, groupID int64, sessionHash string) error
}

// OpenAIWSStateStore 管理 WSv2 的粘连状态。
// - response_id -> account_id 用于续链路由
// - response_id -> conn_id 用于连接内上下文复用
//
// response_id -> account_id 优先走 GatewayCache（Redis），同时维护本地热缓存。
// session_hash -> last_response_id 支持可选 GatewayCache 持久化，用于跨实例恢复 previous_response_id。
// response_id -> conn_id / session_hash -> conn_id 仅在本进程内有效。
type OpenAIWSStateStore interface {
	BindResponseAccount(ctx context.Context, groupID int64, responseID string, accountID int64, ttl time.Duration) error
	GetResponseAccount(ctx context.Context, groupID int64, responseID string) (int64, error)
	DeleteResponseAccount(ctx context.Context, groupID int64, responseID string) error

	BindResponseConn(responseID, connID string, ttl time.Duration)
	GetResponseConn(responseID string) (string, bool)
	DeleteResponseConn(responseID string)

	BindSessionTurnState(groupID int64, sessionHash, turnState string, ttl time.Duration)
	GetSessionTurnState(groupID int64, sessionHash string) (string, bool)
	DeleteSessionTurnState(groupID int64, sessionHash string)

	BindSessionLastResponse(groupID int64, sessionHash, responseID string, ttl time.Duration)
	GetSessionLastResponse(groupID int64, sessionHash string) (string, bool)
	DeleteSessionLastResponse(groupID int64, sessionHash string)

	BindSessionConn(groupID int64, sessionHash, connID string, ttl time.Duration)
	GetSessionConn(groupID int64, sessionHash string) (string, bool)
	DeleteSessionConn(groupID int64, sessionHash string)

	DebugSnapshot() OpenAIWSStateStoreDebugSnapshot
}

type OpenAIWSStateStoreDebugSnapshot struct {
	ResponseAccountLocalEntries int `json:"response_account_local_entries"`
	ResponseConnEntries         int `json:"response_conn_entries"`
	SessionTurnStateEntries     int `json:"session_turn_state_entries"`
	SessionLastResponseEntries  int `json:"session_last_response_entries"`
	SessionConnEntries          int `json:"session_conn_entries"`

	ResponseAccountBindTotal       int64 `json:"response_account_bind_total"`
	ResponseAccountDeleteTotal     int64 `json:"response_account_delete_total"`
	ResponseConnBindTotal          int64 `json:"response_conn_bind_total"`
	ResponseConnDeleteTotal        int64 `json:"response_conn_delete_total"`
	SessionTurnStateBindTotal      int64 `json:"session_turn_state_bind_total"`
	SessionTurnStateDeleteTotal    int64 `json:"session_turn_state_delete_total"`
	SessionLastResponseBindTotal   int64 `json:"session_last_response_bind_total"`
	SessionLastResponseDeleteTotal int64 `json:"session_last_response_delete_total"`
	SessionConnBindTotal           int64 `json:"session_conn_bind_total"`
	SessionConnDeleteTotal         int64 `json:"session_conn_delete_total"`

	ResponseAccountPersistent     bool `json:"response_account_persistent"`
	SessionTurnStatePersistent    bool `json:"session_turn_state_persistent"`
	SessionLastResponsePersistent bool `json:"session_last_response_persistent"`

	LocalCleanupIntervalSeconds int `json:"local_cleanup_interval_seconds"`
	LocalCleanupMaxPerMap       int `json:"local_cleanup_max_per_map"`
	LocalMaxEntriesPerMap       int `json:"local_max_entries_per_map"`
	RedisTimeoutMillis          int `json:"redis_timeout_ms"`
}

type defaultOpenAIWSStateStore struct {
	cache GatewayCache

	responseToAccountMu  sync.RWMutex
	responseToAccount    map[string]openAIWSAccountBinding
	responseToConnMu     sync.RWMutex
	responseToConn       map[string]openAIWSConnBinding
	sessionToTurnStateMu sync.RWMutex
	sessionToTurnState   map[string]openAIWSTurnStateBinding
	sessionToLastRespMu  sync.RWMutex
	sessionToLastResp    map[string]openAIWSLastResponseBinding
	sessionToConnMu      sync.RWMutex
	sessionToConn        map[string]openAIWSSessionConnBinding

	responseAccountBindTotal    atomic.Int64
	responseAccountDeleteTotal  atomic.Int64
	responseConnBindTotal       atomic.Int64
	responseConnDeleteTotal     atomic.Int64
	sessionTurnStateBindTotal   atomic.Int64
	sessionTurnStateDeleteTotal atomic.Int64
	sessionLastRespBindTotal    atomic.Int64
	sessionLastRespDeleteTotal  atomic.Int64
	sessionConnBindTotal        atomic.Int64
	sessionConnDeleteTotal      atomic.Int64

	lastCleanupUnixNano atomic.Int64
}

// NewOpenAIWSStateStore 创建默认 WS 状态存储。
func NewOpenAIWSStateStore(cache GatewayCache) OpenAIWSStateStore {
	store := &defaultOpenAIWSStateStore{
		cache:              cache,
		responseToAccount:  make(map[string]openAIWSAccountBinding, 256),
		responseToConn:     make(map[string]openAIWSConnBinding, 256),
		sessionToTurnState: make(map[string]openAIWSTurnStateBinding, 256),
		sessionToLastResp:  make(map[string]openAIWSLastResponseBinding, 256),
		sessionToConn:      make(map[string]openAIWSSessionConnBinding, 256),
	}
	store.lastCleanupUnixNano.Store(time.Now().UnixNano())
	return store
}

func (s *defaultOpenAIWSStateStore) BindResponseAccount(ctx context.Context, groupID int64, responseID string, accountID int64, ttl time.Duration) error {
	id := normalizeOpenAIWSResponseID(responseID)
	if id == "" || accountID <= 0 {
		return nil
	}
	ttl = normalizeOpenAIWSTTL(ttl)
	s.maybeCleanup()

	expiresAt := time.Now().Add(ttl)
	s.responseToAccountMu.Lock()
	ensureBindingCapacity(s.responseToAccount, id, openAIWSStateStoreMaxEntriesPerMap)
	s.responseToAccount[id] = openAIWSAccountBinding{accountID: accountID, expiresAt: expiresAt}
	s.responseToAccountMu.Unlock()
	s.responseAccountBindTotal.Add(1)

	if s.cache == nil {
		return nil
	}
	cacheKey := openAIWSResponseAccountCacheKey(id)
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(ctx)
	defer cancel()
	return s.cache.SetSessionAccountID(cacheCtx, groupID, cacheKey, accountID, ttl)
}

func (s *defaultOpenAIWSStateStore) GetResponseAccount(ctx context.Context, groupID int64, responseID string) (int64, error) {
	id := normalizeOpenAIWSResponseID(responseID)
	if id == "" {
		return 0, nil
	}
	s.maybeCleanup()

	now := time.Now()
	s.responseToAccountMu.RLock()
	if binding, ok := s.responseToAccount[id]; ok {
		if now.Before(binding.expiresAt) {
			accountID := binding.accountID
			s.responseToAccountMu.RUnlock()
			return accountID, nil
		}
	}
	s.responseToAccountMu.RUnlock()

	if s.cache == nil {
		return 0, nil
	}

	cacheKey := openAIWSResponseAccountCacheKey(id)
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(ctx)
	defer cancel()
	accountID, err := s.cache.GetSessionAccountID(cacheCtx, groupID, cacheKey)
	if err != nil || accountID <= 0 {
		// 缓存读取失败不阻断主流程，按未命中降级。
		return 0, nil
	}
	return accountID, nil
}

func (s *defaultOpenAIWSStateStore) DeleteResponseAccount(ctx context.Context, groupID int64, responseID string) error {
	id := normalizeOpenAIWSResponseID(responseID)
	if id == "" {
		return nil
	}
	s.responseToAccountMu.Lock()
	if _, ok := s.responseToAccount[id]; ok {
		delete(s.responseToAccount, id)
		s.responseAccountDeleteTotal.Add(1)
	}
	s.responseToAccountMu.Unlock()

	if s.cache == nil {
		return nil
	}
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(ctx)
	defer cancel()
	return s.cache.DeleteSessionAccountID(cacheCtx, groupID, openAIWSResponseAccountCacheKey(id))
}

func (s *defaultOpenAIWSStateStore) BindResponseConn(responseID, connID string, ttl time.Duration) {
	id := normalizeOpenAIWSResponseID(responseID)
	conn := strings.TrimSpace(connID)
	if id == "" || conn == "" {
		return
	}
	ttl = normalizeOpenAIWSTTL(ttl)
	s.maybeCleanup()

	s.responseToConnMu.Lock()
	ensureBindingCapacity(s.responseToConn, id, openAIWSStateStoreMaxEntriesPerMap)
	s.responseToConn[id] = openAIWSConnBinding{
		connID:    conn,
		expiresAt: time.Now().Add(ttl),
	}
	s.responseToConnMu.Unlock()
	s.responseConnBindTotal.Add(1)
}

func (s *defaultOpenAIWSStateStore) GetResponseConn(responseID string) (string, bool) {
	id := normalizeOpenAIWSResponseID(responseID)
	if id == "" {
		return "", false
	}
	s.maybeCleanup()

	now := time.Now()
	s.responseToConnMu.RLock()
	binding, ok := s.responseToConn[id]
	s.responseToConnMu.RUnlock()
	if !ok || now.After(binding.expiresAt) || strings.TrimSpace(binding.connID) == "" {
		return "", false
	}
	return binding.connID, true
}

func (s *defaultOpenAIWSStateStore) DeleteResponseConn(responseID string) {
	id := normalizeOpenAIWSResponseID(responseID)
	if id == "" {
		return
	}
	s.responseToConnMu.Lock()
	if _, ok := s.responseToConn[id]; ok {
		delete(s.responseToConn, id)
		s.responseConnDeleteTotal.Add(1)
	}
	s.responseToConnMu.Unlock()
}

func (s *defaultOpenAIWSStateStore) BindSessionTurnState(groupID int64, sessionHash, turnState string, ttl time.Duration) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	state := strings.TrimSpace(turnState)
	if key == "" || state == "" {
		return
	}
	ttl = normalizeOpenAIWSTTL(ttl)
	s.maybeCleanup()

	s.sessionToTurnStateMu.Lock()
	ensureBindingCapacity(s.sessionToTurnState, key, openAIWSStateStoreMaxEntriesPerMap)
	s.sessionToTurnState[key] = openAIWSTurnStateBinding{
		turnState: state,
		expiresAt: time.Now().Add(ttl),
	}
	s.sessionToTurnStateMu.Unlock()
	s.sessionTurnStateBindTotal.Add(1)

	cache := openAIWSSessionTurnStateCacheOf(s.cache)
	if cache == nil {
		return
	}
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
	defer cancel()
	_ = cache.SetOpenAIWSSessionTurnState(cacheCtx, groupID, sessionHash, state, ttl)
}

func (s *defaultOpenAIWSStateStore) GetSessionTurnState(groupID int64, sessionHash string) (string, bool) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	if key == "" {
		return "", false
	}
	s.maybeCleanup()

	now := time.Now()
	s.sessionToTurnStateMu.RLock()
	binding, ok := s.sessionToTurnState[key]
	s.sessionToTurnStateMu.RUnlock()
	if !ok || now.After(binding.expiresAt) || strings.TrimSpace(binding.turnState) == "" {
		cache := openAIWSSessionTurnStateCacheOf(s.cache)
		if cache == nil {
			return "", false
		}
		cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
		defer cancel()
		state, err := cache.GetOpenAIWSSessionTurnState(cacheCtx, groupID, sessionHash)
		state = strings.TrimSpace(state)
		if err != nil || state == "" {
			return "", false
		}
		s.sessionToTurnStateMu.Lock()
		ensureBindingCapacity(s.sessionToTurnState, key, openAIWSStateStoreMaxEntriesPerMap)
		s.sessionToTurnState[key] = openAIWSTurnStateBinding{
			turnState: state,
			expiresAt: time.Now().Add(openAIWSStateStoreCleanupInterval),
		}
		s.sessionToTurnStateMu.Unlock()
		return state, true
	}
	return binding.turnState, true
}

func (s *defaultOpenAIWSStateStore) DeleteSessionTurnState(groupID int64, sessionHash string) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	if key == "" {
		return
	}
	s.sessionToTurnStateMu.Lock()
	if _, ok := s.sessionToTurnState[key]; ok {
		delete(s.sessionToTurnState, key)
		s.sessionTurnStateDeleteTotal.Add(1)
	}
	s.sessionToTurnStateMu.Unlock()

	cache := openAIWSSessionTurnStateCacheOf(s.cache)
	if cache == nil {
		return
	}
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
	defer cancel()
	_ = cache.DeleteOpenAIWSSessionTurnState(cacheCtx, groupID, sessionHash)
}

func (s *defaultOpenAIWSStateStore) BindSessionLastResponse(groupID int64, sessionHash, responseID string, ttl time.Duration) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	id := strings.TrimSpace(responseID)
	if key == "" || id == "" {
		return
	}
	ttl = normalizeOpenAIWSTTL(ttl)
	s.maybeCleanup()

	s.sessionToLastRespMu.Lock()
	ensureBindingCapacity(s.sessionToLastResp, key, openAIWSStateStoreMaxEntriesPerMap)
	s.sessionToLastResp[key] = openAIWSLastResponseBinding{
		responseID: id,
		expiresAt:  time.Now().Add(ttl),
	}
	s.sessionToLastRespMu.Unlock()
	s.sessionLastRespBindTotal.Add(1)

	cache := openAIWSSessionLastResponseCacheOf(s.cache)
	if cache == nil {
		return
	}
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
	defer cancel()
	_ = cache.SetOpenAIWSSessionLastResponse(cacheCtx, groupID, sessionHash, id, ttl)
}

func (s *defaultOpenAIWSStateStore) GetSessionLastResponse(groupID int64, sessionHash string) (string, bool) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	if key == "" {
		return "", false
	}
	s.maybeCleanup()

	now := time.Now()
	s.sessionToLastRespMu.RLock()
	binding, ok := s.sessionToLastResp[key]
	s.sessionToLastRespMu.RUnlock()
	if !ok || now.After(binding.expiresAt) || strings.TrimSpace(binding.responseID) == "" {
		cache := openAIWSSessionLastResponseCacheOf(s.cache)
		if cache == nil {
			return "", false
		}
		cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
		defer cancel()
		responseID, err := cache.GetOpenAIWSSessionLastResponse(cacheCtx, groupID, sessionHash)
		responseID = strings.TrimSpace(responseID)
		if err != nil || responseID == "" {
			return "", false
		}
		s.sessionToLastRespMu.Lock()
		ensureBindingCapacity(s.sessionToLastResp, key, openAIWSStateStoreMaxEntriesPerMap)
		s.sessionToLastResp[key] = openAIWSLastResponseBinding{
			responseID: responseID,
			expiresAt:  time.Now().Add(openAIWSStateStoreCleanupInterval),
		}
		s.sessionToLastRespMu.Unlock()
		return responseID, true
	}
	return binding.responseID, true
}

func (s *defaultOpenAIWSStateStore) DeleteSessionLastResponse(groupID int64, sessionHash string) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	if key == "" {
		return
	}
	s.sessionToLastRespMu.Lock()
	if _, ok := s.sessionToLastResp[key]; ok {
		delete(s.sessionToLastResp, key)
		s.sessionLastRespDeleteTotal.Add(1)
	}
	s.sessionToLastRespMu.Unlock()

	cache := openAIWSSessionLastResponseCacheOf(s.cache)
	if cache == nil {
		return
	}
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(context.Background())
	defer cancel()
	_ = cache.DeleteOpenAIWSSessionLastResponse(cacheCtx, groupID, sessionHash)
}

func (s *defaultOpenAIWSStateStore) BindSessionConn(groupID int64, sessionHash, connID string, ttl time.Duration) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	conn := strings.TrimSpace(connID)
	if key == "" || conn == "" {
		return
	}
	ttl = normalizeOpenAIWSTTL(ttl)
	s.maybeCleanup()

	s.sessionToConnMu.Lock()
	ensureBindingCapacity(s.sessionToConn, key, openAIWSStateStoreMaxEntriesPerMap)
	s.sessionToConn[key] = openAIWSSessionConnBinding{
		connID:    conn,
		expiresAt: time.Now().Add(ttl),
	}
	s.sessionToConnMu.Unlock()
	s.sessionConnBindTotal.Add(1)
}

func (s *defaultOpenAIWSStateStore) GetSessionConn(groupID int64, sessionHash string) (string, bool) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	if key == "" {
		return "", false
	}
	s.maybeCleanup()

	now := time.Now()
	s.sessionToConnMu.RLock()
	binding, ok := s.sessionToConn[key]
	s.sessionToConnMu.RUnlock()
	if !ok || now.After(binding.expiresAt) || strings.TrimSpace(binding.connID) == "" {
		return "", false
	}
	return binding.connID, true
}

func (s *defaultOpenAIWSStateStore) DeleteSessionConn(groupID int64, sessionHash string) {
	key := openAIWSSessionScopedKey(groupID, sessionHash)
	if key == "" {
		return
	}
	s.sessionToConnMu.Lock()
	if _, ok := s.sessionToConn[key]; ok {
		delete(s.sessionToConn, key)
		s.sessionConnDeleteTotal.Add(1)
	}
	s.sessionToConnMu.Unlock()
}

func (s *defaultOpenAIWSStateStore) DebugSnapshot() OpenAIWSStateStoreDebugSnapshot {
	if s == nil {
		return OpenAIWSStateStoreDebugSnapshot{
			LocalCleanupIntervalSeconds: int(openAIWSStateStoreCleanupInterval / time.Second),
			LocalCleanupMaxPerMap:       openAIWSStateStoreCleanupMaxPerMap,
			LocalMaxEntriesPerMap:       openAIWSStateStoreMaxEntriesPerMap,
			RedisTimeoutMillis:          int(openAIWSStateStoreRedisTimeout / time.Millisecond),
		}
	}
	s.maybeCleanup()

	s.responseToAccountMu.RLock()
	responseAccountEntries := len(s.responseToAccount)
	s.responseToAccountMu.RUnlock()

	s.responseToConnMu.RLock()
	responseConnEntries := len(s.responseToConn)
	s.responseToConnMu.RUnlock()

	s.sessionToTurnStateMu.RLock()
	sessionTurnStateEntries := len(s.sessionToTurnState)
	s.sessionToTurnStateMu.RUnlock()

	s.sessionToLastRespMu.RLock()
	sessionLastResponseEntries := len(s.sessionToLastResp)
	s.sessionToLastRespMu.RUnlock()

	s.sessionToConnMu.RLock()
	sessionConnEntries := len(s.sessionToConn)
	s.sessionToConnMu.RUnlock()

	return OpenAIWSStateStoreDebugSnapshot{
		ResponseAccountLocalEntries: responseAccountEntries,
		ResponseConnEntries:         responseConnEntries,
		SessionTurnStateEntries:     sessionTurnStateEntries,
		SessionLastResponseEntries:  sessionLastResponseEntries,
		SessionConnEntries:          sessionConnEntries,

		ResponseAccountBindTotal:       s.responseAccountBindTotal.Load(),
		ResponseAccountDeleteTotal:     s.responseAccountDeleteTotal.Load(),
		ResponseConnBindTotal:          s.responseConnBindTotal.Load(),
		ResponseConnDeleteTotal:        s.responseConnDeleteTotal.Load(),
		SessionTurnStateBindTotal:      s.sessionTurnStateBindTotal.Load(),
		SessionTurnStateDeleteTotal:    s.sessionTurnStateDeleteTotal.Load(),
		SessionLastResponseBindTotal:   s.sessionLastRespBindTotal.Load(),
		SessionLastResponseDeleteTotal: s.sessionLastRespDeleteTotal.Load(),
		SessionConnBindTotal:           s.sessionConnBindTotal.Load(),
		SessionConnDeleteTotal:         s.sessionConnDeleteTotal.Load(),

		ResponseAccountPersistent:     s.cache != nil,
		SessionTurnStatePersistent:    openAIWSSessionTurnStateCacheOf(s.cache) != nil,
		SessionLastResponsePersistent: openAIWSSessionLastResponseCacheOf(s.cache) != nil,

		LocalCleanupIntervalSeconds: int(openAIWSStateStoreCleanupInterval / time.Second),
		LocalCleanupMaxPerMap:       openAIWSStateStoreCleanupMaxPerMap,
		LocalMaxEntriesPerMap:       openAIWSStateStoreMaxEntriesPerMap,
		RedisTimeoutMillis:          int(openAIWSStateStoreRedisTimeout / time.Millisecond),
	}
}

func (s *defaultOpenAIWSStateStore) maybeCleanup() {
	if s == nil {
		return
	}
	now := time.Now()
	last := time.Unix(0, s.lastCleanupUnixNano.Load())
	if now.Sub(last) < openAIWSStateStoreCleanupInterval {
		return
	}
	if !s.lastCleanupUnixNano.CompareAndSwap(last.UnixNano(), now.UnixNano()) {
		return
	}

	// 增量限额清理，避免高规模下一次性全量扫描导致长时间阻塞。
	s.responseToAccountMu.Lock()
	cleanupExpiredAccountBindings(s.responseToAccount, now, openAIWSStateStoreCleanupMaxPerMap)
	s.responseToAccountMu.Unlock()

	s.responseToConnMu.Lock()
	cleanupExpiredConnBindings(s.responseToConn, now, openAIWSStateStoreCleanupMaxPerMap)
	s.responseToConnMu.Unlock()

	s.sessionToTurnStateMu.Lock()
	cleanupExpiredTurnStateBindings(s.sessionToTurnState, now, openAIWSStateStoreCleanupMaxPerMap)
	s.sessionToTurnStateMu.Unlock()

	s.sessionToLastRespMu.Lock()
	cleanupExpiredLastResponseBindings(s.sessionToLastResp, now, openAIWSStateStoreCleanupMaxPerMap)
	s.sessionToLastRespMu.Unlock()

	s.sessionToConnMu.Lock()
	cleanupExpiredSessionConnBindings(s.sessionToConn, now, openAIWSStateStoreCleanupMaxPerMap)
	s.sessionToConnMu.Unlock()
}

func cleanupExpiredAccountBindings(bindings map[string]openAIWSAccountBinding, now time.Time, maxScan int) {
	if len(bindings) == 0 || maxScan <= 0 {
		return
	}
	scanned := 0
	for key, binding := range bindings {
		if now.After(binding.expiresAt) {
			delete(bindings, key)
		}
		scanned++
		if scanned >= maxScan {
			break
		}
	}
}

func cleanupExpiredConnBindings(bindings map[string]openAIWSConnBinding, now time.Time, maxScan int) {
	if len(bindings) == 0 || maxScan <= 0 {
		return
	}
	scanned := 0
	for key, binding := range bindings {
		if now.After(binding.expiresAt) {
			delete(bindings, key)
		}
		scanned++
		if scanned >= maxScan {
			break
		}
	}
}

func cleanupExpiredTurnStateBindings(bindings map[string]openAIWSTurnStateBinding, now time.Time, maxScan int) {
	if len(bindings) == 0 || maxScan <= 0 {
		return
	}
	scanned := 0
	for key, binding := range bindings {
		if now.After(binding.expiresAt) {
			delete(bindings, key)
		}
		scanned++
		if scanned >= maxScan {
			break
		}
	}
}

func cleanupExpiredLastResponseBindings(bindings map[string]openAIWSLastResponseBinding, now time.Time, maxScan int) {
	if len(bindings) == 0 || maxScan <= 0 {
		return
	}
	scanned := 0
	for key, binding := range bindings {
		if now.After(binding.expiresAt) {
			delete(bindings, key)
		}
		scanned++
		if scanned >= maxScan {
			break
		}
	}
}

func cleanupExpiredSessionConnBindings(bindings map[string]openAIWSSessionConnBinding, now time.Time, maxScan int) {
	if len(bindings) == 0 || maxScan <= 0 {
		return
	}
	scanned := 0
	for key, binding := range bindings {
		if now.After(binding.expiresAt) {
			delete(bindings, key)
		}
		scanned++
		if scanned >= maxScan {
			break
		}
	}
}

func ensureBindingCapacity[T any](bindings map[string]T, incomingKey string, maxEntries int) {
	if len(bindings) < maxEntries || maxEntries <= 0 {
		return
	}
	if _, exists := bindings[incomingKey]; exists {
		return
	}
	// 固定上限保护：淘汰任意一项，优先保证内存有界。
	for key := range bindings {
		delete(bindings, key)
		return
	}
}

func normalizeOpenAIWSResponseID(responseID string) string {
	return strings.TrimSpace(responseID)
}

func openAIWSResponseAccountCacheKey(responseID string) string {
	sum := sha256.Sum256([]byte(responseID))
	return openAIWSResponseAccountCachePrefix + hex.EncodeToString(sum[:])
}

func openAIWSSessionLastResponseCacheOf(cache GatewayCache) openAIWSSessionLastResponseCache {
	if cache == nil {
		return nil
	}
	sessionCache, _ := cache.(openAIWSSessionLastResponseCache)
	return sessionCache
}

func openAIWSSessionTurnStateCacheOf(cache GatewayCache) openAIWSSessionTurnStateCache {
	if cache == nil {
		return nil
	}
	sessionCache, _ := cache.(openAIWSSessionTurnStateCache)
	return sessionCache
}

func normalizeOpenAIWSTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return time.Hour
	}
	return ttl
}

func openAIWSSessionScopedKey(groupID int64, sessionHash string) string {
	hash := strings.TrimSpace(sessionHash)
	if hash == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s", groupID, hash)
}

func withOpenAIWSStateStoreRedisTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, openAIWSStateStoreRedisTimeout)
}
