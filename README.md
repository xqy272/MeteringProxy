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

## 仓库结构

```text
main.go                                  入口
config.yaml                              示例配置文件
pricing.yaml                             示例模型定价表
internal/config                          YAML 配置默认值与校验
internal/db                              SQLite 表结构、迁移、查询
internal/extractor                       从 JSON 和 SSE 中提取用量信息
internal/hash                            API Key 和 IP 的加盐哈希
internal/pricing                         成本估算
internal/proxy                           反向代理与用量采集
internal/webui                           仪表盘 API 及内嵌静态界面
internal/writer                          异步批量写入与计数器
scripts/backup.sh                        SQLite 备份脚本
systemd/ai-gateway-metering-proxy.service systemd 单元示例
```

## 运行要求

- Go 1.21+
- 支持 CGO 的构建环境（项目使用了 `github.com/mattn/go-sqlite3`）
- Linux 目标主机（用于部署提供的 systemd 单元）
- 运行 `scripts/backup.sh` 的主机上需安装 `sqlite3` 和 `gzip`

生产环境请在 Linux 上构建 Linux 二进制文件，或在 CI 中使用与服务器相同的目标架构进行构建。

## 构建

```bash
go test ./...
go vet ./...
go build -o ai-gateway-metering-proxy .
```

在 Windows 本地开发时，可构建 `ai-gateway-metering-proxy.exe`；部署到 Linux 时需使用 Linux 构建的二进制文件。

## 配置

服务默认读取 `config.yaml`，也可通过 `-config` 参数指定路径。

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

关键配置项说明：

- `listen`：Caddy 使用的本地监听地址。
- `upstream`：CLIProxyAPI 地址。
- `database`：SQLite 数据库文件路径。
- `salt_file`：用于 API Key 和 IP 哈希的固定盐值文件。
- `queue_capacity`：计量事件队列最大容量，超过后开始丢弃事件。
- `batch_size`：每次 SQLite 批量插入的最大记录数。
- `flush_interval`：刷新部分批次前的最大等待时间。
- `max_nonstream_sample_bytes`：非流式响应用于用量提取的最大采样前缀字节数。
- `webui.base_path`：仪表盘的非根路径，请勿设置为 `/`。
- `pricing_file`：用于估算成本的 YAML 价格表。

## 盐值处理

生成盐值一次，并永久保留。哈希值将盐值文件的字节（包括末尾的换行符，如果存在）原样纳入计算。替换或编辑盐值会破坏历史 API Key 和客户端 IP 的分组。

示例：

```bash
install -d -m 700 -o www-data -g www-data /opt/ai-gateway/metering
python3 -c "import secrets; print(secrets.token_hex(32))" > /opt/ai-gateway/metering/salt
chown www-data:www-data /opt/ai-gateway/metering/salt
chmod 600 /opt/ai-gateway/metering/salt
```

## 部署

1. 构建 Linux 二进制文件。
2. 创建 `/opt/ai-gateway/metering` 目录，所有者为 `www-data:www-data`，权限 `0700`。
3. 生成盐值文件并设置权限 `0600`。
4. 将 `config.yaml` 和 `pricing.yaml` 放入 `/opt/ai-gateway/metering/`。
5. 将二进制文件复制到 `/usr/local/bin/ai-gateway-metering-proxy`。
6. 将 `systemd/ai-gateway-metering-proxy.service` 安装到 `/etc/systemd/system/`。
7. 重载 systemd 并启动服务。
8. 在 Caddy 中将计量接口路径路由至 `127.0.0.1:8320`。
9. 配置备份。

Systemd 操作：

```bash
systemctl daemon-reload
systemctl enable --now ai-gateway-metering-proxy
systemctl status ai-gateway-metering-proxy
```

Caddy 路由配置示例：

```caddyfile
@metered {
    method POST
    path /v1/chat/completions /v1/responses
}

reverse_proxy @metered 127.0.0.1:8320
reverse_proxy 127.0.0.1:8317
```

请根据现有 Caddyfile 进行调整，并确保 `/metering` 已配置认证。

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

健康状态计数器为进程生命期计数器。服务重启后，`parse_errors`、`db_write_errors` 和 `dropped_events` 从零重新开始计数。

## 数据兼容性

`db.Open()` 在启动时执行增量迁移：

- 创建缺失的表和索引
- 为旧版数据库文件添加缺失的列
- 基于 RFC3339 文本时间戳回填 Unix 时间戳列
- 将负数字节数规范化为零
- 通过 `PRAGMA user_version` 跟踪表结构状态

早期版本的历史记录仍可查询。但因旧版解析器缺陷等导致从未被正确采集的数据无法恢复。

## 定价逻辑

价格表仅为估算，不具备计费效力。

当配置了 `reasoning_per_1m` 时，推理 token 被视为输出 token 的子集：

```text
常规输出 = max(总输出 token - 推理 token, 0)
成本 = 输入 + 缓存输入 + 常规输出 + 推理
```

当未配置 `reasoning_per_1m` 时，全部 `output_tokens` 按 `output_per_1m` 计费，推理 token 不再重复计费。

请保持 `pricing.yaml` 与服务商合同定价同步。

## 备份

`scripts/backup.sh` 使用 SQLite 备份 API 对运行中的 WAL 数据库进行在线备份，并写入压缩文件。

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
journalctl -u ai-gateway-metering-proxy -f
curl -s http://127.0.0.1:8320/metering/api/health
ls -lh /opt/ai-gateway/metering/usage.sqlite*
```

上线后请关注仪表盘的以下字段：

- 队列深度通常应回归为零
- 正常负载下丢弃事件应保持为零
- 数据库写入错误应保持为零
- 解析错误应较低且可解释
- token 总量应与服务商侧采样数据在预期的提取误差范围内一致

## 回滚

MeteringProxy 设计支持快速回滚。在 Caddy 中将计量接口路径直接路由回 CLIProxyAPI：

```text
127.0.0.1:8320 -> 127.0.0.1:8317
```

重载 Caddy 即可。现有 SQLite 数据可保留以供后续检查或迁移。

## 生产就绪检查清单

在接入首批生产流量之前：

- `go test ./...` 在目标构建环境通过。
- `go vet ./...` 通过。
- Linux 二进制文件在 CGO 可用环境下构建。
- 盐值文件存在，所有者为服务用户，权限 `0600`。
- 运行时目录所有者为服务用户，权限 `0700`。
- Caddy 已保护 `/metering` 路径。
- 如环境需要，Caddy 请求体限制已配置。
- Caddy 仅将目标 API 路径路由至 MeteringProxy。
- `pricing.yaml` 与实际服务商定价一致。
- 备份已手动执行一次并通过恢复测试。
- 回滚所需的 Caddy 配置变更已准备就绪。
- 已通过小流量请求与服务商侧用量进行对比验证。

## 已知运维局限

- 异步队列满时，计量事件按设计被丢弃。
- 非流式用量提取仅采样有界响应前缀。
- SSE 行长超过内部解析缓冲区时，按原样转发但跳过解析。
- SQLite 适用于单节点计量代理；本项目并非分布式分析存储。
- WebUI 为只读，但仍须由外层反向代理进行保护。
