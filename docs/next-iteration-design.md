# MeteringProxy 下一阶段迭代设计方案

版本定位：个人 CLIProxyAPI 部署增强方案  
状态：设计稿  
适用范围：MeteringProxy 后端、WebUI、运维诊断、计价辅助、已知缺陷修复  

## 1. 核心结论

下一阶段的目标不是把 MeteringProxy 做成公开产品，也不是引入多用户、权限、平台化模型目录或复杂商业能力。这个项目仍然只服务于一个明确场景：

> 为个人部署的 CLIProxyAPI 提供 AI 中转站透明网关、用量计量、成本估算、流量观测和轻量运维控制台。

当前项目已经具备透明代理、基础计量、错误分类、WebUI、CPA management 集成、多模态底座和部分 quota/credential 展示能力。下一阶段应优先补强“使用时能解决问题”的功能能力，同时修复已经明确存在的缺陷和边界问题。

优先方向：

1. 功能与使用体验补强：透明网关能力视图、模型资产、问题诊断、计量可信度。
2. 已知缺陷修复：salt 一致性、health timeline 基线、quota 表达、compressed SSE 诊断。
3. 必要支撑能力：CPA 状态、doctor、healthcheck、查询性能、WebUI 空状态和异常状态。
4. 小功能迭代：基于 CPA 模型列表和实际请求发现未配置价格的模型，辅助生成 pricing stub。

## 2. 边界与非目标

### 2.1 继续保持的项目边界

- 只服务个人部署的 CLIProxyAPI。
- 代理透明性优先于计量完整性。
- 计量、报表、WebUI、DB 写入、side-channel 均不得阻塞正常请求。
- 不保存 prompt、response text、tool arguments、图片内容、音频内容、文件内容、明文 API key、明文 IP。
- 对无法确认的数据明确标注为 missing、request-only、unsupported、partial 或 unavailable，不伪装成完整计量。
- MeteringProxy 可以增强 AI 流量观测，但不替代 CLIProxyAPI 做 provider routing、credential management 或 provider-specific 业务决策。

### 2.2 明确不做

- 不做 SaaS 化、多租户、用户权限或公网产品包装。
- 不做大而全的外部模型价格目录同步系统。
- 不在启动路径中依赖外部价格更新。
- 不自动猜测模型价格。
- 不为了 WebUI 报表改变请求/响应字节、header 或流式传输行为。
- 不强行宣称 quota 完整可用；只有 provider adapter 可靠产出归一化数据时才显示 full quota。

## 3. 当前基线

### 3.1 已具备能力

- OpenAI Chat/Completions、Responses、Anthropic Messages、Gemini generateContent 等文本计量。
- Responses SSE 状态机与 capture outcome/reason。
- 错误响应轻量分类，不保存 provider 原始错误正文。
- 图片、Embedding、多模态 usage_dimensions/image_usage 底座。
- request-only profiles：images variations、audio、video。
- CPA management client、credential health、usage queue、quota diagnostic scaffold。
- WebUI overview、models、requests、issues、quota、observability、images/multimodal API。
- Prometheus metrics。
- SQLite 增量迁移、health metrics、WAL checkpoint。

### 3.2 主要缺口

- WebUI 还没有一张足够清晰的“透明网关能力图”：哪些 AI 路由经过代理、哪些可计量、哪些只是 request-only、哪些只是透传。
- 模型视图没有把 CPA 可用模型、实际使用模型和价格配置统一呈现。
- issues 虽有数据基础，但还需要更可行动的诊断说明。
- usage confidence 还没有作为核心用户可见概念贯穿报表。
- CPA 状态检查是必要能力，但它更多是支撑排障，不应成为主体验中心。
- salt 一致性要求写在文档里，但启动时还没有完整保护。
- health timeline 第一桶无基线时容易被误读为真实区间增量。
- quota adapter 目前没有实际 provider 支持，UI 必须避免过度承诺。
- 压缩 SSE 导致无法解析 usage 时，需要更明确地暴露为诊断状态。

## 4. 功能能力补强

### 4.1 AI 透明网关能力视图

新增 WebUI 区域：`Gateway` / `透明网关`。

目标不是管理 CPA，而是回答 MeteringProxy 作为 AI 中转站到底“看见了什么、能计量什么、不能计量什么”。

核心问题：

- 当前有哪些 AI API 路径经过 MeteringProxy？
- 哪些 endpoint 是 usage-metered？
- 哪些 endpoint 是 request-only？
- 哪些 endpoint 是 passthrough？
- 哪些协议/格式已覆盖：OpenAI-compatible、Responses、Anthropic Messages、Gemini native、CPA provider aliases、Codex direct？
- 流式请求和非流式请求分别占比多少？
- 哪些 endpoint 经常出现 missing usage？
- 哪些 request-only endpoint 需要后续补齐 usage adapter？
- 代理自身有没有改变过用户请求？如有必须视为缺陷。

建议展示为“能力矩阵 + 最近流量”：

| Capability | Status | Recent traffic | Notes |
|---|---|---:|---|
| Chat Completions | usage-metered | 128 | OpenAI-compatible usage |
| Responses | usage-metered | 72 | SSE 状态机，支持 image_generation facts |
| Anthropic Messages | usage-metered | 31 | cache creation/read tokens |
| Gemini generateContent | usage-metered | 44 | native usageMetadata |
| Images generations/edits | usage-metered | 12 | image_usage + usage_dimensions |
| Embeddings | usage-metered | 8 | prompt token usage |
| Audio | request-only | 3 | 不保存音频内容，usage adapter 未验证 |
| Videos | request-only | 2 | async task facts only |
| Unknown routes | passthrough | 5 | 透明转发，不计量 |

建议 API：

```text
GET /metering/api/gateway/capabilities?range=24h
```

返回字段建议：

```json
{
  "range": "24h",
  "summary": {
    "total_requests": 305,
    "usage_metered_requests": 282,
    "request_only_requests": 5,
    "passthrough_requests": 18,
    "stream_requests": 97,
    "missing_usage_requests": 9
  },
  "profiles": [
    {
      "name": "responses",
      "display_name": "Responses API",
      "capture_mode": "usage_metered",
      "metering_kind": "llm_tokens",
      "request_count": 72,
      "missing_usage_count": 4,
      "stream_count": 63,
      "known_limitations": ["compressed_sse_not_metered"]
    }
  ]
}
```

实现来源：

- `profile.Registry` 提供内置 profile 能力。
- `request_usage.endpoint_profile`、`capture_mode`、`metering_kind` 提供实际流量。
- `capture_outcome`、`capture_reason`、`usage_source` 提供计量质量。
- `stream`、`status`、`error_class` 提供行为特征。

设计约束：

- 只展示网关观察事实，不做路由决策。
- 不因为某 endpoint 当前无法计量就阻断请求。
- Unknown passthrough 是正常能力，不应被默认渲染成错误。
- request-only 是明确状态，不等于 missing usage。
- compressed SSE、provider 不返回 usage、side-channel 断开等原因应能被区分。

### 4.2 透明代理与 LLM 体验优化

MeteringProxy 的核心竞争力不是后台健康检查，而是在 AI 流量路径上尽可能保持“透明、低延迟、可观测、可诊断”。下一阶段应围绕 LLM 请求体验补强，而不是只扩展报表。

#### 4.2.1 WebSocket / Realtime / HTTP Upgrade 支持

当前代理主路径以普通 HTTP 和 SSE 为核心。若后续希望 MeteringProxy 成为更完整的 AI 中转站，需要明确处理 WebSocket、Realtime 或其他 HTTP Upgrade 类流量。

目标：

- 对 `Connection: Upgrade`、`Upgrade: websocket`、Realtime session 等长连接有明确策略。
- 能支持时，建立双向透明隧道，不解析业务内容。
- 暂不支持时，文档和 Gateway 能力视图明确标注应绕过 MeteringProxy 或保持 passthrough。
- 不把 WebSocket/Reatime 错误误分类为普通 SSE usage missing。

验收：

- WebSocket/Upgrade 请求要么被透明隧道转发，要么被明确标注为 unsupported/passthrough 策略。
- 不影响现有 HTTP/SSE golden tests。
- 不保存 Realtime 事件内容或音频内容。

#### 4.2.2 代理自身开销可观测

当前已经记录 request latency 和 TTFB，但还需要区分“上游慢”与“网关自身造成的额外开销”。

建议新增指标：

- upstream round-trip TTFB。
- downstream first write delay。
- stream flush count 和 flush error。
- request body prefix sampling bytes。
- response sample bytes。
- enqueue 成功/丢弃耗时。
- 代理自身 transport error 分类计数。

建议 Prometheus 指标：

```text
metering_proxy_upstream_ttfb_ms_bucket
metering_proxy_downstream_write_errors_total
metering_proxy_stream_flushes_total
metering_proxy_request_sample_bytes_total
metering_proxy_response_sample_bytes_total
metering_proxy_transport_errors_total{class="dns_error"}
```

设计约束：

- 指标采集不能引入锁竞争或请求路径阻塞。
- 不记录请求/响应正文。
- 高基数字段如 request_id、api_key_hash 不进入 Prometheus label。

#### 4.2.3 Transport 参数可配置

当前 HTTP transport 参数是保守默认值。个人部署通常够用，但作为中转网关，应允许在高并发或长连接场景下调优。

建议配置：

```yaml
proxy_transport:
  max_idle_conns: 100
  max_idle_conns_per_host: 20
  idle_conn_timeout: "90s"
  tls_handshake_timeout: "10s"
  expect_continue_timeout: "1s"
```

设计约束：

- 默认值保持当前行为。
- 配置上限要 clamp，避免误配置导致资源耗尽。
- 长流式响应继续保持 `WriteTimeout=0` 或有明确的 stream-safe 策略。

#### 4.2.4 Request-only 快路径

对 audio、video、files、unknown passthrough 等 request-only 或 passthrough 流量，代理应尽量少读、少解析、少采样。

目标：

- usage-metered profile 才做必要的 bounded body prefix scan。
- request-only profile 只记录 status、latency、bytes、endpoint、method、stream、error class 等请求事实。
- passthrough profile 不读取 body prefix。
- 大文件/音频/视频请求不因计量逻辑增加明显延迟或内存压力。

验收：

- request-only 大 multipart 请求不会被完整读入内存。
- request-only 与 passthrough 的行为在 Gateway 能力视图中清楚标注。
- request-only 不计入 missing usage。

#### 4.2.5 压缩 SSE 策略显性化

为了保持 header 透明，代理不应默认删除客户端 `Accept-Encoding`。但压缩 SSE 会让 usage 提取不可见。

建议：

- 默认继续保持透明，不改 header。
- 当可判断为压缩 stream 且无法解析 usage 时，记录 `capture_reason=compressed_stream_not_metered`。
- 提供可选配置作为 opt-in，而不是默认行为：

```yaml
stream_metering:
  prefer_uncompressed_sse: false
```

若未来启用该选项，必须明确写入文档：它为了提升计量完整性而改变上游请求 header，不再是完全 header-transparent 模式。

#### 4.2.6 管理端请求与请求路径隔离

Credential health、usage queue、quota refresh 都属于管理端观测能力，不应影响 LLM 请求体验。其中 quota 不应默认常驻轮询；更适合个人部署的方式是手动刷新。

优化要求：

- management 请求设置明确 `User-Agent`。
- management 请求设置组件标识，例如 `X-Metering-Component: credential_health|usage_queue|quota_refresh`。
- quota 默认不自动 probe，不自动 poll；只有用户在 WebUI 点击刷新或显式 CLI refresh 时才请求 CPA。
- WebUI quota refresh 要有去抖/最小间隔，避免连续点击刷 CPA 日志。
- usage queue 如果保留自动轮询，应支持 backoff，避免 CPA 不可用时刷日志。
- 管理端错误只进入诊断，不影响代理主路径。

### 4.3 基于 CLIProxyAPI 当前源码的集成校准

本节基于 `router-for-me/CLIProxyAPI` 当前 `main` 分支源码校准，检查版本为 `v7.2.53`，提交 `4f2e190`。结论是：MeteringProxy 应把 CPA 当作“LLM 路由与凭证执行系统”，而不是把它的内部 quota/cooldown 状态误读成完整额度系统。

#### 4.3.1 可直接复用的 CPA 能力

CPA 当前确认存在以下 management 入口：

- `GET /v0/management/auth-files`
- `GET /v0/management/auth-files/models`
- `POST /v0/management/api-call`
- `POST /v0/management/reset-quota`
- `GET /v0/management/usage-queue`
- `GET /v0/management/anthropic-auth-url`
- `GET /v0/management/codex-auth-url`
- `GET /v0/management/antigravity-auth-url`
- `GET /v0/management/kimi-auth-url`
- `GET /v0/management/xai-auth-url`
- `GET /v0/management/get-auth-status`
- `GET /v0/management/ws-auth`

适合 MeteringProxy 复用的能力：

- `auth-files`：作为凭证健康、OAuth 登录状态、凭证可用性、recent requests、Codex 账号/计划信息和 websockets 标记的主要来源。
- `auth-files/models`：作为“某个 auth 当前支持哪些模型”的辅助来源。
- `usage-queue`：作为 side-channel usage 增强来源，继续与响应内 usage 做 merge/diagnostics。
- `api-call`：作为服务器端固定模板的手动 quota/diagnostic 请求通道。它可以替换 `$TOKEN$`，并按 CPA credential/global proxy 规则代发请求。
- `reset-quota`：作为手动清理 CPA 内部 quota/cooldown 路由状态的维护动作，不等于“刷新额度”。
- OAuth URL/status：可以用于个人部署下的凭证维护入口，但应与透明代理主体验分离。

#### 4.3.2 不能过度推断的 CPA 能力

CPA 的 `quota` 概念主要是请求执行后的 routing/cooldown 状态：

- `Auth.Quota` 和 `ModelState.Quota` 表达的是“最近是否碰到 quota/rate limit/cooldown，以及何时可恢复”。
- `reset-quota` 清理的是 CPA 内部 auth/model 的 cooldown 或 quota-exceeded 路由状态。
- `auth-files` 当前返回 `status`、`status_message`、`disabled`、`unavailable`、`success`、`failed`、`recent_requests`、`next_retry_after` 等，但不应假定它已经完整返回内部 `Quota`、`ModelStates` 或 provider 真实额度明细。
- provider 的真实剩余额度、周期窗口、plan、credits 等仍需要 provider-specific adapter 或固定 `api-call` 模板解析，不能仅凭 CPA `quota` 字段宣称 full quota available。

因此，MeteringProxy 的 quota/UI 命名应收敛为：

- `CPA auth health`：来自 `auth-files`，可自动或手动刷新，表达 credential 是否健康。
- `CPA cooldown/quota state`：来自 CPA 已暴露字段和错误侧信道，表达最近是否因为限流/额度而不可用。
- `Provider quota snapshot`：只在固定 provider adapter 成功解析真实 quota endpoint 后才显示。

#### 4.3.3 透明代理路径的 CPA 启示

CPA 对 WebSocket/Realtime 类能力不是通用 TCP 透明隧道，而是在 `GET /v1/responses` 和 `GET /backend-api/codex/responses` 上做了业务协议级 WebSocket handling，并由 executor 决定是否走 upstream websocket。

MeteringProxy 的定位不同，应优先实现通用透明网关能力：

- 先支持 `Connection: Upgrade` / `Upgrade: websocket` 的双向字节转发。
- 不解析 WebSocket 业务事件，不保存事件正文、音频或工具参数。
- 对 Upgrade 流量只记录 endpoint、status、duration、bytes、open/close/error class。
- 暂不照搬 CPA 的 Responses WebSocket 协议转换逻辑；那是 CPA 的 provider/executor 责任。
- Gateway 能力视图需要区分 `http_sse_metered`、`websocket_tunneled`、`websocket_unsupported`、`request_only`。

#### 4.3.4 对个人部署的产品能力建议

可以借助 CPA 的 Auth 系统增强 MeteringProxy，但建议做成“CPA Auth Mirror”，而不是让 MeteringProxy 接管 Auth：

- WebUI 增加 `CPA Auth` 区域，展示每个 auth 的 provider、label/name、status、status_message、disabled、unavailable、success/failed、recent requests、next_retry_after、project_id、websockets、Codex plan 信息。
- 刷新按钮触发一次 `GET /v0/management/auth-files`，默认不后台轮询。
- 每个 auth 可提供“打开 OAuth 登录 URL”或“查看登录状态”的维护入口，但这些操作不进入 LLM 请求路径。
- 每个 auth 可提供“Reset CPA cooldown”按钮，调用 `POST /v0/management/reset-quota`，文案必须说明这是清理 CPA 路由冷却，不是恢复 provider 额度。
- Provider quota 只做手动刷新，并通过固定模板 `api-call` 执行；模板白名单内置在 MeteringProxy 服务端，不把通用 api-call 暴露给浏览器。
- 个人模式可以选择显示更多账号信息，但默认仍不持久化 raw auth file、provider token、raw api-call response。

#### 4.3.5 推荐落地形态

Quota/OAuth 相关系统应按三层实现，不再作为一个混合的“配额系统”继续扩张：

1. `CPA Auth Mirror`
   - 主线能力。
   - 来源是 `GET /v0/management/auth-files`。
   - 只回答“当前 CPA 有哪些 auth、状态如何、最近是否成功、是否 disabled/unavailable、何时 next retry、是否支持 websockets、是否能看到 plan/project hint”。
   - 默认手动刷新；后续如果需要定时刷新，也只能是很慢的可选刷新，不进入默认路径。

2. `CPA Cooldown State`
   - 维护能力。
   - 用来解释 CPA 内部 auth/model routing cooldown，不表示 provider 真实剩余额度。
   - `POST /v0/management/reset-quota` 在 WebUI 中必须命名为 `Reset CPA cooldown` 或等价文案，不能叫“刷新额度”。

3. `Provider Quota Snapshot`
   - 可选能力。
   - 只由用户手动触发。
   - 通过 MeteringProxy 服务端固定 provider 模板调用 CPA `api-call`，不接受浏览器传任意 URL。
   - 只有 adapter 成功解析真实 provider quota endpoint 时才展示额度；否则显示 `not_refreshed`、`unsupported`、`unavailable` 或 `partial`。

推荐配置语义：

```yaml
cliproxy_management:
  credential_health:
    enabled: true
    refresh_mode: "manual"   # manual first；后续可选 very_slow scheduled
  quota:
    enabled: true
    refresh_mode: "manual"
    min_refresh_interval: "60s"
  oauth:
    enabled: false
```

必须避免：

- 启动自动 quota probe。
- 打开 WebUI 自动刷新 provider quota。
- 后台常驻 quota 轮询。
- 请求前 quota check。
- 按 quota 改写路由、拒绝请求或切换 credential。
- 在 MeteringProxy 实现 provider OAuth 流程。

#### 4.3.6 API 与状态机收敛

不建议继续把 CPA Auth、CPA cooldown reset 和 provider quota snapshot 都塞进一个 `/metering/api/quota` 语义里。它们的数据来源、刷新成本和失败语义不同，应拆成三个清晰边界：

```text
GET  /metering/api/cpa/auth
POST /metering/api/cpa/auth/refresh
POST /metering/api/cpa/cooldown/reset

GET  /metering/api/provider-quota
POST /metering/api/provider-quota/refresh
GET  /metering/api/provider-quota/diagnostics
```

接口语义：

- `GET /metering/api/cpa/auth`：只读当前缓存，不触发 CPA 请求；没有缓存时返回 `not_refreshed`。
- `POST /metering/api/cpa/auth/refresh`：用户显式刷新 CPA Auth Mirror，调用 `GET /v0/management/auth-files`。
- `POST /metering/api/cpa/cooldown/reset`：用户显式执行维护动作，调用 `POST /v0/management/reset-quota`；该接口不改变 provider quota snapshot 状态。
- `GET /metering/api/provider-quota`：只读 provider quota snapshot 缓存，不触发 CPA `/api-call`。
- `POST /metering/api/provider-quota/refresh`：用户显式刷新 provider quota，通过服务端固定模板调用 CPA `/api-call`。
- `GET /metering/api/provider-quota/diagnostics`：展示最近刷新事件、失败原因、rate limit、unsupported provider 和 stale 状态。

如果为了兼容保留旧的 `/metering/api/quota`，它只能作为只读聚合别名，不能再作为新实现中心，也不能在 GET 时触发刷新。

建议状态机：

| State | 适用对象 | 含义 |
|---|---|---|
| `disabled` | Auth/Quota | 配置关闭 |
| `not_refreshed` | Auth/Quota | 已允许功能，但用户尚未手动刷新 |
| `refreshing` | Auth/Quota | 已触发刷新，结果尚未写入 |
| `available` | Auth/Quota | 当前缓存可用 |
| `partial` | Quota | 部分 provider/credential 有 quota 数据，部分失败或不支持 |
| `unsupported` | Quota | Management/API 可达，但没有可用 provider adapter |
| `unavailable` | Auth/Quota | CPA management、`auth-files` 或 `/api-call` 不可达 |
| `rate_limited` | Auth/Quota | 命中 MeteringProxy 最小刷新间隔或 CPA/provider 限制 |
| `stale` | Auth/Quota | 有旧缓存，但超过 freshness 窗口 |
| `error` | Auth/Quota | 最近一次刷新失败，错误已归一化 |

内部实现建议从 `Poller` 收敛为 `RefreshService`：

- `RefreshService` 管理手动 refresh、singleflight、最小刷新间隔、timeout、bounded concurrency 和最近诊断事件。
- GET API 永远只读 cache/repository。
- POST refresh 可以同步排队、异步执行或返回已有 in-flight refresh，但必须立即返回明确状态，不能让 WebUI 请求长期等待 provider。
- 默认不在启动时 refresh，不在 WebUI 打开时 refresh，不在 LLM 请求前 refresh。
- 后续如果确实需要定时刷新，只能通过显式 `refresh_mode: "scheduled"` 开启，默认仍为 `manual`。

配置迁移语义：

- 旧实现中 `quota.enabled: true` 容易被理解为“启动后台 poller”。
- 新语义应调整为“允许用户手动刷新 provider quota”。
- 未配置 `refresh_mode` 时按 `manual` 处理；这能兼容用户当前配置，同时停止持续刷 CPA Docker log。
- 如果将来提供 scheduled 模式，需要新增独立字段表达间隔、backoff 和启动预热策略，不能复用 `enabled` 暗示后台行为。

### 4.4 CPA 状态检查

CPA 健康检查是必须能力，但定位应是支撑排障，不是 WebUI 的主中心。

目标是快速回答：

- MeteringProxy 到 CPA 是否可达？
- CPA `/v1/models` 是否可用？
- CPA management API 是否可用？
- auth-files 是否可读？
- usage queue 是否连接正常？
- quota 当前是 disabled、unsupported、partial、unavailable 还是 available？
- 最近一次 CPA/上游错误是什么？
- 最近一次 proxy transport error 是 DNS、connection refused、timeout 还是其他？

后端建议新增或扩展：

- `GET /metering/api/cpa/status`
- 或并入现有 `/metering/api/observability`

返回字段建议：

```json
{
  "upstream": {
    "base_url": "http://cli-proxy-api:8317",
    "reachable": true,
    "last_error_class": "",
    "last_checked_at": "2026-07-08T00:00:00Z"
  },
  "models": {
    "available": true,
    "count": 42,
    "last_error": ""
  },
  "management": {
    "enabled": true,
    "auth_files_available": true,
    "usage_queue_connected": true,
    "quota_phase": "credential_health",
    "quota_status": "unsupported"
  }
}
```

实现约束：

- 状态探测必须有 timeout。
- 不在用户请求路径做探测。
- 探测失败只影响状态展示，不影响代理转发。
- management key 不出现在日志、响应或 DB 中。

### 4.5 模型资产视图

新增或增强模型页，用来统一展示“我这个 CPA 里有哪些模型，以及它们有没有被计量和计价”。

模型来源：

- CPA `/v1/models` 返回的可用模型。
- `request_usage.model_requested` 实际请求模型。
- `request_usage.model_returned` 实际返回模型。
- `pricing.yaml` 中配置的模型。

展示字段：

- model name
- 来源：CPA available / requested / returned / pricing-only
- endpoint profile
- capture mode：usage-metered / request-only / passthrough / unknown
- request count
- failed count
- token count
- estimated cost
- pricing status：configured / alias / unpriced
- latest seen time

价值：

- 发现 CPA 里有但从未使用的模型。
- 发现实际使用但 CPA 列表未出现的模型。
- 发现实际高频使用但未配置价格的模型。
- 发现 request-only endpoint 下的模型，避免误读为完整成本。

建议 API：

```text
GET /metering/api/model-assets?range=24h
```

返回结构示意：

```json
{
  "range": "24h",
  "items": [
    {
      "model": "claude-sonnet-4-6",
      "sources": ["cpa_models", "requested", "returned", "pricing"],
      "endpoint_profiles": ["anthropic_messages"],
      "capture_mode": "usage_metered",
      "request_count": 128,
      "failed_count": 3,
      "total_tokens": 123456,
      "estimated_cost": 1.23,
      "cost_known": true,
      "pricing_source": "exact",
      "latest_seen_at": "2026-07-08T00:00:00Z"
    }
  ],
  "summary": {
    "models_total": 42,
    "used_models": 12,
    "unpriced_used_models": 2,
    "request_only_models": 1
  }
}
```

### 4.6 问题诊断中心

现有 issues API 应继续向“可行动问题列表”演进。

问题类别建议：

| Class | 含义 | 建议提示 |
|---|---|---|
| `proxy_dns_error` | MeteringProxy 无法解析 CPA host | 检查 Docker 网络、容器名、upstream 配置 |
| `proxy_connection_refused` | CPA 端口未监听或端口错误 | 检查 CPA 容器、端口、监听地址 |
| `proxy_timeout` | 上游响应超时 | 检查 CPA 或 provider 延迟 |
| `auth_failed` | 认证失败 | 检查 API key 或 CPA credential |
| `rate_limited` | provider 限流 | 检查请求速率或 provider quota |
| `quota_exhausted` | 额度耗尽 | 检查 credential health/quota 状态 |
| `response_completed_without_usage` | 响应完成但没有 usage | 检查 provider 语义或 side-channel |
| `stream_ended_without_completed` | Responses stream 未见 completed | 检查断流或 provider 行为 |
| `side_channel_disconnected` | usage queue 断开 | 检查 CPA usage-statistics 和 transport |
| `unpriced_model` | 使用模型未配置价格 | 补 pricing.yaml 或 alias |
| `request_only_usage` | endpoint 仅记录请求事实 | 不应将其解读为完整 usage |

每个 issue 至少展示：

- severity
- count
- latest_at
- endpoint
- model
- status
- error_code/error_type
- sample request_id
- short diagnosis
- suggested action

### 4.7 计量可信度

将 usage confidence 作为用户可见概念贯穿请求列表、模型聚合和成本展示。

建议状态：

| Confidence | 条件 | 含义 |
|---|---|---|
| `observed` | HTTP response/stream 直接采到 usage | 可信度最高 |
| `side_channel` | CPA usage queue 补充 | 可信，但依赖 request-id/side-channel 语义 |
| `request_only` | 只记录请求事实 | 不能代表完整成本或 token |
| `missing_usage` | 本应有 usage 但没有采到 | 需要诊断 |
| `unsupported` | 当前 profile 不支持 usage 提取 | 正常限制 |
| `conflict` | HTTP 与 side-channel 不一致 | 需要人工关注 |

实现方式：

- 已有 `capture_outcome`、`capture_reason`、`usage_source`、`side_usage_match_status` 可作为基础。
- 请求列表直接展示 confidence badge。
- 模型/成本报表展示 confidence breakdown。
- 总成本如果包含 missing/request-only/unpriced，应显示 partial。

### 4.8 轻量价格维护

价格维护不是主线，只做个人部署下的便利工具。

输入来源：

- CPA `/v1/models`
- 实际请求/响应中的 model
- 当前 `pricing.yaml`

功能：

- 列出实际使用但未配置价格的模型。
- 按 token/request count 排序，优先提示真正影响成本估算的模型。
- 生成 pricing stub，减少手写 YAML。
- 提示 alias 候选，但不自动猜价格。

建议命令：

```bash
metering-proxy pricing missing --config config.yaml
metering-proxy pricing stub --config config.yaml --range 30d
```

stub 示例：

```yaml
pricing:
  example-model:
    input_per_1m: 0
    cached_input_per_1m: 0
    cache_creation_per_1m: 0
    output_per_1m: 0
```

WebUI 中只需要一个小区域：

- `2 unpriced models in this range`
- `Generate pricing stub`
- `Cost estimate is partial`

## 5. 已知缺陷修复

### 5.1 Salt 一致性保护

问题：

项目要求历史 DB 有数据时 salt 文件必须保持一致，否则 `api_key_hash` 和 `client_ip_hash` 历史聚合语义会断裂。当前启动流程只检查 salt 文件存在，还没有完整的 salt fingerprint guard。

目标：

- 新 DB 首次启动记录 salt fingerprint。
- 旧 DB 首次升级时，如果已有数据但无 fingerprint，进入一次性绑定流程。
- 后续启动如果 fingerprint 不匹配，拒绝启动并输出明确错误。
- 不记录 salt 明文。

建议设计：

新增元数据表：

```sql
CREATE TABLE IF NOT EXISTS db_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);
```

记录：

```text
salt_fingerprint = SHA256("metering-proxy-salt-fingerprint-v1:" + salt_bytes)
hash_algorithm = hmac-sha256-v1
```

启动流程：

1. 加载 salt。
2. 打开 DB 并迁移。
3. 查询 `request_usage` 是否有历史数据。
4. 查询 `db_metadata.salt_fingerprint`。
5. 如果无历史数据且无 fingerprint：写入 fingerprint。
6. 如果有历史数据且无 fingerprint：写入当前 fingerprint，并记录日志说明这是 legacy bind。
7. 如果有 fingerprint 且不匹配：拒绝启动。

验收：

- 空 DB 首次启动正常写入 fingerprint。
- 有历史数据的 legacy DB 首次升级能绑定当前 salt。
- 绑定后换 salt 启动失败。
- 错误信息包含 DB path、salt path、恢复建议。

### 5.2 Health timeline 第一桶基线

问题：

当查询范围之前没有 health_metrics baseline 时，第一桶可能显示进程累计值，而不是区间增量。

目标：

- 尽可能使用 since 前最近一条 health_metrics 作为 baseline。
- 如果找不到 baseline，第一桶标记 `baseline_missing=true`。
- WebUI 不把 baseline missing 的第一桶当作真实区间增量突出展示。

建议 API 变化：

```json
{
  "timestamp": "2026-07-08T00:00:00Z",
  "parse_errors": 0,
  "db_errors": 0,
  "dropped_events": 0,
  "baseline_missing": true
}
```

### 5.3 Quota 手动刷新与状态表达收敛

问题：

当前 quota poller 已有框架，但 `adapterForProvider` 尚无实际 provider 支持。更大的问题是：quota 被设计成后台定期 probe/poll，这对个人部署不划算。它会持续访问 CPA management API、制造 CPA Docker log 噪音，并且在没有 provider adapter 时几乎没有收益。

结合 CPA 当前源码还需要补充一点：CPA 的 `quota`/`reset-quota` 主要服务于请求失败后的 auth/model cooldown 与路由恢复，不是 provider 真实余额快照。MeteringProxy 可以读取和展示这类 cooldown/health 状态，但不能把它命名为完整额度。完整额度只能来自 provider-specific quota endpoint，并通过固定模板 `api-call` 手动刷新后解析。

原则：

- quota snapshot 默认手动刷新。
- 用户不点 WebUI 刷新，MeteringProxy 就不主动刷新 quota。
- 启动时不做 quota `/api-call` probe。
- 打开 WebUI 只读最近一次缓存结果，不同步请求 CPA。
- 没有缓存时显示 `not_refreshed`，而不是自动刷新。
- 只有 provider adapter 可靠产出归一化数据时，才显示 full quota available。
- `reset-quota` 是“清理 CPA 内部 cooldown”，不是“恢复 provider 额度”，WebUI 文案必须区分。

建议配置：

```yaml
quota:
  enabled: true
  refresh_mode: "manual"   # manual | disabled，后续如有明确需求再增加 scheduled
  min_refresh_interval: "60s"
```

也可以更保守地用现有字段表达：

```yaml
quota:
  enabled: false
```

后续如果保留 `enabled: true`，语义应调整为“允许手动刷新”，而不是“启动后台轮询”。

目标：

- 明确区分：
  - not refreshed
  - management reachable
  - credential health available
  - quota api-call reachable
  - provider adapter supported
  - full quota available
- 只有至少一个 supported provider adapter 写入有效 quota row 时，才显示 full quota available。

建议状态：

| State | 含义 |
|---|---|
| `disabled` | 配置关闭 |
| `not_refreshed` | 已允许 quota，但尚未手动刷新 |
| `refreshing` | 已触发刷新，结果尚未写入 |
| `unsupported` | API 可达但无 provider adapter |
| `unavailable` | management/api-call 不可达 |
| `rate_limited` | 命中最小刷新间隔或 provider 限制 |
| `stale` | 有旧缓存，但超过 freshness 窗口 |
| `error` | 最近一次刷新失败 |
| `partial` | 部分 provider/credential 有数据 |
| `available` | 有受支持 quota row |

建议 API 行为：

- `GET /metering/api/provider-quota`：只返回缓存，不触发 CPA 请求。
- `POST /metering/api/provider-quota/refresh`：触发一次异步刷新，立即返回 `refresh_queued=true` 或当前 in-flight 状态。
- `GET /metering/api/provider-quota/diagnostics`：展示最近刷新结果、错误和时间。
- 连续 refresh 在 `min_refresh_interval` 内返回 `refresh_queued=false` 和 `reason=rate_limited`。

实现形态：

- 用 `RefreshService` 替代后台 `Poller` 作为 quota 的主抽象。
- `RefreshService` 只响应显式 refresh，不在 `Start()` 中 probe 或 loop。
- credential health 和 provider quota 分别维护 cache；provider quota 不可用时，不能把 credential health 称为 full quota fallback。
- 旧 `/metering/api/quota` 如保留，只能作为兼容聚合读接口，且不得触发 refresh。

验收：

- 启动 MeteringProxy 不触发 CPA quota/api-call 日志。
- 打开 WebUI 不触发 CPA quota/api-call 日志。
- 点击刷新才触发一次 quota refresh。
- 无 provider adapter 时，刷新后显示 unsupported，并且不会进入后台重复刷新。
- full quota unavailable 不影响 credential health 和 usage queue 展示。
- CPA cooldown reset 可单独成功或失败，不改变 provider quota snapshot 的状态。

### 5.4 Compressed SSE 诊断显性化

问题：

项目坚持 header 透明，不主动删除 `Accept-Encoding`。如果客户端请求压缩 SSE 且上游返回压缩流，代理无法在不改变字节的前提下解析 usage。

目标：

- 继续保持转发透明。
- 不为了计量强行改 header。
- 当 stream 转发成功但 usage 不可见时，尽量在 capture reason 中给出更明确的诊断。

建议：

- 在 request/response header 信息允许判断时，标记 `capture_reason=compressed_stream_not_metered`。
- issues 中增加对应解释。
- WebUI 明确表达“请求已正常转发，只是计量不可见”。

## 6. 优化项

### 6.1 Doctor 命令

新增本地诊断命令：

```bash
metering-proxy doctor --config config.yaml
```

检查内容：

- config 可解析。
- salt 文件存在、权限合理、fingerprint 匹配。
- DB 可打开、迁移可执行。
- pricing 文件可解析。
- upstream URL 可达。
- `/v1/models` 可访问。
- management key 可读。
- auth-files 可访问。
- usage queue transport 可用。
- quota 状态是否 disabled/not_refreshed/unsupported/unavailable/available。

输出应适合直接复制到排障记录中。

### 6.2 Healthcheck

新增：

```text
GET /healthz
GET /readyz
```

建议语义：

- `/healthz`：进程存活即可返回 200。
- `/readyz`：DB、pricing、salt、基础配置 OK 才返回 200；upstream 可达性可作为 warning，不一定阻断。

Dockerfile 或 README 可加入 healthcheck 示例。

### 6.3 WebUI 异常/空状态

重点补：

- 空 DB。
- CPA 不可达。
- management disabled。
- usage queue disabled。
- quota unsupported。
- 全部数据 request-only。
- 成本 partial。
- 模型全部未配置价格。

### 6.4 查询性能与保留策略

建议补充：

- 大数据量 demo/fixture。
- overview/models/issues/requests query plan 检查。
- side_usage_events retention 配置验证。
- quota_refresh_events retention 配置验证。
- health_metrics 可选保留策略。

## 7. 实施顺序

### Phase 1：功能能力优先

目标：先补强用户每天会用到的能力。

交付：

1. AI 透明网关能力视图。
2. 透明代理与 LLM 体验指标/策略设计落地。
3. 模型资产视图。
4. Issues 诊断增强。
5. CPA 状态检查作为支撑排障能力。
6. Pricing gaps/stub 小工具。

验收：

- 能从 WebUI 看出哪些 AI 路由经过网关、哪些可计量、哪些 request-only、哪些 passthrough。
- 能看出流式/非流式、usage-metered/request-only/passthrough、missing usage 的分布。
- 能区分上游慢、代理传输错误、采样/计量不可见和后台 management 噪音。
- request-only 与 passthrough 路径保持低开销，不为了报表读取大请求体。
- 能看出 CPA 可用模型、实际使用模型、价格配置之间的差异。
- 能看出 CPA 是否可达、usage queue 是否连接、quota 为什么不是 full available，但这些状态不喧宾夺主。
- 高频未定价模型能被列出并生成 stub。
- issues 能给出简短排障建议。

### Phase 2：已知缺陷修复

交付：

1. salt fingerprint guard。
2. health timeline baseline 修复。
3. quota 手动刷新与状态表达收敛。
4. compressed SSE 诊断显性化。

验收：

- 换 salt 后不能静默写入新数据。
- health error timeline 第一桶不再误导。
- quota 不再后台自动刷新，不再把 api-call reachable 误表达为 full quota。
- 打开 WebUI 不触发 CPA quota/api-call 请求；只有点击刷新才触发。
- 压缩 stream 计量不可见时有诊断原因。

### Phase 3：可信度与多模态深化

交付：

1. usage confidence 全链路展示。
2. request-only endpoint 详情增强。
3. side-channel 诊断增强。
4. audio/video/files 根据可观测事实继续补充，但不冒进宣称 usage-metered。

验收：

- 请求列表、模型聚合和成本视图都能表达数据来源与可信度。
- request-only 数据不会被误解为完整用量。
- side-channel 状态和冲突能被定位。

## 8. 测试要求

后端：

- `go test ./...`
- `go vet ./...`
- 涉及并发/队列/HTTP 的改动补 `go test -race ./...`。

代理透明性：

- 现有 golden tests 必须保持通过。
- 新增功能不得改变转发字节、状态码、header 和 SSE flush 行为。

DB：

- legacy DB migration test。
- salt fingerprint guard test。
- metadata 表重复启动幂等 test。
- health baseline test。

WebUI/API：

- 空状态 test。
- gateway capabilities test。
- transparent proxy experience metrics/status test。
- CPA unavailable test。
- quota unsupported test。
- quota manual refresh does not poll on startup/open test。
- unpriced model test。
- model asset source merge test。

隐私：

- 不保存 prompt、response text、图片数据、音频数据、文件内容。
- 不返回 management key、API key、明文 IP、明文 credential identity。
- error message 继续走 sanitize/truncate/redaction。

## 9. 推荐下一步

建议先从 Phase 1 开始，但在实现前先拆成三张小任务：

1. **透明网关能力视图**
   - 复用 `profile.Registry` 作为能力来源。
   - 复用 `request_usage` 聚合最近流量。
   - 输出 gateway capabilities API 与 WebUI 概览。
   - 明确区分 usage-metered、request-only、passthrough、missing usage。

2. **透明代理与 LLM 体验优化**
   - 增加代理自身开销和 transport error 的可观测指标。
   - 明确 WebSocket/Realtime/Upgrade 支持或绕过策略。
   - 为 request-only/passthrough 路径设计低开销快路径。
   - 为 management 请求增加 User-Agent/组件标识和退避策略。

3. **模型资产与价格缺口**
   - 复用 CPA `/v1/models`。
   - 复用 DB models/request queries。
   - 输出 WebUI/API 与 pricing stub。

4. **CPA 状态与 issues 诊断**
   - 复用 observability/quota cache/usage queue/credential health。
   - 补状态聚合 API。
   - 补问题类别文案和建议。

这些任务最贴近“功能、产品能力补强”。其中透明网关能力视图和 LLM 体验优化应排在最前，因为它们定义 MeteringProxy 作为 AI 中转站的核心使用体验；CPA 状态检查是必要支撑，但不是主线。

## 10. 复审后的修改优化方案

重新审视本方案后，建议做以下收敛调整。

### 10.1 增加 Phase 0：代理热路径保护

当前方案已经强调透明代理优先，但实施顺序里还缺少一个明确的“热路径不退化”前置阶段。建议在所有 WebUI、quota、模型资产功能之前，先建立代理主路径的保护线：

交付：

1. 明确 HTTP/SSE/Upgrade 三类转发路径的行为不变量。
2. 增加或整理透明转发 golden tests：状态码、header、body bytes、SSE flush 行为不能被观测功能改变。
3. 增加 request-only/passthrough 大请求体测试，确保不完整读入内存。
4. 增加 management 功能隔离测试，证明 CPA auth/quota/usage queue 不持有代理热路径锁。
5. 建立简单性能基线：非流式、SSE、request-only 大 body 的 p50/p95 overhead 和内存占用。

验收：

- 关闭 WebUI、credential health、quota、usage queue 时，代理行为与基础透明代理一致。
- 打开这些观测能力后，LLM 请求路径不等待 CPA management 请求。
- SSE 不因为计量逻辑被额外缓冲。
- request-only 与 passthrough 不因为报表需求读取大 body。

### 10.2 调整功能优先级

原 Phase 1 可以继续保留，但建议按下面顺序执行：

1. `Transparent Proxy Core`：热路径保护、transport 参数、SSE/Upgrade 策略、request-only 快路径。
2. `Gateway Capability View`：让 WebUI 先回答“哪些路由经过网关、哪些可计量、哪些只是透明转发”。
3. `Issue Diagnosis`：把 missing usage、compressed stream、side-channel 断连、CPA 不可达、quota unsupported 变成可行动问题。
4. `CPA Auth Mirror`：作为维护辅助，手动刷新，不后台轮询。
5. `Provider Quota Snapshot`：可选手动刷新，只有 adapter 验证后启用。
6. `Model Assets + Pricing Stub`：轻量维护工具，不作为核心主线。

这样排序更符合项目定位：MeteringProxy 首先是 AI 中转站透明网关，其次才是本地运维控制台。

### 10.3 收窄 CPA/OAuth 范围

CPA 集成应作为 management mirror，而不是第二套 management center：

- `CPA Auth Mirror` 是主能力。
- `CPA Cooldown State` 是维护动作和诊断信号。
- `Provider Quota Snapshot` 是手动可选能力。
- OAuth 在第一轮不进入 WebUI，只保留“可后续接入 CPA Management 入口”的设计位。
- 不实现 provider OAuth 流程，不编辑 CPA 配置，不暴露 generic `/api-call`。

### 10.4 明确数据语义比功能数量更重要

下一阶段最容易失控的点不是功能做少，而是把不同可信度的数据混在一起。建议把以下字段作为 UI 和 API 的长期约束：

- `capture_mode`：`usage_metered`、`request_only`、`passthrough`、`unsupported`。
- `usage_source`：HTTP response、SSE、side-channel、none。
- `confidence`：observed、side_channel、request_only、missing_usage、unsupported、conflict。
- `quota_state`：disabled、not_refreshed、refreshing、unsupported、unavailable、rate_limited、stale、partial、available、error。
- `source`：CPA models、requested model、returned model、pricing config、provider adapter。

UI 可以变，但这些语义不能含糊。只要语义稳定，后续报表、issues、model assets 和 quota 页面都能自然演进。

### 10.5 推荐落地顺序修订

建议把“下一步”修订为：

1. Phase 0：热路径保护和回归测试。
2. Phase 1：透明网关能力视图 + LLM 体验指标。
3. Phase 2：已知缺陷修复，优先 salt guard、health baseline、compressed SSE 诊断。
4. Phase 3：CPA Auth Mirror + 手动 Provider Quota Snapshot。
5. Phase 4：模型资产视图 + pricing stub。
6. Phase 5：usage confidence 和多模态/request-only 深化。

如果开发资源有限，Phase 0、Phase 1、Phase 2 应先于所有 quota/OAuth 相关功能。quota 相关功能的正确目标是“不要误导、不要打扰 CPA、不要影响 LLM 请求”，而不是做成完整额度平台。
