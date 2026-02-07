# pkg/registry 源码分析

## 目录概览

`pkg/registry` 是 Grafana 后端服务的**核心注册与组装层**。它的主要职责是利用依赖注入（主要通过 Google Wire）将 Grafana 庞大的各个功能模块（Services）、API 接口（APIs）以及应用程序（Apps）连接起来，形成一个完整的运行时系统。

该目录并不包含具体的业务逻辑实现，而是充当了系统的**装配车间**和**胶水层**。

## 核心组件与接口

### 1. 核心接口 (`registry.go`)
该文件定义了跨模块通用的基础契约：

- **`BackgroundService`**: 任何需要在后台长期运行的服务（例如定时清理任务、告警引擎、HTTP 服务器等）都必须实现此接口的 `Run(ctx context.Context) error` 方法。
- **`BackgroundServiceRegistry`**: 用于收集和管理所有后台服务的容器。
- **`UserStatsProvidersRegistry`**: 用于收集各个服务模块使用情况统计数据的接口。
- **`DatabaseMigrator`**: 允许服务注册自己的数据库迁移脚本。
- **`CanBeDisabled`**: 一个可选接口，允许服务根据配置（如 Feature Toggles）决定自己是否应该启动。

### 2. 后台服务总线 (`backgroundsvcs/`)
这是 Grafana 启动时的核心组装点。

- **`Describe: background_services.go`**:
    - 这里的 `ProvideBackgroundServiceRegistry` 函数是一个巨型的构造函数。
    - 它接收了系统中几乎所有的核心服务实例作为参数（如 `HTTPServer`, `AlertNG`, `ProvisioningService`, `TracingService` 等）。
    - 它将这些分散的服务统一打包进 `BackgroundServiceRegistry`。
    - **作用**: 确保当 Grafana 主进程启动时，所有依赖的后台服务能被有序地初始化和运行。

### `registry/backgroundsvcs` 详细解读

该目录不仅负责收集服务，还引入了 **Service Adapter** 模式来统一管理服务的生命周期。

#### Adapter 层 (`backgroundsvcs/adapter`)

Grafana 正在引入 `dskit/services`（源自 Grafana Mimir/Cortex）来实现更健壮的服务生命周期管理。`adapter` 包的作用就是连接旧的 Grafana 服务模型和新的 dskit 服务模型。

1.  **目标**:
    让所有后台服务拥有标准的状态流转：`New` -> `Starting` -> `Running` -> `Stopping` -> `Terminated`。
    并支持依赖管理、健康检查和优雅关闭。

2.  **`serviceAdapter` (`service.go`)**:
    *   这是一个包装器，它将实现了简单的 `registry.BackgroundService` 接口（只有一个 `Run` 方法）的服务，包装成功能丰富的 `dskit.NamedService`。
    *   **运行机制**: 它在 `Running` 阶段调用原始服务的 `Run(ctx)` 方法。当需要停止时，它会取消 context，并等待 `Run` 方法退出，确保服务真正停止后才切换状态到 `Terminated`。

3.  **`ManagerAdapter` (`manager.go`)**:
    *   它是所有后台服务的总管。
    *   **依赖图**: 它构建了一个依赖关系图（Dependency Map），定义了服务启动的顺序（例如：Core 服务先启动，Background Services 后启动）。
    *   **统一启动**: 当 `ManagerAdapter` 启动时，它会启动底层的 `dskit` Manager，进而并发或按顺序启动所有注册的后台服务。
    *   **可见性**: 既然被适配成了 dskit 服务，这些服务就可以被 dskit 的运维工具监控，查看当前状态（是否健康、启动耗时等）。

### 3. API 注册 (`apis/`)
负责 REST API 路由的注册与装配。

- **`WireSet`**: 定义了 API 层的依赖注入图。
- **`ProvideRegistryServiceSink`**: 这是一个特殊的“Sink”函数（接收器）。它的目的不是为了返回什么有用的对象，而是为了强制 Go 的依赖注入系统（Wire）去实例化各个模块的 `APIBuilder`（如 `DashboardsAPIBuilder`, `DataSourceAPIBuilder`）。
- **流程**: 各个业务模块（如 Dashboard, IAM, Datasource）提供自己的 API 构建器，通过这里注册到 Grafana 的 HTTP Server 上。

---

## `registry/apis` 详细解读

`pkg/registry/apis` 目录不仅是代码组织的容器，它代表了 Grafana 向 **Kubernetes 风格 API 架构** 演进的重要一步。这里的代码深度集成了 `k8s.io/apiserver` 的设计模式。

### 核心架构模式

1.  **Builder 模式 (The Builder Pattern)**
    每个子目录（如 `dashboard`, `datasource`）都通过一个 `RegisterAPIService` 函数对外暴露。该函数负责构建一个实现了 `builder.APIGroupBuilder` 接口的结构体。
    *   例如：`dashboard.DashboardsAPIBuilder` 和 `datasource.DataSourceAPIBuilder`。

2.  **Kubernetes API 风格**
    Grafana 在这里并没有使用传统的 HTTP Router (如 Gin/Chi) 风格，而是复用了 K8s 的 API Server 库：
    *   **Group/Version/Resource (GVR)**: 资源被组织成组和版本（如 `dashboard.grafana.app/v1alpha1`）。
    *   **REST Storage**: 实现了 K8s 的 `rest.Storage` 接口（Create, Get, Update, Delete）。
    *   **Admission Control**: 支持准入控制逻辑（Validate）。
    *   **Scheme & Conversion**: 使用 `runtime.Scheme` 进行不同版本 API 对象之间的转换。

### 注册流程详解

整个注册流程是通过依赖注入 (`pkg/registry/apis/wireset.go`) 自动完成的，步骤如下：

1.  **Wire 注入**: `wireset.go` 中的 `WireSet` 包含了所有子模块的 `RegisterAPIService` 函数。
2.  **实例化 Builder**: 当应用启动时，Wire 会调用这些 `RegisterAPIService` 函数。
3.  **构建 API Group**:
    *   在 `RegisterAPIService` 内部，会创建一个具体的 API Builder（例如 `DashboardsAPIBuilder`）。
    *   Builder 会定义它支持的 Group Version（如 `v1`, `v2beta1`）。
    *   它会定义后端存储（Storage），通常包括：
        *   **Legacy Storage**: 指向传统的 SQL 数据库。
        *   **Unified Storage**: 指向新的统一资源存储体系。
        *   **Dual Write**: 一般会通过 `DualWriteBuilder` 同时写向两者。
4.  **注册到 Registrar**:
    *   Builder 创建完成后，会调用 `apiRegistrar.RegisterAPI(builder)`。
    *   这会将该 API Group 注册到 Grafana 内部集成的 K8s-like API Server (`pkg/services/apiserver`)。

### 目录结构示例 (`dashboard/`)

以 `pkg/registry/apis/dashboard` 为例：
*   **`register.go`**: 核心入口。实现了 `DashboardsAPIBuilder`。
    *   `InstallSchema()`: 注册 Go 结构体到 K8s Scheme（定义了 API 对象）。
    *   `UpdateAPIGroupInfo()`: 配置 REST Storage（定义了怎么存取数据）。
    *   `Validate()`: 实现准入控制校验逻辑（如检查 Dashboard 标题、权限等）。
*   **`search.go`**: 实现了搜索相关的 handler。
*   **`legacy/`**: 包含了适配老旧 SQL 存储的代码。

### 总结

`registry/apis` 是 Grafana **下一代 API 架构** 的孵化器。它允许 Grafana 内部像开发 Kubernetes Operator 一样开发核心功能，拥有版本化、声明式 API、准入控制等高级特性，同时通过 Dual Write 机制保持对旧版 SQL 存储的兼容。
这一层也称为 `appregistry`，它看起来是 Grafana 内部向“云原生/Kubernetes 模式”演进的一部分。

- **基于 SDK**: 这里的代码大量使用了 `github.com/grafana/grafana-app-sdk`，表明这些“Apps”是基于新的 App SDK 构建的。
- **类 K8s 模式**: 包含了 `APIGroupRunner` 和 `k8s.io/client-go` 的引用，说明这些应用可能在内部模拟了 Kubernetes 的 Operator/Controller 模式（即使在非 K8s 环境下）。
- **动态特性**: 通过 `ProvideAppInstallers`，根据 Feature Toggles 动态加载不同的功能模块（如 `Alerting Rules`, `Live`, `Correlations` 等）。

## 总结

如果你想理解：
1. **Grafana 启动时到底运行了哪些后台进程？** -> 请看 `registry/backgroundsvcs`。
2. **各个模块的 API 是如何注册到 HTTP Server 的？** -> 请看 `registry/apis`。
3. **新的 App SDK 是如何集成进主程序的？** -> 请看 `registry/apps`。

`pkg/registry` 是理解 Grafana 宏观架构依赖关系的最佳入口。
