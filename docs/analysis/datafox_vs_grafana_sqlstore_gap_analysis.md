# DataFox vs Grafana SQLStore 差距分析报告

## 1. 文件规模对比

| 维度 | Grafana | DataFox | 差距 |
|---|---|---|---|
| 核心文件数 | 19 | 7 | **-12** |
| `db.DB` 接口方法数 | 8 | 5 | **-3** |
| 测试文件数 | 7 | 2 | **-5** |
| Dialect/Migrator 系统 | 19 文件独立子包 | ❌ 无 | **完全缺失** |

---

## 2. 功能差距明细

### 2.1 ❌ 完全缺失的能力

#### (1) 会话上下文传播 (Session Context Propagation)
**Grafana**: `session.go` + `transactions.go` 实现了 Session 复用机制。
- `InTransaction(ctx, fn)` 将事务 Session 存入 `context.Value`
- 后续 `WithDbSession(ctx, fn)` 自动检测并复用已有事务
- **意义**: 支持嵌套调用共享同一事务，避免了 Service A → Service B 跨服务调用时的事务断裂问题
- **DataFox 现状**: `Tx` 直接调用 `gorm.Transaction`，无上下文传播能力

```go
// Grafana 模式 — 嵌套共享事务
store.InTransaction(ctx, func(ctx context.Context) error {
    serviceA.Do(ctx) // 内部 WithDbSession 复用事务
    serviceB.Do(ctx) // 同上
    return nil
})
```

#### (2) 事件总线 (Event Bus / PublishAfterCommit)
**Grafana**: `DBSession.PublishAfterCommit(event)` + 事务提交后通过 `bus.Publish` 广播。
- 用于 `DeleteDataSource` 后触发资源清理（如删除关联 correlations）
- **DataFox 现状**: `store.go:301` 注释 `"Publishing events is handled at a higher level"` — 未实现

#### (3) Dialect / Migrator 子系统
**Grafana**: 独立的 `migrator/` 子包，19 个文件：
- `dialect.go` — 抽象接口 (`BatchSize()`, `Quote()`，`Upsert()` 等)
- `mysql_dialect.go`, `postgres_dialect.go`, `sqlite_dialect.go` — 方言实现
- `migrator.go` — 版本化迁移引擎（migration_log 表跟踪）
- **DataFox 现状**: 使用外部 `golang-migrate` + GORM `AutoMigrate`，无 Dialect 抽象

#### (4) 数据库连接池 Prometheus 指标
**Grafana**: `sqlstore_metrics.go` — 实现 `prometheus.Collector`，暴露 9 个指标：
  - `grafana_database_conn_max_open` / `conn_open` / `conn_in_use` / `conn_idle` (Gauge)
  - `conn_wait_count_total` / `conn_wait_duration_seconds` / `conn_max_idle_closed_total` 等 (Counter)
- **DataFox 现状**: 仅有 `Stats()` 方法返回 `sql.DBStats`，未注册到 Prometheus

#### (5) 批量操作 (Bulk Operations)
**Grafana**: `bulk.go` 提供：
  - `BulkInsert(table, recordsSlice, opts)` — 按 Dialect 的 `BatchSize` 分批插入
  - `InBatches(items, opts, fn)` — 通用分批处理器
- **DataFox 现状**: 无批量操作辅助函数

#### (6) MySQL 支持 + TLS
**Grafana**: 完整支持 MySQL + TLS：
  - `tls_mysql.go` — 自定义 TLS 证书加载 (`ca_cert_path`, `client_key_path`)
  - `database_config.go` 中的 MySQL 连接字符串构建（`collation`, `parseTime`, `ANSI_QUOTES`）
  - MySQL 专用 Dialect (`mysql_dialect.go`)
- **DataFox 现状**: 仅支持 SQLite + Postgres

#### (7) OTel Tracing 集成
**Grafana**: `session.go` 中每次 `startSessionOrUseExisting` 都会创建 OTel Span：
  - Span 名称: `"open session"`
  - 属性: `attribute.Bool("transaction", beginTran)`
  - 错误关联: `tracing.Errorf(span, ...)`
- **DataFox 现状**: `store_test.go` 使用了 OTel 但 SQLStore 核心代码无 Span 埋点

---

### 2.2 ⚠️ 部分实现但差距明显的能力

#### (8) 数据库配置管理
| 配置项 | Grafana `DatabaseConfig` | DataFox `Config` |
|---|---|---|
| 连接字符串构建 | ✅ 自动从 host/port/name 构建 | ❌ 直接传 DSN |
| URL 解析 | ✅ 支持 `database://user:pass@host/db` | ❌ 无 |
| SSL/TLS | ✅ sslmode/ca_cert/client_key | ❌ 无 |
| 事务隔离级别 | ✅ `isolation_level` | ❌ 无 |
| WAL 模式控制 | ✅ `wal = true` | ✅ teststore 硬编码 |
| 查询日志 | ✅ `log_queries` | ✅ `LogLevel` |
| 迁移控制 | ✅ `skip_migrations` / `migration_locking` | ❌ 无 |
| 重试配置 | ✅ `query_retries` + `transaction_retries` 分离 | ⚠️ 单一 `MaxRetries` |

#### (9) 测试基础设施
| 能力 | Grafana | DataFox |
|---|---|---|
| 每测试独立数据库 | ✅ `NewTestStore` | ✅ `NewTestStore` |
| 自动清理 | ✅ `tb.Cleanup` | ✅ `tb.Cleanup` |
| SQLite 支持 | ✅ 临时文件 + 内存 | ✅ 临时文件 + 内存 |
| Postgres 支持 | ✅ 临时数据库 | ✅ 临时数据库 |
| MySQL 支持 | ✅ 临时数据库 | ❌ 无 |
| Feature Flags 选项 | ✅ `WithFeatureFlags` | ❌ 无 |
| Migration 选项 | ✅ `WithOSSMigrations` / `WithoutMigrator` | ❌ 仅 `WithModels` (AutoMigrate) |
| Truncation 选项 | ✅ `WithTruncation` | ✅ `WithTruncation` |
| DB 类型判断辅助 | ✅ `IsTestDbSQLite()` / `IsTestDbPostgres()` | ❌ 无 |
| FakeDB mock | ✅ `db/dbtest` | ✅ `db/dbtest` |

---

## 3. 优先级建议

### P0 — 核心架构 (影响正确性)
1. **Session 上下文传播 + InTransaction** — 跨服务事务一致性
2. **查询/事务重试分离配置** — 当前单一 `MaxRetries` 不够灵活

### P1 — 可观测性 (影响运维)
3. **Prometheus 连接池指标** — 生产环境必备
4. **OTel Tracing Span 埋点** — 数据库调用链路追踪

### P2 — 功能完善 (影响扩展性)
5. **MySQL 支持 + TLS** — 如需兼容更多数据库
6. **批量操作** — 大数据量导入场景
7. **事件总线** — 如需数据源删除后联动清理

### P3 — 工程质量 (影响开发效率)
8. **数据库配置管理增强** — URL 解析、SSL 配置
9. **测试基础设施增强** — `IsTestDbXxx()` 辅助函数、MySQL 临时库

---

**报告生成日期**: 2026-02-10
**分析对象**: Grafana `pkg/services/sqlstore` (19 文件) vs DataFox `pkg/services/sqlstore` (7 文件)
