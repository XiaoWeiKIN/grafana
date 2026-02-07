# Backend 数据源插件注册指南

基于您的架构方案 (`docs/plugin_architecture_plan.md`)，我们将复用 Grafana 的核心注册流程。以下是在您的项目中注册一个 Backend 数据源（例如 `clickhouse`）的完整步骤。

---

## 核心流程映射

| Grafana 阶段 | 您的项目实现 |
| :--- | :--- |
| **Discovery** | 简化为硬编码或配置文件，不需要复杂的文件系统扫描。 |
| **Wiring** | 在 `main.go` 或 `wire.go` 中手动组装依赖。 |
| **Registration** | 使用 `registry.InMemory` 的 `Add` 方法。 |
| **Instantiation** | 通过 `provider` 模式（或直接从 Registry 获取）实例化。 |

---

## 详细注册步骤

### 第一步：实现数据源逻辑

首先，您需要有一个实现了 `backend.QueryDataHandler` 接口的结构体。

**文件**: `pkg/plugins/backendplugin/clickhouse/clickhouse.go` (新建)

```go
package clickhouse

import (
	"context"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"your-project/pkg/plugins/backendplugin"
	"your-project/pkg/plugins/backendplugin/coreplugin"
)

// Service 实现了 ClickHouse 的具体查询逻辑
type Service struct{}

// NewService 创建服务实例
func NewService() *Service {
	return &Service{}
}

// QueryData 处理查询请求
func (s *Service) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()
	// TODO: 实现具体的 ClickHouse 查询逻辑
	return resp, nil
}

// CheckHealth 健康检查
func (s *Service) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "ClickHouse plugin is healthy",
	}, nil
}

// Factory 将 Service 包装为插件工厂函数
// 这是关键适配器，对应 Grafana 的 `asBackendPlugin`
func Factory(s *Service) backendplugin.PluginFactoryFunc {
	// coreplugin.New 会负责将 Service 包装成标准 Plugin 接口
	return func(pluginID string, logger log.Logger, _ trace.Tracer, _ func() []string) (backendplugin.Plugin, error) {
		return coreplugin.New(pluginID, s, coreplugin.WithCheckHealthHandler(s)), nil
	}
}
```

### 第二步：在 Registry 中注册工厂 (Registration)

您需要一个地方来集中管理所有的核心插件工厂。

**文件**: `pkg/services/pluginsintegration/coreplugin/registry.go` (新建或修改)

```go
package coreplugin

import (
    "your-project/pkg/plugins/clickhouse"
    "your-project/pkg/plugins/backendplugin"
)

// CoreRegistry 维护 ID -> Factory 的映射
type CoreRegistry struct {
    store map[string]backendplugin.PluginFactoryFunc
}

// ProvideCoreRegistry 组装所有核心插件
// 类似于 Grafana 的 `ProvideCoreRegistry`
func ProvideCoreRegistry() *CoreRegistry {
    r := &CoreRegistry{
        store: make(map[string]backendplugin.PluginFactoryFunc),
    }

    // 1. 实例化具体服务
    chService := clickhouse.NewService()

    // 2. 注册到 Map 中
    r.store["clickhouse"] = clickhouse.Factory(chService)
    r.store["prometheus"] = prometheus.Factory(prometheus.NewService()) // 示例

    return r
}

// Get 获取工厂函数
func (r *CoreRegistry) Get(pluginID string) backendplugin.PluginFactoryFunc {
    return r.store[pluginID]
}
```

### 第三步：配置 Provider Chain (Instantiation)

在插件加载器中，配置责任链以优先使用 CoreRegistry。

**文件**: `pkg/plugins/backendplugin/provider/service.go` (参考 Grafana 实现)

```go
// ProvideCoreProvider 创建一个专门用于加载核心插件的 Provider
func ProvideCoreProvider(registry *CoreRegistry) PluginBackendProvider {
    return func(ctx context.Context, p *plugins.Plugin) backendplugin.PluginFactoryFunc {
        // 如果是核心插件，尝试从 Registry 获取 Factory
        // 注意：这里我们简化判断，只要 ID 在 Registry 中就认为是核心插件
        if factory := registry.Get(p.ID); factory != nil {
            return factory
        }
        return nil
    }
}
```

### 第四步：在应用启动时组装 (Wiring)

最后，在 `main.go` 或应用的初始化代码中把这一套串起来。

```go
func main() {
    // 1. 初始化 Core Registry (包含 ClickHouse, Prometheus 等)
    coreRegistry := coreplugin.ProvideCoreRegistry()

    // 2. 创建 Provider Chain
    // 链条: CoreProvider -> DefaultProvider (如果有外部插件需求)
    coreProvider := provider.ProvideCoreProvider(coreRegistry)
    providerService := provider.New(coreProvider)

    // 3. 初始化 Plugin Manager (Loader)
    loader := pluginloader.New(..., providerService, ...)
    
    // ... 启动应用 ...
}
```

### 总结交互流程

当用户在前端添加了一个 Type="clickhouse" 的数据源并点击 "Save & Test" 时：

1.  **API Handler** 调用 `DataSourceService.AddDataSource`。
2.  **CheckHealth**: API 调用 `PluginClient.CheckHealth`。
3.  **Loader**: 
    *   PluginClient 发现内存中没有 `clickhouse` 的实例。
    *   调用 `Loader.Load("clickhouse")`。
    *   Loader 调用 `ProviderService.BackendFactory`。
    *   **CoreProvider** 拦截请求，发现 `clickhouse` 在 `CoreRegistry` 中。
    *   执行 `clickhouse.Factory`，返回 `corePlugin` 实例（包裹了 `clickhouse.Service`）。
    *   实例被注册到 `PluginManager.Registry`。
4.  **Execute**: `corePlugin.CheckHealth` 被调用 -> 最终执行 `clickhouse.Service.CheckHealth`。

这样，您的 ClickHouse 插件就成功作为 Backend 插件注册并运行了！
