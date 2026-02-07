# Backend Plugin Provider 源码分析

`pkg/plugins/backendplugin/provider` 包负责**决定如何实例化插件的后端**。它采用**责任链模式 (Chain of Responsibility)**，允许不同的 Provider 尝试处理插件，直到找到一个能够处理的 Provider。

## 核心设计

### 1. `PluginBackendProvider` (函数类型)
```go
type PluginBackendProvider func(_ context.Context, _ *plugins.Plugin) backendplugin.PluginFactoryFunc
```
这是一个函数签名，接收一个插件对象，如果它能处理该插件，则返回一个 `PluginFactoryFunc`（用于创建具体插件实例的工厂函数）；如果不能处理，则返回 `nil`。

### 2. `Service` (责任链容器)
```go
type Service struct {
    providerChain []PluginBackendProvider
}
```
`Service` 持有一个 Provider 列表（切片），这就是责任链。

### 3. `BackendFactory` (执行链)
```go
func (s *Service) BackendFactory(ctx context.Context, p *plugins.Plugin) backendplugin.PluginFactoryFunc {
    // 遍历责任链
    for _, provider := range s.providerChain {
        // 尝试让当前 provider 处理
        if factory := provider(ctx, p); factory != nil {
            return factory // 如果处理成功，立即返回
        }
    }
    return nil // 没有 provider 能处理
}
```
这是核心逻辑：**按顺序询问每个 Provider，"你能处理这个插件吗？"**。

## 内置 Provider

### 1. `DefaultProvider` (默认兜底)
```go
var DefaultProvider = PluginBackendProvider(func(_ context.Context, p *plugins.Plugin) backendplugin.PluginFactoryFunc {
    // 创建一个标准的 gRPC 后端插件
    // 使用插件 ID、可执行文件路径等信息
    return grpcplugin.NewBackendPlugin(p.ID, p.ExecutablePath(), p.SkipHostEnvVars)
})
```
这是**外部插件 (External Plugins)** 的默认处理方式。如果前面的 Provider（如 Core Plugin Provider）没有拦截处理，最终就会由它来启动一个独立的 gRPC 进程。

### 2. `RendererProvider` (渲染器专用)
```go
var RendererProvider PluginBackendProvider = func(_ context.Context, p *plugins.Plugin) backendplugin.PluginFactoryFunc {
    if !p.IsRenderer() {
        return nil // 只处理渲染器插件
    }
    // ... 创建渲染器插件 ...
}
```

## 实际应用：核心插件 vs 外部插件

在 `pkg/services/pluginsintegration/coreplugin/coreplugins.go` 中，我们可以看到这个机制的经典用法：

```go
func ProvideCoreProvider(coreRegistry *Registry) plugins.BackendFactoryProvider {
    // 构造一个链：先尝试 CoreRegistry，再尝试 DefaultProvider
    return provider.New(coreRegistry.BackendFactoryProvider(), provider.DefaultProvider)
}
```

这意味着当系统需要加载一个插件时：
1.  **CoreRegistry Provider**: "这是核心插件吗（比如 Prometheus）？"
    *   如果是 -> 返回核心插件的 Go Factory（进程内运行）。
    *   如果否 -> 返回 `nil`。
2.  **DefaultProvider**: "好吧，那我把它当作普通外部插件。"
    *   启动物理文件系统上的可执行文件（进程外 gRPC 运行）。

## 总结
`provider` 包通过简单的责任链模式，优雅地统一了不同类型插件（进程内 Core Plugin、进程外 External Plugin、Renderer Plugin）的加载逻辑，使得上层调用者无需关心插件的具体运行形态。
