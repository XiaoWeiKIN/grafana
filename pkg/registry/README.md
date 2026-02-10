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

### 4. 应用扩展 (`apps/`)
这一层也称为 `appregistry`，它负责集成 Grafana 的高层功能模块（即 "Apps"）。

- **基于 App SDK**: 这里的代码大量使用了 `github.com/grafana/grafana-app-sdk`，表明这些应用是按照 Grafana 的新应用架构规范构建的。
- **声明式注册**: 通过 `ProvideAppInstallers` 函数，根据 Feature Toggles 动态加载不同的功能模块。

---

## `registry/apps` 详细解读

`pkg/registry/apps` 是 Grafana **功能模块化** 的核心。它不仅定义了应用如何启动，还负责处理新老架构的平滑过渡。

### 核心设计概念

1.  **AppInstaller 模式**
    每个子模块（如 `alerting`, `playlist`, `live`）都提供一个 `AppInstaller`。这个 Installer 负责：
    *   **API 注册**: 定义该应用在 K8s 风格 API Server 中的 GVR（组/版本/资源）。
    *   **业务初始化**: 调用该模块特有的 Service（如 `playlistsvc.Service`）进行初始化。

2.  **Legacy 兼容性 (Legacy Storage)**
    这是 `apps` 目录中最关键的设计点。由于 Grafana 正在从 SQL 存储转向统一资源存储 (Unified Storage)，许多新应用（基于 App SDK）仍需操作旧的数据库表。
    *   **双向存储**: `AppInstaller` 同时也实现了 `LegacyStorageProvider` 接口。
    *   **适配器**: 在子目录中（如 `playlist/legacy_storage.go`），它定义了如何将 K8s 风格的 REST 请求转换为对旧有 SQL Service 的调用。

### 注册与执行流

1.  **依赖注入**: `wireset.go` 收集所有子模块的 `AppInstaller`。
2.  **集合构建**: `apps.go` 中的 `ProvideAppInstallers` 根据当前开启的 Feature Flags，挑选出一组活跃的 Installers。
3.  **Runner 启动**:
    *   `appregistry.Service` 作为一个后台服务被启动。
    *   它持有一个 `APIGroupRunner`，该 Runner 会遍历所有的 Installers。
    *   每个 Installer 会将其对应的 API 组和存储逻辑（包括 Legacy 存储）注册到全局的 API Server 中。

### 5. 使用统计注册 (`usagestatssvcs/`)
负责收集各个服务的使用情况统计信息。

- **`ProvidesUsageStats`**: 这是一个契约，任何想要报告使用指标的服务都需要实现 `GetUsageStats()` 方法。
- **自动化收集**: `UsageStatsProvidersRegistry` 将这些分散的服务收集起来。Grafana 的全局 `UsageStatsService` 会定期遍历这些 Provider，汇总数据并上报（如果启用了遥测）。

---

## 总结

`pkg/registry` 是 Grafana 架构中的“中央总线”。它不涉及具体的业务实现，但通过以下三个维度将整个系统粘合在一起：

1.  **生命周期维度 (`backgroundsvcs`)**: 确保服务能按正确的顺序启动和优雅关闭。
2.  **访问接口维度 (`apis` & `apps`)**: 使用现代的、声明式的 Kubernetes API 风格对外暴露功能。
3.  **观测维度 (`usagestatssvcs`)**: 统一收集各组件的运行指标。

理解了这个目录，就理解了 Grafana 作为一个复杂的单体应用是如何在宏观上进行装配和运转的。
