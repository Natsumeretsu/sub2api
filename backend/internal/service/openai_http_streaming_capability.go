package service

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"
)

const openAIHTTPStreamingCapabilityObservationTTL = 6 * time.Hour
const openAIHTTPStreamingCapabilityCacheTimeout = 3 * time.Second
const openAIHTTPStreamingCapabilityProbeTimeout = 8 * time.Second
const openAIHTTPStreamingCapabilityProbeReadMaxBytes = 64 << 10

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

type openAIHTTPStreamingCapabilityProbeResult struct {
	Supported bool
	Known     bool
	Source    string
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

func (s *OpenAIGatewayService) ResolveOpenAIHTTPStreamingSupportForRequest(
	ctx context.Context,
	account *Account,
	requestedModel string,
) (supported bool, known bool, source string, err error) {
	supported, known, source = s.ResolveOpenAIHTTPStreamingSupport(account)
	if known || s == nil || account == nil || account.ID <= 0 {
		return supported, known, source, nil
	}

	flightKey := openAIHTTPStreamingCapabilityProbeFlightKey(account, requestedModel)
	value, probeErr, _ := s.openaiHTTPStreamingProbeSF.Do(flightKey, func() (any, error) {
		probeSupported, probeKnown, probeSource, err := s.probeOpenAIHTTPStreamingSupport(ctx, account, requestedModel)
		if probeKnown {
			s.setObservedOpenAIHTTPStreamingCapability(account, probeSupported, probeSource)
		}
		return openAIHTTPStreamingCapabilityProbeResult{
			Supported: probeSupported,
			Known:     probeKnown,
			Source:    probeSource,
		}, err
	})
	if probeErr != nil {
		return false, false, "", probeErr
	}
	if result, ok := value.(openAIHTTPStreamingCapabilityProbeResult); ok {
		return result.Supported, result.Known, result.Source, nil
	}
	return false, false, "", nil
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

func (s *OpenAIGatewayService) probeOpenAIHTTPStreamingSupport(
	ctx context.Context,
	account *Account,
	requestedModel string,
) (supported bool, known bool, source string, err error) {
	if s == nil || account == nil || !account.IsOpenAIApiKey() {
		return false, false, "", nil
	}
	if s.httpUpstream == nil {
		return false, false, "probe_unavailable", nil
	}

	probeCtx := ctx
	if probeCtx == nil {
		probeCtx = context.Background()
	}
	if _, hasDeadline := probeCtx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(probeCtx, openAIHTTPStreamingCapabilityProbeTimeout)
		defer cancel()
	}

	token, _, err := s.GetAccessToken(probeCtx, account)
	if err != nil {
		return false, false, "probe_access_token_error", err
	}

	baseURL := account.GetOpenAIBaseURL()
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return false, false, "probe_invalid_base_url", err
	}
	targetURL := buildOpenAIResponsesURL(validatedURL)

	model := strings.TrimSpace(requestedModel)
	if model == "" {
		model = "gpt-5.4"
	}
	body := []byte(`{"model":"` + model + `","stream":true,"store":false,"max_output_tokens":4,"input":[{"role":"user","content":[{"type":"input_text","text":"stream capability probe: reply with ok"}]}]}`)

	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return false, false, "probe_build_request_error", err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	if userAgent := strings.TrimSpace(account.GetOpenAIUserAgent()); userAgent != "" {
		req.Header.Set("user-agent", userAgent)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return false, false, "probe_network_error", err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, readErr := readUpstreamResponseBodyLimited(resp.Body, openAIHTTPStreamingCapabilityProbeReadMaxBytes)
	if readErr != nil {
		return false, false, "probe_read_error", readErr
	}
	return classifyOpenAIHTTPStreamingProbeResponse(resp, respBody)
}

func classifyOpenAIHTTPStreamingProbeResponse(
	resp *http.Response,
	body []byte,
) (supported bool, known bool, source string, err error) {
	if resp == nil {
		return false, false, "probe_inconclusive", nil
	}
	statusCode := resp.StatusCode
	msg := strings.ToLower(strings.TrimSpace(ExtractUpstreamErrorMessage(body)))
	if status, _, _, ok := ResolveOpenAIHTMLUpstreamError(statusCode, resp.Header, body, "", ""); ok {
		if status == http.StatusServiceUnavailable {
			return false, true, "probe_html_challenge", nil
		}
		return false, true, "probe_html_error_page", nil
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if strings.Contains(contentType, "text/event-stream") {
		return classifyOpenAIHTTPStreamingSSEProbeBody(body)
	}

	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone, http.StatusNotImplemented, http.StatusUpgradeRequired:
		return false, true, "probe_http_" + strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")), nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return false, false, "probe_http_" + strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")), nil
	}

	if msg != "" {
		if strings.Contains(msg, "unsupported") && strings.Contains(msg, "stream") {
			return false, true, "probe_stream_unsupported", nil
		}
		if strings.Contains(msg, "text/event-stream") || strings.Contains(msg, "event stream") {
			return false, true, "probe_stream_unsupported", nil
		}
	}

	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return false, true, "probe_non_streaming_success", nil
	}
	if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
		return false, false, "probe_http_rejected", nil
	}
	return false, false, "probe_inconclusive", nil
}

func classifyOpenAIHTTPStreamingSSEProbeBody(body []byte) (supported bool, known bool, source string, err error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false, true, "probe_stream_empty", nil
	}
	if strings.Contains(trimmed, "[DONE]") {
		return true, true, "probe_stream_done_supported", nil
	}
	if strings.Contains(trimmed, `"response.completed"`) ||
		strings.Contains(trimmed, `"response.done"`) ||
		strings.Contains(trimmed, `"response.failed"`) {
		return false, true, "probe_stream_missing_done", nil
	}
	return false, true, "probe_stream_missing_done", nil
}

func openAIHTTPStreamingCapabilityProbeFlightKey(account *Account, requestedModel string) string {
	if account == nil {
		return "openai_http_streaming_probe:0"
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		requestedModel = "gpt-5.4"
	}
	return "openai_http_streaming_probe:" + requestedModel + ":" + strings.TrimSpace(account.GetOpenAIBaseURL()) + ":" + string(account.Type) + ":" + strings.TrimSpace(account.Name)
}
