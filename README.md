# MeteringProxy

MeteringProxy 是一个面向 AI 网关流量的透明计量代理，部署在 Caddy 与 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 之间。它在不修改请求或响应字节的前提下转发 LLM API 调用，同时将用量统计异步写入 SQLite，通过只读 WebUI 提供实时可见性。

## 为什么选择 MeteringProxy

- **透明转发，零侵入**：方法、路径、请求头、请求体、状态码及 SSE 字节流完全逐字透传，对客户端和上游服务商完全透明。
- **安全优先**：不存储提示词、响应正文、明文 API Key 或明文客户端 IP。哈希使用固定加盐，支持按 Key/IP 分组的匿名统计。
- **异步计量，不阻塞流量**：计量写入在独立队列中批量异步完成。队列满时丢弃事件而非阻塞 LLM 请求，转发可靠性始终高于计量完整性。
- **轻量单实例**：SQLite WAL 模式 + 单写入连接，无需 Redis、PostgreSQL 等外部依赖。Docker 一键部署，运维简单。
- **实时成本可见**：内置支持主流模型定价，按输入/缓存/输出/推理 token 分类估算成本，WebUI 提供多维度趋势和占比图表。
- **Key 是一等观测维度**：可为朋友、设备、Agent 或用途分配独立客户端 Key，并按 Key 查看成本、模型、趋势、质量、Issues 与最近请求；MeteringProxy 只做观测归因，不负责签发或吊销 Key。
- **生产就绪**：支持 Docker 部署、增量数据库迁移、热升级与回滚、Prometheus 指标暴露、Caddy 集成最佳实践。

## 架构

```text
客户端 -> Caddy -> MeteringProxy (:8320) -> CLIProxyAPI (:8317) -> 上游模型服务商
```

建议的 Caddy 路由策略：

- 将计量目标 API 路径路由至 MeteringProxy：OpenAI chat/completions、Responses/Codex direct、Anthropic messages、Gemini generateContent，以及 CPA provider aliases。完整支持矩阵见 [docs/cpa-compatibility.md](docs/cpa-compatibility.md)。
- 将非计量的管理与模型接口直接路由至 CLIProxyAPI。
- 使用 Basic Auth 或等效访问控制保护 `/metering` 路径。
- 如有需要，在 Caddy 侧配置请求体大小限制。Go 代理层有意不截断请求体，保持透明代理行为不变。

## 快速开始（Docker）

生产环境推荐使用 Docker 部署，通过 GitHub Container Registry 获取预构建镜像。单实例 SQLite 写入模型 —— 同一数据库文件不应被两个实例同时写入。

<details>
<summary><b>展开部署步骤</b></summary>

### 1. 前提条件

- 已安装 Docker 的 Linux 服务器
- Caddy（或其他反向代理）已部署
- CLIProxyAPI 已在运行

### 2. 创建共享网络

```bash
docker network create ai-gateway
docker network connect ai-gateway <cli-proxy-container-name>
```

> 将 `<cli-proxy-container-name>` 替换为 CLIProxyAPI 容器的实际名称（通过 `docker ps --format '{{.Names}}'` 查看）。容器间通信走 Docker 内部网络，不受 `-p` 绑定地址影响。
>
> **常见坑**：如果 MeteringProxy 和 CPA 各自用 `docker compose` 启动，默认会被分到不同网络（如 `ai-gateway` vs `ai-gateway_default`），导致 DNS 无法解析。用上述 `docker network create` / `docker network connect` 统一加入同一网络即可。

### 3. 准备主机目录

```bash
mkdir -p /opt/ai-gateway/metering/backups
chown -R 1000:1000 /opt/ai-gateway/metering
chmod 700 /opt/ai-gateway/metering
```

### 4. 生成盐值（只需一次）

```bash
python3 -c "import secrets; print(secrets.token_hex(32))" > /opt/ai-gateway/metering/salt
chown 1000:1000 /opt/ai-gateway/metering/salt
chmod 600 /opt/ai-gateway/metering/salt
```

盐值只生成一次，不要在升级或回滚时重新生成。它必须随数据库一起备份——更换盐值会破坏历史哈希分组。

### 5. 创建配置文件

在 `/opt/ai-gateway/metering/` 下创建两个文件：

**config.yaml**

> `upstream` 和 `base_url` 中的主机名必须与 CLIProxyAPI 的容器名一致。例如容器名为 `cli-proxy-api`，则填 `http://cli-proxy-api:8317`。`docker ps --format '{{.Names}}'` 可查看所有运行中的容器名。

```yaml
listen: "0.0.0.0:8320"
upstream: "http://<cpa-container-name>:8317"
database: "/data/usage.sqlite"
salt_file: "/data/salt"
queue_capacity: 1000
batch_size: 50
flush_interval: "1s"
max_nonstream_sample_bytes: 2097152
metering_enabled: true
webui:
  enabled: true
  base_path: "/metering"
pricing_file: "/data/pricing.yaml"
# 可选：完整 64 位小写 HMAC hash -> 显示标签；不要填写明文 Key。
key_labels:
  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "friend-a"
request_metadata:
  initial_bytes: 4096
  max_bytes: 65536
  extended_model_scan: false
observability:
  correlation:
    mode: "passive"
    header: "X-Request-ID"
    side_channel_merge: "stored_only"
    require_propagation_verified: true
cliproxy_management:
  enabled: true
  base_url: "http://<cpa-container-name>:8317/v0/management"
  key_file: "/data/cliproxy-management-key"
  usage_queue:
    enabled: true
    transport: "auto"
    merge_mode: "stored_only"
  credential_health:
    enabled: true
  quota:
    enabled: false
```

启用 `cliproxy_management` 时，需把 CLIProxyAPI management key 写入 `key_file` 指向的文件，并在 CPA 配置中开启 `usage-statistics-enabled: true`。当前版本默认只展示凭证健康与 side-channel 用量事件；完整 quota 快照在没有 provider-specific adapter 时保持关闭。

CPA v7.1.17 起已禁用旧 RESP usage queue 协议。生产环境请保持 `usage_queue.transport: "auto"`，或显式设为 `"http"`；不要在新 CPA 上强制使用 `"resp"`。

**pricing.yaml**

v0.5.0 的完整默认价格文件随 GitHub Release 以独立附件发布，也保存在仓库的 [`pricing.yaml`](pricing.yaml) 中；请直接复制该文件后按项目所有者给定的数值维护。下面仅是格式节选，不是完整默认价格文件。`validate` 会拒绝未知字段、负价格、无效 tier、alias 冲突和不完整的图片价格。

```yaml
pricing:
  # OpenAI
  gpt-5.4-mini:
    input_per_1m: 0.75
    cached_input_per_1m: 0.075
    output_per_1m: 4.50
  gpt-5.4:
    input_per_1m: 2.50
    cached_input_per_1m: 0.25
    output_per_1m: 15.00
  gpt-5.5:
    input_per_1m: 5.00
    cached_input_per_1m: 0.50
    output_per_1m: 30.00
  # Anthropic
  claude-sonnet-4-6:
    input_per_1m: 3.00
    cached_input_per_1m: 0.30
    cache_creation_per_1m: 3.75
    output_per_1m: 15.00
    reasoning_per_1m: 3.00
  claude-opus-4-7:
    input_per_1m: 15.00
    cached_input_per_1m: 1.50
    cache_creation_per_1m: 18.75
    output_per_1m: 75.00
    reasoning_per_1m: 15.00
  claude-haiku-4-5:
    input_per_1m: 1.00
    cached_input_per_1m: 0.10
    cache_creation_per_1m: 1.25
    output_per_1m: 5.00
    reasoning_per_1m: 1.00
  # DeepSeek
  deepseek-v4-flash:
    input_per_1m: 0.14
    cached_input_per_1m: 0.0028
    output_per_1m: 0.28
  deepseek-v4-pro:
    input_per_1m: 0.435
    cached_input_per_1m: 0.003625
    output_per_1m: 0.87

multimodal_pricing:
  gpt-image-2:
    aliases:
      - gpt-image-2.0
      - GPT-Image-2.0 (image)
    text:
      input_per_1m: 5.00
      cached_input_per_1m: 1.25
    image:
      input_per_1m: 8.00
      cached_input_per_1m: 2.00
      output_per_1m: 30.00
  gpt-realtime-2:
    text:
      input_per_1m: 4.00
      cached_input_per_1m: 0.40
      output_per_1m: 24.00
    image:
      input_per_1m: 5.00
      cached_input_per_1m: 0.50
    audio:
      input_per_1m: 32.00
      cached_input_per_1m: 0.40
      output_per_1m: 64.00
  gpt-realtime-translate:
    audio_seconds:
      per_second: 0.00057
  gpt-realtime-whisper:
    audio_seconds:
      per_second: 0.00028
```

### 6. 启动前预检

先用即将部署的镜像严格检查配置、价格、salt 和启用的 management key 文件。该命令不访问网络、不打开或迁移数据库，也不写文件：

```bash
docker run --rm --network none -v /opt/ai-gateway/metering:/data:ro ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0 validate --config /data/config.yaml
```

成功时 stdout 只输出 `ok`。生产 `serve` 同样使用严格配置加载；未知字段或多 YAML 文档会直接拒绝启动，避免配置拼写错误被静默忽略。

### 7. 拉取并启动容器

```bash
docker pull ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0

docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  --network ai-gateway \
  -v /opt/ai-gateway/metering:/data \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0 \
  serve --config /data/config.yaml
```

镜像内置 Docker liveness healthcheck，默认访问 `http://127.0.0.1:8320/healthz`。若配置使用非 8320 端口，必须同时传入例如 `-e METERING_PROXY_HEALTH_URL=http://127.0.0.1:9000/healthz`。

### 8. 配置 Caddy

```caddyfile
api.example.com {
    encode zstd gzip

    request_body {
        max_size 20MB
    }

    @metering_api path /metering/api /metering/api/*
    handle @metering_api {
        basic_auth {
            user <bcrypt-hash>
        }
        header Cache-Control "no-store, no-cache, must-revalidate"
        header Pragma "no-cache"
        header Expires "0"
        reverse_proxy 127.0.0.1:8320
    }

    @metering_ui path /metering /metering/*
    handle @metering_ui {
        basic_auth {
            user <bcrypt-hash>
        }
        header Cache-Control "no-cache"
        reverse_proxy 127.0.0.1:8320
    }

    @metered {
        method POST
        path /v1/chat/completions /v1/completions /v1/responses /v1/responses/compact /v1/messages
        path /backend-api/codex/responses /backend-api/codex/responses/compact
        path /v1/images/generations /v1/images/edits /v1/images/variations
        path /v1/embeddings
        path /v1/audio/speech /v1/audio/transcriptions /v1/audio/translations
        path /v1/videos /v1/videos/* /v1/videos/edits
        path /v1/models/*:generateContent /v1/models/*:streamGenerateContent
        path /v1beta/models/*:generateContent /v1beta/models/*:streamGenerateContent
        path /api/provider/*/chat/completions /api/provider/*/completions /api/provider/*/responses
        path /api/provider/*/images/generations /api/provider/*/images/edits /api/provider/*/images/variations
        path /api/provider/*/embeddings
        path /api/provider/*/v1/chat/completions /api/provider/*/v1/completions /api/provider/*/v1/responses /api/provider/*/v1/messages
        path /api/provider/*/v1/images/generations /api/provider/*/v1/images/edits /api/provider/*/v1/images/variations
        path /api/provider/*/v1/embeddings
        path /api/provider/*/v1/audio/speech /api/provider/*/v1/audio/transcriptions /api/provider/*/v1/audio/translations
        path /api/provider/*/v1/videos /api/provider/*/v1/videos/* /api/provider/*/v1/videos/edits
        path /api/provider/*/v1/models/*:generateContent /api/provider/*/v1/models/*:streamGenerateContent
        path /api/provider/*/v1beta/models/*:generateContent /api/provider/*/v1beta/models/*:streamGenerateContent
    }
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

    handle {
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
}
```

关键点：
- `/metering/api/*` 规则必须位于更宽泛的 `/metering/*` 之前，否则 API 会被错误地当作静态 UI 路径处理。
- 仅将计量目标 API 路径路由至 `:8320`，其余请求直通 `:8317`。
- 为 `/metering` 配置 Basic Auth 或等效访问控制。
- `/metering/api/*` 不应被浏览器、CDN 或反向代理缓存。

重载 Caddy：

```bash
caddy reload --config /path/to/Caddyfile
# Docker Caddy：
docker exec caddy caddy reload --config /etc/caddy/Caddyfile
```

### 9. 验证

```bash
docker ps -a --filter name=metering-proxy
docker logs metering-proxy
curl -fsS http://127.0.0.1:8320/healthz
curl -fsS http://127.0.0.1:8320/readyz
curl -s http://127.0.0.1:8320/metering/api/health
curl -s http://127.0.0.1:8320/metering/api/overview?range=24h
curl -s http://127.0.0.1:8320/metering/api/issues?range=24h
curl -s http://127.0.0.1:8320/metering/api/summary?range=24h
curl -s http://127.0.0.1:8320/metering/api/quota
curl -s http://127.0.0.1:8320/metering/api/observability
```

### 镜像标签说明

| 标签 | 说明 |
|---|---|
| `v0.5.0` | 固定版本，生产推荐 |
| `edge` | 追踪 main 分支最新提交 |
| `latest` | 指向最新发布版本 |

</details>

## 升级与回滚

<details>
<summary><b>展开升级步骤</b></summary>

v0.5.0 首次打开旧库时会增量创建 `request_usage(api_key_hash, created_at_unix)` 索引。索引创建时间取决于历史数据量，应预留维护窗口。发布物必须把镜像（或 binary）、`config.yaml` 与 `pricing.yaml` 作为同一版本 bundle 保存；operator 自定义配置应合并新字段后严格预检，不能盲目覆盖。

```bash
# 0. 升级前确认（关键）
# ---- 确认 ai-gateway 网络存在 ----
docker network inspect ai-gateway >/dev/null 2>&1 || docker network create ai-gateway

# ---- 确认 CLIProxyAPI 容器已加入 ai-gateway ----
CPA_CONTAINER=$(docker ps --format '{{.Names}}' | grep -i cli)
docker network inspect ai-gateway | grep -q "$CPA_CONTAINER" || docker network connect ai-gateway "$CPA_CONTAINER"

# ---- 确认 config.yaml 中 upstream 主机名与 CPA 容器名一致 ----
# 例如 CPA 容器名为 cli-proxy-api 时，upstream 应为 http://cli-proxy-api:8317
# grep 'upstream:' /opt/ai-gateway/metering/config.yaml

# 1. 停止，并把 DB/WAL/SHM、salt、config、pricing 备份到同一快照目录
backup_dir="/opt/ai-gateway/metering/backups/v0.4.2-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup_dir"
docker stop metering-proxy
cp /opt/ai-gateway/metering/usage.sqlite "$backup_dir/"
[ ! -e /opt/ai-gateway/metering/usage.sqlite-wal ] || cp /opt/ai-gateway/metering/usage.sqlite-wal "$backup_dir/"
[ ! -e /opt/ai-gateway/metering/usage.sqlite-shm ] || cp /opt/ai-gateway/metering/usage.sqlite-shm "$backup_dir/"
cp /opt/ai-gateway/metering/salt /opt/ai-gateway/metering/config.yaml /opt/ai-gateway/metering/pricing.yaml "$backup_dir/"

# 2. 用新镜像做只读预检（不会迁移 DB）
docker run --rm --network none -v /opt/ai-gateway/metering:/data:ro ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0 validate --config /data/config.yaml

# 3. 更新镜像并启动；首次启动会执行 additive migration/index
docker pull ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0
docker rm metering-proxy
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  --network ai-gateway \
  -v /opt/ai-gateway/metering:/data \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0 \
  serve --config /data/config.yaml

# 4. 验证本地就绪、迁移和上游透明转发
docker ps --filter name=metering-proxy
curl -fsS http://127.0.0.1:8320/healthz
curl -fsS http://127.0.0.1:8320/readyz
docker inspect metering-proxy --format '{{json .State.Health}}'
# ---- 确认能连通上游 ----
docker exec metering-proxy wget -qO- http://<cpa-container-name>:8317/v1/models
# 返回 401 {"error":"Missing API key"} 表示连通正常
```

数据库文件在宿主机上，升级不会删除历史数据。迁移只创建缺失表/列/索引并回填兼容字段，不删列、不改类型；但仍必须先在备份副本上演练并保留旧 bundle。

</details>

<details>
<summary><b>展开回滚步骤</b></summary>

只替换镜像不是完整回滚。`v0.4.2` 不理解 v0.5.0 的 `long_context` 与图片按张价格结构，因此必须同时恢复旧镜像、旧 `pricing.yaml` 和对应的旧配置；salt 必须保持与数据库一致。新增索引可被旧二进制安全忽略。

```bash
docker stop metering-proxy
docker rm metering-proxy
cp /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/pricing.yaml /opt/ai-gateway/metering/pricing.yaml
cp /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/config.yaml /opt/ai-gateway/metering/config.yaml
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  --network ai-gateway \
  -v /opt/ai-gateway/metering:/data \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.4.2 \
  --config /data/config.yaml
```

如果升级中断或数据库不可用，先停止服务再恢复同一时刻的完整快照。WAL 模式下不能只覆盖主库文件，也不能把不同快照的 DB、WAL/SHM 或 salt 混用：

```bash
docker stop metering-proxy
rm -f /opt/ai-gateway/metering/usage.sqlite-wal /opt/ai-gateway/metering/usage.sqlite-shm
cp /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/usage.sqlite /opt/ai-gateway/metering/usage.sqlite
[ ! -e /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/usage.sqlite-wal ] || cp /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/usage.sqlite-wal /opt/ai-gateway/metering/usage.sqlite-wal
[ ! -e /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/usage.sqlite-shm ] || cp /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/usage.sqlite-shm /opt/ai-gateway/metering/usage.sqlite-shm
cp /opt/ai-gateway/metering/backups/v0.4.2-YYYYMMDD-HHMMSS/salt /opt/ai-gateway/metering/salt
chown 1000:1000 /opt/ai-gateway/metering/usage.sqlite
chmod 600 /opt/ai-gateway/metering/usage.sqlite
# 然后用旧镜像 docker run
```

</details>

## 记录的字段

- 时间戳、接口路径、请求方法、状态码
- 请求总耗时及上游首字节延迟（`latency_ms`、`ttfb_ms`）
- 流式/非流式标志
- API Key 和客户端 IP 的 HMAC-SHA256 加盐哈希
- 请求模型与返回模型名称
- 输入、输出、缓存、推理及总 token 数量
- 多模态用量维度（如 image/text/audio channel、direction、unit、amount）
- 图片请求事实（operation、size、quality、format、图片数量、输入图片数量、mask 是否存在）
- 请求/响应字节数
- 错误分类及 provider type/code/param（如有；不落库 provider 原始 message）

**不会**存储提示词、响应正文、明文 API Key 或明文客户端 IP。

## 配置参考

服务默认执行 `serve --config config.yaml`。旧的无子命令 `--config config.yaml` 启动方式继续兼容；新部署建议显式使用 `serve`。`validate` 和 `hash-key` 不访问上游网络，也不会打开或修改数据库。

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `listen` | `127.0.0.1:8320` | 监听地址 |
| `upstream` | `http://127.0.0.1:8317` | CLIProxyAPI 地址 |
| `database` | — | SQLite 数据库文件路径 |
| `salt_file` | — | 用于哈希的固定盐值文件路径 |
| `queue_capacity` | `1000` | 计量事件队列最大容量，满后丢弃 |
| `batch_size` | `50` | 每次 SQLite 批量插入的最大记录数 |
| `flush_interval` | `1s` | 刷新批次的最大等待时间 |
| `max_nonstream_sample_bytes` | `2097152` (2 MiB) | 非流式响应采样窗口；图片、Responses、Gemini 使用首尾采样以保留尾部 usage |
| `metering_enabled` | `true` | 计量开关，设为 `false` 时仅透明转发 |
| `webui.enabled` | `true` | 启用仪表盘 |
| `webui.base_path` | `/metering` | 仪表盘路径前缀，勿设为 `/` |
| `pricing_file` | — | 成本估算 YAML 文件路径 |
| `key_labels` | `{}` | 完整 64 位小写 Key HMAC hash 到显示标签的映射；启动时加载，不热重载 |
| `request_metadata.initial_bytes` | `4096` | 请求体前缀初始扫描字节数 |
| `request_metadata.max_bytes` | `65536` | 扩展模型扫描最大字节数，上限 64 KiB |
| `request_metadata.extended_model_scan` | `false` | 是否启用扩展请求模型扫描 |
| `observability.correlation.mode` | `passive` | 请求相关性模式：`passive` 或 `inject_if_missing` |
| `observability.correlation.side_channel_merge` | `stored_only` | side-channel 用量合并模式；CPA 未传播 request id 时保持 `stored_only` |
| `cliproxy_management.enabled` | `false` | 启用 CLIProxyAPI Management API 只读联动 |
| `cliproxy_management.base_url` | `http://127.0.0.1:8317/v0/management` | CLIProxyAPI Management API 地址 |
| `cliproxy_management.key_file` | — | management key 文件；启用 management 时必填 |
| `cliproxy_management.usage_queue.enabled` | `false` | 消费 CPA usage queue，需 CPA 开启 `usage-statistics-enabled` |
| `cliproxy_management.usage_queue.transport` | `auto` | usage queue 消费方式：`auto` 优先 HTTP；`resp` 仅旧 CPA 兼容，v7.1.17 起不可用 |
| `cliproxy_management.usage_queue.merge_mode` | `stored_only` | 默认仅存 side event；request-id 传播验证后才可设为 `request_id` |
| `cliproxy_management.credential_health.enabled` | `true` | 从 `auth-files` 同步凭证健康 |
| `cliproxy_management.quota.enabled` | `false` | 完整 quota 快照开关；只有 provider adapter 产出受支持的 quota 行时才报告 `full_quota_available=true` |

## 按 Key 区分

“朋友共享中转”是一个典型场景，但 MeteringProxy 的对象始终是**客户端 API Key**，不是朋友账号、租户或权限主体。可以为朋友、设备、Agent 或用途在 CPA/外围网关分配独立 Key；Key 的签发、轮换与吊销仍由 CPA/外围网关负责，MeteringProxy 只保存不可逆 HMAC hash 并提供观测归因。

### 安全计算 hash 与配置标签

`hash-key` 只从 stdin 接收一行明文，不接受 `--key` 参数，避免明文出现在 shell history 或进程列表；stdout 只输出 64 位小写 hash。下面的交互式写法不会把 Key 写进命令行：

```bash
read -rsp "Client API Key: " metering_plain_key
printf '\n'
printf '%s\n' "$metering_plain_key" | docker run --rm -i --network none -v /opt/ai-gateway/metering:/data:ro ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0 hash-key --config /data/config.yaml
unset metering_plain_key
```

把输出 hash 加入 `config.yaml`：

```yaml
key_labels:
  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "friend-a"
  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789": "agent-lab"
```

hash 必须是完整 64 位小写十六进制；label 会 trim，最多 80 个 Unicode 字符且不能含控制字符。标签允许重复，便于 Key 轮换后保留同一用途名。修改后须受控重启；API、日志和 WebUI 都不会返回明文 Key。

### Key Detail 与完整性

Keys 表显示请求数、失败率、Token、当前价格估算、成本状态和最后活动时间。点击一行会在同页打开 Key Detail，继承当前 range，并下钻：

- 请求/失败、完整 Token 维度、平均与 P95 latency/TTFB、usage confidence；
- 请求量、Token 与成本趋势；
- 模型分布；
- 只属于该 Key 的 Issues 和最近请求。

`unknown` 表示请求没有可用客户端 Key hash，也可独立下钻。Models、Timeseries、Activity、Issues、Requests API 的 `key_hash` 只接受完整 hash 或 `unknown`，不支持 prefix。Key-scoped Issues 只查询 `request_usage`，不会混入全局 credential、quota、side-channel 或 process 项。

`cost_state` 取值为 `complete`、`partial` 或 `unavailable`。`partial` 仍返回已知小计，但会通过 `partial_reasons` 明确说明 `unpriced_model`、`missing_usage`、`request_only`、`unsupported`、`usage_conflict`、`image_count_missing`、`image_size_defaulted` 或 `cost_query_failed`；UI 不会把 partial/unavailable 冒充为可信的 `$0.00`。`cost_known` 仅作为兼容字段保留。

## WebUI

仪表盘默认访问 `https://<域名>/metering`，受外层反向代理的 Basic Auth 保护。

<details>
<summary><b>展开 API 参考</b></summary>

| 端点 | 说明 |
|---|---|
| `GET /metering/api/overview?range=24h\|today\|7d\|30d` | 首屏总览、最近 1h、采集健康、成本完整性 |
| `GET /metering/api/issues?range=...&limit=20&key_hash=` | 全局问题聚合；带 Key 时仅返回该 Key 的 `request_usage` 问题 |
| `GET /metering/api/summary?range=24h\|today\|7d\|30d` | 用量摘要 |
| `GET /metering/api/timeseries?range=...&bucket=10m\|1h\|1d&key_hash=` | 时序数据（请求数、token、延迟、成本），可精确 Key scope |
| `GET /metering/api/activity?range=...&key_hash=` | 成功率、P95 延迟、capture 健康，可精确 Key scope |
| `GET /metering/api/models?range=...&key_hash=` | 按模型维度聚合，可精确 Key scope |
| `GET /metering/api/keys?range=...` | 按客户端 API Key 聚合成本、完整性、质量与最后活动 |
| `GET /metering/api/requests?range=...&limit=100&key_hash=&status=&model=&endpoint=&error_class=` | 最近请求明细及组合过滤 |
| `GET /metering/api/multimodal/summary?range=...` | 多模态用量维度汇总 |
| `GET /metering/api/images/summary?range=...` | 图片请求、图片数、token 与成本汇总 |
| `GET /metering/api/images/models?range=...` | 图片按模型与操作聚合 |
| `GET /metering/api/images/requests?range=...&limit=100` | 最近图片请求明细 |
| `GET /metering/api/errors?range=...&nonzero=true` | 非零错误 bucket |
| `GET /metering/api/quota` | 凭证健康与 quota 状态 |
| `GET /metering/api/quota/diagnostics` | 凭证健康、quota 探测和刷新事件诊断 |
| `POST /metering/api/quota/refresh` | 触发后台 quota/凭证状态刷新 |
| `GET /metering/api/observability` | side-channel、相关性与配额观测状态 |
| `GET /metering/api/health` | 队列深度、丢弃事件、解析/写入错误、计量开关 |
| `GET /metering/api/metadata` | profile 列表、time range、bucket 等前端元数据 |
| `GET /healthz` | 进程/HTTP liveness，不访问 DB、pricing 或外部服务 |
| `GET /readyz` | 本地 config、salt、pricing、SQLite read/write handle 与 writer readiness |
| `GET /metrics` | Prometheus 文本格式，含固定低基数 report query/error/duration 指标 |

健康状态计数器为进程生命期计数器，容器重启后从零开始。

Quota API 的 `full_quota_available=true` 仅表示后台已拿到受支持的 provider quota 行；如果 CPA `/api-call` 不可用、adapter 未验证或只存在凭证健康数据，API 会返回 `phase=credential_health`，并通过 `module_status=partial|unavailable|unsupported|disabled` 表达降级状态。`/metering/api/quota/diagnostics` 会附带最近 quota refresh 诊断、探测 HTTP 状态和 API call 可达性，`/metering/api/observability` 会暴露最近一次 quota 事件与最近错误，便于区分 `api_call_bad_request`、`api_call_unavailable`、`quota_unsupported` 和 provider adapter 问题。一个凭证可以有多条 quota 行，例如订阅常见的 `5h` 窗口和 `weekly` 周限额；WebUI 会按凭证聚合这些窗口，优先展示短窗口压力，再展示周限额和重置时间。

请求错误展示优先使用 `error_class` / `error_code`，HTTP status 只作为传输层事实保留。例如代理连接失败会细分为 `proxy_connection_refused`、`proxy_timeout`、`proxy_dns_error` 等，而不是只在 UI 中显示 `502`。代理自身产生的上游传输错误会返回安全的 `X-Metering-Proxy-Error-Type/Class/Code` 响应头；超时会使用 `504 Gateway Timeout`，其他连接类错误仍保持网关错误语义。

WebUI 为只读面板，不修改配置或数据库。页面使用 Tabs 模式，默认展示请求总览、成本/Token/请求趋势、模型分布、图片计量、API Key 维度、近期问题、凭证健康、额度/凭证刷新诊断与采集诊断。Keys 行可打开内联 Key Detail；自动刷新和 range 变化会保留选择，Key 在新 range 无数据时显示明确空状态。最近 100 条请求明细默认隐藏，仅在点击展开或从 issue 卡片进入时查询。WebUI 支持中英文切换、明暗主题和响应式窄屏布局，语言偏好保存在浏览器本地；页面右上角提供项目 GitHub 链接。

后端为 `/metering/api/*` 设置不可缓存响应头。若页面顶部显示 `Partial` 或 `Error`，说明至少一个 API 请求失败，其余面板仍会继续展示。排查时优先检查反向代理是否缓存了 `/metering/api/*`，以及 Basic Auth 凭据是否正确传递。

</details>

## 定价逻辑

价格表仅为估算，不具备计费效力。报表不会把价格冻结写入每条请求；**当前进程启动时加载的 `pricing.yaml` 会重新估算全部历史已观测 usage**。因此它适合运营分析，不是不可变账单、支付或结算依据。模型匹配链路：**精确匹配 → 显式别名 → 规范化（去除日期后缀）→ unknown**。

在 `pricing.yaml` 中通过 `aliases` 字段定义别名：

```yaml
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
    aliases:
      - gpt-4o-latest
```

推理 token 被视为输出 token 的子集：
- 若配置了 `reasoning_per_1m`：常规输出 = max(总输出 - 推理, 0)，推理单独计费。
- 若未配置 `reasoning_per_1m`：全部输出按 `output_per_1m` 计费，推理不重复计费。

Anthropic cache creation token 优先按 `cache_creation_per_1m` 计费；未配置时回退为普通输入价格。缓存读取 token 按 `cached_input_per_1m` 计费。

`long_context` 是通用可选结构，按**单次请求的 input token** 判断档位；两个各自低于阈值的请求不会因为聚合后越过阈值而被错算成长上下文。只有阈值已经确认的模型才应配置该结构；默认文件中的 Grok 4.5 固定使用项目所有者给定的 flat 价 `input 2.00 / cached 0.30 / output 6.00`（每 1M token），不配置 long-context 阈值。Gemini 3.1 Pro Preview（200k）仅作为 **generic long-context 定价/档位测试样例** 出现在 `pricing.yaml` 中；这条价格配置不会选择 MeteringProxy 的运行模型或开发施工模型。

图片模型可同时按 token、输入图片张数和按 1K/2K 尺寸的输出张数计价。缺失 size 时使用 `default` 价格并标记 `image_size_defaulted`；缺失输出张数时不猜测，标记 `image_count_missing`。图片 request 命中 multimodal pricing 后不会再把同一份文本 summary 重复计费。

价格表只在进程启动时严格加载，修改后须先执行 `validate` 再受控重启服务。

## 备份

<details>
<summary><b>展开备份命令</b></summary>

```bash
backup_file="./backup-$(date +%Y%m%d-%H%M%S).sqlite"
docker exec metering-proxy sqlite3 /data/usage.sqlite ".backup /tmp/backup.sqlite"
docker exec metering-proxy sqlite3 /tmp/backup.sqlite "PRAGMA integrity_check" | grep -qx ok
docker cp metering-proxy:/tmp/backup.sqlite "$backup_file"
gzip "$backup_file"
```

</details>

## 运维

### 日常检查

```bash
# 容器状态
docker ps --filter name=metering-proxy
docker ps --filter name=cli-proxy-api

# 日志
docker logs metering-proxy --tail 50
docker logs metering-proxy -f        # 实时跟踪

# liveness / readiness
curl -fsS http://127.0.0.1:8320/healthz
curl -fsS http://127.0.0.1:8320/readyz

# WebUI 采集健康与 Prometheus
curl -s http://127.0.0.1:8320/metering/api/health
curl -s http://127.0.0.1:8320/metrics | head -30

# 数据库文件
ls -lh /opt/ai-gateway/metering/usage.sqlite*
```

`/healthz` 只证明进程和 HTTP server 存活；`/readyz` 只检查已加载的 config/salt/pricing、SQLite 两个 handle 和 async writer。两者都不会探测 upstream、provider、CPA management 或 quota，避免外部抖动触发编排器反复重启。响应不包含 DB/salt 路径、management URL 或凭据。

### 容器网络排错

```bash
# 查看容器在哪些 Docker 网络上
docker inspect metering-proxy --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}'
docker inspect cli-proxy-api   --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}'

# 两容器不在同一网络 → DNS 无法互相解析，502 proxy_dns_error
# 修复：把它们加入同一网络
docker network connect <network-name> <container-name>

# 验证容器内 DNS 解析
docker exec metering-proxy sh -c "nslookup cli-proxy-api 2>/dev/null || cat /etc/hosts"

# 查看容器 DNS 配置
docker exec metering-proxy cat /etc/resolv.conf

# 查看所有 Docker 网络
docker network ls

# 查看某网络的成员
docker network inspect ai-gateway --format '{{range .Containers}}{{.Name}} {{end}}'

# 测试容器到上游的连通性
docker exec metering-proxy wget -qO- http://cli-proxy-api:8317/v1/models
# 返回 401 {"error":"Missing API key"} 即表示连通正常
```

### 服务重启

```bash
# MeteringProxy
docker restart metering-proxy

# CLIProxyAPI
docker restart cli-proxy-api

# Caddy 热重载
docker exec caddy caddy reload --config /etc/caddy/Caddyfile

# systemd 服务重启（非容器场景）
systemctl restart sing-box
systemctl status sing-box
journalctl -u sing-box --since "5 min ago" --no-pager
```

### 快速诊断 502

502 响应头中有诊断信息：

| 响应头 | 含义 |
|---|---|
| `x-metering-proxy-error-class: proxy_dns_error` | MeteringProxy 无法解析上游域名，查容器网络 |
| `x-metering-proxy-error-class: proxy_connection_refused` | 上游端口未监听或防火墙拒绝 |
| `x-metering-proxy-error-class: proxy_timeout` | 上游连接超时，返回 504 |
| `x-metering-proxy-error-class: proxy_upstream_error` | 其他上游传输错误 |

没有 `x-metering-proxy` 头 = 问题在 Caddy 或 CPA 自身。

### 上线后关注指标

- 队列深度通常应回归为零
- 正常负载下丢弃事件和数据库写入错误应保持为零
- 解析错误应较低且可解释
- token 总量应与服务商侧采样数据在预期误差范围内一致
- `metering_proxy_report_queries_total{report="..."}` 与 `metering_proxy_report_query_errors_total{report="..."}` 应按固定 report 枚举增长；不会出现 Key、model、endpoint 等高基数 label
- 使用 `metering_proxy_report_query_duration_ms_sum / _count` 计算各报表平均耗时，重点观察大 range 的全局 Keys 聚合

## 附录

<details>
<summary><b>附录 A：CPA 在宿主机裸跑</b></summary>

如果你的 CLIProxyAPI 不在 Docker 中而是直接跑在宿主机上，容器内需能访问宿主机的 `8317` 端口：

**方案一：使用 `--network host`（最简单）**

```bash
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  --network host \
  -v /opt/ai-gateway/metering:/data \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0 \
  serve --config /data/config.yaml
```

此时 `config.yaml` 中 upstream 用 `127.0.0.1:8317`。

**方案二：桥接网络 + `host-gateway`（Docker 20.10+）**

```bash
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  -v /opt/ai-gateway/metering:/data \
  --add-host host.docker.internal:host-gateway \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.5.0 \
  serve --config /data/config.yaml
```

此时 `config.yaml` 中 upstream 用 `http://host.docker.internal:8317`。`host-gateway` 会自动解析到 Docker 网桥网关 IP。

> **注意**：如果 CPA 也是 Docker 容器（常见情况），不要用 `host.docker.internal`，直接用 Docker 服务名（如 `http://cli-proxy-api:8317`）并确保两容器在同一网络。`host.docker.internal` 仅用于 CPA 直接在宿主机上跑的场景。

</details>

<details>
<summary><b>生产就绪检查清单</b></summary>

- [ ] Docker 容器正常运行且配置了 `--restart unless-stopped`
- [ ] `go test ./...`、`go test -race ./...` 和 `go vet ./...` 通过
- [ ] Docker 镜像可构建：`docker build -t metering-proxy:local .`
- [ ] 部署前对目标 `config.yaml` / `pricing.yaml` / salt（及启用时的 management key）执行 `validate --config ...`，stdout 为 `ok`
- [ ] 盐值文件存在，owner 为 `1000:1000`，权限 `0600`，已备份
- [ ] 宿主机运行时目录权限 `0700`、owner `1000:1000`
- [ ] `config.yaml` 容器内路径均使用 `/data/` 前缀
- [ ] 默认镜像 healthcheck 指向 `http://127.0.0.1:8320/healthz`；非 8320 监听时已设置 `METERING_PROXY_HEALTH_URL`
- [ ] `GET /healthz` 返回 200（不访问 DB/upstream）；`GET /readyz` 返回 200（本地 config/salt/pricing/SQLite/writer）
- [ ] `pricing.yaml` 与实际服务商定价一致；理解报表成本是**当前启动价重估历史 usage**，不是冻结账单
- [ ] 可选 `key_labels` 仅使用完整 64 位小写 hash，从不写入明文 Key
- [ ] Caddy（或等价层）已用 Basic Auth 等访问控制保护 `/metering` 与 `/metering/api/*`
- [ ] `/metering/api/*` 返回正确的 `Cache-Control` 头，且未被浏览器/CDN/反向代理缓存
- [ ] Caddy 仅将目标 API 路径路由至 MeteringProxy
- [ ] 部署流程为 stop-then-start，不并发运行两个实例写同一个 SQLite 文件
- [ ] 备份已手动执行一次并通过 `PRAGMA integrity_check`
- [ ] 数据库恢复演练确认会先删除旧 WAL/SHM 文件
- [ ] 回滚所需的**旧镜像 + 旧 config + 旧 pricing + DB/salt 快照**已作为同一版本 bundle 准备就绪
- [ ] 小流量 live smoke 覆盖至少两把测试 Key 的 Keys / Key Detail / Issues / Requests，并与服务商侧用量对比

</details>

<details>
<summary><b>仓库结构</b></summary>

```text
main.go                                  入口
cli.go / serve.go                        serve / validate / hash-key CLI
config.yaml                              示例配置文件
pricing.yaml                             默认定价表（也作为 Release 附件）
Dockerfile                               多阶段 Docker 构建与 /healthz healthcheck
.github/workflows/build.yml              CI：测试、构建、镜像推送、发布
internal/config                          YAML 配置默认值与严格校验
internal/db                              SQLite 表结构、迁移、查询与 demo seed
internal/event                           写侧领域事件、常量与映射
internal/extractor                       从 JSON 和 SSE 中提取用量信息
internal/hash                            API Key 和 IP 的 HMAC-SHA256 加盐哈希
internal/metrics                         Prometheus 指标（含低基数 report 指标）
internal/pricing                         成本估算、alias、long_context 与图片计价
internal/profile                         Endpoint Profile 注册与匹配
internal/proxy                           反向代理与用量采集（热路径）
internal/report                          读侧报表服务、CostState 与 Key 聚合
internal/store                           写侧 EventSink / HealthWriter 等接口边界
internal/streamproto                     流协议抽象（SSE 等）
internal/webui                           仪表盘 API 及内嵌静态界面
internal/writer                          异步批量写入与计数器
testdata/                                测试 fixture 与 golden 文件
scripts/backup.sh                        SQLite 备份脚本
systemd/ai-gateway-metering-proxy.service systemd 单元示例
docs/v0.5.0-release-notes.md             发布说明（annotated tag 消息来源）
docs/v0.5.0-release-validation.md        本地 RC、性能、隐私、WebUI 与 Docker 验证证据
```

</details>

## 本地 WebUI 开发

开发 WebUI 时无需每次 Docker 部署，通过 `--dev-static` 直接从磁盘加载静态文件，改完刷新浏览器即可看到变化：

```bash
# 首次（带演示数据）：
go run . --config config.dev.yaml --dev-static --seed-demo

# 后续（数据库已有数据）：
go run . --config config.dev.yaml --dev-static
```

- `--dev-static`：静态文件从 `internal/webui/static/` 磁盘路径加载，非内嵌文件系统。
- `--seed-demo`：向 `*.dev.sqlite` 插入约 220 条合成请求记录，并写入演示用凭证健康、`5h`/`weekly` 额度快照和 quota refresh 诊断行。要求同时传 `--dev-static`，数据库路径必须是相对路径且以 `.dev.sqlite` 结尾；绝对路径与生产库文件名会被拒绝。
- Demo Key hash 均为合法 64 位小写十六进制的**合成值**，不来自真实 Key。seed 覆盖两把带标签 Key（`friend-a`、`agent-lab`）、一把未标签 Key、`unknown`、一把 partial Key，以及 long-context（Gemini 3.1 Pro 档位测试）、1K/2K 图片、可下钻 Issues/Requests。
- Demo 标签仅在进程启动时与配置 `key_labels` 合并进内存，**不会**写入 SQLite；关闭 `--seed-demo` 后生产行为完全不变。
- 当 `--seed-demo` 且未启用真实 `cliproxy_management` 时，WebUI 会用这些演示快照展示凭证健康与额度状态。
- `config.dev.yaml`：本地开发配置，监听 `127.0.0.1:8320`，使用本地 `salt`、`pricing.yaml` 及 `usage.dev.sqlite`。已加入 `.gitignore`。
- 两个 flag 默认均为 `false`，不传时生产行为完全不变。

## 构建

CI 通过 GitHub Actions 自动完成测试、构建和镜像推送。本地构建：

```bash
# 本地开发（Windows）
go test ./...
go vet ./...
go build -o ai-gateway-metering-proxy.exe .

# 本地构建 Docker 镜像
docker build -t ai-gateway-metering-proxy .
```

## 数据兼容性

`db.Open()` 在启动时执行增量迁移——创建缺失的表和索引、为旧版数据库添加缺失列、回填 Unix 时间戳、规范化负数字节数为零，通过 `PRAGMA user_version` 跟踪版本。历史记录仍可查询，但旧版解析器缺陷导致的未采集数据无法恢复。

v0.5.0 相对 `v0.4.2` 的 schema 变化是 additive 的：首次打开旧库时会创建 `request_usage(api_key_hash, created_at_unix)` 组合索引以服务精确 Key 查询。迁移不删列、不改类型、不重写历史 usage；成本仍只在读侧按**当前进程加载的 pricing** 重估，不会把价格冻结写入历史请求。旧二进制可以忽略新索引并继续打开数据库。完整的迁移体系设计（含三层递进迁移、盐值管理、破坏性变更处理）见 [数据迁移体系设计案](docs/data-migration-design.md)。

## 已知局限

- 异步队列满时计量事件按设计被丢弃。
- 非流式用量提取仅采样有界响应前缀。
- SSE 行长超过内部解析缓冲区时，按原样转发但跳过解析。
- 客户端若协商压缩 SSE，代理保持头透明并标记 `capture_reason=compressed_stream_not_metered`，不解析 usage。
- 报表成本始终是当前启动价对历史已观测 usage 的重估，不是不可变账单。
- 在文档化的百万行 fixture 上，两次完整实测中精确 Key 的 30d 明细报告最慢约 70 ms；**全局 Keys 列表 30d 约 14.1–14.5 s**，属于有意记录的剩余瓶颈，不是可接受的交互延迟目标。详见 [Key 查询性能](docs/v0.5.0-key-query-performance.md)。
- SQLite 适用于单节点计量；本项目并非分布式分析存储。
- WebUI 为只读，仍须由外层 Caddy Basic Auth 或等价访问控制保护。

## 许可证

本项目采用 [MIT License](LICENSE)。CLIProxyAPI 同为 MIT 协议，两者兼容，可自由使用、修改和分发。
