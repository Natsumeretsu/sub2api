package service

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"
)

const (
	openAICompactCapabilityProbeTTL          = 6 * time.Hour
	openAICompactCapabilityProbeTimeout      = 8 * time.Second
	openAICompactCapabilityProbeReadMaxBytes = 64 << 10
	openAICompactProbePreviousResponseID     = "resp_compact_capability_probe"
)

type openAICompactCapabilityObservation struct {
	Supported bool
	Source    string
	CheckedAt time.Time
	ExpiresAt time.Time
}

func (s *OpenAIGatewayService) ResolveOpenAIResponsesCompactSupport(
	ctx context.Context,
	account *Account,
	requestedModel string,
) (supported bool, known bool, source string, err error) {
	supported, known, source = resolveOpenAIResponsesCompactStaticCapability(account)
	if known {
		return supported, true, source, nil
	}
	if observed, ok := s.getObservedOpenAIResponsesCompactCapability(account); ok {
		return observed.Supported, true, observed.Source, nil
	}
	supported, known, source, err = s.probeOpenAIResponsesCompactSupport(ctx, account, requestedModel)
	if known {
		s.setObservedOpenAIResponsesCompactCapability(account, supported, source)
	}
	return supported, known, source, err
}

func (s *OpenAIGatewayService) SupportsOpenAIResponsesCompactForRuntime(account *Account) bool {
	supported, known, _ := resolveOpenAIResponsesCompactStaticCapability(account)
	if known {
		return supported
	}
	if observed, ok := s.getObservedOpenAIResponsesCompactCapability(account); ok {
		return observed.Supported
	}
	return false
}

func resolveOpenAIResponsesCompactStaticCapability(account *Account) (supported bool, known bool, source string) {
	if account == nil {
		return false, false, ""
	}
	return account.ResolveOpenAIResponsesCompactCapability()
}

func (s *OpenAIGatewayService) getObservedOpenAIResponsesCompactCapability(account *Account) (openAICompactCapabilityObservation, bool) {
	if s == nil || account == nil || account.ID <= 0 {
		return openAICompactCapabilityObservation{}, false
	}
	raw, ok := s.openaiCompactCapability.Load(account.ID)
	if !ok {
		return openAICompactCapabilityObservation{}, false
	}
	observation, ok := raw.(openAICompactCapabilityObservation)
	if !ok {
		s.openaiCompactCapability.Delete(account.ID)
		return openAICompactCapabilityObservation{}, false
	}
	if !observation.ExpiresAt.IsZero() && time.Now().After(observation.ExpiresAt) {
		s.openaiCompactCapability.Delete(account.ID)
		return openAICompactCapabilityObservation{}, false
	}
	return observation, true
}

func (s *OpenAIGatewayService) setObservedOpenAIResponsesCompactCapability(account *Account, supported bool, source string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	now := time.Now()
	s.openaiCompactCapability.Store(account.ID, openAICompactCapabilityObservation{
		Supported: supported,
		Source:    strings.TrimSpace(source),
		CheckedAt: now,
		ExpiresAt: now.Add(openAICompactCapabilityProbeTTL),
	})
}

func (s *OpenAIGatewayService) SetObservedOpenAIResponsesCompactCapability(account *Account, supported bool, source string) {
	s.setObservedOpenAIResponsesCompactCapability(account, supported, source)
}

func (s *OpenAIGatewayService) probeOpenAIResponsesCompactSupport(
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
		probeCtx, cancel = context.WithTimeout(probeCtx, openAICompactCapabilityProbeTimeout)
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
	targetURL := appendOpenAIResponsesRequestPathSuffix(buildOpenAIResponsesURL(validatedURL), "/compact")

	model := strings.TrimSpace(requestedModel)
	if model == "" {
		model = "gpt-5.4"
	}
	body := []byte(`{"model":"` + model + `","instructions":"compact capability probe","input":[{"role":"user","content":[{"type":"input_text","text":"compact capability probe"}]}],"previous_response_id":"` + openAICompactProbePreviousResponseID + `"}`)

	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return false, false, "probe_build_request_error", err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
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

	respBody, readErr := readUpstreamResponseBodyLimited(resp.Body, openAICompactCapabilityProbeReadMaxBytes)
	if readErr != nil {
		return false, false, "probe_read_error", readErr
	}
	supported, known, source = classifyOpenAIResponsesCompactProbeResponse(resp.StatusCode, respBody)
	return supported, known, source, nil
}

func classifyOpenAIResponsesCompactProbeResponse(statusCode int, body []byte) (supported bool, known bool, source string) {
	msg := strings.ToLower(strings.TrimSpace(ExtractUpstreamErrorMessage(body)))

	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone, http.StatusNotImplemented:
		return false, true, "probe_http_" + http.StatusText(statusCode)
	}

	if msg != "" {
		if strings.Contains(msg, "unsupported parameter") && strings.Contains(msg, "previous_response_id") {
			return false, true, "probe_unsupported_previous_response_id"
		}
		if strings.Contains(msg, "previous_response_not_found") ||
			strings.Contains(msg, "previous response with id") ||
			strings.Contains(msg, "not found.") && strings.Contains(msg, "previous response") {
			return true, true, "probe_previous_response_supported"
		}
		if strings.Contains(msg, "not found") && strings.Contains(msg, "compact") {
			return false, true, "probe_route_not_found"
		}
		if strings.Contains(msg, "unsupported") && strings.Contains(msg, "compact") {
			return false, true, "probe_compact_unsupported"
		}
	}

	if statusCode >= http.StatusOK && statusCode < http.StatusBadRequest {
		return true, true, "probe_http_success"
	}
	if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
		// Generic 4xx only proves the provider rejected the anchored compact probe;
		// it is not strong enough evidence that previous_response-aware compact is usable.
		return false, true, "probe_http_rejected"
	}
	return false, false, "probe_inconclusive"
}
