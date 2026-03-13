# Responses Continuation 设计基线（Fork 版）

本文档记录当前 fork 在 OpenAI Responses continuation 上的本地判断、外部交叉验证、用户体验优先的设计取舍，以及后续逐步落实路线。

配套能力矩阵见：`docs/RESPONSES_CONTINUATION_CAPABILITY_MATRIX_CN.md`。

适用范围：

- `responses_websockets_v2`
- `previous_response_id`
- `function_call_output`
- `store=false`
- 流式/非流式工具回合
- `/v1/responses/compact` 相关设计边界

不适用范围：

- 泛产品选型
- 非 `sub2api` fork 的部署运维细节
- 任何 transcript replay 风格的自定义历史重建

## 1. 当前本地判断

### 1.1 已经补强到高完成度的部分

当前 fork 已经落了一组连续 continuation hardening patch，已经把最危险的一段链路收紧到了较高完成度：

- `WSv2 + store=false + function_call_output/tool 回合`
- `previous_response_not_found`
- strict-affinity / preflight ping fail
- stale `previous_response_id`
- 无本地锚点时的显式失败出口

当前能力重点不是“永不失败”，而是：

1. 本地能确认锚点时，优先本地恢复
2. 请求本身具备自包含工具上下文时，允许受控降级重试
3. 本地锚点已对齐但 upstream 仍判 stale 时，只有自包含工具回合才允许受控降级
4. 既无本地锚点、请求也不自包含时，明确 fail-close
5. 不做 transcript replay

### 1.2 还没有完全闭环的部分

以下问题仍然存在，不能声称已经“全覆盖”：

- HTTP / 非 WSv2 路径仍不是与 WSv2 等价的 continuation 语义
- `response_id -> conn_id`、`session_hash -> conn_id` 仍是进程内状态
- TTL 失效后 continuation 仍可能中断
- 当前默认策略仍偏 correctness-first，不是 cache-hit / 少建连 / 少状态同时最优
- `/compact` 仍需要单独做 capability contract，不能假装与普通 `/responses` 完全等价
- 非原生 upstream、跨协议转换、跨实例漂移仍有额外语义边界
- 非流式 `/responses` / `/responses/compact` 的成功响应如果为空体、半截 SSE、或完成后仍缺 final response payload，必须视为协议级失败；不能再把 `200 + 空 body` 原样回给客户端

## 2. 外部交叉验证结论

### 2.1 官方结论

OpenAI 官方文档指向两个核心事实：

1. 多轮 continuation 的首选机制是 `previous_response_id`
2. 如果不依赖 `previous_response_id`，则需要在下一轮 `input` 里提供足够的前序上下文项

参考：

- Responses 迁移与状态：<https://developers.openai.com/api/docs/guides/migrate-to-responses>
- Tools / function calling：<https://developers.openai.com/api/docs/guides/tools>
- Prompt caching：<https://developers.openai.com/api/docs/guides/prompt-caching>
- Realtime cost / cached tokens：<https://developers.openai.com/api/docs/guides/realtime-costs>

这意味着“把错误送上游”并不天然错误，但前提是请求本身已经满足协议所需上下文，而不是把明显不完整的 payload 继续上推。

### 2.2 行业实践结论

云厂商和会话基础设施的常见结论是一致的：

- correctness 相关状态应尽量外置到共享状态层
- sticky session 和连接亲和更适合作为性能优化，而不是唯一真相源
- TTL 是容量控制与可恢复性的折中，不是语义保证

参考：

- AWS Well-Architected: <https://docs.aws.amazon.com/wellarchitected/2023-04-10/framework/rel_mitigate_interaction_failure_stateless.html>
- AWS Prescriptive Guidance: <https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-asp-net-web-forms/ha-scaling.html>
- Redis Session Management: <https://redis.io/solutions/session-management/>

### 2.3 学术与社区结论

学术和社区都说明两点：

1. token 节省与缓存命中主要依赖稳定前缀、cache-aware routing，而不只是 continuation state
2. `/compact`、tool continuation、本地状态与上游状态的交界面天然脆弱

参考：

- Preble: <https://arxiv.org/abs/2407.00023>
- Don't Break the Cache: <https://arxiv.org/abs/2601.06007>
- OpenAI Codex issue: <https://github.com/openai/codex/issues/5087>

## 3. 设计取舍：用户体验优先

本 fork 的目标优先级调整为：

1. 用户体验
2. continuation correctness
3. 工程完备与实现纯度

这不等于“为了体验无脑放行”，而是把策略改成分层：

### 3.1 不再把问题简化成二选一

错误的二选一：

- 方案 A：一律本地 fail-close
- 方案 B：一律把错误送上游碰碰运气

更合理的三段式：

1. **本地确定性恢复**
   条件：本地能确认上一轮锚点  
   动作：对齐 `previous_response_id` 或恢复会话状态

2. **受控的自包含重试**
   条件：本地没有锚点，但请求本身已经携带完整工具上下文  
   动作：移除陈旧 `previous_response_id`，以自包含 payload 重试一次

3. **明确 fail-close**
   条件：既没有本地锚点，请求也不自包含  
   动作：本地拒绝，并返回清晰诊断原因

### 3.2 为什么“把错误送上游”有时是对的

有两类“送上游”必须区分：

- **盲送**：本地已知 payload 语义不完整，还继续发  
  这会把本地错误变成上游 400，通常既伤体验也伤可诊断性

- **受控上推**：本地确认 payload 在不依赖 `previous_response_id` 时仍是协议完整的  
  这种情况上推是合理的，因为这是合规的自包含 continuation，而不是碰运气

因此，本 fork 后续追求的不是“永远不要送上游”，而是：

- 不送明显坏请求
- 允许合规的自包含重试
- 为未来协议漂移保留明确的兼容探测位置，而不是把所有请求都混成模糊重试

### 3.3 为什么“无本地锚点就 fail-close”不能做成绝对原则

如果请求自带：

- `tool_call` / `function_call` 上下文，或
- `item_reference` 对所有 `call_id` 的覆盖

那么此时虽然本地无锚点，但 continuation 不一定不可继续。  
这时直接 fail-close 会损伤体验，而且会错过一类本来合法的 continuation。

因此，“无本地锚点即 fail-close”只适用于：

- 请求仍依赖 `previous_response_id`
- 请求不自包含
- 或本地无法判断自包含语义成立

## 4. 当前实施原则

### 4.1 已实施

- 不做 transcript replay
- `function_call_output` 先做本地上下文校验
- stale `previous_response_id` 且本地有锚点时，优先对齐恢复
- 本地锚点已对齐但 upstream 仍判 stale 时，仅自包含 payload 允许单次 drop-`previous_response_id` 重试
- 无本地锚点时，如果 payload 自包含，则允许单次 drop-`previous_response_id` 重试
- 无本地锚点且 payload 不自包含，则明确 fail-close
- `session_hash -> last_response_id / turn_state` 从共享缓存回填本地时，使用共享剩余 TTL，而不是额外延长本地寿命
- 当 `session_hash -> account_id` 粘连缺失，但 `session_hash -> last_response_id` 与 `response_id -> account_id` 仍可用时，优先从共享响应状态恢复 sticky account，并回填会话级粘连
- HTTP 中途降级进入 `Responses(...)` 时，如果会话仍属于强 continuation cohort，则在账号选择前优先从共享会话状态回填 `previous_response_id`，并把 `requiredTransport` 收紧到 `WSv2`
- 对强 continuation cohort 的 HTTP 中途降级，不再默认静默剥离 `previous_response_id`；只有弱会话或明确不满足 cohort 条件时才继续走旧的 strip 行为
- `client_request_id` 中间件现在优先接受客户端显式传入的 `X-Client-Request-ID` / `Client-Request-ID`，再退回本地生成 UUID，作为后续 turn 级幂等键的最小基础
- 当客户端显式提供 `client_request_id` 时，turn 级幂等键优先跟随该键，并且不会因为 `previous_response_id` 的对齐、剥离或陈旧漂移而改变；当客户端未显式提供时，网关会退回到 `session_hash + previous_response_id + payload fingerprint` 派生键，避免每次重试都重新生成完全无关的 UUID
- `/responses` handler 在已有下游输出后，不再对同一 turn 做同账号或跨账号静默重试；如果 streaming 已开始，只补一个明确的终止错误，而不是重新生成第二份回答
- WS ingress turn 在 `wroteDownstream=true` 时，会显式阻断本轮重试，并记录对应的 duplicate-turn / emitted-bytes 计数
- 调度器已显式区分 `continuation cohort / degraded cohort`，并把请求 cohort、选中 cohort 与 cohort fallback 作为决策字段输出，避免“只看 transport 看不见语义层级”
- `prompt_cache_key` 已进入 scheduler 的 cache-affinity 输入；调度决策不再只看 availability 和 cohort，而会在同 cohort 候选里优先选择与当前 cache-affinity 更匹配的账号，把 continuation 亲和与缓存前缀亲和一起前置
- 非流式 `/responses` / `/responses/compact` 在 API key passthrough 和 OAuth SSE-to-JSON 两条链上，都已经补了协议完整性校验：空 body、半截 SSE、或 `response.completed/response.done` 后缺 final response payload 时，不再透传 `200` 成功，而是先构造成 `502` 的可重试协议级 failover，让 handler 侧优先走同账号/跨账号受控重试

### 4.2 暂不实施

- 不对任何模糊工具回合做无限制上游探测
- 不把连接亲和状态当作 correctness 唯一来源
- 不引入 full transcript replay 或 merge

## 5. 后续路线图

### Phase 1：完成文档化与受控自包含重试

- 落地本设计文档
- 把“无本地锚点但 payload 自包含”纳入受控恢复
- 补两条针对 `previous_response_not_found` / preflight ping fail 的回归

### Phase 2：能力矩阵与可观测性

- 明确区分 `WSv2 / HTTP / legacy / compact`
- 输出 capability matrix，而不是把所有 transport 当同一种 continuation
- 增加以下指标：
  - `ws_to_http_mid_session_total`
  - `previous_response_recovered_from_session_total`
  - `previous_response_id_stripped_mid_session_total`
  - `account_switch_with_cache_drop_total`
  - `strong_cohort_fallback_total`
  - `cache_affinity_selection_total`
  - `duplicate_turn_retry_blocked_after_emit_total`
  - `emitted_bytes_before_retry_total`
  - `turn_reuse_processing_conflict_total`
  - `turn_reuse_emitted_conflict_total`
  - `turn_reuse_completed_conflict_total`
  - fail-close 原因
  - stale anchor 对齐次数
  - self-contained retry 次数
  - TTL 过期造成的断链次数
  - 连接 churn
  - cached tokens 与 prompt cache hit surrogate

### Phase 3：共享 correctness 状态面

- 继续把 correctness 相关状态外置到共享缓存或更稳的存储层
- 连接态保留为 hint，不再承担 correctness 唯一职责

### Phase 4：cache-aware routing

- 将 continuation correctness 与 prompt cache hit 分层
- 在稳定前缀和 `prompt_cache_key` 前提下做 session-aware / cache-aware routing
- 避免为了 correctness 盲目增加连接和状态

### Phase 5：兼容性探测模式

只有在以下前提满足时，才考虑引入“兼容性探测”模式：

- 明确是 UX-first 部署
- 有足够日志与指标识别误探测
- 探测次数和触发条件严格受限

该模式的目标不是替代 correctness 逻辑，而是在协议漂移或上游行为变化时，为自包含请求提供一层受控兜底。

## 6. 当前结论

当前 fork 不应再以“纯工程完备”作为唯一最优，而应以：

- continuation 不自造错误
- 遇到歧义时尽量不给用户制造无谓中断
- 在不违反协议完整性的前提下优先保住用户体验

作为设计主线。
