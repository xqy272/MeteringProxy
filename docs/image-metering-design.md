# 多模态计量设计与实施方案（图片 P0）

状态：设计方案与落地记录  
日期：2026-05-21  
核心要求：图片相关数据必须能计量；同时修复现有短板，把多模态计量能力继续推进到图片、音频、Realtime、Embedding、Video 和成本报表。

## 实施状态

截至 2026-05-21，本方案的 Phase 0 + Phase 1 已落地，并推进了 Phase 2/3/4 的低风险部分：

- 新增 `usage_dimensions` 与 `image_usage` schema，`request_usage` 继续兼容旧报表。
- `/v1/images/generations` 与 `/v1/images/edits` 已进入 usage-metered；支持 OpenAI Images JSON/SSE usage、图片数、partial image 数、图片请求安全元数据。
- `/v1/responses` 的 `image_generation_call` 已进入图片事实计量；记录 image_count/partial_image_count，不保存 `result` 或 `revised_prompt`。
- Gemini/Google `generateContent` 的图片 `inlineData` 输出已进入图片事实计量；只记录图片数量与 usage metadata，不保存 `data`。
- `/v1/images/variations`、`/v1/audio/*`、`/v1/videos*` 已进入 request-only，先保证请求事实可见。
- `/v1/embeddings` 已进入 usage-metered。
- 非流式图片响应改为 prefix+tail 采样，避免大 `b64_json` 把尾部 `usage` 挤出采样窗口。
- Gemini 非流式响应同样使用 prefix+tail 兜底，避免大 `inlineData.data` 把尾部 `usageMetadata` 挤出采样窗口。
- `pricing.yaml` 支持 `multimodal_pricing`，可按 text/image/audio channel 和 direction 分别估算成本；图片成本会区分非缓存输入、缓存输入和输出。
- WebUI/API 新增 `/api/multimodal/summary`、`/api/images/summary`、`/api/images/models`、`/api/images/requests`。

## 目标修正

“至少能计量图片相关数据”是 P0 验收门槛，不是最终边界。本项目作为 AI 网关透明计量代理，应该从单一 `llm_tokens` 计量扩展为多模态请求事实 + 多维用量明细：

- 图片：必须进入 P0，覆盖 direct Image API、CPA provider alias、Responses image tool。
- 文本：保留现有 Chat/Responses/Anthropic/Gemini token 计量，并修复与多模态混合请求的归类问题。
- Embedding：补齐 `/v1/embeddings` token 计量。
- 音频：补齐 speech/transcription/translation 请求级计量，并支持秒级或 token 级成本。
- Realtime：先做 session/request 事实和可对账结构，再按 CPA/上游可观测事件推进真实 audio/text/image token 计量。
- Video：先 request-only + async status 追踪，再按 provider usage contract 计量。
- 成本：扩展 `pricing.yaml` 与 calculator，支持 modality/channel/direction 维度，而不是继续把所有成本塞进文本 token 模型。

## 官方现状

本方案基于 2026-05-21 可确认的官方资料：

- CLIProxyAPI 最新发布为 [v7.1.19](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v7.1.19)，发布时间为 2026-05-20。
- CLIProxyAPI 官方配置项 `disable-image-generation` 支持 `false`、`true`、`"chat"` 三态；`true` 会让 `/v1/images/generations` 与 `/v1/images/edits` 返回 404，`"chat"` 会禁用非 images 端点注入但保留 images 端点可用。来源：[CLIProxyAPI 配置选项](https://help.router-for.me/cn/configuration/options)。
- CLIProxyAPI 的 xAI/Grok 文档说明图片请求使用 `/v1/images/generations` 与 `/v1/images/edits`，模型为 `grok-imagine-image` 或 `grok-imagine-image-quality`。来源：[xAI / Grok provider 文档](https://help.router-for.me/configuration/provider/xai)。
- CLIProxyAPI Management API 目前提供 `GET /v0/management/usage-queue?count=N`，示例 payload 以 token usage 为主；队列需要 `usage-statistics-enabled: true`。来源：[CLIProxyAPI Management API](https://help.router-for.me/cn/management/api)。
- OpenAI 图片能力同时通过 Image API 与 Responses API 提供；Image API 包含 generations、edits，variations 只适用于支持它的模型。来源：[OpenAI Image generation guide](https://developers.openai.com/api/docs/guides/image-generation)。
- OpenAI Images API 的 JSON/SSE 成功响应可包含 `usage`，其中可能有 `input_tokens`、`output_tokens`、`total_tokens` 和 `input_tokens_details.image_tokens` 等字段。来源：[Images create](https://developers.openai.com/api/docs/api-reference/images/create)、[Images edits](https://developers.openai.com/api/docs/api-reference/images/createEdit)。
- OpenAI Realtime 文档把 `gpt-realtime-2`、`gpt-realtime-translate`、`gpt-realtime-whisper` 作为不同实时音频场景的入口；Realtime session 与普通 HTTP request 不同，需要单独处理连接生命周期和事件用量。来源：[Realtime and audio guide](https://developers.openai.com/api/docs/guides/realtime)。

## 实施前代码短板

现有项目已经完成文本 LLM 主链路，但多模态会暴露这些短板：

- `docs/cpa-compatibility.md` 仍把 `/v1/images/generations`、`/v1/images/edits`、`/v1/videos*` 标为 pass-through。
- `event.MeteringImageCount`、`MeteringAudioSeconds`、`MeteringEmbeddingTokens` 已有枚举，但 proxy/profile/db/report 还没有真正贯通。
- `CaptureRequestOnly` 已有常量，但缺少完整请求事实记录路径；这会影响 files/uploads/videos/status 这类不直接返回 usage 的端点。
- 非流式响应只采样前缀。图片响应里的 `b64_json` 可能很大，`usage` 在尾部时会被截断。
- SSE parser 不能简单放大 line limit；图片 partial/completed event 可能包含大 base64，必须跳过大字符串而不是缓存整行。
- `pricing.yaml` 与 `pricing.ModelPrice` 只有文本 token 输入/缓存/输出字段，无法表达 image/audio/realtime 的 channel-specific pricing。
- WebUI 汇总维度是 token-centric，缺少 modality、operation、seconds、image_count、missing_usage 等视图。

## 总体架构

保留 `request_usage` 作为 HTTP 请求事实表。新增“用量维度表”，不要把所有多模态数据继续塞进 `input_tokens/output_tokens`。

```sql
CREATE TABLE IF NOT EXISTS usage_dimensions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_usage_id INTEGER NOT NULL DEFAULT 0,
    request_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    created_at_unix INTEGER NOT NULL DEFAULT 0,

    endpoint_profile TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',

    modality TEXT NOT NULL DEFAULT '',     -- text, image, audio, video, embedding, moderation, realtime
    channel TEXT NOT NULL DEFAULT '',      -- text, image, audio, video, none
    metric TEXT NOT NULL DEFAULT '',       -- tokens, seconds, count, bytes, request
    direction TEXT NOT NULL DEFAULT '',    -- input, cached_input, output, cache_creation, none
    unit TEXT NOT NULL DEFAULT '',         -- token, second, image, request, byte
    amount REAL NOT NULL DEFAULT 0,

    usage_source TEXT NOT NULL DEFAULT '', -- http_response, stream_completed, cpa_side_channel, org_usage, request_metadata
    capture_outcome TEXT NOT NULL DEFAULT '',
    capture_reason TEXT NOT NULL DEFAULT '',
    details_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_usage_dimensions_created_at_unix
    ON usage_dimensions(created_at_unix);
CREATE INDEX IF NOT EXISTS idx_usage_dimensions_request_usage_id
    ON usage_dimensions(request_usage_id);
CREATE INDEX IF NOT EXISTS idx_usage_dimensions_modality_created
    ON usage_dimensions(modality, created_at_unix);
CREATE INDEX IF NOT EXISTS idx_usage_dimensions_model_created
    ON usage_dimensions(model, created_at_unix);
```

图片还需要一个轻量详情表，避免 WebUI 每次都解析 JSON：

```sql
CREATE TABLE IF NOT EXISTS image_usage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_usage_id INTEGER NOT NULL DEFAULT 0,
    request_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    created_at_unix INTEGER NOT NULL DEFAULT 0,

    operation TEXT NOT NULL DEFAULT '',    -- generation, edit, variation, responses_tool
    provider TEXT NOT NULL DEFAULT '',
    model_requested TEXT NOT NULL DEFAULT '',
    model_returned TEXT NOT NULL DEFAULT '',
    size TEXT NOT NULL DEFAULT '',
    quality TEXT NOT NULL DEFAULT '',
    output_format TEXT NOT NULL DEFAULT '',
    stream INTEGER NOT NULL DEFAULT 0,
    image_count INTEGER NOT NULL DEFAULT 0,
    partial_image_count INTEGER NOT NULL DEFAULT 0,
    input_image_count INTEGER NOT NULL DEFAULT 0,
    has_mask INTEGER NOT NULL DEFAULT 0,

    usage_source TEXT NOT NULL DEFAULT '',
    capture_outcome TEXT NOT NULL DEFAULT '',
    capture_reason TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);
```

兼容策略：

- `request_usage` 继续写现有 token 字段，保证旧 WebUI 和 API 不破。
- 新的多模态报表优先读 `usage_dimensions`。
- 对纯文本 LLM，可以同步写一组 `usage_dimensions`，但第一阶段不强制回填历史数据。

## 路由推进矩阵

### P0：必须落地

| Route | Profile | Capture | Notes |
|---|---|---|---|
| `POST /v1/images/generations` | `openai_images_generations` | usage metered | JSON + SSE |
| `POST /v1/images/edits` | `openai_images_edits` | usage metered | JSON + multipart |
| `POST /api/provider/{provider}/v1/images/generations` | `openai_images_generations` | usage metered | CPA alias |
| `POST /api/provider/{provider}/v1/images/edits` | `openai_images_edits` | usage metered | CPA alias |
| `POST /v1/images/variations` | `openai_images_variations` | request-only first | 需 CPA 实测后再升 usage metered |
| `POST /api/provider/{provider}/v1/images/variations` | `openai_images_variations` | request-only first | 预留 |
| `POST /v1/responses` with `image_generation` tool | `responses` + image detail | mixed usage | 不保存 result/revised_prompt |
| `POST /v1beta/models/{model}:generateContent` with image `inlineData` output | `gemini_generate_content` + image detail | mixed usage | 不保存 inlineData.data |

### P1：继续推进

| Route | Capture | Notes |
|---|---|---|
| `POST /v1/embeddings` | embedding tokens | 通常响应含 usage/prompt_tokens |
| `POST /v1/audio/speech` | request-only + audio bytes/seconds when known | TTS 可能按字符/token/音频计价，需 provider adapter |
| `POST /v1/audio/transcriptions` | audio seconds or model usage | multipart，不能保存音频正文 |
| `POST /v1/audio/translations` | audio seconds or model usage | multipart，不能保存音频正文 |
| `POST /v1/files`、`POST /v1/uploads/*` | request-only | 支撑 image/audio/video 引用文件对账 |

### P2：需要更强 CPA/上游契约

| Route | Capture | Notes |
|---|---|---|
| Realtime session endpoints | session facts + side-channel/reconciliation | HTTP 创建请求不足以代表真实用量 |
| `POST /v1/videos`、`POST /v1/videos/edits` | request-only + async usage | 先记录任务，再从 status/side-channel 补齐 |
| `GET /v1/videos/{id}` | request-only + status metadata | 不作为生成用量本身 |
| Organization usage APIs | reconciliation | 用 admin key 对账，不走用户流量代理 |

## 图片提取策略

### 请求 metadata

JSON request 提取：

- `model`
- `n`
- `size`
- `quality`
- `output_format`
- `stream`
- `partial_images`
- `images` 数量
- `mask` 是否存在

Multipart request 提取：

- 只解析 form field header 和小文本字段。
- `image`、`image[]`、`mask` 文件 part 只计数，不保存文件名，不读取正文。
- 请求转发时同步维护首尾采样窗口；如果 `model`、`size`、`quality`、`output_format` 在大文件之后，仍尽力从尾部安全字段补齐，不阻塞转发。

### 非流式响应

不能依赖当前的前缀 sample。需要实现 bounded JSON scanner：

- 转发响应时同步喂给 scanner。
- 识别顶层 `usage`、`data` 数组长度、model。
- 遇到 `b64_json`、`url`、`result` 只跳过字符串内容并计数。
- Gemini 响应遇到 `inlineData.data` 只跳过字符串内容，并按 `mimeType=image/*` 计数。
- scanner 内存上限固定，例如 128 KiB。

最低 MVP 可以用 prefix + tail sample，但必须标记 `capture_confidence=best_effort`。推荐直接做 scanner。

### SSE 响应

图片流式事件可能包括：

- `image_generation.partial_image`
- `image_generation.completed`
- `image_edit.partial_image`
- `image_edit.completed`
- Responses 中的 `response.image_generation_call.partial_image`

新增 `ImageSSEScanner`：

- 识别 `event:` 与 `data:`。
- partial event 只增加 `partial_image_count`。
- completed event 提取 usage 和最终 image_count。
- 超大 `data:` 行不算 parse error；只要完成事件和 usage 能识别，就算 captured。

## 多模态定价设计

你提供的 OpenAI 多模态价格需要进入配置，但不能直接塞进当前 `pricing` 结构，因为当前 parser 只支持文本 token 单价。建议新增 `multimodal_pricing`，并保留旧 `pricing`：

```yaml
multimodal_pricing:
  gpt-realtime-2:
    text:
      input_per_1m: 4.00
      cached_input_per_1m: 0.40
      output_per_1m: 24.00
    audio:
      input_per_1m: 32.00
      cached_input_per_1m: 0.40
      output_per_1m: 64.00
    image:
      input_per_1m: 5.00
      cached_input_per_1m: 0.50

  gpt-realtime-translate:
    audio_seconds:
      per_minute: 0.034
      per_second: 0.00057

  gpt-realtime-whisper:
    audio_seconds:
      per_minute: 0.017
      per_second: 0.00028

  gpt-image-2:
    text:
      input_per_1m: 5.00
      cached_input_per_1m: 1.25
    image:
      input_per_1m: 8.00
      cached_input_per_1m: 2.00
      output_per_1m: 30.00
```

成本计算规则：

- `usage_dimensions.modality/channel/direction` 决定使用哪组价格。
- `metric=tokens` 且 `unit=token` 时按每 1M token 计价。
- `metric=seconds` 且 `unit=second` 时优先用 `per_second`/`input_per_second`/`output_per_second`，没有则用对应 `per_minute / 60`。
- 配置中没有对应价格时，成本返回 `known=false`，WebUI 显示 partial，而不是显示 0 当成免费。
- 价格来自 operator 配置；代码不硬编码官方价格。

## CPA side-channel 策略

当前 CPA usage queue 示例仍以 token usage 为核心，图片尺寸、质量、image_count 还不能作为已验证契约。因此：

- P0 以 HTTP 响应解析为主，CPA side-channel 为补强。
- 如果 side-channel endpoint 是 images 或 responses image tool，且 payload 有 tokens，则写入 `usage_dimensions` 并关联 `request_usage`。
- 如果 CPA 未来增加 `image_count`、`size`、`quality`、`source` 等字段，先进入 `details_json`，再升为结构化列。
- 默认保持 `stored_only`，不自动覆盖 HTTP 捕获结果；merge 需要显式配置。

## WebUI 与 API

新增 API：

- `GET /metering/api/multimodal/summary?range=24h`
- `GET /metering/api/multimodal/dimensions?range=24h`
- `GET /metering/api/images/summary?range=24h`
- `GET /metering/api/images/models?range=24h`
- `GET /metering/api/images/requests?range=24h&limit=100`

图片 summary 示例：

```json
{
  "range": "24h",
  "request_count": 12,
  "failed_count": 1,
  "image_count": 14,
  "partial_image_count": 6,
  "input_text_tokens": 300,
  "input_image_tokens": 900,
  "output_image_tokens": 8000,
  "total_tokens": 9200,
  "missing_usage_count": 2,
  "cost": {
    "known_cost": 0.214,
    "partial": true,
    "unpriced_dimensions": 1
  }
}
```

WebUI 调整：

- Overview 增加 multimodal 区块：text/image/audio/video/embedding/realtime 分组。
- Images tab：请求数、图片数、失败数、missing usage、按 operation/model/size/quality 分组。
- Costs tab：展示 known cost、partial cost、unpriced dimensions，不再把 unknown cost 静默当 0。

## Caddy 与 CPA 配置

Caddy 需要把图片和后续多模态端点路由到 MeteringProxy：

```caddyfile
@metered {
    method POST
    path /v1/chat/completions /v1/completions /v1/responses /v1/responses/compact /v1/messages
    path /v1/images/generations /v1/images/edits /v1/images/variations
    path /v1/embeddings
    path /v1/audio/speech /v1/audio/transcriptions /v1/audio/translations
    path /api/provider/*/images/generations /api/provider/*/images/edits /api/provider/*/images/variations
    path /api/provider/*/embeddings
    path /api/provider/*/v1/images/generations /api/provider/*/v1/images/edits /api/provider/*/v1/images/variations
    path /api/provider/*/v1/embeddings
    path /api/provider/*/v1/audio/speech /api/provider/*/v1/audio/transcriptions /api/provider/*/v1/audio/translations
}
```

CPA 配置：

```yaml
disable-image-generation: false
# 或仅禁用 chat/responses 注入，保留 images endpoints：
# disable-image-generation: "chat"
usage-statistics-enabled: true
```

如果 `disable-image-generation: true`，MeteringProxy 仍记录 404 request fact，但不能计入成功图片生成。

## 实施计划

### Phase 0：底座修复

1. 更新 `docs/cpa-compatibility.md` 到 CPA v7.1.19。
2. 实现 `CaptureRequestOnly` 的完整记录路径：转发透明，记录 status、latency、bytes、error、request_id。
3. 新增 `usage_dimensions` schema、store、query。
4. 扩展 pricing parser：保留旧 `pricing`，新增 `multimodal_pricing`。
5. WebUI/API 对 unknown cost 显示 partial，不再静默为 0。

### Phase 1：图片 P0

1. 新增 image profiles 和 matchers。
2. 新增 JSON/multipart request metadata scanner。
3. 新增 image response scanner，跳过 `b64_json`。
4. 新增 image SSE scanner，处理 partial/completed。
5. 写入 `image_usage` 和 `usage_dimensions`。
6. 更新 README Caddy 示例与 CPA 配置提示。
7. 增加 fake CPA v7.1.19 contract tests。

### Phase 2：Responses 图片与 Embedding

1. Responses request 中识别 `tools[].type == "image_generation"`。
2. Responses output 中识别 `image_generation_call`，不保存 `result`/`revised_prompt`。
3. `/v1/embeddings` 增加 profile 与 extractor，写 `modality=embedding`。

### Phase 3：音频与 Realtime

1. `/v1/audio/*` 先 request-only + metadata，禁止保存音频正文。
2. 对能返回 duration/usage 的 provider adapter 写 `audio_seconds` 或 audio tokens。
3. Realtime session 创建请求先计 request fact；真实用量依赖 CPA side-channel、OpenAI event usage 或官方 usage reconciliation。
4. 使用你给的 `gpt-realtime-2`、`gpt-realtime-translate`、`gpt-realtime-whisper` 定价计算成本。

### Phase 4：Video 与官方对账

1. `/v1/videos*` 先 request-only，记录 task id hash、status、latency。
2. 如 provider 返回 duration/frames/resolution/usage，再写 `usage_dimensions`。
3. 可选 OpenAI admin usage adapter，保存官方 bucket 聚合数据，用于 proxy captured 与 official aggregate 对账。

## 验收标准

P0 必须满足：

- `/v1/images/generations` 非流式大 base64 响应仍能记录 image_count 和 usage tokens。
- `/v1/images/generations` 流式 partial/completed 能记录 partial_image_count、image_count 和 completed usage。
- `/v1/images/edits` multipart 不把 prompt、文件名、图片字节、mask 字节写入 SQLite、日志或测试 golden。
- `/api/provider/xai/v1/images/generations` 能匹配图片 profile。
- CPA `disable-image-generation=true` 返回 404 时，记录失败请求，但 image_count 为 0。
- `usage_dimensions` 中能看到 image/text token 维度，成本可按 `gpt-image-2` 图片/文本价格分别计算。
- `go test ./...` 通过。

P1/P2 验收：

- `/v1/embeddings` 能记录 embedding tokens。
- `/v1/audio/*` 至少能 request-only，能提取安全 metadata。
- Realtime session 能记录 session request fact，成本明确显示 partial，直到真实 usage contract 接入。
- WebUI 能展示 multimodal summary 和 unpriced dimensions。

## 风险与处理

| 风险 | 处理 |
|---|---|
| 图片 base64 过大导致内存上升 | scanner 跳过大字符串，不扩大全局 SSE line limit |
| provider 不返回 usage | 记录 request/image_count，显示 missing_usage_count |
| CPA side-channel 暂无图片字段 | P0 以 HTTP 响应解析为主，side-channel 只补强 |
| 多模态价格频繁变化 | 价格只进配置，不硬编码 |
| Realtime 用量不在 HTTP 响应里 | 先 request fact，再接 side-channel/event/reconciliation |
| 旧 WebUI 依赖 token 字段 | 保留 `request_usage`，新增多模态查询，不破坏旧接口 |

## 推荐落地顺序

先做 Phase 0 + Phase 1。这样不是“只做图片”，而是先修复 request-only、schema、pricing、scanner 这些底座问题，再用图片端点完成端到端验证。随后 Phase 2/3/4 可以自然接上 Embedding、Audio、Realtime 和 Video，而不会再次重构数据库与成本模型。
