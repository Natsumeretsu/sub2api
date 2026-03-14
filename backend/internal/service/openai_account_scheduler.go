package service

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const (
	openAIAccountScheduleLayerPreviousResponse = "previous_response_id"
	openAIAccountScheduleLayerSessionSticky    = "session_hash"
	openAIAccountScheduleLayerLoadBalance      = "load_balance"
)

type OpenAIContinuationCohort string

const (
	OpenAIContinuationCohortStrong   OpenAIContinuationCohort = "strong"
	OpenAIContinuationCohortDegraded OpenAIContinuationCohort = "degraded"
)

type OpenAIAccountScheduleRequest struct {
	GroupID            *int64
	SessionHash        string
	StickyAccountID    int64
	PreviousResponseID string
	RequestedModel     string
	StreamRequested    bool
	CacheAffinityKey   string
	RequiredTransport  OpenAIUpstreamTransport
	RequiredCohort     OpenAIContinuationCohort
	ExcludedIDs        map[int64]struct{}
}

type OpenAIAccountScheduleDecision struct {
	Layer               string
	StickyPreviousHit   bool
	StickySessionHit    bool
	RequestedCohort     string
	SelectedCohort      string
	CohortFallback      bool
	CacheAffinityUsed   bool
	CacheAffinityScore  float64
	CandidateCount      int
	TopK                int
	LatencyMs           int64
	LoadSkew            float64
	SelectedAccountID   int64
	SelectedAccountType string
}

type OpenAIAccountSchedulerMetricsSnapshot struct {
	SelectTotal              int64
	StickyPreviousHitTotal   int64
	StickySessionHitTotal    int64
	LoadBalanceSelectTotal   int64
	AccountSwitchTotal       int64
	CohortFallbackTotal      int64
	CacheAffinitySelectTotal int64
	SchedulerLatencyMsTotal  int64
	SchedulerLatencyMsAvg    float64
	StickyHitRatio           float64
	AccountSwitchRate        float64
	LoadSkewAvg              float64
	RuntimeStatsAccountCount int
}

type OpenAIAccountScheduler interface {
	Select(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error)
	ReportResult(accountID int64, success bool, firstTokenMs *int)
	ReportSwitch()
	SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot
}

type openAIAccountSchedulerMetrics struct {
	selectTotal            atomic.Int64
	stickyPreviousHitTotal atomic.Int64
	stickySessionHitTotal  atomic.Int64
	loadBalanceSelectTotal atomic.Int64
	accountSwitchTotal     atomic.Int64
	cohortFallbackTotal    atomic.Int64
	cacheAffinityTotal     atomic.Int64
	latencyMsTotal         atomic.Int64
	loadSkewMilliTotal     atomic.Int64
}

func (m *openAIAccountSchedulerMetrics) recordSelect(decision OpenAIAccountScheduleDecision) {
	if m == nil {
		return
	}
	m.selectTotal.Add(1)
	m.latencyMsTotal.Add(decision.LatencyMs)
	m.loadSkewMilliTotal.Add(int64(math.Round(decision.LoadSkew * 1000)))
	if decision.StickyPreviousHit {
		m.stickyPreviousHitTotal.Add(1)
	}
	if decision.StickySessionHit {
		m.stickySessionHitTotal.Add(1)
	}
	if decision.Layer == openAIAccountScheduleLayerLoadBalance {
		m.loadBalanceSelectTotal.Add(1)
	}
	if decision.CohortFallback {
		m.cohortFallbackTotal.Add(1)
	}
	if decision.CacheAffinityUsed {
		m.cacheAffinityTotal.Add(1)
	}
}

func (m *openAIAccountSchedulerMetrics) recordSwitch() {
	if m == nil {
		return
	}
	m.accountSwitchTotal.Add(1)
}

type openAIAccountRuntimeStats struct {
	accounts     sync.Map
	accountCount atomic.Int64
}

type openAIAccountRuntimeStat struct {
	errorRateEWMABits atomic.Uint64
	ttftEWMABits      atomic.Uint64
}

func newOpenAIAccountRuntimeStats() *openAIAccountRuntimeStats {
	return &openAIAccountRuntimeStats{}
}

func (s *openAIAccountRuntimeStats) loadOrCreate(accountID int64) *openAIAccountRuntimeStat {
	if value, ok := s.accounts.Load(accountID); ok {
		stat, _ := value.(*openAIAccountRuntimeStat)
		if stat != nil {
			return stat
		}
	}

	stat := &openAIAccountRuntimeStat{}
	stat.ttftEWMABits.Store(math.Float64bits(math.NaN()))
	actual, loaded := s.accounts.LoadOrStore(accountID, stat)
	if !loaded {
		s.accountCount.Add(1)
		return stat
	}
	existing, _ := actual.(*openAIAccountRuntimeStat)
	if existing != nil {
		return existing
	}
	return stat
}

func updateEWMAAtomic(target *atomic.Uint64, sample float64, alpha float64) {
	for {
		oldBits := target.Load()
		oldValue := math.Float64frombits(oldBits)
		newValue := alpha*sample + (1-alpha)*oldValue
		if target.CompareAndSwap(oldBits, math.Float64bits(newValue)) {
			return
		}
	}
}

func (s *openAIAccountRuntimeStats) report(accountID int64, success bool, firstTokenMs *int) {
	if s == nil || accountID <= 0 {
		return
	}
	const alpha = 0.2
	stat := s.loadOrCreate(accountID)

	errorSample := 1.0
	if success {
		errorSample = 0.0
	}
	updateEWMAAtomic(&stat.errorRateEWMABits, errorSample, alpha)

	if firstTokenMs != nil && *firstTokenMs > 0 {
		ttft := float64(*firstTokenMs)
		ttftBits := math.Float64bits(ttft)
		for {
			oldBits := stat.ttftEWMABits.Load()
			oldValue := math.Float64frombits(oldBits)
			if math.IsNaN(oldValue) {
				if stat.ttftEWMABits.CompareAndSwap(oldBits, ttftBits) {
					break
				}
				continue
			}
			newValue := alpha*ttft + (1-alpha)*oldValue
			if stat.ttftEWMABits.CompareAndSwap(oldBits, math.Float64bits(newValue)) {
				break
			}
		}
	}
}

func (s *openAIAccountRuntimeStats) snapshot(accountID int64) (errorRate float64, ttft float64, hasTTFT bool) {
	if s == nil || accountID <= 0 {
		return 0, 0, false
	}
	value, ok := s.accounts.Load(accountID)
	if !ok {
		return 0, 0, false
	}
	stat, _ := value.(*openAIAccountRuntimeStat)
	if stat == nil {
		return 0, 0, false
	}
	errorRate = clamp01(math.Float64frombits(stat.errorRateEWMABits.Load()))
	ttftValue := math.Float64frombits(stat.ttftEWMABits.Load())
	if math.IsNaN(ttftValue) {
		return errorRate, 0, false
	}
	return errorRate, ttftValue, true
}

func (s *openAIAccountRuntimeStats) size() int {
	if s == nil {
		return 0
	}
	return int(s.accountCount.Load())
}

type defaultOpenAIAccountScheduler struct {
	service *OpenAIGatewayService
	metrics openAIAccountSchedulerMetrics
	stats   *openAIAccountRuntimeStats
}

func newDefaultOpenAIAccountScheduler(service *OpenAIGatewayService, stats *openAIAccountRuntimeStats) OpenAIAccountScheduler {
	if stats == nil {
		stats = newOpenAIAccountRuntimeStats()
	}
	return &defaultOpenAIAccountScheduler{
		service: service,
		stats:   stats,
	}
}

func (s *defaultOpenAIAccountScheduler) Select(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{}
	req.StreamRequested = req.StreamRequested || isOpenAIStreamingRequested(ctx)
	req.RequiredCohort = normalizeOpenAIRequiredCohort(req.RequiredCohort, req.RequiredTransport)
	req.CacheAffinityKey = normalizeOpenAICacheAffinityKey(req.CacheAffinityKey, req.SessionHash, req.PreviousResponseID, req.RequestedModel)
	decision.RequestedCohort = string(req.RequiredCohort)
	start := time.Now()
	defer func() {
		decision.LatencyMs = time.Since(start).Milliseconds()
		s.metrics.recordSelect(decision)
	}()

	previousResponseID := strings.TrimSpace(req.PreviousResponseID)
	if previousResponseID != "" {
		selection, err := s.service.SelectAccountByPreviousResponseID(
			ctx,
			req.GroupID,
			previousResponseID,
			req.RequestedModel,
			req.ExcludedIDs,
		)
		if err != nil {
			return nil, decision, err
		}
		if selection != nil && selection.Account != nil {
			if !s.isAccountEligibleForRequest(ctx, selection.Account, req) {
				selection = nil
			}
		}
		if selection != nil && selection.Account != nil {
			decision.Layer = openAIAccountScheduleLayerPreviousResponse
			decision.StickyPreviousHit = true
			decision.SelectedAccountID = selection.Account.ID
			decision.SelectedAccountType = selection.Account.Type
			decision.SelectedCohort = string(s.resolveAccountContinuationCohort(ctx, selection.Account, req.RequestedModel))
			if req.SessionHash != "" {
				_ = s.service.BindStickySession(ctx, req.GroupID, req.SessionHash, selection.Account.ID)
			}
			return selection, decision, nil
		}
	}

	selection, err := s.selectBySessionHash(ctx, req)
	if err != nil {
		return nil, decision, err
	}
	if selection != nil && selection.Account != nil {
		decision.Layer = openAIAccountScheduleLayerSessionSticky
		decision.StickySessionHit = true
		decision.SelectedAccountID = selection.Account.ID
		decision.SelectedAccountType = selection.Account.Type
		decision.SelectedCohort = string(s.resolveAccountContinuationCohort(ctx, selection.Account, req.RequestedModel))
		return selection, decision, nil
	}

	selection, candidateCount, topK, loadSkew, cohortFallback, err := s.selectByLoadBalance(ctx, req)
	decision.Layer = openAIAccountScheduleLayerLoadBalance
	decision.CandidateCount = candidateCount
	decision.TopK = topK
	decision.LoadSkew = loadSkew
	decision.CohortFallback = cohortFallback
	if err != nil {
		return nil, decision, err
	}
	if selection != nil && selection.Account != nil {
		decision.SelectedAccountID = selection.Account.ID
		decision.SelectedAccountType = selection.Account.Type
		decision.SelectedCohort = string(s.resolveAccountContinuationCohort(ctx, selection.Account, req.RequestedModel))
		if req.CacheAffinityKey != "" {
			decision.CacheAffinityUsed = true
			decision.CacheAffinityScore = computeOpenAICacheAffinityScore(req.CacheAffinityKey, selection.Account.ID)
		}
	}
	return selection, decision, nil
}

func (s *defaultOpenAIAccountScheduler) selectBySessionHash(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, error) {
	sessionHash := strings.TrimSpace(req.SessionHash)
	if sessionHash == "" || s == nil || s.service == nil || s.service.cache == nil {
		return nil, nil
	}

	accountID := req.StickyAccountID
	if accountID <= 0 {
		var err error
		accountID, err = s.service.getStickySessionAccountID(ctx, req.GroupID, sessionHash)
		if err != nil || accountID <= 0 {
			return nil, nil
		}
	}
	if accountID <= 0 {
		return nil, nil
	}
	if req.ExcludedIDs != nil {
		if _, excluded := req.ExcludedIDs[accountID]; excluded {
			return nil, nil
		}
	}

	account, err := s.service.getSchedulableAccount(ctx, accountID)
	if err != nil || account == nil {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, nil
	}
	if shouldClearStickySession(account, req.RequestedModel) || !account.IsOpenAI() || !account.IsSchedulable() {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, nil
	}
	if req.RequestedModel != "" && !account.IsModelSupported(req.RequestedModel) {
		return nil, nil
	}
	if !s.isAccountEligibleForRequest(ctx, account, req) {
		_ = s.service.deleteStickySessionAccountID(ctx, req.GroupID, sessionHash)
		return nil, nil
	}

	result, acquireErr := s.service.tryAcquireAccountSlot(ctx, accountID, account.Concurrency)
	if acquireErr == nil && result.Acquired {
		_ = s.service.refreshStickySessionTTL(ctx, req.GroupID, sessionHash, s.service.openAIWSSessionStickyTTL())
		return &AccountSelectionResult{
			Account:     account,
			Acquired:    true,
			ReleaseFunc: result.ReleaseFunc,
		}, nil
	}

	cfg := s.service.schedulingConfig()
	// WaitPlan.MaxConcurrency 使用 Concurrency（非 EffectiveLoadFactor），因为 WaitPlan 控制的是 Redis 实际并发槽位等待。
	if s.service.concurrencyService != nil {
		return &AccountSelectionResult{
			Account: account,
			WaitPlan: &AccountWaitPlan{
				AccountID:      accountID,
				MaxConcurrency: account.Concurrency,
				Timeout:        cfg.StickySessionWaitTimeout,
				MaxWaiting:     cfg.StickySessionMaxWaiting,
			},
		}, nil
	}
	return nil, nil
}

type openAIAccountCandidateScore struct {
	account          *Account
	loadInfo         *AccountLoadInfo
	score            float64
	errorRate        float64
	ttft             float64
	hasTTFT          bool
	affinity         float64
	bridgePreference float64
}

type openAIAccountCandidateHeap []openAIAccountCandidateScore

func (h openAIAccountCandidateHeap) Len() int {
	return len(h)
}

func (h openAIAccountCandidateHeap) Less(i, j int) bool {
	// 最小堆根节点保存“最差”候选，便于 O(log k) 维护 topK。
	return isOpenAIAccountCandidateBetter(h[j], h[i])
}

func (h openAIAccountCandidateHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *openAIAccountCandidateHeap) Push(x any) {
	candidate, ok := x.(openAIAccountCandidateScore)
	if !ok {
		panic("openAIAccountCandidateHeap: invalid element type")
	}
	*h = append(*h, candidate)
}

func (h *openAIAccountCandidateHeap) Pop() any {
	old := *h
	n := len(old)
	last := old[n-1]
	*h = old[:n-1]
	return last
}

func isOpenAIAccountCandidateBetter(left openAIAccountCandidateScore, right openAIAccountCandidateScore) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	if left.account.Priority != right.account.Priority {
		return left.account.Priority < right.account.Priority
	}
	if left.loadInfo.LoadRate != right.loadInfo.LoadRate {
		return left.loadInfo.LoadRate < right.loadInfo.LoadRate
	}
	if left.loadInfo.WaitingCount != right.loadInfo.WaitingCount {
		return left.loadInfo.WaitingCount < right.loadInfo.WaitingCount
	}
	return left.account.ID < right.account.ID
}

func selectTopKOpenAICandidates(candidates []openAIAccountCandidateScore, topK int) []openAIAccountCandidateScore {
	if len(candidates) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 1
	}
	if topK >= len(candidates) {
		ranked := append([]openAIAccountCandidateScore(nil), candidates...)
		sort.Slice(ranked, func(i, j int) bool {
			return isOpenAIAccountCandidateBetter(ranked[i], ranked[j])
		})
		return ranked
	}

	best := make(openAIAccountCandidateHeap, 0, topK)
	for _, candidate := range candidates {
		if len(best) < topK {
			heap.Push(&best, candidate)
			continue
		}
		if isOpenAIAccountCandidateBetter(candidate, best[0]) {
			best[0] = candidate
			heap.Fix(&best, 0)
		}
	}

	ranked := make([]openAIAccountCandidateScore, len(best))
	copy(ranked, best)
	sort.Slice(ranked, func(i, j int) bool {
		return isOpenAIAccountCandidateBetter(ranked[i], ranked[j])
	})
	return ranked
}

type openAISelectionRNG struct {
	state uint64
}

func newOpenAISelectionRNG(seed uint64) openAISelectionRNG {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return openAISelectionRNG{state: seed}
}

func (r *openAISelectionRNG) nextUint64() uint64 {
	// xorshift64*
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

func (r *openAISelectionRNG) nextFloat64() float64 {
	// [0,1)
	return float64(r.nextUint64()>>11) / (1 << 53)
}

func deriveOpenAISelectionSeed(req OpenAIAccountScheduleRequest) uint64 {
	hasher := fnv.New64a()
	writeValue := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		_, _ = hasher.Write([]byte(trimmed))
		_, _ = hasher.Write([]byte{0})
	}

	writeValue(req.SessionHash)
	writeValue(req.PreviousResponseID)
	writeValue(req.RequestedModel)
	writeValue(req.CacheAffinityKey)
	if req.GroupID != nil {
		_, _ = hasher.Write([]byte(strconv.FormatInt(*req.GroupID, 10)))
	}

	seed := hasher.Sum64()
	// 对“无会话锚点”的纯负载均衡请求引入时间熵，避免固定命中同一账号。
	if strings.TrimSpace(req.SessionHash) == "" && strings.TrimSpace(req.PreviousResponseID) == "" {
		seed ^= uint64(time.Now().UnixNano())
	}
	if seed == 0 {
		seed = uint64(time.Now().UnixNano()) ^ 0x9e3779b97f4a7c15
	}
	return seed
}

func buildOpenAIWeightedSelectionOrder(
	candidates []openAIAccountCandidateScore,
	req OpenAIAccountScheduleRequest,
) []openAIAccountCandidateScore {
	if len(candidates) <= 1 {
		return append([]openAIAccountCandidateScore(nil), candidates...)
	}

	pool := append([]openAIAccountCandidateScore(nil), candidates...)
	weights := make([]float64, len(pool))
	minScore := pool[0].score
	for i := 1; i < len(pool); i++ {
		if pool[i].score < minScore {
			minScore = pool[i].score
		}
	}
	for i := range pool {
		// 将 top-K 分值平移到正区间，避免“单一最高分账号”长期垄断。
		weight := (pool[i].score - minScore) + 1.0
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			weight = 1.0
		}
		weights[i] = weight
	}

	order := make([]openAIAccountCandidateScore, 0, len(pool))
	rng := newOpenAISelectionRNG(deriveOpenAISelectionSeed(req))
	for len(pool) > 0 {
		total := 0.0
		for _, w := range weights {
			total += w
		}

		selectedIdx := 0
		if total > 0 {
			r := rng.nextFloat64() * total
			acc := 0.0
			for i, w := range weights {
				acc += w
				if r <= acc {
					selectedIdx = i
					break
				}
			}
		} else {
			selectedIdx = int(rng.nextUint64() % uint64(len(pool)))
		}

		order = append(order, pool[selectedIdx])
		pool = append(pool[:selectedIdx], pool[selectedIdx+1:]...)
		weights = append(weights[:selectedIdx], weights[selectedIdx+1:]...)
	}
	return order
}

func (s *defaultOpenAIAccountScheduler) selectByLoadBalance(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, int, int, float64, bool, error) {
	accounts, err := s.service.listSchedulableAccounts(ctx, req.GroupID)
	if err != nil {
		return nil, 0, 0, 0, false, err
	}
	if len(accounts) == 0 {
		return nil, 0, 0, 0, false, errors.New("no available OpenAI accounts")
	}

	filtered := make([]*Account, 0, len(accounts))
	loadReq := make([]AccountWithConcurrency, 0, len(accounts))
	streamCapableFiltered := make([]*Account, 0, len(accounts))
	streamCapableLoadReq := make([]AccountWithConcurrency, 0, len(accounts))
	streamUnknownFiltered := make([]*Account, 0, len(accounts))
	streamUnknownLoadReq := make([]AccountWithConcurrency, 0, len(accounts))
	streamFallbackFiltered := make([]*Account, 0, len(accounts))
	streamFallbackLoadReq := make([]AccountWithConcurrency, 0, len(accounts))
	usingStreamFallbackBucket := false
	requiredCohort := normalizeOpenAIRequiredCohort(req.RequiredCohort, req.RequiredTransport)
	for i := range accounts {
		account := &accounts[i]
		if req.ExcludedIDs != nil {
			if _, excluded := req.ExcludedIDs[account.ID]; excluded {
				continue
			}
		}
		if !account.IsSchedulable() || !account.IsOpenAI() {
			continue
		}
		if req.RequestedModel != "" && !account.IsModelSupported(req.RequestedModel) {
			continue
		}
		if !s.isAccountTransportCompatible(ctx, account, req.RequiredTransport, req.RequestedModel) {
			continue
		}
		loadEntry := AccountWithConcurrency{
			ID:             account.ID,
			MaxConcurrency: account.EffectiveLoadFactor(),
		}
		if req.StreamRequested && req.RequiredTransport != OpenAIUpstreamTransportResponsesWebsocketV2 && req.RequiredTransport != OpenAIUpstreamTransportResponsesWebsocket {
			supported, known, _, err := s.service.ResolveOpenAIHTTPStreamingSupportForRequest(ctx, account, req.RequestedModel)
			switch {
			case err != nil:
				streamUnknownFiltered = append(streamUnknownFiltered, account)
				streamUnknownLoadReq = append(streamUnknownLoadReq, loadEntry)
				continue
			case known && supported:
				streamCapableFiltered = append(streamCapableFiltered, account)
				streamCapableLoadReq = append(streamCapableLoadReq, loadEntry)
				continue
			case known:
				streamFallbackFiltered = append(streamFallbackFiltered, account)
				streamFallbackLoadReq = append(streamFallbackLoadReq, loadEntry)
				continue
			default:
				streamUnknownFiltered = append(streamUnknownFiltered, account)
				streamUnknownLoadReq = append(streamUnknownLoadReq, loadEntry)
				continue
			}
		}
		filtered = append(filtered, account)
		loadReq = append(loadReq, loadEntry)
	}
	if req.StreamRequested && req.RequiredTransport != OpenAIUpstreamTransportResponsesWebsocketV2 && req.RequiredTransport != OpenAIUpstreamTransportResponsesWebsocket {
		switch {
		case len(streamCapableFiltered) > 0:
			filtered = streamCapableFiltered
			loadReq = streamCapableLoadReq
		case len(streamUnknownFiltered) > 0:
			filtered = streamUnknownFiltered
			loadReq = streamUnknownLoadReq
		case len(filtered) == 0 && len(streamFallbackFiltered) > 0:
			filtered = streamFallbackFiltered
			loadReq = streamFallbackLoadReq
			usingStreamFallbackBucket = true
		}
	}
	if len(filtered) == 0 {
		return nil, 0, 0, 0, false, errors.New("no available OpenAI accounts")
	}

	cohortFallback := false
	if requiredCohort != "" {
		cohortFiltered := make([]*Account, 0, len(filtered))
		cohortLoadReq := make([]AccountWithConcurrency, 0, len(loadReq))
		for i, account := range filtered {
			if s.resolveAccountContinuationCohort(ctx, account, req.RequestedModel) != requiredCohort {
				continue
			}
			cohortFiltered = append(cohortFiltered, account)
			cohortLoadReq = append(cohortLoadReq, loadReq[i])
		}
		if len(cohortFiltered) > 0 {
			filtered = cohortFiltered
			loadReq = cohortLoadReq
		} else {
			cohortFallback = true
		}
	}

	loadMap := map[int64]*AccountLoadInfo{}
	if s.service.concurrencyService != nil {
		if batchLoad, loadErr := s.service.concurrencyService.GetAccountsLoadBatch(ctx, loadReq); loadErr == nil {
			loadMap = batchLoad
		}
	}

	minPriority, maxPriority := filtered[0].Priority, filtered[0].Priority
	maxWaiting := 1
	loadRateSum := 0.0
	loadRateSumSquares := 0.0
	minTTFT, maxTTFT := 0.0, 0.0
	hasTTFTSample := false
	candidates := make([]openAIAccountCandidateScore, 0, len(filtered))
	for _, account := range filtered {
		loadInfo := loadMap[account.ID]
		if loadInfo == nil {
			loadInfo = &AccountLoadInfo{AccountID: account.ID}
		}
		if account.Priority < minPriority {
			minPriority = account.Priority
		}
		if account.Priority > maxPriority {
			maxPriority = account.Priority
		}
		if loadInfo.WaitingCount > maxWaiting {
			maxWaiting = loadInfo.WaitingCount
		}
		errorRate, ttft, hasTTFT := s.stats.snapshot(account.ID)
		if hasTTFT && ttft > 0 {
			if !hasTTFTSample {
				minTTFT, maxTTFT = ttft, ttft
				hasTTFTSample = true
			} else {
				if ttft < minTTFT {
					minTTFT = ttft
				}
				if ttft > maxTTFT {
					maxTTFT = ttft
				}
			}
		}
		loadRate := float64(loadInfo.LoadRate)
		loadRateSum += loadRate
		loadRateSumSquares += loadRate * loadRate
		candidates = append(candidates, openAIAccountCandidateScore{
			account:   account,
			loadInfo:  loadInfo,
			errorRate: errorRate,
			ttft:      ttft,
			hasTTFT:   hasTTFT,
		})
	}
	loadSkew := calcLoadSkewByMoments(loadRateSum, loadRateSumSquares, len(candidates))

	weights := s.service.openAIWSSchedulerWeights()
	for i := range candidates {
		item := &candidates[i]
		priorityFactor := 1.0
		if maxPriority > minPriority {
			priorityFactor = 1 - float64(item.account.Priority-minPriority)/float64(maxPriority-minPriority)
		}
		loadFactor := 1 - clamp01(float64(item.loadInfo.LoadRate)/100.0)
		queueFactor := 1 - clamp01(float64(item.loadInfo.WaitingCount)/float64(maxWaiting))
		errorFactor := 1 - clamp01(item.errorRate)
		ttftFactor := 0.5
		if item.hasTTFT && hasTTFTSample && maxTTFT > minTTFT {
			ttftFactor = 1 - clamp01((item.ttft-minTTFT)/(maxTTFT-minTTFT))
		}
		affinityFactor := computeOpenAICacheAffinityScore(req.CacheAffinityKey, item.account.ID)
		item.affinity = affinityFactor

		item.score = weights.Priority*priorityFactor +
			weights.Load*loadFactor +
			weights.Queue*queueFactor +
			weights.ErrorRate*errorFactor +
			weights.TTFT*ttftFactor +
			weights.CacheAffinity*affinityFactor
		if usingStreamFallbackBucket {
			item.bridgePreference = s.computeOpenAIHTTPStreamFallbackPreference(item.account)
			item.score += 2.0 * item.bridgePreference
		}
	}
	if len(candidates) == 0 {
		return nil, 0, 0, 0, cohortFallback, errors.New("no available OpenAI accounts")
	}

	topK := s.service.openAIWSLBTopK()
	if topK > len(candidates) {
		topK = len(candidates)
	}
	if topK <= 0 {
		topK = 1
	}
	rankedCandidates := selectTopKOpenAICandidates(candidates, topK)
	selectionOrder := buildOpenAIWeightedSelectionOrder(rankedCandidates, req)

	for i := 0; i < len(selectionOrder); i++ {
		candidate := selectionOrder[i]
		fresh := s.service.resolveFreshSchedulableOpenAIAccount(ctx, candidate.account, req.RequestedModel)
		if fresh == nil || !s.isAccountEligibleForRequest(ctx, fresh, req) {
			continue
		}
		result, acquireErr := s.service.tryAcquireAccountSlot(ctx, fresh.ID, fresh.Concurrency)
		if acquireErr != nil {
			return nil, len(candidates), topK, loadSkew, cohortFallback, acquireErr
		}
		if result != nil && result.Acquired {
			if req.SessionHash != "" {
				_ = s.service.BindStickySession(ctx, req.GroupID, req.SessionHash, fresh.ID)
			}
			return &AccountSelectionResult{
				Account:     fresh,
				Acquired:    true,
				ReleaseFunc: result.ReleaseFunc,
			}, len(candidates), topK, loadSkew, cohortFallback, nil
		}
	}

	cfg := s.service.schedulingConfig()
	// WaitPlan.MaxConcurrency 使用 Concurrency（非 EffectiveLoadFactor），因为 WaitPlan 控制的是 Redis 实际并发槽位等待。
	for _, candidate := range selectionOrder {
		fresh := s.service.resolveFreshSchedulableOpenAIAccount(ctx, candidate.account, req.RequestedModel)
		if fresh == nil || !s.isAccountEligibleForRequest(ctx, fresh, req) {
			continue
		}
		return &AccountSelectionResult{
			Account: fresh,
			WaitPlan: &AccountWaitPlan{
				AccountID:      fresh.ID,
				MaxConcurrency: fresh.Concurrency,
				Timeout:        cfg.FallbackWaitTimeout,
				MaxWaiting:     cfg.FallbackMaxWaiting,
			},
		}, len(candidates), topK, loadSkew, cohortFallback, nil
	}

	return nil, len(candidates), topK, loadSkew, cohortFallback, errors.New("no available accounts")
}

func isOpenAIStreamingRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	streamRequested, _ := ctx.Value(ctxkey.OpenAIStreamRequested).(bool)
	return streamRequested
}

func (s *defaultOpenAIAccountScheduler) isAccountTransportCompatible(
	ctx context.Context,
	account *Account,
	requiredTransport OpenAIUpstreamTransport,
	requestedModel string,
) bool {
	// HTTP 入站可回退到 HTTP 线路，不需要在账号选择阶段做传输协议强过滤。
	if requiredTransport == OpenAIUpstreamTransportAny || requiredTransport == OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || s.service == nil || account == nil {
		return false
	}
	return s.service.resolveOpenAIWSProtocolDecision(ctx, account, requestedModel).Transport == requiredTransport
}

func (s *defaultOpenAIAccountScheduler) isAccountEligibleForRequest(
	ctx context.Context,
	account *Account,
	req OpenAIAccountScheduleRequest,
) bool {
	if !s.isAccountTransportCompatible(ctx, account, req.RequiredTransport, req.RequestedModel) {
		return false
	}
	if req.StreamRequested && !s.isAccountHTTPStreamingCompatible(ctx, account, req.RequiredTransport, req.RequestedModel) {
		return false
	}
	return true
}

func (s *defaultOpenAIAccountScheduler) isAccountHTTPStreamingCompatible(
	ctx context.Context,
	account *Account,
	requiredTransport OpenAIUpstreamTransport,
	requestedModel string,
) bool {
	if requiredTransport == OpenAIUpstreamTransportResponsesWebsocketV2 || requiredTransport == OpenAIUpstreamTransportResponsesWebsocket {
		return true
	}
	if s == nil || s.service == nil || account == nil {
		return false
	}
	supported, known, _, err := s.service.ResolveOpenAIHTTPStreamingSupportForRequest(ctx, account, requestedModel)
	if err != nil {
		return true
	}
	if !known {
		return true
	}
	if supported {
		return true
	}
	return canUseOpenAIHTTPNonStreamingBridge(requiredTransport, true)
}

func (s *defaultOpenAIAccountScheduler) computeOpenAIHTTPStreamFallbackPreference(account *Account) float64 {
	if s == nil || s.service == nil || account == nil {
		return 0
	}
	supported, known, source := s.service.ResolveOpenAIHTTPStreamingSupport(account)
	if supported {
		return 1
	}
	if !known {
		return 0.5
	}
	score := 0.2
	source = strings.TrimSpace(strings.ToLower(source))
	switch {
	case strings.HasPrefix(source, "protocol_failure:"):
		score = 0.0
	case strings.HasPrefix(source, "probe_"):
		score = 0.35
	}
	if s.service.SupportsOpenAIResponsesCompactForRuntime(account) {
		score += 0.45
	}
	return clamp01(score)
}

func normalizeOpenAIRequiredCohort(requiredCohort OpenAIContinuationCohort, requiredTransport OpenAIUpstreamTransport) OpenAIContinuationCohort {
	switch requiredCohort {
	case OpenAIContinuationCohortStrong, OpenAIContinuationCohortDegraded:
		return requiredCohort
	}
	if requiredTransport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		return OpenAIContinuationCohortStrong
	}
	return OpenAIContinuationCohortDegraded
}

func normalizeOpenAICacheAffinityKey(cacheAffinityKey string, sessionHash string, previousResponseID string, requestedModel string) string {
	cacheAffinityKey = strings.TrimSpace(cacheAffinityKey)
	if cacheAffinityKey != "" {
		return cacheAffinityKey
	}
	sessionHash = strings.TrimSpace(sessionHash)
	previousResponseID = strings.TrimSpace(previousResponseID)
	requestedModel = strings.TrimSpace(requestedModel)
	if sessionHash == "" && previousResponseID == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if sessionHash != "" {
		parts = append(parts, sessionHash)
	}
	if previousResponseID != "" {
		parts = append(parts, previousResponseID)
	}
	if requestedModel != "" {
		parts = append(parts, requestedModel)
	}
	return strings.Join(parts, "|")
}

func computeOpenAICacheAffinityScore(cacheAffinityKey string, accountID int64) float64 {
	cacheAffinityKey = strings.TrimSpace(cacheAffinityKey)
	if cacheAffinityKey == "" || accountID <= 0 {
		return 0
	}
	sum := sha256.Sum256([]byte(cacheAffinityKey + "|" + strconv.FormatInt(accountID, 10)))
	value := binary.BigEndian.Uint64(sum[:8])
	return float64(value) / float64(math.MaxUint64)
}

func (s *defaultOpenAIAccountScheduler) resolveAccountContinuationCohort(
	ctx context.Context,
	account *Account,
	requestedModel string,
) OpenAIContinuationCohort {
	if s == nil || s.service == nil || account == nil {
		return OpenAIContinuationCohortDegraded
	}
	if s.service.resolveOpenAIWSProtocolDecision(ctx, account, requestedModel).Transport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		return OpenAIContinuationCohortStrong
	}
	return OpenAIContinuationCohortDegraded
}

func (s *defaultOpenAIAccountScheduler) ReportResult(accountID int64, success bool, firstTokenMs *int) {
	if s == nil || s.stats == nil {
		return
	}
	s.stats.report(accountID, success, firstTokenMs)
}

func (s *defaultOpenAIAccountScheduler) ReportSwitch() {
	if s == nil {
		return
	}
	s.metrics.recordSwitch()
}

func (s *defaultOpenAIAccountScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	if s == nil {
		return OpenAIAccountSchedulerMetricsSnapshot{}
	}

	selectTotal := s.metrics.selectTotal.Load()
	prevHit := s.metrics.stickyPreviousHitTotal.Load()
	sessionHit := s.metrics.stickySessionHitTotal.Load()
	switchTotal := s.metrics.accountSwitchTotal.Load()
	latencyTotal := s.metrics.latencyMsTotal.Load()
	loadSkewTotal := s.metrics.loadSkewMilliTotal.Load()

	snapshot := OpenAIAccountSchedulerMetricsSnapshot{
		SelectTotal:              selectTotal,
		StickyPreviousHitTotal:   prevHit,
		StickySessionHitTotal:    sessionHit,
		LoadBalanceSelectTotal:   s.metrics.loadBalanceSelectTotal.Load(),
		AccountSwitchTotal:       switchTotal,
		CohortFallbackTotal:      s.metrics.cohortFallbackTotal.Load(),
		CacheAffinitySelectTotal: s.metrics.cacheAffinityTotal.Load(),
		SchedulerLatencyMsTotal:  latencyTotal,
		RuntimeStatsAccountCount: s.stats.size(),
	}
	if selectTotal > 0 {
		snapshot.SchedulerLatencyMsAvg = float64(latencyTotal) / float64(selectTotal)
		snapshot.StickyHitRatio = float64(prevHit+sessionHit) / float64(selectTotal)
		snapshot.AccountSwitchRate = float64(switchTotal) / float64(selectTotal)
		snapshot.LoadSkewAvg = float64(loadSkewTotal) / 1000 / float64(selectTotal)
	}
	return snapshot
}

func (s *OpenAIGatewayService) getOpenAIAccountScheduler() OpenAIAccountScheduler {
	if s == nil {
		return nil
	}
	s.openaiSchedulerOnce.Do(func() {
		if s.openaiAccountStats == nil {
			s.openaiAccountStats = newOpenAIAccountRuntimeStats()
		}
		if s.openaiScheduler == nil {
			s.openaiScheduler = newDefaultOpenAIAccountScheduler(s, s.openaiAccountStats)
		}
	})
	return s.openaiScheduler
}

func (s *OpenAIGatewayService) SelectAccountWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	cacheAffinityKey ...string,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{}
	scheduler := s.getOpenAIAccountScheduler()
	requiredCohort := normalizeOpenAIRequiredCohort("", requiredTransport)
	if scheduler == nil {
		selection, err := s.SelectAccountWithLoadAwareness(ctx, groupID, sessionHash, requestedModel, excludedIDs)
		decision.Layer = openAIAccountScheduleLayerLoadBalance
		decision.RequestedCohort = string(requiredCohort)
		return selection, decision, err
	}

	stickyAccountID, stickyRecovered, _ := s.resolveOpenAIWSStickyAccountIDForSession(ctx, groupID, sessionHash)

	affinityKey := ""
	if len(cacheAffinityKey) > 0 {
		affinityKey = strings.TrimSpace(cacheAffinityKey[0])
	}

	selection, decision, err := scheduler.Select(ctx, OpenAIAccountScheduleRequest{
		GroupID:            groupID,
		SessionHash:        sessionHash,
		StickyAccountID:    stickyAccountID,
		PreviousResponseID: previousResponseID,
		RequestedModel:     requestedModel,
		StreamRequested:    isOpenAIStreamingRequested(ctx),
		CacheAffinityKey:   affinityKey,
		RequiredTransport:  requiredTransport,
		RequiredCohort:     requiredCohort,
		ExcludedIDs:        excludedIDs,
	})
	s.rebindRecoveredOpenAIWSStickyAccount(ctx, groupID, sessionHash, stickyAccountID, stickyRecovered, selection)
	return selection, decision, err
}

func (s *OpenAIGatewayService) resolveOpenAIWSStickyAccountIDForSession(ctx context.Context, groupID *int64, sessionHash string) (int64, bool, string) {
	sessionHash = strings.TrimSpace(sessionHash)
	if sessionHash == "" {
		return 0, false, ""
	}
	if s != nil && s.cache != nil {
		if accountID, err := s.getStickySessionAccountID(ctx, groupID, sessionHash); err == nil && accountID > 0 {
			return accountID, false, ""
		}
	}
	stateStore := s.getOpenAIWSStateStore()
	if stateStore == nil {
		return 0, false, ""
	}
	lastResponseID, ok := stateStore.GetSessionLastResponse(derefGroupID(groupID), sessionHash)
	lastResponseID = strings.TrimSpace(lastResponseID)
	if !ok || lastResponseID == "" {
		return 0, false, ""
	}
	accountID, err := stateStore.GetResponseAccount(ctx, derefGroupID(groupID), lastResponseID)
	if err != nil || accountID <= 0 {
		return 0, false, lastResponseID
	}
	return accountID, true, lastResponseID
}

func (s *OpenAIGatewayService) rebindRecoveredOpenAIWSStickyAccount(ctx context.Context, groupID *int64, sessionHash string, stickyAccountID int64, stickyRecovered bool, selection *AccountSelectionResult) {
	if !stickyRecovered || strings.TrimSpace(sessionHash) == "" || stickyAccountID <= 0 || selection == nil || selection.Account == nil {
		return
	}
	if selection.Account.ID != stickyAccountID {
		return
	}
	if err := s.setStickySessionAccountID(ctx, groupID, sessionHash, stickyAccountID, s.openAIWSSessionStickyTTL()); err == nil {
		recordOpenAIWSContinuationSessionStickyRebindFromLastResponse()
	}
}

func (s *OpenAIGatewayService) ReportOpenAIAccountScheduleResult(accountID int64, success bool, firstTokenMs *int) {
	scheduler := s.getOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportResult(accountID, success, firstTokenMs)
}

func (s *OpenAIGatewayService) RecordOpenAIAccountSwitch() {
	scheduler := s.getOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportSwitch()
}

func (s *OpenAIGatewayService) SnapshotOpenAIAccountSchedulerMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	scheduler := s.getOpenAIAccountScheduler()
	if scheduler == nil {
		return OpenAIAccountSchedulerMetricsSnapshot{}
	}
	return scheduler.SnapshotMetrics()
}

func (s *OpenAIGatewayService) openAIWSSessionStickyTTL() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds > 0 {
		return time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second
	}
	return openaiStickySessionTTL
}

func (s *OpenAIGatewayService) openAIWSLBTopK() int {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.LBTopK > 0 {
		return s.cfg.Gateway.OpenAIWS.LBTopK
	}
	return 7
}

func (s *OpenAIGatewayService) openAIWSSchedulerWeights() GatewayOpenAIWSSchedulerScoreWeightsView {
	if s != nil && s.cfg != nil {
		return GatewayOpenAIWSSchedulerScoreWeightsView{
			Priority:      s.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority,
			Load:          s.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Load,
			Queue:         s.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Queue,
			ErrorRate:     s.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.ErrorRate,
			TTFT:          s.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.TTFT,
			CacheAffinity: s.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.CacheAffinity,
		}
	}
	return GatewayOpenAIWSSchedulerScoreWeightsView{
		Priority:      1.0,
		Load:          1.0,
		Queue:         0.7,
		ErrorRate:     0.8,
		TTFT:          0.5,
		CacheAffinity: 1.3,
	}
}

type GatewayOpenAIWSSchedulerScoreWeightsView struct {
	Priority      float64
	Load          float64
	Queue         float64
	ErrorRate     float64
	TTFT          float64
	CacheAffinity float64
}

func clamp01(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func calcLoadSkewByMoments(sum float64, sumSquares float64, count int) float64 {
	if count <= 1 {
		return 0
	}
	mean := sum / float64(count)
	variance := sumSquares/float64(count) - mean*mean
	if variance < 0 {
		variance = 0
	}
	return math.Sqrt(variance)
}
