# WebUI 下一版实施方案

版本定位：v0.2.0 设计稿
状态：待实施
适用范围：MeteringProxy WebUI、报表 API、轻量解析、数据库报表层

## 1. 核心结论

下一版的目标不是继续给现有页面补几个样式，而是把 WebUI 从“数据表堆叠”调整为“透明代理的轻量运维仪表盘”。

MeteringProxy 的核心仍然是：

- 透明代理 LLM 请求。
- 尽可能不影响流式与非流式 LLM 体验。
- 提供异步、只读、可解释的计量与健康可视化。

因此下一版允许增加解析，但必须遵守以下边界：

- 不保存 prompt、messages、response text、tool arguments、图片或文件内容。
- 不为了统计、诊断、展示而改变上游响应字节。
- 不在转发前做任何会决定是否放行的业务判断。
- 不为了 WebUI 数据阻塞正常响应输出。
- 所有解析都必须 bounded，包括内存、字段长度、JSON 深度和采样大小。
- 解析失败只能影响观测数据质量，不能影响代理转发。

下一版应该把正常状态降噪，把异常状态变得可诊断。正常时页面应当克制；出错时页面应当直接告诉操作者发生了什么、影响了谁、最近一次在哪里。

## 2. 本版必须交付

本版作为一个完整版本交付，不拆成阶段。以下内容要么全部做好，要么在实施前明确裁剪。

### 2.1 WebUI 信息架构重做

首屏只展示高优先级信息：

- 请求量。
- Token。
- 预估成本。
- P95 延迟。
- 最近问题或当前健康状态。

从首屏降级的内容：

- 24h 失败率不再单独占一个大卡片。
- 采集质量不再单独占一个大卡片。
- 采集健康不再使用四个大 tile 默认展示。

替代方式：

- 失败率显示为“最近 1h / 当前范围”的紧凑状态。
- 采集健康显示为一条小型状态摘要，例如“采集正常，队列 0，丢弃 0”。
- 只有当失败、采集跳过、解析错误、DB 写入错误、队列积压非零时，才提升为 warning 或 error 状态。

### 2.2 错误视图重做

默认错误模块不再展示稀疏 bucket 表。

默认展示逻辑：

- 没有错误时显示一句明确状态：`No issues in this range` / `此范围内没有问题`。
- 有错误时展示紧凑 issue list。

Issue list 每行展示：

- 错误分类。
- 次数。
- 最近一次时间。
- HTTP 状态码。
- 影响模型。
- endpoint。
- 示例错误消息。

请求级错误分类至少包括，这些分类来自 `request_usage.error_class`：

- `quota_exhausted`：额度耗尽。
- `rate_limited`：限流。
- `auth_failed`：认证失败。
- `invalid_request`：请求参数错误。
- `context_length`：上下文长度超限。
- `upstream_5xx`：上游 5xx。
- `proxy_upstream_error`：代理无法连接上游。
- `unknown`：未能分类。

系统级问题不写入 `request_usage.error_class`，只来自采集状态、writer 计数器和 `health_metrics`：

- `capture_parse_error`：采集解析错误。
- `db_write_error`：数据库写入错误。
- `dropped_event`：事件丢弃。

bucket 明细仍然保留，但放在“展开详情”里，用于排查趋势，不作为默认页面主体。

### 2.3 失败响应轻量解析

对 `status >= 400` 的响应进行适度解析，收益高于风险，因为请求已经失败，不存在正常 LLM 输出体验。

解析策略：

- 非流式失败响应：继续先转发给客户端，同时用现有 bounded sample 采样。复制完成后解析。
- 流式失败响应：必须建立独立的错误采样分支，不能只复用现有 usage 解析。处理顺序必须是先 `write` + `flush` 给客户端，再在旁路中收集有限的 SSE `data:` payload 或 JSON sample。采样只在 `status >= 400` 时启用，最多保留前 5 个 SSE `data:` payload，且总 payload 不超过 8KB，结束后统一解析。
- 代理自身上游错误：不向客户端泄漏内部连接错误，但入库时记录安全分类。

流式路径约束：

- `tryExtractSSEUsage` 继续只负责 usage，不扩展为通用错误解析器。
- 新增的流式错误采样器只保存 stripped `data:` payload，不保存原始流。
- 采样、strip、解析都必须发生在 flush 之后。
- 解析失败、采样溢出、JSON 不合法只能导致错误分类为空或 `unknown`，不能影响下一次读取、写入或 flush。
- 如果错误响应是 SSE，第一个或前几个 `data:` event 可能就是错误 JSON，因此采样器要能处理 `data: {"error": ...}` 和多行 SSE。

支持的错误结构：

OpenAI 风格：

```json
{
  "error": {
    "message": "...",
    "type": "...",
    "code": "...",
    "param": "..."
  }
}
```

Anthropic 风格：

```json
{
  "type": "error",
  "error": {
    "type": "...",
    "message": "..."
  }
}
```

通用风格：

```json
{
  "message": "...",
  "code": "...",
  "type": "..."
}
```

落库字段建议：

- `error_class TEXT DEFAULT ''`
- `error_type TEXT DEFAULT ''`
- `error_code TEXT DEFAULT ''`
- `error_param TEXT DEFAULT ''`
- `error_message TEXT DEFAULT ''`
- `error_message_truncated INTEGER DEFAULT 0`

字段约束：

- `error_message` 最多保存 500 UTF-8 bytes 或等价安全长度。
- 去除换行控制字符。
- 对疑似 API key、Bearer token、长 base64、长 hex 串做脱敏。
- 不保存原始响应体。

### 2.4 正常请求与响应的轻量元数据解析

正常请求和响应可以做适度解析，但必须比失败解析更克制。

本版默认范围：

- `model`，已有。
- `stream`，已有。

`finish_reason` / `stop_reason` 不作为 v0.2.0 强制项。原因是 Chat Completions 流式响应的 `finish_reason` 出现在非 usage SSE 行中，当前 usage 解析器不会捕获；如果本版实施它，必须新增轻量的 terminal metadata parser，并保证仍然先 `write` + `flush` 再解析。

以下请求元数据允许后续扩展，但不作为 v0.2.0 必须落库项，除非本版 UI 明确消费：

- `reasoning_effort`。
- `service_tier`。
- `max_tokens` 或 `max_output_tokens`。
- `temperature`。
- `tool_count`。
- `input_modality_flags`，只保存是否包含文本、图片、文件等粗粒度布尔信息，不保存内容。

允许解析并保存的成功响应元数据：

- `model_returned`，已有。
- `finish_reason` 或 `stop_reason`。
- usage 与 token 细分，已有。

不允许解析或保存：

- message content。
- output text。
- tool arguments。
- image URL。
- file 内容。
- 任意用户 prompt 片段。

可选落库字段，只有实施对应提取逻辑时才新增。非流式响应可以从现有 bounded sample 中低风险提取；流式响应必须新增 terminal metadata parser：

- `finish_reason TEXT DEFAULT ''`
- `stop_reason TEXT DEFAULT ''`

延后字段，除非实施时确认 UI 会直接使用：

- `reasoning_effort TEXT DEFAULT ''`
- `service_tier TEXT DEFAULT ''`
- `max_output_tokens INTEGER DEFAULT 0`
- `temperature REAL DEFAULT 0`
- `tool_count INTEGER DEFAULT 0`
- `has_text_input INTEGER DEFAULT 0`
- `has_image_input INTEGER DEFAULT 0`
- `has_file_input INTEGER DEFAULT 0`

这些字段只作为分析和筛选元数据，不参与代理转发决策。

### 2.5 模型归属修正

当前模型统计只按 `model_returned` 聚合。失败响应通常没有 `model_returned`，因此会落到 `unknown`，这在生产 UI 中很突兀。

下一版模型归属规则：

```text
model_returned -> model_requested -> unidentified
```

后端报表需要返回：

- `model`：用于展示的有效模型。
- `model_source`：`returned` / `requested` / `unidentified`。
- `request_count`。
- `failed_count`。
- token 与 cost 字段。

该变更必须贯穿：

```text
db.ModelRow -> event.ModelReport -> /api/models -> WebUI models table/filter
```

不能只修改 SQL 聚合而遗漏 report 类型。`failed_count` 用于模型视图和问题诊断，`model_source` 用于解释模型是来自响应、请求还是无法识别。

UI 文案规则：

- 不直接显示 `unknown`。
- 如果模型无法归属，显示“无法识别模型” / `Unidentified model`。
- 如果价格未配置，显示“未配置价格” / `Unpriced`，而不是 `unknown`。
- 如果存在未配置价格模型，总成本区域显示“部分估算”。

已知限制：

- `model_requested` 只从当前请求 body prefix 中提取，默认 prefix 为 4KB。
- 如果请求体很大且 `model` 字段落在 prefix 之后，模型可能仍然无法归属。这是透明代理边界下的可接受限制，不应为了模型归属继续预读更大的请求体。

### 2.6 最近 1h 指标

当前范围最短是 24h，但失败判断更适合看最近 1h。

下一版必须提供最近 1h 指标：

- 最近 1h 请求数。
- 最近 1h 失败数。
- 最近 1h 失败率。
- 最近 1h P95 latency。
- 最近 1h 最新错误。

这不一定要作为全局 range 选项暴露给用户，但后端需要支持 WebUI 获取。

建议新增 `/metering/api/overview`，但该接口必须支持局部失败，不能 all-or-nothing。每个 section 使用 `data` + `error` envelope。某个 DB 查询失败时，其它 section 仍应正常返回。

```json
{
  "range": "24h",
  "selected": {
    "data": {
      "total_requests": 100,
      "failed_requests": 1,
      "total_tokens": 100000,
      "total_cost": 0.12,
      "p95_latency_ms": 8000,
      "p95_ttfb_ms": 1200
    },
    "error": ""
  },
  "recent_1h": {
    "data": {
      "total_requests": 12,
      "failed_requests": 0,
      "failure_rate": 0,
      "p95_latency_ms": 5000,
      "latest_error": null
    },
    "error": ""
  },
  "capture": {
    "data": {
      "queue_depth": 0,
      "dropped_events": 0,
      "parse_errors": 0,
      "db_write_errors": 0,
      "capture_failed": 0,
      "capture_skipped": 0,
      "status": "healthy"
    },
    "error": ""
  },
  "cost": {
    "data": {
      "known_cost": 0.12,
      "unpriced_models": 0,
      "partial": false
    },
    "error": ""
  }
}
```

现有接口继续保留，避免破坏兼容。

`cost.data.unpriced_models` 基于当前范围内的 effective model 聚合结果计算，不扫描原始请求行。实现时可以复用 models 报表结果逐个调用 pricing matcher；如果模型数量异常大，可以限制为报表返回的前 N 个模型并在响应中标记 `partial`。

兼容性规则：

- 旧 `/api/summary` 保持兼容，不强制增加 `partial` 字段。
- 成本是否部分估算由 `/api/overview.cost` 和 `/api/models` 汇总逻辑表达。
- WebUI 的总成本卡片优先使用 overview cost section；如果 overview 不可用，再退回 summary 并显示较保守的文案。

### 2.7 新增 Issue API

建议新增 `/metering/api/issues?range=24h&limit=20`。

API 语义：

- `items` 只包含请求级问题，来源是 `request_usage.error_class` 与请求行上的安全错误摘要。
- `system` 只包含系统级采集问题，来源是 writer 快照与 `health_metrics`，包括 `capture_parse_error`、`db_write_error`、`dropped_event`。
- WebUI 可以把请求级问题和系统级问题合并成一个视觉模块，但 API 层必须保持来源分离。

错误来源矩阵：

| 来源 | 分类写入位置 | 典型 class |
|------|-------------|------------|
| 上游返回 `status >= 400` 且 body 可解析 | extractor 解析 sample 后写入 `request_usage.error_class` | `quota_exhausted`, `rate_limited`, `auth_failed`, `invalid_request`, `context_length`, `upstream_5xx`, `unknown` |
| proxy `RoundTrip` 失败，无上游响应体 | proxy `writeError()` 直接写入 event，不经过 extractor | `proxy_upstream_error` |
| usage / 响应采集解析失败 | `capture_outcome` / `capture_reason` 与 health/system 聚合 | `capture_parse_error` |
| writer DB 写入失败 | writer 计数器与 `health_metrics` | `db_write_error` |
| writer 队列满导致事件丢弃 | writer 计数器与 `health_metrics` | `dropped_event` |

返回结构：

```json
{
  "range": "24h",
  "total": 2,
  "items": [
    {
      "class": "quota_exhausted",
      "label": "Quota exhausted",
      "count": 1,
      "severity": "error",
      "latest_at": "2026-05-04T01:23:45Z",
      "status": 429,
      "endpoint": "/v1/responses",
      "model": "gpt-5.4",
      "model_source": "requested",
      "api_key_hash": "abc123...",
      "error_type": "insufficient_quota",
      "error_code": "insufficient_quota",
      "message": "You exceeded your current quota...",
      "request_id": "..."
    }
  ],
  "system": {
    "parse_errors": 0,
    "db_errors": 0,
    "dropped_events": 0,
    "items": [
      {
        "class": "capture_parse_error",
        "label": "Capture parse error",
        "count": 5,
        "scope": "range",
        "severity": "warning"
      }
    ]
  }
}
```

计数语义：

- 请求级 `items[].count` 是当前 `range` 内的聚合值。
- 系统级 `system.items[].count` 优先使用当前 `range` 内 `health_metrics` 的 delta 值。
- 如果某个系统计数只能来自当前进程 writer snapshot，无法可靠换算到 range，则该 item 必须返回 `scope: "process"`；如果来自 range delta，则返回 `scope: "range"`。
- WebUI 展示系统级 process scope 时必须明确文案，例如“since process start” / “自进程启动以来”，避免和请求级 range count 混淆。

排序规则：

- 严重程度高的靠前。
- 最近发生的靠前。
- 请求错误聚合键为 `error_class + status + endpoint + effective_model + api_key_hash`。`error_code`、`message`、`request_id` 取最新一条作为示例。
- 系统采集类错误单独聚合，不和请求错误混在同一聚合键里。
- 如果升级前历史数据 `error_class = ''`，UI 按 `unknown` 处理并显示为“未分类错误” / `Unclassified issue`。
- 请求级和系统级 item 都必须返回 `severity`，前端不需要重复实现 class 到 severity 的映射。

严重程度参考：

- `error`: `auth_failed`, `quota_exhausted`, `proxy_upstream_error`, `db_write_error`
- `warning`: `rate_limited`, `upstream_5xx`, `context_length`, `capture_parse_error`, `dropped_event`
- `info`: `invalid_request`, `unknown`

同一严重程度内，请求级 issue 按 `latest_at DESC` 再按 `count DESC` 排序；系统级 issue 按 severity 再按 count 排序。

### 2.8 表格与对齐规范

下一版需要建立统一表格规范，而不是逐个表手调。

列类型规则：

- 文本列左对齐。
- 时间列左对齐，使用等宽字体。
- 纯数字列右对齐，使用等宽字体。
- 金额列右对齐。
- badge 状态列居中或左对齐二选一，全站统一。
- progress bar 列使用固定内部布局：左侧 bar，右侧数值，整体不依赖 table cell 的默认对齐。

模型表建议列：

- 模型。
- 请求。
- 失败。
- Token。
- 平均 Token。
- 缓存率。
- 估算成本。
- 价格状态。

成本占比可以保留，但建议作为 tooltip 或次级视觉，不再和“成本占比 token share”混合展示。

### 2.9 图表继续优化

图表保持无构建链 SVG。

需要完成：

- Requests 图表 tooltip 与 bucket hover 行为稳定。
- Tokens 图表视觉更轻，不要像彩色堆叠墙。
- 错误图不默认展示表格，可在 issue detail 中用迷你趋势或 bucket 明细。
- 图表容器不得出现横向或纵向滚动条。
- 移动端图表高度和标签不能溢出。

### 2.10 文案与双语

下一版中文和英文都要完整覆盖。

文案原则：

- `unknown` 只用于技术兜底，不直接显示给用户。
- `Unpriced` / “未配置价格”用于价格缺失。
- `Unidentified model` / “无法识别模型”用于模型归属缺失。
- `Unclassified issue` / “未分类错误”用于升级前或无法分类的错误数据。
- `Partial estimate` / “部分估算”用于成本不完整。
- `No issues in this range` / “此范围内没有问题”用于正常错误状态。

## 3. 建议文件改动范围

后端与数据：

- `internal/event/event.go`
- `internal/event/report.go`
- `internal/event/mapping.go`
- `internal/db/db.go`
- `internal/db/db_test.go`
- `internal/db/demo.go`
- `internal/extractor/error.go`
- `internal/extractor/request_meta.go`（仅当本版实施额外请求元数据字段时）
- `internal/extractor/extractor_test.go`
- `internal/proxy/proxy.go`
- `internal/proxy/proxy_test.go`
- `internal/proxy/golden_test.go`
- `internal/webui/server.go`
- `internal/webui/server_test.go`

前端：

- `internal/webui/static/index.html`
- `internal/webui/static/app.js`
- `internal/webui/static/styles.css`
- `internal/webui/static/i18n.js`

文档：

- `README.md`
- `docs/webui-next-version-implementation-plan.md`

新增字段必须贯穿以下链路并有测试覆盖：

```text
extractor/proxy -> event.Event -> event.EventToRecord -> db.UsageRecord -> report API -> WebUI
```

不能只改数据库结构或 WebUI 字段名，遗漏事件映射层。

数据库写入注意事项：

- `InsertBatch` 当前使用位置参数，新增 error 字段后参数数量会继续增加。
- 实施时可以继续使用位置参数，但必须用测试覆盖字段顺序；如果改动范围允许，优先考虑命名参数或集中构造参数列表，降低错位风险。
- `event.EventToRecord`、`db.UsageRecord`、`InsertBatch` 的字段顺序必须同步审查。

## 4. 数据库迁移策略

本版只允许增量迁移：

- 新增列。
- 新增索引。
- 新增报表查询。
- 新增只读 API。

不做：

- 删除列。
- 重命名列。
- 改列类型。
- 重建主表。

建议新增索引：

- `idx_request_usage_error_class_created_at_unix`
- `idx_request_usage_model_requested_created_at_unix`
- `idx_request_usage_model_returned_created_at_unix`
- `idx_request_usage_status_created_at_unix`

如果索引数量需要控制，优先级为：

1. `status + created_at_unix`
2. `error_class + created_at_unix`
3. `model_returned + created_at_unix`
4. `model_requested + created_at_unix`

`effective_model` 的 SQL 落地方式：

```sql
COALESCE(NULLIF(TRIM(model_returned), ''), NULLIF(TRIM(model_requested), ''), 'unidentified')
```

该表达式需要用于：

- `Models()` 聚合。
- `/api/issues` 聚合键。
- overview 成本与未配置价格统计。

首版不新增 SQLite VIEW，避免引入额外迁移复杂度。可以在 Go 代码中集中维护这个 SQL 表达式常量，避免多个查询临时手写导致不一致。

`model_source` 的 SQL 落地方式需要和 `effective_model` 同步维护。对未聚合的请求行：

```sql
CASE
  WHEN NULLIF(TRIM(model_returned), '') IS NOT NULL THEN 'returned'
  WHEN NULLIF(TRIM(model_requested), '') IS NOT NULL THEN 'requested'
  ELSE 'unidentified'
END
```

对 `Models()` 这种按 effective model 聚合后的结果，需要在聚合上下文中判断该组是否至少有一行来自响应模型：

```sql
CASE
  WHEN SUM(CASE WHEN NULLIF(TRIM(model_returned), '') IS NOT NULL THEN 1 ELSE 0 END) > 0 THEN 'returned'
  WHEN SUM(CASE WHEN NULLIF(TRIM(model_requested), '') IS NOT NULL THEN 1 ELSE 0 END) > 0 THEN 'requested'
  ELSE 'unidentified'
END
```

这两个表达式也应在 Go 代码中集中维护，避免 models、issues、overview 使用不同语义。

## 5. 解析实现细节

### 5.1 错误解析器

新增 `internal/extractor/error.go`。

核心类型：

```go
type ErrorInfo struct {
    Class            string
    Type             string
    Code             string
    Param            string
    Message          string
    MessageTruncated bool
}
```

核心函数：

```go
func ExtractErrorInfo(sample []byte, status int, contentType string) (*ErrorInfo, error)
```

输入约束：

- 非流式路径传入 bounded body sample。
- 流式 SSE 路径必须先 strip `data:` 前缀和 completion marker，再把 payload 传给错误解析器。
- 如果一个 SSE event 不包含 JSON 错误结构，解析器返回 nil，不计为采集解析错误。
- HTML、纯文本、空响应体都要能安全处理，不能产生 panic。
- 如果上游忽略 `DisableCompression` 仍返回 `Content-Encoding: gzip` 等压缩错误响应，错误解析器可以返回 nil，不计为解析错误。为了字节透明，本版不主动解压并重组响应体。

分类规则示例：

- status 401 或 403 -> `auth_failed`
- status 429 且 code/type/message 包含 quota、insufficient、balance、credit -> `quota_exhausted`
- status 429 且包含 rate、limit、too many -> `rate_limited`
- status 400 且包含 context、token limit、maximum context -> `context_length`
- status 400 -> `invalid_request`
- status >= 500 -> `upstream_5xx`

分类必须保守。不能确定时返回 `unknown`，不要过度猜测。

`proxy_upstream_error` 不经过 `ExtractErrorInfo()`。它发生在 proxy `RoundTrip` 失败时，没有上游响应体，必须由 proxy 层 `writeError()` 直接设置到 event 的 error fields。

### 5.2 请求元数据解析器

如本版实施额外请求元数据，再新增 `internal/extractor/request_meta.go`。如果本版只保留已有 `model` 与 `stream`，则不需要新增该文件。

解析只基于已有 request body prefix，默认 4KB。如果字段不在 prefix 内，不为了它继续读更多请求体。

这个限制需要明确写入代码注释与测试说明：大请求中 `model` 字段如果落在 4KB prefix 之后，`model_requested` 为空是可接受结果，不是 bug。

允许字段：

- model。
- stream。
- reasoning effort。
- service tier。
- temperature。
- max output tokens。
- tool count。
- 粗粒度 input modality flags。

不解析 messages 内容。

### 5.3 成功响应元数据解析

成功响应元数据解析是 v0.2.0 的可选项，不是主交付目标。只有在不扩大代理热路径复杂度的情况下才实施。

如果实施，范围限定为：

- 非流式 Chat Completions：从完整 bounded sample 中提取 `choices[].finish_reason`。
- 流式 Chat Completions：新增轻量 SSE terminal metadata parser，只检查包含 `"finish_reason"` 的 SSE payload，且必须在 flush 后解析。
- 非流式 Responses API：从 bounded sample 中提取稳定的 `response.status`、`response.incomplete_details.reason` 或等价停止原因。
- 流式 Responses API：只在 `response.completed` event 中提取停止原因，不扫描普通 delta 内容。

如果字段结构不稳定，优先不保存，不能让解析器变复杂。

## 6. WebUI 设计细节

### 6.1 页面结构

建议页面顺序：

1. Header 与状态条。
2. Overview metrics。
3. Requests / Tokens 图表。
4. Recent Issues。
5. Models。
6. API Keys。
7. Capture Diagnostics，默认折叠或紧凑展示。
8. Request Details，默认折叠。

### 6.2 Overview cards

建议卡片：

- Requests：主值为当前范围请求数，副值为最近 1h 失败。
- Tokens：主值为当前范围 token，副值为 cached/output/reasoning mix。
- Cost：主值为预估成本，副值显示 full estimate 或 partial estimate。
- Latency：主值为 P95 latency，副值为 P95 TTFB。
- Health：正常时显示 `Healthy`，异常时显示最高优先级 issue。

不再单独展示 24h failure rate 与 capture quality。

### 6.3 Recent Issues

Recent Issues 是下一版的核心信息模块。

UI 形态：

- 正常状态：一行安静的 empty state。
- 异常状态：紧凑列表。
- 每行使用轻量严重程度标识，不使用大面积红色。
- 示例消息最多两行，超出省略。
- 支持展开查看最近请求明细。

### 6.4 Models

模型表视觉目标：

- 读起来像成本和流量排行，而不是数据库 dump。
- 对齐稳定。
- 价格缺失和模型无法识别是说明性状态，不是惊吓式 warning。

建议：

- 模型名称下方可以显示 `from request` / `from response` 小字。
- `Unpriced` badge 使用 neutral/warn 之间的低强度颜色。
- 如果一个模型成本未知，成本列显示 `-`，价格列显示 `未配置价格`。
- 如果总成本是部分估算，在面板 subtitle 中展示。

### 6.5 Capture Diagnostics

采集健康降级为诊断模块。

默认显示：

```text
Capture healthy · queue 0 · dropped 0 · parse 0 · db 0
```

非零时：

```text
Capture attention · queue 3 · parse 2
```

展开后显示详细指标与最近 health_metrics。

## 7. 测试要求

本版不能只靠人工查看 UI。最低测试要求如下。

### 7.1 Extractor tests

覆盖：

- OpenAI quota exhausted。
- OpenAI rate limit。
- OpenAI auth failed。
- Anthropic error payload。
- generic message/code/type。
- 非 JSON 错误体。
- 空响应体错误，例如 status 500 但 body 为空。
- HTML 错误页面，例如反向代理返回的 502 HTML。
- SSE 错误流，例如多个 `data:` 行中某一行是错误 JSON。
- 超长 message 截断。
- API key 与 Bearer token 脱敏。
- 正常请求元数据解析不读取 message content。

### 7.2 Proxy tests

覆盖：

- `status >= 400` JSON 响应字节透明。
- 失败响应解析后入库 error fields。
- 非流式成功响应字节透明。
- 流式成功响应 flush 不被额外解析阻塞。
- 流式失败响应字节透明。
- 流式错误采样发生在 flush 之后。
- 上游连接失败不泄漏内部错误给客户端，但事件记录为 `proxy_upstream_error`。
- 采样溢出时不做不可靠解析。
- 错误解析 panic 被 recover，不能影响响应。
- panic recovery 测试可使用包内测试 hook 或 parser wrapper 注入 panic，但不要为测试暴露复杂的生产注入机制。
- 如果实施 `finish_reason` / `stop_reason`，必须覆盖流式 Chat Completions 的非 usage SSE 行提取。

### 7.3 DB tests

覆盖：

- 新增列迁移。
- 模型聚合 fallback：returned -> requested -> unidentified。
- issues 聚合按 `error_class + status + endpoint + effective_model + api_key_hash` 分组。
- 最近 1h 指标。
- 未配置价格模型导致 partial estimate。

### 7.4 WebUI server tests

覆盖：

- `/api/overview` no-store。
- `/api/issues` no-store。
- 旧接口兼容。
- base path 注入不破坏静态资源。

### 7.5 Frontend validation

最低要求：

- `node --check internal/webui/static/app.js`
- `go test ./...`
- `go build ./...`
- 手动浏览器验证桌面宽屏。
- 手动浏览器验证移动宽度。
- 确认图表无滚动条。
- 确认正常数据下页面低噪声。
- 确认错误数据下 Recent Issues 可读。

如果本地具备浏览器自动化环境，增加截图验证。

### 7.6 Overview performance validation

使用 demo 数据或测试夹具生成足够规模的事件后，验证 `/api/overview` 在合理时间内返回。

最低要求：

- 至少覆盖 100k 级别事件。
- 查询必须使用 `created_at_unix` 相关索引。
- section 局部失败时仍返回其它 section。
- 性能测试不要求作为每次 CI 必跑，但要有可复现命令或测试入口。

### 7.7 Golden fixtures

在 `testdata/fixtures/` 中补充错误场景：

- `chat_completions_error_429.json`：非流式额度耗尽。
- `chat_completions_error_stream.txt`：流式 SSE 错误。
- `responses_error_401.json`：认证失败。

这些 fixture 必须用于验证响应字节透明、错误分类和 WebUI issue 数据。

## 8. 性能与安全约束

硬性约束：

- 请求 body prefix 维持小采样，默认 4KB。
- 响应 sample 使用现有上限或显式上限，不能无限增长。
- 错误 message 截断。
- 不写入原始响应体。
- 不写入 prompt 或 output。
- 不在请求转发前调用 WebUI API、数据库查询、quota 查询或外部管理 API。
- SQLite 写入仍然通过异步 writer。

解析顺序要求：

- 非流式：先复制响应给客户端，再使用 sample 解析。
- 流式：先 write + flush，再采样或解析当前 chunk / SSE line。
- 解析失败不得影响下一次 write/flush。
- 所有解析入口必须 recover panic，把 panic 转换为观测字段缺失或 `unknown`，不能传导到代理转发。
- 解析代码不能执行外部调用，不能等待数据库，不能使用无界 goroutine。

## 9. 配置建议

错误解析可以默认开启，但提供关闭开关：

```yaml
capture:
  error_details_enabled: true
  success_metadata_enabled: false
  request_metadata_enabled: false
  max_error_message_bytes: 500
```

如果不想引入新配置文件结构，也可以先使用当前配置默认值，并把开关延后。但实现时必须让解析器足够独立，以便后续加配置。

## 10. 明确不做

本版不做：

- Quota 查询。
- OAuth 登录。
- 额度管理。
- 告警推送。
- 前端构建链。
- 用户内容审计。
- Prompt / response 内容搜索。
- 代理侧限流。
- 模型 fallback。
- 凭证切换。
- 对 LLM 请求链路做任何放行/拒绝判断。

这些功能要么属于后续版本，要么不符合 MeteringProxy 的核心边界。

## 11. 验收标准

功能验收：

- 生产中的额度耗尽错误能在 WebUI 中显示为可读 issue。
- 模型统计不再突兀显示 `unknown`。
- 未配置价格显示为 `未配置价格` / `Unpriced`。
- 成本不完整时显示 `部分估算` / `Partial estimate`。
- 默认错误模块不展示稀疏 bucket 表。
- 正常情况下采集健康低噪声展示。
- 最近 1h 失败状态可见。

质量验收：

- 所有新增解析都有单元测试。
- 代理透明性测试继续通过。
- Golden tests 继续通过。
- `go test ./...` 通过。
- `go build ./...` 通过。
- `node --check` 通过。
- WebUI 桌面和移动端不出现明显溢出。
- 图表不出现横向或纵向滚动条。

风险验收：

- 解析失败不影响代理转发。
- writer 队列满不影响响应。
- 数据库迁移仅增量，旧库可直接启动。
- 新增 API 失败不会拖垮整页。
- WebUI API 仍然 no-store。

## 12. 推荐实施顺序

虽然本版作为一个完整版本交付，实际编码仍建议按以下依赖顺序执行：

1. 数据模型与迁移。
2. 错误解析器与必要的成功响应元数据解析器；请求元数据解析器仅在本版实际消费额外请求字段时加入。
3. proxy 接入解析结果，但保持字节透明。
4. DB 报表查询：overview、issues、模型 fallback。
5. WebUI API。
6. WebUI 信息架构重做。
7. demo/testdata 更新。
8. README 更新。
9. 全量测试与人工 UI 审查。

任何步骤发现会影响透明代理或 LLM 体验时，优先裁剪展示功能，而不是扩大代理链路复杂度。

步骤 7 的 demo/testdata 更新必须覆盖新 UI 能力：

- demo 数据包含 `quota_exhausted`、`rate_limited`、`auth_failed`、`upstream_5xx`。
- demo 数据包含安全的 `error_message` 示例。
- demo 数据包含 `model_returned` 为空但 `model_requested` 有值的失败记录，用于验证 fallback。
- demo 数据包含无法识别模型记录。
- demo 数据包含未配置价格模型，用于验证 `Unpriced` 和 `Partial estimate`。
- demo 数据能让 Recent Issues 默认状态和异常状态都可被人工验证。

步骤 3 是本版最高风险点，必须设置代码审查检查点：

- 确认所有流式路径采样都发生在 `write` + `flush` 之后。
- 确认非流式路径仍然先复制响应给客户端，再解析 sample。
- 确认错误解析不做外部调用、不访问数据库、不阻塞 writer。
- 确认解析 panic 被 recover。
- 确认采样上限、错误消息上限和脱敏逻辑都有测试。
- 确认 golden byte-transparent tests 没有因为解析改动而变化。
- 如果本版实施 `finish_reason` / `stop_reason`，确认流式提取方案已经覆盖非 usage SSE 行。
- 确认 `handleStream` 没有因为错误采样继续膨胀到难以审查；必要时提取辅助函数，例如 `errorPayloadSampler`、`appendSSEPayload`、`finalizeStreamEvent`。
- 确认 `proxy_upstream_error` 在 `writeError()` 路径由 proxy 层直接设置，不依赖 extractor。
