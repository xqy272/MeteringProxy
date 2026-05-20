# MeteringProxy

MeteringProxy 是一个面向 AI 网关流量的透明计量代理，部署在 Caddy 与 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 之间。它在不修改请求或响应字节的前提下转发 LLM API 调用，同时将用量统计异步写入 SQLite，通过只读 WebUI 提供实时可见性。

## 为什么选择 MeteringProxy

- **透明转发，零侵入**：方法、路径、请求头、请求体、状态码及 SSE 字节流完全逐字透传，对客户端和上游服务商完全透明。
- **安全优先**：不存储提示词、响应正文、明文 API Key 或明文客户端 IP。哈希使用固定加盐，支持按 Key/IP 分组的匿名统计。
- **异步计量，不阻塞流量**：计量写入在独立队列中批量异步完成。队列满时丢弃事件而非阻塞 LLM 请求，转发可靠性始终高于计量完整性。
- **轻量单实例**：SQLite WAL 模式 + 单写入连接，无需 Redis、PostgreSQL 等外部依赖。Docker 一键部署，运维简单。
- **实时成本可见**：内置支持主流模型定价，按输入/缓存/输出/推理 token 分类估算成本，WebUI 提供多维度趋势和占比图表。
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

**pricing.yaml**（请与实际服务商合同定价对齐）

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
```

### 6. 拉取并启动容器

```bash
docker pull ghcr.io/xqy272/ai-gateway-metering-proxy:v0.3.1

docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  --network ai-gateway \
  -v /opt/ai-gateway/metering:/data \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.3.1 \
  -config /data/config.yaml
```

### 7. 配置 Caddy

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
        path /v1/models/*:generateContent /v1/models/*:streamGenerateContent
        path /v1beta/models/*:generateContent /v1beta/models/*:streamGenerateContent
        path /api/provider/*/chat/completions /api/provider/*/completions /api/provider/*/responses
        path /api/provider/*/v1/chat/completions /api/provider/*/v1/completions /api/provider/*/v1/responses /api/provider/*/v1/messages
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

### 8. 验证

```bash
docker ps -a --filter name=metering-proxy
docker logs metering-proxy
curl -s http://127.0.0.1:8320/metering/api/health
curl -s http://127.0.0.1:8320/metering/api/summary?range=24h
curl -s http://127.0.0.1:8320/metering/api/quota
curl -s http://127.0.0.1:8320/metering/api/observability
```

### 镜像标签说明

| 标签 | 说明 |
|---|---|
| `v0.3.1` | 固定版本，生产推荐 |
| `edge` | 追踪 main 分支最新提交 |
| `latest` | 指向最新发布版本 |

</details>

## 升级与回滚

<details>
<summary><b>展开升级步骤</b></summary>

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

# 1. 停止 + 备份
docker stop metering-proxy
cp /opt/ai-gateway/metering/usage.sqlite /opt/ai-gateway/metering/backups/usage.sqlite.$(date +%Y%m%d-%H%M%S).bak
cp /opt/ai-gateway/metering/salt       /opt/ai-gateway/metering/backups/salt.$(date +%Y%m%d-%H%M%S).bak

# 2. 更新镜像
docker pull ghcr.io/xqy272/ai-gateway-metering-proxy:v0.3.1
docker rm metering-proxy
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  --network ai-gateway \
  -v /opt/ai-gateway/metering:/data \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.3.1 \
  -config /data/config.yaml

# 3. 验证
docker ps --filter name=metering-proxy
docker exec metering-proxy sqlite3 /data/usage.sqlite "PRAGMA user_version;"
# ---- 确认能连通上游 ----
docker exec metering-proxy wget -qO- http://<cpa-container-name>:8317/v1/models
# 返回 401 {"error":"Missing API key"} 表示连通正常
```

数据库文件在宿主机上，升级不会丢失历史数据。迁移在容器启动时自动执行（仅增量 ALTER TABLE ADD COLUMN，不删列不改类型）。

</details>

<details>
<summary><b>展开回滚步骤</b></summary>

使用旧版本镜像即可（DB 迁移是纯增量的，旧版本不受新增列影响）：

```bash
docker stop metering-proxy
docker rm metering-proxy
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  --network ai-gateway \
  -v /opt/ai-gateway/metering:/data \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.1.1 \
  -config /data/config.yaml
```

如果迁移导致数据库不可用，先停止服务再恢复备份。WAL 模式下不能只覆盖主库文件：

```bash
docker stop metering-proxy
rm -f /opt/ai-gateway/metering/usage.sqlite-wal /opt/ai-gateway/metering/usage.sqlite-shm
cp /opt/ai-gateway/metering/backups/usage.sqlite.YYYYMMDD-HHMMSS.bak /opt/ai-gateway/metering/usage.sqlite
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
- 请求/响应字节数
- 错误分类及 provider type/code/param（如有；不落库 provider 原始 message）

**不会**存储提示词、响应正文、明文 API Key 或明文客户端 IP。

## 配置参考

服务默认读取 `config.yaml`，也可通过 `-config` 参数指定路径。

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `listen` | `127.0.0.1:8320` | 监听地址 |
| `upstream` | `http://127.0.0.1:8317` | CLIProxyAPI 地址 |
| `database` | — | SQLite 数据库文件路径 |
| `salt_file` | — | 用于哈希的固定盐值文件路径 |
| `queue_capacity` | `1000` | 计量事件队列最大容量，满后丢弃 |
| `batch_size` | `50` | 每次 SQLite 批量插入的最大记录数 |
| `flush_interval` | `1s` | 刷新批次的最大等待时间 |
| `max_nonstream_sample_bytes` | `2097152` (2 MiB) | 非流式响应采样前缀字节数 |
| `metering_enabled` | `true` | 计量开关，设为 `false` 时仅透明转发 |
| `webui.enabled` | `true` | 启用仪表盘 |
| `webui.base_path` | `/metering` | 仪表盘路径前缀，勿设为 `/` |
| `pricing_file` | — | 成本估算 YAML 文件路径 |
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
| `cliproxy_management.quota.enabled` | `false` | 完整 quota 快照；无 provider-specific adapter 时保持关闭 |

## WebUI

仪表盘默认访问 `https://<域名>/metering`，受外层反向代理的 Basic Auth 保护。

<details>
<summary><b>展开 API 参考</b></summary>

| 端点 | 说明 |
|---|---|
| `GET /metering/api/summary?range=24h\|today\|7d\|30d` | 用量摘要 |
| `GET /metering/api/timeseries?range=...&bucket=10m\|1h\|1d` | 时序数据（请求数、token、延迟） |
| `GET /metering/api/activity?range=...` | 成功率、P95 延迟、capture 健康 |
| `GET /metering/api/models?range=...` | 按模型维度聚合 |
| `GET /metering/api/keys?range=...` | 按 API Key 维度聚合 |
| `GET /metering/api/requests?range=...&limit=100&status=...&model=&endpoint=` | 最近请求明细 |
| `GET /metering/api/errors?range=...&nonzero=true` | 非零错误 bucket |
| `GET /metering/api/quota` | 凭证健康与 quota 状态 |
| `POST /metering/api/quota/refresh` | 触发后台 quota/凭证状态刷新 |
| `GET /metering/api/observability` | side-channel、相关性与配额观测状态 |
| `GET /metering/api/health` | 队列深度、丢弃事件、解析/写入错误、计量开关 |
| `GET /metering/api/metadata` | profile 列表、time range、bucket 等前端元数据 |
| `GET /metrics` | Prometheus 文本格式 |

健康状态计数器为进程生命期计数器，容器重启后从零开始。

WebUI 为只读面板，不修改配置或数据库。页面默认展示请求总览、token 堆叠趋势、请求趋势、模型成本占比、API Key 维度、请求健康摘要。最近 100 条请求明细默认隐藏，仅在点击展开后查询。WebUI 支持中英文切换，语言偏好保存在浏览器本地；页面右上角提供项目 GitHub 链接。

后端为 `/metering/api/*` 设置不可缓存响应头。若页面顶部显示 `Partial` 或 `Error`，说明至少一个 API 请求失败，其余面板仍会继续展示。排查时优先检查反向代理是否缓存了 `/metering/api/*`，以及 Basic Auth 凭据是否正确传递。

</details>

## 定价逻辑

价格表仅为估算，不具备计费效力。模型匹配链路：**精确匹配 → 显式别名 → 规范化（去除日期后缀）→ unknown**。

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

价格表只在进程启动时加载，修改后须重启服务。

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

常用检查命令：

```bash
docker logs metering-proxy
docker exec metering-proxy wget -qO- http://127.0.0.1:8320/metering/api/health
ls -lh /opt/ai-gateway/metering/usage.sqlite*
```

上线后关注指标：

- 队列深度通常应回归为零
- 正常负载下丢弃事件和数据库写入错误应保持为零
- 解析错误应较低且可解释
- token 总量应与服务商侧采样数据在预期误差范围内一致

## 附录

<details>
<summary><b>附录 A：裸机 CLIProxyAPI</b></summary>

如果你的 CLIProxyAPI 不在 Docker 中而是直接跑在宿主机上：

**config.yaml：**

```yaml
upstream: "http://host.docker.internal:8317"
```

**docker run：**

```bash
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  -v /opt/ai-gateway/metering:/data \
  --add-host host.docker.internal:host-gateway \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xqy272/ai-gateway-metering-proxy:v0.3.1 \
  -config /data/config.yaml
```

`--add-host host.docker.internal:host-gateway` 在容器 `/etc/hosts` 中添加一条记录，将 `host.docker.internal` 解析到宿主机 IP。

</details>

<details>
<summary><b>生产就绪检查清单</b></summary>

- [ ] Docker 容器正常运行且配置了 `--restart unless-stopped`
- [ ] `go test ./...`、`go test -race ./...` 和 `go vet ./...` 通过
- [ ] Docker 镜像可构建：`docker build -t metering-proxy:local .`
- [ ] 盐值文件存在，owner 为 `1000:1000`，权限 `0600`，已备份
- [ ] 宿主机运行时目录权限 `0700`、owner `1000:1000`
- [ ] `config.yaml` 容器内路径均使用 `/data/` 前缀
- [ ] `pricing.yaml` 与实际服务商定价一致
- [ ] Caddy 已保护 `/metering` 路径，`/metering/api/*` 返回正确的 `Cache-Control` 头
- [ ] Caddy 仅将目标 API 路径路由至 MeteringProxy
- [ ] 部署流程为 stop-then-start，不并发运行两个实例写同一个 SQLite 文件
- [ ] 备份已手动执行一次并通过 `PRAGMA integrity_check`
- [ ] 数据库恢复演练确认会先删除旧 WAL/SHM 文件
- [ ] 回滚所需的旧镜像 tag、SQLite 备份和 Caddy 配置变更已准备就绪
- [ ] 已通过小流量请求与服务商侧用量进行对比验证

</details>

<details>
<summary><b>仓库结构</b></summary>

```text
main.go                                  入口
config.yaml                              示例配置文件
pricing.yaml                             示例模型定价表
Dockerfile                               多阶段 Docker 构建
.github/workflows/build.yml              CI：测试、构建、镜像推送、发布
internal/config                          YAML 配置默认值与校验
internal/db                              SQLite 表结构、迁移、查询
internal/event                           领域事件模型与报告类型
internal/extractor                       从 JSON 和 SSE 中提取用量信息
internal/hash                            API Key 和 IP 的加盐哈希
internal/metrics                         Prometheus 指标暴露
internal/pricing                         成本估算与模型别名
internal/profile                         Endpoint Profile 注册与匹配
internal/proxy                           反向代理与用量采集
internal/store                           写入/查询接口边界
internal/streamproto                     流协议抽象（SSE 等）
internal/webui                           仪表盘 API 及内嵌静态界面
internal/writer                          异步批量写入与计数器
testdata/                                测试 fixture 与 golden 文件
scripts/backup.sh                        SQLite 备份脚本
systemd/ai-gateway-metering-proxy.service systemd 单元示例
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
- `--seed-demo`：向数据库插入约 220 条模拟记录。要求同时传 `--dev-static`，且数据库文件名必须以 `.dev.sqlite` 结尾、不可用绝对路径。
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

`db.Open()` 在启动时执行增量迁移——创建缺失的表和索引、为旧版数据库添加缺失列、回填 Unix 时间戳、规范化负数字节数为零，通过 `PRAGMA user_version` 跟踪版本。历史记录仍可查询，但旧版解析器缺陷导致的未采集数据无法恢复。完整的迁移体系设计（含三层递进迁移、盐值管理、破坏性变更处理）见 [数据迁移体系设计案](docs/data-migration-design.md)。

## 已知局限

- 异步队列满时计量事件按设计被丢弃。
- 非流式用量提取仅采样有界响应前缀。
- SSE 行长超过内部解析缓冲区时，按原样转发但跳过解析。
- SQLite 适用于单节点计量；本项目并非分布式分析存储。
- WebUI 为只读，仍须由外层反向代理保护。

## 许可证

本项目采用 [MIT License](LICENSE)。CLIProxyAPI 同为 MIT 协议，两者兼容，可自由使用、修改和分发。
