# Grafana Plugin Loader Load 方法调用流程分析

本文档详细分析 Grafana 中 `Load` 方法的调用位置以及 Backend Core Plugin 的加载触发机制。

## 目录

- [Load 方法调用链分析](#load-方法调用链分析)
- [Backend Core Plugin 加载流程](#backend-core-plugin-加载流程)
- [关键代码位置](#关键代码位置)
- [流程图](#流程图)
- [总结](#总结)

---

## Load 方法调用链分析

### 1. 主要调用位置概览

`pkg/plugins/manager/loader/loader.go:61` 中的 `Load` 方法主要在以下场景被调用：

#### 1.1 应用启动时的自动加载（主要入口）

**调用链**:
```
应用启动
  └─> pluginstore.ProvideService()
       ├─> 【立即加载模式】pkg/services/pluginsintegration/pluginstore/store.go:56-63
       │    └─> for _, ps := range pluginSources.List(ctx)
       │         └─> pluginLoader.Load(ctx, ps)
       │
       └─> 【延迟加载模式】pkg/services/pluginsintegration/pluginstore/store.go:100-119
            └─> Service.starting(ctx)
                 └─> for _, ps := range s.pluginSources.List(ctx)
                      └─> s.pluginLoader.Load(ctx, ps)
```

**代码位置**: `pkg/services/pluginsintegration/pluginstore/store.go`

```go
// 立即加载模式（默认）
func ProvideService(...) (*Service, error) {
    // line 56-63
    for _, ps := range pluginSources.List(ctx) {
        loadedPlugins, err := pluginLoader.Load(ctx, ps)  // ← 触发加载
        if err != nil {
            logger.Error("Loading plugin source failed", "source", ps.PluginClass(ctx), "error", err)
            return nil, err
        }
        totalPlugins += len(loadedPlugins)
    }
}

// 延迟加载模式（通过 feature toggle 启用）
func (s *Service) starting(ctx context.Context) error {
    // line 109-116
    for _, ps := range s.pluginSources.List(ctx) {
        loadedPlugins, err := s.pluginLoader.Load(ctx, ps)  // ← 触发加载
        if err != nil {
            logger.Error("Loading plugin source failed", "source", ps.PluginClass(ctx), "error", err)
            return err
        }
        totalPlugins += len(loadedPlugins)
    }
}
```

#### 1.2 插件动态安装时

**调用链**:
```
用户/API 触发安装
  └─> pluginManager.Install()
       └─> pkg/plugins/manager/installer.go:85
            └─> m.pluginLoader.Load(ctx, sources.NewLocalSource(...))
```

**代码位置**: `pkg/plugins/manager/installer.go:85`

```go
func (m *Manager) Install(ctx context.Context, pluginID, version string, opts CompatOpts) error {
    // ...
    // line 85
    _, err = m.pluginLoader.Load(ctx, sources.NewLocalSource(plugins.ClassExternal, []string{archive.Path}))
    return err
}
```

#### 1.3 渲染器插件加载

**调用链**:
```
渲染器管理器启动
  └─> renderer.Manager.start()
       └─> pkg/services/pluginsintegration/renderer/renderer.go:95
            └─> m.loader.Load(ctx, src)
```

**代码位置**: `pkg/services/pluginsintegration/renderer/renderer.go:95`

```go
func (m *Manager) start(ctx context.Context) error {
    // line 95
    ps, err := m.loader.Load(ctx, src)
    if err != nil {
        return err
    }
    // ...
}
```

#### 1.4 Core Plugin 元数据加载

**调用链**:
```
CoreProvider.GetMeta()
  └─> CoreProvider.loadPlugins()
       └─> apps/plugins/pkg/app/meta/core.go:113
            └─> p.loader.Load(ctx, src)
```

**代码位置**: `apps/plugins/pkg/app/meta/core.go:113`

```go
func (p *CoreProvider) loadPlugins(ctx context.Context) error {
    // ...
    src := sources.NewLocalSource(plugins.ClassCore, []string{pluginsPath})
    // line 113
    loadedPlugins, err := p.loader.Load(ctx, src)
    if err != nil {
        return err
    }
    // ...
}
```

### 2. Load 方法的内部实现

**文件**: `pkg/plugins/manager/loader/loader.go:61-118`

```go
func (l *Loader) Load(ctx context.Context, src plugins.PluginSource) ([]*plugins.Plugin, error) {
    end := l.instrumentLoad(ctx, src)

    // ===== 阶段 1: Discovery =====
    st := time.Now()
    discoveredPlugins, err := l.discovery.Discover(ctx, src)
    if err != nil {
        return nil, err
    }
    l.log.Debug("Discovered", "class", src.PluginClass(ctx), "duration", time.Since(st))

    // ===== 阶段 2: Bootstrap =====
    st = time.Now()
    bootstrappedPlugins := []*plugins.Plugin{}
    for _, foundBundle := range discoveredPlugins {
        bootstrappedPlugin, err := l.bootstrap.Bootstrap(ctx, src, foundBundle)
        if err != nil {
            l.errorTracker.Record(ctx, &plugins.Error{
                PluginID:  foundBundle.Primary.JSONData.ID,
                ErrorCode: plugins.ErrorCode(err.Error()),
            })
            continue
        }
        bootstrappedPlugins = append(bootstrappedPlugins, bootstrappedPlugin...)
    }
    l.log.Debug("Bootstrapped", "class", src.PluginClass(ctx), "duration", time.Since(st))

    // ===== 阶段 3: Validation =====
    st = time.Now()
    validatedPlugins := []*plugins.Plugin{}
    for _, bootstrappedPlugin := range bootstrappedPlugins {
        err := l.validation.Validate(ctx, bootstrappedPlugin)
        if err != nil {
            l.recordError(ctx, bootstrappedPlugin, err)
            continue
        }
        validatedPlugins = append(validatedPlugins, bootstrappedPlugin)
    }
    l.log.Debug("Validated", "class", src.PluginClass(ctx), "duration", time.Since(st), "total", len(validatedPlugins))

    // ===== 阶段 4: Initialization =====
    st = time.Now()
    initializedPlugins := []*plugins.Plugin{}
    for _, validatedPlugin := range validatedPlugins {
        initializedPlugin, err := l.initializer.Initialize(ctx, validatedPlugin)
        if err != nil {
            l.recordError(ctx, validatedPlugin, err)
            continue
        }
        initializedPlugins = append(initializedPlugins, initializedPlugin)
    }
    l.log.Debug("Initialized", "class", src.PluginClass(ctx), "duration", time.Since(st))

    // Clean errors from registry for initialized plugins
    for _, p := range initializedPlugins {
        l.errorTracker.Clear(ctx, p.ID)
    }

    end(initializedPlugins)

    return initializedPlugins, nil
}
```

---

## Backend Core Plugin 加载流程

Backend Core Plugin（如 Prometheus、MySQL 等）的加载是一个多阶段的复杂流程，涉及插件发现、元数据解析、后端客户端初始化等多个步骤。

### 流程概览

```
应用启动
  ↓
配置插件源 (Plugin Sources)
  ↓
遍历所有插件源并调用 Load
  ↓
【Discovery 阶段】发现 plugin.json 文件
  ↓
【Bootstrap 阶段】解析 plugin.json，创建 Plugin 对象
  ↓
【Validation 阶段】验证签名和 Angular
  ↓
【Initialization 阶段】初始化 Backend Client
  ↓
检查 Backend 字段 → 如果为 true:
  ↓
查找 BackendFactoryProvider (Core Registry)
  ↓
创建 backend client 并注册
  ↓
启动 backend 进程（如果需要）
  ↓
注册到 Plugin Registry
```

### 详细流程分解

#### 阶段 0: 插件源配置

**文件**: `pkg/services/pluginsintegration/pluginsources/pluginsources.go:29-38`

```go
func (s *Service) List(_ context.Context) []plugins.PluginSource {
    r := []plugins.PluginSource{
        // Core plugins 来自固定路径
        sources.NewLocalSource(
            plugins.ClassCore,       // ← 标记为 Core Plugin
            s.corePluginPaths(),     // ← 返回 core plugin 路径
        ),
    }
    r = append(r, s.externalPluginSources()...)    // 外部插件
    r = append(r, s.pluginSettingSources()...)     // 配置中的插件
    return r
}

// Core plugin 路径配置
func (s *Service) corePluginPaths() []string {
    datasourcePaths := filepath.Join(s.staticRootPath, "app", "plugins", "datasource")
    panelsPath := filepath.Join(s.staticRootPath, "app", "plugins", "panel")
    return []string{datasourcePaths, panelsPath}
}
```

**Core Plugin 路径**:
- `{StaticRootPath}/app/plugins/datasource/` - 数据源插件
- `{StaticRootPath}/app/plugins/panel/` - 面板插件

#### 阶段 1: Discovery（发现插件）

**文件**: `pkg/plugins/manager/pipeline/discovery/discovery.go:55-83`

```go
func (d *Discovery) Discover(ctx context.Context, src plugins.PluginSource) ([]*plugins.FoundBundle, error) {
    pluginClass := src.PluginClass(ctx)  // 获取插件类别（Core/External）

    // 使用 source 的 Discover 方法（对于本地文件，会遍历目录查找 plugin.json）
    found, err := src.Discover(ctx)
    if err != nil {
        ctxLogger.Warn("Discovery source failed", "class", pluginClass, "error", err)
        return nil, err
    }

    ctxLogger.Debug("Found plugins", "class", pluginClass, "count", len(found))

    // 应用过滤器（如类型过滤、去重、禁用插件等）
    result := found
    for _, filter := range d.filterSteps {
        result, err = filter(ctx, src.PluginClass(ctx), result)
        if err != nil {
            return nil, err
        }
    }

    return result, nil
}
```

**发现结果**: 包含插件的 `plugin.json` 内容和文件系统路径

#### 阶段 2: Bootstrap（构建插件对象）

**文件**: `pkg/plugins/manager/pipeline/bootstrap/bootstrap.go:70-99`

```go
func (b *Bootstrap) Bootstrap(ctx context.Context, src plugins.PluginSource, found *plugins.FoundBundle) ([]*plugins.Plugin, error) {
    // 步骤 1: Construct - 创建 Plugin 结构体
    ps, err := b.constructStep(ctx, src, found)
    if err != nil {
        return nil, err
    }

    // 步骤 2: Decorate - 装饰插件（设置 URL、默认值等）
    bootstrappedPlugins := make([]*plugins.Plugin, 0, len(ps))
    for _, p := range ps {
        var ip *plugins.Plugin
        for _, decorate := range b.decorateSteps {
            ip, err = decorate(ctx, p)
            if err != nil {
                return nil, err
            }
        }
        bootstrappedPlugins = append(bootstrappedPlugins, ip)
    }

    return bootstrappedPlugins, nil
}
```

**Construct 步骤** (`pkg/plugins/manager/pipeline/bootstrap/factory.go:56-86`):

```go
func (f *DefaultPluginFactory) newPlugin(p plugins.FoundPlugin, class plugins.Class, sig plugins.Signature,
    info pluginassets.PluginInfo) (*plugins.Plugin, error) {
    // ...
    plugin := &plugins.Plugin{
        JSONData:      p.JSONData,  // ← 包含 Backend: true 字段
        Class:         class,        // ← Core/External
        FS:            p.FS,
        BaseURL:       baseURL,
        Module:        moduleURL,
        Signature:     sig.Status,
        SignatureType: sig.Type,
        SignatureOrg:  sig.SigningOrg,
    }

    plugin.SetLogger(log.New(fmt.Sprintf("plugin.%s", plugin.ID)))
    // ...
    return plugin, nil
}
```

**关键点**: `plugin.JSONData.Backend` 字段来自 `plugin.json` 文件

#### 阶段 3: Validation（验证插件）

**文件**: `pkg/services/pluginsintegration/pipeline/pipeline.go:60-68`

```go
func ProvideValidationStage(cfg *config.PluginManagementCfg, sv signature.Validator, ai angularinspector.Inspector) *validation.Validate {
    return validation.New(cfg, validation.Opts{
        ValidateFuncs: []validation.ValidateFunc{
            SignatureValidationStep(sv),              // 签名验证
            validation.ModuleJSValidationStep(),      // Module 验证
            validation.AngularDetectionStep(cfg, ai), // Angular 检测
        },
    })
}
```

**对于 Core Plugins**: 签名验证通常会跳过或使用更宽松的策略

#### 阶段 4: Initialization（初始化插件）

**核心步骤**: `pkg/services/pluginsintegration/pipeline/pipeline.go:70-89`

```go
func ProvideInitializationStage(...) *initialization.Initialize {
    return initialization.New(cfg, initialization.Opts{
        InitializeFuncs: []initialization.InitializeFunc{
            ExternalServiceRegistrationStep(...),            // 外部服务注册
            initialization.BackendClientInitStep(...),       // ← 关键：Backend Client 初始化
            initialization.BackendProcessStartStep(pm),      // ← 关键：启动 Backend 进程
            RegisterPluginRolesStep(roleRegistry),           // 角色注册
            RegisterActionSetsStep(actionSetRegistry),       // 权限集注册
            // ... 其他初始化步骤
            initialization.PluginRegistrationStep(pr),       // 最终注册到 registry
        },
    })
}
```

##### 4.1 Backend Client 初始化

**文件**: `pkg/plugins/manager/pipeline/initialization/steps.go:45-62`

```go
func (b *BackendClientInit) Initialize(ctx context.Context, p *plugins.Plugin) (*plugins.Plugin, error) {
    // 检查是否为 backend plugin
    if p.Backend {  // ← 来自 plugin.json 的 "backend": true
        // 获取 backend factory
        backendFactory := b.backendProvider.BackendFactory(ctx, p)
        if backendFactory == nil {
            return nil, errors.New("could not find backend factory for plugin")
        }

        // 创建环境变量函数
        envFunc := func() []string { return b.envVarProvider.PluginEnvVars(ctx, p) }

        // 创建 backend client
        if backendClient, err := backendFactory(p.ID, p.Logger(), b.tracer, envFunc); err != nil {
            return nil, err
        } else {
            p.RegisterClient(backendClient)  // ← 注册 client 到 plugin
        }
    }
    return p, nil
}
```

##### 4.2 Backend Factory Provider 查找

**文件**: `pkg/services/pluginsintegration/coreplugin/coreplugins.go:135-142`

```go
// Core Plugin Registry 的 BackendFactoryProvider
func (cr *Registry) BackendFactoryProvider() func(_ context.Context, p *plugins.Plugin) backendplugin.PluginFactoryFunc {
    return func(_ context.Context, p *plugins.Plugin) backendplugin.PluginFactoryFunc {
        // 只处理 Core Plugin
        if !p.IsCorePlugin() {
            return nil
        }

        // 从注册表获取对应的工厂函数
        return cr.Get(p.ID)  // ← 查找 prometheus、mysql 等的 factory
    }
}
```

##### 4.3 Core Plugin Registry 内容

**文件**: `pkg/services/pluginsintegration/coreplugin/coreplugins.go:101-129`

```go
func ProvideCoreRegistry(tracer trace.Tracer, am *azuremonitor.Service, cw *cloudwatch.Service,
    cm *cloudmonitoring.Service, es *elasticsearch.Service, grap *graphite.Service,
    idb *influxdb.Service, lk *loki.Service, otsdb *opentsdb.Service, pr *prometheus.Service,
    t *tempo.Service, td *testdatasource.Service, pg *postgres.Service, my *mysql.Service,
    ms *mssql.Service, graf *grafanads.Service, pyroscope *pyroscope.Service,
    parca *parca.Service, zipkin *zipkin.Service, jaeger *jaeger.Service) *Registry {

    return NewRegistry(map[string]backendplugin.PluginFactoryFunc{
        CloudWatch:      asBackendPlugin(cw),        // cloudwatch
        CloudMonitoring: asBackendPlugin(cm),        // stackdriver
        AzureMonitor:    asBackendPlugin(am),        // grafana-azure-monitor-datasource
        Elasticsearch:   asBackendPlugin(es),        // elasticsearch
        Graphite:        asBackendPlugin(grap),      // graphite
        InfluxDB:        asBackendPlugin(idb),       // influxdb
        Loki:            asBackendPlugin(lk),        // loki
        OpenTSDB:        asBackendPlugin(otsdb),     // opentsdb
        Prometheus:      asBackendPlugin(pr),        // prometheus ← 示例
        Tempo:           asBackendPlugin(t),         // tempo
        TestData:        asBackendPlugin(td),        // grafana-testdata-datasource
        PostgreSQL:      asBackendPlugin(pg),        // grafana-postgresql-datasource
        MySQL:           asBackendPlugin(my),        // mysql
        MSSQL:           asBackendPlugin(ms),        // mssql
        Grafana:         asBackendPlugin(graf),      // grafana
        Pyroscope:       asBackendPlugin(pyroscope), // grafana-pyroscope-datasource
        Parca:           asBackendPlugin(parca),     // parca
        Zipkin:          asBackendPlugin(zipkin),    // zipkin
        Jaeger:          asBackendPlugin(jaeger),    // jaeger
    })
}
```

##### 4.4 asBackendPlugin 转换

**文件**: `pkg/services/pluginsintegration/coreplugin/coreplugins.go:145-169`

```go
func asBackendPlugin(svc any) backendplugin.PluginFactoryFunc {
    opts := backend.ServeOpts{}

    // 检查服务实现了哪些接口
    if queryHandler, ok := svc.(backend.QueryDataHandler); ok {
        opts.QueryDataHandler = queryHandler
    }
    if resourceHandler, ok := svc.(backend.CallResourceHandler); ok {
        opts.CallResourceHandler = resourceHandler
    }
    if streamHandler, ok := svc.(backend.StreamHandler); ok {
        opts.StreamHandler = streamHandler
    }
    if healthHandler, ok := svc.(backend.CheckHealthHandler); ok {
        opts.CheckHealthHandler = healthHandler
    }
    if storageHandler, ok := svc.(backend.AdmissionHandler); ok {
        opts.AdmissionHandler = storageHandler
    }

    // 至少实现一个接口才创建插件
    if opts.QueryDataHandler != nil || opts.CallResourceHandler != nil ||
        opts.CheckHealthHandler != nil || opts.StreamHandler != nil {
        return coreplugin.New(opts)  // ← 创建 backend plugin factory
    }

    return nil
}
```

##### 4.5 具体数据源服务的创建

以 Prometheus 为例：

**文件**: `pkg/tsdb/prometheus/prometheus.go`

```go
// ProvideService 通过 Wire 依赖注入创建
func ProvideService(httpClientProvider *httpclient.Provider) *Service {
    return &Service{
        httpClientProvider: httpClientProvider,
    }
}

// 实现 backend.QueryDataHandler 接口
func (s *Service) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    // Prometheus 查询实现
    // ...
}

// 实现 backend.CheckHealthHandler 接口
func (s *Service) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
    // 健康检查实现
    // ...
}
```

##### 4.6 Backend 进程启动

**文件**: `pkg/plugins/manager/pipeline/initialization/steps.go:83-92`

```go
func (b *BackendClientStarter) Start(ctx context.Context, p *plugins.Plugin) (*plugins.Plugin, error) {
    // 启动 backend 进程（对于 external plugins）
    if err := b.processManager.Start(ctx, p); err != nil {
        b.log.Error("Could not start plugin backend", "pluginId", p.ID, "error", err)
        return nil, (&plugins.Error{
            PluginID:  p.ID,
            ErrorCode: plugins.ErrorCodeFailedBackendStart,
        }).WithMessage(err.Error())
    }
    return p, nil
}
```

**注意**: Core plugins 是 in-process 的，不需要启动独立进程

##### 4.7 最终注册

**文件**: `pkg/plugins/manager/pipeline/initialization/steps.go:113-123`

```go
func (r *PluginRegistration) Initialize(ctx context.Context, p *plugins.Plugin) (*plugins.Plugin, error) {
    // 注册到 plugin registry
    if err := r.pluginRegistry.Add(ctx, p); err != nil {
        r.log.Error("Could not register plugin", "pluginId", p.ID, "error", err)
        return nil, err
    }

    if !p.IsCorePlugin() {
        r.log.Info("Plugin registered", "pluginId", p.ID)
    }

    return p, nil
}
```

---

## 关键代码位置

### Load 方法相关

| 位置 | 文件路径 | 说明 |
|------|---------|------|
| **Load 方法定义** | `pkg/plugins/manager/loader/loader.go:61` | Load 方法的主要实现 |
| **启动加载（立即）** | `pkg/services/pluginsintegration/pluginstore/store.go:56-63` | 应用启动时立即加载 |
| **启动加载（延迟）** | `pkg/services/pluginsintegration/pluginstore/store.go:109-116` | 服务启动阶段加载 |
| **动态安装** | `pkg/plugins/manager/installer.go:85` | 插件安装时触发 |
| **渲染器加载** | `pkg/services/pluginsintegration/renderer/renderer.go:95` | 渲染器插件加载 |
| **元数据加载** | `apps/plugins/pkg/app/meta/core.go:113` | Core plugin 元数据加载 |

### Backend Core Plugin 相关

| 位置 | 文件路径 | 说明 |
|------|---------|------|
| **插件源配置** | `pkg/services/pluginsintegration/pluginsources/pluginsources.go:29-78` | 配置 core plugin 路径 |
| **Discovery 阶段** | `pkg/plugins/manager/pipeline/discovery/discovery.go:55-83` | 发现 plugin.json |
| **Bootstrap 阶段** | `pkg/plugins/manager/pipeline/bootstrap/bootstrap.go:70-99` | 解析并创建 Plugin 对象 |
| **Plugin Factory** | `pkg/plugins/manager/pipeline/bootstrap/factory.go:56-86` | 创建 Plugin 结构体 |
| **Validation 阶段** | `pkg/services/pluginsintegration/pipeline/pipeline.go:60-68` | 验证配置 |
| **Initialization 配置** | `pkg/services/pluginsintegration/pipeline/pipeline.go:70-89` | 初始化步骤配置 |
| **Backend Client Init** | `pkg/plugins/manager/pipeline/initialization/steps.go:45-62` | 初始化 backend client |
| **Backend Process Start** | `pkg/plugins/manager/pipeline/initialization/steps.go:83-92` | 启动 backend 进程 |
| **Core Registry** | `pkg/services/pluginsintegration/coreplugin/coreplugins.go:101-129` | Core plugin 注册表 |
| **Backend Factory Provider** | `pkg/services/pluginsintegration/coreplugin/coreplugins.go:135-142` | 查找 factory 函数 |
| **asBackendPlugin** | `pkg/services/pluginsintegration/coreplugin/coreplugins.go:145-169` | 转换为 backend plugin |
| **Plugin Registration** | `pkg/plugins/manager/pipeline/initialization/steps.go:113-123` | 最终注册到 registry |

### Prometheus 示例

| 位置 | 文件路径 | 说明 |
|------|---------|------|
| **plugin.json** | `public/app/plugins/datasource/prometheus/plugin.json` | 插件元数据配置 |
| **Service 实现** | `pkg/tsdb/prometheus/prometheus.go` | Go 服务实现 |
| **ProvideService** | `pkg/tsdb/prometheus/prometheus.go` | Wire 依赖注入工厂 |

---

## 流程图

### 整体流程图

```
┌─────────────────────────────────────────────────────────────────┐
│                         应用启动                                 │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│           pluginstore.ProvideService / starting()               │
│  - 遍历所有 Plugin Sources (Core, External, Settings)           │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│             pluginLoader.Load(ctx, pluginSource)                │
│  入口: pkg/plugins/manager/loader/loader.go:61                  │
└────────────────────────┬────────────────────────────────────────┘
                         │
        ┌────────────────┴────────────────┐
        │                                 │
        ▼                                 ▼
┌──────────────────┐            ┌──────────────────┐
│  Core Plugins    │            │ External Plugins │
│  ClassCore       │            │  ClassExternal   │
└────────┬─────────┘            └────────┬─────────┘
         │                               │
         └───────────────┬───────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                    阶段 1: Discovery                             │
│  - 遍历目录查找 plugin.json                                      │
│  - 应用过滤器（类型、去重、禁用等）                               │
│  文件: pkg/plugins/manager/pipeline/discovery/discovery.go      │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                    阶段 2: Bootstrap                             │
│  步骤 2.1: Construct                                             │
│    - 读取 plugin.json                                            │
│    - 创建 Plugin 对象 (包含 Backend 字段)                         │
│  步骤 2.2: Decorate                                              │
│    - 设置 BaseURL, Module                                        │
│    - 配置 LoadingStrategy                                        │
│  文件: pkg/plugins/manager/pipeline/bootstrap/                  │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                    阶段 3: Validation                            │
│  - 签名验证                                                       │
│  - ModuleJS 验证                                                 │
│  - Angular 检测                                                  │
│  文件: pkg/plugins/manager/pipeline/validation/                 │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                   阶段 4: Initialization                         │
│  文件: pkg/plugins/manager/pipeline/initialization/             │
└────────────────────────┬────────────────────────────────────────┘
                         │
          ┌──────────────┴──────────────┐
          │                             │
          ▼                             ▼
┌───────────────────────┐     ┌───────────────────────┐
│ 步骤 4.1:             │     │ 步骤 4.2:             │
│ ExternalService       │     │ BackendClientInit     │
│ RegistrationStep      │     │ (关键步骤)             │
└───────────────────────┘     └──────────┬────────────┘
                                         │
                      ┌──────────────────┴───────────────┐
                      │  检查 p.Backend == true?         │
                      └──────────────────┬───────────────┘
                                         │ Yes
                                         ▼
                      ┌──────────────────────────────────┐
                      │ backendProvider.BackendFactory() │
                      │                                  │
                      │ 对于 Core Plugin:                │
                      │  → CoreRegistry.Get(pluginID)    │
                      │                                  │
                      │ 对于 External Plugin:            │
                      │  → 启动独立进程                   │
                      └──────────────────┬───────────────┘
                                         │
                                         ▼
                      ┌──────────────────────────────────┐
                      │   创建 backend client             │
                      │   backendClient, err :=          │
                      │     factory(pluginID, logger)    │
                      └──────────────────┬───────────────┘
                                         │
                                         ▼
                      ┌──────────────────────────────────┐
                      │   p.RegisterClient(backendClient)│
                      └──────────────────┬───────────────┘
                                         │
                                         ▼
                      ┌──────────────────────────────────┐
                      │ 步骤 4.3:                         │
                      │ BackendProcessStartStep          │
                      │ (External plugins 需要)           │
                      └──────────────────┬───────────────┘
                                         │
                                         ▼
                      ┌──────────────────────────────────┐
                      │ 步骤 4.4:                         │
                      │ RegisterPluginRolesStep          │
                      │ RegisterActionSetsStep           │
                      └──────────────────┬───────────────┘
                                         │
                                         ▼
                      ┌──────────────────────────────────┐
                      │ 步骤 4.5:                         │
                      │ PluginRegistrationStep           │
                      │ → 注册到 Plugin Registry          │
                      └──────────────────┬───────────────┘
                                         │
                                         ▼
                      ┌──────────────────────────────────┐
                      │         加载完成                  │
                      │  返回 []*plugins.Plugin           │
                      └──────────────────────────────────┘
```

### Backend Core Plugin 特定流程

```
┌─────────────────────────────────────────────────────────────────┐
│               Backend Core Plugin 加载流程                        │
└─────────────────────────────────────────────────────────────────┘

1. 插件源配置
   ↓
   pluginsources.Service.List()
   └─> sources.NewLocalSource(plugins.ClassCore, corePluginPaths())
       ├─> {StaticRootPath}/app/plugins/datasource/
       └─> {StaticRootPath}/app/plugins/panel/

2. 发现阶段
   ↓
   发现: public/app/plugins/datasource/prometheus/plugin.json
   内容:
   {
     "id": "prometheus",
     "type": "datasource",
     "backend": true,  ← 关键字段！
     ...
   }

3. Bootstrap 阶段
   ↓
   创建 Plugin 对象:
   plugin := &plugins.Plugin{
     JSONData: {
       ID: "prometheus",
       Backend: true,  ← 从 plugin.json 读取
     },
     Class: plugins.ClassCore,  ← 标记为 Core
     ...
   }

4. Initialization - Backend Client Init
   ↓
   if plugin.Backend == true {  ← 检查标志
     ↓
     backendProvider.BackendFactory(ctx, plugin)
     ↓
     对于 Core Plugin:
       CoreRegistry.BackendFactoryProvider()
       └─> if plugin.IsCorePlugin() {
             return CoreRegistry.Get(plugin.ID)  // "prometheus"
           }
     ↓
     查找 Core Registry 中的映射:
       map[string]backendplugin.PluginFactoryFunc{
         "prometheus": asBackendPlugin(prometheusService),  ← 找到！
         "mysql":      asBackendPlugin(mysqlService),
         ...
       }
     ↓
     prometheusService 来自 Wire 依赖注入:
       ProvideCoreRegistry(
         ...
         pr *prometheus.Service,  ← prometheus.ProvideService() 创建
         ...
       )
     ↓
     asBackendPlugin(prometheusService)
     └─> coreplugin.New(backend.ServeOpts{
           QueryDataHandler: prometheusService,
           CheckHealthHandler: prometheusService,
         })
     ↓
     backendClient := factory(pluginID, logger, tracer, envFunc)
     plugin.RegisterClient(backendClient)  ← 注册 client
   }

5. 最终注册
   ↓
   pluginRegistry.Add(ctx, plugin)
   ↓
   插件可用！
```

---

## 总结

### Load 方法的核心作用

1. **统一入口**: 所有插件（Core/External）都通过 `Load` 方法加载
2. **四阶段处理**: Discovery → Bootstrap → Validation → Initialization
3. **多场景调用**:
   - 启动加载（主要）
   - 动态安装
   - 渲染器加载
   - 元数据查询

### Backend Core Plugin 的关键特点

1. **需要 plugin.json**: 即使是纯 Go 实现，也必须有 `plugin.json` 配置
2. **Backend 字段关键**: `"backend": true` 触发 backend client 初始化
3. **In-Process 执行**: Core plugins 运行在主进程中，不需要独立进程
4. **Wire 依赖注入**: 通过 Wire 自动注入所有 core plugin 服务
5. **统一接口**: 实现 `backend.QueryDataHandler` 等标准接口

### 为什么需要完整流程

即使是纯后端插件，也需要完整流程的原因：

1. **元数据管理**: 名称、类型、Logo、Dashboard 等
2. **权限配置**: Routes、Roles、ActionSets
3. **前后端协同**: 前端需要知道插件能力（metrics、alerting 等）
4. **统一生命周期**: 注册、初始化、错误追踪、指标统计
5. **配置验证**: 签名、类型、依赖等

### 核心文件清单

**加载流程**:
- `pkg/plugins/manager/loader/loader.go` - Load 方法主实现
- `pkg/services/pluginsintegration/pluginstore/store.go` - 应用启动加载

**Pipeline 阶段**:
- `pkg/plugins/manager/pipeline/discovery/` - Discovery 阶段
- `pkg/plugins/manager/pipeline/bootstrap/` - Bootstrap 阶段
- `pkg/plugins/manager/pipeline/validation/` - Validation 阶段
- `pkg/plugins/manager/pipeline/initialization/` - Initialization 阶段

**Core Plugin 支持**:
- `pkg/services/pluginsintegration/coreplugin/coreplugins.go` - Core Registry
- `pkg/services/pluginsintegration/pluginsources/pluginsources.go` - 插件源配置
- `pkg/services/pluginsintegration/pipeline/pipeline.go` - Pipeline 配置

**具体实现示例（Prometheus）**:
- `public/app/plugins/datasource/prometheus/plugin.json` - 元数据
- `pkg/tsdb/prometheus/prometheus.go` - Go 实现

---

## 附录

### plugin.json 示例（Prometheus）

```json
{
  "type": "datasource",
  "name": "Prometheus",
  "id": "prometheus",
  "category": "tsdb",
  "backend": true,
  "metrics": true,
  "alerting": true,
  "annotations": true,
  "queryOptions": {
    "minInterval": true
  },
  "routes": [
    {
      "method": "POST",
      "path": "api/v1/query",
      "reqRole": "Viewer",
      "reqAction": "datasources:query"
    }
  ],
  "info": {
    "description": "Open source time series database & alerting",
    "author": {
      "name": "Grafana Labs"
    },
    "logos": {
      "small": "img/prometheus_logo.svg"
    }
  }
}
```

### Wire 依赖注入配置

**文件**: `pkg/services/pluginsintegration/pluginsintegration.go`

```go
var WireSet = wire.NewSet(
    // ...
    pluginstore.ProvideService,
    loader.ProvideService,
    registry.ProvideService,
    coreplugin.ProvideCoreRegistry,  // ← Core Registry
    // ...
)

// ProvideCoreRegistry 接收所有 core plugin 服务
func ProvideCoreRegistry(
    tracer trace.Tracer,
    am *azuremonitor.Service,
    cw *cloudwatch.Service,
    pr *prometheus.Service,  // ← prometheus.ProvideService() 创建
    // ... 其他数据源
) *Registry {
    return NewRegistry(map[string]backendplugin.PluginFactoryFunc{
        "prometheus": asBackendPlugin(pr),
        // ...
    })
}
```

### 相关命令

```bash
# 查看 core plugin 路径
ls -la public/app/plugins/datasource/

# 查看 plugin.json
cat public/app/plugins/datasource/prometheus/plugin.json

# 查找所有 backend plugin
grep -r '"backend": true' public/app/plugins/datasource/*/plugin.json

# 查看 Load 方法调用
grep -rn "\.Load(ctx" pkg/services/pluginsintegration/
```

---

**文档生成时间**: 2026-02-06
**Grafana 版本**: v11.4.0+
**分析人员**: Claude Code