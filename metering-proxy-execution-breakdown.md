# MeteringProxy 实施任务拆分

版本：v1.0  
日期：2026-05-03  
状态：执行中  
对应设计文档：`metering-proxy-evolution-plan.md`

---

## 1. 使用方式

本文件用于把目标设计文档拆成可执行任务。

执行原则：

1. 按工作包顺序推进，不跳过阻塞项。
2. 每个任务完成后都应有明确产出。
3. 每个工作包结束时都要运行对应验收。
4. 任一关键回归出现，停止继续向后推进，先修复。

建议节奏：

- 先完成 `W0`，锁定现有行为。
- 再完成 `W1-W4`，把数据面重构到目标形态。
- 再完成 `W5-W6`，补足控制面与运维能力。
- 最后完成 `W7`，做上线前收口与演练。

---

## 2. 总览

| 工作包 | 目标 | 是否阻塞后续 |
| --- | --- | --- |
| `W0` | 固化现有行为与回归基线 | 是 |
| `W1` | 引入独立事件模型 | 是 |
| `W2` | 引入 profile 与 stream protocol | 是 |
| `W3` | 重构存储边界与 SQLite 字段 | 是 |
| `W4` | 代理主路径切换到新抽象 | 是 |
| `W5` | 补齐 health / metrics / metadata / kill switch / 自检 | 否，但上线前必须完成 |
| `W6` | 完成 pricing alias 与 canonicalization | 否，但上线前必须完成 |
| `W7` | 上线前验证、回滚演练、收口 | 是 |

---

## 3. W0：基线保护

### `W0-T1` 收集真实 fixture

目的：

- 为 golden file 回归提供真实样本。

任务：

- 收集一份 `chat completions` SSE 响应样本。
- 收集一份非流式 JSON 响应样本。
- 对样本做脱敏，确保不含 prompt、response 正文、明文 key。

产出：

- `testdata/fixtures/chat_completions_stream.txt`
- `testdata/fixtures/chat_completions_nonstream.json` 或等价文件

完成标准：

- fixture 可稳定复现现有解析与透传行为。

### `W0-T2` 增加 golden file 测试框架

依赖：

- `W0-T1`

任务：

- 为 SSE 路径增加逐字节回归测试。
- 为非流式路径增加逐字节回归测试。

产出：

- golden tests

完成标准：

- 任意响应字节变化都会导致测试失败。

### `W0-T3` 补充现有行为说明

任务：

- 记录当前流式、非流式、队列溢出、解析失败的预期行为。
- 明确“什么算回归”。

产出：

- 测试注释或 `testdata/README.md`

完成标准：

- 后续重构者可以直接理解 fixture 和测试意图。

### `W0` 验收

- `go test ./...` 通过
- golden tests 通过
- 现有 proxy / writer / extractor 关键测试仍通过

---

## 4. W1：引入独立事件模型

### `W1-T1` 新建 `internal/event`

任务：

- 定义领域事件结构。
- 定义聚合查询结果类型，如 summary、timeseries、model aggregate。
- 在事件结构中显式纳入：
  `capture_outcome`
  `capture_reason`

产出：

- `internal/event/event.go`
- `internal/event/report.go`

完成标准：

- 事件结构不直接依赖 SQLite 细节。

### `W1-T2` 建立存储映射层

依赖：

- `W1-T1`

任务：

- 定义从领域事件到数据库记录的转换。
- 定义从数据库查询结果到领域聚合结果的转换。
- 明确：
  `internal/event.Event` 为领域事件
  `db.UsageRecord` 保留为数据库行映射结构

产出：

- 显式 mapping 代码

完成标准：

- `db.UsageRecord` 不再承担唯一领域模型职责。

### `W1-T3` 修改 writer 入口

依赖：

- `W1-T2`

任务：

- writer 层开始接收领域事件或事件包装结构。

产出：

- writer 输入边界调整

完成标准：

- writer 不依赖上层请求解析细节。

### `W1` 验收

- 事件模型已独立存在
- writer 与 db 的职责边界比当前更清晰
- 现有测试通过

---

## 5. W2：Profile 与 Stream Protocol

### `W2-T1` 新建 profile registry

任务：

- 定义 `EndpointProfile` 结构。
- 实现按 method/path 匹配 profile。

产出：

- `internal/profile/profile.go`
- `internal/profile/registry.go`

完成标准：

- 支持 `chat_completions`、`responses`、`unknown_passthrough`

### `W2-T2` 定义 capture mode 与 metering kind 常量

依赖：

- `W2-T1`

任务：

- 统一 capture mode 与 metering kind 枚举或常量。

产出：

- `internal/profile/types.go` 或等价文件

完成标准：

- 不再散落魔法字符串。

### `W2-T3` 新建 stream protocol 抽象

依赖：

- `W2-T1`

任务：

- 定义 stream protocol 接口或配置结构。
- 首发支持 OpenAI 风格 SSE。

产出：

- `internal/streamproto`

完成标准：

- stream 解析差异不再散落在 proxy 主循环里。

### `W2-T4` 定义 profile bootstrap 机制

依赖：

- `W2-T1`
- `W2-T2`

任务：

- 定义 profile 的初始化入口。
- 明确代码内置注册与配置覆盖的组合方式。
- 在启动时将启用的 profile 装载到 registry。

产出：

- registry bootstrap 逻辑

完成标准：

- profile 来源路径明确
- 启动时可得到首发所需 profile 集合

### `W2-T5` 提取 chat / responses extractor 绑定

依赖：

- `W2-T1`
- `W2-T3`
- `W2-T4`

任务：

- 将 chat/responses 的 non-stream 与 stream extractor 绑定到 profile。

产出：

- profile 到 extractor 的绑定层

完成标准：

- 调用方可通过 profile 获取对应 extractor，而无需手写路径分支。

### `W2-T6` 增加 profile contract tests

依赖：

- `W2-T1`
- `W2-T5`

任务：

- 覆盖 path match、capture mode、metering kind、stream protocol、extractor 选择。

产出：

- `internal/profile/*_test.go`

完成标准：

- profile 行为可测试、可回归。

### `W2` 验收

- profile registry 可用
- stream protocol 抽象可用
- chat/responses/unknown 可通过统一 profile 运行
- profile contract tests 通过

---

## 6. W3：存储边界与 SQLite 扩展

### `W3-T1` 抽出 `EventSink`

任务：

- writer 改为依赖写接口，而非直接依赖具体 DB 结构。

产出：

- `EventSink` 接口

完成标准：

- writer 只关心写事件。

### `W3-T2` 抽出 `ReportStore`

任务：

- webui 改为依赖查询接口，而非直接依赖 SQL 细节。

产出：

- `ReportStore` 接口

完成标准：

- webui 只关心报表结果。

### `W3-T3` 新增数据库字段

依赖：

- `W1-T1`
- `W2-T1`
- `W2-T2`

任务：

- 为表结构增加：
  `endpoint_profile`
  `capture_mode`
  `metering_kind`
  `usage_raw_json`
  `usage_raw_truncated`
  `billable_input`
  `billable_output`
  `billable_total`
  `billable_unit`
  `capture_outcome`
  `capture_reason`

产出：

- migration

完成标准：

- 旧库可平滑迁移
- 新字段可读写

### `W3-T4` 更新 config schema 与示例配置

任务：

- 同步更新 `config.go`
- 同步更新示例 `config.yaml`
- 同步更新配置校验逻辑

适用范围包括：

- `metering_enabled`
- trusted proxy 相关配置
- profile 启停或覆盖相关配置
- 其他新增 runtime 配置

产出：

- 配置结构更新
- 示例配置更新

完成标准：

- 新配置项不会只存在于代码或只存在于示例文件的一侧

### `W3-T5` 限制 `usage_raw_json` 存储策略

依赖：

- `W3-T3`

任务：

- 限制最大长度
- 增加 truncated 标记
- 默认查询不返回该字段

产出：

- 存储与查询策略落地

完成标准：

- 大字段不拖慢常规列表查询

### `W3-T6` 存储层测试

依赖：

- `W3-T1`
- `W3-T2`
- `W3-T5`

任务：

- 增加 migration、读写、兼容性测试

完成标准：

- 新字段与旧字段兼容

### `W3` 验收

- `EventSink` / `ReportStore` 可用
- SQLite migration 可用
- 新字段兼容旧库
- 存储测试通过

---

## 7. W4：代理主路径重构

### `W4-T1` 改造 proxy 主循环

依赖：

- `W2-T4`
- `W3-T1`

任务：

- 让 proxy 主循环以 profile 为驱动。
- 在开始实现前，先将新的 `ServeHTTP` 生命周期伪代码固化到设计文档。

完成标准：

- 主循环不再依赖散落的路径判断。
- 生命周期步骤与设计文档一致。

### `W4-T2` 接入统一事件生成

依赖：

- `W1-T1`
- `W4-T1`

任务：

- stream 与 non-stream 路径都生成统一事件结构。

完成标准：

- 上层统一写入事件。

### `W4-T3` 引入 `capture_outcome` / `capture_reason`

依赖：

- `W4-T2`

任务：

- 明确记录：
  captured
  skipped
  failed

完成标准：

- 没采到 usage 时原因可解释。

### `W4-T4` 保持非流式边读边写语义

依赖：

- `W4-T1`

任务：

- 确保重构后 non-stream 仍为边读边写，不回退成全量缓冲。

完成标准：

- 相关回归测试通过。

### `W4-T5` 保持流式字节透明

依赖：

- `W4-T1`

任务：

- 保证 stream 重构后 golden test 与现有字节透明测试通过。

完成标准：

- 不改写原始流字节。

### `W4` 验收

- proxy 主循环切换到新抽象
- unified event 已落地
- capture outcome / reason 可用
- stream / non-stream 回归测试通过

---

## 8. W5：可观测性与控制面

### `W5-T1` 增加 `/metrics`

任务：

- 暴露 Prometheus 指标。

至少包括：

- `queue_depth`
- `dropped_events_total`
- `parse_errors_total`
- `db_write_errors_total`
- `sse_line_skips_total`
- `request_latency_ms`
- `request_ttfb_ms`

### `W5-T2` 扩展 `/api/health`

任务：

- 增加 metering enabled 状态
- 暴露最新健康快照

### `W5-T3` 增加 `/api/metadata`

任务：

- 返回 profile 列表、capture mode、metering kind、range、bucket

### `W5-T4` 前端改为 metadata 驱动

依赖：

- `W5-T3`

任务：

- 去掉 endpoint 下拉框硬编码
- 改由 metadata 动态渲染

### `W5-T5` 增加 kill switch

任务：

- 增加 `metering_enabled`
- 明确关闭时的执行语义：
  继续透明转发，但跳过请求体前缀读取、usage 提取、事件构建与 writer 入队

### `W5-T6` 增加启动自检

任务：

- 检查 salt
- 检查 SQLite 写权限
- 检查 pricing 解析

### `W5` 验收

- `/metrics` 可用
- `/api/health` 可用
- `/api/metadata` 可用
- kill switch 可用
- 自检逻辑可用

---

## 9. W6：价格与别名

### `W6-T1` 定义 canonicalization 规则并扩展 pricing schema

任务：

- 为模型价格增加显式 alias 支持
- 定义 canonicalization 规则
- 明确 canonicalization 只用于 pricing lookup，不改写已存储模型名

### `W6-T2` 实现定价匹配链路

依赖：

- `W6-T1`

任务：

- 实现：
  exact match -> explicit alias -> canonical form -> unknown
- 支持 provider-specific canonicalization
- 不允许模糊前缀匹配
- 不允许隐式版本后缀截断

### `W6-T3` 增加 pricing tests

任务：

- 覆盖 exact match
- alias match
- unknown model
- 不允许误匹配

### `W6` 验收

- alias 生效
- unknown 行为明确
- 不存在静默误计费路径

---

## 10. W7：上线前收口

### `W7-T1` 完整测试回归

任务：

- 跑 `go test ./...`
- 跑 `go vet ./...`
- 跑 golden tests

### `W7-T2` 本地或预发布验证

任务：

- 验证 chat completions stream
- 验证 chat completions non-stream
- 验证 responses stream
- 验证 responses non-stream

### `W7-T3` kill switch 演练

任务：

- 开启
- 关闭
- 确认关闭后仍能转发但不计量

### `W7-T4` Caddy 回滚演练

任务：

- 演练从 MeteringProxy 回切到 CLIProxyAPI 直连

### `W7-T5` 发布前检查记录

任务：

- 记录验收结果
- 记录回滚路径
- 记录未决问题

### `W7` 验收

- 所有测试通过
- 所有演练通过
- 无阻塞级未决问题

---

## 11. 建议执行顺序

严格顺序：

1. `W0`
2. `W1`
3. `W2`
4. `W3`
5. `W4`
6. `W5`
7. `W6`
8. `W7`

可有限并行的部分：

- `W5-T1` 与 `W5-T2` 可并行
- `W5-T3` 与 `W5-T6` 可并行
- `W6` 可在 `W5` 后半段并行推进

不建议并行的部分：

- `W1-W4`

原因：

- 这些工作包都直接作用于数据面和结构边界，耦合高，强并行容易制造回归。

---

## 12. 完成定义

项目可以进入正式上线阶段，当且仅当：

- `W0-W7` 全部完成
- 对应验收全部通过
- 满足主设计文档中的上线验收标准

如果任务实现与设计文档冲突：

1. 先暂停推进
2. 更新设计文档或修正实现
3. 再继续执行

不允许长期存在“实现已经变了，但文档还没更新”的状态。
