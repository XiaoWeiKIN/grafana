# dskit: Module & Service 详解与实战指南

`dskit` (Distributed Systems Kit) 是 Grafana Labs 开发的一套用于构建分布式系统的库（核心源自 Cortex）。它的核心是 **Service**（生命周期管理）和 **Module**（依赖注入与编排）。

## 1. 核心概念

### 1.1 Service (服务)
`Service` 是一个具有生命周期的组件。它定义了组件如何**启动**、**运行**和**停止**。

-   **状态机**：`New` -> `Starting` -> `Running` -> `Stopping` -> `Terminated` (或 `Failed`)。
-   **类型**：
    -   `NewBasicService`: 只有 `Starting`, `Running`, `Stopping` 三个回调。
    -   `NewTimerService`: 定期执行任务的服务。
    -   `Manager`: 管理一组 Service 的服务。

### 1.2 Module (模块)
`Module` 是对 `Service` 的一种高级封装，用于处理**依赖关系**。

-   **解耦**：模块是一个名字（String）和一个工厂函数（Factory Function）。
-   **依赖管理**：模块 A 可以声明依赖模块 B。Manager 会确保先启动 B，再启动 A。
-   **延迟初始化**：只有当某个模块被需要（是 Target 或 Target 的依赖）时，它才会被初始化。

---

## 2. 完整实战 Demo

这个 Demo 模拟了一个简单的系统，包含三个模块：
1.  **Database**: 模拟数据库连接池（基础依赖）。
2.  **UserStore**: 依赖数据库，提供用户存储功能。
3.  **ApiServer**: 依赖 UserStore，提供 HTTP 服务（顶层应用）。

### 2.1 代码实现 (`main.go`)

你可以将以下代码保存为 `main.go` 并运行。

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grafana/dskit/modules"
	"github.com/grafana/dskit/services"
)

// ==========================================
// 1. 定义模块名称 (常量最佳)
// ==========================================
const (
	ModDatabase  = "database"
	ModUserStore = "user-store"
	ModApiServer = "api-server"
)

// ==========================================
// 2. 定义具体的服务 (实现业务逻辑)
// ==========================================

// --- Database Service ---
type DBService struct {
	services.Service // 嵌入接口，自动获得基础能力
}

func NewDBService() *DBService {
	s := &DBService{}
	// 使用 NewBasicService 包装核心逻辑
	s.Service = services.NewBasicService(s.starting, s.running, s.stopping)
	return s
}

func (s *DBService) starting(ctx context.Context) error {
	fmt.Println("[DB] Connecting to database...")
	time.Sleep(500 * time.Millisecond) // 模拟耗时
	fmt.Println("[DB] Connected!")
	return nil
}

func (s *DBService) running(ctx context.Context) error {
	fmt.Println("[DB] Ready for queries.")
	<-ctx.Done() // 阻塞直到上下文取消
	return nil
}

func (s *DBService) stopping(_ error) error {
	fmt.Println("[DB] Closing connection...")
	return nil
}

// --- API Server Service ---
type ApiServer struct {
	services.Service
}

func NewApiServer() *ApiServer {
	s := &ApiServer{}
	s.Service = services.NewBasicService(nil, s.running, nil) // starting/stopping 可选
	return s
}

func (s *ApiServer) running(ctx context.Context) error {
	fmt.Println("[API] HTTP Server Listening on :8080")
	// 模拟运行，直到收到停止信号
	<-ctx.Done()
	return nil
}

// ==========================================
// 3. 依赖注入与编排 (main)
// ==========================================

func main() {
	// 创建模块管理器
	manager := modules.NewManager(nil) // nil logger for demo

	// --- A. 注册 Database 模块 ---
	// 这是一个底层模块，没有依赖
	manager.RegisterModule(ModDatabase, func() (services.Service, error) {
		return NewDBService(), nil
	})

	// --- B. 注册 UserStore 模块 ---
	// 它依赖 Database。注意这里演示了 Invisible Module (不直接对外暴露)
	manager.RegisterModule(ModUserStore, func() (services.Service, error) {
		fmt.Println("[UserStore] Initializing...")
		return services.NewIdleService(), nil // 使用 IdleService 占位，演示即便没有复杂逻辑也可以是模块
	})

	// --- C. 注册 ApiServer 模块 ---
	// 它依赖 UserStore
	manager.RegisterModule(ModApiServer, func() (services.Service, error) {
		return NewApiServer(), nil
	})

	// --- D. 定义依赖关系图 ---
	deps := map[string][]string{
		ModUserStore: {ModDatabase},  // UserStore 依赖 Database
		ModApiServer: {ModUserStore}, // ApiServer 依赖 UserStore
	}
	// 将依赖图注入管理器
	manager.BuildModuleMap(deps)

	// --- E. 启动系统 ---
	fmt.Println("--- Starting System ---")
	
	// 初始化所有模块 (目标是 ApiServer，因为它依赖了所有东西)
	// InitModuleServices 会计算依赖链：DB -> UserStore -> ApiServer
	serviceMap, err := manager.InitModuleServices(ModApiServer)
	if err != nil {
		panic(err)
	}

	// 将所有初始化的服务放入一个 ServiceManager 进行统一管理
	var svcs []services.Service
	for _, s := range serviceMap {
		svcs = append(svcs, s)
	}
	
	servManager, err := services.NewManager(svcs...)
	if err != nil {
		panic(err)
	}

	// 监听系统信号以优雅退出
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		fmt.Println("\n--- Signal Received, Stopping... ---")
		cancel() // 取消上下文，触发所有服务的 stopping/running 退出
	}()

	// 启动所有服务！
	// dskit 会自动按顺序启动：DB (Starting->Running) -> UserStore -> ApiServer
	if err := servManager.StartAsync(ctx); err != nil {
		panic(err)
	}

	// 等待所有服务停止
	err = servManager.AwaitStopped(context.Background())
	fmt.Println("--- System Stopped ---")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}
```

## 3. 运行结果解析

运行上述代码，你将看到如下顺序的输出：

```text
--- Starting System ---
[UserStore] Initializing...    <-- 初始化 UserStore (工厂函数被调用)
[DB] Connecting to database... <-- DB 开始启动 (Service.Starting)
[DB] Connected!                <-- DB 启动完成
[DB] Ready for queries.        <-- DB 进入 Running 状态
[API] HTTP Server Listening... <-- 只有 DB Running 后，API Server 才开始运行
```

当按下 `Ctrl+C`：
```text
--- Signal Received, Stopping... ---
[DB] Closing connection...     <-- 接收到 Context Done，开始停止
--- System Stopped ---
```

## 4. 总结：为什么要通过 Module 使用 Service？

直接使用 `Service` 就像**手动挡车**：你需要自己手动 `db.Start()`, 等它好了再 `store.Start()`, 再 `api.Start()`。代码由于大量的顺序控制变得极度耦合。

使用 `Module` 就像**自动挡+导航**：
1.  你只需要定义“目的地”（Target Module: `ApiServer`）。
2.  你只需要定义“地图”（依赖关系：API 依赖 UserStore，UserStore 依赖 DB）。
3.  `dskit` 替你计算路线，并且在“开车”时自动按顺序启动引擎、挂挡、松手刹。并行的地方它还会自动并发（比如 Cache 和 DB 可以同时启动）。

---

## 5. 常见问题 (FAQ)

### Q1: 只有模块能定义依赖关系吗？如果一个模块 A 有 N 个服务，A 中的服务能否定义依赖？
-   **模块即服务**：`dskit` 的设计原则是**模块对应一个服务**。一个模块工厂函数返回**一个** `services.Service`。
-   **模块内服务依赖**：如果你的模块 A 需要管理 N 个子服务，通常的做法是让模块 A 返回一个 `services.Manager`（它本身也是一个 Service），由这个内部的 Manager 来管理这 N 个子服务。这 N 个子服务之间的依赖关系，由 `services.NewManager(subServices...)` 传入的顺序决定（Manager 默认并行启动无依赖的，但通常我们认为这是单一模块内部事务）。
-   **建议拆分**：如果这 N 个服务之间有明确的、重要的启动依赖关系（例如 B 必须在 A 之后启动），最佳实践是将它们拆分为**两个独立的 Module**，并通过 `dskit` 的 `BuildModuleMap` 显式定义模块依赖。

### Q2: 我有一个新项目，使用 trace/slog，有必要实现像 Grafana `pkg/modules` 那样的包装层吗？
-   **结论**：**通常没有必要**。
-   **理由**：
    1.  Grafana 的 `pkg/modules` 是为了兼容其历史遗留的架构和特定的追踪/日志需求而做的重度适配层。
    2.  `dskit` 原生已经支持得很好。对于新项目，你可以直接在 Service 的 `Starting/Running` 回调中加入 log 或 trace 代码。
    3.  **如果需要生命周期追踪**：你可以实现一个简单的 `services.Listener`（参考 Grafana 的实现），在状态变更时打点。这比维护一整套 `pkg/modules` 包装器要轻量得多、也更 Pythonic (Go-idiomatic)。
