# 插件加载器 (Plugin Loader)

`loader` 包负责将插件从来源（如文件系统）加载到 Grafana 系统中的完整生命周期。它编排了一个复杂的管线，将原始文件转换为初始化后的 `plugins.Plugin` 对象。

## 核心职责

`Loader` 为每个插件源协调以下阶段：

1.  **Discovery (发现)**：扫描提供的源（如目录路径）以寻找潜在的插件包。
2.  **Bootstrap (引导)**：读取基础插件元数据（`plugin.json`），创建主从插件包。
3.  **Validation (校验)**：确保插件有效（例如：签名验证、结构检查）。
4.  **Initialization (初始化)**：执行插件对象的最终设置，为注册做好准备。

## 架构设计

`Loader` 本身是一个高层编排者，它将实际工作委托给通过构造函数注入的专业组件：

```go
type Loader struct {
    discovery    discovery.Discoverer
    bootstrap    bootstrap.Bootstrapper
    validation   validation.Validator
    initializer  initialization.Initializer
    termination  termination.Terminator
    // ...
}
```

### 关键接口

-   **`Service`**：由 `Loader` 实现的主公开接口。
    -   `Load(ctx, src)`：从源加载插件。
    -   `Unload(ctx, p)`：卸载特定插件。

### 错误处理

加载器集成了 `ErrorTracker`，用于记录在加载过程中遇到的任何问题（例如：签名无效、JSON 格式错误）。这些错误会根据插件 ID 进行追踪。

## 常见问题

### 它是用来加载后端插件的吗？
**是的，但不完全是。** `loader` 负责加载**所有类型**的插件（包括纯前端插件、数据源、面板，以及带有后端的插件）。
它的职责是解析元数据（读取 `plugin.json` 和文件结构）。
- 如果插件包含后端可执行文件，`loader` 会读取其后端配置。
- **真正的后端进程启动和管理**是由专门的后端插件模块（通常在 `pkg/plugins/backendplugin`）负责的。

### 1. 它和 gRPC Plugin 有什么区别？
**Loader 是“登记员”，gRPC Plugin 是“执行执行者”。**

| 特性 | Loader (插件加载器) | gRPC Plugin (后端插件) |
| :--- | :--- | :--- |
| **职责** | **静态解析**：扫描磁盘、读取 `plugin.json`、校验签名。 | **动态执行**：启动外部进程，处理数据查询。 |
| **状态** | 处理插件**元数据**（长什么样）。 | 处理插件**实际行为**（做什么）。 |
| **运行位置** | 在 Grafana 主进程内。 | 独立的外部二进制进程。 |

### 2. Loader 会处理 Panel（面板）和 App 吗？
**是的。** 虽然 Panel 和 App 主要是前端代码，但它们的“户口本”在 Go 后端。
- **元数据提取**：Go 端需要读取并解析它们的 `plugin.json`，以便在 UI 中正确显示。
- **安全性**：Go 端负责校验前端代码的数字签名，确保安全。
- **静态资源分发**：Go 端根据 Loader 记录的路径，为浏览器提供 JS 文件的 HTTP 服务。

### 3. Go 代码与前端插件的关系
- **房东与租客**：Go 后端是“房东”，负责管理空间、路由和安全；前端插件是“租客”，在被分配的容器中运行。
- **数据桥梁**：前端插件无法直接连接数据库，必须通过 Go 后端提供的代理接口，间接调用后端插件或外部 API。

## 使用示例

```go
// 示例实例化（通常由依赖注入处理）
loader := loader.New(
    cfg,
    discoverySvc,
    bootstrapSvc,
    validationSvc,
    initializationSvc,
    terminationSvc,
    errorTracker,
)

// 加载插件
plugins, err := loader.Load(ctx, pluginSource)
```
