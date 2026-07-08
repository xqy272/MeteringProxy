# MeteringProxy 性能优先设计（修订版）

状态：参考材料，非主设计入口
核心原则：透明代理优先（invariant #1/#3），观测能力旁路增强，性能优化 benchmark 驱动。

> 本文档是 `next-iteration-design.md` 的性能专项补充，不替代主方案。
> 修订说明：初版提出全量 Tee/side observer 重构和默认剥离 Accept-Encoding，
> 经 review 后判断过度。本版收敛为 benchmark 驱动的针对性修复。

---

## 1. 核心判断（修正后）

当前架构不是"完全串行"。响应路径已经在 `copyAndFlush` 中边读边转发边 flush；SSE 也是先写给客户端再做解析。真正的串行瓶颈只有一处：

**metered 路径在 RoundTrip 之前读取 `bodyPrefix`。**

即使最终不需要完整解析（比如上游返回错误），请求体也已经被部分读取。`forwardRequestOnly` 快路径已经为 request-only profile 绕过了这个问题，但 usage-metered profile 仍然存在。

因此下一阶段的性能工作应该是：
1. 先建立 benchmark 基线，找到真实瓶颈
2. 针对性修复请求体前置读取
3. 补充 transport 层优化（连接预热、HTTP/2、连接池可观测）
4. 不做全量架构重写

---

## 2. Phase 0：Benchmark 基线（前置，1 周）

在任何重构之前，先建立可重复的基准测试。

### 2.1 基准测试场景

```text
benchmark-nonstream-small:    非流式，请求体 4KB，响应体 8KB
benchmark-nonstream-large:    非流式，请求体 1MB（audio），响应体 1MB
benchmark-sse-small:          SSE，100 chunks，每 chunk 200 bytes
benchmark-sse-large:          SSE，10000 chunks，每 chunk 1KB
benchmark-request-only-large: request-only，请求体 10MB，响应体 10MB
benchmark-concurrent:         50 并发非流式请求
```

### 2.2 测量指标

- 代理 overhead（请求进入到 upstream RoundTrip 开始）：p50/p95/p99
- 端到端 TTFB
- 热路径内存分配（allocs/op）
- SSE 单 chunk 转发延迟
- observer 处理延迟
- events dropped 计数

### 2.3 交付物

```text
internal/proxy/benchmark_test.go
docs/performance-baseline.md  （记录当前基线数值）
```

### 2.4 验收

- benchmark 可重复运行，结果稳定（波动 < 10%）
- 基线数值记录在案，作为后续优化的对比基准
- 如果 benchmark 发现请求体前置读取确实是显著瓶颈，进入 Phase 1；否则只做 transport 优化

---

## 3. Phase 1：请求体前置读取修复（条件性，1-2 周）

仅在 Phase 0 benchmark 确认请求体前置读取是瓶颈时执行。

### 3.1 当前问题

metered 路径的 `ServeHTTP` 在 RoundTrip 之前执行 bounded body prefix scan：

```text
当前流程：
读 bodyPrefix (InitialBytes, 可能扩展到 MaxBytes)
  -> 解析 model
  -> 构造 upstream request（body 已被部分消费）
  -> RoundTrip
```

这导致：
- RoundTrip 被延迟，即使上游能立即响应
- 大请求体场景下内存压力增加
- 错误响应场景下做了无用功

### 3.2 评估方案：TeeReader 局部引入

**注意：io.TeeReader 不是零成本。** 它的 `Write` 到 side buffer 在 `Read` 调用中同步执行，仍在热路径上。它的价值是避免"RoundTrip 前读 body"，而不是把观测完全移出热路径。

评估方向：

```go
// 请求体用 TeeReader 包装：转发的同时写入 bounded side buffer
// side buffer 写满后静默丢弃，不影响转发
var reqTee *boundedTeeWriter
if r.Body != nil {
    reqTee = newBoundedTeeWriter(pool.getSampleBuf())
    r.Body = teeBody(r.Body, reqTee)
}

upstreamReq := p.cloneRequest(r)
// RoundTrip 消费 body 时，TeeReader 自动分流到 side buffer
// 不在 RoundTrip 前主动读 body
resp, err := p.transport.RoundTrip(upstreamReq)

// model 提取从 side buffer 异步进行
if reqTee != nil {
    p.observer.enqueueModelExtraction(reqTee.sample())
}
```

关键约束：
- side buffer 写满后 `Write` 变成 no-op，不阻塞转发
- side buffer 写入是内存操作，开销远小于网络 IO
- model 提取移到 observer，不阻塞响应转发

### 3.3 验收

- benchmark 证明 RoundTrip 前的 overhead 显著降低
- 现有 golden tests 全部通过（转发字节不变）
- 现有计量测试全部通过（model 提取结果不变）
- `go test -race ./...` 通过
- request-only 快路径行为不变（已经不读 body prefix）

---

## 4. Phase 2：Transport 层优化（1 周）

这部分不依赖 Phase 1，可以并行。

### 4.1 连接预热（opt-in，默认关闭）

预热会主动请求 CPA `/v1/models`，带来启动日志和副作用。对个人部署，
第一个真实 LLM 请求建立 TCP 连接的代价可以接受，不值得为此主动打 CPA。

因此默认不预热，仅作为 opt-in：

```yaml
proxy_transport:
  warmup_on_start: false   # 默认关闭；开启时异步请求 /v1/models 预热连接
```

开启时：
- 异步执行，不阻塞启动
- 失败只记 warn 日志，不影响启动
- 不在 LLM 请求路径上等待预热完成

不开启时，依靠 transport 配置（MaxIdleConns、MaxIdleConnsPerHost）让连接池
在正常流量下自然填充。第一个请求多付一次 TCP 握手，后续请求复用。

### 4.2 HTTP/2 配置

```go
transport := &http.Transport{
    ForceAttemptHTTP2:     true,
    MaxIdleConns:          128,
    MaxIdleConnsPerHost:   16,
    IdleConnTimeout:       90 * time.Second,
    TLSHandshakeTimeout:   10 * time.Second,
    ResponseHeaderTimeout: 0,  // 流式响应不设超时
    // 不设 DisableCompression：保持 header 透明（invariant #3）
}
```

### 4.3 Transport 配置化

```yaml
proxy_transport:
  max_idle_conns: 128
  max_idle_conns_per_host: 16
  idle_conn_timeout: "90s"
  tls_handshake_timeout: "10s"
  response_header_timeout: "0s"
  expect_continue_timeout: "1s"
  force_http2: true
```

默认值保持当前行为，配置上限 clamp 防止误配置。

### 4.4 连接池可观测

通过 atomic 计数器收集，不进入热路径锁：

```text
metering_proxy_transport_conns_active
metering_proxy_transport_conns_idle
metering_proxy_transport_conns_created_total
metering_proxy_transport_conns_closed_total
metering_proxy_transport_dns_errors_total
metering_proxy_transport_dial_errors_total
```

### 4.5 验收

- 首次请求 TTFB 不因 TCP 握手而显著增加
- HTTP/2 多路复用生效（观察 conns_per_host 指标）
- 连接池指标可在 Prometheus 端点查询
- 现有 golden tests 不受影响

---

## 5. Phase 3：缓冲区池化（条件性，benchmark 后决定）

### 5.1 判断前提

Go 的分配器本身很强，32KB buffer 的分配在低并发下可能不是瓶颈。
是否引入 sync.Pool 应由 Phase 0 benchmark 的 allocs/op 数据决定，
不要凭直觉优化。

### 5.2 sync.Pool 引入（仅在 benchmark 确认 GC 压力显著时）

当前 `copyAndFlush` 每次请求分配 32KB buffer：

```go
func copyAndFlush(w http.ResponseWriter, r io.Reader) {
    buf := make([]byte, 32*1024)  // 每次分配
    // ...
}
```

改为池化：

```go
var copyBufPool = sync.Pool{
    New: func() any { return make([]byte, 32*1024) },
}

func copyAndFlush(w http.ResponseWriter, r io.Reader) {
    buf := copyBufPool.Get().([]byte)
    defer copyBufPool.Put(buf)
    // ...
}
```

### 5.2 其他池化点

- sample buffer（bodyPrefix 读取用的 buffer）
- rawEvent 对象（如果引入 observer）
- SSE 解析临时 buffer

### 5.3 验收

- benchmark 中 allocs/op 显著下降
- GC 压力在高并发场景下降低
- 现有测试全部通过

---

## 6. 关于 Accept-Encoding（不修改）

保持现有决策不变：

- **不剥离** `Accept-Encoding`（invariant #3 - header transparency）
- 压缩 SSE 无法计量时，标记 `capture_reason=compressed_stream_not_metered`
- 提供 opt-in 配置 `stream_metering.prefer_uncompressed_sse: false`（默认 false）
- 如果用户设为 true，明确记录这不再是完全 header-transparent 模式

这是 CLAUDE.md 已经记录的 intentional tradeoff，不改变。

---

## 7. 关于观测 API（不新增重量级 API）

不新增 gateway capability view、model assets view、issues API 等重量级查询 API 到性能工作中。这些属于主方案 `next-iteration-design.md` 的功能能力范畴，应在热路径保护完成后按主方案的顺序推进。

性能工作只新增：

```text
GET /healthz    # 存活检查
GET /readyz     # 就绪检查（DB、salt、pricing 可用）
```

以及 Prometheus metrics 端点（如果还没有）。

---

## 8. 实施顺序（benchmark 数据修订后）

Phase 0 已完成（提交 `249d98f`），基线数据见 `docs/performance-baseline.md`。
数据推翻了"请求体前置读取是主要瓶颈"的直觉判断：

- 默认 4KB prefix 仅 12 μs pre-RT，对 LLM 请求可忽略。
- 真实瓶颈在响应侧：非流式大响应 20.1 MB/op，SSE 大响应 44.0 MB/op + 110K allocs/op。

因此实施顺序修订为：

```text
Phase 0: Benchmark 基线              ← 已完成
Phase 1: pprof 定位响应侧分配来源    ← 下一步，不猜测，用数据定位
Phase 2: SSE 逐 chunk 解析分配优化   ← pprof 确认后，优化 top 分配点
Phase 3: 非流式响应采样缓冲优化      ← pprof 确认后，优化 top 分配点
Phase 4: Transport 层优化            ← HTTP/2、连接池配置、指标；预热 opt-in
Phase 5: ExtendedModelScan 优化      ← 低优先级，默认禁用
```

明确不做：
- 请求体 Tee 重构（数据不支持，12 μs 不值得引入复杂度）
- 默认剥离 Accept-Encoding（invariant #3）

Phase 1-3 的优化必须用 benchmark 对比验证，每次只改 top 分配点。
注意区分代理逻辑分配和 benchmark harness 分配（pprof 可解决）。

然后回到主方案 `next-iteration-design.md` 的 Phase 1（透明网关能力视图、LLM 体验指标等）。

---

## 9. 成功标准

性能基线（Phase 0 已记录，见 `docs/performance-baseline.md`）：

| 指标 | 基线值 | 目标 |
|------|--------|------|
| SSE large allocs/op | 110,098 | Phase 2 后显著下降（目标 <10K） |
| SSE large B/op | 44.0 MB | Phase 2 后显著下降 |
| nonstream large B/op | 20.1 MB | Phase 3 后下降（目标 <5MB，排除 harness） |
| request-only large B/op | 4 KB | 保持不变（金标准） |
| request-only large allocs/op | 53 | 保持不变 |
| 默认 4KB prefix pre-RT | 12 μs | 不劣化 |
| 首次请求 TTFB | 未测量 | Phase 4 后改善 |

功能基线：
- 所有现有 golden tests 通过
- 所有现有计量提取结果不变
- header 透明性不变（不剥离 Accept-Encoding）
- 转发字节、状态码、SSE flush 行为不变

---

## 10. 演进历程

| 阶段 | 判断 | 结果 |
|------|------|------|
| 初版 | 全量 Tee/side observer 重构，默认剥离 Accept-Encoding | 过度，被 review 否决 |
| 修订版 | benchmark 驱动，条件性局部修复，保持 header 透明 | 方向正确 |
| benchmark 后 | 数据推翻"请求体前置读取是主要瓶颈"，真实瓶颈在响应侧 | 优先级转向 SSE/采样分配优化 |

最终立场：
- 不做请求体 Tee 重构（默认 4KB prefix 仅 12 μs，不值得）
- 不剥离 Accept-Encoding（invariant #3）
- 先 pprof 定位响应侧 top 分配点，再针对性优化
- 每次优化用 benchmark 对比验证

---

## 11. 定位

本文档是性能专项参考，不替代主方案 `next-iteration-design.md`。
主设计入口仍是 `next-iteration-design.md`，本文档仅在其 Phase 0
（热路径保护）需要展开性能细节时作为参考。

下一步最合理的是补 `internal/proxy/benchmark_test.go`，用数据驱动
后续优化决策，而不是继续争论架构。
