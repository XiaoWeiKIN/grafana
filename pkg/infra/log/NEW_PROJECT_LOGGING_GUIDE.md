# 新项目日志最佳实践推荐

如果是一个**全新的项目**，没有历史包袱，我**强烈不推荐**使用 Grafana 这种“全局单例 + 自定义封装”的模式。

推荐采用 **“显式依赖注入 + 结构化日志接口”** 的方案。

## 1. 核心原则

1.  **显式传递 (Dependency Injection)**：不要在代码深处调用 `GetGlobalLogger()`。Logger 应该作为结构体的一个字段，或者函数的参数传入。
2.  **结构化 (Structured)**：必须是 Key-Value 对，方便后续导入 ES/Loki 进行查询。
3.  **接口抽象 (Interface Abstraction)**：不要直接依赖 `zap.Logger` 结构体，而是依赖一个接口（如 Go 1.21+ 标准库 `log/slog` 或自定义接口）。

## 2. 推荐方案：使用 Go 1.21+ `log/slog`

Go 1.21 引入了官方的结构化日志库 `log/slog`。它是目前新项目的**首选**。

### 2.1 为什么选 `slog`？
*   **标准库**：无需引入第三方依赖，所有 Go 开发者都懂。
*   **高性能**：设计时借鉴了 Zap 的优化思路。
*   **生态统一**：库作者可以用 `slog` 打印日志，应用开发者可以决定底层是用 `JSON` 输出还是发给 `Zap` 处理。

### 2.2 代码范式

**定义 Service (依赖注入):**

```go
import "log/slog"

type UserService struct {
    // 依赖接口，而不是具体实现
    logger *slog.Logger
    db     *sql.DB
}

// 构造函数强制要求传入 Logger
func NewUserService(logger *slog.Logger, db *sql.DB) *UserService {
    // 可以在这里绑定 component 属性，这样该 service 的所有日志都会带上 component=user_service
    childLogger := logger.With("component", "user_service")
    return &UserService{
        logger: childLogger,
        db:     db,
    }
}

func (s *UserService) CreateUser(ctx context.Context, name string) error {
    // 使用 Context 里的 TraceID (如果集成了链路追踪)
    // 强类型 KV，高性能
    s.logger.InfoContext(ctx, "creating new user", 
        "name", name, 
        "attempt", 1,
    )
    return nil
}
```

**在 `main.go` 中组装 (Wire):**

```go
func main() {
    // 1. 初始化具体的 Logger 实现 (比如生产环境用 JSON)
    handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    })
    logger := slog.New(handler)

    // 2. 注入依赖
    svc := NewUserService(logger, db)
    
    // 3. 运行
    svc.CreateUser(context.Background(), "alice")
}
```

## 3. 进阶方案：Zap 作为 Slog 的后端

如果你需要极致性能（Zap v1.27+ 稍微比 Slog 快一点点）或者 Zap 丰富的功能（比如各种 Encoder），你可以：
1.  **业务代码**：依然只依赖 `*slog.Logger`。
2.  **启动代码**：使用 `zap` 来实现 `slog` 的 Handler。

```go
import (
    "go.uber.org/zap"
    "go.uber.org/zap/exp/slog" // Zap 官方适配器
    "log/slog"
)

func main() {
    // 初始化 Zap
    zapLogger := zap.NewProduction()
    
    // 将 Zap 转换为 Slog Logger
    logger := slog.New(zapslog.NewHandler(zapLogger.Core(), nil))
    
    // 传入你的 Service
    NewUserService(logger, ...)
}
```

## 4. 总结：新项目“黄金法则”

| 维度 | 建议 | 理由 |
| :--- | :--- | :--- |
| **API 选择** | **`log/slog`** | 标准库，未来趋势，零第三方依赖。 |
| **传递方式** | **构造函数注入** | 方便测试（单测时可以传个 No-op logger），依赖关系清晰。 |
| **全局变量** | **禁止 (或仅在 main 使用)** | 避免副作用，避免“不知道这行日志是谁打的”问题。 |
| **Context** | **必须传递 `ctx`** | `InfoContext(ctx, ...)` 配合 OTEL 可以自动关联 TraceID/SpanID。 |

这是目前 Go 社区最推崇的现代化工程实践。
