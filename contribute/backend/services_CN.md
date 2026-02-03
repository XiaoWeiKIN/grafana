# 服务

Grafana _服务_ 封装应用逻辑，并通过一组相关操作将其暴露给应用程序的其他部分。

Grafana 使用 [Wire](https://github.com/google/wire)，这是一个代码生成工具，通过[依赖注入](https://en.wikipedia.org/wiki/Dependency_injection)自动连接组件。Wire 将组件之间的依赖表示为函数参数，鼓励使用显式初始化而非全局变量。

尽管 Grafana 中的服务做不同的事情，但它们共享一些模式。为了更好地理解服务的工作方式，让我们从头开始构建一个！

在服务能够开始与 Grafana 的其他部分通信之前，它需要在 Wire 中注册。请参阅以下服务示例中的 `ProvideService` 工厂方法，并注意它是如何在 `wire.go` 示例中被引用的。

当你运行 Wire 时，它会检查 `ProvideService` 的参数，并确保其所有依赖项都已正确连接和初始化。

**服务示例：**

```go
package example

// Service 服务负责 X、Y 和 Z。
type Service struct {
    logger   log.Logger
    cfg      *setting.Cfg
    sqlStore db.DB
}

// ProvideService 为其他服务提供 Service 作为依赖。
func ProvideService(cfg *setting.Cfg, sqlStore db.DB) (*Service, error) {
    s := &Service{
        logger:     log.New("service"),
        cfg:        cfg,
        sqlStore:   sqlStore,
    }

    if s.IsDisabled() {
        // 跳过某些初始化逻辑
        return s, nil
    }

    if err := s.init(); err != nil {
        return nil, err
    }

    return s, nil
}

func (s *Service) init() error {
    // 额外的初始化逻辑...
    return nil
}

// IsDisabled 如果服务被禁用则返回 true。
//
// 满足 registry.CanBeDisabled 接口，保证
// 如果服务被禁用则不会调用 Run()。
func (s *Service) IsDisabled() bool {
	return !s.cfg.IsServiceEnabled()
}

// Run 在后台运行服务。
//
// 满足 registry.BackgroundService 接口，
// 保证服务可以注册为后台服务。
func (s *Service) Run(ctx context.Context) error {
    // 后台服务逻辑...
    <-ctx.Done()
    return ctx.Err()
}
```

[wire.go](/pkg/server/wire.go)

```go
// +build wireinject

package server

import (
	"github.com/google/wire"
	"github.com/grafana/grafana/pkg/example"
    "github.com/grafana/grafana/pkg/infra/db"
)

var wireBasicSet = wire.NewSet(
	example.ProvideService,

)

var wireSet = wire.NewSet(
	wireBasicSet,
	sqlstore.ProvideService,
)

var wireTestSet = wire.NewSet(
	wireBasicSet,
)

func Initialize(cla setting.CommandLineArgs, opts Options, apiOpts api.ServerOptions) (*Server, error) {
	wire.Build(wireExtsSet)
	return &Server{}, nil
}

func InitializeForTest(cla setting.CommandLineArgs, opts Options, apiOpts api.ServerOptions, sqlStore db.DB) (*Server, error) {
	wire.Build(wireExtsTestSet)
	return &Server{}, nil
}

```

## 后台服务

后台服务在 Grafana 启动和关闭的生命周期之间在后台运行。要让你的服务在后台运行，它必须满足 `registry.BackgroundService` 接口。将其传递给 [ProvideBackgroundServiceRegistry](/pkg/registry/backgroundsvcs/background_services.go) 函数中的 `NewBackgroundServiceRegistry` 调用来注册它。

有关 `Run` 方法的示例，请参阅前面的示例。

## 禁用服务

如果你想保证当满足某些条件时 Grafana 不运行后台服务，或者如果服务被禁用，你的服务必须满足 `registry.CanBeDisabled` 接口。当 `service.IsDisabled` 方法返回 `true` 时，Grafana 不会调用 `service.Run` 方法。

如果你想无论服务是否禁用都运行某些初始化代码，你需要在服务工厂方法中处理这一点。

有关 `IsDisabled` 方法和服务禁用时的自定义初始化代码的示例，请参阅前面的实现代码。

## 运行 Wire（生成代码）

运行 `make run` 时会在第一次运行时调用 `make gen-go`。`gen-go` 会调用 Wire 二进制文件并在 [`wire_gen.go`](/pkg/server/wire_gen.go) 中生成代码。Wire 二进制文件使用 `go tool` 安装，它会下载并安装所需的所有工具，包括指定版本的 Wire 二进制文件。

## OSS 与 Enterprise

Grafana OSS 和 Grafana Enterprise 共享代码和依赖。Grafana Enterprise 会覆盖或扩展某些 OSS 服务。

有一个 [`wireexts_oss.go`](/pkg/server/wireexts_oss.go) 文件，它需要 `wireinject` 和 `oss` 构建标签作为要求。在这里你可以注册可能有其他实现的服务，例如 Grafana Enterprise。

类似地，Enterprise 源代码仓库中有一个 `wireexts_enterprise.go` 文件，你可以在其中覆盖或注册其他服务实现。

要扩展 OSS 后台服务，请为该类型创建一个特定的后台接口，并将该类型注入到 [`ProvideBackgroundServiceRegistry`](/pkg/registry/backgroundsvcs/background_services.go) 而不是具体类型。然后，在 [`wireexts_oss.go`](/pkg/server/wireexts_oss.go) 和 enterprise `wireexts` 文件中为该接口添加 Wire 绑定。

## 方法

服务的任何公共方法都应将 `context.Context` 作为第一个参数。如果方法调用总线，它将尽可能传播其他服务或数据库上下文。
