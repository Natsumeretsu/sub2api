# Responses Continuation Capability Matrix（Fork 版）

本文档把当前 fork 在不同 transport、不同请求面与不同恢复策略下的 continuation 能力明确分层，避免把所有 `/responses` 路径混成一个承诺面。

适用目标：

- 判断当前请求实际跑在 `WSv2`、HTTP 还是其他降级面
- 明确 `previous_response_id` 是否会被保留
- 明确 `function_call_output` 是本地校验、受控恢复，还是直接降级
- 为后续指标、压测、调度与成本归因提供统一术语

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
| HTTP `/v1/responses` | `degraded` | 不等同于 WSv2；对强 continuation cohort，请求进入 handler 时仍会优先从共享会话状态回填 `previous_response_id`，但 anchored HTTP turn 不再先被 `requiredTransport=WSv2` 提前筛空，而是先选到同账号 candidate 再判断该 HTTP surface 是否真的支持该参数；如果当前只是 sticky-only 强会话而非 response-bound anchored turn，则不会再被误锁成 `WSv2-only` | 以 handler 前置校验为主；强 cohort 只会在 HTTP surface 明确支持 `previous_response_id` 时继续保链，否则直接显式 `503` 软中断；若 service 已拿到 structured upstream 4xx 但尚未写响应，则 handler fallback 会优先透传结构化 4xx，只有 `Unsupported parameter: previous_response_id` 这类 transport 能力缺口会被提升成软中断；若 degraded HTTP surface 返回 `text/html` 或 Cloudflare challenge 页，当前 fork 会先做协议级 HTML/challenge 识别并回结构化 JSON 软中断，而不是把整页 HTML 原样透传给客户端；弱会话仍可能 strip；一旦已经向下游写出字节，不再静默重放同一 turn；对于 gateway 侧 WS bridge 选中的 degraded HTTP surface，若普通 `/responses` 明确不支持 `previous_response_id`，当前 fork 会退回本地 replay input + 稳定 `prompt_cache_key`，而不是继续把 anchored turn 原样上推；若当前 group 只剩 `http_streaming_incapable` 的 degraded HTTP 账号，则 scheduler 只在 HTTP non-stream bridge 可用时保留这些账号，并优先选择 bridge 质量更高的候选，由 gateway 对客户端维持流式语义 | 请求体 + 共享会话状态 + 账号 capability + 上游 | 不承诺与 WSv2 等价，但不再把 strong anchored turn 静默打进不支持 `previous_response_id` 的 HTTP fallback，也不再把 sticky-only 强会话误筛成 `no available OpenAI accounts`；当前 live direct probe 已验证：`stream=true` 可以在 `RightCode` 上经 bridge 返回完整 `response.completed` 事件流，而不是再落成 `PackyCode + response.failed` 伪失败；若客户端未显式提供 `client_request_id`，则 turn key 退回为 `session_hash + previous_response_id + payload fingerprint` 派生键 |
| HTTP `/v1/responses/compact` | `degraded` | compact 单独规范化 | 不做 replay merge；非流式成功响应若为空体、半截 SSE、或缺 final response payload，会先进入协议级 failover，再受控重试；generic `4xx` 不再被 compact probe 误判成 supported，协议级 failfast 会把当前账号降为 compact-incapable 观察值并切到下一个候选 | compact 请求体 + 上游 + 观测能力 | 不能假装与普通 `/responses` 完全等价，也不能再把 `200 + 空 body` 直接回给客户端；当前 live 已验证这种策略能把 compact 成功路径重新稳定到 `RightCode` |
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
- 因此 `apikey_ws_transport_unverified` 现在只表示“当前未验证为 strong”，不再触发请求时乐观探测；user path 会优先按 `degraded-only` 处理，等待显式声明或已有观测把账号提升为 `strong cohort`

当前 live capability 不能再用硬编码账号数表达，而应按 runtime panel 与数据库实时读取：

- `Team号池` / `Private` 当前 scheduler 候选以 API-key relay 为主，不再能从“系统里存在 OAuth 账号”直接推出 “group 内存在 strong cohort”
- 2026-03-14 的本地 live 数据已验证：`Team号池` 与 `Private` 的 group membership 里实际包含 `PackyCode`、`RightCode`、`Giot(Free)`；其中 `Giot(Free)` 当前 `schedulable=false`
- 因此 live 的首要问题已经不是“强账号选错”，而是 **当 group 里只剩 degraded HTTP surface 时，gateway 如何避免把 anchored continuation 打碎**
- 2026-03-14 的后续源码修复又补了一层：当 group 当前没有任何 strong cohort 账号时，WS ingress 不再先试 `ResponsesWebsocketV2` 再回退，而是直接走 bridge 友好的 `Any` transport；这一步的意义不是“追求体验”，而是让 transport 选择与当前 group 的真实 capability 保持一致，避免把一次本可预知的 degraded 路径伪装成“强链路失败后的 fallback”
- 同一条 live 主线还证明了另一件事：`degraded` 不是单一能力面。`PackyCode` 与 `RightCode` 都属于 `degraded-only`，但 `PackyCode` 的 HTTP `/responses` `stream=true` surface 已被实锤为会在未收到 `[DONE]` 时提前结束，而 `RightCode` 尚未观测到同类问题。因此 scheduler 不能只看 `strong/degraded cohort`，还必须继续细分 `HTTP streaming capable / incapable / unknown`。当前 fork 已把这类观测写入共享 gateway cache，因此一旦某个账号被观测为 `http_streaming_incapable`，同一 Redis 观测窗口内的新进程和重启后的实例都会优先避开该账号，而不是每次重启都重新踩一次
- 2026-03-15 的连续 6 次 manual live WS probe 又补了一层更强的 live truth：degraded-only 组下的 stream fallback 不能只停在“直接选 bridge 友好的 `Any` transport”，还必须在候选集合内部先做 bridge-quality 硬分层。当前 fork 已经按最高 `bridgePreference` 过滤 fallback 候选集合，因此 `group#3 / Private` 的 6 次连续 probe 全部稳定命中 `RightCode(account_id=16)`，没有再随机落回 `PackyCode(account_id=14)`

这一步的目的，是阻断“把不支持 `/responses` WS transport 的 relay 账号误当成 strong account”这一类根因，避免账号切换时把强 continuation 会话切碎、把缓存亲和打掉、再把用户体验问题伪装成普通 fallback。

### 3.2 HTTP fallback capability 也必须单独建模

### 3.3 `/responses/compact` 的 anchored capability 也必须单独建模

2026-03-14 的 live probe 已经证明，当前 `RightCode` 这一类 API-key OpenAI surface 不能再被粗暴近似成“API-key = compact 不支持”：

- 直接上游探针命中 `https://right.codes/codex/v1/responses/compact`
- 带 `previous_response_id` 的 probe 请求返回 `400 previous_response_not_found`
- 这说明 `RightCode` 的 compact route 存在，并且会正确解析 `previous_response_id`

因此当前 fork 不能再把 “anchored compact 请求” 继续套用 `WS strong cohort -> requiredTransport=ResponsesWebsocketV2` 这套推导。对 `/responses/compact`，正确模型是：

- scheduler 不强制要求 `ResponsesWebsocketV2`
- 对 selected account 先做 compact capability probe
- `known + supported` 时保留 `previous_response_id`
- `known + unsupported` 时显式切走或 fail-fast
- `unknown` 时优先保留，让上游给出真实协议语义，而不是静默剥离锚点放大 token 消耗

除了 `WS continuation capability`，live 日志已经证明还必须单独建模：

- `HTTP previous_response capability`

原因很直接：

- 同一个 OAuth 账号可能支持 `WSv2` continuation
- 但同一个 OAuth 账号的 HTTP passthrough fallback surface 仍然会返回 `Unsupported parameter: previous_response_id`

因此当前 fork 的规则是：

- OpenAI API-key 官方 surface 默认视为支持 HTTP `previous_response_id`
- OpenAI OAuth passthrough HTTP surface 默认视为不支持，除非显式声明 capability
- 自定义 OpenAI API-key surface 不再靠静态近似拍脑袋；会优先吃显式 capability，再回退 runtime probe
- 一旦当前 turn 仍然是 response-bound continuation，而选中的 HTTP surface 又不支持 `previous_response_id`，handler 会直接软中断，不再把 anchored turn 继续上推，也不会再先在 scheduler 阶段误报 `no available OpenAI accounts`
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
- 当前计数语义补充：
  - `ws_to_http_mid_session`
    只统计真正具备 response-bound continuation 锚点的 HTTP 中途降级。
  - `account_switch_with_cache_drop`
    只统计真正落地的跨账号 continuation；被 anchored block 拦下的尝试单独记到 `anchored_cross_account_switch_blocked_total`。
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

当前已经新增了一条 request 级 token 归因闭环，用来解释 degraded bridge 场景下“token 为什么突然涨”：

- `usage_logs` 记录真实 `request_id`
- `ops_system_logs` 新增 `openai.turn_token_attribution`
- `GET /api/v1/admin/ops/requests?request_id=<...>` 已能直接返回：
  - `input_tokens`
  - `cache_read_tokens`
  - `openai_ws_mode`
  - `token_attribution.bridge_used`
  - `token_attribution.bridge_mode`
  - `token_attribution.bridge_source`
  - `token_attribution.replay_input_items`
  - `token_attribution.replay_input_bytes`
  - `token_attribution.prompt_cache_key_source`
  - `token_attribution.prompt_cache_key_used`
  - `token_attribution.upstream_input_tokens`
  - `token_attribution.billable_input_tokens`

这条面当前能回答：

- 当前 turn 是否走了 bridge
- replay input 大小是多少
- cache 读了多少
- billable input 实际是多少
- 当前 request 与最近一次 compact 请求之间的窗口差异是多少

当前 `compact_window` 的语义已经明确下来：

- 相关面不是“整个会话抽象估算”，而是“同一 `session_hash` 下最近一次 compact 请求与当前 request 的真实对账”
- 只有当最近一次 compact 请求本身存在可用归因时，才会在 request drilldown 里返回 `compact_window`
- 若最近一次 compact 请求 `compact_outcome=succeeded`，则 drilldown 会继续返回：
  - `billable_input_delta`
  - `cache_read_delta`
  - `upstream_input_delta`
- 若最近一次 compact 请求失败，当前则只返回 compact request 本身的 request id / outcome / age，不伪造 delta

这条面当前还**不能** truthfully 回答：

- compact 前后 token 差异的精确 delta
- 长周期聚合后的 per-account compact 节省率

## 5. 与使用逻辑完备优先的关系

本 matrix 的核心不是“哪里最纯”，也不是“哪里看起来最顺滑”，而是“哪里最符合真实 capability、最能解释当前状态机为什么这样走”。

因此当前优先级是：

1. transport / 账号 / anchored continuation 的选择先符合真实 capability
2. 不让本地代理自造 continuation 错误
3. 条件不足时再明确 fail-close 或受控降级

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
