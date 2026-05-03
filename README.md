# MeteringProxy

MeteringProxy 是一个面向 AI 网关流量的透明计量代理。它部署在 Caddy 与 CLIProxyAPI 之间，在不变更请求或响应字节的前提下转发 LLM 请求，同时将用量统计数据异步记录到 SQLite 中，供只读仪表盘查询。

首要目标是提供运维可见性，同时不将 LLM 请求链路置于风险之中。如果计量系统过载或数据库暂时不可用，请求转发应继续执行，计量事件允许被丢弃。

## 架构

```text
客户端 -> Caddy -> MeteringProxy (:8320) -> CLIProxyAPI (:8317) -> 上游模型服务商
```

建议的 Caddy 路由策略：

- 仅将 `POST /v1/chat/completions` 和 `POST /v1/responses` 路由至 MeteringProxy。
- 将非计量的管理与模型接口直接路由至 CLIProxyAPI。
- 使用 Basic Auth 或等效的访问控制层保护 `/metering` 路径。
- 如有需要，在 Caddy 侧配置请求体大小限制。Go 代理层有意不对请求体进行截断，以保持透明代理的行为不发生变化。

## 部署（推荐：Docker）

生产环境推荐使用 Docker 部署，通过 GitHub Container Registry 获取预构建镜像。

### 1. 前提条件

- 已安装 Docker 的 Linux 服务器
- Caddy（或其他反向代理）已部署在服务器上
- CLIProxyAPI 已在 `127.0.0.1:8317` 运行

### 2. 准备主机目录

```bash
mkdir -p /opt/ai-gateway/metering/backups
chown 1000:1000 /opt/ai-gateway/metering   # 与容器内 appuser 的 uid:gid 对应
```

### 3. 生成盐值（只需一次）

```bash
python3 -c "import secrets; print(secrets.token_hex(32))" > /opt/ai-gateway/metering/salt
chmod 600 /opt/ai-gateway/metering/salt
```

### 4. 创建配置文件

在 `/opt/ai-gateway/metering/` 下创建两个文件：

**config.yaml** — 容器内路径全部指向 `/data`：

```yaml
listen: "0.0.0.0:8320"
upstream: "http://host.docker.internal:8317"
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
```

**pricing.yaml** — 参考定价表（请与服务商实际合同定价对齐）：

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
    output_per_1m: 15.00
    reasoning_per_1m: 3.00
  claude-opus-4-7:
    input_per_1m: 15.00
    cached_input_per_1m: 1.50
    output_per_1m: 75.00
    reasoning_per_1m: 15.00
  claude-haiku-4-5:
    input_per_1m: 1.00
    cached_input_per_1m: 0.10
    output_per_1m: 5.00
    reasoning_per_1m: 1.00
  # DeepSeek
  deepseek-v4-flash:
    input_per_1m: 1.00
    cached_input_per_1m: 0.02
    output_per_1m: 2.00
  deepseek-v4-pro:
    input_per_1m: 3.00
    cached_input_per_1m: 0.025
    output_per_1m: 6.00
```

### 5. 拉取并启动容器

```bash
docker pull ghcr.io/xyq272/ai-gateway-metering-proxy:v0.1.0

docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  -v /opt/ai-gateway/metering:/data \
  --add-host host.docker.internal:host-gateway \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xyq272/ai-gateway-metering-proxy:v0.1.0 \
  -config /data/config.yaml
```

> `--add-host host.docker.internal:host-gateway` 让容器能通过 `host.docker.internal` 访问宿主机上的 CLIProxyAPI。如果你的 CLIProxyAPI 也在 Docker 中，改用 `--network` 把两个容器放到同一个网络，并把 `upstream` 改为容器名。

### 6. 验证

```bash
# 检查容器状态
docker ps -a --filter name=metering-proxy

# 查看日志
docker logs metering-proxy

# 健康检查
curl -s http://127.0.0.1:8320/metering/api/health

# 仪表盘
curl -s http://127.0.0.1:8320/metering/api/summary?range=24h
```

### 7. 升级

```bash
docker pull ghcr.io/xyq272/ai-gateway-metering-proxy:v0.2.0
docker stop metering-proxy
docker rm metering-proxy
# 用新版本号重新 docker run（参数同上）
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  -v /opt/ai-gateway/metering:/data \
  --add-host host.docker.internal:host-gateway \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xyq272/ai-gateway-metering-proxy:v0.2.0 \
  -config /data/config.yaml
```

数据库文件在宿主机上，升级不会丢失历史数据。迁移在容器启动时自动执行。

### 镜像标签说明

| 标签 | 说明 |
|---|---|
| `v0.1.0` | 固定版本，生产推荐使用 |
| `edge` | 追踪 main 分支最新提交，适合测试 |
| `latest` | 指向最新发布版本 |

---

## 部署（备选：裸机 + systemd）

如果不使用 Docker，也可以直接在 Linux 上运行二进制文件。

### 1. 构建 Linux 二进制（或从 Release 下载）

```bash
wget https://github.com/xyq272/ai-gateway-metering-proxy/releases/download/v0.1.0/ai-gateway-metering-proxy
chmod +x ai-gateway-metering-proxy
sudo cp ai-gateway-metering-proxy /usr/local/bin/
```

### 2. 准备环境

```bash
install -d -m 700 -o www-data -g www-data /opt/ai-gateway/metering
python3 -c "import secrets; print(secrets.token_hex(32))" > /opt/ai-gateway/metering/salt
chown www-data:www-data /opt/ai-gateway/metering/salt
chmod 600 /opt/ai-gateway/metering/salt
```

### 3. 放置配置文件

将 `config.yaml` 和 `pricing.yaml` 放入 `/opt/ai-gateway/metering/`，此时路径应使用实际系统路径：

```yaml
listen: "127.0.0.1:8320"
upstream: "http://127.0.0.1:8317"
database: "/opt/ai-gateway/metering/usage.sqlite"
salt_file: "/opt/ai-gateway/metering/salt"
# ...其余配置同上
```

### 4. 安装 systemd 服务

```bash
sudo cp systemd/ai-gateway-metering-proxy.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ai-gateway-metering-proxy
sudo systemctl status ai-gateway-metering-proxy
```

systemd 单元示例见 [systemd/ai-gateway-metering-proxy.service](systemd/ai-gateway-metering-proxy.service)。

### 5. 配置 Caddy

```caddyfile
@metered {
    method POST
    path /v1/chat/completions /v1/responses
}

reverse_proxy @metered 127.0.0.1:8320
reverse_proxy 127.0.0.1:8317
```

---

## 记录的字段

MeteringProxy 存储请求元数据及服务商返回的用量字段：

- 时间戳、接口路径、请求方法、状态码
- 请求总耗时及上游响应首字节延迟（`latency_ms`、`ttfb_ms`）
- 流式/非流式标志
- API Key 和客户端 IP 的 SHA256 加盐哈希值
- 请求模型与返回模型名称
- 输入、输出、缓存、推理及总 token 数量
- 请求/响应字节数
- 代理/上游错误信息（如有）

**不会**存储提示词、响应正文、明文 API Key 或明文客户端 IP。

## 关键特性

- 对方法、路径、查询参数、请求头、请求体、状态码及 SSE 字节流实现透明转发。
- SSE 响应即时推送给客户端，解析工作在二次处理的尽力而为模式下完成。
- 非流式响应完整转发，仅采样有界前缀用于用量提取。
- SQLite 以 WAL 模式运行，使用单个写入连接。
- 用量写入采用异步批量写入。
- 队列溢出时丢弃计量事件，而非阻塞 LLM 流量。
- 数据库迁移采用增量叠加方式，兼容旧版数据库文件，可重复执行。

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
| `metering_enabled` | `true` | 计量开关，设为 `false` 时跳过计量，仅透明转发 |
| `webui.enabled` | `true` | 启用仪表盘 |
| `webui.base_path` | `/metering` | 仪表盘路径前缀，勿设为 `/` |
| `pricing_file` | — | 成本估算 YAML 文件路径 |

## 盐值处理

生成盐值一次，并永久保留。哈希值将盐值文件的字节（包括末尾的换行符，如果存在）原样纳入计算。替换或编辑盐值会破坏历史 API Key 和客户端 IP 的分组。

> Docker 部署时，盐值文件位于 `/opt/ai-gateway/metering/salt`（宿主机），挂载后容器内路径为 `/data/salt`。备份主机目录即可保护盐值。

## WebUI

仪表盘默认路径：

```text
https://<域名>/metering
```

仪表盘 API 接口：

- `GET /metering/api/summary?range=24h|today|7d|30d`
- `GET /metering/api/timeseries?range=24h|today|7d|30d&bucket=10m|1h|1d`
- `GET /metering/api/models?range=24h|today|7d|30d`
- `GET /metering/api/keys?range=24h|today|7d|30d`
- `GET /metering/api/requests?range=24h|today|7d|30d&limit=100&status=success|4xx|5xx&model=&endpoint=`
- `GET /metering/api/errors?range=24h|today|7d|30d`
- `GET /metering/api/health`
- `GET /metering/api/metadata`
- `GET /metrics`（Prometheus 文本格式）

`/api/health` 返回队列深度、丢弃事件、解析错误、数据库写入错误、计量开关状态和最新健康快照。`/api/metadata` 返回 profile 列表、支持的 time range 和 bucket 等前端动态渲染所需信息。

健康状态计数器为进程生命期计数器。容器/服务重启后，`parse_errors`、`db_write_errors` 和 `dropped_events` 从零重新开始计数。

## 定价逻辑

价格表仅为估算，不具备计费效力。匹配链路为：

```text
精确匹配 -> 显式别名 -> 规范化形式（去除日期后缀） -> unknown
```

模型名称规范化仅用于定价查找，不会更改持久化存储的 `model_returned` 值。不支持隐式前缀匹配、模糊匹配或版本后缀自动截断。

在 `pricing.yaml` 中通过 `aliases` 字段定义显式别名：

```yaml
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
    aliases:
      - gpt-4o-latest
```

当配置了 `reasoning_per_1m` 时，推理 token 被视为输出 token 的子集：

```text
常规输出 = max(总输出 token - 推理 token, 0)
成本 = 输入 + 缓存输入 + 常规输出 + 推理
```

当未配置 `reasoning_per_1m` 时，全部 `output_tokens` 按 `output_per_1m` 计费，推理 token 不再重复计费。

请保持 `pricing.yaml` 与服务商合同定价同步。

## 备份

`scripts/backup.sh` 使用 SQLite 备份 API 对运行中的 WAL 数据库进行在线备份，并写入压缩文件。

**Docker 环境中运行备份：**

```bash
docker exec metering-proxy sqlite3 /data/usage.sqlite ".backup /tmp/backup.sqlite"
docker cp metering-proxy:/tmp/backup.sqlite ./backup-$(date +%Y%m%d).sqlite
gzip backup-*.sqlite
```

**裸机环境使用 backup.sh：**

默认值：

- 数据库：`/opt/ai-gateway/metering/usage.sqlite`
- 备份目录：`/opt/ai-gateway/metering/backups`
- 每日备份保留：90 天
- 每月备份保留：12 份

环境变量覆盖：

```bash
DB_PATH=/opt/ai-gateway/metering/usage.sqlite \
BACKUP_DIR=/opt/ai-gateway/metering/backups \
RETENTION_DAYS=90 \
MONTHLY_KEEP=12 \
/path/to/scripts/backup.sh
```

通过 cron 或 systemd timer 安装。确保备份用户有权读取数据库并可写入备份目录。

## 运维

常用检查命令：

```bash
# Docker 部署
docker logs metering-proxy
docker exec metering-proxy wget -qO- http://127.0.0.1:8320/metering/api/health

# 裸机部署
journalctl -u ai-gateway-metering-proxy -f
curl -s http://127.0.0.1:8320/metering/api/health

# 检查数据库文件
ls -lh /opt/ai-gateway/metering/usage.sqlite*
```

上线后请关注仪表盘的以下字段：

- 队列深度通常应回归为零
- 正常负载下丢弃事件应保持为零
- 数据库写入错误应保持为零
- 解析错误应较低且可解释
- token 总量应与服务商侧采样数据在预期的提取误差范围内一致

## 回滚

Docker 部署回滚：

```bash
docker stop metering-proxy
docker rm metering-proxy
# 用上一个版本号重新 docker run
docker run -d \
  --name metering-proxy \
  --restart unless-stopped \
  -v /opt/ai-gateway/metering:/data \
  --add-host host.docker.internal:host-gateway \
  -p 127.0.0.1:8320:8320 \
  ghcr.io/xyq272/ai-gateway-metering-proxy:v0.1.0 \
  -config /data/config.yaml
```

裸机部署回滚：在 Caddy 中将计量接口路径直接路由回 CLIProxyAPI：

```text
127.0.0.1:8320 -> 127.0.0.1:8317
```

重载 Caddy 即可。现有 SQLite 数据可保留以供后续检查或迁移。

## 生产就绪检查清单

在接入首批生产流量之前：

- [ ] Docker 容器正常运行且配置了 `--restart unless-stopped`
- [ ] `go test ./...` 和 `go vet ./...` 在 CI 中通过
- [ ] 盐值文件存在，权限 `0600`，已备份
- [ ] 运行时目录权限正确（宿主机 `0700`）
- [ ] `config.yaml` 容器内路径均使用 `/data/` 前缀
- [ ] `pricing.yaml` 与实际服务商定价一致
- [ ] Caddy 已保护 `/metering` 路径
- [ ] 如环境需要，Caddy 请求体限制已配置
- [ ] Caddy 仅将目标 API 路径路由至 MeteringProxy
- [ ] 备份已手动执行一次并通过恢复测试
- [ ] 回滚所需的 Caddy 配置变更已准备就绪
- [ ] 已通过小流量请求与服务商侧用量进行对比验证

## 仓库结构

```text
main.go                                  入口
config.yaml                              示例配置文件
pricing.yaml                             示例模型定价表
Dockerfile                               多阶段 Docker 构建
.dockerignore                            Docker 构建排除文件
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
systemd/ai-gateway-metering-proxy.service systemd 单元示例（仅裸机部署用）
```

## 构建

CI 通过 GitHub Actions 自动完成测试、构建和镜像推送。如需本地构建：

```bash
# 本地开发（Windows）
go test ./...
go vet ./...
go build -o ai-gateway-metering-proxy.exe .

# 本地构建 Docker 镜像
docker build -t ai-gateway-metering-proxy .

# Linux 裸机构建（需要 CGO 可用）
go build -o ai-gateway-metering-proxy .
```

## 数据兼容性

`db.Open()` 在启动时执行增量迁移：

- 创建缺失的表和索引
- 为旧版数据库文件添加缺失的列
- 基于 RFC3339 文本时间戳回填 Unix 时间戳列
- 将负数字节数规范化为零
- 通过 `PRAGMA user_version` 跟踪表结构状态

早期版本的历史记录仍可查询。但因旧版解析器缺陷等导致从未被正确采集的数据无法恢复。

## 已知运维局限

- 异步队列满时，计量事件按设计被丢弃。
- 非流式用量提取仅采样有界响应前缀。
- SSE 行长超过内部解析缓冲区时，按原样转发但跳过解析。
- SQLite 适用于单节点计量代理；本项目并非分布式分析存储。
- WebUI 为只读，但仍须由外层反向代理进行保护。
