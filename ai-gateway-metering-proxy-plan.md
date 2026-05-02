# AI Gateway 透明计量代理方案

版本：v0.2  
日期：2026-05-02  
状态：准备实施  
目标：在不影响 LLM 体验的前提下，为 CLIProxyAPI 提供长期可维护的请求、token、成本和账号维度统计能力，并提供独立 WebUI。

---

## 1. 当前判断

CLIProxyAPI 新版本已经不再提供旧的全局 usage 接口：

```text
/v0/management/usage         -> 404
/v0/management/usage/export  -> 404
/v0/management/usage/import  -> 404
```

当前还能看到的统计入口主要是：

```text
/v0/management/api-key-usage  # OpenAI 兼容供应商 API key 的 recent_requests
/v0/management/auth-files     # Codex/OAuth auth 的 recent_requests
```

这些接口可以做辅助状态观测，但不适合作为长期完整账本，因为它们是 CLIProxyAPI 内部统计模型的一部分，未来仍可能变化或移除。

结论：长期统计不再依赖 CLIProxyAPI 官方 statistics 模块，而是在主调用链路前增加一个尽量透明的 Metering Proxy，从真实响应里复制 usage 信息并持久化。

核心原则不变：

```text
统计可以丢，LLM 请求不能慢、不能断、不能被改写。
```

---

## 2. 为什么自己做，而不是直接上现成项目

第一版建议自己做极简透明代理，不直接把 LiteLLM、Portkey、Helicone、Langfuse 这类项目放进主链路。

原因是这些项目大多是完整 LLM Gateway 或完整 observability 平台，会天然引入更多能力：模型路由、fallback、重试、key 管理、缓存、协议适配、服务端面板、复杂存储。它们适合更大的平台化场景，但不适合作为当前 Codex + CLIProxyAPI 主链路前的一层极简统计代理。

本项目的目标不是替代 CLIProxyAPI，而是补足统计能力。因此第一版只做：

- 透明转发 OpenAI-compatible 请求。
- 从响应中复制 usage。
- 异步写入 SQLite。
- 提供只读 WebUI。
- 保留 CLIProxyAPI 的账号调度、session affinity、冷却、供应商管理能力。

现成项目可借鉴但不接管主链路：

| 项目 | 可借鉴 | 不作为第一版主链路的原因 |
| --- | --- | --- |
| LiteLLM | 成本模型、spend tracking、dashboard 思路 | 完整网关，路由/重试/key 管理语义较重 |
| Portkey | 网关治理、fallback、观测设计 | 能力过重，会改变主链路职责边界 |
| Helicone | 观测 UI、请求统计、流式处理经验 | 需要接入其 proxy/服务体系，不够极简 |
| Langfuse | tracing、项目级观测、报表 | 自托管依赖重，超出 2H2G 小服务器目标 |
| CPA Usage Keeper | SQLite 和可视化思路 | 依赖 CPA 统计入口，不是透明响应采样 |

后续如果需要，可以把 Metering Proxy 的数据导出给这些系统，但第一版不让它们接管请求路径。

---

## 3. 总体架构

正式架构：

```text
Client
  -> Caddy
  -> Metering Proxy
  -> CLIProxyAPI
  -> upstream model provider
```

管理面继续绕过 Metering Proxy：

```text
/management.html       -> CLIProxyAPI
/v0/management*        -> CLIProxyAPI
/v1/models             -> CLIProxyAPI
/v1/chat/completions   -> Metering Proxy -> CLIProxyAPI
/v1/responses          -> Metering Proxy -> CLIProxyAPI
/metering              -> Metering Proxy WebUI
/metering/api/*        -> Metering Proxy WebUI API
```

第一版代理范围：

```text
POST /v1/chat/completions
POST /v1/responses
```

第一版不代理：

```text
/v1/models
/v0/management*
/management.html
```

这样可以减少不必要的行为差异，所有管理功能仍直接由 CLIProxyAPI 提供。

---

## 4. 非目标

Metering Proxy 不是完整网关，明确不做：

- 不做模型路由。
- 不做账号路由。
- 不做请求重试。
- 不做 fallback。
- 不做 payload override。
- 不做 prompt 注入。
- 不做 OpenAI/Anthropic/Gemini 协议互转。
- 不记录 prompt 原文。
- 不记录 assistant response 原文。
- 不保存原始 API key。
- 不依赖 CLIProxyAPI 的 `/v0/management/usage*`。
- 不接管 CLIProxyAPI 的 WebUI 和配置管理。

---

## 5. 透明转发原则

主链路规则：

- 请求方法、路径、query 尽量原样转发。
- 请求体原样转发。
- 响应状态码原样转发。
- 响应体原样转发。
- SSE chunk 原样、及时 flush。
- 不缓冲完整流式响应。
- 不等待数据库写入后再返回。
- 不因统计解析失败中断响应。
- 不因数据库不可用中断响应。
- 不主动 retry 上游 LLM 请求，避免重复扣额度或重复工具调用。

统计写入规则：

- 解析成功则异步写入。
- 解析失败只记录本地错误计数，不影响响应。
- 写入失败只记录本地错误计数，不影响响应。
- 内存队列满时丢弃统计事件，不阻塞 LLM。
- 允许少量统计丢失，不允许请求链路被统计系统拖慢。

---

## 6. Caddy 路由

第一阶段只把模型调用切到 Metering Proxy，管理面继续直连 CLIProxyAPI。

```caddyfile
api.20230102.xyz {
    encode zstd gzip

    request_body {
        max_size 20MB
    }

    @management_ui path /management.html
    handle @management_ui {
        basic_auth {
            qingyang REPLACE_WITH_CADDY_HASH
        }
        header Cache-Control "private, max-age=3600"
        reverse_proxy 127.0.0.1:8317
    }

    @management_api path /v0/management*
    handle @management_api {
        reverse_proxy 127.0.0.1:8317
    }

    @metering_ui path /metering /metering/*
    handle @metering_ui {
        basic_auth {
            qingyang REPLACE_WITH_CADDY_HASH
        }
        reverse_proxy 127.0.0.1:8320
    }

    @metered path /v1/chat/completions /v1/responses
    handle @metered {
        reverse_proxy 127.0.0.1:8320 {
            stream_close_delay 5m
            transport http {
                dial_timeout 5s
                response_header_timeout 180s
                read_timeout 0
                write_timeout 0
            }
        }
    }

    reverse_proxy 127.0.0.1:8317 {
        stream_close_delay 5m
        transport http {
            dial_timeout 5s
            response_header_timeout 180s
            read_timeout 0
            write_timeout 0
        }
    }
}
```

监听关系：

```text
Metering Proxy: 127.0.0.1:8320
CLIProxyAPI:    http://127.0.0.1:8317
```

快速回滚：

```text
把 @metered 的 reverse_proxy 127.0.0.1:8320 改回 127.0.0.1:8317
sudo caddy fmt --overwrite /etc/caddy/Caddyfile
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

---

## 7. 数据来源

### 7.1 Chat Completions streaming

最终 chunk 可能包含：

```json
{
  "choices": [
    {
      "delta": {},
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 361,
    "completion_tokens": 56,
    "total_tokens": 417,
    "completion_tokens_details": {
      "reasoning_tokens": 11
    },
    "prompt_tokens_details": {
      "cached_tokens": 0
    }
  }
}
```

映射：

```text
prompt_tokens -> input_tokens
completion_tokens -> output_tokens
completion_tokens_details.reasoning_tokens -> reasoning_tokens
prompt_tokens_details.cached_tokens -> cached_tokens
total_tokens -> total_tokens
```

### 7.2 Responses streaming

最终 `response.completed` 事件可能包含：

```json
{
  "type": "response.completed",
  "response": {
    "model": "gpt-5.4-mini-2026-03-17",
    "usage": {
      "input_tokens": 371,
      "output_tokens": 43,
      "output_tokens_details": {
        "reasoning_tokens": 0
      },
      "input_tokens_details": {
        "cached_tokens": 0
      },
      "total_tokens": 414
    }
  }
}
```

映射：

```text
response.model -> model_returned
response.usage.input_tokens -> input_tokens
response.usage.output_tokens -> output_tokens
response.usage.output_tokens_details.reasoning_tokens -> reasoning_tokens
response.usage.input_tokens_details.cached_tokens -> cached_tokens
response.usage.total_tokens -> total_tokens
```

### 7.3 非流式 JSON

非流式 JSON 响应可以在转发给客户端时复制一份有限大小的响应字节给 parser。

限制：

```text
max_nonstream_sample_bytes: 2MB
```

超过上限时，只记录 status、latency、model_requested、request_bytes、response_bytes，不记录 tokens。

---

## 8. 可统计字段

第一版保存以下字段：

```text
created_at
request_id
endpoint
method
status
latency_ms
stream
client_ip_hash
api_key_hash
model_requested
model_returned
input_tokens
output_tokens
reasoning_tokens
cached_tokens
total_tokens
request_bytes
response_bytes
error
```

可选增强字段：

```text
finish_reason
service_tier
provider_hint
upstream_request_id
```

注意：如果 CLIProxyAPI 响应中没有暴露实际命中的 OAuth auth，Metering Proxy 不能凭空知道具体用了哪个 Codex 账号。账号级请求数可以继续辅助读取 `/v0/management/auth-files` 的 recent_requests，但 token 级别的账号归因不应伪造。

---

## 9. 数据库设计

第一版使用 SQLite WAL。单机、小团队、2H2G 服务器足够，部署和备份简单。

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;

CREATE TABLE IF NOT EXISTS request_usage (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at TEXT NOT NULL,
  request_id TEXT,
  endpoint TEXT NOT NULL,
  method TEXT NOT NULL,
  status INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL,
  stream INTEGER NOT NULL,
  client_ip_hash TEXT,
  api_key_hash TEXT,
  model_requested TEXT,
  model_returned TEXT,
  input_tokens INTEGER DEFAULT 0,
  output_tokens INTEGER DEFAULT 0,
  reasoning_tokens INTEGER DEFAULT 0,
  cached_tokens INTEGER DEFAULT 0,
  total_tokens INTEGER DEFAULT 0,
  request_bytes INTEGER DEFAULT 0,
  response_bytes INTEGER DEFAULT 0,
  error TEXT
);

CREATE INDEX IF NOT EXISTS idx_request_usage_created_at
  ON request_usage(created_at);

CREATE INDEX IF NOT EXISTS idx_request_usage_model
  ON request_usage(model_returned);

CREATE INDEX IF NOT EXISTS idx_request_usage_key
  ON request_usage(api_key_hash);

CREATE INDEX IF NOT EXISTS idx_request_usage_status
  ON request_usage(status);
```

API key 只保存 hash：

```text
sha256(local_salt + bearer_token)
```

salt 保存在服务器本地，例如：

```text
/opt/ai-gateway/metering/salt
```

权限：

```text
salt: 600
usage.sqlite: 600
```

---

## 10. WebUI 范围

既然 Metering Proxy 是独立统计系统，就需要自己的 WebUI。第一版 WebUI 只做只读统计，不做配置修改。

入口：

```text
https://api.20230102.xyz/metering
```

通过 Caddy Basic Auth 保护，不单独开放公网无认证入口。

### 10.1 第一版页面

第一版 WebUI 包含：

- Overview：今日、昨日、近 7 天、近 30 天请求数、失败数、token 数、估算成本。
- Requests：最近请求列表，可按时间、endpoint、status、model、api_key_hash 过滤。
- Models：按模型聚合 input/output/reasoning/cached/total tokens。
- Keys：按 client API key hash 聚合请求量、失败率、token 数。
- Errors：非 2xx、上游错误、解析失败、DB 写入失败趋势。
- Health：queue depth、dropped events、parse errors、db write errors。

第一版不做：

- 不展示 prompt。
- 不展示 assistant response。
- 不展示原始 API key。
- 不做用户管理。
- 不做账单扣费。
- 不做 CLIProxyAPI 配置编辑。

### 10.2 WebUI API

Metering Proxy 提供 localhost 服务内的只读 API，由 Caddy `/metering/*` 反代并用 Basic Auth 保护。

```text
GET /metering/api/summary?range=24h|7d|30d
GET /metering/api/timeseries?range=24h|7d|30d&bucket=10m|1h|1d
GET /metering/api/models?range=24h|7d|30d
GET /metering/api/keys?range=24h|7d|30d
GET /metering/api/requests?limit=100&status=&model=&endpoint=
GET /metering/api/errors?range=24h|7d|30d
GET /metering/api/health
```

### 10.3 成本估算

第一版可以支持本地价格表：

```yaml
pricing:
  gpt-5.4-mini:
    input_per_1m: 0
    cached_input_per_1m: 0
    output_per_1m: 0
  deepseek-chat:
    input_per_1m: 0
    cached_input_per_1m: 0
    output_per_1m: 0
```

价格表仅用于估算展示，不影响请求链路。未知模型显示 token 数，不强行估价。

---

## 11. 队列与失败策略

推荐参数：

```yaml
queue_capacity: 1000
batch_size: 50
flush_interval: 1s
max_nonstream_sample_bytes: 2097152
```

失败策略：

- 队列满：丢弃统计事件，递增 `dropped_events_total`。
- DB locked：短暂重试一次，失败则丢弃。
- JSON parse error：递增 `parse_error_total`，不影响响应。
- 上游失败：仍记录 status、latency、error。
- 客户端中断：记录已知状态，不 panic。
- Proxy 自身异常：由 systemd 或 Docker 重启；Caddy 可快速回滚直连 CLIProxyAPI。

---

## 12. HTTP 行为细节

必须保留或正确处理：

- `Authorization`
- `Content-Type`
- `Accept`
- `User-Agent`
- `OpenAI-*` 或客户端自定义头
- `Transfer-Encoding`
- SSE `Content-Type: text/event-stream`
- flush timing
- client disconnect
- upstream timeout

可以新增内部响应头：

```text
X-Metering-Proxy: 1
```

不新增会影响客户端行为的头。

---

## 13. 安全策略

- Metering Proxy 只监听 `127.0.0.1`。
- WebUI 通过 Caddy Basic Auth 保护。
- 数据库文件权限 `600`。
- 不记录 prompt、response body、Authorization 原文。
- 日志中不输出 API key。
- 统计数据库和备份不放进公开仓库。
- WebUI 第一版只读，不提供删除、修改、导入、重放请求功能。

---

## 14. 配置建议

最小配置：

```yaml
listen: "127.0.0.1:8320"
upstream: "http://127.0.0.1:8317"
database: "/opt/ai-gateway/metering/usage.sqlite"
salt_file: "/opt/ai-gateway/metering/salt"
queue_capacity: 1000
batch_size: 50
flush_interval: "1s"
max_nonstream_sample_bytes: 2097152
webui:
  enabled: true
  base_path: "/metering"
pricing_file: "/opt/ai-gateway/metering/pricing.yaml"
```

---

## 15. 部署方式

第一版可以用 Docker Compose 或 systemd。考虑到当前 CLIProxyAPI 已经用 Docker Compose，建议 Metering Proxy 也放进同一个 compose，减少分散管理。

目录建议：

```text
/opt/ai-gateway/metering/
  config.yaml
  pricing.yaml
  usage.sqlite
  salt
  backups/
```

如果用 systemd，服务只监听 localhost：

```text
ExecStart=/usr/local/bin/ai-gateway-metering-proxy -config /opt/ai-gateway/metering/config.yaml
Restart=always
RestartSec=3
```

---

## 16. 备份策略

SQLite 使用 WAL 后，备份不要直接只拷贝 `usage.sqlite`。推荐使用 SQLite backup 或短暂停止服务后打包。

每日备份目标：

```text
/opt/ai-gateway/metering/backups/usage-YYYY-MM-DD.sqlite.gz
```

保留策略：

```text
每日备份保留 90 天
每月备份保留 12 个月
```

统计数据库损坏时，主链路仍然应该可用。最坏情况是统计从空库重新开始。

---

## 17. 验收测试

上线前必须通过：

1. `/v1/chat/completions` 非流式响应内容与直连 CLIProxyAPI 一致。
2. `/v1/chat/completions` 流式逐段输出，无明显缓冲。
3. `/v1/responses` 流式输出 `response.output_text.delta` 和 `response.completed`。
4. DB 停止或锁住时，LLM 请求仍然成功。
5. 队列填满时，LLM 请求仍然成功。
6. 客户端中断请求时，proxy 不 panic。
7. Caddy reload 不切断正在进行的流式请求。
8. 数据库不包含 prompt、response body、Authorization 原文。
9. 统计记录能正确区分 endpoint、model、status、latency、tokens。
10. WebUI 只能看到聚合和元数据，不能看到敏感原文。
11. 与直连 CLIProxyAPI 做 A/B，模型输出不应因 proxy 层改变。

---

## 18. 实施阶段

### 阶段 1：旁路验证

- 启动 Metering Proxy，但 Caddy 暂不接正式流量。
- 用本机 curl 直连 `127.0.0.1:8320` 测试 chat/completions 和 responses。
- 确认 DB 写入和 WebUI 展示。

### 阶段 2：个人流量切入

- Caddy 将 `/v1/chat/completions`、`/v1/responses` 切到 Metering Proxy。
- 保留快速回滚配置。
- 连续观察 1-3 天。

### 阶段 3：正式使用

- 保留 Caddy 直连 CLIProxyAPI 的 fallback 配置。
- 启用每日 SQLite 备份。
- 根据实际使用补充模型价格表和 WebUI 聚合视图。

---

## 19. 当前结论

现在应直接进入透明计量代理方案，不再继续维护旧的 CLIProxyAPI usage export/import 快照方案。

第一版自己做更合适，原因是：

- 目标足够窄。
- 主链路必须透明。
- 小服务器不适合引入重型观测栈。
- CLIProxyAPI 仍负责账号、路由、session affinity 和供应商管理。
- Metering Proxy 只负责复制 usage 并展示统计。

后续维护预期：

```text
主链路维护：低频
统计解析维护：中低频
WebUI/报表维护：按需求迭代
```

只要坚持透明原则，即使统计解析短期失效，也不应该影响 LLM 调用体验。
