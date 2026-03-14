package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *opsRepository) ListRequestDetails(ctx context.Context, filter *service.OpsRequestDetailFilter) ([]*service.OpsRequestDetail, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, fmt.Errorf("nil ops repository")
	}

	page, pageSize, startTime, endTime := filter.Normalize()
	offset := (page - 1) * pageSize

	conditions := make([]string, 0, 16)
	args := make([]any, 0, 24)

	// Placeholders $1/$2 reserved for time window inside the CTE.
	args = append(args, startTime.UTC(), endTime.UTC())

	addCondition := func(condition string, values ...any) {
		conditions = append(conditions, condition)
		args = append(args, values...)
	}

	if filter != nil {
		if kind := strings.TrimSpace(strings.ToLower(filter.Kind)); kind != "" && kind != "all" {
			if kind != string(service.OpsRequestKindSuccess) && kind != string(service.OpsRequestKindError) {
				return nil, 0, fmt.Errorf("invalid kind")
			}
			addCondition(fmt.Sprintf("kind = $%d", len(args)+1), kind)
		}

		if platform := strings.TrimSpace(strings.ToLower(filter.Platform)); platform != "" {
			addCondition(fmt.Sprintf("platform = $%d", len(args)+1), platform)
		}
		if filter.GroupID != nil && *filter.GroupID > 0 {
			addCondition(fmt.Sprintf("group_id = $%d", len(args)+1), *filter.GroupID)
		}

		if filter.UserID != nil && *filter.UserID > 0 {
			addCondition(fmt.Sprintf("user_id = $%d", len(args)+1), *filter.UserID)
		}
		if filter.APIKeyID != nil && *filter.APIKeyID > 0 {
			addCondition(fmt.Sprintf("api_key_id = $%d", len(args)+1), *filter.APIKeyID)
		}
		if filter.AccountID != nil && *filter.AccountID > 0 {
			addCondition(fmt.Sprintf("account_id = $%d", len(args)+1), *filter.AccountID)
		}

		if model := strings.TrimSpace(filter.Model); model != "" {
			addCondition(fmt.Sprintf("model = $%d", len(args)+1), model)
		}
		if requestID := strings.TrimSpace(filter.RequestID); requestID != "" {
			addCondition(fmt.Sprintf("request_id = $%d", len(args)+1), requestID)
		}
		if q := strings.TrimSpace(filter.Query); q != "" {
			like := "%" + strings.ToLower(q) + "%"
			startIdx := len(args) + 1
			addCondition(
				fmt.Sprintf("(LOWER(COALESCE(request_id,'')) LIKE $%d OR LOWER(COALESCE(model,'')) LIKE $%d OR LOWER(COALESCE(message,'')) LIKE $%d)",
					startIdx, startIdx+1, startIdx+2,
				),
				like, like, like,
			)
		}

		if filter.MinDurationMs != nil {
			addCondition(fmt.Sprintf("duration_ms >= $%d", len(args)+1), *filter.MinDurationMs)
		}
		if filter.MaxDurationMs != nil {
			addCondition(fmt.Sprintf("duration_ms <= $%d", len(args)+1), *filter.MaxDurationMs)
		}
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	cte := `
WITH combined AS (
  SELECT
    'success'::TEXT AS kind,
    ul.created_at AS created_at,
    ul.request_id AS request_id,
    COALESCE(NULLIF(g.platform, ''), NULLIF(a.platform, ''), '') AS platform,
    ul.model AS model,
    ul.duration_ms AS duration_ms,
    NULL::INT AS status_code,
    NULL::BIGINT AS error_id,
    NULL::TEXT AS phase,
    NULL::TEXT AS severity,
    NULL::TEXT AS message,
    ul.user_id AS user_id,
    ul.api_key_id AS api_key_id,
    ul.account_id AS account_id,
    ul.group_id AS group_id,
    ul.stream AS stream,
    ul.input_tokens AS input_tokens,
    ul.cache_read_tokens AS cache_read_tokens,
    ul.openai_ws_mode AS openai_ws_mode,
    COALESCE(attr.extra, '{}'::jsonb)::text AS attribution_json,
    compact_attr.request_id AS compact_request_id,
    COALESCE(compact_attr.extra, '{}'::jsonb)::text AS compact_attribution_json,
    CASE
      WHEN compact_attr.created_at IS NOT NULL
      THEN GREATEST((EXTRACT(EPOCH FROM (ul.created_at - compact_attr.created_at)) * 1000)::BIGINT, 0)
      ELSE NULL::BIGINT
    END AS compact_age_ms,
    compact_window_stats.turn_count AS compact_window_turn_count,
    compact_window_stats.bridge_turn_count AS compact_window_bridge_turn_count,
    compact_window_stats.replay_input_items AS compact_window_replay_input_items,
    compact_window_stats.replay_input_bytes AS compact_window_replay_input_bytes,
    compact_window_stats.billable_input_tokens AS compact_window_billable_input_tokens,
    compact_window_stats.cache_read_tokens AS compact_window_cache_read_tokens,
    compact_window_stats.upstream_input_tokens AS compact_window_upstream_input_tokens
  FROM usage_logs ul
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  LEFT JOIN LATERAL (
    SELECT l.extra
    FROM ops_system_logs l
    WHERE COALESCE(l.request_id, '') = COALESCE(ul.request_id, '')
      AND l.message = 'openai.turn_token_attribution'
    ORDER BY l.created_at DESC, l.id DESC
    LIMIT 1
  ) attr ON TRUE
  LEFT JOIN LATERAL (
    SELECT l.request_id, l.created_at, l.extra
    FROM ops_system_logs l
    WHERE l.message = 'openai.turn_token_attribution'
      AND COALESCE(l.extra->>'session_hash', '') <> ''
      AND COALESCE(l.extra->>'session_hash', '') = COALESCE(attr.extra->>'session_hash', '')
      AND COALESCE((l.extra->>'compact_request')::BOOLEAN, false) = true
      AND COALESCE(l.request_id, '') <> COALESCE(ul.request_id, '')
      AND l.created_at <= ul.created_at
    ORDER BY l.created_at DESC, l.id DESC
    LIMIT 1
  ) compact_attr ON TRUE
  LEFT JOIN LATERAL (
    SELECT
      COUNT(1)::BIGINT AS turn_count,
      COALESCE(SUM(CASE WHEN COALESCE((l.extra->>'bridge_used')::BOOLEAN, false) THEN 1 ELSE 0 END), 0)::BIGINT AS bridge_turn_count,
      COALESCE(SUM(COALESCE((l.extra->>'replay_input_items')::INT, 0)), 0)::BIGINT AS replay_input_items,
      COALESCE(SUM(COALESCE((l.extra->>'replay_input_bytes')::INT, 0)), 0)::BIGINT AS replay_input_bytes,
      COALESCE(SUM(COALESCE((l.extra->>'billable_input_tokens')::INT, 0)), 0)::BIGINT AS billable_input_tokens,
      COALESCE(SUM(COALESCE((l.extra->>'cache_read_tokens')::INT, 0)), 0)::BIGINT AS cache_read_tokens,
      COALESCE(SUM(COALESCE((l.extra->>'upstream_input_tokens')::INT, 0)), 0)::BIGINT AS upstream_input_tokens
    FROM ops_system_logs l
    WHERE compact_attr.created_at IS NOT NULL
      AND l.message = 'openai.turn_token_attribution'
      AND COALESCE(l.extra->>'session_hash', '') <> ''
      AND COALESCE(l.extra->>'session_hash', '') = COALESCE(attr.extra->>'session_hash', '')
      AND COALESCE((l.extra->>'compact_request')::BOOLEAN, false) = false
      AND (
        (l.created_at > compact_attr.created_at AND l.created_at <= ul.created_at)
        OR COALESCE(l.request_id, '') = COALESCE(ul.request_id, '')
      )
  ) compact_window_stats ON TRUE
  WHERE ul.created_at >= $1 AND ul.created_at < $2

  UNION ALL

  SELECT
    'error'::TEXT AS kind,
    o.created_at AS created_at,
    COALESCE(NULLIF(o.request_id,''), NULLIF(o.client_request_id,''), '') AS request_id,
    COALESCE(NULLIF(o.platform, ''), NULLIF(g.platform, ''), NULLIF(a.platform, ''), '') AS platform,
    o.model AS model,
    o.duration_ms AS duration_ms,
    o.status_code AS status_code,
    o.id AS error_id,
    o.error_phase AS phase,
    o.severity AS severity,
    o.error_message AS message,
    o.user_id AS user_id,
    o.api_key_id AS api_key_id,
    o.account_id AS account_id,
    o.group_id AS group_id,
    o.stream AS stream,
    NULL::INT AS input_tokens,
    NULL::INT AS cache_read_tokens,
    NULL::BOOLEAN AS openai_ws_mode,
    '{}'::jsonb::text AS attribution_json,
    NULL::TEXT AS compact_request_id,
    '{}'::jsonb::text AS compact_attribution_json,
    NULL::BIGINT AS compact_age_ms,
    NULL::BIGINT AS compact_window_turn_count,
    NULL::BIGINT AS compact_window_bridge_turn_count,
    NULL::BIGINT AS compact_window_replay_input_items,
    NULL::BIGINT AS compact_window_replay_input_bytes,
    NULL::BIGINT AS compact_window_billable_input_tokens,
    NULL::BIGINT AS compact_window_cache_read_tokens,
    NULL::BIGINT AS compact_window_upstream_input_tokens
  FROM ops_error_logs o
  LEFT JOIN groups g ON g.id = o.group_id
  LEFT JOIN accounts a ON a.id = o.account_id
  WHERE o.created_at >= $1 AND o.created_at < $2
    AND COALESCE(o.status_code, 0) >= 400
)
`

	countQuery := fmt.Sprintf(`%s SELECT COUNT(1) FROM combined %s`, cte, where)
	var total int64
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		if err == sql.ErrNoRows {
			total = 0
		} else {
			return nil, 0, err
		}
	}

	sort := "ORDER BY created_at DESC"
	if filter != nil {
		switch strings.TrimSpace(strings.ToLower(filter.Sort)) {
		case "", "created_at_desc":
			// default
		case "duration_desc":
			sort = "ORDER BY duration_ms DESC NULLS LAST, created_at DESC"
		default:
			return nil, 0, fmt.Errorf("invalid sort")
		}
	}

	listQuery := fmt.Sprintf(`
%s
SELECT
  kind,
  created_at,
  request_id,
  platform,
  model,
  duration_ms,
  status_code,
  error_id,
  phase,
  severity,
  message,
  user_id,
  api_key_id,
  account_id,
  group_id,
  stream,
  input_tokens,
  cache_read_tokens,
  openai_ws_mode,
  attribution_json,
  compact_request_id,
  compact_attribution_json,
  compact_age_ms,
  compact_window_turn_count,
  compact_window_bridge_turn_count,
  compact_window_replay_input_items,
  compact_window_replay_input_bytes,
  compact_window_billable_input_tokens,
  compact_window_cache_read_tokens,
  compact_window_upstream_input_tokens
FROM combined
%s
%s
LIMIT $%d OFFSET $%d
`, cte, where, sort, len(args)+1, len(args)+2)

	listArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := r.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	toIntPtr := func(v sql.NullInt64) *int {
		if !v.Valid {
			return nil
		}
		i := int(v.Int64)
		return &i
	}
	toInt64Ptr := func(v sql.NullInt64) *int64 {
		if !v.Valid {
			return nil
		}
		i := v.Int64
		return &i
	}

	out := make([]*service.OpsRequestDetail, 0, pageSize)
	for rows.Next() {
		var (
			kind      string
			createdAt time.Time
			requestID sql.NullString
			platform  sql.NullString
			model     sql.NullString

			durationMs sql.NullInt64
			statusCode sql.NullInt64
			errorID    sql.NullInt64

			phase    sql.NullString
			severity sql.NullString
			message  sql.NullString

			userID    sql.NullInt64
			apiKeyID  sql.NullInt64
			accountID sql.NullInt64
			groupID   sql.NullInt64

			stream                           bool
			inputTokens                      sql.NullInt64
			cacheReadTokens                  sql.NullInt64
			openAIWSMode                     sql.NullBool
			attributionJSON                  sql.NullString
			compactRequestID                 sql.NullString
			compactAttributionJSON           sql.NullString
			compactAgeMs                     sql.NullInt64
			compactWindowTurnCount           sql.NullInt64
			compactWindowBridgeTurnCount     sql.NullInt64
			compactWindowReplayInputItems    sql.NullInt64
			compactWindowReplayInputBytes    sql.NullInt64
			compactWindowBillableInputTokens sql.NullInt64
			compactWindowCacheReadTokens     sql.NullInt64
			compactWindowUpstreamInputTokens sql.NullInt64
		)

		if err := rows.Scan(
			&kind,
			&createdAt,
			&requestID,
			&platform,
			&model,
			&durationMs,
			&statusCode,
			&errorID,
			&phase,
			&severity,
			&message,
			&userID,
			&apiKeyID,
			&accountID,
			&groupID,
			&stream,
			&inputTokens,
			&cacheReadTokens,
			&openAIWSMode,
			&attributionJSON,
			&compactRequestID,
			&compactAttributionJSON,
			&compactAgeMs,
			&compactWindowTurnCount,
			&compactWindowBridgeTurnCount,
			&compactWindowReplayInputItems,
			&compactWindowReplayInputBytes,
			&compactWindowBillableInputTokens,
			&compactWindowCacheReadTokens,
			&compactWindowUpstreamInputTokens,
		); err != nil {
			return nil, 0, err
		}

		attribution := service.DecodeOpenAITurnTokenAttributionJSON(strings.TrimSpace(attributionJSON.String))
		compactAttribution := service.DecodeOpenAITurnTokenAttributionJSON(strings.TrimSpace(compactAttributionJSON.String))
		currentInputValue := 0
		if inputTokens.Valid {
			currentInputValue = int(inputTokens.Int64)
		}
		currentCacheReadValue := 0
		if cacheReadTokens.Valid {
			currentCacheReadValue = int(cacheReadTokens.Int64)
		}
		item := &service.OpsRequestDetail{
			Kind:      service.OpsRequestKind(kind),
			CreatedAt: createdAt,
			RequestID: strings.TrimSpace(requestID.String),
			Platform:  strings.TrimSpace(platform.String),
			Model:     strings.TrimSpace(model.String),

			DurationMs: toIntPtr(durationMs),
			StatusCode: toIntPtr(statusCode),
			ErrorID:    toInt64Ptr(errorID),
			Phase:      phase.String,
			Severity:   severity.String,
			Message:    message.String,

			UserID:    toInt64Ptr(userID),
			APIKeyID:  toInt64Ptr(apiKeyID),
			AccountID: toInt64Ptr(accountID),
			GroupID:   toInt64Ptr(groupID),

			Stream:          stream,
			InputTokens:     toIntPtr(inputTokens),
			CacheReadTokens: toIntPtr(cacheReadTokens),
			OpenAIWSMode: func() *bool {
				if !openAIWSMode.Valid {
					return nil
				}
				v := openAIWSMode.Bool
				return &v
			}(),
			TokenAttribution: attribution,
			CompactWindow: service.BuildOpenAICompactWindowAttribution(
				currentInputValue,
				currentCacheReadValue,
				attribution,
				strings.TrimSpace(compactRequestID.String),
				compactAgeMs.Int64,
				compactAttribution,
				&service.OpenAICompactWindowRollup{
					TurnCount:           int(compactWindowTurnCount.Int64),
					BridgeTurnCount:     int(compactWindowBridgeTurnCount.Int64),
					ReplayInputItems:    int(compactWindowReplayInputItems.Int64),
					ReplayInputBytes:    int(compactWindowReplayInputBytes.Int64),
					BillableInputTokens: int(compactWindowBillableInputTokens.Int64),
					CacheReadTokens:     int(compactWindowCacheReadTokens.Int64),
					UpstreamInputTokens: int(compactWindowUpstreamInputTokens.Int64),
				},
			),
		}

		if item.Platform == "" {
			item.Platform = "unknown"
		}

		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return out, total, nil
}
