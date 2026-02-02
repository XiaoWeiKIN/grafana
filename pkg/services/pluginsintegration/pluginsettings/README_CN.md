# PluginSettings 包源码分析

`pluginsettings` 包负责管理 Grafana 插件的配置设置，包括插件的启用状态、JSON 配置数据、加密的敏感数据等。这是 App 插件配置管理的核心组件。

---

## 背景知识：什么是 App 插件？

在 Grafana 中，**App 插件**是一种特殊的插件类型，它可以将多个功能（面板、数据源、页面）打包成一个完整的应用。

### 插件类型对比

| 类型 | 说明 | 示例 |
|-----|------|-----|
| **Datasource** | 数据源插件，连接外部数据 | Prometheus、MySQL、InfluxDB |
| **Panel** | 面板插件，可视化数据 | Graph、Table、Stat |
| **App** | 应用插件，打包多功能的完整应用 | Grafana OnCall、Grafana k6 |

### App 插件的特点

1. **可以包含多个子组件**
   - 自定义页面（Pages）
   - 嵌入的数据源
   - 嵌入的面板
   - 后端服务

2. **有独立的配置页面**
   - 在 Grafana 的 Configuration → Plugins 中配置
   - 可以有 JSON 配置和加密的敏感配置（SecureJSONData）

3. **可以启用/禁用**
   - 这就是 `pluginsettings` 包中 `Enabled` 字段的用途

### 代码中的判断

```go
// 判断是否是 App 插件
if plugin.IsApp() {
    appSettings, err := p.appInstanceSettings(ctx, pluginID, orgID)
    // ...
    pCtx.AppInstanceSettings = appSettings  // App 插件特有的设置
}
```

### App vs Datasource 插件上下文

```go
backend.PluginContext {
    // 通用字段
    PluginID      string
    PluginVersion string
    OrgID         int64
    User          *User
    
    // App 插件专用（二选一）
    AppInstanceSettings        *AppInstanceSettings
    
    // Datasource 插件专用（二选一）
    DataSourceInstanceSettings *DataSourceInstanceSettings
}
```

### 常见 App 插件示例

- **Grafana OnCall** - 值班告警管理
- **Grafana SLO** - SLO 管理
- **Grafana Incident** - 事故管理
- **Grafana k6** - 性能测试
- **Grafana Cloud** - 云服务集成

> **总结**：**Datasource 插件**负责"从哪里获取数据"，**Panel 插件**负责"如何展示数据"，而 **App 插件**则是一个可以包含多种功能的"完整应用"。

---

## 一、包结构概览

```
pluginsettings/
├── models.go           # 数据模型定义
├── pluginsettings.go   # Service 接口定义
├── fake.go             # 测试用 Mock 实现
└── service/
    └── service.go      # Service 接口的实际实现
```

---

## 二、核心数据模型 (models.go)

### 2.1 DTO - 插件设置完整数据

```go
type DTO struct {
    ID             int64              // 主键ID
    OrgID          int64              // 组织ID
    PluginID       string             // 插件ID（如 "grafana-piechart-panel"）
    PluginVersion  string             // 插件版本
    JSONData       map[string]any     // JSON 配置数据（明文）
    SecureJSONData map[string][]byte  // 加密的敏感数据
    Enabled        bool               // 是否启用
    Pinned         bool               // 是否固定版本
    Updated        time.Time          // 更新时间
}
```

### 2.2 InfoDTO - 轻量级信息

```go
type InfoDTO struct {
    PluginID      string  // 插件ID
    OrgID         int64   // 组织ID
    Enabled       bool    // 是否启用
    Pinned        bool    // 是否固定
    PluginVersion string  // 版本号
    AutoEnabled   bool    // 是否自动启用
}
```

### 2.3 PluginSetting - 数据库模型

```go
type PluginSetting struct {
    Id             int64              // 主键
    PluginId       string             // 插件ID
    OrgId          int64              // 组织ID
    Enabled        bool               // 启用状态
    Pinned         bool               // 固定版本
    JsonData       map[string]any     // JSON配置
    SecureJsonData map[string][]byte  // 加密敏感数据
    PluginVersion  string             // 版本
    Created        time.Time          // 创建时间
    Updated        time.Time          // 更新时间
}
```

### 2.4 常用参数结构

```go
// 查询参数
type GetArgs struct {
    OrgID int64
}

type GetByPluginIDArgs struct {
    PluginID string
    OrgID    int64
}

// 更新参数
type UpdateArgs struct {
    Enabled                 bool
    Pinned                  bool
    JSONData                map[string]any
    SecureJSONData          map[string]string   // 明文输入
    PluginVersion           string
    PluginID                string
    OrgID                   int64
    EncryptedSecureJSONData map[string][]byte   // 加密后存储
}
```

---

## 三、Service 接口 (pluginsettings.go)

```go
type Service interface {
    // 获取组织下所有插件设置
    GetPluginSettings(ctx context.Context, args *GetArgs) ([]*InfoDTO, error)
    
    // 根据插件ID获取设置
    GetPluginSettingByPluginID(ctx context.Context, args *GetByPluginIDArgs) (*DTO, error)
    
    // 更新插件设置
    UpdatePluginSetting(ctx context.Context, args *UpdateArgs) error
    
    // 仅更新插件版本
    UpdatePluginSettingPluginVersion(ctx context.Context, args *UpdatePluginVersionArgs) error
    
    // 解密敏感数据
    DecryptedValues(ps *DTO) map[string]string
}
```

---

## 四、Service 实现 (service/service.go)

### 4.1 结构定义

```go
type Service struct {
    db              db.DB                      // 数据库连接
    decryptionCache secureJSONDecryptionCache  // 解密缓存
    secretsService  secrets.Service            // 加密服务
    logger          log.Logger
}

// 解密缓存结构
type secureJSONDecryptionCache struct {
    cache map[int64]cachedDecryptedJSON  // ID -> 解密结果
    sync.Mutex
}

type cachedDecryptedJSON struct {
    updated time.Time         // 数据更新时间（用于失效判断）
    json    map[string]string // 解密后的数据
}
```

### 4.2 核心方法实现

#### GetPluginSettingByPluginID - 获取单个插件设置

```go
func (s *Service) GetPluginSettingByPluginID(ctx context.Context, args *GetByPluginIDArgs) (*DTO, error) {
    query := &GetPluginSettingByIdQuery{
        OrgId:    args.OrgID,
        PluginId: args.PluginID,
    }

    err := s.getPluginSettingById(ctx, query)
    if err != nil {
        return nil, err
    }

    return &DTO{
        ID:             query.Result.Id,
        OrgID:          query.Result.OrgId,
        PluginID:       query.Result.PluginId,
        PluginVersion:  query.Result.PluginVersion,
        JSONData:       query.Result.JsonData,
        SecureJSONData: query.Result.SecureJsonData,
        Enabled:        query.Result.Enabled,
        Pinned:         query.Result.Pinned,
        Updated:        query.Result.Updated,
    }, nil
}
```

#### UpdatePluginSetting - 更新插件设置

```go
func (s *Service) UpdatePluginSetting(ctx context.Context, args *UpdateArgs) error {
    // 1. 加密敏感数据
    encryptedSecureJsonData, err := s.secretsService.EncryptJsonData(ctx, args.SecureJSONData, secrets.WithoutScope())
    if err != nil {
        return err
    }

    // 2. 执行更新
    return s.updatePluginSetting(ctx, &UpdatePluginSettingCmd{
        Enabled:                 args.Enabled,
        Pinned:                  args.Pinned,
        JsonData:                args.JSONData,
        SecureJsonData:          args.SecureJSONData,
        PluginVersion:           args.PluginVersion,
        PluginId:                args.PluginID,
        OrgId:                   args.OrgID,
        EncryptedSecureJsonData: encryptedSecureJsonData,
    })
}
```

#### DecryptedValues - 解密敏感数据（带缓存）

```go
func (s *Service) DecryptedValues(ps *DTO) map[string]string {
    s.decryptionCache.Lock()
    defer s.decryptionCache.Unlock()

    // 1. 检查缓存（根据 Updated 时间判断有效性）
    if item, present := s.decryptionCache.cache[ps.ID]; present && ps.Updated.Equal(item.updated) {
        return item.json
    }

    // 2. 缓存未命中，执行解密
    json, err := s.secretsService.DecryptJsonData(context.Background(), ps.SecureJSONData)
    if err != nil {
        s.logger.Error("Failed to decrypt secure json data", "error", err)
        return map[string]string{}
    }

    // 3. 更新缓存
    s.decryptionCache.cache[ps.ID] = cachedDecryptedJSON{
        updated: ps.Updated,
        json:    json,
    }

    return json
}
```

---

## 五、数据库操作

### 5.1 查询插件设置

```go
func (s *Service) getPluginSettingById(ctx context.Context, query *GetPluginSettingByIdQuery) error {
    return s.db.WithDbSession(ctx, func(sess *db.Session) error {
        pluginSetting := PluginSetting{OrgId: query.OrgId, PluginId: query.PluginId}
        has, err := sess.Get(&pluginSetting)
        if err != nil {
            return err
        } else if !has {
            return ErrPluginSettingNotFound
        }
        query.Result = &pluginSetting
        return nil
    })
}
```

### 5.2 更新/插入（Upsert）

```go
func (s *Service) updatePluginSetting(ctx context.Context, cmd *UpdatePluginSettingCmd) error {
    return s.db.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
        var pluginSetting PluginSetting

        // 查询是否存在
        exists, err := sess.Where("org_id=? and plugin_id=?", cmd.OrgId, cmd.PluginId).Get(&pluginSetting)
        if err != nil {
            return err
        }

        if !exists {
            // 不存在则插入
            pluginSetting = PluginSetting{
                PluginId:       cmd.PluginId,
                OrgId:          cmd.OrgId,
                Enabled:        cmd.Enabled,
                Pinned:         cmd.Pinned,
                JsonData:       cmd.JsonData,
                PluginVersion:  cmd.PluginVersion,
                SecureJsonData: cmd.EncryptedSecureJsonData,
                Created:        time.Now(),
                Updated:        time.Now(),
            }

            // 发布状态变更事件
            sess.PublishAfterCommit(&PluginStateChangedEvent{
                PluginId: cmd.PluginId,
                OrgId:    cmd.OrgId,
                Enabled:  cmd.Enabled,
            })

            _, err = sess.Insert(&pluginSetting)
            return err
        }

        // 存在则更新
        // 合并加密数据
        for key, encryptedData := range cmd.EncryptedSecureJsonData {
            pluginSetting.SecureJsonData[key] = encryptedData
        }

        // 状态变化时发布事件
        if pluginSetting.Enabled != cmd.Enabled {
            sess.PublishAfterCommit(&PluginStateChangedEvent{
                PluginId: cmd.PluginId,
                OrgId:    cmd.OrgId,
                Enabled:  cmd.Enabled,
            })
        }

        pluginSetting.Updated = time.Now()
        pluginSetting.Enabled = cmd.Enabled
        pluginSetting.JsonData = cmd.JsonData
        pluginSetting.Pinned = cmd.Pinned
        pluginSetting.PluginVersion = cmd.PluginVersion

        _, err = sess.ID(pluginSetting.Id).Update(&pluginSetting)
        return err
    })
}
```

---

## 六、与 PluginContext 的关联

在 `plugincontext` 包中，`pluginsettings.Service` 用于获取 App 插件的配置：

```go
// plugincontext/plugincontext.go:127-153
func (p *Provider) appInstanceSettings(ctx context.Context, pluginID string, orgID int64) (*backend.AppInstanceSettings, error) {
    jsonData := json.RawMessage{}
    decryptedSecureJSONData := map[string]string{}
    var updated time.Time

    // 获取插件设置
    ps, err := p.getCachedPluginSettings(ctx, pluginID, orgID)
    if err != nil {
        if !errors.Is(err, pluginsettings.ErrPluginSettingNotFound) {
            return nil, fmt.Errorf("Failed to get plugin settings: %w", err)
        }
    } else {
        jsonData, _ = json.Marshal(ps.JSONData)
        // 解密敏感数据
        decryptedSecureJSONData = p.pluginSettingsService.DecryptedValues(ps)
        updated = ps.Updated
    }

    return &backend.AppInstanceSettings{
        JSONData:                jsonData,
        DecryptedSecureJSONData: decryptedSecureJSONData,
        Updated:                 updated,
    }, nil
}
```

---

## 七、安全机制

### 7.1 敏感数据加密流程

```mermaid
graph LR
    A[用户输入明文] --> B[SecureJSONData]
    B --> C[secretsService.EncryptJsonData]
    C --> D[EncryptedSecureJSONData]
    D --> E[存储到数据库]
    
    E --> F[从数据库读取]
    F --> G[secretsService.DecryptJsonData]
    G --> H[DecryptedSecureJSONData]
    H --> I[传递给插件]
```

### 7.2 缓存失效策略

- 解密缓存基于 `Updated` 时间戳判断有效性
- 当插件设置更新时，缓存自动失效（因为时间戳变化）

---

## 八、数据库表结构

```sql
-- plugin_setting 表结构（推断）
CREATE TABLE plugin_setting (
    id              BIGINT PRIMARY KEY,
    plugin_id       VARCHAR(255) NOT NULL,
    org_id          BIGINT NOT NULL,
    enabled         BOOLEAN DEFAULT FALSE,
    pinned          BOOLEAN DEFAULT FALSE,
    json_data       JSON,
    secure_json_data BLOB,  -- 加密存储
    plugin_version  VARCHAR(50),
    created         DATETIME,
    updated         DATETIME,
    
    UNIQUE KEY (org_id, plugin_id)
);
```

---

## 九、事件机制

当插件启用状态变化时，会发布 `PluginStateChangedEvent`：

```go
type PluginStateChangedEvent struct {
    PluginId string
    OrgId    int64
    Enabled  bool
}
```

这允许其他服务订阅插件状态变化并做出响应。

---

## 十、相关源码文件

| 文件 | 说明 |
|-----|------|
| [models.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/pluginsettings/models.go) | 数据模型定义 |
| [pluginsettings.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/pluginsettings/pluginsettings.go) | Service 接口 |
| [service.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/pluginsettings/service/service.go) | Service 实现 |
| [fake.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/pluginsettings/fake.go) | 测试用 Mock |
| [plugincontext.go](file:///Users/wangxiaowei1/xiaowei/grafana/pkg/services/pluginsintegration/plugincontext/plugincontext.go) | 调用方 |
