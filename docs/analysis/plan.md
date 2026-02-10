# DataFox SQLStore 差距补齐计划

## 概述
DataFox SQLStore (7 文件) 相比 Grafana SQLStore (19 文件) 在以下 9 个方面存在差距，按优先级排列。

---

## Phase 1: 数据库配置管理 ← 当前
**状态**: 🔄 进行中

| 差距项 | 说明 |
|---|---|
| 连接字符串构建 | Grafana 从 host/port/name 自动构建；DataFox 要求手传 DSN |
| URL 解析 | Grafana 支持 `postgres://user:pass@host/db` 格式 |
| SSL/TLS 配置 | Grafana 支持 sslmode/ca_cert/client_key |
| WAL 模式 | Grafana 配置化；DataFox 仅 teststore 硬编码 |
| 事务隔离级别 | Grafana 支持配置；DataFox 无 |
| 重试分离 | Grafana `query_retries` + `transaction_retries`；DataFox 单一 `MaxRetries` |

**变更文件**:
- `[NEW]  pkg/services/sqlstore/database_config.go`
- `[NEW]  pkg/services/sqlstore/database_config_test.go`
- `[MOD]  pkg/services/sqlstore/store.go`
- `[MOD]  pkg/services/sqlstore/retry.go`
- `[MOD]  pkg/services/sqlstore/teststore.go`

---

## Phase 2: Session 上下文传播 (P0) ✅
**状态**: ✅ 已完成

- `session.go` — InTransaction + contextTxKey + 嵌套复用
- `store.go` Exec — 自动检测 context 中的事务并复用
- `db.DB` 接口 — 新增 InTransaction 方法
- `session_test.go` — 5 测试用例（提交/回滚/嵌套复用/嵌套回滚/独立 Exec）

---

## Phase 3: Prometheus 连接池指标 (P1)
**状态**: ⏳ 待实施

- 实现 `prometheus.Collector` 暴露连接池 Gauge/Counter
- 指标: max_open / open / in_use / idle / wait_count / wait_duration

---

## Phase 4: OTel Tracing 埋点 (P1)
**状态**: ⏳ 待实施

- 每次 Exec/Tx 创建 OTel Span
- 记录操作类型、事务标记、错误信息

---

## Phase 5: 批量操作 (P2) ✅
**状态**: ✅ 已完成

- `bulk.go` — BulkInsert (GORM CreateInBatches 封装) + InBatches (通用分批器)
- `bulk_test.go` — 8 测试用例（分批/余数/错误中断/非 slice/GORM 集成/InTransaction 组合）

---

## Phase 6: 事件总线 (P2)
**状态**: ⏳ 待实施

- 事务提交后广播事件 (`PublishAfterCommit`)

---

## Phase 7: MySQL 支持 + TLS (P2)
**状态**: ⏳ 待实施

- MySQL Dialector 支持
- MySQL TLS 证书加载
