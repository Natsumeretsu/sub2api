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
| HTTP `/v1/responses` | `degraded` | 不等同于 WSv2；但对强 continuation cohort，请求进入 handler 时会优先从共享会话状态回填 `previous_response_id`，并把账号选择收紧到 `WSv2` 能力面 | 以 handler 前置校验为主；强 cohort 会优先保链，弱会话仍可能 strip | 请求体 + 共享会话状态 + 上游 | 不承诺与 WSv2 等价，但不再把强会话静默打回弱 continuation |
| HTTP `/v1/responses/compact` | `degraded` | compact 单独规范化 | 不做 replay merge | compact 请求体 + 上游 | 不能假装与普通 `/responses` 完全等价 |
| WSv1 / legacy websocket | `degraded` | 不在当前 hardening 主战场 | 不承诺与 WSv2 同等级恢复 | 旧状态面 | 仅保守兼容 |
| 非原生 upstream / 跨协议变换 continuation | `unsupported-by-contract` | 不列入当前强承诺面 | 不在当前 10 patch 的 correctness 保证范围内 | 具体 adapter 自身 | 后续需单独 matrix |

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

### 3.2 当前明确不承诺的点

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
  当前返回体已经包含 `counters`、`config`、`state` 三层，其中 `state` 会暴露本地 `session state / TTL / conn churn` 相关快照

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

- 把当前观测接到更易读的 admin/runtime 页面或 dashboard，而不只是原始 JSON
- 为 `state` 快照继续补充 TTL 命中率、连接回收与跨实例恢复相关指标

### Phase 3

- 区分 correctness state 与 performance state
- 明确 `prompt_cache_key`、cache-aware routing、连接复用的职责边界

### Phase 4

- 对 `compact` 与非原生 upstream 单独出 matrix，而不是混在同一承诺表里
