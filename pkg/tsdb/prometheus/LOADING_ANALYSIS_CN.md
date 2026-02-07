# Prometheus 核心插件加载流程详解

本文档帮您梳理 Prometheus 这个核心数据源插件是如何被 Grafana 加载的完整流程。

核心流程分为四个阶段：**发现 (Discovery)** -> **注入 (Wiring)** -> **注册 (Registration)** -> **实例化 (Instantiation)**。

---

## 1. 发现阶段 (Discovery)
**负责组件**: `pluginsources`
**作用**: 告诉 Grafana "有一个叫 prometheus 的插件存在"。

Grafana 启动时，`pluginSources` 服务会扫描文件系统。
*   **代码位置**: `pkg/services/pluginsintegration/pluginsources/pluginsources.go`
*   **逻辑**: 扫描 `public/app/plugins/datasource` 目录。
*   **结果**: 找到 `public/app/plugins/datasource/prometheus/plugin.json`。
    *   Grafana 读取这个 JSON，解析出 ID=`prometheus`, Name=`Prometheus`, Type=`datasource` 等元数据。
    *   此时，Grafana 知道了插件的存在，可以在前端列表中显示它。

---

## 2. 注入阶段 (Wiring)
**负责组件**: `wire` (Google Wire 依赖注入)
**作用**: 将 Prometheus 的后端服务实现注入到依赖图中。

*   **代码位置**: `pkg/server/wire.go`
*   **逻辑**:
    ```go
    // wire.go
    prometheus.ProvideService, // 1. 声明 Prometheus 服务提供者
    coreplugin.ProvideCoreRegistry, // 2. 注入核心插件注册表
    ```
*   **工厂函数**: `pkg/tsdb/prometheus/prometheus.go`
    ```go
    func ProvideService(httpClientProvider httpclient.Provider) *Service {
        return &Service{...} // 创建 Prometheus 服务单例
    }
    ```

---

## 3. 注册阶段 (Registration)
**负责组件**: `coreplugin.Registry`
**作用**: 建立 `String ID ("prometheus")` -> `Go Instance` 的映射关系。

在依赖注入初始化时，`ProvideCoreRegistry` 会被调用。
*   **代码位置**: `pkg/services/pluginsintegration/coreplugin/coreplugins.go`
*   **关键代码**:
    ```go
    func ProvideCoreRegistry(..., pr *prometheus.Service, ...) *Registry {
        return NewRegistry(map[string]backendplugin.PluginFactoryFunc{
            // ...
            Prometheus: asBackendPlugin(pr), // 映射建立！
            //Key="prometheus"  Value=封装了pr的工厂函数
            // ...
        })
    }
    ```
*   `asBackendPlugin` 适配器会将 `prometheus.Service` 转换为标准的 `backendplugin.PluginFactoryFunc`，使其对外表现得像一个普通后端插件。

---

## 4. 实例化阶段 (Instantiation)
**负责组件**: `plugins.manager` (具体在 `pipeline` 初始化步骤中)
**作用**: 真正创建插件的客户端实例。

Grafana 启动时，插件管理器会执行初始化流水线 (`pkg/plugins/manager/pipeline/initialization`)。其中关键的一步是 `BackendClientInitStep`。

*   **代码位置**: `pkg/plugins/manager/pipeline/initialization/steps.go`
*   **详细流程**:
    1.  **获取 Factory**: 调用 `backendProvider.BackendFactory(ctx, p)`。
        *   这里会触发 `provider.go` 的责任链逻辑。
        *   对于 Prometheus，`CoreProvider` 拦截并返回 `coreplugins.go` 中注册的 Factory。
    2.  **创建 Client**: 立即调用获取到的 Factory。
        *   ```go
            // steps.go
            if backendClient, err := backendFactory(p.ID, p.Logger(), b.tracer, envFunc); err != nil {
                return nil, err
            } else {
                p.RegisterClient(backendClient) // 将客户端绑定到 Plugin 对象上
            }
            ```
    3.  **结果**: `p.Client()` 现在指向了一个直接调用 Prometheus Go Service 的适配器，而不是 gRPC 客户端。

---

## 5. 总结流程图

```mermaid
graph TD
    subgraph "启动阶段: 发现 & 注入"
        A[扫描 public/app/plugins] -->|发现 plugin.json| B(Plugin Store 记录元数据)
        C[wire.go] -->|注入依赖| D[prometheus.Service]
        D -->|注册| E[Core Registry Map]
    end

    subgraph "初始化阶段: 实例化"
        F[Plugin Manager Pipeline] --> G[BackendClientInitStep]
        G -->|1. 请求 Factory| H[Provider Chain]
        H -->|2. 命中 CoreProvider| I[返回 Core Factory]
        G -->|3. 调用 Factory| J[创建 Backend Client]
        J -->|4. 绑定| K[Plugin.RegisterClient(Client)]
    end

    subgraph "运行阶段: 查询"
        L[用户查询] --> M[DataSourceService]
        M -->|调用| N[Plugin.Client()]
        N -->|进程内直接调用| O[prometheus.Service.QueryData]
## 6. 源码级深度解析 - 实例化细节

### 6.1 触发点：流水线中的 `BackendClientInitStep`

实例化发生在 Grafana 启动的初始化流水线中。
文件：`pkg/plugins/manager/pipeline/initialization/steps.go`

```go
// 这是一个初始化步骤函数
func (b *BackendClientInit) Initialize(ctx context.Context, p *plugins.Plugin) (*plugins.Plugin, error) {
	if p.Backend {
        // 关键调用 1: 寻找合适的工厂函数
		backendFactory := b.backendProvider.BackendFactory(ctx, p)
		if backendFactory == nil {
			return nil, errors.New("could not find backend factory for plugin")
		}

        // ... 省略环境变量准备 ...

        // 关键调用 2: 执行工厂函数，真正创建客户端
		if backendClient, err := backendFactory(p.ID, p.Logger(), b.tracer, envFunc); err != nil {
			return nil, err
		} else {
            // 关键调用 3: 绑定
			p.RegisterClient(backendClient)
		}
	}
	return p, nil
}
```

### 6.2 分发逻辑：`provider` 责任链

`b.backendProvider.BackendFactory(ctx, p)` 实际上是调用了 `provider` 包的逻辑。
文件：`pkg/plugins/backendplugin/provider/provider.go`

```go
func (s *Service) BackendFactory(ctx context.Context, p *plugins.Plugin) backendplugin.PluginFactoryFunc {
	for _, provider := range s.providerChain {
        // 依次询问：CoreProvider -> DefaultProvider (gRPC)
		if factory := provider(ctx, p); factory != nil {
			return factory // 命中即返回
		}
	}
	return nil
}
```

### 6.3 核心实现：`coreplugins.go` 的适配器

当轮到 `CoreProvider` 时，它会检查 `coreRegistry`。
文件：`pkg/services/pluginsintegration/coreplugin/coreplugins.go`

```go
// 注入时的映射建立
func ProvideCoreRegistry(..., pr *prometheus.Service) *Registry {
	return NewRegistry(map[string]backendplugin.PluginFactoryFunc{
        // ...
		Prometheus: asBackendPlugin(pr), // 将 prometheus.Service 包装成 Factory
	})
}

// 适配器函数
func asBackendPlugin(svc any) backendplugin.PluginFactoryFunc {
	// 识别服务实现了哪些接口
    opts := backend.ServeOpts{}
	if queryHandler, ok := svc.(backend.QueryDataHandler); ok {
		opts.QueryDataHandler = queryHandler
	}
    // ...
	return coreplugin.New(opts) // 返回 Factory
}
```

### 6.4 最终产物：`corePlugin` 结构体

`coreplugin.New` 返回的 Factory 最终创建的是一个 `corePlugin` 实例。
文件：`pkg/plugins/backendplugin/coreplugin/core_plugin.go`

```go
type corePlugin struct {
	// ...
	backend.QueryDataHandler // 实际上就是 prometheus.Service
}

func (cp *corePlugin) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	if cp.QueryDataHandler != nil {
        // 直接在这个进程内调用函数！没有网络开销。
		return cp.QueryDataHandler.QueryData(ctx, req)
	}
	return nil, plugins.ErrMethodNotImplemented
}
```

这就是为什么查询核心插件非常快的原因——它只是裹了一层 `corePlugin` 壳的本地函数调用。

## 关键代码对应的文件

| 阶段 | 文件 | 说明 |
|-----|------|------|
| **发现** | [pluginsources.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/pluginsources/pluginsources.go) | 扫描前端目录路径定义 |
| **元数据** | [plugin.json](file:///Users/wangxiaowei1/xiaowei/grafana/public/app/plugins/datasource/prometheus/plugin.json) | Prometheus 插件元数据 |
| **服务实现** | [prometheus.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/tsdb/prometheus/prometheus.go) | 后端逻辑入口 |
| **映射注册** | [coreplugins.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/coreplugin/coreplugins.go) | Map["prometheus"] = Service |
| **加载策略** | [provider.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/plugins/backendplugin/provider/provider.go) | 优先加载核心插件逻辑 |
