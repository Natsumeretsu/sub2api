package service

import (
	"net/http"
	"strings"
)

func OpenAIHTTPPreviousResponseUnsupportedMessage() string {
	return "Strong continuation is temporarily unavailable on the selected HTTP fallback surface; retry later to preserve session continuity and cache affinity"
}

func ResolveOpenAIStructuredUpstreamError(upstreamStatus int, upstreamMessage, upstreamDetail string) (status int, errType string, message string, ok bool) {
	upstreamMessage = strings.TrimSpace(upstreamMessage)
	upstreamDetail = strings.TrimSpace(upstreamDetail)
	if upstreamStatus <= 0 && upstreamMessage == "" && upstreamDetail == "" {
		return 0, "", "", false
	}
	combined := strings.ToLower(strings.TrimSpace(upstreamMessage + "\n" + upstreamDetail))
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
