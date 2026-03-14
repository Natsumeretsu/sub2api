package service

import (
	"context"
	"strings"
	"time"
)

const openAIHTTPStreamingCapabilityObservationTTL = 6 * time.Hour
const openAIHTTPStreamingCapabilityCacheTimeout = 3 * time.Second

type openAIHTTPStreamingCapabilityObservation struct {
	Supported bool
	Source    string
	CheckedAt time.Time
	ExpiresAt time.Time
}

type openAIHTTPStreamingCapabilityCache interface {
	GetOpenAIHTTPStreamingCapability(ctx context.Context, accountID int64) (bool, string, error)
	SetOpenAIHTTPStreamingCapability(ctx context.Context, accountID int64, supported bool, source string, ttl time.Duration) error
}

type openAIHTTPStreamingCapabilityTTLCache interface {
	GetOpenAIHTTPStreamingCapabilityWithTTL(ctx context.Context, accountID int64) (bool, string, time.Duration, error)
}

func (s *OpenAIGatewayService) ResolveOpenAIHTTPStreamingSupport(account *Account) (supported bool, known bool, source string) {
	supported, known, source = resolveOpenAIHTTPStreamingStaticCapability(account)
	if known {
		return supported, true, source
	}
	if observed, ok := s.getObservedOpenAIHTTPStreamingCapability(account); ok {
		return observed.Supported, true, observed.Source
	}
	if observed, ok := s.getCachedOpenAIHTTPStreamingCapability(account); ok {
		s.openaiHTTPStreamingCapability.Store(account.ID, observed)
		return observed.Supported, true, observed.Source
	}
	return false, false, ""
}

func resolveOpenAIHTTPStreamingStaticCapability(account *Account) (supported bool, known bool, source string) {
	if account == nil {
		return false, false, ""
	}
	return account.ResolveOpenAIHTTPStreamingCapability()
}

func (s *OpenAIGatewayService) getObservedOpenAIHTTPStreamingCapability(account *Account) (openAIHTTPStreamingCapabilityObservation, bool) {
	if s == nil || account == nil || account.ID <= 0 {
		return openAIHTTPStreamingCapabilityObservation{}, false
	}
	raw, ok := s.openaiHTTPStreamingCapability.Load(account.ID)
	if !ok {
		return openAIHTTPStreamingCapabilityObservation{}, false
	}
	observation, ok := raw.(openAIHTTPStreamingCapabilityObservation)
	if !ok {
		s.openaiHTTPStreamingCapability.Delete(account.ID)
		return openAIHTTPStreamingCapabilityObservation{}, false
	}
	if !observation.ExpiresAt.IsZero() && time.Now().After(observation.ExpiresAt) {
		s.openaiHTTPStreamingCapability.Delete(account.ID)
		return openAIHTTPStreamingCapabilityObservation{}, false
	}
	return observation, true
}

func (s *OpenAIGatewayService) setObservedOpenAIHTTPStreamingCapability(account *Account, supported bool, source string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	now := time.Now()
	observation := openAIHTTPStreamingCapabilityObservation{
		Supported: supported,
		Source:    strings.TrimSpace(source),
		CheckedAt: now,
		ExpiresAt: now.Add(openAIHTTPStreamingCapabilityObservationTTL),
	}
	s.openaiHTTPStreamingCapability.Store(account.ID, observation)
	s.setCachedOpenAIHTTPStreamingCapability(account, observation)
}

func (s *OpenAIGatewayService) getCachedOpenAIHTTPStreamingCapability(account *Account) (openAIHTTPStreamingCapabilityObservation, bool) {
	if s == nil || s.cache == nil || account == nil || account.ID <= 0 {
		return openAIHTTPStreamingCapabilityObservation{}, false
	}
	cache := openAIHTTPStreamingCapabilityCacheOf(s.cache)
	if cache == nil {
		return openAIHTTPStreamingCapabilityObservation{}, false
	}
	cacheCtx, cancel := withOpenAIHTTPStreamingCapabilityTimeout()
	defer cancel()
	if ttlCache := openAIHTTPStreamingCapabilityTTLCacheOf(s.cache); ttlCache != nil {
		supported, source, ttl, err := ttlCache.GetOpenAIHTTPStreamingCapabilityWithTTL(cacheCtx, account.ID)
		if err != nil {
			return openAIHTTPStreamingCapabilityObservation{}, false
		}
		return newOpenAIHTTPStreamingCapabilityObservation(supported, source, ttl), true
	}
	supported, source, err := cache.GetOpenAIHTTPStreamingCapability(cacheCtx, account.ID)
	if err != nil {
		return openAIHTTPStreamingCapabilityObservation{}, false
	}
	return newOpenAIHTTPStreamingCapabilityObservation(supported, source, openAIHTTPStreamingCapabilityObservationTTL), true
}

func (s *OpenAIGatewayService) setCachedOpenAIHTTPStreamingCapability(account *Account, observation openAIHTTPStreamingCapabilityObservation) {
	if s == nil || s.cache == nil || account == nil || account.ID <= 0 {
		return
	}
	cache := openAIHTTPStreamingCapabilityCacheOf(s.cache)
	if cache == nil {
		return
	}
	ttl := time.Until(observation.ExpiresAt)
	if ttl <= 0 {
		ttl = openAIHTTPStreamingCapabilityObservationTTL
	}
	cacheCtx, cancel := withOpenAIHTTPStreamingCapabilityTimeout()
	defer cancel()
	_ = cache.SetOpenAIHTTPStreamingCapability(cacheCtx, account.ID, observation.Supported, observation.Source, ttl)
}

func newOpenAIHTTPStreamingCapabilityObservation(supported bool, source string, ttl time.Duration) openAIHTTPStreamingCapabilityObservation {
	now := time.Now()
	if ttl <= 0 {
		ttl = openAIHTTPStreamingCapabilityObservationTTL
	}
	return openAIHTTPStreamingCapabilityObservation{
		Supported: supported,
		Source:    strings.TrimSpace(source),
		CheckedAt: now,
		ExpiresAt: now.Add(ttl),
	}
}

func withOpenAIHTTPStreamingCapabilityTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), openAIHTTPStreamingCapabilityCacheTimeout)
}

func openAIHTTPStreamingCapabilityCacheOf(cache GatewayCache) openAIHTTPStreamingCapabilityCache {
	if cache == nil {
		return nil
	}
	streamingCache, _ := cache.(openAIHTTPStreamingCapabilityCache)
	return streamingCache
}

func openAIHTTPStreamingCapabilityTTLCacheOf(cache GatewayCache) openAIHTTPStreamingCapabilityTTLCache {
	if cache == nil {
		return nil
	}
	ttlCache, _ := cache.(openAIHTTPStreamingCapabilityTTLCache)
	return ttlCache
}
