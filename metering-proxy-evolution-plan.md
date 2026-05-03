# MeteringProxy 目标设计与实施文档

版本：v1.0  
日期：2026-05-03  
状态：执行中  
用途：本文件作为 MeteringProxy 上线前的目标设计、实施计划和验收标准。项目应推进到本文档定义的目标状态后，再进入正式上线阶段。

配套任务拆分文档：`metering-proxy-execution-breakdown.md`

---

## 1. 文档定位

这不是讨论方向的规划稿，而是执行文档。

本文档回答三类问题：

1. 最终要做成什么。
2. 代码结构和运行时行为应如何设计。
3. 上线前必须完成哪些工作，完成到什么程度。

本文档优先级高于零散讨论。后续如果设计发生变化，应直接更新本文档，而不是让实现与文档长期分叉。

---

## 2. 最终目标

MeteringProxy 的最终目标是：

```text
在尽最大可能不影响 LLM 体验的前提下，
为关键 LLM 调用路径提供稳定、可维护、可回滚的透明计量能力。
```

更具体地说：

- 对外保持透明反向代理行为。
- 对内生成统一计量事件。
- 异步写入 SQLite。
- 提供只读查询 API 和 WebUI。
- 与 CLIProxyAPI 的内部统计实现解耦。
- 为未来扩展更多 endpoint 留好结构空间。

---

## 3. 上线版本的边界

### 3.1 首发代理范围

上线版本只代理以下路径：

- `POST /v1/chat/completions`
- `POST /v1/responses`

继续直连，不进入计量代理范围：

- `/v1/models`
- `/v0/management*`
- `/management.html`
- 所有暂未纳入设计的其他路径

### 3.2 首发必须支持的能力

- 流式与非流式透明转发
- usage 提取
- 请求数、状态码、延迟、TTFB、token、成本估算
- API key / IP 哈希匿名化
- 异步队列写入
- 只读 WebUI
- `/api/health`
- `/metrics`
- kill switch
- 快速回滚

### 3.3 首发明确不做

- 模型路由
- fallback
- retry
- cache
- prompt 注入
- 配置后台
- 用户管理
- 多租户权限系统
- 采样率
- PostgreSQL / ClickHouse 支持

---

## 4. 核心不变量

这些约束是设计底线，不允许为了方便实现而破坏。

1. 不记录 prompt 原文。
2. 不记录 response 原文。
3. 不记录明文 API key。
4. 不记录明文客户端 IP。
5. 不因 usage 解析失败而影响请求返回。
6. 不因数据库失败而影响请求返回。
7. 不因队列满而阻塞请求。
8. 不允许无意改写响应字节。
9. 需要能一键回滚到 `Caddy -> CLIProxyAPI` 直连。
10. 非流式路径必须继续保持边读边写语义，不允许回退为“读完整个响应再返回”。

---

## 5. 核心架构决策

### 5.1 总体拓扑

```text
Client
  -> Caddy
  -> MeteringProxy
  -> CLIProxyAPI
  -> upstream provider
```

### 5.2 数据面与控制面分离

MeteringProxy 内部逻辑分为两类：

- 数据面：代理转发、usage 提取、事件生成、异步入队
- 控制面：查询、聚合、价格估算、健康分析、WebUI、指标暴露

规则：

- 数据面只做最小必要工作
- 控制面不得反向影响数据面时延与稳定性

### 5.3 事件模型作为长期中心

长期演进中，系统应围绕统一事件模型，而不是围绕具体路径分支。

但执行策略是“逐步扶正”，不是“首发前整套推翻重写”：

- 第一阶段保留现有显式代理结构
- 在此基础上引入独立事件模型
- 再让 writer、db、webui 逐步围绕该模型稳定下来

事件模型与数据库结构的关系必须明确：

- `internal/event.Event` 是领域事件
- `db.UsageRecord` 保留为数据库行映射结构
- mapping 层负责两者之间的转换

这条边界的目标是：

- 领域模型不被 SQL 表结构绑死
- 数据库迁移不反向污染上层领域对象
- 在最小改动路径下完成事件模型引入

### 5.4 Profile 化而不是路径硬编码

首发只支持两个 endpoint，但内部设计不能继续依赖零散的路径字符串判断。

系统需要引入 endpoint profile registry，用 profile 管理：

- 路径匹配
- capture 行为
- metering 类型
- stream 协议
- usage extractor

### 5.5 小接口而不是重型抽象

不采用“一个超级 Store 接口统管所有读写”的方式。  
中期目标是小接口分离：

- `EventSink`
- `ReportStore`

这样可以控制抽象成本，避免为了“架构完整性”把项目做重。

---

## 6. 目标代码结构

首发完成后的目标结构建议如下：

```text
main.go
internal/
  config/
  event/
  profile/
  streamproto/
  extractor/
  pricing/
  proxy/
  writer/
  store/
    sqlite/
  webui/
  metrics/
```

说明：

- `event`：领域事件与聚合模型
- `profile`：endpoint profile registry 和匹配逻辑
- `streamproto`：流协议抽象
- `extractor`：usage 提取逻辑
- `store/sqlite`：SQLite 实现
- `metrics`：Prometheus 指标暴露与内部计数整理

第一阶段不要求目录一次性拆到极致，但职责必须开始按这个方向靠拢。

---

## 7. 目标领域模型

### 7.1 统一事件模型

建议引入独立领域事件类型，例如：

```go
type Event struct {
    ID string
    Timestamp time.Time

    EndpointProfile string
    CaptureMode     string
    MeteringKind    string

    Method    string
    Path      string
    Status    int
    Stream    bool
    LatencyMs int64
    TTFBMs    int64

    APIKeyHash   string
    ClientIPHash string

    ModelRequested string
    ModelReturned  string

    InputTokens     int64
    OutputTokens    int64
    ReasoningTokens int64
    CachedTokens    int64
    TotalTokens     int64

    BillableInput  float64
    BillableOutput float64
    BillableTotal  float64
    BillableUnit   string

    UsageRawJSON      string
    UsageRawTruncated bool

    CaptureOutcome string
    CaptureReason  string

    RequestBytes  int64
    ResponseBytes int64
    Error         string
}
```

### 7.2 允许的 capture mode

- `passthrough`
- `request_only`
- `usage_metered`

### 7.3 允许的 metering kind

- `none`
- `llm_tokens`
- `embedding_tokens`
- `audio_seconds`
- `image_count`
- `request_only`
- `unknown`

### 7.4 允许的 capture outcome

- `captured`
- `skipped`
- `failed`

### 7.5 capture reason 最小集合

- `capture_disabled`
- `profile_passthrough`
- `request_only_profile`
- `usage_not_present`
- `sample_limit_exceeded`
- `stream_protocol_unsupported`
- `parse_error`
- `writer_queue_full`

目标不是一开始把所有 reason 穷举完，而是保证：

- “为什么没采到”是可解释的
- UI 和运维能区分“正常跳过”与“异常失败”

---

## 8. Endpoint Profile 设计

### 8.1 Profile 最小字段

每个 profile 至少包含：

- `name`
- `method`
- `path matcher`
- `capture_mode`
- `metering_kind`
- `stream_protocol`
- `request model extractor`
- `non-stream usage extractor`
- `stream usage extractor`
- `pricing key strategy`

### 8.2 首发 profile

- `chat_completions`
- `responses`
- `unknown_passthrough`

### 8.3 首发后可扩展 profile

- `embeddings`
- `audio_transcriptions`
- `image_generation`

### 8.4 Profile 来源策略

首发不采用“完全配置驱动”的 profile 定义方式。

建议：

- profile 元数据主要由代码内置
- 配置文件只负责启停、路径覆盖、别名等轻量控制

原因：

- endpoint 协议语义属于产品逻辑
- 提取器逻辑本质上是 Go 代码，不适合伪装成纯配置
- 过早把 profile 全部 YAML 化只会增加维护复杂度

但仍需要定义 profile 初始化机制：

- profile 元数据由代码内置注册
- 配置只负责启停、路径覆盖或轻量运行时控制
- 启动时由统一 bootstrap 逻辑装载到 registry

---

## 9. Stream Protocol 设计

### 9.1 目标

把流协议差异隔离出代理主循环，使其只影响 usage 提取，而不影响透明转发。

### 9.2 协议层至少需要描述

- 是否使用 SSE
- 如何识别事件边界
- 是否存在完成标记，例如 `[DONE]`
- 是否需要处理 `event:` 字段
- 每行或每帧的大小上限

### 9.3 首发支持

首发只需要支持 OpenAI 风格 SSE，但实现上不能把这条路径写死成不可扩展的特殊分支。

### 9.4 重要原则

如果协议识别失败：

- 仍应尽量保持字节透传
- 允许 usage 提取失败
- 不允许客户端侧行为被破坏

---

## 10. 代理执行路径设计

### 10.1 公共路径

每次请求处理分为以下步骤：

1. 匹配 endpoint profile
2. 读取必要请求元数据
3. 透明转发到上游
4. 按 profile 进行 usage 提取
5. 生成统一事件
6. 异步写入队列

建议在实现前明确如下伪代码语义：

```go
func ServeHTTP(w http.ResponseWriter, r *http.Request) {
    profile := registry.Match(r.Method, r.URL.Path)

    if !meteringEnabled {
        forwardTransparent(w, r)
        return
    }

    if profile.CaptureMode == Passthrough {
        forwardTransparent(w, r)
        return
    }

    reqMeta := readRequestMetadata(r, profile)
    respMeta, usageResult := forwardAndCapture(w, r, profile, reqMeta)
    event := buildEvent(profile, reqMeta, respMeta, usageResult)
    writer.Enqueue(event)
}
```

这里的关键不是函数名，而是责任分层：

- 匹配 profile
- 判断是否纯透传
- 提取最小请求元数据
- 转发并 best-effort 捕获 usage
- 构建事件
- 异步写入

### 10.2 请求体读取策略

首发保留当前“小前缀读取 + 透传重放”的策略。

原因：

- 能获取 `model_requested`
- 能识别请求侧 `stream` hint
- 在上游失败或响应缺字段时仍保留请求侧上下文

因此当前请求体前缀读取逻辑不作为首发前重构目标删除。

### 10.3 非流式响应策略

非流式路径继续采用：

- 边读边写返回客户端
- 同时缓冲有限样本用于 usage 提取

首发保留 bounded sample 策略，不引入复杂的 token-level 流式 JSON 解析。

### 10.4 流式响应策略

流式路径必须保证：

- 原始字节尽快 flush
- SSE usage 解析为 best-effort
- 长行、分块、协议异常不阻断透传

---

## 11. 存储设计

### 11.1 存储边界

中期目标是：

- writer 依赖 `EventSink`
- webui 依赖 `ReportStore`
- SQLite 只是一个实现，不是整个系统的领域边界

### 11.2 SQLite 仍为首发存储

首发继续使用 SQLite，要求：

- 单写连接
- 可独立读连接
- WAL 模式
- 迁移兼容旧库

### 11.3 建议新增字段

在现有表结构基础上，逐步补齐以下字段：

- `endpoint_profile`
- `capture_mode`
- `metering_kind`
- `usage_raw_json`
- `usage_raw_truncated`
- `billable_input`
- `billable_output`
- `billable_total`
- `billable_unit`
- `capture_outcome`
- `capture_reason`

### 11.4 `usage_raw_json` 约束

- 仅保存最小 usage 子集
- 上限建议 4KB
- 默认不进入明细列表查询
- 仅调试/导出接口按需返回

### 11.5 首发不做

- PostgreSQL 支持
- 多存储后端插件化
- 复杂分片

---

## 12. 定价设计

### 12.1 首发要求

价格系统必须支持：

- 精确模型名匹配
- 显式 alias
- provider-specific canonicalization

匹配链路必须明确为：

```text
exact match -> explicit alias -> canonical form -> unknown
```

其中 canonicalization 只用于 pricing lookup，不改变持久化存储中的原始 `model_returned`。

### 12.2 明确约束

不允许：

- 隐式版本后缀截断
- 自动前缀回退
- 模糊匹配

alias 必须显式写在 `pricing.yaml` 中。

原则：

```text
宁可 unknown，不可静默误计费。
```

---

## 13. 可观测性与运维设计

### 13.1 `/api/health`

用于快速查看当前进程状态，至少包含：

- queue depth
- dropped events
- parse errors
- db write errors
- metering enabled state
- latest health snapshot

### 13.2 `/metrics`

用于 Prometheus 采集，区分两类指标。

实时指标：

- `queue_depth`
- `dropped_events_total`
- `parse_errors_total`
- `db_write_errors_total`
- `sse_line_skips_total`

分布指标：

- `request_latency_ms`
- `request_ttfb_ms`

### 13.3 kill switch

增加配置：

```yaml
metering_enabled: true
```

关闭时：

- 继续透明转发
- 跳过请求体前缀读取
- 跳过 response usage 提取
- 跳过事件构建
- 跳过 writer 入队
- 通过 health / metrics 明确暴露 `capture_disabled`

也就是说，kill switch 的目标语义是：

```text
匹配路径后仍继续作为透明代理工作，
但完全绕过所有计量相关代码路径。
```

### 13.4 启动自检

启动时至少检查：

- salt 文件存在且可读
- SQLite 路径可写
- pricing 文件可解析

这些失败时应 fail-fast。

上游不可达更适合进入 health warning，而不是阻止进程启动。

---

## 14. WebUI 与 API 设计

### 14.1 首发 API 集合

- `GET /metering/api/summary`
- `GET /metering/api/timeseries`
- `GET /metering/api/models`
- `GET /metering/api/keys`
- `GET /metering/api/requests`
- `GET /metering/api/errors`
- `GET /metering/api/health`
- `GET /metering/api/metadata`

### 14.2 Metadata API 目标

前端不再硬编码以下内容：

- endpoint 筛选项
- 支持的 range
- 支持的 bucket
- profile 友好名称
- metering kind 标签

### 14.3 前端结构策略

首发前不强制拆成复杂前端工程。  
允许继续使用 embed 的静态 HTML/JS，但必须开始由 metadata API 驱动动态配置。

如果在实施 `W5-T4` 时单文件前端修改成本明显升高，可以在不引入构建系统的前提下做物理拆分，但这不是前置约束。

---

## 15. 测试与回归设计

### 15.1 当前已有覆盖应保留

当前项目已经覆盖以下关键路径：

- SSE 字节透明转发
- SSE 跨 chunk usage 解析
- Responses SSE 解析
- 队列满时丢弃
- 非流式大响应不截断
- 解析错误计数逻辑

这些测试不能因为重构而丢失。

### 15.2 Phase 0 必补测试

- profile contract tests
- metadata API tests
- pricing alias tests
- kill switch tests
- capture reason tests
- `usage_raw_json` 截断测试

### 15.3 Golden file 字节回归

必须增加：

- 一份真实 chat completions SSE fixture
- 一份真实 non-stream fixture

代理前后逐字节比对。  
任意字节差异都视为回归。

这是上线前的必备保护。

### 15.4 上线阻断条件

以下任一失败，禁止上线：

- 字节级 golden test 失败
- chat/responses 行为回归
- 非流式路径失去边读边写语义
- kill switch 不工作
- pricing alias 行为不确定

---

## 16. 实施计划

项目按以下工作包执行，必须按顺序推进。

### W0：基线保护

目标：

- 固化现有行为
- 补齐 golden tests
- 明确回归基线

输出：

- fixture
- golden test
- 行为基线说明

完成标准：

- 当前实现的字节透传行为已被测试锁定

### W1：引入事件模型

目标：

- 新增独立 `event` 包
- 让代理、writer、db 开始围绕领域事件通信

输出：

- `internal/event`
- 事件结构定义

完成标准：

- 不再把数据库行结构当作唯一领域对象

### W2：Profile 与 Stream Protocol

目标：

- 引入 profile registry
- 引入 `capture_mode`
- 引入 `metering_kind`
- 引入 stream protocol 抽象

输出：

- `internal/profile`
- `internal/streamproto`

完成标准：

- chat/responses/unknown 能通过统一 profile 路径运行

### W3：存储边界与 SQLite 扩展

目标：

- 明确 `EventSink` / `ReportStore`
- 补数据库字段
- 保持旧数据兼容

输出：

- `internal/store/sqlite`
- schema migration

完成标准：

- 写入与查询边界清晰
- 旧库可迁移

### W4：代理重构

目标：

- 将主代理逻辑切到 profile 驱动
- 保持现有转发行为不变

输出：

- 重构后的 `internal/proxy`

完成标准：

- 现有功能保持一致
- 行为测试全部通过

### W5：可观测性与控制面

目标：

- 增加 `/metrics`
- 增加 `/api/metadata`
- 增加 kill switch
- 增加启动自检

输出：

- metrics
- metadata
- health 扩展

完成标准：

- 运维面具备上线所需观测能力

### W6：价格与别名

目标：

- 支持显式 alias
- 固化 canonicalization 边界

输出：

- pricing schema 更新
- pricing tests

完成标准：

- 不存在静默误匹配

### W7：上线前收口

目标：

- 跑完整测试
- 验证回滚路径
- 验证 kill switch
- 验证 Caddy 配置

输出：

- 上线检查记录

完成标准：

- 满足第 17 节全部验收标准

---

## 17. 上线验收标准

只有同时满足以下条件，项目才可上线：

1. 只代理 `/v1/chat/completions` 和 `/v1/responses`
2. profile 机制已落地，主代理不再靠零散路径判断
3. 独立事件模型已引入
4. `capture_mode` / `metering_kind` 已落地
5. stream protocol 抽象已落地
6. SQLite 扩展字段与迁移已完成
7. `/api/health`、`/metrics`、`/api/metadata` 可用
8. kill switch 可用
9. pricing alias 可用且无隐式模糊匹配
10. golden file 字节回归测试通过
11. `go test ./...` 通过
12. `go vet ./...` 通过
13. 小流量本地或预发布环境验证通过
14. Caddy 快速回滚路径已演练

---

## 18. 最终建议

项目应按本文档执行，而不是继续追加零散功能。

最重要的三条执行原则是：

1. 首发范围克制，只守住两个关键 endpoint。
2. 数据面保持显式、简单、透明，不为抽象而抽象。
3. 逐步把系统扶正到“统一事件模型 + profile 驱动 + 可观测 + 可回滚”的状态。

MeteringProxy 的定位始终应是：

```text
一个极简、透明、可回滚、可扩展的计量代理，
而不是一个重型全功能 LLM Gateway。
```

如果后续实现与本文档冲突，应优先修正文档或修正实现，而不是让两者长期偏离。
