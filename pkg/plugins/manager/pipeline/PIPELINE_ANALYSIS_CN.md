# Grafana Plugin Pipeline 源码分析

`pkg/plugins/manager/pipeline` 目录定义了 Grafana 插件加载流程的各个具体阶段。该目录下的代码结构清晰地反映了插件生命周期的管理策略。

整个加载流水线（Pipeline）由以下五个核心阶段组成，按执行顺序排列：

1.  **Discovery (发现)**
2.  **Bootstrap (引导)**
3.  **Validation (验证)**
4.  **Initialization (初始化)**
5.  **Termination (终止/卸载)**

## 1. Discovery (发现阶段)

**目录**: `pkg/plugins/manager/pipeline/discovery`

此阶段负责从指定源（如文件系统）查找插件，并进行初步过滤。

*   **核心接口**: `Discoverer`
    ```go
    func Discover(ctx context.Context, src plugins.PluginSource) ([]*plugins.FoundBundle, error)
    ```
*   **具体实现**: `Discovery` 结构体
*   **处理逻辑**:
    1.  调用 `src.Discover(ctx)` 获取原始的 `FoundBundle` 列表。
    2.  应用一系列 **FilterFunc** 进行过滤。
*   **关键步骤 (Steps)**:
    *   `PermittedPluginTypesFilter`: 过滤掉不允许的插件类型（例如只允许 DataSource，不允许 App）。
    *   `DisablePluginsStep`: 过滤掉配置中明确禁用的插件。
    *   `NewDuplicatePluginIDFilterStep`: 过滤掉重复 ID 的插件（去重）。

## 2. Bootstrap (引导阶段)

**目录**: `pkg/plugins/manager/pipeline/bootstrap`

此阶段负责将“发现”到的原始数据（FoundBundle）转换为内部使用的 `Plugin` 对象，并填充元数据。

*   **核心接口**: `Bootstrapper`
    ```go
    func Bootstrap(ctx context.Context, src plugins.PluginSource, bundle *plugins.FoundBundle) ([]*plugins.Plugin, error)
    ```
*   **具体实现**: `Bootstrap` 结构体
*   **处理逻辑**:
    1.  **Construct**: 使用工厂方法创建基础 `Plugin` 结构体。
    2.  **Decorate**: 应用一系列装饰器增强插件信息。
*   **关键步骤 (Steps)**:
    *   **Construct**: 计算插件签名状态，实例化 `Plugin` 对象。
    *   **Decorate**:
        *   `AppDefaultNavURL`: 为 App 插件设置默认导航 URL。
        *   `Template`: 处理版本号占位符（%VERSION%）。
        *   `AppChild`: 配置 App 插件的子插件（继承父插件版本等）。
        *   `LoadingStrategy`: 决定静态资源加载策略（本地 serve 还是 CDN）。

## 3. Validation (验证阶段)

**目录**: `pkg/plugins/manager/pipeline/validation`

此阶段负责对插件进行合法性和兼容性检查。

*   **核心接口**: `Validator`
    ```go
    func Validate(ctx context.Context, ps *plugins.Plugin) error
    ```
*   **具体实现**: `Validate` 结构体
*   **处理逻辑**: 按顺序执行一系列验证函数，任何一步报错则验证失败。
*   **关键步骤 (Steps)**:
    *   `SignatureValidationStep`: 校验插件签名（Manifest 完整性）。
    *   `ModuleJSValidationStep`: 检查 `module.js` 是否存在（前端插件必须）。
    *   `AngularDetectionStep`: 检测是否使用了旧版 Angular 架构（如果系统配置禁用了 Angular 插件，则在此处拦截）。

## 4. Initialization (初始化阶段)

**目录**: `pkg/plugins/manager/pipeline/initialization`

此阶段负责将“静态”的插件对象转变为“运行时”可用的服务。

*   **核心接口**: `Initializer`
    ```go
    func Initialize(ctx context.Context, ps *plugins.Plugin) (*plugins.Plugin, error)
    ```
*   **具体实现**: `Initialize` 结构体
*   **关键步骤 (Steps)**:
    *   `ExternalServiceRegistration`: 注册外部云服务。
    *   `BackendClientInit`: 准备后端插件客户端（Env, Factory）。
    *   `BackendProcessStart`: **启动后端进程**（Core 步骤）。
    *   `RegisterPluginRoles`: 注册 RBAC 角色。
    *   `PluginRegistration`: **注册到全局 Registry**（上线）。

*(注：Initialization 的详细分析见 `LOADER_ANALYSIS_CN.md`)*

## 5. Termination (终止/卸载阶段)

**目录**: `pkg/plugins/manager/pipeline/termination`

此阶段负责插件的卸载和资源清理。

*   **核心接口**: `Terminator`
    ```go
    func Terminate(ctx context.Context, p *plugins.Plugin) (*plugins.Plugin, error)
    ```
*   **具体实现**: `Terminate` 结构体
*   **关键步骤 (Steps)**:
    *   `BackendProcessTerminator`: 停止后端进程（SIGTERM/KILL）。
    *   `Deregister`: 从全局 Registry 中移除插件记录。

## 总结

Grafana 的插件 Pipeline 设计高度统一：
1.  **一致的模式**: 每个阶段都采用 `Interface` + `Implementation` + `Steps Chain` 的模式。
2.  **关注点分离**:
    *   Discovery 只管找。
    *   Bootstrap 只管造。
    *   Validation 只管查。
    *   Initialization 只管起。
    *   Termination 只管停。
3.  **易于扩展**: 想要增加新的检查或处理逻辑，只需编写一个新的 `Func` 并加入到对应的 Stage Steps 列表中即可（通常在 `Provide...Stage` 的 Wire 注入处配置）。
