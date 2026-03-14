package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	openAIWSTransportCapabilityProbeTTL     = 6 * time.Hour
	openAIWSTransportCapabilityProbeTimeout = 8 * time.Second
)

type openAIWSTransportCapabilityObservation struct {
	Supported bool
	Source    string
	CheckedAt time.Time
	ExpiresAt time.Time
}

func (s *OpenAIGatewayService) ResolveOpenAIResponsesWebSocketTransportSupport(
	ctx context.Context,
	account *Account,
	requestedModel string,
) (supported bool, known bool, source string, err error) {
	supported, known, source = resolveOpenAIResponsesWebSocketTransportStaticCapability(account)
	if known {
		return supported, true, source, nil
	}
	if observed, ok := s.getObservedOpenAIResponsesWebSocketTransportCapability(account); ok {
		return observed.Supported, true, observed.Source, nil
	}
	supported, known, source, err = s.probeOpenAIResponsesWebSocketTransportSupport(ctx, account, requestedModel)
	if known {
		s.setObservedOpenAIResponsesWebSocketTransportCapability(account, supported, source)
	}
	return supported, known, source, err
}

func (s *OpenAIGatewayService) SupportsOpenAIResponsesWebSocketTransportForRuntime(account *Account) bool {
	supported, known, _ := resolveOpenAIResponsesWebSocketTransportStaticCapability(account)
	if known {
		return supported
	}
	if observed, ok := s.getObservedOpenAIResponsesWebSocketTransportCapability(account); ok {
		return observed.Supported
	}
	return false
}

func resolveOpenAIResponsesWebSocketTransportStaticCapability(account *Account) (supported bool, known bool, source string) {
	if account == nil {
		return false, false, ""
	}
	return account.ResolveOpenAIResponsesWebSocketTransportCapability()
}

func (s *OpenAIGatewayService) getObservedOpenAIResponsesWebSocketTransportCapability(account *Account) (openAIWSTransportCapabilityObservation, bool) {
	if s == nil || account == nil || account.ID <= 0 {
		return openAIWSTransportCapabilityObservation{}, false
	}
	raw, ok := s.openaiWSTransportCapability.Load(account.ID)
	if !ok {
		return openAIWSTransportCapabilityObservation{}, false
	}
	observation, ok := raw.(openAIWSTransportCapabilityObservation)
	if !ok {
		s.openaiWSTransportCapability.Delete(account.ID)
		return openAIWSTransportCapabilityObservation{}, false
	}
	if !observation.ExpiresAt.IsZero() && time.Now().After(observation.ExpiresAt) {
		s.openaiWSTransportCapability.Delete(account.ID)
		return openAIWSTransportCapabilityObservation{}, false
	}
	return observation, true
}

func (s *OpenAIGatewayService) setObservedOpenAIResponsesWebSocketTransportCapability(account *Account, supported bool, source string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	now := time.Now()
	s.openaiWSTransportCapability.Store(account.ID, openAIWSTransportCapabilityObservation{
		Supported: supported,
		Source:    strings.TrimSpace(source),
		CheckedAt: now,
		ExpiresAt: now.Add(openAIWSTransportCapabilityProbeTTL),
	})
}

func (s *OpenAIGatewayService) probeOpenAIResponsesWebSocketTransportSupport(
	ctx context.Context,
	account *Account,
	requestedModel string,
) (supported bool, known bool, source string, err error) {
	if s == nil || account == nil || !isOpenAIResponsesWebSocketTransportProbeEligible(account) {
		return false, false, "", nil
	}

	probeCtx := ctx
	if probeCtx == nil {
		probeCtx = context.Background()
	}
	if _, hasDeadline := probeCtx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(probeCtx, openAIWSTransportCapabilityProbeTimeout)
		defer cancel()
	}

	token, _, err := s.GetAccessToken(probeCtx, account)
	if err != nil {
		return false, false, "probe_access_token_error", err
	}

	wsURL, err := s.buildOpenAIResponsesWSURL(account)
	if err != nil {
		return false, false, "probe_invalid_ws_url", err
	}

	headers, _ := s.buildOpenAIWSHeaders(
		nil,
		account,
		token,
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
		false,
		"",
		"",
		normalizeOpenAIWSTransportProbePromptCacheKey(requestedModel),
	)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	dialer := s.getOpenAIWSPassthroughDialer()
	if dialer == nil {
		return false, false, "probe_unavailable", nil
	}
	conn, statusCode, _, err := dialer.Dial(probeCtx, wsURL, headers, proxyURL)
	if err != nil {
		return classifyOpenAIResponsesWebSocketTransportProbeDialError(statusCode, err)
	}
	if conn == nil {
		return false, false, "probe_inconclusive", nil
	}
	_ = conn.Close()
	return true, true, "probe_ws_handshake_supported", nil
}

func classifyOpenAIResponsesWebSocketTransportProbeDialError(statusCode int, err error) (supported bool, known bool, source string, probeErr error) {
	if err == nil {
		return false, false, "", nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, false, "probe_timeout", err
	}

	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone, http.StatusNotImplemented, http.StatusUpgradeRequired:
		return false, true, openAIWSTransportProbeHTTPStatusSource(statusCode), nil
	case http.StatusTooManyRequests:
		return false, false, "probe_rate_limited", nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, false, "probe_auth_failed", nil
	}

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "upgrade required"), strings.Contains(msg, "status=426"):
		return false, true, "probe_upgrade_required", nil
	case strings.Contains(msg, "websocket") && strings.Contains(msg, "unsupported"):
		return false, true, "probe_ws_unsupported", nil
	case strings.Contains(msg, "handshake rejected"):
		return false, true, "probe_handshake_rejected", nil
	}
	if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
		return false, true, openAIWSTransportProbeHTTPStatusSource(statusCode), nil
	}
	return false, false, "probe_inconclusive", nil
}

func openAIWSTransportProbeHTTPStatusSource(statusCode int) string {
	statusText := strings.ToLower(strings.TrimSpace(http.StatusText(statusCode)))
	statusText = strings.ReplaceAll(statusText, " ", "_")
	return "probe_http_" + statusText
}

func isOpenAIResponsesWebSocketTransportProbeEligible(account *Account) bool {
	if account == nil || !account.IsOpenAI() || !account.IsOpenAIApiKey() {
		return false
	}
	if account.IsOpenAIWSForceHTTPEnabled() {
		return false
	}
	if !account.IsOpenAIResponsesWebSocketV2Enabled() {
		return false
	}
	return account.ResolveOpenAIResponsesWebSocketV2Mode(OpenAIWSIngressModeCtxPool) != OpenAIWSIngressModeOff
}

func normalizeOpenAIWSTransportProbePromptCacheKey(requestedModel string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		requestedModel = "gpt-5.4"
	}
	return "ws-transport-probe:" + requestedModel
}

func (s *OpenAIGatewayService) resolveOpenAIWSProtocolDecisionForRuntime(account *Account) OpenAIWSProtocolDecision {
	decision := s.getOpenAIWSProtocolResolver().Resolve(account)
	if account == nil || !account.IsOpenAIApiKey() {
		return decision
	}
	if decision.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		return decision
	}
	if strings.TrimSpace(decision.Reason) != "apikey_ws_transport_unverified" {
		return decision
	}
	if s.SupportsOpenAIResponsesWebSocketTransportForRuntime(account) {
		return OpenAIWSProtocolDecision{
			Transport: OpenAIUpstreamTransportResponsesWebsocketV2,
			Reason:    "ws_v2_observed_supported",
		}
	}
	return decision
}

func (s *OpenAIGatewayService) resolveOpenAIWSProtocolDecision(
	ctx context.Context,
	account *Account,
	requestedModel string,
) OpenAIWSProtocolDecision {
	decision := s.resolveOpenAIWSProtocolDecisionForRuntime(account)
	if account == nil || !account.IsOpenAIApiKey() {
		return decision
	}
	if decision.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		return decision
	}
	if strings.TrimSpace(decision.Reason) != "apikey_ws_transport_unverified" {
		return decision
	}

	supported, known, source, err := s.ResolveOpenAIResponsesWebSocketTransportSupport(ctx, account, requestedModel)
	if err != nil {
		logOpenAIWSModeInfo(
			"transport_capability_probe_error account_id=%d account_name=%s err=%s",
			account.ID,
			normalizeOpenAIWSLogValue(account.Name),
			truncateOpenAIWSLogValue(err.Error(), openAIWSLogValueMaxLen),
		)
	}
	if supported {
		return OpenAIWSProtocolDecision{
			Transport: OpenAIUpstreamTransportResponsesWebsocketV2,
			Reason:    "ws_v2_probe_" + strings.TrimSpace(source),
		}
	}
	if known {
		return openAIWSHTTPDecision("apikey_ws_transport_" + strings.TrimSpace(source))
	}
	return decision
}
