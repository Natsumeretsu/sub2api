package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/soraerror"
)

func OpenAIHTTPPreviousResponseUnsupportedMessage() string {
	return "Strong continuation is temporarily unavailable on the selected HTTP fallback surface; retry later to preserve session continuity and cache affinity"
}

func ResolveOpenAIHTMLUpstreamError(
	upstreamStatus int,
	headers http.Header,
	body []byte,
	upstreamMessage,
	upstreamDetail string,
) (status int, errType string, message string, ok bool) {
	if status, errType, message, ok := resolveOpenAIHTMLUpstreamPreviewError(
		upstreamStatus,
		strings.TrimSpace(string(body)),
		headers,
		body,
	); ok {
		return status, errType, message, true
	}
	return resolveOpenAIHTMLUpstreamPreviewError(
		upstreamStatus,
		strings.TrimSpace(upstreamMessage+"\n"+upstreamDetail),
		headers,
		body,
	)
}

func ResolveOpenAIBodyAwareUpstreamError(
	upstreamStatus int,
	headers http.Header,
	body []byte,
	upstreamMessage,
	upstreamDetail string,
) (status int, errType string, message string, ok bool) {
	if status, errType, message, ok := ResolveOpenAIHTMLUpstreamError(
		upstreamStatus,
		headers,
		body,
		upstreamMessage,
		upstreamDetail,
	); ok {
		return status, errType, message, true
	}
	return ResolveOpenAIStructuredUpstreamError(upstreamStatus, upstreamMessage, upstreamDetail)
}

func ResolveOpenAIStructuredUpstreamError(upstreamStatus int, upstreamMessage, upstreamDetail string) (status int, errType string, message string, ok bool) {
	upstreamMessage = strings.TrimSpace(upstreamMessage)
	upstreamDetail = strings.TrimSpace(upstreamDetail)
	if upstreamStatus <= 0 && upstreamMessage == "" && upstreamDetail == "" {
		return 0, "", "", false
	}
	combined := strings.ToLower(strings.TrimSpace(upstreamMessage + "\n" + upstreamDetail))
	if status, errType, message, ok := resolveOpenAIHTMLUpstreamPreviewError(upstreamStatus, combined, nil, nil); ok {
		return status, errType, message, true
	}
	if strings.Contains(combined, "unsupported parameter: previous_response_id") {
		return http.StatusServiceUnavailable, "api_error", OpenAIHTTPPreviousResponseUnsupportedMessage(), true
	}
	switch upstreamStatus {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		errType = "invalid_request_error"
	case http.StatusNotFound:
		errType = "not_found_error"
	case http.StatusConflict:
		errType = "conflict_error"
	case http.StatusTooManyRequests:
		errType = "rate_limit_error"
	default:
		if upstreamStatus <= 0 {
			if upstreamMessage == "" {
				return 0, "", "", false
			}
			return http.StatusBadGateway, "upstream_error", upstreamMessage, true
		}
		return 0, "", "", false
	}
	if upstreamMessage == "" {
		upstreamMessage = "Upstream request failed"
	}
	return upstreamStatus, errType, upstreamMessage, true
}

func resolveOpenAIHTMLUpstreamPreviewError(
	upstreamStatus int,
	preview string,
	headers http.Header,
	body []byte,
) (status int, errType string, message string, ok bool) {
	preview = strings.ToLower(strings.TrimSpace(preview))
	if !looksLikeOpenAIHTMLPreview(preview, headers) {
		return 0, "", "", false
	}
	if looksLikeOpenAICloudflareChallengePreview(preview, headers, body) {
		base := fmt.Sprintf(
			"Upstream request hit a Cloudflare challenge page (HTTP %d); retry later or switch to a clean account or network",
			normalizedOpenAIHTMLUpstreamStatus(upstreamStatus),
		)
		return http.StatusServiceUnavailable, "api_error", soraerror.FormatCloudflareChallengeMessage(base, headers, body), true
	}
	return http.StatusBadGateway,
		"upstream_error",
		fmt.Sprintf(
			"Upstream returned an unexpected HTML error page (HTTP %d), not a structured OpenAI error response",
			normalizedOpenAIHTMLUpstreamStatus(upstreamStatus),
		),
		true
}

func looksLikeOpenAIHTMLPreview(preview string, headers http.Header) bool {
	if headers != nil {
		contentType := strings.ToLower(strings.TrimSpace(headers.Get("Content-Type")))
		if strings.Contains(contentType, "text/html") {
			return true
		}
	}
	if preview == "" {
		return false
	}
	if strings.Contains(preview, "<!doctype html") || strings.Contains(preview, "<html") {
		return true
	}
	if strings.Contains(preview, "http status 400") && strings.Contains(preview, "bad request") {
		return true
	}
	return false
}

func looksLikeOpenAICloudflareChallengePreview(preview string, headers http.Header, body []byte) bool {
	if headers != nil && strings.EqualFold(strings.TrimSpace(headers.Get("cf-mitigated")), "challenge") {
		return true
	}
	if soraerror.IsCloudflareChallengeResponse(upstreamStatusForChallengePreview(headers, preview), headers, body) {
		return true
	}
	if preview == "" {
		return false
	}
	for _, marker := range []string{
		"window._cf_chl_opt",
		"challenge-platform",
		"cdn-cgi",
		"enable javascript and cookies to continue",
		"just a moment",
	} {
		if strings.Contains(preview, marker) {
			return true
		}
	}
	return false
}

func upstreamStatusForChallengePreview(headers http.Header, preview string) int {
	if headers != nil && strings.EqualFold(strings.TrimSpace(headers.Get("cf-mitigated")), "challenge") {
		return http.StatusForbidden
	}
	if strings.Contains(preview, "challenge-platform") || strings.Contains(preview, "window._cf_chl_opt") {
		return http.StatusForbidden
	}
	return 0
}

func normalizedOpenAIHTMLUpstreamStatus(status int) int {
	if status > 0 {
		return status
	}
	return http.StatusBadGateway
}
