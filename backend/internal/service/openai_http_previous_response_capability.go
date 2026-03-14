package service

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"
)

const (
	openAIHTTPPreviousResponseProbeTTL          = 6 * time.Hour
	openAIHTTPPreviousResponseProbeTimeout      = 8 * time.Second
	openAIHTTPPreviousResponseProbeReadMaxBytes = 64 << 10
	openAIHTTPProbePreviousResponseID           = "resp_http_previous_response_probe"
)

type openAIHTTPPreviousResponseCapabilityObservation struct {
	Supported bool
	Source    string
	CheckedAt time.Time
	ExpiresAt time.Time
}

func (s *OpenAIGatewayService) ResolveOpenAIHTTPPreviousResponseSupport(
	ctx context.Context,
	account *Account,
	requestedModel string,
) (supported bool, known bool, source string, err error) {
	supported, known, source = resolveOpenAIHTTPPreviousResponseStaticCapability(account)
	if known {
		return supported, true, source, nil
	}
	if observed, ok := s.getObservedOpenAIHTTPPreviousResponseCapability(account); ok {
		return observed.Supported, true, observed.Source, nil
	}
	supported, known, source, err = s.probeOpenAIHTTPPreviousResponseSupport(ctx, account, requestedModel)
	if known {
		s.setObservedOpenAIHTTPPreviousResponseCapability(account, supported, source)
	}
	return supported, known, source, err
}

func resolveOpenAIHTTPPreviousResponseStaticCapability(account *Account) (supported bool, known bool, source string) {
	if account == nil || !account.IsOpenAI() {
		return false, false, ""
	}
	for _, key := range account.openAIHTTPPreviousResponseCapabilityKeys() {
		if enabled, ok := account.getBoolCredential(key); ok {
			return enabled, true, "explicit_credential"
		}
		if enabled, ok := account.getBoolExtra(key); ok {
			return enabled, true, "explicit_extra"
		}
	}
	if account.IsOpenAIOAuth() {
		if account.IsOpenAIPassthroughEnabled() {
			return false, true, "oauth_passthrough_default_unsupported"
		}
		return true, true, "oauth_default_supported"
	}
	if account.IsOpenAIApiKey() {
		if account.isOfficialOpenAIBaseURL() {
			return true, true, "official_apikey_default_supported"
		}
		return false, false, ""
	}
	return false, false, ""
}

func (s *OpenAIGatewayService) getObservedOpenAIHTTPPreviousResponseCapability(account *Account) (openAIHTTPPreviousResponseCapabilityObservation, bool) {
	if s == nil || account == nil || account.ID <= 0 {
		return openAIHTTPPreviousResponseCapabilityObservation{}, false
	}
	raw, ok := s.openaiHTTPPrevCapability.Load(account.ID)
	if !ok {
		return openAIHTTPPreviousResponseCapabilityObservation{}, false
	}
	observation, ok := raw.(openAIHTTPPreviousResponseCapabilityObservation)
	if !ok {
		s.openaiHTTPPrevCapability.Delete(account.ID)
		return openAIHTTPPreviousResponseCapabilityObservation{}, false
	}
	if !observation.ExpiresAt.IsZero() && time.Now().After(observation.ExpiresAt) {
		s.openaiHTTPPrevCapability.Delete(account.ID)
		return openAIHTTPPreviousResponseCapabilityObservation{}, false
	}
	return observation, true
}

func (s *OpenAIGatewayService) setObservedOpenAIHTTPPreviousResponseCapability(account *Account, supported bool, source string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	now := time.Now()
	s.openaiHTTPPrevCapability.Store(account.ID, openAIHTTPPreviousResponseCapabilityObservation{
		Supported: supported,
		Source:    strings.TrimSpace(source),
		CheckedAt: now,
		ExpiresAt: now.Add(openAIHTTPPreviousResponseProbeTTL),
	})
}

func (s *OpenAIGatewayService) probeOpenAIHTTPPreviousResponseSupport(
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
		probeCtx, cancel = context.WithTimeout(probeCtx, openAIHTTPPreviousResponseProbeTimeout)
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
	body := []byte(`{"model":"` + model + `","stream":false,"input":[{"role":"user","content":[{"type":"input_text","text":"http previous_response capability probe"}]}],"previous_response_id":"` + openAIHTTPProbePreviousResponseID + `"}`)

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

	respBody, readErr := readUpstreamResponseBodyLimited(resp.Body, openAIHTTPPreviousResponseProbeReadMaxBytes)
	if readErr != nil {
		return false, false, "probe_read_error", readErr
	}
	return classifyOpenAIHTTPPreviousResponseProbeResponse(resp.StatusCode, respBody)
}

func classifyOpenAIHTTPPreviousResponseProbeResponse(statusCode int, body []byte) (supported bool, known bool, source string, err error) {
	msg := strings.ToLower(strings.TrimSpace(ExtractUpstreamErrorMessage(body)))

	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone, http.StatusNotImplemented:
		return false, true, "probe_http_" + http.StatusText(statusCode), nil
	}

	if msg != "" {
		if strings.Contains(msg, "unsupported parameter") && strings.Contains(msg, "previous_response_id") {
			return false, true, "probe_unsupported_previous_response_id", nil
		}
		if strings.Contains(msg, "previous_response_not_found") ||
			(strings.Contains(msg, "previous response with id") && strings.Contains(msg, "not found")) {
			return true, true, "probe_previous_response_supported", nil
		}
	}

	if statusCode > 0 && statusCode < http.StatusInternalServerError {
		return true, true, "probe_http_response", nil
	}
	return false, false, "probe_inconclusive", nil
}
