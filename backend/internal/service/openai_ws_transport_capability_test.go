//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type openAIWSTransportProbeConnStub struct {
	closed bool
}

func (c *openAIWSTransportProbeConnStub) WriteJSON(ctx context.Context, value any) error {
	_ = ctx
	_ = value
	return nil
}

func (c *openAIWSTransportProbeConnStub) ReadMessage(ctx context.Context) ([]byte, error) {
	_ = ctx
	return nil, errors.New("not implemented")
}

func (c *openAIWSTransportProbeConnStub) Ping(ctx context.Context) error {
	_ = ctx
	return nil
}

func (c *openAIWSTransportProbeConnStub) Close() error {
	c.closed = true
	return nil
}

type openAIWSTransportProbeDialerStub struct {
	conn        openAIWSClientConn
	err         error
	statusCode  int
	handshake   http.Header
	dialCount   int
	lastWSURL   string
	lastHeaders http.Header
}

func (d *openAIWSTransportProbeDialerStub) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
) (openAIWSClientConn, int, http.Header, error) {
	_ = ctx
	_ = proxyURL
	d.dialCount++
	d.lastWSURL = wsURL
	d.lastHeaders = cloneHeader(headers)
	return d.conn, d.statusCode, cloneHeader(d.handshake), d.err
}

func TestResolveOpenAIResponsesWebSocketTransportSupport_ProbeConfirmsHandshakeCapability(t *testing.T) {
	account := &Account{
		ID:          16,
		Name:        "RightCode",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://right.codes/codex/v1",
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    OpenAIWSIngressModePassthrough,
		},
	}
	dialer := &openAIWSTransportProbeDialerStub{conn: &openAIWSTransportProbeConnStub{}}
	svc := &OpenAIGatewayService{
		cfg:                       newOpenAIWSV2TestConfig(),
		openaiWSPassthroughDialer: dialer,
	}

	supported, known, source, err := svc.ResolveOpenAIResponsesWebSocketTransportSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_ws_handshake_supported", source)
	require.Equal(t, 1, dialer.dialCount)
	require.Equal(t, "wss://right.codes/codex/v1/responses", dialer.lastWSURL)
	require.Equal(t, "Bearer sk-test", dialer.lastHeaders.Get("authorization"))
	require.Equal(t, openAIWSBetaV2Value, dialer.lastHeaders.Get("OpenAI-Beta"))

	supported, known, source, err = svc.ResolveOpenAIResponsesWebSocketTransportSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_ws_handshake_supported", source)
	require.Equal(t, 1, dialer.dialCount, "second lookup should hit observed cache")
}

func TestResolveOpenAIResponsesWebSocketTransportSupport_ProbeRejectsHandshake(t *testing.T) {
	account := &Account{
		ID:          17,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.invalid/v1",
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}
	dialer := &openAIWSTransportProbeDialerStub{
		err:        errors.New("bad handshake"),
		statusCode: http.StatusMethodNotAllowed,
	}
	svc := &OpenAIGatewayService{
		cfg:                       newOpenAIWSV2TestConfig(),
		openaiWSPassthroughDialer: dialer,
	}

	supported, known, source, err := svc.ResolveOpenAIResponsesWebSocketTransportSupport(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.False(t, supported)
	require.True(t, known)
	require.Equal(t, "probe_http_method_not_allowed", source)
}

func TestOpenAIGatewayService_ResolveOpenAIWSProtocolDecision_DoesNotProbeUnverifiedAPIKeyOnRequestPath(t *testing.T) {
	account := Account{
		ID:          2401,
		Name:        "RightCode",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://right.codes/codex/v1",
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    OpenAIWSIngressModePassthrough,
		},
	}
	dialer := &openAIWSTransportProbeDialerStub{conn: &openAIWSTransportProbeConnStub{}}
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool

	svc := &OpenAIGatewayService{
		cfg:                       cfg,
		openaiWSPassthroughDialer: dialer,
	}

	decision := svc.resolveOpenAIWSProtocolDecision(context.Background(), &account, "gpt-5.4")
	require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
	require.Equal(t, "apikey_ws_transport_unverified", decision.Reason)
	require.Equal(t, 0, dialer.dialCount)
}

func TestOpenAIGatewayService_ResolveOpenAIStrongContinuationAvailabilityInGroup_UsesRuntimeObservation(t *testing.T) {
	groupID := int64(10113)
	account := Account{
		ID:          2401,
		Name:        "RightCode",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		AccountGroups: []AccountGroup{
			{GroupID: groupID},
		},
		GroupIDs: []int64{groupID},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    OpenAIWSIngressModePassthrough,
		},
	}
	repo := newGroupAwareMockRepo([]Account{account})
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cfg:         newOpenAIWSV2TestConfig(),
	}

	hasStrong, known := svc.ResolveOpenAIStrongContinuationAvailabilityInGroup(context.Background(), &groupID)
	require.True(t, known)
	require.False(t, hasStrong)

	svc.setObservedOpenAIResponsesWebSocketTransportCapability(&account, true, "probe_ws_handshake_supported")

	hasStrong, known = svc.ResolveOpenAIStrongContinuationAvailabilityInGroup(context.Background(), &groupID)
	require.True(t, known)
	require.True(t, hasStrong)
}

func TestOpenAIWSContinuationRuntimeSnapshot_UsesObservedTransportCapability(t *testing.T) {
	account := Account{
		ID:          18,
		Name:        "RightCode",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Schedulable: true,
		Status:      StatusActive,
		GroupIDs:    []int64{2},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    OpenAIWSIngressModePassthrough,
		},
	}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
		cfg:         newOpenAIWSV2TestConfig(),
	}
	svc.setObservedOpenAIResponsesWebSocketTransportCapability(&account, true, "probe_ws_handshake_supported")

	snapshot := svc.buildOpenAIWSContinuationCapabilitySnapshot(context.Background())
	require.Equal(t, int64(1), snapshot.GlobalStrongCohortAccounts)
	require.Equal(t, int64(0), snapshot.GlobalDegradedOnlyAccounts)
	require.Len(t, snapshot.Groups, 1)
	require.Equal(t, int64(1), snapshot.Groups[0].StrongCohortAccounts)
	require.Equal(t, []string{"RightCode"}, snapshot.Groups[0].StrongCohortAccountNames)
}
