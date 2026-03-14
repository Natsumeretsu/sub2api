# Responses Continuation Capability Matrix（Fork 版）

本文档把当前 fork 在不同 transport、不同请求面与不同恢复策略下的 continuation 能力明确分层，避免把所有 `/responses` 路径混成一个承诺面。

适用目标：

- 判断当前请求实际跑在 `WSv2`、HTTP 还是其他降级面
- 明确 `previous_response_id` 是否会被保留
- 明确 `function_call_output` 是本地校验、受控恢复，还是直接降级
- 为后续指标、压测和 UX 调优提供统一术语

不适用目标：

- 对外宣传“全路径等价”
- 把未验证组合态包装成已支持
- 用 capability matrix 替代真实压测与指标

## 1. 术语

- `strong`
  - continuation 语义是当前 fork 的主承诺面
  - 有明确本地状态和恢复路径
  - 不依赖 transcript replay

- `degraded`
  - 可以工作，但 continuation 语义不是最强
  - 可能会丢失 `previous_response_id`、依赖 HTTP、或只做有限恢复

- `fail-close`
  - 本地已知请求不完整或恢复条件不足
  - 不再盲目上推，直接返回明确错误

- `self-contained retry`
  - 本地没有锚点，但请求自带完整工具上下文
  - 允许移除陈旧 `previous_response_id` 后单次重试

## 2. 能力矩阵

| Surface | 当前状态 | `previous_response_id` | 工具回合策略 | 状态来源 | 备注 |
| --- | --- | --- | --- | --- | --- |
| WSv2 `ctx_pool/passthrough` 普通 turn | `strong` | 默认保留；必要时可对齐或单次降级 | 文本 turn 可做受控恢复 | 本地 turn + 会话状态 + 连接提示 | 当前主承诺面 |
| WSv2 `store=false` + `function_call_output` | `strong` | 优先对齐；必要时单次 fresh-conn 恢复 | 本地校验 -> 有锚点恢复 -> 自包含单次重试 -> 否则 fail-close | `last_response_id`、`turn_state`、连接亲和 | 当前 continuation hardening 主覆盖面；会话状态从共享缓存回填本地时会跟随共享剩余 TTL |
| WSv2 `store=true` | `degraded-strong` | 允许依赖上游 history | 本地仍做基础校验，但更偏透传 | 上游 history + 本地辅助状态 | 语义更依赖上游 |
| HTTP `/v1/responses` | `degraded` | 不等同于 WSv2；对强 continuation cohort，请求进入 handler 时仍会优先从共享会话状态回填 `previous_response_id`，但 handler 会再检查所选 HTTP fallback surface 是否真的支持该参数 | 以 handler 前置校验为主；强 cohort 只会在 HTTP surface 明确支持 `previous_response_id` 时继续保链，否则直接显式 `503` 软中断；弱会话仍可能 strip；一旦已经向下游写出字节，不再静默重放同一 turn | 请求体 + 共享会话状态 + 账号 capability + 上游 | 不承诺与 WSv2 等价，但不再把 strong anchored turn 静默打进不支持 `previous_response_id` 的 HTTP fallback；若客户端未显式提供 `client_request_id`，则 turn key 退回为 `session_hash + previous_response_id + payload fingerprint` 派生键 |
| HTTP `/v1/responses/compact` | `degraded` | compact 单独规范化 | 不做 replay merge；非流式成功响应若为空体、半截 SSE、或缺 final response payload，会先进入协议级 failover，再受控重试 | compact 请求体 + 上游 | 不能假装与普通 `/responses` 完全等价，也不能再把 `200 + 空 body` 直接回给客户端 |
| WSv1 / legacy websocket | `degraded` | 不在当前 hardening 主战场 | 不承诺与 WSv2 同等级恢复 | 旧状态面 | 仅保守兼容 |
| 非原生 upstream / 跨协议变换 continuation | `unsupported-by-contract` | 不列入当前强承诺面 | 不在当前 fork 的 continuation correctness 保证范围内 | 具体 adapter 自身 | 后续需单独 matrix |

## 3. 当前本地判断

### 3.1 当前最强承诺面

当前 fork 真正追求高 UX 与高 correctness 的，是这一条：

- `responses_websockets_v2`
- `store=false`
- `function_call_output`
- strict-affinity / preflight ping
- `previous_response_not_found`

这条路径已经具备：

1. 本地 `function_call_output` 上下文校验
2. stale `previous_response_id` 对齐
3. 本地锚点已对齐但 upstream 仍判 stale 时，仅自包含 payload 允许单次自包含重试
4. 无本地锚点但 payload 自包含时的单次自包含重试
5. 无锚点且不自包含时的明确 fail-close
6. 会话级 `last_response_id` / `turn_state` 状态面
7. 共享缓存回填本地时尊重共享剩余 TTL，不额外放大本地寿命
8. 当 `session_hash -> account_id` 缺失但共享 `last_response_id -> account_id` 仍在时，可恢复 sticky account 并回填会话粘连
9. 不做 transcript replay

### 3.1.1 账号能力分层的当前真相

当前 fork 对 OpenAI 账号的强弱分层，已经不再只看配置愿望，而是区分“真实 transport capability”：

- OAuth 账号默认可进入 `strong cohort`
- API-key relay 账号默认只进入 `degraded-only`
- API-key relay 只有显式声明真实 `/responses` WS transport capability 时，才允许进入 `strong cohort`

当前 live capability 快照因此已经收敛为：

- `Team号池`：`11 strong + 1 degraded-only(PackyCode)`
- `Private`：`0 strong + 1 degraded-only(PackyCode)`

这一步的目的，是阻断“把不支持 `/responses` WS transport 的 relay 账号误当成 strong account”这一类根因，避免账号切换时把强 continuation 会话切碎、把缓存亲和打掉、再把用户体验问题伪装成普通 fallback。

### 3.2 HTTP fallback capability 也必须单独建模

除了 `WS continuation capability`，live 日志已经证明还必须单独建模：

- `HTTP previous_response capability`

原因很直接：

- 同一个 OAuth 账号可能支持 `WSv2` continuation
- 但同一个 OAuth 账号的 HTTP passthrough fallback surface 仍然会返回 `Unsupported parameter: previous_response_id`

因此当前 fork 的规则是：

- OpenAI API-key 官方 surface 默认视为支持 HTTP `previous_response_id`
- OpenAI OAuth passthrough HTTP surface 默认视为不支持，除非显式声明 capability
- 一旦当前 turn 仍然是 response-bound continuation，而选中的 HTTP surface 又不支持 `previous_response_id`，handler 会直接软中断，不再把 anchored turn 继续上推
### 3.3 当前明确不承诺的点

- HTTP 与 WSv2 语义完全等价
- 非原生 upstream continuation 与 native OpenAI continuation 完全等价
- 进程内 `conn_id` 状态在跨实例、重启、长 TTL 过期后仍保持强一致
- `/compact` 与普通 `/responses` 的恢复路径完全一致

## 4. 本地已落地的可观测项

当前 fork 已落地轻量 continuation 计数，覆盖这些行为：

- handler 本地拒绝：
  - `function_call_output_missing_call_id`
  - `function_call_output_missing_item_reference`
- 中途降级与会话恢复：
  - `ws_to_http_mid_session`
  - `previous_response_recovered_from_session`
  - `previous_response_stripped_mid_session`
- `account_switch_with_cache_drop`
- `strong_cohort_fallback`
- `cache_affinity_selection`
- `duplicate_turn_retry_blocked_after_emit`
- `emitted_bytes_before_retry`
- `turn_reuse_processing_conflict`
- `turn_reuse_emitted_conflict`
- `turn_reuse_completed_conflict`
- `previous_response_not_found`：
  - `align_previous_response_id`
  - `drop_previous_response_id`
  - `drop_previous_response_id_self_contained`
  - `missing_local_anchor fail-close`
  - `stale_local_anchor fail-close`
- `session_hash -> last_response_id -> account_id`：
  - `sticky session rebind from shared response state`
- preflight ping fail：
  - `align_previous_response_id`
  - `drop_previous_response_id`
  - `drop_previous_response_id_self_contained`
  - `missing_local_anchor fail-close`
  - `stale_local_anchor fail-close`

当前这些统计还是进程内轻量计数，定位目的是：

- 回归测试验证
- 运行时快速诊断
- 提供一个只读 admin/debug surface：`GET /api/v1/admin/ops/runtime/continuation`
  当前返回体已经包含 `counters`、`config`、`state`、`capability` 四层：
  - `state` 暴露本地 `session state / TTL / conn churn` 相关快照
  - `capability` 暴露可调度 OpenAI 账号的 `compact capability / strong cohort / degraded cohort` 按组汇总，可直接解释为什么某个 group 当前只能 fast-fail

当前还**没有**承诺：

- Prometheus 级监控
- 跨实例汇总
- 长周期持久化指标

## 5. 与用户体验优先的关系

本 matrix 的核心不是“哪里最纯”，而是“哪里最能稳定地给用户正确又不中断的体验”。

因此当前优先级是：

1. 不让本地代理自造 continuation 错误
2. 有充分依据时优先保住当轮请求
3. 条件不足时再明确 fail-close

这也是为什么当前强承诺面优先是 `WSv2`，而不是把所有 transport 统一包装成同一种 continuation。

## 6. 后续落实顺序

### Phase 2 当前步

- 落 capability matrix
- 落最小 continuation 统计

### Phase 2 下一步

- 继续补充更细的 `TTL hit / conn churn / cross-instance recovery` 指标，而不只是账号能力摘要
- 为 capability 视图增加更强的当前 group / 当前 cohort 可用性提示，减少管理员人工对照账号池的成本

### Phase 3

- 区分 correctness state 与 performance state
- 明确 `prompt_cache_key`、cache-aware routing、连接复用的职责边界

### Phase 4

- 对 `compact` 与非原生 upstream 单独出 matrix，而不是混在同一承诺表里
