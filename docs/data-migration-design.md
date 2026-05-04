# MeteringProxy 数据迁移体系设计案

版本：v1.0-draft
日期：2026-05-04
状态：设计案
用途：本文档定义 MeteringProxy 数据库从任意历史版本迁移至最新版本的能力边界、分层架构、盐值管理策略及实施路线。本文档是对 `项目设计与实施文档.md` S0.1 中迁移链设计预留点的展开设计。

---

## 0. 核心结论

当前项目已具备纯增量迁移能力（`ensureColumns` + `createIndexes` + `schema_tasks`），但无法应对破坏性模式变更（列删除、类型变更、约束新增）和盐值轮换后的哈希聚合断裂。

本设计案提出三层递进式迁移体系：

- **Layer 1（已有）**：纯增量迁移，兜底所有 `ALTER TABLE ADD COLUMN` 场景。
- **Layer 2（新增）**：顺序化破坏性迁移链，支持表重建等非幂等操作。
- **Layer 3（预留）**：导出/导入逃生舱，应对跨引擎、跨大版本迁移。

对于盐值变更，核心结论是：除 `api_key_hash` 与 `client_ip_hash` 外，其余 30 个明文字段在任何场景下均可无损迁移；哈希列在盐值不变时无需处理，盐值变更时通过 `salt_version` 列标记边界即可避免错配——跨盐值的 Key 级聚合打通需依赖请求携带明文 Key 的过渡期双哈希回填，任何纯数据库手段均无法绕过哈希的单向数学约束。

---

## 1. 现状分析

### 1.1 当前迁移能力

`internal/db/db.go` `migrate()` 函数在每次 `Open()` 时执行，具备以下纯增量迁移能力：

| 操作 | 机制 | SQL | 幂等性 |
|------|------|-----|--------|
| 新建表 | `CREATE TABLE IF NOT EXISTS` | 完整 DDL | 是 |
| 补全列 | `ensureColumns` → `ALTER TABLE ADD COLUMN` | 单列 DDL | 是 |
| 补全索引 | `createIndexes` → `CREATE INDEX IF NOT EXISTS` | 单索引 DDL | 是 |
| 数据修复 | `runSchemaTask` → 任务函数 | UPDATE/回填 | 是（一次执行，永久跳过） |
| 版本记录 | `INSERT OR IGNORE INTO schema_migrations` | INSERT | 是 |

### 1.2 迁移基础设施

已有三张治理表：

```sql
-- 结构迁移版本（当前仅写入 v4_baseline 一条记录）
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    applied_at TEXT NOT NULL
);

-- 一次性数据任务（当前仅 backfill_v4_normalization 一条记录）
CREATE TABLE IF NOT EXISTS schema_tasks (
    name TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

-- SQLite 内置标量版本号
PRAGMA user_version = 4;
```

### 1.3 能力缺口

以下场景当前迁移体系无法处理：

| 场景 | 无法处理的原因 |
|------|---------------|
| 列类型变更（如 `INTEGER` → `REAL`） | SQLite `ALTER TABLE` 不支持修改列类型 |
| 列删除或重命名 | 当前未实现 `DROP COLUMN` / `RENAME COLUMN` 逻辑 |
| 新增 NOT NULL 约束 | SQLite `ALTER TABLE` 不支持向已有列添加约束 |
| 主键重定义 | 需要重建表 |
| 非幂等数据迁移（分批、带进度） | `schema_tasks` 只有完成/未完成两态，无可恢复中间态 |
| 盐值轮换 | 无 `salt_version` 标记，新旧哈希混在一起无法区分 |
| 跨存储引擎迁移（SQLite → PostgreSQL） | 超出 SQL 层面，需导出/导入 |

---

## 2. 三层迁移体系设计

### 2.1 层级关系

三层为递进叠加关系，上层依赖下层的基础设施（`schema_migrations` 版本号体系统一共用），而非替代关系。

```
Layer 3: 导出/导入逃生舱（跨引擎、跨主版本迁移）
    ↑ 兜底
Layer 2: 顺序化破坏性迁移链（表重建、非幂等操作）
    ↑ 兜底
Layer 1: 纯增量迁移（加列、加索引、数据回填）—— 已有
```

启动时按 L1 → L2 顺序执行，L3 为独立 CLI 工具，不参与启动流程。旧库无论处于哪个版本，均可通过此体系对齐到最新版本。

### 2.2 Layer 1：纯增量迁移（已有，保留不动）

位置：`internal/db/db.go` `migrate()` 当前逻辑。

能力范围：所有 `ALTER TABLE ADD COLUMN`、`CREATE INDEX`、一次性数据回填。

不变：此层继续作为安全网，确保即使 L2 迁移链因 bug 遗漏了某列，`ensureColumns` 也会在启动时补齐。

### 2.3 Layer 2：顺序化破坏性迁移链（新增）

#### 2.3.1 数据结构

```go
// 非幂等结构迁移定义
type migration struct {
    version int
    name    string
    up      string // 原始 SQL（不支持参数化，仅内部定义使用）
}
```

#### 2.3.2 迁移注册表

```go
var migrations = []migration{
    // 示例：v5 拆分 model_requested 为 provider + model
    // {
    //     version: 5,
    //     name:    "split_model_column",
    //     up: `
    //         ALTER TABLE request_usage RENAME TO request_usage_old_v5;
    //         CREATE TABLE request_usage (
    //             id INTEGER PRIMARY KEY AUTOINCREMENT,
    //             ...
    //             provider TEXT NOT NULL DEFAULT '',
    //             model TEXT NOT NULL DEFAULT '',
    //             ...
    //         );
    //         INSERT INTO request_usage
    //             SELECT ..., model_requested, '' as provider, model_requested as model, ...
    //             FROM request_usage_old_v5;
    //     `,
    // },
}
```

注：`up` 定义为原始 SQL 字符串而非 `func(*sql.DB) error`，是为了确保迁移逻辑的可审查性和可重复性。如果未来某个迁移需要 Go 逻辑（如外部 API 调用、复杂数据变换），再扩展 `fn func(*sql.Tx) error` 字段，但首发应优先使用纯 SQL。

#### 2.3.3 迁移执行逻辑

```go
func applyMigrations(sqlDB *sql.DB) error {
    tx, err := sqlDB.Begin()
    if err != nil {
        return fmt.Errorf("applyMigrations: begin tx: %w", err)
    }
    defer tx.Rollback()

    for _, m := range migrations {
        var existing int
        err := tx.QueryRow(
            "SELECT version FROM schema_migrations WHERE version = ?",
            m.version,
        ).Scan(&existing)
        if err == nil {
            continue // 已应用，跳过
        }
        if err != sql.ErrNoRows {
            return fmt.Errorf("applyMigrations: check v%d: %w", m.version, err)
        }

        if _, err := tx.Exec(m.up); err != nil {
            return fmt.Errorf("applyMigrations: execute v%d (%s): %w", m.version, m.name, err)
        }

        if _, err := tx.Exec(
            "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
            m.version, m.name, time.Now().UTC().Format(time.RFC3339),
        ); err != nil {
            return fmt.Errorf("applyMigrations: record v%d: %w", m.version, err)
        }
    }

    return tx.Commit()
}
```

关键约束：

- 所有待执行的迁移在**同一事务**中顺序执行。如果任一迁移失败，整个事务回滚，数据库状态保持不变。
- 迁移完成后立即在事务中写入版本记录，保证原子性。
- SQLite 的 `ALTER TABLE RENAME TO` 和 `CREATE TABLE` 在事务中是安全的，即使进程在迁移中途崩溃，未提交的变更会全部回滚。

#### 2.3.4 表重建安全模式

破坏性迁移的标准写法：

```sql
-- 1. 保留旧表（带版本后缀，非 DROP）
ALTER TABLE request_usage RENAME TO request_usage_old_v5;

-- 2. 创建新结构
CREATE TABLE request_usage (...);

-- 3. 数据搬迁
INSERT INTO request_usage SELECT ... FROM request_usage_old_v5;

-- 4. 不在此事务中 DROP _old 表（见下文）
```

旧表清理策略：

- 不在迁移事务中执行 `DROP TABLE _old`。原因：如果数据搬迁逻辑有误，旧表是唯一恢复来源。
- 迁移成功后的下次启动时，检测并删除所有 `*_old_v*` 表。此操作独立于迁移链，失败不影响启动。
- 清理逻辑应记录日志，告知运维迁移产生了哪些遗留表。

如果磁盘空间敏感且数据搬迁已验证正确，可在迁移事务中一并执行 `DROP TABLE _old`。首发保守起见采用延迟清理。

#### 2.3.5 在 migrate() 中的插入位置

```
migrate(sqlDB):
  1. PRAGMA 设置
  2. CREATE TABLE IF NOT EXISTS (schema_migrations, schema_tasks, 基础业务表)
  3. → applyMigrations(sqlDB)              ← 新增，L2 破坏性迁移
  4. ensureColumns (安全网)
  5. createIndexes
  6. runSchemaTask (一次性数据回填)
  7. INSERT OR IGNORE schema_migrations (v4_baseline，幂等)
  8. PRAGMA user_version = schemaVersion
```

注意：即使 L2 迁移链落地，步骤 7 中 `INSERT OR IGNORE` 的 `v4_baseline` 也保留不动。`v4_baseline` 标记的语义是"通过纯增量模式对齐到 v4 基线的数据库"，这是所有旧库到达最新版本的必经版本号。

### 2.4 Layer 3：导出/导入逃生舱（预留）

#### 2.4.1 定位

L3 不作为启动时迁移的一部分，而是独立的 CLI 工具或子命令。适用场景：

- SQLite → PostgreSQL 跨引擎迁移。
- 大规模表结构重构，重建成本高于导出重导。
- 用户希望做一次完整的数据清洗和重导。

#### 2.4.2 数据格式

```json
{"table":"request_usage","row":{"id":1,"created_at":"2026-05-04T10:00:00Z","endpoint":"/v1/chat/completions",...}}
{"table":"request_usage","row":{"id":2,...}}
{"table":"health_metrics","row":{"id":1,"timestamp":"2026-05-04T10:01:00Z",...}}
```

采用 JSONL（每行一条记录），原因：

- 可流式写入/读取，不依赖内存大小。
- 人类可审计，方便排障。
- 与表结构耦合度低，未来加列不需要格式升级。

#### 2.4.3 工具形式

```bash
# 导出
metering-proxy export --db usage.sqlite --output backup.jsonl

# 导入
metering-proxy import --db new.sqlite --input backup.jsonl

# 跨引擎
metering-proxy export --db usage.sqlite --output - | \
  psql -c "COPY request_usage FROM STDIN"
```

`export` 命令不处理 `id` 列（由目标库重新生成），不导出 `api_key_hash` 字段（如盐值已变更，旧哈希无意义且可能误导）。

#### 2.4.4 数据完整性验证

导入完成后执行：

```sql
SELECT COUNT(*) FROM request_usage;         -- 行数比对
SELECT COUNT(*) FROM health_metrics;
SELECT SUM(total_tokens) FROM request_usage; -- 聚合值比对（抽样）
```

---

## 3. 数据可迁移性矩阵

### 3.1 按列分类

`request_usage` 共 32 列（不含 `id`），分为两类：

| 分类 | 列名 | 列数 | 纯数据库迁移 | 盐变更后迁移 |
|------|------|:----:|:------------:|:------------:|
| 明文结构化 | `created_at`, `created_at_unix`, `request_id`, `endpoint`, `method`, `status`, `latency_ms`, `ttfb_ms`, `stream`, `model_requested`, `model_returned`, `input_tokens`, `output_tokens`, `reasoning_tokens`, `cached_tokens`, `total_tokens`, `request_bytes`, `response_bytes`, `error`, `endpoint_profile`, `capture_mode`, `metering_kind`, `usage_raw_json`, `usage_raw_truncated`, `billable_input`, `billable_output`, `billable_total`, `billable_unit`, `capture_outcome`, `capture_reason` | 30 | 完全可迁移 | 完全可迁移 |
| 单向哈希 | `api_key_hash`, `client_ip_hash` | 2 | 完全可迁移 | **不可跨盐聚合**（数学约束） |

### 3.2 按迁移类型分类

| 迁移类型 | 可行性 | 依赖条件 | 实现层级 |
|----------|:------:|----------|:--------:|
| 新增列（`ADD COLUMN`） | 是 | 无 | L1 |
| 新增索引 | 是 | 无 | L1 |
| 删除列 | 是 | 表重建 | L2 |
| 列类型变更 | 是 | 表重建 + CAST | L2 |
| 新增 NOT NULL | 是 | 表重建 + 默认值填充 | L2 |
| 列拆分/合并 | 是 | 表重建 + 表达式变换 | L2 |
| 跨盐哈希聚合 | **否** | 数学不可行；需要新请求携带明文 Key | N/A |
| 盐内哈希聚合 | 是 | `salt_version` 标记 | L1 |
| 跨引擎迁移 | 是 | L3 CLI 工具 | L3 |

---

## 4. 盐值管理设计

### 4.1 问题定义

`api_key_hash = HMAC-SHA256(salt, plaintext_key)` 和 `client_ip_hash = HMAC-SHA256(salt, plaintext_ip)` 是单向函数。当盐值变更时，同一把 Key 或同一个 IP 会产生不同的哈希值，导致 `GROUP BY` 聚合将新旧记录计为两个不同来源。

### 4.2 盐值不变时的策略

不处理。当前所有行的哈希使用同一盐值，聚合语义自洽。这是绝大多数情况下的常态。

### 4.3 salt_version 标记

新增列，成本为零，为未来提供边界标记：

```sql
ALTER TABLE request_usage ADD COLUMN salt_version INTEGER DEFAULT 1;
```

插入时写入当前盐版本：

```go
// 在 recordUsage 或 InsertBatch 路径中
row.SaltVersion = currentSaltVersion // 从配置中读取，初始为 1
```

查询时按版本分组，避免跨盐错配：

```sql
SELECT api_key_hash, salt_version, SUM(total_tokens) AS tokens
FROM request_usage
WHERE created_at_unix >= ?
GROUP BY api_key_hash, salt_version
ORDER BY tokens DESC;
```

### 4.4 换盐时的数据过渡策略

当不得不更换盐值时，推荐过渡期双哈希方案：

**第一阶段：部署新盐**

1. 新增列：`api_key_hash_v2 TEXT DEFAULT ''`, `client_ip_hash_v2 TEXT DEFAULT ''`
2. `salt_version` 递增为 2
3. 新请求同时写入两套哈希：`api_key_hash`（旧盐）和 `api_key_hash_v2`（新盐）
4. 历史数据中 `api_key_hash_v2` 为空

**第二阶段：渐进回填**

当携带明文 Key 的请求到达时，用旧哈希定位历史行，回填新哈希：

```sql
UPDATE request_usage
SET api_key_hash_v2 = ?
WHERE api_key_hash = ? AND api_key_hash_v2 = '';
```

这是一个渐进过程：一个 Key 被再次使用后，它的全部历史行才被回填。如果某个 Key 在换盐后再未出现，其历史行将永久保留空的 `_v2` 值。

**第三阶段：旧哈希废弃**

当所有有效数据已回填或超出保留窗口后（如 90 天），可以选择：

- 停止写入 `api_key_hash` 列。
- 将 `api_key_hash_v2` 重命名为 `api_key_hash`（通过一次 L2 表重建迁移）。
- 回退到单一哈希列模式。

此三阶段对业务透明，不影响任何明文结构化列。

### 4.5 什么情况下盐必须变更

以下场景可能触发盐值变更——但在这个项目中，所有这些场景的发生概率都极低：

| 触发场景 | 概率 | 说明 |
|----------|:----:|------|
| 盐文件泄露 | 低 | 备份文件、日志、配置文件意外暴露；泄露的后果是对低熵 IP 的字典反推成为可能，而非直接暴露 Key |
| 合规要求定期轮换密钥材料 | 看行业 | 一般面向金融/医疗行业，AI 网关计量数据通常不在此范围 |
| 从 HMAC-SHA256 升级到 bcrypt/Argon2 | 极低 | 分组聚合不需要密码哈希的抗暴力破解特性，HMAC-SHA256 足够 |
| 安全意识过度敏感，主观决定轮换 | 低 | 需权衡轮换成本与风险增量 |

实际运营中，盐值大概率永远不变。`salt_version` 是低成本保险，不加也能过，加了给未来留退路。

---

## 5. 风险与边界

### 5.1 不可逾越的边界

1. **哈希单向性**：任何纯数据库手段均无法将旧盐哈希转换为新盐哈希。这是密码学硬边界，不是架构缺陷。
2. **salt 文件丢失**：salt 文件是历史哈希分组能力的根。丢失后所有历史记录的 `api_key_hash` 和 `client_ip_hash` 失去聚合语义，与换盐但无新版哈希的情况等效。
3. **物理文件损坏**：SQLite 文件损坏（WAL 损坏、页损坏）无法通过迁移修复，需从备份恢复。L3 导出工具可配合 `PRAGMA integrity_check` 做损坏检测，但修复不是本项目职责。

### 5.2 被排除的方案

以下方案经过评估后排除：

| 方案 | 排除原因 |
|------|----------|
| 存储明文 API Key / Client IP | 违反 CLAUDE.md 第 2 条不变量；将安全风险从哈希碰撞提升为明文泄露；备份文件升级为高敏资产；增加合规负担 |
| 使用可逆加密替代哈希 | 密钥管理引入新的轮换问题；与哈希相比只是推迟了信任根，没有解决根本问题 |
| 每次启动全量重建所有表 | 在大库上启动时间不可控；丢失迁移链的可追溯性 |
| 使用 ORM 自动迁移 | 引入新依赖；黑箱操作不符合"迁移行为必须可审查"的约束 |

### 5.3 实施约束

所有迁移操作必须遵守 CLAUDE.md 中的不变式：

| 不变式 | 迁移中的体现 |
|--------|-------------|
| 转发优先于计量 | 迁移仅在启动时执行，不参与请求路径 |
| 不存储敏感原文 | 表重建/导出时只操作哈希值和结构化计数 |
| 请求响应透明 | 迁移不影响代理数据路径 |
| 不阻塞流量 | 迁移失败阻止启动（安全失败），不破坏运行中服务 |
| 迁移仅增量 | 本设计将"仅增量"扩展为"增量 + 有序破坏性迁移链"，破坏性操作在表副本上执行，原始数据保留到清理确认 |
| salt 文件稳定性 | 新增 `salt_version` 不改变 salt 文件本身；`salt_version` 只是记录"用的是哪个盐" |

---

## 6. 实施计划

### 6.1 实施分级

| 编号 | 事项 | 分级 | 说明 |
|------|------|:----:|------|
| M1 | `salt_version` 列新增 | S1 | 一行 DDL，零风险，尽早加 |
| M2 | L2 顺序化迁移链 + `migration` 结构体落地 | S1 | 核心扩展点，约 80-100 行 |
| M3 | 表重建安全模式（RENAME TO _old + INSERT SELECT + 延迟清理） | S1 | 随 M2 一起实现 |
| M4 | L2 旧表清理逻辑 | S2 | 检测 + 删除 `*_old_v*` 表，独立于迁移链 |
| M5 | L3 导出/导入 CLI | S2 | 首期不需要，在确有跨引擎需求时再启动 |

### 6.2 M2 改动范围

| 文件 | 改动 |
|------|------|
| `internal/db/db.go` | 新增 `migration` 类型、`migrations` 注册表、`applyMigrations()`；在 `migrate()` 中插入调用点 |
| `internal/db/db_test.go` | 新增：L2 迁移链执行测试、迁移幂等性测试、事务回滚测试、`_old` 表清理测试、旧库跨版本升级测试 |

### 6.3 不受影响的模块

以下模块与迁移体系无耦合，无需修改：

- `internal/proxy/` — 代理转发路径
- `internal/extractor/` — 用量提取
- `internal/pricing/` — 成本计算
- `internal/hash/` — 哈希（仅在加 `salt_version` 时可能需要新增 `SaltVersion` 配置字段，视实现方式而定）
- `internal/writer/` — 异步写入
- `internal/webui/` — WebUI 查询
- `main.go` — 仅在 L3 CLI 工具时涉及

### 6.4 执行顺序

| 顺序 | 内容 | 分级 | 预估 |
|------|------|:----:|------|
| 1 | M1：`salt_version` 列 + `ALTER TABLE ADD COLUMN` | S1 | 15 分钟 |
| 2 | M2：L2 迁移链基础设施 | S1 | 60-90 分钟 |
| 3 | M4：旧表清理逻辑 | S2 | 20 分钟 |
| 4 | M5：L3 CLI 工具 | S2 | 按需启动 |

---

## 7. 附录：启动时 migrate 完整流程（目标态）

```
migrate(sqlDB):
  1.  PRAGMA journal_mode = WAL
  2.  PRAGMA synchronous = NORMAL
  3.  CREATE TABLE IF NOT EXISTS schema_migrations
  4.  CREATE TABLE IF NOT EXISTS schema_tasks
  5.  CREATE TABLE IF NOT EXISTS request_usage       ← 基线 DDL
  6.  CREATE TABLE IF NOT EXISTS health_metrics
  7.  applyMigrations(sqlDB)                          ← L2 破坏性迁移（新增）
        for each pending migration v5, v6, ...:
          [tx] 执行 up SQL → 写入 schema_migrations
  8.  ensureColumns("schema_migrations", ...)         ← L1 安全网
  9.  ensureColumns("request_usage", ...)
  10. ensureColumns("health_metrics", ...)
  11. createIndexes()
  12. runSchemaTask("backfill_v4_normalization", ...)  ← 历史数据标准化
  13. INSERT OR IGNORE INTO schema_migrations (v4_baseline)
  14. PRAGMA user_version = schemaVersion
```

旧版本数据库通过此流程自动对齐到最新版本。无论旧库版本是多少（v4 以下亦或 L2 引入后的 v5、v6...），启动后状态始终一致。
