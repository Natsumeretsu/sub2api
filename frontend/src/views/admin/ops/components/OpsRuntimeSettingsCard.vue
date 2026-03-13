<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { opsAPI } from '@/api/admin/ops'
import type { OpsAlertRuntimeSettings, OpsRuntimeContinuationResponse } from '../types'
import BaseDialog from '@/components/common/BaseDialog.vue'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const saving = ref(false)

const alertSettings = ref<OpsAlertRuntimeSettings | null>(null)
const continuationRuntime = ref<OpsRuntimeContinuationResponse | null>(null)
const continuationLoadFailed = ref(false)

const showAlertEditor = ref(false)
const draftAlert = ref<OpsAlertRuntimeSettings | null>(null)

type ValidationResult = { valid: boolean; errors: string[] }

function normalizeSeverities(input: Array<string | null | undefined> | null | undefined): string[] {
  if (!input || input.length === 0) return []
  const allowed = new Set(['P0', 'P1', 'P2', 'P3'])
  const out: string[] = []
  const seen = new Set<string>()
  for (const raw of input) {
    const s = String(raw || '')
      .trim()
      .toUpperCase()
    if (!s) continue
    if (!allowed.has(s)) continue
    if (seen.has(s)) continue
    seen.add(s)
    out.push(s)
  }
  return out
}

function validateRuntimeSettings(settings: OpsAlertRuntimeSettings): ValidationResult {
  const errors: string[] = []

  const evalSeconds = settings.evaluation_interval_seconds
  if (!Number.isFinite(evalSeconds) || evalSeconds < 1 || evalSeconds > 86400) {
    errors.push(t('admin.ops.runtime.validation.evalIntervalRange'))
  }

  // Thresholds validation
  const thresholds = settings.thresholds
  if (thresholds) {
    if (thresholds.sla_percent_min != null) {
      if (!Number.isFinite(thresholds.sla_percent_min) || thresholds.sla_percent_min < 0 || thresholds.sla_percent_min > 100) {
        errors.push(t('admin.ops.runtime.validation.slaMinPercentRange'))
      }
    }
    if (thresholds.ttft_p99_ms_max != null) {
      if (!Number.isFinite(thresholds.ttft_p99_ms_max) || thresholds.ttft_p99_ms_max < 0) {
        errors.push(t('admin.ops.runtime.validation.ttftP99MaxRange'))
      }
    }
    if (thresholds.request_error_rate_percent_max != null) {
      if (!Number.isFinite(thresholds.request_error_rate_percent_max) || thresholds.request_error_rate_percent_max < 0 || thresholds.request_error_rate_percent_max > 100) {
        errors.push(t('admin.ops.runtime.validation.requestErrorRateMaxRange'))
      }
    }
    if (thresholds.upstream_error_rate_percent_max != null) {
      if (!Number.isFinite(thresholds.upstream_error_rate_percent_max) || thresholds.upstream_error_rate_percent_max < 0 || thresholds.upstream_error_rate_percent_max > 100) {
        errors.push(t('admin.ops.runtime.validation.upstreamErrorRateMaxRange'))
      }
    }
  }

  const lock = settings.distributed_lock
  if (lock?.enabled) {
    if (!lock.key || lock.key.trim().length < 3) {
      errors.push(t('admin.ops.runtime.validation.lockKeyRequired'))
    } else if (!lock.key.startsWith('ops:')) {
      errors.push(t('admin.ops.runtime.validation.lockKeyPrefix', { prefix: 'ops:' }))
    }
    if (!Number.isFinite(lock.ttl_seconds) || lock.ttl_seconds < 1 || lock.ttl_seconds > 86400) {
      errors.push(t('admin.ops.runtime.validation.lockTtlRange'))
    }
  }

  // Silencing validation (alert-only)
  const silencing = settings.silencing
  if (silencing?.enabled) {
    const until = (silencing.global_until_rfc3339 || '').trim()
    if (until) {
      const parsed = Date.parse(until)
      if (!Number.isFinite(parsed)) errors.push(t('admin.ops.runtime.silencing.validation.timeFormat'))
    }

    const entries = Array.isArray(silencing.entries) ? silencing.entries : []
    for (let idx = 0; idx < entries.length; idx++) {
      const entry = entries[idx]
      const untilEntry = (entry?.until_rfc3339 || '').trim()
      if (!untilEntry) {
        errors.push(t('admin.ops.runtime.silencing.entries.validation.untilRequired'))
        break
      }
      const parsedEntry = Date.parse(untilEntry)
      if (!Number.isFinite(parsedEntry)) {
        errors.push(t('admin.ops.runtime.silencing.entries.validation.untilFormat'))
        break
      }
      const ruleId = (entry as any)?.rule_id
      if (typeof ruleId === 'number' && (!Number.isFinite(ruleId) || ruleId <= 0)) {
        errors.push(t('admin.ops.runtime.silencing.entries.validation.ruleIdPositive'))
        break
      }
      if ((entry as any)?.severities) {
        const raw = (entry as any).severities
        const normalized = normalizeSeverities(Array.isArray(raw) ? raw : [raw])
        if (Array.isArray(raw) && raw.length > 0 && normalized.length === 0) {
          errors.push(t('admin.ops.runtime.silencing.entries.validation.severitiesFormat'))
          break
        }
      }
    }
  }

  return { valid: errors.length === 0, errors }
}

const alertValidation = computed(() => {
  if (!draftAlert.value) return { valid: true, errors: [] as string[] }
  return validateRuntimeSettings(draftAlert.value)
})

const hasAnyRuntimeData = computed(() => !!alertSettings.value || !!continuationRuntime.value)
const continuationSnapshot = computed(() => continuationRuntime.value?.openai_ws || null)

type ContinuationDisplayItem = {
  key: string
  label: string
  value: string | number
  tone?: 'neutral' | 'info' | 'success' | 'warning'
}

type ContinuationSummaryItem = {
  key: string
  label: string
  value: string
  description: string
  tone: 'neutral' | 'info' | 'success' | 'warning'
}

type ContinuationDiagnosisRule = {
  key: string
  label: string
  threshold: string
  observed: string
  action: string
  tone: 'neutral' | 'info' | 'success' | 'warning'
  hit: boolean
}

const CONTINUATION_RULE_THRESHOLDS = {
  validationRejectWarn: 1,
  validationRejectEscalate: 3,
  failCloseWarn: 1,
  recoveryNotice: 1,
  recoveryWarn: 5,
  selfContainedNotice: 1,
  persistenceWarnMinCount: 2,
  saturationInfoPercent: 50,
  saturationWarnPercent: 80,
  connChurnWarn: 10
} as const

function boolLabel(value: boolean | null | undefined): string {
  return value ? t('common.enabled') : t('common.disabled')
}

function metricToneClass(tone: ContinuationDisplayItem['tone']): string {
  switch (tone) {
    case 'success':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case 'warning':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    case 'info':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
    default:
      return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
  }
}

function formatCount(value: number | null | undefined): string {
  return typeof value === 'number' && Number.isFinite(value) ? String(value) : '0'
}

function formatPercent(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '0%'
  return `${Math.round(value)}%`
}

const continuationModeBadges = computed<ContinuationDisplayItem[]>(() => {
  const config = continuationSnapshot.value?.config
  if (!config) return []
  return [
    {
      key: 'enabled',
      label: t('admin.ops.runtime.continuation.badges.gateway'),
      value: boolLabel(config.enabled),
      tone: config.enabled ? 'success' : 'warning'
    },
    {
      key: 'wsv2',
      label: t('admin.ops.runtime.continuation.badges.wsv2'),
      value: boolLabel(config.responses_websockets_v2),
      tone: config.responses_websockets_v2 ? 'success' : 'warning'
    },
    {
      key: 'force_http',
      label: t('admin.ops.runtime.continuation.badges.forceHttp'),
      value: boolLabel(config.force_http),
      tone: config.force_http ? 'warning' : 'info'
    },
    {
      key: 'mode',
      label: t('admin.ops.runtime.continuation.badges.connMode'),
      value: config.store_disabled_conn_mode || t('common.unknown'),
      tone: config.store_disabled_conn_mode === 'strict' ? 'info' : 'neutral'
    }
  ]
})

const continuationCounterItems = computed<ContinuationDisplayItem[]>(() => {
  const counters = continuationSnapshot.value?.counters
  if (!counters) return []
  return [
    {
      key: 'validation_missing_call_id',
      label: t('admin.ops.runtime.continuation.counters.validationMissingCallId'),
      value: formatCount(counters.validation_reject_missing_call_id_total),
      tone: counters.validation_reject_missing_call_id_total > 0 ? 'warning' : 'neutral'
    },
    {
      key: 'validation_missing_item_reference',
      label: t('admin.ops.runtime.continuation.counters.validationMissingItemReference'),
      value: formatCount(counters.validation_reject_missing_item_reference_total),
      tone: counters.validation_reject_missing_item_reference_total > 0 ? 'warning' : 'neutral'
    },
    {
      key: 'prev_align_retry',
      label: t('admin.ops.runtime.continuation.counters.prevAlignRetry'),
      value: formatCount(counters.prev_not_found_align_retry_total),
      tone: counters.prev_not_found_align_retry_total > 0 ? 'info' : 'neutral'
    },
    {
      key: 'prev_drop_retry',
      label: t('admin.ops.runtime.continuation.counters.prevDropRetry'),
      value: formatCount(counters.prev_not_found_drop_retry_total),
      tone: counters.prev_not_found_drop_retry_total > 0 ? 'info' : 'neutral'
    },
    {
      key: 'prev_self_contained_retry',
      label: t('admin.ops.runtime.continuation.counters.prevSelfContainedRetry'),
      value: formatCount(counters.prev_not_found_drop_self_contained_retry_total),
      tone: counters.prev_not_found_drop_self_contained_retry_total > 0 ? 'success' : 'neutral'
    },
    {
      key: 'prev_missing_anchor_fail_close',
      label: t('admin.ops.runtime.continuation.counters.prevMissingAnchorFailClose'),
      value: formatCount(counters.prev_not_found_fail_closed_missing_anchor_total),
      tone: counters.prev_not_found_fail_closed_missing_anchor_total > 0 ? 'warning' : 'neutral'
    },
    {
      key: 'preflight_align_retry',
      label: t('admin.ops.runtime.continuation.counters.preflightAlignRetry'),
      value: formatCount(counters.preflight_ping_align_retry_total),
      tone: counters.preflight_ping_align_retry_total > 0 ? 'info' : 'neutral'
    },
    {
      key: 'preflight_drop_retry',
      label: t('admin.ops.runtime.continuation.counters.preflightDropRetry'),
      value: formatCount(counters.preflight_ping_drop_retry_total),
      tone: counters.preflight_ping_drop_retry_total > 0 ? 'info' : 'neutral'
    },
    {
      key: 'preflight_self_contained_retry',
      label: t('admin.ops.runtime.continuation.counters.preflightSelfContainedRetry'),
      value: formatCount(counters.preflight_ping_drop_self_contained_retry_total),
      tone: counters.preflight_ping_drop_self_contained_retry_total > 0 ? 'success' : 'neutral'
    },
    {
      key: 'preflight_missing_anchor_fail_close',
      label: t('admin.ops.runtime.continuation.counters.preflightMissingAnchorFailClose'),
      value: formatCount(counters.preflight_ping_fail_closed_missing_anchor_total),
      tone: counters.preflight_ping_fail_closed_missing_anchor_total > 0 ? 'warning' : 'neutral'
    }
  ]
})

const continuationConfigItems = computed<ContinuationDisplayItem[]>(() => {
  const config = continuationSnapshot.value?.config
  if (!config) return []
  return [
    { key: 'responses_websockets', label: t('admin.ops.runtime.continuation.config.responsesWebsockets'), value: boolLabel(config.responses_websockets) },
    { key: 'allow_store_recovery', label: t('admin.ops.runtime.continuation.config.allowStoreRecovery'), value: boolLabel(config.allow_store_recovery) },
    {
      key: 'ingress_previous_response_recovery_enabled',
      label: t('admin.ops.runtime.continuation.config.ingressRecovery'),
      value: boolLabel(config.ingress_previous_response_recovery_enabled)
    },
    {
      key: 'store_disabled_force_new_conn',
      label: t('admin.ops.runtime.continuation.config.forceNewConn'),
      value: boolLabel(config.store_disabled_force_new_conn)
    },
    {
      key: 'sticky_session_ttl_seconds',
      label: t('admin.ops.runtime.continuation.config.stickySessionTTL'),
      value: `${config.sticky_session_ttl_seconds}s`
    },
    {
      key: 'sticky_response_id_ttl_seconds',
      label: t('admin.ops.runtime.continuation.config.stickyResponseTTL'),
      value: `${config.sticky_response_id_ttl_seconds}s`
    },
    {
      key: 'sticky_previous_response_ttl_seconds',
      label: t('admin.ops.runtime.continuation.config.stickyPreviousResponseTTL'),
      value: `${config.sticky_previous_response_ttl_seconds}s`
    },
    {
      key: 'retry_total_budget_ms',
      label: t('admin.ops.runtime.continuation.config.retryBudget'),
      value: `${config.retry_total_budget_ms}ms`
    },
    {
      key: 'fallback_cooldown_seconds',
      label: t('admin.ops.runtime.continuation.config.fallbackCooldown'),
      value: `${config.fallback_cooldown_seconds}s`
    },
    {
      key: 'conn_pool',
      label: t('admin.ops.runtime.continuation.config.connPool'),
      value: `${config.min_idle_per_account} / ${config.max_idle_per_account} / ${config.max_conns_per_account}`
    }
  ]
})

const continuationStateEntryItems = computed<ContinuationDisplayItem[]>(() => {
  const state = continuationSnapshot.value?.state
  if (!state) return []
  return [
    { key: 'response_account_local_entries', label: t('admin.ops.runtime.continuation.state.responseAccountEntries'), value: formatCount(state.response_account_local_entries) },
    { key: 'response_conn_entries', label: t('admin.ops.runtime.continuation.state.responseConnEntries'), value: formatCount(state.response_conn_entries) },
    { key: 'session_turn_state_entries', label: t('admin.ops.runtime.continuation.state.sessionTurnStateEntries'), value: formatCount(state.session_turn_state_entries) },
    { key: 'session_last_response_entries', label: t('admin.ops.runtime.continuation.state.sessionLastResponseEntries'), value: formatCount(state.session_last_response_entries) },
    { key: 'session_conn_entries', label: t('admin.ops.runtime.continuation.state.sessionConnEntries'), value: formatCount(state.session_conn_entries) }
  ]
})

const continuationStatePersistenceItems = computed<ContinuationDisplayItem[]>(() => {
  const state = continuationSnapshot.value?.state
  if (!state) return []
  return [
    {
      key: 'response_account_persistent',
      label: t('admin.ops.runtime.continuation.state.responseAccountPersistent'),
      value: boolLabel(state.response_account_persistent),
      tone: state.response_account_persistent ? 'success' : 'warning'
    },
    {
      key: 'session_turn_state_persistent',
      label: t('admin.ops.runtime.continuation.state.sessionTurnStatePersistent'),
      value: boolLabel(state.session_turn_state_persistent),
      tone: state.session_turn_state_persistent ? 'success' : 'warning'
    },
    {
      key: 'session_last_response_persistent',
      label: t('admin.ops.runtime.continuation.state.sessionLastResponsePersistent'),
      value: boolLabel(state.session_last_response_persistent),
      tone: state.session_last_response_persistent ? 'success' : 'warning'
    }
  ]
})

const continuationStateChurnItems = computed<ContinuationDisplayItem[]>(() => {
  const state = continuationSnapshot.value?.state
  if (!state) return []
  return [
    { key: 'response_account_bind_total', label: t('admin.ops.runtime.continuation.state.responseAccountBindTotal'), value: formatCount(state.response_account_bind_total) },
    { key: 'response_account_delete_total', label: t('admin.ops.runtime.continuation.state.responseAccountDeleteTotal'), value: formatCount(state.response_account_delete_total) },
    { key: 'response_conn_bind_total', label: t('admin.ops.runtime.continuation.state.responseConnBindTotal'), value: formatCount(state.response_conn_bind_total) },
    { key: 'response_conn_delete_total', label: t('admin.ops.runtime.continuation.state.responseConnDeleteTotal'), value: formatCount(state.response_conn_delete_total) },
    { key: 'session_turn_state_bind_total', label: t('admin.ops.runtime.continuation.state.sessionTurnStateBindTotal'), value: formatCount(state.session_turn_state_bind_total) },
    { key: 'session_turn_state_delete_total', label: t('admin.ops.runtime.continuation.state.sessionTurnStateDeleteTotal'), value: formatCount(state.session_turn_state_delete_total) },
    { key: 'session_last_response_bind_total', label: t('admin.ops.runtime.continuation.state.sessionLastResponseBindTotal'), value: formatCount(state.session_last_response_bind_total) },
    { key: 'session_last_response_delete_total', label: t('admin.ops.runtime.continuation.state.sessionLastResponseDeleteTotal'), value: formatCount(state.session_last_response_delete_total) },
    { key: 'session_conn_bind_total', label: t('admin.ops.runtime.continuation.state.sessionConnBindTotal'), value: formatCount(state.session_conn_bind_total) },
    { key: 'session_conn_delete_total', label: t('admin.ops.runtime.continuation.state.sessionConnDeleteTotal'), value: formatCount(state.session_conn_delete_total) }
  ]
})

const continuationStateLimitItems = computed<ContinuationDisplayItem[]>(() => {
  const state = continuationSnapshot.value?.state
  if (!state) return []
  return [
    { key: 'local_cleanup_interval_seconds', label: t('admin.ops.runtime.continuation.state.localCleanupInterval'), value: `${state.local_cleanup_interval_seconds}s` },
    { key: 'local_cleanup_max_per_map', label: t('admin.ops.runtime.continuation.state.localCleanupMaxPerMap'), value: formatCount(state.local_cleanup_max_per_map) },
    { key: 'local_max_entries_per_map', label: t('admin.ops.runtime.continuation.state.localMaxEntriesPerMap'), value: formatCount(state.local_max_entries_per_map) },
    { key: 'redis_timeout_ms', label: t('admin.ops.runtime.continuation.state.redisTimeoutMs'), value: `${state.redis_timeout_ms}ms` }
  ]
})

const continuationDerivedStats = computed(() => {
  const snapshot = continuationSnapshot.value
  if (!snapshot) return null

  const counters = snapshot.counters
  const state = snapshot.state

  const validationRejects =
    counters.validation_reject_missing_call_id_total +
    counters.validation_reject_missing_item_reference_total
  const failClosed =
    counters.prev_not_found_fail_closed_missing_anchor_total +
    counters.preflight_ping_fail_closed_missing_anchor_total
  const recoveries =
    counters.prev_not_found_align_retry_total +
    counters.prev_not_found_drop_retry_total +
    counters.prev_not_found_drop_self_contained_retry_total +
    counters.preflight_ping_align_retry_total +
    counters.preflight_ping_drop_retry_total +
    counters.preflight_ping_drop_self_contained_retry_total
  const selfContainedRecoveries =
    counters.prev_not_found_drop_self_contained_retry_total +
    counters.preflight_ping_drop_self_contained_retry_total
  const persistentCount = [
    state.response_account_persistent,
    state.session_turn_state_persistent,
    state.session_last_response_persistent
  ].filter(Boolean).length
  const maxEntries = Math.max(
    state.response_account_local_entries,
    state.response_conn_entries,
    state.session_turn_state_entries,
    state.session_last_response_entries,
    state.session_conn_entries,
    0
  )
  const saturationPercent =
    state.local_max_entries_per_map > 0 ? (maxEntries / state.local_max_entries_per_map) * 100 : 0
  const connDeleteCount = state.response_conn_delete_total + state.session_conn_delete_total

  return {
    validationRejects,
    failClosed,
    recoveries,
    selfContainedRecoveries,
    persistentCount,
    maxEntries,
    saturationPercent,
    connDeleteCount
  }
})

const continuationSummaryItems = computed<ContinuationSummaryItem[]>(() => {
  const snapshot = continuationSnapshot.value
  const derived = continuationDerivedStats.value
  if (!snapshot || !derived) return []

  const config = snapshot.config
  const persistencePercent = (derived.persistentCount / 3) * 100

  let postureValue = t('admin.ops.runtime.continuation.summary.posture.healthy')
  let postureDesc = t('admin.ops.runtime.continuation.summary.postureHealthyDesc')
  let postureTone: ContinuationSummaryItem['tone'] = 'success'

  if (!config.enabled) {
    postureValue = t('admin.ops.runtime.continuation.summary.posture.disabled')
    postureDesc = t('admin.ops.runtime.continuation.summary.postureDisabledDesc')
    postureTone = 'warning'
  } else if (config.force_http || !config.responses_websockets_v2) {
    postureValue = t('admin.ops.runtime.continuation.summary.posture.degraded')
    postureDesc = t('admin.ops.runtime.continuation.summary.postureDegradedDesc')
    postureTone = 'warning'
  } else if (
    derived.failClosed >= CONTINUATION_RULE_THRESHOLDS.failCloseWarn ||
    derived.validationRejects >= CONTINUATION_RULE_THRESHOLDS.validationRejectWarn
  ) {
    postureValue = t('admin.ops.runtime.continuation.summary.posture.attention')
    postureDesc = t('admin.ops.runtime.continuation.summary.postureAttentionDesc')
    postureTone = 'warning'
  } else if (derived.recoveries >= CONTINUATION_RULE_THRESHOLDS.recoveryNotice) {
    postureValue = t('admin.ops.runtime.continuation.summary.posture.recovering')
    postureDesc = t('admin.ops.runtime.continuation.summary.postureRecoveringDesc')
    postureTone = 'info'
  }

  let experienceValue = t('admin.ops.runtime.continuation.summary.experience.stable')
  let experienceDesc = t('admin.ops.runtime.continuation.summary.experienceStableDesc')
  let experienceTone: ContinuationSummaryItem['tone'] = 'success'
  if (derived.failClosed >= CONTINUATION_RULE_THRESHOLDS.failCloseWarn) {
    experienceValue = t('admin.ops.runtime.continuation.summary.experience.failClose')
    experienceDesc = t('admin.ops.runtime.continuation.summary.experienceFailCloseDesc', { count: derived.failClosed })
    experienceTone = 'warning'
  } else if (derived.selfContainedRecoveries >= CONTINUATION_RULE_THRESHOLDS.selfContainedNotice) {
    experienceValue = t('admin.ops.runtime.continuation.summary.experience.selfContained')
    experienceDesc = t('admin.ops.runtime.continuation.summary.experienceSelfContainedDesc', { count: derived.selfContainedRecoveries })
    experienceTone = 'success'
  } else if (derived.recoveries >= CONTINUATION_RULE_THRESHOLDS.recoveryNotice) {
    experienceValue = t('admin.ops.runtime.continuation.summary.experience.recovered')
    experienceDesc = t('admin.ops.runtime.continuation.summary.experienceRecoveredDesc', { count: derived.recoveries })
    experienceTone = 'info'
  }

  const persistenceTone: ContinuationSummaryItem['tone'] =
    derived.persistentCount === 3 ? 'success' : derived.persistentCount >= CONTINUATION_RULE_THRESHOLDS.persistenceWarnMinCount ? 'info' : 'warning'
  const saturationTone: ContinuationSummaryItem['tone'] =
    derived.saturationPercent >= CONTINUATION_RULE_THRESHOLDS.saturationWarnPercent
      ? 'warning'
      : derived.saturationPercent >= CONTINUATION_RULE_THRESHOLDS.saturationInfoPercent
        ? 'info'
        : 'success'

  return [
    {
      key: 'posture',
      label: t('admin.ops.runtime.continuation.summary.labels.posture'),
      value: postureValue,
      description: postureDesc,
      tone: postureTone
    },
    {
      key: 'experience',
      label: t('admin.ops.runtime.continuation.summary.labels.experience'),
      value: experienceValue,
      description: experienceDesc,
      tone: experienceTone
    },
    {
      key: 'persistence',
      label: t('admin.ops.runtime.continuation.summary.labels.persistence'),
      value: `${derived.persistentCount}/3 (${formatPercent(persistencePercent)})`,
      description: t('admin.ops.runtime.continuation.summary.persistenceDesc', { count: derived.persistentCount }),
      tone: persistenceTone
    },
    {
      key: 'saturation',
      label: t('admin.ops.runtime.continuation.summary.labels.saturation'),
      value: formatPercent(derived.saturationPercent),
      description: t('admin.ops.runtime.continuation.summary.saturationDesc', {
        entries: derived.maxEntries,
        limit: snapshot.state.local_max_entries_per_map
      }),
      tone: saturationTone
    }
  ]
})

const continuationDiagnosisRules = computed<ContinuationDiagnosisRule[]>(() => {
  const snapshot = continuationSnapshot.value
  const derived = continuationDerivedStats.value
  if (!snapshot || !derived) return []

  const rules: ContinuationDiagnosisRule[] = [
    {
      key: 'validation',
      label: t('admin.ops.runtime.continuation.rules.validation'),
      threshold: t('admin.ops.runtime.continuation.rules.thresholdAtLeast', {
        count: CONTINUATION_RULE_THRESHOLDS.validationRejectWarn
      }),
      observed: formatCount(derived.validationRejects),
      action: t('admin.ops.runtime.continuation.actions.clientPayload'),
      tone:
        derived.validationRejects >= CONTINUATION_RULE_THRESHOLDS.validationRejectEscalate
          ? 'warning'
          : derived.validationRejects >= CONTINUATION_RULE_THRESHOLDS.validationRejectWarn
            ? 'info'
            : 'neutral',
      hit: derived.validationRejects >= CONTINUATION_RULE_THRESHOLDS.validationRejectWarn
    },
    {
      key: 'fail_close',
      label: t('admin.ops.runtime.continuation.rules.failClose'),
      threshold: t('admin.ops.runtime.continuation.rules.thresholdAtLeast', {
        count: CONTINUATION_RULE_THRESHOLDS.failCloseWarn
      }),
      observed: formatCount(derived.failClosed),
      action: t('admin.ops.runtime.continuation.actions.localAnchor'),
      tone: derived.failClosed >= CONTINUATION_RULE_THRESHOLDS.failCloseWarn ? 'warning' : 'neutral',
      hit: derived.failClosed >= CONTINUATION_RULE_THRESHOLDS.failCloseWarn
    },
    {
      key: 'recovery',
      label: t('admin.ops.runtime.continuation.rules.recoveryPressure'),
      threshold: t('admin.ops.runtime.continuation.rules.thresholdRecovery', {
        low: CONTINUATION_RULE_THRESHOLDS.recoveryNotice,
        high: CONTINUATION_RULE_THRESHOLDS.recoveryWarn
      }),
      observed: formatCount(derived.recoveries),
      action:
        derived.selfContainedRecoveries >= CONTINUATION_RULE_THRESHOLDS.selfContainedNotice
          ? t('admin.ops.runtime.continuation.actions.healthy')
          : t('admin.ops.runtime.continuation.actions.notInStrongMode'),
      tone:
        derived.recoveries >= CONTINUATION_RULE_THRESHOLDS.recoveryWarn
          ? 'warning'
          : derived.recoveries >= CONTINUATION_RULE_THRESHOLDS.recoveryNotice
            ? 'info'
            : 'success',
      hit: derived.recoveries >= CONTINUATION_RULE_THRESHOLDS.recoveryNotice
    },
    {
      key: 'persistence',
      label: t('admin.ops.runtime.continuation.rules.persistence'),
      threshold: t('admin.ops.runtime.continuation.rules.thresholdPersistence', {
        count: CONTINUATION_RULE_THRESHOLDS.persistenceWarnMinCount
      }),
      observed: `${derived.persistentCount}/3`,
      action: t('admin.ops.runtime.continuation.actions.persistence'),
      tone:
        derived.persistentCount < CONTINUATION_RULE_THRESHOLDS.persistenceWarnMinCount
          ? 'warning'
          : derived.persistentCount < 3
            ? 'info'
            : 'success',
      hit: derived.persistentCount < 3
    },
    {
      key: 'capacity',
      label: t('admin.ops.runtime.continuation.rules.capacity'),
      threshold: t('admin.ops.runtime.continuation.rules.thresholdCapacity', {
        info: CONTINUATION_RULE_THRESHOLDS.saturationInfoPercent,
        warn: CONTINUATION_RULE_THRESHOLDS.saturationWarnPercent
      }),
      observed: formatPercent(derived.saturationPercent),
      action: t('admin.ops.runtime.continuation.actions.capacity'),
      tone:
        derived.saturationPercent >= CONTINUATION_RULE_THRESHOLDS.saturationWarnPercent
          ? 'warning'
          : derived.saturationPercent >= CONTINUATION_RULE_THRESHOLDS.saturationInfoPercent
            ? 'info'
            : 'success',
      hit: derived.saturationPercent >= CONTINUATION_RULE_THRESHOLDS.saturationInfoPercent
    },
    {
      key: 'conn_churn',
      label: t('admin.ops.runtime.continuation.rules.connChurn'),
      threshold: t('admin.ops.runtime.continuation.rules.thresholdAtLeast', {
        count: CONTINUATION_RULE_THRESHOLDS.connChurnWarn
      }),
      observed: formatCount(derived.connDeleteCount),
      action: t('admin.ops.runtime.continuation.actions.capacity'),
      tone: derived.connDeleteCount >= CONTINUATION_RULE_THRESHOLDS.connChurnWarn ? 'info' : 'success',
      hit: derived.connDeleteCount >= CONTINUATION_RULE_THRESHOLDS.connChurnWarn
    }
  ]

  return rules
})

const continuationActionItems = computed<string[]>(() => {
  const snapshot = continuationSnapshot.value
  const derived = continuationDerivedStats.value
  if (!snapshot || !derived) return []
  const config = snapshot.config
  const actions: string[] = []

  if (!config.enabled) {
    actions.push(t('admin.ops.runtime.continuation.actions.gatewayDisabled'))
  }
  if (config.force_http || !config.responses_websockets_v2) {
    actions.push(t('admin.ops.runtime.continuation.actions.notInStrongMode'))
  }
  if (derived.validationRejects >= CONTINUATION_RULE_THRESHOLDS.validationRejectWarn) {
    actions.push(t('admin.ops.runtime.continuation.actions.clientPayload'))
  }
  if (derived.failClosed >= CONTINUATION_RULE_THRESHOLDS.failCloseWarn) {
    actions.push(t('admin.ops.runtime.continuation.actions.localAnchor'))
  }
  if (
    derived.persistentCount < 3
  ) {
    actions.push(t('admin.ops.runtime.continuation.actions.persistence'))
  }
  if (derived.saturationPercent >= CONTINUATION_RULE_THRESHOLDS.saturationWarnPercent) {
    actions.push(t('admin.ops.runtime.continuation.actions.capacity'))
  } else if (derived.connDeleteCount >= CONTINUATION_RULE_THRESHOLDS.connChurnWarn) {
    actions.push(t('admin.ops.runtime.continuation.actions.capacity'))
  }
  if (derived.recoveries >= CONTINUATION_RULE_THRESHOLDS.recoveryWarn) {
    actions.push(t('admin.ops.runtime.continuation.actions.notInStrongMode'))
  } else if (derived.selfContainedRecoveries >= CONTINUATION_RULE_THRESHOLDS.selfContainedNotice) {
    actions.push(t('admin.ops.runtime.continuation.actions.healthy'))
  } else if (snapshot.state.local_max_entries_per_map > 0) {
    if (derived.saturationPercent >= CONTINUATION_RULE_THRESHOLDS.saturationWarnPercent) {
      actions.push(t('admin.ops.runtime.continuation.actions.capacity'))
    }
  }
  if (actions.length === 0) {
    actions.push(t('admin.ops.runtime.continuation.actions.healthy'))
  }

  return Array.from(new Set(actions))
})

async function loadSettings() {
  loading.value = true
  continuationLoadFailed.value = false
  try {
    const [alertResult, continuationResult] = await Promise.allSettled([
      opsAPI.getAlertRuntimeSettings(),
      opsAPI.getRuntimeContinuationSnapshot()
    ])

    if (alertResult.status === 'fulfilled') {
      alertSettings.value = alertResult.value
    } else {
      alertSettings.value = null
      throw alertResult.reason
    }

    if (continuationResult.status === 'fulfilled') {
      continuationRuntime.value = continuationResult.value
    } else {
      continuationLoadFailed.value = true
      console.error('[OpsRuntimeSettingsCard] Failed to load continuation runtime snapshot', continuationResult.reason)
      appStore.showWarning(
        (continuationResult.reason as any)?.response?.data?.detail || t('admin.ops.runtime.continuation.loadFailed')
      )
    }
  } catch (err: any) {
    console.error('[OpsRuntimeSettingsCard] Failed to load runtime settings', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.runtime.loadFailed'))
  } finally {
    loading.value = false
  }
}

function openAlertEditor() {
  if (!alertSettings.value) return
  draftAlert.value = JSON.parse(JSON.stringify(alertSettings.value))

  // Backwards-compat: ensure nested settings exist even if API payload is older.
  if (draftAlert.value) {
    if (!draftAlert.value.distributed_lock) {
      draftAlert.value.distributed_lock = { enabled: true, key: 'ops:alert:evaluator:leader', ttl_seconds: 30 }
    }
    if (!draftAlert.value.silencing) {
      draftAlert.value.silencing = { enabled: false, global_until_rfc3339: '', global_reason: '', entries: [] }
    }
    if (!Array.isArray(draftAlert.value.silencing.entries)) {
      draftAlert.value.silencing.entries = []
    }
    if (!draftAlert.value.thresholds) {
      draftAlert.value.thresholds = {
        sla_percent_min: 99.5,
        ttft_p99_ms_max: 500,
        request_error_rate_percent_max: 5,
        upstream_error_rate_percent_max: 5
      }
    }
  }

  showAlertEditor.value = true
}

function addSilenceEntry() {
  if (!draftAlert.value) return
  if (!draftAlert.value.silencing) {
    draftAlert.value.silencing = { enabled: true, global_until_rfc3339: '', global_reason: '', entries: [] }
  }
  if (!Array.isArray(draftAlert.value.silencing.entries)) {
    draftAlert.value.silencing.entries = []
  }
  draftAlert.value.silencing.entries.push({
    rule_id: undefined,
    severities: [],
    until_rfc3339: '',
    reason: ''
  })
}

function removeSilenceEntry(index: number) {
  if (!draftAlert.value?.silencing?.entries) return
  draftAlert.value.silencing.entries.splice(index, 1)
}

function updateSilenceEntryRuleId(index: number, raw: string) {
  const entries = draftAlert.value?.silencing?.entries
  if (!entries || !entries[index]) return
  const trimmed = raw.trim()
  if (!trimmed) {
    delete (entries[index] as any).rule_id
    return
  }
  const n = Number.parseInt(trimmed, 10)
  ;(entries[index] as any).rule_id = Number.isFinite(n) ? n : undefined
}

function updateSilenceEntrySeverities(index: number, raw: string) {
  const entries = draftAlert.value?.silencing?.entries
  if (!entries || !entries[index]) return
  const parts = raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
  ;(entries[index] as any).severities = normalizeSeverities(parts)
}

async function saveAlertSettings() {
  if (!draftAlert.value) return
  if (!alertValidation.value.valid) {
    appStore.showError(alertValidation.value.errors[0] || t('admin.ops.runtime.validation.invalid'))
    return
  }

  saving.value = true
  try {
    alertSettings.value = await opsAPI.updateAlertRuntimeSettings(draftAlert.value)
    showAlertEditor.value = false
    appStore.showSuccess(t('admin.ops.runtime.saveSuccess'))
  } catch (err: any) {
    console.error('[OpsRuntimeSettingsCard] Failed to save alert runtime settings', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.runtime.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadSettings()
})
</script>

<template>
  <div class="rounded-3xl bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
    <div class="mb-4 flex items-start justify-between gap-4">
      <div>
        <h3 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.ops.runtime.title') }}</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.description') }}</p>
      </div>
      <button
        class="flex items-center gap-1.5 rounded-lg bg-gray-100 px-3 py-1.5 text-xs font-bold text-gray-700 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
        :disabled="loading"
        @click="loadSettings"
      >
        <svg class="h-3.5 w-3.5" :class="{ 'animate-spin': loading }" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
        </svg>
        {{ t('common.refresh') }}
      </button>
    </div>

    <div v-if="!hasAnyRuntimeData" class="text-sm text-gray-500 dark:text-gray-400">
      <span v-if="loading">{{ t('admin.ops.runtime.loading') }}</span>
      <span v-else>{{ t('admin.ops.runtime.noData') }}</span>
    </div>

    <div v-else class="space-y-6">
      <div v-if="alertSettings" class="rounded-2xl bg-gray-50 p-4 dark:bg-dark-700/50">
        <div class="mb-3 flex items-center justify-between">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.runtime.alertTitle') }}</h4>
          <button class="btn btn-sm btn-secondary" @click="openAlertEditor">{{ t('common.edit') }}</button>
        </div>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div class="text-xs text-gray-600 dark:text-gray-300">
            {{ t('admin.ops.runtime.evalIntervalSeconds') }}:
            <span class="ml-1 font-medium text-gray-900 dark:text-white">{{ alertSettings.evaluation_interval_seconds }}s</span>
          </div>
          <div
            v-if="alertSettings.silencing?.enabled && alertSettings.silencing.global_until_rfc3339"
            class="text-xs text-gray-600 dark:text-gray-300 md:col-span-2"
          >
            {{ t('admin.ops.runtime.silencing.globalUntil') }}:
            <span class="ml-1 font-mono text-gray-900 dark:text-white">{{ alertSettings.silencing.global_until_rfc3339 }}</span>
          </div>

          <details class="col-span-1 md:col-span-2">
            <summary class="cursor-pointer text-xs font-medium text-blue-600 hover:text-blue-700 dark:text-blue-400">
              {{ t('admin.ops.runtime.showAdvancedDeveloperSettings') }}
            </summary>
            <div class="mt-2 grid grid-cols-1 gap-3 rounded-lg bg-gray-100 p-3 dark:bg-dark-800 md:grid-cols-2">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.lockEnabled') }}:
                <span class="ml-1 font-mono text-gray-700 dark:text-gray-300">{{ alertSettings.distributed_lock.enabled }}</span>
              </div>
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.lockKey') }}:
                <span class="ml-1 font-mono text-gray-700 dark:text-gray-300">{{ alertSettings.distributed_lock.key }}</span>
              </div>
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.lockTTLSeconds') }}:
                <span class="ml-1 font-mono text-gray-700 dark:text-gray-300">{{ alertSettings.distributed_lock.ttl_seconds }}s</span>
              </div>
            </div>
          </details>
        </div>
      </div>

      <div v-if="continuationRuntime && continuationSnapshot" class="rounded-2xl bg-gray-50 p-4 dark:bg-dark-700/50">
        <div class="mb-4 flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div>
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.runtime.continuation.title') }}</h4>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.ops.runtime.continuation.description') }}
            </p>
            <div class="mt-2 text-[11px] text-gray-500 dark:text-gray-400">
              {{ t('admin.ops.runtime.continuation.source') }}:
              <span class="ml-1 font-mono text-gray-700 dark:text-gray-200">{{ continuationRuntime.source }}</span>
            </div>
          </div>

          <div class="flex flex-wrap gap-2">
            <span
              v-for="item in continuationModeBadges"
              :key="item.key"
              class="inline-flex items-center gap-2 rounded-full px-3 py-1 text-xs font-medium"
              :class="metricToneClass(item.tone)"
            >
              <span class="text-[11px] uppercase tracking-wide opacity-80">{{ item.label }}</span>
              <span>{{ item.value }}</span>
            </span>
          </div>
        </div>

        <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
          <div
            v-for="item in continuationSummaryItems"
            :key="item.key"
            class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {{ item.label }}
              </div>
              <span class="rounded-full px-2 py-0.5 text-[10px] font-semibold" :class="metricToneClass(item.tone)">
                {{ item.value }}
              </span>
            </div>
            <div class="mt-3 text-xs leading-5 text-gray-600 dark:text-gray-300">
              {{ item.description }}
            </div>
          </div>
        </div>

        <div class="mb-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="mb-3 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('admin.ops.runtime.continuation.diagnosisRulesTitle') }}
          </div>
          <div class="space-y-3">
            <div
              v-for="item in continuationDiagnosisRules"
              :key="item.key"
              class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
              :class="{
                'bg-white dark:bg-dark-800': item.tone === 'neutral',
                'bg-blue-50 dark:bg-blue-950/20': item.tone === 'info',
                'bg-emerald-50 dark:bg-emerald-950/20': item.tone === 'success',
                'bg-amber-50 dark:bg-amber-950/20': item.tone === 'warning'
              }"
            >
              <div class="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between">
                <div>
                  <div class="text-xs font-semibold text-gray-900 dark:text-white">{{ item.label }}</div>
                  <div class="mt-1 text-[11px] text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.runtime.continuation.ruleThresholdLabel') }}:
                    <span class="ml-1 font-mono">{{ item.threshold }}</span>
                  </div>
                  <div class="mt-1 text-[11px] text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.runtime.continuation.ruleObservedLabel') }}:
                    <span class="ml-1 font-mono">{{ item.observed }}</span>
                  </div>
                </div>
                <div class="flex flex-wrap items-center gap-2">
                  <span class="rounded-full px-2 py-0.5 text-[10px] font-semibold" :class="metricToneClass(item.tone)">
                    {{
                      item.hit
                        ? t('admin.ops.runtime.continuation.ruleHit')
                        : t('admin.ops.runtime.continuation.ruleNotHit')
                    }}
                  </span>
                </div>
              </div>
              <div class="mt-2 text-xs text-gray-700 dark:text-gray-200">
                {{ item.action }}
              </div>
            </div>
          </div>
        </div>

        <div class="mb-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('admin.ops.runtime.continuation.nextActionsTitle') }}
          </div>
          <ul class="space-y-2 text-xs text-gray-700 dark:text-gray-200">
            <li v-for="item in continuationActionItems" :key="item" class="flex items-start gap-2">
              <span class="mt-[3px] inline-block h-1.5 w-1.5 rounded-full bg-blue-500"></span>
              <span>{{ item }}</span>
            </li>
          </ul>
        </div>

        <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-5">
          <div
            v-for="item in continuationCounterItems"
            :key="item.key"
            class="rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-800"
          >
            <div class="text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              {{ item.label }}
            </div>
            <div class="mt-2 flex items-end justify-between gap-3">
              <div class="text-xl font-bold text-gray-900 dark:text-white">{{ item.value }}</div>
              <span class="rounded-full px-2 py-0.5 text-[10px] font-semibold" :class="metricToneClass(item.tone)">
                {{ t('admin.ops.runtime.continuation.countersTag') }}
              </span>
            </div>
          </div>
        </div>

        <div
          v-if="continuationLoadFailed"
          class="mt-4 rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-200"
        >
          {{ t('admin.ops.runtime.continuation.partialDataHint') }}
        </div>

        <details class="mt-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <summary class="cursor-pointer text-xs font-semibold text-gray-700 dark:text-gray-200">
            {{ t('admin.ops.runtime.continuation.advancedSummary') }}
          </summary>

          <div class="mt-4 space-y-5">
            <div>
              <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.continuation.sections.config') }}
              </div>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-5">
                <div
                  v-for="item in continuationConfigItems"
                  :key="item.key"
                  class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900"
                >
                  <div class="text-gray-500 dark:text-gray-400">{{ item.label }}</div>
                  <div class="mt-1 font-mono text-gray-900 dark:text-white">{{ item.value }}</div>
                </div>
              </div>
            </div>

            <div>
              <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.continuation.sections.stateEntries') }}
              </div>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-5">
                <div
                  v-for="item in continuationStateEntryItems"
                  :key="item.key"
                  class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900"
                >
                  <div class="text-gray-500 dark:text-gray-400">{{ item.label }}</div>
                  <div class="mt-1 font-mono text-gray-900 dark:text-white">{{ item.value }}</div>
                </div>
              </div>
            </div>

            <div>
              <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.continuation.sections.persistence') }}
              </div>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <div
                  v-for="item in continuationStatePersistenceItems"
                  :key="item.key"
                  class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900"
                >
                  <div class="text-gray-500 dark:text-gray-400">{{ item.label }}</div>
                  <div class="mt-2 inline-flex rounded-full px-2 py-0.5 text-[10px] font-semibold" :class="metricToneClass(item.tone)">
                    {{ item.value }}
                  </div>
                </div>
              </div>
            </div>

            <div>
              <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.continuation.sections.churn') }}
              </div>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-5">
                <div
                  v-for="item in continuationStateChurnItems"
                  :key="item.key"
                  class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900"
                >
                  <div class="text-gray-500 dark:text-gray-400">{{ item.label }}</div>
                  <div class="mt-1 font-mono text-gray-900 dark:text-white">{{ item.value }}</div>
                </div>
              </div>
            </div>

            <div>
              <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.runtime.continuation.sections.limits') }}
              </div>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
                <div
                  v-for="item in continuationStateLimitItems"
                  :key="item.key"
                  class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900"
                >
                  <div class="text-gray-500 dark:text-gray-400">{{ item.label }}</div>
                  <div class="mt-1 font-mono text-gray-900 dark:text-white">{{ item.value }}</div>
                </div>
              </div>
            </div>
          </div>
        </details>
      </div>

      <div
        v-else-if="continuationLoadFailed"
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-200"
      >
        {{ t('admin.ops.runtime.continuation.loadFailedInline') }}
      </div>
    </div>
  </div>

  <BaseDialog :show="showAlertEditor" :title="t('admin.ops.runtime.alertTitle')" width="extra-wide" @close="showAlertEditor = false">
    <div v-if="draftAlert" class="space-y-4">
      <div
        v-if="!alertValidation.valid"
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-200"
      >
        <div class="font-bold">{{ t('admin.ops.runtime.validation.title') }}</div>
        <ul class="mt-1 list-disc space-y-1 pl-4">
          <li v-for="msg in alertValidation.errors" :key="msg">{{ msg }}</li>
        </ul>
      </div>

      <div>
        <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.evalIntervalSeconds') }}</div>
        <input
          v-model.number="draftAlert.evaluation_interval_seconds"
          type="number"
          min="1"
          max="86400"
          class="input"
          :aria-invalid="!alertValidation.valid"
        />
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.ops.runtime.evalIntervalHint') }}</p>
      </div>

      <div class="rounded-2xl bg-gray-50 p-4 dark:bg-dark-700/50">
        <div class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.runtime.metricThresholds') }}</div>
        <p class="mb-4 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.metricThresholdsHint') }}</p>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.slaMinPercent') }}</div>
            <input
              v-model.number="draftAlert.thresholds.sla_percent_min"
              type="number"
              min="0"
              max="100"
              step="0.1"
              class="input"
              placeholder="99.5"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.slaMinPercentHint') }}</p>
          </div>



          <div>
            <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.ttftP99MaxMs') }}</div>
            <input
              v-model.number="draftAlert.thresholds.ttft_p99_ms_max"
              type="number"
              min="0"
              step="100"
              class="input"
              placeholder="500"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.ttftP99MaxMsHint') }}</p>
          </div>

          <div>
            <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.requestErrorRateMaxPercent') }}</div>
            <input
              v-model.number="draftAlert.thresholds.request_error_rate_percent_max"
              type="number"
              min="0"
              max="100"
              step="0.1"
              class="input"
              placeholder="5"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.requestErrorRateMaxPercentHint') }}</p>
          </div>

          <div>
            <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.upstreamErrorRateMaxPercent') }}</div>
            <input
              v-model.number="draftAlert.thresholds.upstream_error_rate_percent_max"
              type="number"
              min="0"
              max="100"
              step="0.1"
              class="input"
              placeholder="5"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.upstreamErrorRateMaxPercentHint') }}</p>
          </div>
        </div>
      </div>

      <div class="rounded-2xl bg-gray-50 p-4 dark:bg-dark-700/50">
        <div class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.runtime.silencing.title') }}</div>

        <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input v-model="draftAlert.silencing.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300" />
          <span>{{ t('admin.ops.runtime.silencing.enabled') }}</span>
        </label>

        <div v-if="draftAlert.silencing.enabled" class="mt-4 space-y-4">
          <div>
            <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.silencing.globalUntil') }}</div>
            <input
              v-model="draftAlert.silencing.global_until_rfc3339"
              type="text"
              class="input font-mono text-sm"
                      placeholder="2026-01-05T00:00:00Z"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.silencing.untilHint') }}</p>
          </div>

          <div>
            <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.silencing.reason') }}</div>
            <input
              v-model="draftAlert.silencing.global_reason"
              type="text"
              class="input"
              :placeholder="t('admin.ops.runtime.silencing.reasonPlaceholder')"
            />
          </div>

          <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
            <div class="flex items-start justify-between gap-4">
              <div>
                <div class="text-xs font-bold text-gray-900 dark:text-white">{{ t('admin.ops.runtime.silencing.entries.title') }}</div>
                <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.ops.runtime.silencing.entries.hint') }}</p>
              </div>
              <button class="btn btn-sm btn-secondary" type="button" @click="addSilenceEntry">
                {{ t('admin.ops.runtime.silencing.entries.add') }}
              </button>
            </div>

            <div v-if="!draftAlert.silencing.entries?.length" class="mt-3 rounded-lg bg-gray-50 p-3 text-xs text-gray-500 dark:bg-dark-900 dark:text-gray-400">
              {{ t('admin.ops.runtime.silencing.entries.empty') }}
            </div>

            <div v-else class="mt-4 space-y-4">
              <div
                v-for="(entry, idx) in draftAlert.silencing.entries"
                :key="idx"
                class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-900"
              >
                <div class="mb-3 flex items-center justify-between">
                  <div class="text-xs font-bold text-gray-900 dark:text-white">
                    {{ t('admin.ops.runtime.silencing.entries.entryTitle', { n: idx + 1 }) }}
                  </div>
                  <button class="btn btn-sm btn-danger" type="button" @click="removeSilenceEntry(idx)">{{ t('common.delete') }}</button>
                </div>

                <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
                  <div>
                    <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.silencing.entries.ruleId') }}</div>
                    <input
                      :value="typeof (entry as any).rule_id === 'number' ? String((entry as any).rule_id) : ''"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="t('admin.ops.runtime.silencing.entries.ruleIdPlaceholder')"
                      @input="updateSilenceEntryRuleId(idx, ($event.target as HTMLInputElement).value)"
                    />
                  </div>

                  <div>
                    <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.silencing.entries.severities') }}</div>
                    <input
                      :value="Array.isArray((entry as any).severities) ? (entry as any).severities.join(', ') : ''"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="t('admin.ops.runtime.silencing.entries.severitiesPlaceholder')"
                      @input="updateSilenceEntrySeverities(idx, ($event.target as HTMLInputElement).value)"
                    />
                  </div>

                  <div>
                    <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.silencing.entries.until') }}</div>
                    <input
                      v-model="(entry as any).until_rfc3339"
                      type="text"
                      class="input font-mono text-sm"
              placeholder="2026-01-05T00:00:00Z"
                    />
                  </div>

                  <div>
                    <div class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.runtime.silencing.entries.reason') }}</div>
                    <input
                      v-model="(entry as any).reason"
                      type="text"
                      class="input"
                      :placeholder="t('admin.ops.runtime.silencing.reasonPlaceholder')"
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <details class="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-800">
        <summary class="cursor-pointer text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.ops.runtime.advancedSettingsSummary') }}</summary>
        <div class="mt-3 grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="inline-flex items-center gap-2 text-xs text-gray-700 dark:text-gray-300">
              <input v-model="draftAlert.distributed_lock.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300" />
              <span>{{ t('admin.ops.runtime.lockEnabled') }}</span>
            </label>
          </div>
          <div class="md:col-span-2">
            <div class="mb-1 text-xs font-medium text-gray-500">{{ t('admin.ops.runtime.lockKey') }}</div>
            <input v-model="draftAlert.distributed_lock.key" type="text" class="input text-xs font-mono" />
            <p v-if="draftAlert.distributed_lock.enabled" class="mt-1 text-[11px] text-gray-500 dark:text-gray-400">
              {{ t('admin.ops.runtime.validation.lockKeyHint', { prefix: 'ops:' }) }}
            </p>
          </div>
          <div>
            <div class="mb-1 text-xs font-medium text-gray-500">{{ t('admin.ops.runtime.lockTTLSeconds') }}</div>
            <input v-model.number="draftAlert.distributed_lock.ttl_seconds" type="number" min="1" max="86400" class="input text-xs font-mono" />
          </div>
        </div>
      </details>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button class="btn btn-secondary" @click="showAlertEditor = false">{{ t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="saving || !alertValidation.valid" @click="saveAlertSettings">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

