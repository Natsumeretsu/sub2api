package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

type openAIHTTPStreamBridgeDecision struct {
	UseBridge           bool
	CapabilityKnown     bool
	CapabilitySupported bool
	Source              string
}

func canUseOpenAIHTTPNonStreamingBridge(requiredTransport OpenAIUpstreamTransport, streamRequested bool) bool {
	if !streamRequested {
		return false
	}
	switch requiredTransport {
	case OpenAIUpstreamTransportResponsesWebsocketV2, OpenAIUpstreamTransportResponsesWebsocket:
		return false
	default:
		return true
	}
}

func (s *OpenAIGatewayService) resolveOpenAIHTTPStreamBridgeDecision(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	requestedModel string,
	clientRequestedStream bool,
	requiredTransport OpenAIUpstreamTransport,
) openAIHTTPStreamBridgeDecision {
	decision := openAIHTTPStreamBridgeDecision{}
	if s == nil || account == nil || !canUseOpenAIHTTPNonStreamingBridge(requiredTransport, clientRequestedStream) {
		return decision
	}
	if c == nil || GetOpenAIClientTransport(c) != OpenAIClientTransportHTTP || isOpenAIRemoteCompactRequest(c) {
		return decision
	}

	supported, known, source, err := s.ResolveOpenAIHTTPStreamingSupportForRequest(ctx, account, requestedModel)
	if err != nil || !known || supported {
		decision.CapabilityKnown = known
		decision.CapabilitySupported = supported
		decision.Source = source
		return decision
	}

	decision.UseBridge = true
	decision.CapabilityKnown = true
	decision.CapabilitySupported = false
	decision.Source = source
	return decision
}

func setOpenAIRequestStreamValue(body []byte, stream bool) ([]byte, bool, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || !gjson.ValidBytes(trimmed) {
		return body, false, nil
	}
	current := gjson.GetBytes(trimmed, "stream")
	if current.Exists() && ((current.Type == gjson.True && stream) || (current.Type == gjson.False && !stream)) {
		return body, false, nil
	}
	next, err := sjson.SetBytes(trimmed, "stream", stream)
	if err != nil {
		return body, false, err
	}
	return next, true, nil
}

func setOpenAIUpstreamAcceptHeader(req *http.Request, isStream bool) {
	if req == nil {
		return
	}
	if isStream {
		if strings.TrimSpace(req.Header.Get("accept")) == "" {
			req.Header.Set("accept", "text/event-stream")
		}
		return
	}
	req.Header.Set("accept", "application/json")
}

func openAIResponseTerminalEventType(body []byte) string {
	status := strings.TrimSpace(strings.ToLower(gjson.GetBytes(body, "status").String()))
	switch status {
	case "failed":
		return "response.failed"
	case "incomplete":
		return "response.incomplete"
	default:
		if openAIResponseHasMeaningfulError(body) {
			return "response.failed"
		}
		return "response.completed"
	}
}

func openAIResponseHasMeaningfulError(body []byte) bool {
	errorValue := gjson.GetBytes(body, "error")
	if !errorValue.Exists() {
		return false
	}
	if errorValue.Type == gjson.Null {
		return false
	}
	raw := strings.TrimSpace(errorValue.Raw)
	return raw != "" && raw != "null"
}

func buildOpenAISyntheticStreamingTerminalEvent(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	eventType := openAIResponseTerminalEventType(trimmed)
	payload := make([]byte, 0, len(trimmed)+64)
	payload = append(payload, `{"type":"`...)
	payload = append(payload, eventType...)
	payload = append(payload, `","response":`...)
	payload = append(payload, trimmed...)
	payload = append(payload, '}')
	return payload
}

func writeOpenAISyntheticStreamingHeaders(
	filter *responseheaders.CompiledHeaderFilter,
	c *gin.Context,
	resp *http.Response,
) error {
	if c == nil || c.Writer == nil {
		return errors.New("streaming bridge context is nil")
	}
	if filter != nil && resp != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, filter)
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if resp != nil {
		if v := resp.Header.Get("x-request-id"); v != "" {
			c.Header("x-request-id", v)
		}
	}
	return nil
}

func (s *OpenAIGatewayService) handleStreamingResponseBridge(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	startTime time.Time,
	originalModel string,
	mappedModel string,
	passthrough bool,
) (*openaiStreamingResult, error) {
	if resp == nil {
		return nil, errors.New("upstream bridge response is nil")
	}
	maxBytes := resolveUpstreamResponseReadLimit(s.cfg)
	body, err := readUpstreamResponseBodyLimited(resp.Body, maxBytes)
	if err != nil {
		if errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			return nil, buildOpenAIStructuredProtocolFailover(resp, http.StatusBadGateway, "upstream_error", "Upstream response too large", nil, true)
		}
		return nil, err
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))

	bodyLooksLikeSSE := bytes.Contains(body, []byte("data:")) || bytes.Contains(body, []byte("event:"))
	if isEventStreamResponse(resp.Header) || bodyLooksLikeSSE {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if passthrough {
			result, err := s.handleStreamingResponsePassthrough(ctx, resp, c, account, startTime)
			if err != nil {
				return nil, err
			}
			return &openaiStreamingResult{usage: result.usage, firstTokenMs: result.firstTokenMs}, nil
		}
		return s.handleStreamingResponse(ctx, resp, c, account, startTime, originalModel, mappedModel)
	}

	if msg, invalid := openAINonStreamingJSONProtocolMessage(c, body); invalid {
		return nil, buildOpenAINonStreamingProtocolFailover(resp, msg)
	}

	responseBody := bytes.TrimSpace(body)
	if !passthrough {
		if originalModel != mappedModel {
			responseBody = s.replaceModelInResponseBody(responseBody, mappedModel, originalModel)
		}
		responseBody = s.correctToolCallsInResponseBody(responseBody)
	}

	if err := writeOpenAISyntheticStreamingHeaders(s.responseHeaderFilter, c, resp); err != nil {
		return nil, err
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}
	bufferedWriter := bufio.NewWriterSize(c.Writer, 4*1024)
	flushBuffered := func() error {
		if err := bufferedWriter.Flush(); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	usage := &OpenAIUsage{}
	if parsedUsage, parsed := extractOpenAIUsageFromJSONBytes(responseBody); parsed {
		*usage = parsedUsage
	}
	terminalEvent := buildOpenAISyntheticStreamingTerminalEvent(responseBody)
	firstTokenMsValue := int(time.Since(startTime).Milliseconds())
	firstTokenMs := &firstTokenMsValue

	frames := [][]byte{
		terminalEvent,
		[]byte("[DONE]"),
	}
	for _, frame := range frames {
		if _, err := bufferedWriter.WriteString("data: "); err != nil {
			return nil, err
		}
		if _, err := bufferedWriter.Write(frame); err != nil {
			return nil, err
		}
		if _, err := bufferedWriter.WriteString("\n\n"); err != nil {
			return nil, err
		}
	}
	if err := flushBuffered(); err != nil {
		return nil, err
	}

	if s != nil {
		logger.FromContext(ctx).With(
			zap.String("component", "service.openai_gateway"),
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.String("terminal_event_type", openAIResponseTerminalEventType(responseBody)),
			zap.Bool("passthrough", passthrough),
		).Info("openai.http_stream_bridge_synthesized_terminal")
	}

	return &openaiStreamingResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}
