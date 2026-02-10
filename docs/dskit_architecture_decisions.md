# DSKit 模块系统架构决策文档

本文档记录了关于 Grafana 模块系统（基于 dskit）的分析及针对新项目的架构决策。

---

## 1. 核心概念辨析

### 1.1 `pkg/modules` vs `pkg/registry/backgroundsvcs/adapter`
- **`pkg/modules`**: 核心模块管理器。这是对 dskit `modules.Manager` 的封装，负责模块的注册、依赖解析和生命周期管理。
- **`adapter`**: 兼容层。由于 Grafana 存在大量仅实现 `Run(ctx)` 的旧式后台服务，该层负责将这些服务“包装”成 dskit 识别的 `services.Service`。

**结论**：新项目如果全部采用 dskit 模式，物理上不需要 `adapter` 目录，但可能需要其“逻辑”（见下文对简单服务的处理）。

### 1.2 `Engine` 接口的作用
```go
type Engine interface {
    Run(context.Context) error
    Shutdown(context.Context, string) error
}
```
**设计意图**：接口隔离原则。程序入口（如 `main.go`）只需要启动和停止能力，不需要知道模块如何注册（`Registry` 接口团队）。通过组合小接口，降低了组件间的耦合。

---

## 2. 依赖管理设计

### 2.1 为什么 Grafana 有两个 `dependencies.go`？
- **`pkg/modules` 版**: 处理基础设施依赖（如分布式环、Consul 集成等）。
- **`adapter` 版**: 处理业务层后台服务的启动顺序（如插件安装器、配置加载器）。

### 2.2 新项目方案
**合并管理**：新项目建议在 `pkg/modules/dependencies.go` 中统一维护一张依赖图，避免碎片化。

---

## 3. 聚合模块：Core vs All
- **`Core`**: 实际的服务模块（通常包含单机版 Grafana 的所有核心逻辑）。
- **`All`**: 虚拟模块（`nil` 服务）。它通过依赖关系拉起 `Core` 以及其他所有可选组件。
- **作用**: 在 `adapter` 中使用 `nil` 注册这些模块是为了建立“同步点”和“启动入口”，它们类似于 Makefile 中的伪目标（Phony Targets）。

---

## 4. 关键决策：如何处理简单后台服务？

对于只需执行一段循环逻辑的服务，有两种实现方案：

### 方案 A：适配器模式 (Grafana 风格)
- **优势**: 服务定义极简（实现 `Run(ctx)` 即可），适合大量业务开发。
- **代价**: 需要维护一个 Registry 来显式收集所有服务。

### 方案 B：Helper 函数模式 (简化版)
```go
func NewSimpleService(name string, run func(ctx) error) services.NamedService
```
- **本质**: 与适配器逻辑相同。
- **区别**: 放弃全局 Registry 收集，改用 Wire 显式注入每个服务。

**架构推荐**：
- 如果服务预计超过 10 个且变动频繁，使用 **适配器模式**（统一收集）。
- 如果项目初期核心服务明确且数量有限，使用 **Helper 函数模式**（代码路径更短）。

---

## 5. 新项目 Wire 注入模式参考

为了兼顾 DI 和模块注册，建议采用以下模式：

```go
func ProvideMyService(reg modules.Registry) (*MyService, error) {
    s := &MyService{}
    // 使用包装函数减少起手代码
    s.NamedService = modules.NewSimpleService("my-service", s.run)
    
    // 注入时自动完成注册
    reg.RegisterInvisibleModule("my-service", func() (services.Service, error) {
        return s, nil
    })
    
    return s, nil
}
```

---

## 6. pkg/registry 包的必要性分析

### 6.1 Grafana 的 `pkg/registry` 结构

Grafana 的 `pkg/registry` 包含四个子系统：

| 子系统 | 用途 | 新项目是否需要 |
|--------|------|---------------|
| `backgroundsvcs` | 后台服务管理 | ✅ 需要（但可简化） |
| `apis` | Kubernetes 风格 API Server | ❌ 不需要（除非做 K8s 集成） |
| `apps` | 应用扩展/插件机制 | ❌ 不需要（除非需要插件系统） |
| `usagestatssvcs` | 使用统计收集 | ❌ 可选 |

### 6.2 新项目的简化方案

#### 方案 1：完全去掉 `registry` 包（推荐服务数 < 10）

```
pkg/
├── modules/                    # 模块管理（必需）
│   ├── modules.go
│   ├── dependencies.go
│   └── helpers.go              # NewSimpleService 等辅助函数
└── services/                   # 所有业务服务
    ├── api/
    ├── query/
    └── cleanup/
```

每个服务直接注册到 `modules.Manager`，无需中间层。

#### 方案 2：保留轻量级 `registry`（服务数 10-30）

```
pkg/
├── modules/
├── registry/
│   ├── registry.go             # 只定义 BackgroundService 接口
│   └── adapter/                # 适配器（可选）
└── services/
```

**`registry.go` 只保留核心接口**：

```go
package registry

import "context"

type BackgroundService interface {
    Run(ctx context.Context) error
}

type CanBeDisabled interface {
    IsDisabled() bool
}

type BackgroundServiceRegistry interface {
    GetServices() []BackgroundService
}
```

### 6.3 决策建议

| 场景 | 建议 |
|------|------|
| 服务数量 < 10 | 不需要 `registry`，直接用 `modules` |
| 服务数量 10-30 | 轻量级 `registry` + `adapter` |
| 需要插件系统 | 参考 Grafana 的 `apps` 设计 |
| 需要 K8s 集成 | 参考 Grafana 的 `apis` 设计 |

**结论**：Grafana 的 `registry` 是为了管理其复杂性（APIs、Apps、UsageStats）。如果只有后台服务，直接用 `modules` 包即可。

---

## 7. BackgroundService 接口设计原理

### 7.1 为什么只有 `Run` 方法，没有 `Shutdown` 方法？

```go
type BackgroundService interface {
    Run(ctx context.Context) error  // 只有一个方法
}
```

**答案**：通过 `context.Context` 实现优雅关闭，这是 Go 语言的最佳实践。

### 7.2 工作原理

#### 服务实现方（监听 context）

```go
type CleanupService struct{}

func (s *CleanupService) Run(ctx context.Context) error {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():           // ← 监听 shutdown 信号
            log.Info("Cleanup service shutting down...")
            // 执行清理逻辑
            return ctx.Err()         // 返回，完成 shutdown
        case <-ticker.C:
            s.doCleanup()
        }
    }
}
```

#### 管理方（取消 context）

```go
// 启动时
ctx, cancel := context.WithCancel(context.Background())
go service.Run(ctx)

// 关闭时
cancel()  // ← 触发所有服务的 ctx.Done()
```

### 7.3 设计对比

| 设计方式 | 代码复杂度 | 优势 |
|---------|-----------|------|
| **显式 Shutdown 方法** | 高（需要协调两个方法） | 更明确 |
| **Context 模式** ✅ | 低（只需一个方法） | 符合 Go 惯用法、自动传播 |

#### 显式 Shutdown（不推荐）

```go
type Service interface {
    Run(ctx context.Context) error
    Shutdown(ctx context.Context) error  // 额外方法
}

// 实现时需要手动管理通道
type MyService struct {
    stopCh chan struct{}
}

func (s *MyService) Shutdown(ctx context.Context) error {
    close(s.stopCh)  // 需要手动关闭
    return nil
}
```

#### Context 模式（推荐）✅

```go
type Service interface {
    Run(ctx context.Context) error  // 一个方法搞定
}

func (s *MyService) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():  // Context 自动处理
            return ctx.Err()
        }
    }
}
```

### 7.4 Context 模式的优势

| 优势 | 说明 |
|------|------|
| **简洁** | 服务只需实现一个方法 |
| **标准** | 符合 Go 的惯用法（context 传递取消信号） |
| **自动传播** | context 取消会自动传播到所有子 goroutine |
| **超时控制** | 可以用 `context.WithTimeout` 实现强制关闭 |
| **组合性** | 可以轻松组合多个 context（如超时 + 取消） |

### 7.5 实际的 Shutdown 流程

在 adapter 中的实现：

```go
// pkg/registry/backgroundsvcs/adapter/service.go
func (a *serviceAdapter) running(ctx context.Context) error {
    serviceCtx, serviceCancel := context.WithCancel(ctx)
    
    go func() {
        <-a.stopCh  // 等待 dskit 的停止信号
        serviceCancel()  // ← 取消 context，触发服务 shutdown
    }()
    
    err := a.service.Run(serviceCtx)  // 服务通过 ctx.Done() 感知到关闭
    if err != nil && err != context.Canceled {
        return err
    }
    <-serviceCtx.Done()  // 确保清理完成
    return nil
}
```

### 7.6 总结

| 问题 | 答案 |
|------|------|
| 为什么没有 `Shutdown()` 方法？ | Context 的 `Done()` 通道就是 shutdown 信号 |
| 如何通知服务关闭？ | 调用 `cancel()` 函数 |
| 服务如何响应关闭？ | 监听 `<-ctx.Done()` |
| 如何确保清理完成？ | 在 `Run` 方法返回前执行清理逻辑 |
| 如何实现超时关闭？ | 使用 `context.WithTimeout` |

---

## 8. 总结建议

1. **坚持使用 dskit**: 它的状态机（New -> Starting -> Running -> Stopping -> Terminated）非常稳健。
2. **统一依赖图**: 放在 `pkg/modules` 下。
3. **可观测性先行**: 确保 `Tracing` 和 `Metrics` 位于依赖图的最底层（无依赖）。
4. **延迟抽象**: 初期可以使用 Helper 函数，当后台服务爆炸式增长时，再引入 `BackgroundServiceRegistry`。
5. **使用 Context 模式**: 不要为服务添加显式的 `Shutdown` 方法，通过 `ctx.Done()` 实现优雅关闭。
6. **按需引入 registry**: 只有后台服务时不需要完整的 `pkg/registry` 包，直接用 `pkg/modules` 即可。
