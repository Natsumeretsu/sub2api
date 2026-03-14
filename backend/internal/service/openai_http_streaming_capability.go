package service

import (
	"strings"
	"time"
)

const openAIHTTPStreamingCapabilityObservationTTL = 6 * time.Hour

type openAIHTTPStreamingCapabilityObservation struct {
	Supported bool
	Source    string
	CheckedAt time.Time
	ExpiresAt time.Time
}

func (s *OpenAIGatewayService) ResolveOpenAIHTTPStreamingSupport(account *Account) (supported bool, known bool, source string) {
	supported, known, source = resolveOpenAIHTTPStreamingStaticCapability(account)
	if known {
		return supported, true, source
	}
	if observed, ok := s.getObservedOpenAIHTTPStreamingCapability(account); ok {
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
	s.openaiHTTPStreamingCapability.Store(account.ID, openAIHTTPStreamingCapabilityObservation{
		Supported: supported,
		Source:    strings.TrimSpace(source),
		CheckedAt: now,
		ExpiresAt: now.Add(openAIHTTPStreamingCapabilityObservationTTL),
	})
}
