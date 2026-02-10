# dskit Module & Service 模式详解

`dskit` (Distributed Systems Kit) 是 Grafana Labs 开发的一套用于构建分布式系统的库（核心源自 Cortex/Mimir）。它的核心是 **Module**（依赖注入与编排）和 **Service**（生命周期管理）。这种模式实现了一种**模块化的单体架构 (Modular Monolith)**，使得系统既可以作为单体运行，也可以灵活拆分为微服务。

### 1. Service (服务): 生命周期管理的原子单位

**Service** 是对“长期运行组件”的抽象。任何需要启动、运行、停止的后台任务都是一个 Service。

*   **状态机 (State Machine)**
    每个 Service 都有严格的状态流转，确保生命周期的确定性：
    `New` → `Starting` → `Running` → `Stopping` → `Terminated` (或 `Failed`)
    
*   **核心接口**
    ```go
    type Service interface {
        StartAsync(context.Context) error // 异步启动
        AwaitRunning(context.Context) error // 等待启动完成
        StopAsync() // 异步停止
        AwaitTerminated(context.Context) error // 等待停止完成
        State() State // 获取当前状态
        // ...
    }
    ```

*   **常用实现**
    *   **`NewBasicService`**: 最常用的实辅助方法。你只需要提供 `starting`, `running`, `stopping` 三个函数，它会自动处理状态锁、并发控制和错误处理。
    *   **`Manager`**: `services.NewManager(svcs...)` 可以管理一组 Service。它像一个“超级 Service”，启动它等于按依赖顺序启动它管理的所有子服务；停止它等于反序停止所有子服务。

### 2. Module (模块): 依赖注入与装配的容器

**Module** 是对 Service 的**封装**和**依赖描述**。它解决的是“谁依赖谁”、“需要启动哪些服务”的问题。

*   **定义**
    模块在 `dskit` 中本质上就是一个 **名字 (Name)** 和一个 **工厂函数 (Factory)**。
    ```go
    // 伪代码
    RegisterModule(name string, initFn func() (services.Service, error))
    ```
    *注意*: `initFn` 返回的是一个 `Service`。这意味着一个模块在运行时被实例化为一个服务（或服务管理器）。

*   **依赖图 (Dependency Graph)**
    你需要定义模块之间的依赖关系。
    *   *示例*: `Server` 模块依赖 `Store` 模块，`Store` 模块依赖 `Database` 模块。
    *   `dskit` 会根据这个图进行拓扑排序，确保初始化顺序正确 (DB -> Store -> Server)。

*   **按需加载 (Target)**
    这是 `dskit` 最强大的特性。你可以在启动时指定一个 `target` (例如 "all", "ingester", "querier")。
    `dskit` 会计算：为了达成这个 `target` 模块，我需要初始化哪些前置依赖模块？
    *   **未被依赖的模块完全不会被初始化**（工厂函数不会执行）。这对于单一二进制文件支持多种运行模式至关重要。

### 3. 它们如何协同工作 (Grafana 源码透视)

在 Grafana 的 `pkg/server/module_server.go` 中，我们可以清晰地看到这个模式的落地：

1.  **Registry (注册)**:
    `ModuleServer` 初始化一个 module manager。
    ```go
    // pkg/server/module_server.go
    m := modules.New(s.log, s.cfg.Target)
    m.RegisterModule(modules.Core, ...) 
    m.RegisterModule(modules.StorageServer, ...)
    ```

2.  **Wiring (编排)**:
    各模块之间定义了依赖关系 (在 `pkg/modules/dependencies.go` 中定义)。

3.  **Boot (启动)**:
    当调用 `m.Run(ctx)` 时：
    1.  `dskit` 根据配置的 `target` 计算依赖树。
    2.  按顺序调用相关模块的工厂函数，得到一组 `Service` 实例。
    3.  将这些 `Service` 实例放入一个 `services.Manager`。
    4.  调用 `Manager.StartAsync()`，核心组件依次启动。

### 4. 总结：Grafana 为什么选择这种模式？

*   **解耦**: 模块之间只通过 Service 接口交互，初始化逻辑被隔离。
*   **灵活的部署架构**: 同一套代码，可以通过改变 `target` 部署为单体，也可以部署为微服务，而无需修改代码。
*   **可靠的生命周期**: 统一的启动/停止顺序，避免了“数据库还没连上就开始处理请求”或“日志服务先停了导致报错无法记录”等并发问题。

---

## RegisterInvisibleModule 详解

`RegisterInvisibleModule` 并不是 `dskit` 原生提供的直接方法名，它是 Grafana 在 `pkg/modules` 层为了封装方便而添加的一个辅助方法。

### 1. 什么是 "Invisible" (不可见)？

在 `dskit` 中，模块有一个属性叫做 **UserInvisibleModule**。

*   **可见模块 (默认)**:
    用户可以通过命令行参数 `-target <module_name>` 显式指定要启动这个模块。
    
*   **不可见模块 (Invisible)**:
    用户**不能**通过命令行 `-target` 直接启动它。
    它存在的唯一意义是**作为其他模块的依赖**被动启动。

### 2. 源码实现

在 `pkg/modules/tracing/manager.go` 中，我们可以看到它的实现：

```go
// pkg/modules/tracing/manager.go

// RegisterInvisibleModule registers a module with the UserInvisibleModule option
func (m *ModuleManagerWrapper) RegisterInvisibleModule(name string, fn initFn) {
    // ... 包装 initFn 用于自动注入 Tracing ...
    var wrappedFn initFn
    if fn != nil {
        wrappedFn = m.wrapInitFn(fn)
    }
    // 最终调用 dskit 原生的 RegisterModule，但带上了 UserInvisibleModule 选项
    m.Manager.RegisterModule(name, wrappedFn, modules.UserInvisibleModule)
}
```

### 3. 应用场景与价值

**场景**: 基础设施类模块。

想象一下 Grafana 内部的一些“隐形基础设施”，例如：
*   **UsageStats**: 使用统计服务。
*   **InstrumentationServer**: 暴露 `/metrics` 和 `/debug/pprof` 端点的 HTTP 服务。

这些模块是系统的“底座”。
*   你永远不会想运行一个只包含“使用统计”的 Grafana 实例 (即运行 `./grafana -target=usage-stats` 是没有意义的)。
*   但是，如果启动了 `Server` 模块，`Server` 依赖了 `UsageStats`，那么 `UsageStats` 就必须被顺带初始化。

`RegisterInvisibleModule` 用于注册这些**纯粹的库级、基础设施级模块**。这防止了用户误操作启动了无意义的进程模式，同时保证了基础设施服务的自动加载。

---

## 架构建议 —— 构建可观测性平台

如果你要从零构建一个类似 Grafana/Prometheus/Cortex 的可观测性查询平台，是否应该使用这套模式？

### 1. 结论
**强烈推荐使用 `dskit` 的 Service/Module 模式。**

### 2. 理由
*   **复杂依赖治理**：查询平台组件众多（Querier, Cache, Store Gateway, Rule Engine），手动管理初始化顺序极易出错。`dskit` 的依赖图自动解决此问题。
*   **灵活部署形态**：初期单体部署，后期通过 `-target` 拆分微服务（Querier 独立扩容），架构演进无需重构代码（同一套 binary，不同启动参数）。
*   **可靠的生命周期**：确保数据库连接在 HTTP 服务启动前就绪，在 HTTP 流量停止后才断开，实现真正的 Graceful Shutdown。

### 3. 实施路径
*   **推荐方案 (轻量级)**：直接使用 `github.com/grafana/dskit` 原生库。
    *   像 Grafana 那样定义模块常量和依赖图。
    *   如果需要监控启动时间，自己实现一个简单的 `TracingListener` 注入即可。
*   **避免过度设计**：不要照搬 Grafana `pkg/modules` 的所有代码，因为它耦合了 Grafana 内部特定的基础设施（log, tracing 包），直接使用原生库通常已经足够好用。

---

## dskit/modules 深度使用与最佳实践

本节介绍如何在自己的代码中从零开始使用 `dskit` 模式。

### 1. 使用流程四部曲

#### 第一步：注册 (Register)
在 `main.go` 或初始化代码中，告诉 **Manager** 有哪些模块。

```go
mm := modules.NewManager(logger)

// 注册基础模块
mm.RegisterModule("db", func() (services.Service, error) {
    return NewDBService(cfg.DB), nil
})

// 注册高级模块
mm.RegisterModule("api", func() (services.Service, error) {
    return NewAPIService(cfg.API), nil
}, modules.UserVisibleModule)
```

#### 第二步：定义依赖 (Dependency)
明确模块之间的先后顺序。

```go
// API 模块需要数据库才能工作
mm.AddDependency("api", "db")
```

#### 第三步：初始化 (Initialize)
根据你想要启动的“目标模块”，**Manager** 会自动算出所有需要被拉起的依赖，并按拓扑排序执行 `initFn`。

```go
// 这一步会执行 db 的 initFn 和 api 的 initFn
// 并返回 map["db"]Service, map["api"]Service
serviceMap, err := mm.InitModuleServices("api")
```

#### 第四步：运行 (Run)
将生成的服务交给 `services.Manager` 进行并发启动。

```go
sm, _ := services.NewManager(getServicesInList(serviceMap)...)
sm.StartAsync(ctx)
```

### 2. 核心原理：如何保证启动顺序？

虽然 `services.Manager` 是并发启动所有服务的，但 `dskit` 通过 **module_service_wrapper** 保证了依赖顺序：

1.  **自动包装**：`InitModuleServices` 返回的不是原生 Service，而是套了一个壳 (`moduleService`)。
2.  **启动阻塞 (Inside-out)**：当 `api.Start()` 被调用时，它内部会先阻塞，调用 `db.AwaitRunning()`。只有 `db` 真正 Running 后，`api` 才会开始启动。
3.  **停止阻塞 (Outside-in)**：停止时相反，`db` 会等待 `api` 完全停止 (`AwaitTerminated`) 后才开始关闭。

### 3. 最佳实践建议

*   **精细化拆分**：不要把所有东西都写在一个大 Service 里。利用 Module 将存储、中间件、业务逻辑拆开，通过 `AddDependency` 串联。
*   **配置驱动**：可以根据配置文件动态决定注册哪些模块。
*   **组合启动 (Virtual Modules)**：如果程序有多种角色（如读/写节点），可以定义虚拟 Module 作为入口：

    ```go
    // 定义一个虚拟角色，不包含实际逻辑，只聚合依赖
    mm.RegisterModule("writer-node", nil) 
    mm.AddDependency("writer-node", "db", "ingester", "api")
    // 启动时只需指定这个虚拟模块，即可拉起所有依赖
    mm.InitModuleServices("writer-node")
    ```
