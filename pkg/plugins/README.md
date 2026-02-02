# Plugins 包

`pkg/plugins` 包提供了 Grafana 插件系统的核心功能。它负责前端和后端插件的发现、加载、验证以及生命周期管理。

## 概览

Grafana 的可扩展架构严重依赖于插件。此包充当了 Grafana 核心与外部扩展之间的桥梁。它处理以下细节：

- 解析 `plugin.json` 配置。
- 验证插件签名 (Manifests)。
- 管理后端插件进程 (gRPC) 的生命周期。
- 协助从仓库安装插件。

## 核心组件

### `Plugin` 结构体
定义在 `plugins.go` 中，`Plugin` 结构体是已加载插件的运行时表示。它作为一个容器，包含：
- **元数据 (Metadata)**：JSON 配置（ID、类型、版本、名称）。
- **资源 (Resources)**：访问插件的文件系统（资源、module.js 等）。
- **后端客户端 (Backend Client)**：如果插件包含后端组件，此结构体将持有负责与外部进程通信的客户端。

### 配置 (`models.go`)
`JSONData` 结构体代表 `plugin.json` 的模式 (schema)。它定义了插件的所有功能标志和元数据，包括：
- **类型 (Type)**：`datasource`（数据源）、`panel`（面板）、`app`（应用）或 `renderer`（渲染器）。
- **依赖 (Dependencies)**：所需的 Grafana 版本和其他插件依赖。
- **包含 (Includes)**：随插件包含的仪表盘和页面钩子。
- **路由 (Routes)**：用于访问外部资源的代理路由。

### 插件管理器 (Plugin Manager)
`manager` 子包编排整个插件系统。其职责包括：
1.  **扫描 (Scanning)**：在指定的插件目录中定位插件。
2.  **加载 (Loading)**：将元数据和资源读取到内存中。
3.  **验证 (Validation)**：确保插件已签名且有效。
4.  **注册 (Registry)**：维护所有可用插件的状态，供 Grafana 的其他部分使用。

## 目录结构

| 目录 | 描述 |
|-----------|-------------|
| `backendplugin/` |包含管理后端插件的逻辑，包括 gRPC 实现、进程管理（启动/停止）和健康检查。|
| `manager/` | 核心编排逻辑。包含用于 `loader`（加载文件）、`registry`（存储状态）和 `sources`（发现）的子包。|
| `repo/` | 用于与 Grafana 插件仓库 (GPR) 交互的客户端代码，主要用于安装和更新。|
| `storage/` | 插件文件存储和检索的抽象。|
| `codegen/` | 与插件相关的代码生成工具。|

## 关键概念

-   **前端与后端 (Frontend vs Backend)**：前端插件在浏览器中运行 (JS/TS)。后端插件作为独立的二进制文件在服务器上运行。此包同时也管理这两者，但为后者提供了额外的逻辑 (`backendplugin`)。
-   **签名 (Signatures)**：默认情况下，插件必须经过签名才能加载。`plugins.go` 和 `manager` 逻辑处理 `MANIFEST.txt` 文件的验证。
-   **插件类别 (Plugin Class)**：插件分为 `Core`（核心，随 Grafana 捆绑）或 `External`（外部，由用户安装）。

## 用法

此包主要由 `pkg/server` 和其他核心组件使用，以初始化插件子系统。

```go
// 示例 (概念性):
pluginManager.Scan(ctx)
p, exists := pluginManager.Get("my-plugin-id")
if exists {
    // 特定行为...
}
```
