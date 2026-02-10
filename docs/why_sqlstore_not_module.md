# SQLStore 为什么不是 Module？

## 问题
为什么 `pkg/services/sqlstore` 没有被定义到 `modules` 中？这明明是数据库依赖啊。

## 答案：同步依赖 vs 异步服务

### 1. SQLStore 的特性

```go
type SQLStore struct {
    cfg      *setting.Cfg
    engine   *xorm.Engine  // 数据库连接池
    // ...
}

func ProvideService(cfg *setting.Cfg, ...) (*SQLStore, error) {
    s, err := newStore(cfg, nil, features, migrations, bus, tracer)
    if err != nil {
        return nil, err
    }
    
    // 同步执行迁移
    if err := s.Migrate(s.dbCfg.MigrationLock); err != nil {
        return nil, err
    }
    
    return s, nil  // 返回已就绪的实例
}
```

**关键特征**：
- ✅ 在 `ProvideService` 中**同步初始化完成**（连接池、迁移）
- ✅ 返回的是**已就绪的对象**，可以立即使用
- ❌ **没有** `Run(ctx)` 方法
- ❌ **不需要**后台循环

### 2. dskit Module 的特性

```go
type BackgroundService interface {
    Run(ctx context.Context) error  // 需要持续运行
}
```

**适用场景**：
- HTTP Server（监听端口）
- 定时任务（周期清理）
- 消息队列消费者
- 告警引擎

---

## 架构对比

### 同步依赖（SQLStore 模式）

```
Wire 注入阶段:
┌─────────────────────────────────────┐
│  ProvideService()                   │
│    ├─ 创建连接池                     │
│    ├─ 执行数据库迁移                 │
│    └─ 返回 *SQLStore (已就绪)       │
└─────────────────────────────────────┘
           │
           ▼
┌─────────────────────────────────────┐
│  其他服务注入 *SQLStore              │
│  func ProvideUserService(            │
│      store *SQLStore) *UserService   │
└─────────────────────────────────────┘

运行阶段:
  直接调用 store.Query(...)  // 无需等待启动
```

### 异步服务（Module 模式）

```
Wire 注入阶段:
┌─────────────────────────────────────┐
│  ProvideCleanupService()            │
│    └─ 返回 *CleanupService (未启动) │
└─────────────────────────────────────┘
           │
           ▼
┌─────────────────────────────────────┐
│  注册到 modules.Manager              │
└─────────────────────────────────────┘

运行阶段:
  manager.Run(ctx)
    ├─ Starting: 初始化
    ├─ Running: service.Run(ctx)  // 后台循环
    └─ Stopping: 清理资源
```

---

## 为什么这样设计？

### 1. 启动顺序保证

```go
// Wire 自动解决依赖顺序
func InitializeApp() (*App, error) {
    wire.Build(
        sqlstore.ProvideService,        // 1. 先初始化数据库
        user.ProvideService,            // 2. 再初始化依赖数据库的服务
        cleanup.ProvideCleanupService,  // 3. 最后初始化后台服务
        modules.New,
    )
}
```

**SQLStore 必须在所有依赖它的服务之前就绪**，所以它是同步初始化的。

### 2. 连接池的生命周期

数据库连接池：
- 创建时：建立连接
- 运行时：被动响应查询请求
- 关闭时：调用 `engine.Close()`

**不需要** `Run(ctx)` 循环，因为它是**被动服务**。

---

## 类似的同步依赖

在 Grafana 中，以下组件也是同步依赖，不是 Module：

| 组件 | 原因 |
|------|------|
| `sqlstore.SQLStore` | 数据库连接池 |
| `setting.Cfg` | 配置对象 |
| `log.Logger` | 日志实例 |
| `tracing.Tracer` | Trace 客户端 |
| `bus.Bus` | 事件总线 |

---

## 你的项目应该如何设计？

### 同步依赖（不需要 Module）

```go
// pkg/infra/db/db.go
func ProvideDB(cfg *config.Config) (*gorm.DB, error) {
    db, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{})
    if err != nil {
        return nil, err
    }
    
    // 同步执行迁移
    if err := db.AutoMigrate(&User{}, &DataSource{}); err != nil {
        return nil, err
    }
    
    return db, nil  // 返回已就绪的 DB
}
```

### 异步服务（需要 Module）

```go
// pkg/services/cleanup/cleanup.go
func ProvideCleanupService(
    db *gorm.DB,              // 注入同步依赖
    reg modules.Registry,
) *CleanupService {
    s := &CleanupService{db: db}
    s.NamedService = modules.NewSimpleService("cleanup", s.run)
    
    reg.RegisterInvisibleModule("cleanup", func() (services.Service, error) {
        return s, nil
    })
    return s
}

func (s *CleanupService) run(ctx context.Context) error {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-ticker.C:
            s.db.Exec("DELETE FROM logs WHERE created_at < NOW() - INTERVAL '30 days'")
        }
    }
}
```

---

## 总结

| 类型 | 特征 | 是否需要 Module | 示例 |
|------|------|----------------|------|
| **同步依赖** | 初始化后立即可用，被动响应 | ❌ 否 | DB、Config、Logger |
| **异步服务** | 需要后台循环，主动执行任务 | ✅ 是 | HTTP Server、定时任务、消费者 |

**SQLStore 是同步依赖，不是异步服务，所以不需要注册到 modules 中。**
