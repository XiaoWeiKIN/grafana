# 后端插件包 (Backend Plugin Package)

`pkg/plugins/backendplugin` 包负责 Grafana 中后端插件的生命周期管理和通信。后端插件是独立于 Grafana 服务器进程运行的可执行二进制文件，但通过 gRPC 与其进行通信。

## 概览

此包抽象了以下复杂性：
1.  **进程管理**：启动、停止和监控插件子进程。
2.  **gRPC 通信**：建立和维护 Grafana（客户端）与插件（服务器）之间的 gRPC 连接。
3.  **协议协商**：确保 Grafana 与插件 SDK 之间的兼容性。

## 关键组件

### 1. `Plugin` 接口 (`ifaces.go`)
定义了后端插件的能力。此接口遮蔽 (shadows) 了 SDK 中的 `backend.Handler` 接口，但添加了生命周期方法：
-   `Start(ctx)` / `Stop(ctx)`：管理插件进程。
-   `IsManaged()` / `IsDecommissioned()`：状态检查。
-   `Target()`：指示插件是在内存中运行还是作为外部本地进程运行。

### 2. 提供者 (Provider) (`provider/`)
`provider` 包是创建插件实例的入口点。它使用 **工厂 (Factory)** 模式根据插件的元数据实例化正确的插件实现。
-   **DefaultProvider**：创建标准后端插件。
-   **RendererProvider**：图像渲染器插件的专用提供者。

### 3. 实现

#### `grpcplugin` (外部进程)
位于 `grpcplugin/` 中，这是第三方后端插件的标准实现。
-   使用 HashiCorp 的 `go-plugin` 系统。
-   管理子进程和 gRPC 客户端连接。
-   处理协议版本控制 (Client V2)。

#### `coreplugin` (进程内)
位于 `coreplugin/` 中，此实现用于实际上已编译到 Grafana 二进制文件中的“后端”插件（内部功能）。
-   在与 Grafana 相同的进程中运行。
-   无 gRPC 开销；直接函数调用。
-   用于内置数据源或共享相同接口的功能。

### 4. 扩展 (`pluginextensionv2`)
定义非标准插件的专用 gRPC 契约，例如：
-   **Renderer**：用于生成面板/仪表板的图像。
-   **Sanitizer**：用于 HTML 清理任务。

## 用法

此包主要由父目录中的 `manager` 使用，以便在需要时（例如，执行查询时）启动插件。

```go
// 概念用法
factory := provider.DefaultProvider(ctx, pluginReq)
pluginInstance, err := factory(pluginID, logger, tracer, envGetter)
if err != nil {
    // 处理错误
}
pluginInstance.Start(ctx)
defer pluginInstance.Stop(ctx)
```
