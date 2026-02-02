# PluginStore 包源码分析

`pluginstore` 是**插件元数据的对外查询接口**，位于 `registry` 之上，提供简化的插件查询 API。

## 架构位置

```
┌─────────────────────────────────────────┐
│   plugincontext.Provider / 其他服务      │
└─────────────────┬───────────────────────┘
                  │ 调用 Plugin()
                  ▼
┌─────────────────────────────────────────┐
│          pluginstore.Store              │
│  · 提供简化的 Plugin DTO                 │
│  · 过滤已退役插件                        │
│  · 按类型筛选                           │
└─────────────────┬───────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────┐
│          registry.Service               │
│     (存储完整的 *plugins.Plugin)         │
└─────────────────────────────────────────┘
```

## 与 Registry 的区别

| 组件 | 存储内容 | 用途 |
|-----|---------|------|
| `registry.Service` | `*plugins.Plugin`（完整对象） | 内部使用 |
| `pluginstore.Store` | `pluginstore.Plugin`（DTO） | 对外暴露 |

## 核心接口

```go
type Store interface {
    Plugin(ctx context.Context, pluginID string) (Plugin, bool)
    Plugins(ctx context.Context, pluginTypes ...plugins.Type) []Plugin
}
```

## Plugin DTO 结构

```go
type Plugin struct {
    plugins.JSONData              // 基础信息（ID、Name、Type）
    
    FS                plugins.FS
    Class             plugins.Class  // Core/Bundled/External
    
    Signature         plugins.SignatureStatus
    SignatureType     plugins.SignatureType
    
    Parent            *ParentPlugin  // App 插件父子关系
    Children          []string
    
    Module            string         // 前端模块路径
    BaseURL           string
    ExternalService   *auth.ExternalService
}
```

## 关键逻辑

```go
// 获取插件时过滤已退役的
func (s *Service) Plugin(ctx context.Context, pluginID string) (Plugin, bool) {
    p, exists := s.pluginRegistry.Plugin(ctx, pluginID, "")
    if !exists || p.IsDecommissioned() {
        return Plugin{}, false
    }
    return ToGrafanaDTO(p), true
}
```

## 相关文件

| 文件 | 说明 |
|-----|------|
| [store.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/pluginstore/store.go) | Store 接口与 Service 实现 |
| [plugins.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/pluginstore/plugins.go) | Plugin DTO 定义 |
