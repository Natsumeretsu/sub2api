//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetPoolModeRetryCount(t *testing.T) {
	tests := []struct {
		name     string
		account  *Account
		expected int
	}{
		{
			name: "default_when_not_pool_mode",
			account: &Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformOpenAI,
				Credentials: map[string]any{},
			},
			expected: defaultPoolModeRetryCount,
		},
		{
			name: "default_when_missing_retry_count",
			account: &Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode": true,
				},
			},
			expected: defaultPoolModeRetryCount,
		},
		{
			name: "supports_float64_from_json_credentials",
			account: &Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": float64(5),
				},
			},
			expected: 5,
		},
		{
			name: "supports_json_number",
			account: &Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": json.Number("4"),
				},
			},
			expected: 4,
		},
		{
			name: "supports_string_value",
			account: &Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": "2",
				},
			},
			expected: 2,
		},
		{
			name: "negative_value_is_clamped_to_zero",
			account: &Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": -1,
				},
			},
			expected: 0,
		},
		{
			name: "oversized_value_is_clamped_to_max",
			account: &Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": 99,
				},
			},
			expected: maxPoolModeRetryCount,
		},
		{
			name: "invalid_value_falls_back_to_default",
			account: &Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": "oops",
				},
			},
			expected: defaultPoolModeRetryCount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.account.GetPoolModeRetryCount())
		})
	}
}

func TestSupportsOpenAIResponsesCompact(t *testing.T) {
	t.Run("oauth accounts support compact by default", func(t *testing.T) {
		account := &Account{Type: AccountTypeOAuth, Platform: PlatformOpenAI}
		require.True(t, account.SupportsOpenAIResponsesCompact())
		supported, decided, source := account.ResolveOpenAIResponsesCompactCapability()
		require.True(t, supported)
		require.True(t, decided)
		require.Equal(t, "oauth_default", source)
	})

	t.Run("apikey accounts are undecided without explicit signal", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"base_url": "https://codex-api.packycode.com/v1",
			},
		}
		require.False(t, account.SupportsOpenAIResponsesCompact())
		supported, decided, source := account.ResolveOpenAIResponsesCompactCapability()
		require.False(t, supported)
		require.False(t, decided)
		require.Equal(t, "", source)
	})

	t.Run("apikey accounts honor bool credential", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"base_url":                    "https://codex-api.packycode.com/v1",
				"responses_compact_supported": true,
			},
		}
		require.True(t, account.SupportsOpenAIResponsesCompact())
		supported, decided, source := account.ResolveOpenAIResponsesCompactCapability()
		require.True(t, supported)
		require.True(t, decided)
		require.Equal(t, "credential:responses_compact_supported", source)
	})

	t.Run("apikey accounts honor string credential", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"base_url":                    "https://codex-api.packycode.com/v1",
				"responses_compact_supported": "true",
			},
		}
		require.True(t, account.SupportsOpenAIResponsesCompact())
	})

	t.Run("apikey accounts honor extra capability", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Extra: map[string]any{
				"openai_apikey_responses_compact_supported": true,
			},
		}
		require.True(t, account.SupportsOpenAIResponsesCompact())
		supported, decided, source := account.ResolveOpenAIResponsesCompactCapability()
		require.True(t, supported)
		require.True(t, decided)
		require.Equal(t, "extra:openai_apikey_responses_compact_supported", source)
	})
}

func TestSupportsOpenAIResponsesWebSocketTransport(t *testing.T) {
	t.Run("oauth accounts support websocket transport by default", func(t *testing.T) {
		account := &Account{Type: AccountTypeOAuth, Platform: PlatformOpenAI}
		require.True(t, account.SupportsOpenAIResponsesWebSocketTransport())
	})

	t.Run("apikey accounts require explicit opt in", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"base_url": "https://codex-api.packycode.com/v1",
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			},
		}
		require.False(t, account.SupportsOpenAIResponsesWebSocketTransport())
	})

	t.Run("apikey accounts honor explicit credential capability", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"responses_websockets_v2_supported": true,
			},
		}
		require.True(t, account.SupportsOpenAIResponsesWebSocketTransport())
	})

	t.Run("apikey accounts honor explicit extra capability", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_supported": "true",
			},
		}
		require.True(t, account.SupportsOpenAIResponsesWebSocketTransport())
	})
}

func TestSupportsOpenAIHTTPPreviousResponseID(t *testing.T) {
	t.Run("oauth passthrough accounts default to unsupported", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeOAuth,
			Platform: PlatformOpenAI,
			Extra: map[string]any{
				"openai_passthrough": true,
			},
		}
		require.False(t, account.SupportsOpenAIHTTPPreviousResponseID())
	})

	t.Run("oauth non passthrough accounts default to supported", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeOAuth,
			Platform: PlatformOpenAI,
			Extra:    map[string]any{},
		}
		require.True(t, account.SupportsOpenAIHTTPPreviousResponseID())
	})

	t.Run("oauth explicit capability overrides passthrough default", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeOAuth,
			Platform: PlatformOpenAI,
			Extra: map[string]any{
				"openai_passthrough": true,
				"openai_oauth_http_previous_response_id_supported": true,
			},
		}
		require.True(t, account.SupportsOpenAIHTTPPreviousResponseID())
	})

	t.Run("official api key surface supports previous_response_id by default", func(t *testing.T) {
		account := &Account{
			Type:        AccountTypeAPIKey,
			Platform:    PlatformOpenAI,
			Credentials: map[string]any{"api_key": "sk-test"},
		}
		require.True(t, account.SupportsOpenAIHTTPPreviousResponseID())
	})

	t.Run("custom api key surface defaults to unsupported", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://codex-api.packycode.com/v1",
			},
		}
		require.False(t, account.SupportsOpenAIHTTPPreviousResponseID())
	})

	t.Run("custom api key surface honors explicit capability", func(t *testing.T) {
		account := &Account{
			Type:     AccountTypeAPIKey,
			Platform: PlatformOpenAI,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://codex-api.packycode.com/v1",
			},
			Extra: map[string]any{
				"openai_apikey_http_previous_response_id_supported": true,
			},
		}
		require.True(t, account.SupportsOpenAIHTTPPreviousResponseID())
	})
}
