# 内置插件架构实现方案

## 概述

本方案提供一个完整的内置插件（Core Plugin）架构实现，基于 Grafana 的设计理念，使用 `grafana-plugin-sdk-go` 作为核心依赖。

**目标**：实现一个可扩展的插件系统，支持多种数据源插件通过统一接口进行数据查询。

---

## 一、架构概览

遵循 Grafana 的核心设计，我们将插件系统分为 **元数据管理（Metadata Store）** 和 **后端执行管理（Execution Registry）** 两部分。

```
                       ┌──────────────────────────┐
                       │      Query Service       │
                       └────────────┬─────────────┘
                                    │
              ┌─────────────────────┴─────────────────────┐
              ▼                                           ▼
┌──────────────────────────┐                ┌────────────────────────────┐
│      Plugin Store        │                │       Plugin Client        │
│   (元数据/类型验证/别名)  │                │    (路由并调用后端插件)    │
└─────────────┬────────────┘                └─────────────┬──────────────┘
              │                                           │
              │                                           ▼
              │                             ┌────────────────────────────┐
              │                             │      Plugin Registry       │
              └────────────────────────────▶│    (后端执行实例管理集)    │
                                            └─────────────┬──────────────┘
                                                          │
                                    ┌─────────────────────┼─────────────────────┐
                                    ▼                     ▼                     ▼
                             ┌────────────┐        ┌────────────┐        ┌────────────┐
                             │   MySQL    │        │ Prometheus │        │ Clickhouse │
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
    gorm.io/gorm v1.25.7
    gorm.io/driver/mysql v1.5.4    // MySQL
    gorm.io/driver/postgres v1.5.6 // PostgreSQL
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
│   ├── errors.go                    # 插件错误定义
│   ├── backendplugin/
│   │   ├── ifaces.go                # 插件接口定义 (Execution)
│   │   └── coreplugin/
│   │       └── core_plugin.go       # 内置插件实现
│   │
│   └── manager/
│       ├── registry/
│       │   ├── registry.go          # 注册表接口 (Execution Registry)
│       │   └── in_memory.go         # 内存注册表实现
│       │
│       └── client/
│           └── client.go            # 插件客户端 (Route to Registry)
│
├── services/
│   ├── pluginsintegration/
│   │   ├── pluginstore/
│   │   │   └── store.go             # 插件元数据存储 (Metadata Store)
│   │   └── plugincontext/
│   │       └── provider.go          # 插件上下文提供者
│   ├── datasources/                 # 数据源服务
│   │   ├── errors.go                # 错误定义
│   │   ├── models.go                # 数据模型和 DTO
│   │   ├── service.go               # 服务接口
│   │   └── service/
│   │       ├── datasource_service.go    # GORM 实现
│   │       └── cache.go                 # 缓存实现
│
└── query/
    └── service.go                   # 查询服务
```

---

## 四、完整代码实现

### 4.1 插件元数据存储 (pkg/services/pluginsintegration/pluginstore/store.go)

```go
package pluginstore

import (
	"context"

	"your-project/pkg/plugins/backendplugin"
)

// Store 是插件元数据的公开存储接口
type Store interface {
	// Plugin 根据 ID 查找插件元数据
	Plugin(ctx context.Context, pluginID string) (PluginMetadata, bool)
	// Plugins 返回指定类型的插件
	Plugins(ctx context.Context, pluginTypes ...string) []PluginMetadata
}

type PluginMetadata struct {
	ID       string
	Name     string
	Type     string
	AliasIDs []string
}
```

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
    pluginStore    pluginstore.Store // 可选：用于增强验证
}

// ProvideService 创建插件客户端服务（用于依赖注入）
func ProvideService(pluginRegistry registry.Service, pluginStore pluginstore.Store) *Service {
    return &Service{
        pluginRegistry: pluginRegistry,
        pluginStore:    pluginStore,
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

### 4.7 数据源服务 (pkg/services/datasources)

参考 Grafana `pkg/services/datasources`，简化实现，使用 GORM v2。

#### 4.7.1 错误定义 (pkg/services/datasources/errors.go)

```go
package datasources

import "errors"

var (
    ErrDataSourceNotFound      = errors.New("data source not found")
    ErrDataSourceUIDExists     = errors.New("data source with the same uid already exists")
    ErrDataSourceNameExists    = errors.New("data source with the same name already exists")
    ErrDataSourceAccessDenied  = errors.New("data source access denied")
    ErrDataSourceInvalidType   = errors.New("invalid data source type")
)
```

#### 4.7.2 数据源模型 (pkg/services/datasources/models.go)

```go
package datasources

import (
    "encoding/json"
    "time"
)

// 数据源访问模式
type DsAccess string

const (
    DS_ACCESS_DIRECT DsAccess = "direct"  // 浏览器直连
    DS_ACCESS_PROXY  DsAccess = "proxy"   // 通过后端代理
)

// DataSource 数据源模型（GORM 表结构）
type DataSource struct {
    ID      int64  `json:"id" gorm:"primaryKey;autoIncrement"`
    OrgID   int64  `json:"orgId" gorm:"column:org_id;index"`
    Version int    `json:"version"`

    Name           string          `json:"name" gorm:"uniqueIndex:idx_datasource_org_name"`
    Type           string          `json:"type"`  // 对应 pluginID
    Access         DsAccess        `json:"access"`
    URL            string          `json:"url"`
    User           string          `json:"user"`
    Database       string          `json:"database"`
    BasicAuth      bool            `json:"basicAuth"`
    BasicAuthUser  string          `json:"basicAuthUser"`
    IsDefault      bool            `json:"isDefault"`
    ReadOnly       bool            `json:"readOnly"`
    UID            string          `json:"uid" gorm:"uniqueIndex:idx_datasource_org_uid"`
    JSONData       json.RawMessage `json:"jsonData" gorm:"type:text"`
    SecureJSONData map[string][]byte `json:"-" gorm:"type:text;serializer:json"`

    Created time.Time `json:"created" gorm:"autoCreateTime"`
    Updated time.Time `json:"updated" gorm:"autoUpdateTime"`
}

// TableName GORM 表名
func (DataSource) TableName() string {
    return "data_source"
}

// --- 查询参数 ---

// GetDataSourceQuery 获取单个数据源的查询参数
type GetDataSourceQuery struct {
    ID    int64
    UID   string
    Name  string
    OrgID int64  // 必填
}

// GetDataSourcesByTypeQuery 按类型获取数据源的查询参数
type GetDataSourcesByTypeQuery struct {
    OrgID int64   // 可选，0 表示所有组织
    Type  string  // 必填
}

// --- 命令参数 ---

// AddDataSourceCommand 添加数据源命令
type AddDataSourceCommand struct {
    Name           string            `json:"name" binding:"required"`
    Type           string            `json:"type" binding:"required"`
    Access         DsAccess          `json:"access" binding:"required"`
    URL            string            `json:"url"`
    User           string            `json:"user"`
    Database       string            `json:"database"`
    BasicAuth      bool              `json:"basicAuth"`
    BasicAuthUser  string            `json:"basicAuthUser"`
    IsDefault      bool              `json:"isDefault"`
    JSONData       json.RawMessage   `json:"jsonData"`
    SecureJSONData map[string]string `json:"secureJsonData"`  // 明文，需加密
    UID            string            `json:"uid"`

    OrgID int64 `json:"-"`  // 从上下文获取
}

// UpdateDataSourceCommand 更新数据源命令
type UpdateDataSourceCommand struct {
    Name           string            `json:"name" binding:"required"`
    Type           string            `json:"type" binding:"required"`
    Access         DsAccess          `json:"access" binding:"required"`
    URL            string            `json:"url"`
    User           string            `json:"user"`
    Database       string            `json:"database"`
    BasicAuth      bool              `json:"basicAuth"`
    BasicAuthUser  string            `json:"basicAuthUser"`
    IsDefault      bool              `json:"isDefault"`
    JSONData       json.RawMessage   `json:"jsonData"`
    SecureJSONData map[string]string `json:"secureJsonData"`
    UID            string            `json:"uid"`
    Version        int               `json:"version"`  // 乐观锁

    OrgID int64 `json:"-"`
    ID    int64 `json:"-"`
}

// DeleteDataSourceCommand 删除数据源命令
type DeleteDataSourceCommand struct {
    ID    int64
    UID   string
    Name  string
    OrgID int64
}
```

#### 4.7.3 服务接口 (pkg/services/datasources/service.go)

```go
package datasources

import "context"

// DataSourceService 数据源服务接口（简化版）
type DataSourceService interface {
    // GetDataSource 获取单个数据源
    GetDataSource(ctx context.Context, query *GetDataSourceQuery) (*DataSource, error)

    // GetDataSourcesByType 按类型获取数据源
    GetDataSourcesByType(ctx context.Context, query *GetDataSourcesByTypeQuery) ([]*DataSource, error)

    // AddDataSource 添加数据源
    AddDataSource(ctx context.Context, cmd *AddDataSourceCommand) (*DataSource, error)

    // UpdateDataSource 更新数据源
    UpdateDataSource(ctx context.Context, cmd *UpdateDataSourceCommand) (*DataSource, error)

    // DeleteDataSource 删除数据源
    DeleteDataSource(ctx context.Context, cmd *DeleteDataSourceCommand) error
}

// CacheService 数据源缓存接口
type CacheService interface {
    GetByID(ctx context.Context, id int64, skipCache bool) (*DataSource, error)
    GetByUID(ctx context.Context, uid string, skipCache bool) (*DataSource, error)
    InvalidateCache(uid string)
}
```

#### 4.7.4 GORM 实现 (pkg/services/datasources/service/datasource_service.go)

```go
package service

import (
    "context"
    "errors"
    "time"

    "gorm.io/gorm"

    "your-project/pkg/services/datasources"
)

// Service 数据源服务实现
type Service struct {
    db            *gorm.DB
    secretService SecretService
}

// SecretService 加密服务接口
type SecretService interface {
    Encrypt(ctx context.Context, data []byte) ([]byte, error)
    Decrypt(ctx context.Context, data []byte) ([]byte, error)
}

// NewService 创建数据源服务
func NewService(db *gorm.DB, secretService SecretService) *Service {
    return &Service{
        db:            db,
        secretService: secretService,
    }
}

// GetDataSource 获取单个数据源
func (s *Service) GetDataSource(ctx context.Context, query *datasources.GetDataSourceQuery) (*datasources.DataSource, error) {
    if query.OrgID == 0 {
        return nil, errors.New("orgID is required")
    }

    var ds datasources.DataSource
    tx := s.db.WithContext(ctx).Where("org_id = ?", query.OrgID)

    // 按优先级查询：UID > ID > Name
    if query.UID != "" {
        tx = tx.Where("uid = ?", query.UID)
    } else if query.ID != 0 {
        tx = tx.Where("id = ?", query.ID)
    } else if query.Name != "" {
        tx = tx.Where("name = ?", query.Name)
    } else {
        return nil, errors.New("UID, ID, or Name is required")
    }

    if err := tx.First(&ds).Error; err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
            return nil, datasources.ErrDataSourceNotFound
        }
        return nil, err
    }

    return &ds, nil
}

// GetDataSourcesByType 按类型获取数据源
func (s *Service) GetDataSourcesByType(ctx context.Context, query *datasources.GetDataSourcesByTypeQuery) ([]*datasources.DataSource, error) {
    if query.Type == "" {
        return nil, errors.New("type is required")
    }

    var result []*datasources.DataSource
    tx := s.db.WithContext(ctx).Where("type = ?", query.Type)

    if query.OrgID != 0 {
        tx = tx.Where("org_id = ?", query.OrgID)
    }

    if err := tx.Find(&result).Error; err != nil {
        return nil, err
    }

    return result, nil
}

// AddDataSource 添加数据源
func (s *Service) AddDataSource(ctx context.Context, cmd *datasources.AddDataSourceCommand) (*datasources.DataSource, error) {
    if cmd.OrgID == 0 {
        return nil, errors.New("orgID is required")
    }

    // 生成 UID（如果未提供）
    if cmd.UID == "" {
        cmd.UID = generateUID()
    }

    // 检查唯一性
    if err := s.checkUniqueness(ctx, cmd.OrgID, cmd.UID, cmd.Name, 0); err != nil {
        return nil, err
    }

    // 加密敏感数据
    encryptedData, err := s.encryptSecureData(ctx, cmd.SecureJSONData)
    if err != nil {
        return nil, err
    }

    ds := &datasources.DataSource{
        OrgID:          cmd.OrgID,
        Name:           cmd.Name,
        Type:           cmd.Type,
        Access:         cmd.Access,
        URL:            cmd.URL,
        User:           cmd.User,
        Database:       cmd.Database,
        BasicAuth:      cmd.BasicAuth,
        BasicAuthUser:  cmd.BasicAuthUser,
        IsDefault:      cmd.IsDefault,
        UID:            cmd.UID,
        JSONData:       cmd.JSONData,
        SecureJSONData: encryptedData,
        Version:        1,
    }

    // 如果设置为默认，取消其他默认
    if cmd.IsDefault {
        s.db.WithContext(ctx).Model(&datasources.DataSource{}).
            Where("org_id = ? AND is_default = ?", cmd.OrgID, true).
            Update("is_default", false)
    }

    if err := s.db.WithContext(ctx).Create(ds).Error; err != nil {
        return nil, err
    }

    return ds, nil
}

// UpdateDataSource 更新数据源
func (s *Service) UpdateDataSource(ctx context.Context, cmd *datasources.UpdateDataSourceCommand) (*datasources.DataSource, error) {
    // 获取现有数据源
    existing, err := s.GetDataSource(ctx, &datasources.GetDataSourceQuery{
        ID:    cmd.ID,
        UID:   cmd.UID,
        OrgID: cmd.OrgID,
    })
    if err != nil {
        return nil, err
    }

    // 乐观锁检查
    if cmd.Version != 0 && cmd.Version != existing.Version {
        return nil, errors.New("optimistic lock error: version mismatch")
    }

    // 检查名称唯一性
    if cmd.Name != existing.Name {
        if err := s.checkUniqueness(ctx, cmd.OrgID, "", cmd.Name, existing.ID); err != nil {
            return nil, err
        }
    }

    // 加密敏感数据
    encryptedData, err := s.encryptSecureData(ctx, cmd.SecureJSONData)
    if err != nil {
        return nil, err
    }

    // 更新字段
    updates := map[string]interface{}{
        "name":             cmd.Name,
        "type":             cmd.Type,
        "access":           cmd.Access,
        "url":              cmd.URL,
        "user":             cmd.User,
        "database":         cmd.Database,
        "basic_auth":       cmd.BasicAuth,
        "basic_auth_user":  cmd.BasicAuthUser,
        "is_default":       cmd.IsDefault,
        "json_data":        cmd.JSONData,
        "secure_json_data": encryptedData,
        "version":          existing.Version + 1,
        "updated":          time.Now(),
    }

    // 如果设置为默认
    if cmd.IsDefault && !existing.IsDefault {
        s.db.WithContext(ctx).Model(&datasources.DataSource{}).
            Where("org_id = ? AND is_default = ? AND id != ?", cmd.OrgID, true, existing.ID).
            Update("is_default", false)
    }

    if err := s.db.WithContext(ctx).Model(existing).Updates(updates).Error; err != nil {
        return nil, err
    }

    return s.GetDataSource(ctx, &datasources.GetDataSourceQuery{ID: existing.ID, OrgID: cmd.OrgID})
}

// DeleteDataSource 删除数据源
func (s *Service) DeleteDataSource(ctx context.Context, cmd *datasources.DeleteDataSourceCommand) error {
    tx := s.db.WithContext(ctx).Where("org_id = ?", cmd.OrgID)

    if cmd.UID != "" {
        tx = tx.Where("uid = ?", cmd.UID)
    } else if cmd.ID != 0 {
        tx = tx.Where("id = ?", cmd.ID)
    } else if cmd.Name != "" {
        tx = tx.Where("name = ?", cmd.Name)
    } else {
        return errors.New("UID, ID, or Name is required")
    }

    result := tx.Delete(&datasources.DataSource{})
    if result.Error != nil {
        return result.Error
    }

    if result.RowsAffected == 0 {
        return datasources.ErrDataSourceNotFound
    }

    return nil
}

// --- 私有方法 ---

func (s *Service) checkUniqueness(ctx context.Context, orgID int64, uid, name string, excludeID int64) error {
    var count int64

    if uid != "" {
        tx := s.db.WithContext(ctx).Model(&datasources.DataSource{}).
            Where("org_id = ? AND uid = ?", orgID, uid)
        if excludeID != 0 {
            tx = tx.Where("id != ?", excludeID)
        }
        tx.Count(&count)
        if count > 0 {
            return datasources.ErrDataSourceUIDExists
        }
    }

    if name != "" {
        tx := s.db.WithContext(ctx).Model(&datasources.DataSource{}).
            Where("org_id = ? AND name = ?", orgID, name)
        if excludeID != 0 {
            tx = tx.Where("id != ?", excludeID)
        }
        tx.Count(&count)
        if count > 0 {
            return datasources.ErrDataSourceNameExists
        }
    }

    return nil
}

func (s *Service) encryptSecureData(ctx context.Context, data map[string]string) (map[string][]byte, error) {
    if len(data) == 0 {
        return nil, nil
    }

    result := make(map[string][]byte, len(data))
    for key, value := range data {
        encrypted, err := s.secretService.Encrypt(ctx, []byte(value))
        if err != nil {
            return nil, err
        }
        result[key] = encrypted
    }
    return result, nil
}

func generateUID() string {
    // 简单实现，生产环境建议使用 UUID
    return fmt.Sprintf("ds-%d", time.Now().UnixNano())
}
```

#### 4.7.5 项目结构

```
pkg/services/datasources/
├── errors.go           # 错误定义
├── models.go           # 数据模型和 DTO
├── service.go          # 服务接口
└── service/
    └── datasource_service.go  # GORM 实现
```

---

### 4.8 数据源缓存 (pkg/services/datasources/service/cache.go)

```go
package service

import (
    "context"
    "fmt"
    "sync"
    "time"

    "your-project/pkg/services/datasources"
)

const (
    defaultCacheTTL = 5 * time.Second
)

// cacheEntry 缓存条目
type cacheEntry struct {
    ds        *datasources.DataSource
    expiresAt time.Time
}

// Cache 数据源缓存实现
type Cache struct {
    dsService *Service  // 底层数据源服务
    orgID     int64     // 组织 ID（可选）
    ttl       time.Duration
    
    mu    sync.RWMutex
    cache map[string]*cacheEntry  // key: uid or "id:{id}"
}

// NewCache 创建数据源缓存
func NewCache(dsService *Service, orgID int64, ttl time.Duration) *Cache {
    if ttl == 0 {
        ttl = defaultCacheTTL
    }
    return &Cache{
        dsService: dsService,
        orgID:     orgID,
        ttl:       ttl,
        cache:     make(map[string]*cacheEntry),
    }
}

// GetByUID 根据 UID 获取数据源
func (c *Cache) GetByUID(ctx context.Context, uid string, skipCache bool) (*datasources.DataSource, error) {
    if !skipCache {
        if ds := c.get(uid); ds != nil {
            return ds, nil
        }
    }
    
    ds, err := c.dsService.GetDataSource(ctx, &datasources.GetDataSourceQuery{
        UID:   uid,
        OrgID: c.orgID,
    })
    if err != nil {
        return nil, err
    }
    
    c.set(uid, ds)
    return ds, nil
}

// GetByID 根据 ID 获取数据源
func (c *Cache) GetByID(ctx context.Context, id int64, skipCache bool) (*datasources.DataSource, error) {
    key := fmt.Sprintf("id:%d", id)
    
    if !skipCache {
        if ds := c.get(key); ds != nil {
            return ds, nil
        }
    }
    
    ds, err := c.dsService.GetDataSource(ctx, &datasources.GetDataSourceQuery{
        ID:    id,
        OrgID: c.orgID,
    })
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

func (c *Cache) get(key string) *datasources.DataSource {
    c.mu.RLock()
    defer c.mu.RUnlock()
    
    entry, exists := c.cache[key]
    if !exists || time.Now().After(entry.expiresAt) {
        return nil
    }
    return entry.ds
}

func (c *Cache) set(key string, ds *datasources.DataSource) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    c.cache[key] = &cacheEntry{
        ds:        ds,
        expiresAt: time.Now().Add(c.ttl),
    }
}
```

---

### 4.9 插件上下文提供者 (pkg/services/pluginsintegration/plugincontext/provider.go)

```go
package plugincontext

import (
    "context"
    
    "github.com/grafana/grafana-plugin-sdk-go/backend"
    
    "your-project/pkg/plugins"
    "your-project/pkg/plugins/manager/registry"
    "your-project/pkg/services/datasources"
)

// Provider 负责构建插件上下文
type Provider struct {
    registry      registry.Service
    dsCache       datasources.CacheService
    secretService SecretService
}

// SecretService 定义解密服务的接口
type SecretService interface {
    Decrypt(ctx context.Context, data []byte) ([]byte, error)
}

// NewProvider 创建插件上下文提供者
func NewProvider(registry registry.Service, dsCache datasources.CacheService, secretService SecretService) *Provider {
    return &Provider{
        registry:      registry,
        dsCache:       dsCache,
        secretService: secretService,
    }
}

// GetWithDataSource 构建插件上下文
func (p *Provider) GetWithDataSource(ctx context.Context, pluginID string, ds *datasources.DataSource) (backend.PluginContext, error) {
    _, exists := p.registry.Plugin(ctx, pluginID)
    if !exists {
        return backend.PluginContext{}, plugins.ErrPluginNotRegistered
    }
    
    decryptedData := p.decryptSecureData(ctx, ds)
    
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

// GetDataSourceInstanceSettings 根据 UID 获取数据源配置
func (p *Provider) GetDataSourceInstanceSettings(ctx context.Context, uid string) (*backend.DataSourceInstanceSettings, error) {
    ds, err := p.dsCache.GetByUID(ctx, uid, false)
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

func (p *Provider) decryptSecureData(ctx context.Context, ds *datasources.DataSource) map[string]string {
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
    
    "your-project/pkg/plugins/manager/client"
    "your-project/pkg/services/datasources"
    "your-project/pkg/services/pluginsintegration/plugincontext"
)

// Service 查询服务
type Service struct {
    pluginClient    *client.Service
    contextProvider *plugincontext.Provider
    dsCache         datasources.CacheService
}

// NewService 创建查询服务
func NewService(
    pluginClient *client.Service,
    contextProvider *plugincontext.Provider,
    dsCache datasources.CacheService,
) *Service {
    return &Service{
        pluginClient:    pluginClient,
        contextProvider: contextProvider,
        dsCache:         dsCache,
    }
}

// QueryData 执行数据查询
func (s *Service) QueryData(ctx context.Context, datasourceUID string, queries []backend.DataQuery) (*backend.QueryDataResponse, error) {
    ds, err := s.dsCache.GetByUID(ctx, datasourceUID, false)
    if err != nil {
        return nil, fmt.Errorf("failed to get datasource: %w", err)
    }
    
    pCtx, err := s.contextProvider.GetWithDataSource(ctx, ds.Type, ds)
    if err != nil {
        return nil, err
    }
    
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
    
    "gorm.io/driver/mysql"
    "gorm.io/gorm"
    
    "your-project/pkg/plugins/manager/registry"
    "your-project/pkg/plugins/manager/client"
    "your-project/pkg/plugins/backendplugin/coreplugin"
    "your-project/pkg/services/pluginsintegration/plugincontext"
    "your-project/pkg/services/datasources"
    dsservice "your-project/pkg/services/datasources/service"
    "your-project/pkg/query"
)

func main() {
    ctx := context.Background()
    
    // 1. 初始化数据库（GORM v2）
    dsn := "user:pass@tcp(127.0.0.1:3306)/dbname?parseTime=true"
    db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
    if err != nil {
        log.Fatal(err)
    }
    
    // 自动迁移
    db.AutoMigrate(&datasources.DataSource{})
    
    // 2. 创建数据源服务
    dsService := dsservice.NewService(db, secretService)
    
    // 3. 创建数据源缓存（TTL 5秒）
    dsCache := dsservice.NewCache(dsService, 1, 5*time.Second)  // orgID=1
    
    // 4. 创建插件元数据存储 (Store)
    // 实际项目中可能从文件系统加载 JSON，这里简化为内存
    pluginStore := pluginstore.NewInMemory() 
    
    // 5. 创建插件后端注册表 (Registry)
    pluginRegistry := registry.NewInMemory()
    
    // 6. 创建插件客户端
    pluginClient := client.ProvideService(pluginRegistry, pluginStore)
    
    // 6. 创建上下文提供者
    contextProvider := plugincontext.NewProvider(pluginRegistry, dsCache, secretService)
    
    // 7. 创建查询服务
    queryService := query.NewService(pluginClient, contextProvider, dsCache)
    
    // 8. 注册插件
    registerPlugins(ctx, pluginRegistry)
    
    log.Println("Service started")
    _ = queryService
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
| **Plugin 接口** | 定义插件必须实现的方法（包含执行逻辑与元数据访问） |
| **Plugin Store** | 插件元数据的单一事实来源。负责插件类型验证、别名（AliasIDs）管理，支持前端与后端插件。 |
| **Plugin Registry** | 存储和管理所有已注册的可用**后端执行实例**。 |
| **corePlugin** | 内置插件的通用实现，封装具体的 Handler |
| **Client** | 查询服务与插件执行引擎之间的桥梁，负责根据元数据路由请求到 Registry。 |
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
