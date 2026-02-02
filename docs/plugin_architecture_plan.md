# 内置插件架构实现方案

## 概述

本方案提供一个完整的内置插件（Core Plugin）架构实现，基于 Grafana 的设计理念，使用 `grafana-plugin-sdk-go` 作为核心依赖。

**目标**：实现一个可扩展的插件系统，支持多种数据源插件通过统一接口进行数据查询。

---

## 一、架构概览

```
┌─────────────────────────────────────────────────────────────────┐
│                         Query Service                            │
│              (接收查询请求，协调各组件工作)                         │
└──────────────────────────────┬──────────────────────────────────┘
                               │
              ┌────────────────┴────────────────┐
              ▼                                 ▼
┌──────────────────────────┐      ┌──────────────────────────────┐
│  PluginContext Provider  │      │        Plugin Client          │
│   (构建插件执行上下文)     │      │   (调用插件执行查询)           │
└──────────────────────────┘      └──────────────┬───────────────┘
                                                 │
                                                 ▼
                               ┌─────────────────────────────────┐
                               │         Plugin Registry          │
                               │   (存储所有已注册的插件)           │
                               │    map[pluginID] → Plugin        │
                               └──────────────┬──────────────────┘
                                              │
                        ┌─────────────────────┼─────────────────────┐
                        ▼                     ▼                     ▼
                 ┌────────────┐        ┌────────────┐        ┌────────────┐
                 │   MySQL    │        │ Prometheus │        │ ClickHouse │
                 │   Plugin   │        │   Plugin   │        │   Plugin   │
                 └────────────┘        └────────────┘        └────────────┘
```

---

## 二、核心依赖

```go
// go.mod
module your-project

go 1.21

require (
    github.com/grafana/grafana-plugin-sdk-go v0.199.0
)
```

**SDK 提供的核心类型**：
- `backend.QueryDataRequest` / `backend.QueryDataResponse` - 查询请求/响应
- `backend.PluginContext` - 插件上下文
- `backend.DataSourceInstanceSettings` - 数据源配置
- `backend.DataQuery` - 单个查询
- `data.Frame` / `data.Field` - 数据帧（表格数据）

---

## 三、项目结构

```
pkg/
├── plugins/
│   ├── errors.go                    # 错误定义
│   ├── backendplugin/
│   │   ├── ifaces.go                # 插件接口定义
│   │   └── coreplugin/
│   │       └── core_plugin.go       # 内置插件实现
│   │
│   └── manager/
│       ├── registry/
│       │   ├── registry.go          # 注册表接口
│       │   └── in_memory.go         # 内存注册表实现
│       │
│       └── client/
│           └── client.go            # 插件客户端
│
├── services/
│   └── plugincontext/
│       └── provider.go              # 插件上下文提供者
│
├── datasource/
│   ├── model.go                     # 数据源模型
│   └── store.go                     # 数据源存储
│
└── query/
    └── service.go                   # 查询服务
```

---

## 四、完整代码实现

### 4.1 错误定义 (pkg/plugins/errors.go)

```go
package plugins

import "errors"

var (
    // ErrPluginNotRegistered 插件未注册
    ErrPluginNotRegistered = errors.New("plugin not registered")
    
    // ErrPluginUnavailable 插件不可用（已退役或未启动）
    ErrPluginUnavailable = errors.New("plugin unavailable")
    
    // ErrMethodNotImplemented 插件未实现该方法
    ErrMethodNotImplemented = errors.New("method not implemented")
    
    // ErrPluginRequestCanceled 请求被取消
    ErrPluginRequestCanceled = errors.New("plugin request canceled")
    
    // ErrPluginRequestFailure 请求失败
    ErrPluginRequestFailure = errors.New("plugin request failed")
    
    // ErrPluginHealthCheckFailed 健康检查失败
    ErrPluginHealthCheckFailed = errors.New("plugin health check failed")
)
```

---

### 4.2 插件接口 (pkg/plugins/backendplugin/ifaces.go)

```go
package backendplugin

import (
    "context"
    
    "github.com/grafana/grafana-plugin-sdk-go/backend"
)

// Target 表示插件的运行位置
type Target string

const (
    // TargetInMemory 内置插件，在主进程中运行
    TargetInMemory Target = "in_memory"
)

// Plugin 定义了一个后端插件必须实现的接口
type Plugin interface {
    // PluginID 返回插件唯一标识符
    PluginID() string
    
    // Start 启动插件（内置插件通常为空实现）
    Start(ctx context.Context) error
    
    // Stop 停止插件
    Stop(ctx context.Context) error
    
    // IsManaged 是否由管理器管理生命周期
    IsManaged() bool
    
    // Exited 插件是否已退出
    Exited() bool
    
    // Decommission 标记插件为退役状态
    Decommission() error
    
    // IsDecommissioned 是否已退役
    IsDecommissioned() bool
    
    // Target 返回插件运行位置
    Target() Target
    
    // 以下接口来自 grafana-plugin-sdk-go/backend
    // 插件根据需要实现对应的 Handler
    
    backend.QueryDataHandler       // 查询数据（必须实现）
    backend.CheckHealthHandler     // 健康检查（推荐实现）
    backend.CallResourceHandler    // 资源调用（可选）
}
```

---

### 4.3 内置插件实现 (pkg/plugins/backendplugin/coreplugin/core_plugin.go)

```go
package coreplugin

import (
    "context"
    "sync"
    
    "github.com/grafana/grafana-plugin-sdk-go/backend"
    
    "your-project/pkg/plugins/backendplugin"
)

// corePlugin 是内置插件的实现
// 它直接在主进程中运行，不需要启动外部进程
type corePlugin struct {
    pluginID       string
    mu             sync.RWMutex
    decommissioned bool
    
    // 嵌入 SDK 的 Handler 接口
    // 这些接口由具体的数据源实现提供
    backend.QueryDataHandler
    backend.CheckHealthHandler
    backend.CallResourceHandler
}

// New 创建一个新的内置插件
// 
// 参数:
//   - pluginID: 插件唯一标识符，如 "mysql", "prometheus"
//   - queryHandler: 查询数据处理器（必须提供）
//   - opts: 可选的处理器
func New(pluginID string, queryHandler backend.QueryDataHandler, opts ...Option) backendplugin.Plugin {
    p := &corePlugin{
        pluginID:         pluginID,
        QueryDataHandler: queryHandler,
    }
    
    for _, opt := range opts {
        opt(p)
    }
    
    return p
}

// Option 用于配置 corePlugin 的可选参数
type Option func(*corePlugin)

// WithCheckHealthHandler 添加健康检查处理器
func WithCheckHealthHandler(h backend.CheckHealthHandler) Option {
    return func(p *corePlugin) {
        p.CheckHealthHandler = h
    }
}

// WithCallResourceHandler 添加资源调用处理器
func WithCallResourceHandler(h backend.CallResourceHandler) Option {
    return func(p *corePlugin) {
        p.CallResourceHandler = h
    }
}

// --- 实现 backendplugin.Plugin 接口 ---

func (p *corePlugin) PluginID() string {
    return p.pluginID
}

func (p *corePlugin) Start(ctx context.Context) error {
    // 内置插件不需要启动，直接可用
    return nil
}

func (p *corePlugin) Stop(ctx context.Context) error {
    // 内置插件不需要停止
    return nil
}

func (p *corePlugin) IsManaged() bool {
    // 内置插件不由外部进程管理
    return false
}

func (p *corePlugin) Exited() bool {
    // 内置插件永远不会退出
    return false
}

func (p *corePlugin) Decommission() error {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.decommissioned = true
    return nil
}

func (p *corePlugin) IsDecommissioned() bool {
    p.mu.RLock()
    defer p.mu.RUnlock()
    return p.decommissioned
}

func (p *corePlugin) Target() backendplugin.Target {
    return backendplugin.TargetInMemory
}

// --- 实现 SDK Handler 接口 ---

func (p *corePlugin) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    if p.QueryDataHandler == nil {
        return nil, backend.ErrMethodNotImplemented
    }
    return p.QueryDataHandler.QueryData(ctx, req)
}

func (p *corePlugin) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
    if p.CheckHealthHandler == nil {
        return &backend.CheckHealthResult{
            Status:  backend.HealthStatusOk,
            Message: "Plugin is running",
        }, nil
    }
    return p.CheckHealthHandler.CheckHealth(ctx, req)
}

func (p *corePlugin) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
    if p.CallResourceHandler == nil {
        return backend.ErrMethodNotImplemented
    }
    return p.CallResourceHandler.CallResource(ctx, req, sender)
}
```

---

### 4.4 注册表接口 (pkg/plugins/manager/registry/registry.go)

```go
package registry

import (
    "context"
    
    "your-project/pkg/plugins/backendplugin"
)

// Service 定义插件注册表的接口
type Service interface {
    // Plugin 根据 ID 获取插件
    Plugin(ctx context.Context, pluginID string) (backendplugin.Plugin, bool)
    
    // Plugins 获取所有已注册的插件
    Plugins(ctx context.Context) []backendplugin.Plugin
    
    // Add 注册一个插件
    Add(ctx context.Context, plugin backendplugin.Plugin) error
    
    // Remove 移除一个插件
    Remove(ctx context.Context, pluginID string) error
}
```

---

### 4.5 内存注册表实现 (pkg/plugins/manager/registry/in_memory.go)

```go
package registry

import (
    "context"
    "fmt"
    "sync"
    
    "your-project/pkg/plugins/backendplugin"
)

// InMemory 是一个基于内存的插件注册表
type InMemory struct {
    store map[string]backendplugin.Plugin
    mu    sync.RWMutex
}

// NewInMemory 创建一个新的内存注册表
func NewInMemory() *InMemory {
    return &InMemory{
        store: make(map[string]backendplugin.Plugin),
    }
}

// Plugin 根据 ID 获取插件
func (i *InMemory) Plugin(ctx context.Context, pluginID string) (backendplugin.Plugin, bool) {
    i.mu.RLock()
    defer i.mu.RUnlock()
    
    p, exists := i.store[pluginID]
    return p, exists
}

// Plugins 获取所有已注册的插件
func (i *InMemory) Plugins(ctx context.Context) []backendplugin.Plugin {
    i.mu.RLock()
    defer i.mu.RUnlock()
    
    result := make([]backendplugin.Plugin, 0, len(i.store))
    for _, p := range i.store {
        result = append(result, p)
    }
    return result
}

// Add 注册一个插件
func (i *InMemory) Add(ctx context.Context, plugin backendplugin.Plugin) error {
    i.mu.Lock()
    defer i.mu.Unlock()
    
    if _, exists := i.store[plugin.PluginID()]; exists {
        return fmt.Errorf("plugin %s already registered", plugin.PluginID())
    }
    
    i.store[plugin.PluginID()] = plugin
    return nil
}

// Remove 移除一个插件
func (i *InMemory) Remove(ctx context.Context, pluginID string) error {
    i.mu.Lock()
    defer i.mu.Unlock()
    
    if _, exists := i.store[pluginID]; !exists {
        return fmt.Errorf("plugin %s not found", pluginID)
    }
    
    delete(i.store, pluginID)
    return nil
}
```

---

### 4.6 插件客户端 (pkg/plugins/manager/client/client.go)

```go
package client

import (
    "context"
    "errors"
    
    "github.com/grafana/grafana-plugin-sdk-go/backend"
    
    "your-project/pkg/plugins"
    "your-project/pkg/plugins/backendplugin"
    "your-project/pkg/plugins/manager/registry"
)

var _ plugins.Client = (*Service)(nil)

var (
    errNilRequest = errors.New("req cannot be nil")
    errNilSender  = errors.New("sender cannot be nil")
)

// Service 实现了 plugins.Client 接口
type Service struct {
    pluginRegistry registry.Service
}

// ProvideService 创建插件客户端服务（用于依赖注入）
func ProvideService(pluginRegistry registry.Service) *Service {
    return &Service{
        pluginRegistry: pluginRegistry,
    }
}

// QueryData 执行数据查询
func (s *Service) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    if req == nil {
        return nil, errNilRequest
    }

    p, exists := s.plugin(ctx, req.PluginContext.PluginID)
    if !exists {
        return nil, plugins.ErrPluginNotRegistered
    }

    resp, err := p.QueryData(ctx, req)
    if err != nil {
        // 处理上下文取消
        if errors.Is(err, context.Canceled) {
            return nil, plugins.ErrPluginRequestCanceled
        }
        return nil, plugins.ErrPluginRequestFailure
    }

    // 为响应中的 Frame 设置 RefID
    for refID, res := range resp.Responses {
        for _, f := range res.Frames {
            if f.RefID == "" {
                f.RefID = refID
            }
        }
    }

    return resp, nil
}

// CheckHealth 检查插件健康状态
func (s *Service) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
    if req == nil {
        return nil, errNilRequest
    }

    p, exists := s.plugin(ctx, req.PluginContext.PluginID)
    if !exists {
        return nil, plugins.ErrPluginNotRegistered
    }

    resp, err := p.CheckHealth(ctx, req)
    if err != nil {
        if errors.Is(err, plugins.ErrMethodNotImplemented) {
            return nil, err
        }
        if errors.Is(err, context.Canceled) {
            return nil, plugins.ErrPluginRequestCanceled
        }
        return nil, plugins.ErrPluginHealthCheckFailed
    }

    return resp, nil
}

// CallResource 调用插件资源
func (s *Service) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
    if req == nil {
        return errNilRequest
    }
    if sender == nil {
        return errNilSender
    }

    p, exists := s.plugin(ctx, req.PluginContext.PluginID)
    if !exists {
        return plugins.ErrPluginNotRegistered
    }

    err := p.CallResource(ctx, req, sender)
    if err != nil {
        if errors.Is(err, context.Canceled) {
            return plugins.ErrPluginRequestCanceled
        }
        return plugins.ErrPluginRequestFailure
    }

    return nil
}

// plugin 从注册表获取可用的插件（过滤已退役的）
func (s *Service) plugin(ctx context.Context, pluginID string) (backendplugin.Plugin, bool) {
    p, exists := s.pluginRegistry.Plugin(ctx, pluginID)
    if !exists {
        return nil, false
    }

    if p.IsDecommissioned() {
        return nil, false
    }

    return p, true
}
```

**与 Grafana 一致的设计**：
- 私有 `plugin()` 方法封装插件获取和退役检查
- 错误处理：区分取消、未实现、失败等情况
- QueryData 自动填充 Frame 的 RefID
- 参数校验返回具体错误

---

### 4.7 数据源模型 (pkg/datasource/model.go)

```go
package datasource

import (
    "encoding/json"
    "time"
)

// DataSource 表示一个数据源配置
type DataSource struct {
    ID             int64             `json:"id"`
    UID            string            `json:"uid"`
    Name           string            `json:"name"`
    Type           string            `json:"type"`  // 对应 pluginID
    URL            string            `json:"url"`
    Database       string            `json:"database"`
    User           string            `json:"user"`
    JSONData       json.RawMessage   `json:"jsonData"`
    SecureJSONData map[string][]byte `json:"-"`  // 加密存储，不序列化
    Created        time.Time         `json:"created"`
    Updated        time.Time         `json:"updated"`
}
```

---

### 4.8 数据源缓存 (pkg/datasource/cache.go)

```go
package datasource

import (
    "context"
    "fmt"
    "sync"
    "time"
)

const (
    defaultCacheTTL = 5 * time.Second
)

// CacheService 定义数据源缓存服务接口
type CacheService interface {
    // GetByUID 根据 UID 获取数据源（优先从缓存）
    GetByUID(ctx context.Context, uid string, skipCache bool) (*DataSource, error)
    
    // GetByID 根据 ID 获取数据源（优先从缓存）
    GetByID(ctx context.Context, id int64, skipCache bool) (*DataSource, error)
    
    // InvalidateCache 使缓存失效
    InvalidateCache(uid string)
}

// Store 定义数据源存储接口（数据库层）
type Store interface {
    GetByUID(ctx context.Context, uid string) (*DataSource, error)
    GetByID(ctx context.Context, id int64) (*DataSource, error)
}

// cacheEntry 缓存条目
type cacheEntry struct {
    ds        *DataSource
    expiresAt time.Time
}

// Cache 数据源缓存实现
type Cache struct {
    store Store
    ttl   time.Duration
    
    mu    sync.RWMutex
    cache map[string]*cacheEntry  // key: uid or "id:{id}"
}

// NewCache 创建数据源缓存
func NewCache(store Store, ttl time.Duration) *Cache {
    if ttl == 0 {
        ttl = defaultCacheTTL
    }
    return &Cache{
        store: store,
        ttl:   ttl,
        cache: make(map[string]*cacheEntry),
    }
}

// GetByUID 根据 UID 获取数据源
func (c *Cache) GetByUID(ctx context.Context, uid string, skipCache bool) (*DataSource, error) {
    if !skipCache {
        if ds := c.get(uid); ds != nil {
            return ds, nil
        }
    }
    
    ds, err := c.store.GetByUID(ctx, uid)
    if err != nil {
        return nil, err
    }
    
    c.set(uid, ds)
    return ds, nil
}

// GetByID 根据 ID 获取数据源
func (c *Cache) GetByID(ctx context.Context, id int64, skipCache bool) (*DataSource, error) {
    key := fmt.Sprintf("id:%d", id)
    
    if !skipCache {
        if ds := c.get(key); ds != nil {
            return ds, nil
        }
    }
    
    ds, err := c.store.GetByID(ctx, id)
    if err != nil {
        return nil, err
    }
    
    c.set(key, ds)
    c.set(ds.UID, ds)  // 同时按 UID 缓存
    return ds, nil
}

// InvalidateCache 使缓存失效
func (c *Cache) InvalidateCache(uid string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.cache, uid)
}

func (c *Cache) get(key string) *DataSource {
    c.mu.RLock()
    defer c.mu.RUnlock()
    
    entry, exists := c.cache[key]
    if !exists || time.Now().After(entry.expiresAt) {
        return nil
    }
    return entry.ds
}

func (c *Cache) set(key string, ds *DataSource) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    c.cache[key] = &cacheEntry{
        ds:        ds,
        expiresAt: time.Now().Add(c.ttl),
    }
}
```

---

### 4.9 插件上下文提供者 (pkg/services/plugincontext/provider.go)

```go
package plugincontext

import (
    "context"
    
    "github.com/grafana/grafana-plugin-sdk-go/backend"
    
    "your-project/pkg/datasource"
    "your-project/pkg/plugins"
    "your-project/pkg/plugins/manager/registry"
)

// Provider 负责构建插件上下文
// 职责：
//   1. 校验插件是否存在
//   2. 从缓存获取数据源配置
//   3. 构建插件上下文（包含解密后的数据源配置）
type Provider struct {
    registry        registry.Service         // 插件注册表
    dsCache         datasource.CacheService  // 数据源缓存
    secretService   SecretService            // 加密服务
}

// SecretService 定义解密服务的接口
type SecretService interface {
    Decrypt(ctx context.Context, data []byte) ([]byte, error)
}

// NewProvider 创建一个新的上下文提供者
func NewProvider(registry registry.Service, dsCache datasource.CacheService, secretService SecretService) *Provider {
    return &Provider{
        registry:      registry,
        dsCache:       dsCache,
        secretService: secretService,
    }
}

// GetWithDataSource 构建包含数据源配置的插件上下文
func (p *Provider) GetWithDataSource(ctx context.Context, pluginID string, ds *datasource.DataSource) (backend.PluginContext, error) {
    // 1. 校验插件是否存在
    _, exists := p.registry.Plugin(ctx, pluginID)
    if !exists {
        return backend.PluginContext{}, plugins.ErrPluginNotRegistered
    }
    
    // 2. 解密敏感数据
    decryptedData := p.decryptSecureData(ctx, ds)
    
    // 3. 构建上下文
    return backend.PluginContext{
        PluginID: pluginID,
        DataSourceInstanceSettings: &backend.DataSourceInstanceSettings{
            ID:                      ds.ID,
            UID:                     ds.UID,
            Name:                    ds.Name,
            URL:                     ds.URL,
            Database:                ds.Database,
            User:                    ds.User,
            JSONData:                ds.JSONData,
            DecryptedSecureJSONData: decryptedData,
            Updated:                 ds.Updated,
        },
    }, nil
}

// GetDataSourceInstanceSettings 根据 UID 获取数据源配置（使用缓存）
func (p *Provider) GetDataSourceInstanceSettings(ctx context.Context, uid string) (*backend.DataSourceInstanceSettings, error) {
    ds, err := p.dsCache.GetByUID(ctx, uid, false)  // 使用缓存
    if err != nil {
        return nil, err
    }
    
    decryptedData := p.decryptSecureData(ctx, ds)
    
    return &backend.DataSourceInstanceSettings{
        ID:                      ds.ID,
        UID:                     ds.UID,
        Name:                    ds.Name,
        URL:                     ds.URL,
        Database:                ds.Database,
        User:                    ds.User,
        JSONData:                ds.JSONData,
        DecryptedSecureJSONData: decryptedData,
        Updated:                 ds.Updated,
    }, nil
}

// decryptSecureData 解密数据源的敏感数据
func (p *Provider) decryptSecureData(ctx context.Context, ds *datasource.DataSource) map[string]string {
    result := make(map[string]string)
    
    for key, encryptedValue := range ds.SecureJSONData {
        decrypted, err := p.secretService.Decrypt(ctx, encryptedValue)
        if err != nil {
            continue
        }
        result[key] = string(decrypted)
    }
    
    return result
}
```


### 4.10 查询服务 (pkg/query/service.go)

```go
package query

import (
    "context"
    "fmt"
    
    "github.com/grafana/grafana-plugin-sdk-go/backend"
    
    "your-project/pkg/datasource"
    "your-project/pkg/plugins/manager/client"
    "your-project/pkg/services/plugincontext"
)

// Service 是查询服务的实现
type Service struct {
    pluginClient    *client.Service
    contextProvider *plugincontext.Provider
    dsCache         datasource.CacheService  // 使用缓存
}

// NewService 创建查询服务
func NewService(
    pluginClient *client.Service,
    contextProvider *plugincontext.Provider,
    dsCache datasource.CacheService,
) *Service {
    return &Service{
        pluginClient:    pluginClient,
        contextProvider: contextProvider,
        dsCache:         dsCache,
    }
}

// QueryData 执行数据查询
func (s *Service) QueryData(ctx context.Context, datasourceUID string, queries []backend.DataQuery) (*backend.QueryDataResponse, error) {
    // 1. 从缓存获取数据源配置
    ds, err := s.dsCache.GetByUID(ctx, datasourceUID, false)
    if err != nil {
        return nil, fmt.Errorf("failed to get datasource: %w", err)
    }
    
    // 2. 构建插件上下文
    pCtx, err := s.contextProvider.GetWithDataSource(ctx, ds.Type, ds)
    if err != nil {
        return nil, err
    }
    
    // 3. 执行查询
    return s.pluginClient.QueryData(ctx, &backend.QueryDataRequest{
        PluginContext: pCtx,
        Queries:       queries,
    })
}
```

---

## 五、初始化与使用

### 5.1 服务初始化 (cmd/server/main.go)

```go
package main

import (
    "context"
    "log"
    "time"
    
    "your-project/pkg/plugins/manager/registry"
    "your-project/pkg/plugins/manager/client"
    "your-project/pkg/plugins/backendplugin/coreplugin"
    "your-project/pkg/services/plugincontext"
    "your-project/pkg/datasource"
    "your-project/pkg/query"
)

func main() {
    ctx := context.Background()
    
    // 1. 创建插件注册表
    pluginRegistry := registry.NewInMemory()
    
    // 2. 创建插件客户端
    pluginClient := client.ProvideService(pluginRegistry)
    
    // 3. 创建数据源缓存（TTL 5秒）
    dsCache := datasource.NewCache(dsStore, 5*time.Second)
    
    // 4. 创建上下文提供者
    contextProvider := plugincontext.NewProvider(pluginRegistry, dsCache, secretService)
    
    // 5. 创建查询服务
    queryService := query.NewService(pluginClient, contextProvider, dsCache)
    
    // 6. 注册插件
    registerPlugins(ctx, pluginRegistry)
    
    log.Println("Service started")
}

func registerPlugins(ctx context.Context, reg registry.Service) {
    // mysqlPlugin := coreplugin.New("mysql", mysqlHandler)
    // reg.Add(ctx, mysqlPlugin)
}
```

### 5.2 数据源插件示例

```go
package mysql

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    
    "github.com/grafana/grafana-plugin-sdk-go/backend"
    "github.com/grafana/grafana-plugin-sdk-go/data"
    _ "github.com/go-sql-driver/mysql"
)

// MySQLHandler 实现 backend.QueryDataHandler 接口
type MySQLHandler struct{}

func NewHandler() *MySQLHandler {
    return &MySQLHandler{}
}

func (h *MySQLHandler) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    response := backend.NewQueryDataResponse()
    
    // 从上下文获取数据源配置
    ds := req.PluginContext.DataSourceInstanceSettings
    password := ds.DecryptedSecureJSONData["password"]
    dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s", ds.User, password, ds.URL, ds.Database)
    
    db, err := sql.Open("mysql", dsn)
    if err != nil {
        return nil, err
    }
    defer db.Close()
    
    // 处理每个查询
    for _, q := range req.Queries {
        response.Responses[q.RefID] = h.executeQuery(ctx, db, q)
    }
    
    return response, nil
}

func (h *MySQLHandler) executeQuery(ctx context.Context, db *sql.DB, q backend.DataQuery) backend.DataResponse {
    var queryModel struct {
        RawSQL string `json:"rawSql"`
    }
    json.Unmarshal(q.JSON, &queryModel)
    
    rows, err := db.QueryContext(ctx, queryModel.RawSQL)
    if err != nil {
        return backend.DataResponse{Error: err}
    }
    defer rows.Close()
    
    // 构建数据帧
    frame := data.NewFrame("response")
    // ... 填充数据 ...
    
    return backend.DataResponse{
        Frames: data.Frames{frame},
    }
}
```

---

## 六、组件职责说明

| 组件 | 职责 |
|-----|------|
| **Plugin 接口** | 定义插件必须实现的方法 |
| **corePlugin** | 内置插件的通用实现，封装具体的 Handler |
| **Registry** | 存储和管理所有已注册的插件 |
| **Client** | 查询服务与插件之间的桥梁，负责路由请求 |
| **PluginContext Provider** | 构建插件执行所需的上下文信息 |
| **QueryService** | 接收外部查询请求，协调各组件完成查询 |

---

## 七、数据流

```
1. HTTP 请求到达 QueryService
       │
       ▼
2. QueryService 从 DatasourceStore 获取数据源配置
       │
       ▼
3. PluginContext Provider 构建 backend.PluginContext
   - 设置 PluginID
   - 设置 DataSourceInstanceSettings（包含解密后的密码）
   - 设置 GrafanaConfig
       │
       ▼
4. QueryService 调用 Client.QueryData()
       │
       ▼
5. Client 从 Registry 获取对应的 Plugin
       │
       ▼
6. Client 调用 Plugin.QueryData()
       │
       ▼
7. corePlugin 调用内部 Handler 的 QueryData()
       │
       ▼
8. Handler 执行实际的数据库查询，返回 data.Frame
       │
       ▼
9. 响应沿原路返回
```

---

## 八、扩展指南

### 添加新的数据源插件

1. 创建 Handler 实现 `backend.QueryDataHandler`
2. 在启动时注册到 Registry

```go
// 1. 实现 Handler
type ClickHouseHandler struct{}

func (h *ClickHouseHandler) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    // 实现查询逻辑
}

// 2. 注册插件
clickhousePlugin := coreplugin.New("clickhouse", &ClickHouseHandler{})
registry.Add(ctx, clickhousePlugin)
```

### 添加健康检查

```go
// 实现 CheckHealthHandler
func (h *MySQLHandler) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
    // 尝试连接数据库
    return &backend.CheckHealthResult{
        Status:  backend.HealthStatusOk,
        Message: "Database connection successful",
    }, nil
}

// 注册时添加选项
plugin := coreplugin.New("mysql", mysqlHandler, 
    coreplugin.WithCheckHealthHandler(mysqlHandler))
```

---

## 九、实现检查清单

### Phase 1: 基础框架
- [ ] 创建项目结构
- [ ] 添加 grafana-plugin-sdk-go 依赖
- [ ] 实现 `plugins/errors.go`
- [ ] 实现 `backendplugin/ifaces.go`
- [ ] 实现 `backendplugin/coreplugin/core_plugin.go`

### Phase 2: 注册与路由
- [ ] 实现 `registry/registry.go` 接口
- [ ] 实现 `registry/in_memory.go`
- [ ] 实现 `client/client.go`

### Phase 3: 上下文与查询
- [ ] 实现 `datasource/model.go`
- [ ] 实现 `plugincontext/provider.go`
- [ ] 实现 `query/service.go`

### Phase 4: 集成测试
- [ ] 编写单元测试
- [ ] 实现一个简单的测试插件
- [ ] 端到端测试

---

## 十、参考资源

- [grafana-plugin-sdk-go 文档](https://pkg.go.dev/github.com/grafana/grafana-plugin-sdk-go)
- [backend 包](https://pkg.go.dev/github.com/grafana/grafana-plugin-sdk-go/backend)
- [data 包](https://pkg.go.dev/github.com/grafana/grafana-plugin-sdk-go/data)
