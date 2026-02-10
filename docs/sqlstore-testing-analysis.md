# Grafana SQLStore 测试体系深度解析报告

## 1. 整体架构概览

`pkg/infra/db/` 是 Grafana 数据库层的**公共门面 (facade)**，它并不包含核心实现，而是将所有功能委托给 `pkg/services/sqlstore/`。整个测试体系分为三层。

### 1.1 目录结构

```
pkg/infra/db/
├── db.go              # 公共接口 + 测试辅助函数的转发层
├── sqlbuilder.go      # SQL 构建器 (带 RBAC 权限过滤)
└── dbtest/
    └── dbtest.go      # FakeDB 桩对象 (用于纯单元测试)

pkg/services/sqlstore/
├── sqlstore.go              # SQLStore 核心实现 + 旧版测试基础设施
├── sqlstore_testinfra.go    # 新版并行安全测试基础设施 (NewTestStore)
├── session.go               # xorm 会话管理 (DBSession)
├── transactions.go          # 事务管理 + SQLite 重试
├── database_config.go       # 数据库连接配置
├── database_wrapper.go      # SQL hooks (instrumentation/tracing)
├── bulk.go                  # 批量插入工具
└── sqlutil/
    └── sqlutil.go           # TestDB 配置 (SQLite/MySQL/Postgres 连接信息)

pkg/services/sqlstore/session/
└── session.go               # sqlx 会话层 (SessionDB / SessionTx)

pkg/tests/testsuite/
└── testsuite.go             # TestMain 辅助封装 (SetupTestDB/CleanupTestDB)
```

### 1.2 模块依赖关系

```mermaid
graph TB
    subgraph "公共门面层 pkg/infra/db"
        DB_IFACE["db.DB 接口"]
        DB_GO["db.go<br/>InitTestDB / IsTestDbXxx"]
        SQLBUILDER["sqlbuilder.go<br/>SQLBuilder + RBAC 过滤"]
        FAKEDB["dbtest/dbtest.go<br/>FakeDB 桩对象"]
    end

    subgraph "核心实现层 pkg/services/sqlstore"
        SQLSTORE["sqlstore.go<br/>SQLStore 结构体<br/>ProvideService / initTestDB"]
        TESTINFRA["sqlstore_testinfra.go<br/>NewTestStore (新版)"]
        SESSION["session.go<br/>DBSession / WithDbSession"]
        TXN["transactions.go<br/>InTransaction / 重试逻辑"]
        SQLUTIL["sqlutil/sqlutil.go<br/>ITestDB / TestDB / GetTestDB"]
        DBCFG["database_config.go<br/>DatabaseConfig"]
    end

    subgraph "辅助层"
        TESTSUITE["pkg/tests/testsuite<br/>testsuite.Run(m)"]
        MIGRATOR["migrator<br/>数据库迁移"]
    end

    subgraph "外部依赖"
        XORM["xorm Engine"]
        SQLX["sqlx DB"]
    end

    DB_GO -->|委托| SQLSTORE
    DB_GO -->|委托| SQLUTIL
    DB_IFACE -.->|实现| SQLSTORE
    DB_IFACE -.->|实现| FAKEDB
    SQLBUILDER -->|使用| MIGRATOR

    SQLSTORE -->|使用| SESSION
    SQLSTORE -->|使用| TXN
    SQLSTORE -->|使用| DBCFG
    SQLSTORE -->|使用| MIGRATOR
    SQLSTORE -->|使用| SQLUTIL
    TESTINFRA -->|使用| SQLSTORE

    TESTSUITE -->|调用| DB_GO

    SESSION -->|包装| XORM
    SQLSTORE -->|创建| SQLX
    SQLSTORE -->|创建| XORM
```

---

## 2. 核心接口: `db.DB`

定义于 `pkg/infra/db/db.go:18-49`，这是所有数据库消费者依赖的接口：

```go
type DB interface {
    WithTransactionalDbSession(ctx context.Context, callback sqlstore.DBTransactionFunc) error
    WithDbSession(ctx context.Context, callback sqlstore.DBTransactionFunc) error
    GetDialect() migrator.Dialect
    GetDBType() core.DbType
    GetEngine() *xorm.Engine
    GetSqlxSession() *session.SessionDB
    InTransaction(ctx context.Context, fn func(ctx context.Context) error) error
    Quote(value string) string
    RecursiveQueriesAreSupported() (bool, error)
}
```

这个接口是**测试策略的分水岭** -- 所有测试要么注入一个真实的 `*sqlstore.SQLStore`，要么注入一个 `*dbtest.FakeDB`。

### 2.1 接口实现关系

```mermaid
classDiagram
    class DB {
        <<interface>>
        +WithTransactionalDbSession(ctx, callback) error
        +WithDbSession(ctx, callback) error
        +GetDialect() Dialect
        +GetDBType() DbType
        +GetEngine() *Engine
        +GetSqlxSession() *SessionDB
        +InTransaction(ctx, fn) error
        +Quote(value) string
        +RecursiveQueriesAreSupported() (bool, error)
    }

    class SQLStore {
        -cfg *Cfg
        -engine *xorm.Engine
        -dialect Dialect
        -dbCfg DatabaseConfig
        -sqlxsession *SessionDB
        -log Logger
        -tracer Tracer
        +Migrate(isDatabaseLockingEnabled) error
        +Reset() error
    }

    class FakeDB {
        +ExpectedError error
    }

    class ITestDB {
        <<interface>>
        +Helper()
        +Fatalf(format, args)
        +Logf(format, args)
        +Log(args)
        +Cleanup(func)
        +Skipf(format, args)
    }

    class TestDB {
        +DriverName string
        +ConnStr string
        +Path string
        +Host string
        +Port string
        +User string
        +Password string
        +Database string
        +Cleanup func
    }

    DB <|.. SQLStore : 生产/集成测试实现
    DB <|.. FakeDB : 单元测试桩实现
    ITestDB <|.. testing.T : 满足
    ITestDB <|.. testing.B : 满足
    SQLStore --> TestDB : 测试时使用
```

---

## 3. 两大测试策略

### 3.1 测试决策流程

```mermaid
flowchart TD
    START["需要编写测试"] --> Q1{"需要真实<br/>数据库操作?"}

    Q1 -->|否| FAKE["策略 B: dbtest.FakeDB<br/>纯单元测试"]
    Q1 -->|是| Q2{"需要并行安全?"}

    Q2 -->|否| OLD["策略 A-1: db.InitTestDB<br/>旧版集成测试"]
    Q2 -->|是| NEW["策略 A-2: sqlstore.NewTestStore<br/>新版集成测试"]

    FAKE --> FAKE_DETAIL["- 不需要 TestMain<br/>- ExpectedError 模拟错误<br/>- 极快, 无 I/O<br/>- 任意函数名"]

    OLD --> OLD_DETAIL["- 需要 TestMain + testsuite.Run<br/>- 函数名 TestIntegration 开头<br/>- 全局共享, truncate 隔离<br/>- 不可并行"]

    NEW --> NEW_DETAIL["- 不需要 TestMain<br/>- 每个测试独立数据库<br/>- 函数式选项<br/>- 可安全并行"]

    style FAKE fill:#d4edda,stroke:#28a745
    style OLD fill:#fff3cd,stroke:#ffc107
    style NEW fill:#cce5ff,stroke:#007bff
```

### 3.2 策略 A: 集成测试 -- `db.InitTestDB` (真实数据库)

| 维度         | 说明                                                          |
| ------------ | ------------------------------------------------------------- |
| **入口**     | `db.InitTestDB(t)` 或 `db.InitTestDBWithCfg(t)`               |
| **数据库**   | 默认 SQLite，通过 `GRAFANA_TEST_DB` 切换 MySQL/Postgres/MSSQL |
| **命名规范** | 函数名必须以 `TestIntegration` 开头                           |
| **保护守卫** | 必须调用 `testutil.SkipIntegrationTestInShortMode(t)`         |
| **TestMain** | 必须在包级 `TestMain` 中调用 `testsuite.Run(m)`               |
| **隔离机制** | 全局单例 + mutex + 每次测试前 truncate 所有表                 |
| **并行安全** | 否 (共享全局状态)                                             |

#### 旧版 InitTestDB 工作流程

```mermaid
sequenceDiagram
    participant TM as TestMain
    participant TS as testsuite.Run
    participant DB as db.SetupTestDB
    participant T as TestIntegrationXxx
    participant INIT as sqlstore.initTestDB
    participant ENGINE as xorm.Engine

    TM->>TS: testsuite.Run(m)
    TS->>DB: db.SetupTestDB()
    DB->>DB: testSQLStoreSetup = true

    TS->>TM: m.Run()

    Note over T: 第一个测试
    T->>INIT: db.InitTestDB(t)
    INIT->>INIT: mutex.Lock()
    INIT->>INIT: 检查 testSQLStoreSetup == true
    INIT->>INIT: 读取 GRAFANA_TEST_DB 环境变量
    INIT->>INIT: sqlutil.GetTestDB(dbType)
    INIT->>ENGINE: xorm.NewEngine(driver, connStr)
    INIT->>INIT: newStore(cfg, engine)
    INIT->>ENGINE: RunMigrations (OSS)
    INIT->>INIT: testSQLStore = store (缓存)
    INIT->>ENGINE: TRUNCATE 所有表
    INIT->>INIT: Reset() (重建默认 org/admin)
    INIT-->>T: 返回 *SQLStore

    Note over T: 后续测试 (复用引擎)
    T->>INIT: db.InitTestDB(t)
    INIT->>INIT: mutex.Lock()
    INIT->>INIT: testSQLStore != nil, 跳过创建
    INIT->>ENGINE: TRUNCATE 所有表
    INIT->>INIT: Reset()
    INIT-->>T: 返回同一个 *SQLStore

    TM->>TS: 所有测试完成
    TS->>DB: db.CleanupTestDB()
    DB->>ENGINE: engine.Close()
    DB->>DB: 执行 cleanup 函数 (删除临时 SQLite 文件)
    DB->>DB: 重置所有全局状态
```

#### 典型用法示例

```go
// pkg/services/dashboardsnapshots/database/database_test.go

func TestMain(m *testing.M) {
    testsuite.Run(m) // 必须! 否则 InitTestDB 会 Fatal
}

func TestIntegrationDashboardSnapshotDBAccess(t *testing.T) {
    testutil.SkipIntegrationTestInShortMode(t)

    sqlstore := db.InitTestDB(t)
    dashStore := ProvideStore(sqlstore, setting.NewCfg())

    t.Run("Should be able to get snapshot by key", func(t *testing.T) {
        cmd := dashboardsnapshots.CreateDashboardSnapshotCommand{
            Key: "hej", DashboardEncrypted: encData, UserID: 1000, OrgID: 1,
        }
        result, err := dashStore.CreateDashboardSnapshot(context.Background(), &cmd)
        require.NoError(t, err)

        query := dashboardsnapshots.GetDashboardSnapshotQuery{Key: "hej"}
        queryResult, err := dashStore.GetDashboardSnapshot(context.Background(), &query)
        require.NoError(t, err)
        assert.NotNil(t, queryResult)
    })
}
```

#### 全局状态管理

`sqlstore.go` 中维护的关键全局变量:

```go
var testSQLStoreSetup = false          // SetupTestDB 是否已调用
var testSQLStore *SQLStore             // 全局共享的测试 SQLStore 单例
var testSQLStoreMutex sync.Mutex       // 保护并发访问
var testSQLStoreCleanup []func()       // CleanupTestDB 时执行的清理函数
```

---

### 3.3 策略 A-2: 新版集成测试 -- `NewTestStore` (并行安全)

定义于 `pkg/services/sqlstore/sqlstore_testinfra.go`，是旧版 `InitTestDB` 的全面升级。

| 对比维度      | 旧版 `InitTestDB`                  | 新版 `NewTestStore`               |
| ------------- | ---------------------------------- | --------------------------------- |
| 数据库隔离    | 全局单例 + truncate                | **每个测试独立数据库**            |
| 并行安全      | 否 (全局 mutex)                    | **是**                            |
| 需要 TestMain | 是                                 | **否**                            |
| 选项风格      | `InitTestDBOpt{FeatureFlags, Cfg}` | 函数式选项                        |
| Truncate      | 默认开启                           | **按需开启** (`WithTruncation()`) |
| Migration     | 总是运行 OSS                       | 可配置                            |

#### NewTestStore 工作流程

```mermaid
sequenceDiagram
    participant T as TestIntegrationXxx
    participant NTS as NewTestStore
    participant CTD as createTemporaryDatabase
    participant ENGINE as xorm.Engine
    participant CLEANUP as t.Cleanup()

    T->>NTS: sqlstore.NewTestStore(t, opts...)
    NTS->>NTS: 解析函数式选项
    NTS->>CTD: createTemporaryDatabase(tb)

    alt SQLite
        CTD->>CTD: 创建临时文件
        CTD->>CLEANUP: 注册删除临时文件
    else MySQL / Postgres
        CTD->>ENGINE: CREATE DATABASE grafana_test_<random_hex>
        CTD->>CLEANUP: 注册 DROP DATABASE
    end

    CTD-->>NTS: 返回 TestDB 连接信息
    NTS->>ENGINE: xorm.NewEngine(driver, connStr)
    NTS->>NTS: newStore(cfg, engine)

    alt 有 Migrator
        NTS->>ENGINE: 运行 migrations
    end

    alt WithTruncation
        NTS->>ENGINE: TRUNCATE 所有表
    end

    NTS->>CLEANUP: 注册 engine.Close()
    NTS-->>T: 返回 *SQLStore (独立实例)

    Note over T: 测试结束
    T->>CLEANUP: 自动执行
    CLEANUP->>ENGINE: engine.Close()
    CLEANUP->>ENGINE: DROP DATABASE (如果非 SQLite)
```

#### 可用的函数式选项

```go
WithFeatureFlags(flags map[string]bool)     // 设置 feature flags
WithFeatureFlag(flag string)                 // 开启单个 feature flag
WithoutFeatureFlags(flags ...string)         // 关闭指定 feature flags
WithOSSMigrations()                          // 使用 OSS 迁移 (默认)
WithMigrator(factory)                        // 自定义迁移器
WithoutMigrator()                            // 不运行迁移
WithTracer(tracer)                           // 自定义 tracer
WithoutDefaultOrgAndUser()                   // 不创建默认 org/admin
WithCfg(cfg)                                 // 自定义配置
WithTruncation()                             // 启用 truncate
```

#### 典型用法示例

```go
// pkg/services/sqlstore/sqlstore_testinfra_test.go

func TestIntegrationTempDatabaseConnect(t *testing.T) {
    testutil.SkipIntegrationTestInShortMode(t)
    store := sqlstore.NewTestStore(t, sqlstore.WithoutMigrator())
    // 每个测试独享数据库, 可安全并行
    err := store.WithDbSession(context.Background(), func(sess *db.Session) error {
        _, err := sess.Exec("SELECT 1")
        return err
    })
    require.NoError(t, err)
}
```

---

### 3.4 策略 B: 单元测试 -- `dbtest.FakeDB` (桩对象)

| 维度         | 说明                                       |
| ------------ | ------------------------------------------ |
| **入口**     | `dbtest.NewFakeDB()` 或 `&dbtest.FakeDB{}` |
| **数据库**   | 无 -- 所有方法返回零值或 `ExpectedError`   |
| **命名规范** | 无特殊要求                                 |
| **TestMain** | 不需要                                     |
| **速度**     | 极快 (无 I/O)                              |
| **用途**     | 测试业务逻辑、HTTP handler、服务编排       |

#### FakeDB 实现

```go
// pkg/infra/db/dbtest/dbtest.go

type FakeDB struct {
    ExpectedError error  // 唯一可配置字段
}

func NewFakeDB() *FakeDB { return &FakeDB{} }

// 所有接口方法都直接返回 ExpectedError (默认 nil)
func (f *FakeDB) WithTransactionalDbSession(ctx, callback) error { return f.ExpectedError }
func (f *FakeDB) WithDbSession(ctx, callback) error              { return f.ExpectedError }
func (f *FakeDB) InTransaction(ctx, fn) error                    { return f.ExpectedError }

// 所有 getter 返回零值
func (f *FakeDB) GetDBType() core.DbType        { return "" }
func (f *FakeDB) GetDialect() migrator.Dialect   { return nil }
func (f *FakeDB) GetEngine() *xorm.Engine        { return nil }
func (f *FakeDB) GetSqlxSession() *session.SessionDB { return nil }
func (f *FakeDB) Quote(value string) string      { return "" }
```

#### FakeDB 使用模式

```mermaid
flowchart LR
    subgraph "单元测试"
        TEST["TestXxx(t)"]
        FAKE["dbtest.NewFakeDB()"]
        SVC["被测服务"]
    end

    TEST -->|创建| FAKE
    TEST -->|注入| SVC
    FAKE -->|实现 db.DB| SVC

    subgraph "FakeDB 行为"
        DEFAULT["默认: 所有操作返回 nil"]
        ERROR["设置 ExpectedError:<br/>所有操作返回指定错误"]
    end

    FAKE --- DEFAULT
    FAKE --- ERROR

    style FAKE fill:#d4edda,stroke:#28a745
    style DEFAULT fill:#f8f9fa,stroke:#6c757d
    style ERROR fill:#f8d7da,stroke:#dc3545
```

#### 典型用法示例

**示例 1: 模拟数据库故障 (Health API)**

```go
// pkg/api/health_test.go
func TestHealthAPI_DatabaseUnhealthy(t *testing.T) {
    m, hs := setupHealthAPITestEnvironment(t)
    // 注入数据库错误
    hs.SQLStore.(*dbtest.FakeDB).ExpectedError = errors.New("bad")

    req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
    rec := httptest.NewRecorder()
    m.ServeHTTP(rec, req)

    require.Equal(t, 503, rec.Code)
    require.JSONEq(t, `{"database": "failing"}`, rec.Body.String())
}
```

**示例 2: 作为构造依赖占位 (Search Service)**

```go
// pkg/services/search/service_test.go
func TestSearch_SortedResults(t *testing.T) {
    db := dbtest.NewFakeDB()  // DB 只是构造依赖, 不会被真正调用
    ds := dashboards.NewFakeDashboardService(t)
    ds.On("SearchDashboards", mock.Anything, mock.Anything).Return(hits, nil)

    svc := &SearchService{sqlstore: db, dashboardService: ds}
    results, err := svc.SearchHandler(context.Background(), query)
    require.Nil(t, err)
    assert.Equal(t, "AABB", results[1].Title)
}
```

**示例 3: 预设特定错误 (Plugin Context)**

```go
// pkg/services/pluginsintegration/plugincontext/plugincontext_test.go
func TestGet(t *testing.T) {
    // 预设 "插件设置未找到" 错误
    db := &dbtest.FakeDB{ExpectedError: pluginsettings.ErrPluginSettingNotFound}
    pcp := plugincontext.ProvideService(cfg, cache, store, cacheService,
        dsService, pluginSettings.ProvideService(db, secretsService), reqCfg)

    pCtx, err := pcp.Get(context.Background(), "plugin-id", identity, orgID)
    require.NoError(t, err) // 上层服务能正确处理 "未找到" 错误
}
```

---

## 4. 多数据库 CI 测试矩阵

### 4.1 环境变量驱动机制

```mermaid
flowchart TD
    ENV["环境变量 GRAFANA_TEST_DB"]

    ENV -->|未设置 / 'sqlite'| SQLITE["SQLite<br/>临时文件, WAL 模式<br/>cache=private"]
    ENV -->|'mysql'| MYSQL["MySQL 8.0<br/>grafana:password<br/>@tcp(localhost)/grafana_tests"]
    ENV -->|'postgres'| PG["PostgreSQL<br/>grafanatest:grafanatest<br/>@localhost/grafanatest"]
    ENV -->|'mssql'| MSSQL["MS SQL Server"]

    subgraph "检测函数 (db.go:67-97)"
        IS_SQLITE["IsTestDbSQLite()"]
        IS_MYSQL["IsTestDbMySQL()"]
        IS_PG["IsTestDbPostgres()"]
        IS_MSSQL["IsTestDBMSSQL()"]
    end

    SQLITE --- IS_SQLITE
    MYSQL --- IS_MYSQL
    PG --- IS_PG
    MSSQL --- IS_MSSQL

    subgraph "连接配置 (sqlutil.go)"
        SQLITE_CFG["sqLite3TestDB()<br/>file:tmpdir/test.db?<br/>journal_mode=WAL&<br/>synchronous=OFF"]
        MYSQL_CFG["mySQLTestDB()<br/>grafana:password@tcp<br/>(localhost:3306)/grafana_tests<br/>?parseTime=true"]
        PG_CFG["postgresTestDB()<br/>user=grafanatest<br/>password=grafanatest<br/>host=localhost<br/>sslmode=disable"]
    end

    SQLITE --> SQLITE_CFG
    MYSQL --> MYSQL_CFG
    PG --> PG_CFG

    style SQLITE fill:#e8f5e9,stroke:#4caf50
    style MYSQL fill:#fff3e0,stroke:#ff9800
    style PG fill:#e3f2fd,stroke:#2196f3
    style MSSQL fill:#fce4ec,stroke:#f44336
```

### 4.2 CI 执行策略

```mermaid
flowchart TB
    subgraph "GitHub Actions: pr-test-integration.yml"
        direction TB

        TRIGGER["PR 触发"]

        subgraph "MySQL Job (16 shards)"
            MYSQL_SVC["Service: mysql:8.0.43<br/>MYSQL_DATABASE=grafana_tests<br/>MYSQL_USER=grafana"]
            MYSQL_RUN["GRAFANA_TEST_DB=mysql<br/>go test -p=1 -run '^TestIntegration'<br/>-timeout=8m"]
        end

        subgraph "Postgres Job (16 shards)"
            PG_SVC["Service: postgres<br/>POSTGRES_USER=grafanatest"]
            PG_RUN["GRAFANA_TEST_DB=postgres<br/>go test -p=1 -run '^TestIntegration'<br/>-timeout=8m"]
        end

        TRIGGER --> MYSQL_SVC --> MYSQL_RUN
        TRIGGER --> PG_SVC --> PG_RUN
    end

    subgraph "Makefile 目标"
        MAKE_PG["make test-go-integration-postgres<br/>先启动 devenv-postgres"]
        MAKE_MYSQL["make test-go-integration-mysql<br/>先启动 devenv-mysql"]
    end

    subgraph "关键参数"
        P1["-p=1 (串行执行包)"]
        P2["-count=1 (不缓存)"]
        P3["-run '^TestIntegration'"]
        P4["-timeout=10m"]
    end
```

### 4.3 本地运行集成测试

```bash
# SQLite (默认, 无需额外配置)
go test -run "^TestIntegration" ./pkg/services/...

# MySQL (需要本地 MySQL 服务)
GRAFANA_TEST_DB=mysql go test -p=1 -run "^TestIntegration" ./pkg/services/...

# PostgreSQL (需要本地 Postgres 服务)
GRAFANA_TEST_DB=postgres go test -p=1 -run "^TestIntegration" ./pkg/services/...

# 使用 Makefile (自动启动 devenv 容器)
make test-go-integration-mysql
make test-go-integration-postgres
```

---

## 5. `SQLBuilder` -- 带权限过滤的 SQL 构建器

`pkg/infra/db/sqlbuilder.go` 提供了一个服务于 Dashboard 查询场景的 SQL 拼接工具。

### 5.1 结构与方法

```mermaid
classDiagram
    class SQLBuilder {
        -cfg *setting.Cfg
        -features FeatureToggles
        -sql bytes.Buffer
        -params []any
        -leftJoin string
        -recQry string
        -recQryParams []any
        -recursiveQueriesAreSupported bool
        -dialect Dialect
        +Write(sql string, params ...any)
        +GetSQLString() string
        +GetParams() []any
        +AddParams(params ...any)
        +WriteDashboardPermissionFilter(user, permission, queryType)
    }

    class AccessControlDashboardPermissionFilter {
        +LeftJoin() string
        +Where() (string, []any)
        +With() (string, []any)
    }

    SQLBuilder --> AccessControlDashboardPermissionFilter : WriteDashboardPermissionFilter 调用
```

### 5.2 SQL 生成流程

`GetSQLString()` 最终拼接的 SQL 结构:

```sql
-- recQry (递归 CTE, 由 WriteDashboardPermissionFilter 注入)
WITH RECURSIVE ... AS (...)
-- sql (主查询, 由 Write() 累积)
SELECT ... FROM dashboard WHERE ...
-- leftJoin (由 WriteDashboardPermissionFilter 注入)
LEFT OUTER JOIN ...
-- WHERE 条件 (由 WriteDashboardPermissionFilter 追加 AND 子句)
AND <RBAC 权限过滤条件>
```

---

## 6. 事务与会话管理

### 6.1 会话层次结构

```mermaid
flowchart TB
    subgraph "pkg/services/sqlstore/session.go"
        DBS["DBSession<br/>包装 xorm.Session<br/>+ transactionOpen bool<br/>+ events []any"]
        DBTF["DBTransactionFunc<br/>func(sess *DBSession) error"]
    end

    subgraph "pkg/services/sqlstore/session/session.go"
        SDB["SessionDB<br/>包装 sqlx.DB"]
        STX["SessionTx<br/>包装 sqlx.Tx"]
    end

    subgraph "pkg/services/sqlstore/transactions.go"
        WTS["WithTransactionalDbSession<br/>开启事务会话"]
        IT["InTransaction<br/>事务放入 context"]
        RETRY["inTransactionWithRetryCtx<br/>SQLite 锁重试 (最多 n 次)"]
    end

    subgraph "调用方"
        CALLER["业务代码"]
    end

    CALLER -->|"db.InTransaction(ctx, fn)"| IT
    IT --> RETRY
    RETRY -->|创建| DBS
    RETRY -->|存入 context| DBS

    CALLER -->|"db.WithDbSession(ctx, cb)"| DBS
    DBS -->|"检查 context 中是否有事务"| DBS

    CALLER -->|"db.WithTransactionalDbSession"| WTS
    WTS --> DBS

    SDB -->|"Beginx()"| STX
```

### 6.2 事务重试机制 (SQLite)

```mermaid
sequenceDiagram
    participant C as 调用方
    participant IT as InTransaction
    participant RETRY as inTransactionWithRetryCtx
    participant SESS as DBSession
    participant DB as SQLite

    C->>IT: InTransaction(ctx, fn)
    IT->>RETRY: inTransactionWithRetryCtx(ctx, engine, fn)

    loop 最多 TransactionRetries 次
        RETRY->>SESS: 创建新 Session
        RETRY->>SESS: Begin Transaction
        RETRY->>C: fn(ctx) -- 执行业务逻辑
        alt 成功
            RETRY->>SESS: Commit
            RETRY->>C: 发布 events (PublishAfterCommit)
            RETRY-->>C: return nil
        else SQLite database locked
            RETRY->>SESS: Rollback
            Note over RETRY: 等待后重试
        else 其他错误
            RETRY->>SESS: Rollback
            RETRY-->>C: return error
        end
    end
```

---

## 7. 完整测试生命周期

### 7.1 集成测试完整流程

```mermaid
flowchart TD
    subgraph "测试包 TestMain"
        A1["func TestMain(m *testing.M)"]
        A2["testsuite.Run(m)"]
        A3["db.SetupTestDB()"]
        A4["testSQLStoreSetup = true"]
        A5["m.Run()"]
        A6["db.CleanupTestDB()"]
        A7["关闭引擎 + 删除临时文件"]
    end

    subgraph "单个测试函数"
        B1["func TestIntegrationXxx(t)"]
        B2["testutil.SkipIntegrationTestInShortMode(t)"]
        B3["db.InitTestDB(t)"]
        B4{"testSQLStore<br/>== nil ?"}
        B5["创建引擎 + 运行迁移<br/>缓存到 testSQLStore"]
        B6["TRUNCATE 所有表<br/>重置序列"]
        B7["Reset(): 重建默认 org/admin"]
        B8["返回 *SQLStore"]
        B9["执行测试逻辑<br/>真实 SQL 读写"]
    end

    A1 --> A2 --> A3 --> A4 --> A5
    A5 --> B1
    B1 --> B2 --> B3 --> B4
    B4 -->|是| B5 --> B6
    B4 -->|否| B6
    B6 --> B7 --> B8 --> B9
    B9 --> A5
    A5 --> A6 --> A7

    style B5 fill:#fff3cd,stroke:#ffc107
    style B6 fill:#f8d7da,stroke:#dc3545
    style B9 fill:#d4edda,stroke:#28a745
```

### 7.2 两代测试基础设施对比

```mermaid
graph LR
    subgraph "旧版 InitTestDB"
        OLD_TM["TestMain 必须"]
        OLD_SINGLE["全局单例 SQLStore"]
        OLD_TRUNC["每次 TRUNCATE 全表"]
        OLD_MUTEX["sync.Mutex 保护"]
        OLD_SEQ["串行执行"]

        OLD_TM --> OLD_SINGLE --> OLD_TRUNC --> OLD_MUTEX --> OLD_SEQ
    end

    subgraph "新版 NewTestStore"
        NEW_NO_TM["无需 TestMain"]
        NEW_MULTI["每测试独立 DB"]
        NEW_CLEANUP["t.Cleanup() 自动清理"]
        NEW_OPTS["函数式选项"]
        NEW_PARALLEL["可安全并行"]

        NEW_NO_TM --> NEW_MULTI --> NEW_CLEANUP --> NEW_OPTS --> NEW_PARALLEL
    end

    style OLD_SEQ fill:#fff3cd,stroke:#ffc107
    style NEW_PARALLEL fill:#d4edda,stroke:#28a745
```

---

## 8. 总结

### 8.1 核心设计理念

1. **接口隔离** -- `db.DB` 接口使得真实实现 (`SQLStore`) 和桩对象 (`FakeDB`) 可互换，测试策略由注入决定
2. **环境变量驱动** -- 同一套测试代码通过 `GRAFANA_TEST_DB` 运行在不同数据库上，无需修改测试代码
3. **约定优于配置** -- `TestIntegration` 前缀 + `testsuite.Run` 的固定模式，CI 通过 `-run "^TestIntegration"` 精确筛选
4. **渐进演进** -- 旧版 `InitTestDB` (全局单例) 和新版 `NewTestStore` (每测试隔离) 两套共存，逐步迁移

### 8.2 选型指南

| 场景                      | 推荐策略                          | 理由                            |
| ------------------------- | --------------------------------- | ------------------------------- |
| HTTP handler / API 层测试 | `dbtest.FakeDB`                   | DB 只是构造依赖，不会被真正调用 |
| 服务编排 / 业务逻辑测试   | `dbtest.FakeDB`                   | 关注逻辑分支，不关注 SQL        |
| 数据库错误处理测试        | `dbtest.FakeDB` + `ExpectedError` | 精确控制错误场景                |
| SQL 查询正确性验证        | `db.InitTestDB` 或 `NewTestStore` | 需要真实 SQL 执行               |
| 数据库迁移测试            | `NewTestStore` + `WithMigrator()` | 需要独立 schema                 |
| 事务/并发测试             | `NewTestStore`                    | 需要并行安全的独立数据库        |
| 跨数据库兼容性验证        | `db.InitTestDB` + CI 矩阵         | 通过 `GRAFANA_TEST_DB` 切换     |
