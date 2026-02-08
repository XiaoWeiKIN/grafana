# Grafana Backend Architecture Analysis & Guides

本仓库包含对 Grafana 后端架构（特别是数据存储和模块管理部分）的深入分析及其实战指南。

## 目录

1.  [SQLStore 源码分析](#1-sqlstore-源码分析)
2.  [GORM 数据库迁移指南](#2-gorm-数据库迁移指南)
3.  [dskit Module & Service 详解与 Demo](#3-dskit-module--service-详解与-demo)

---

## 1. SQLStore 源码分析

`pkg/services/sqlstore` 是 Grafana 的持久层核心。

### 核心组件
*   **SQLStore**: 管理数据库连接 (`xorm`) 和配置。
*   **Migrator**: 自定义的迁移引擎，支持多数据库方言 (MySQL, PG, SQLite) 和分布式锁。
*   **Session**: 封装 `sqlx`，提供统一的事务和查询接口。

### 关键机制
*   **迁移执行时机**: 服务启动时 (`ProvideService` -> `Migrate`)。
*   **并发控制**: 使用 Advisory Lock 防止多实例同时迁移。
*   **事务重试**: 针对 SQLite 的锁竞争实现了自动重试机制。

[查看详细分析报告 (sqlstore_analysis.md)](sqlstore_analysis.md)

---

## 2. GORM 数据库迁移指南

如何在自己的 Go 项目中实现类似 Grafana 的稳健迁移机制？我们推荐使用 **GORM + go-gormigrate**。

### 推荐方案
*   **工具**: `go-gormigrate` (支持版本控制、回滚)。
*   **执行位置**:
    *   **单体应用**: `main.go` 启动时同步执行。
    *   **K8s/生产环境**: 封装为独立的 CLI 命令 (`myapp migrate`)，作为 Job 运行。

[查看完整 GORM 迁移指南 (gorm_migration_guide.md)](gorm_migration_guide.md)

---

## 3. dskit Module & Service 详解与 Demo

Grafana 使用 `dskit` (源自 Cortex) 来管理其复杂的后台服务依赖。

### 核心概念
*   **Service**: 定义组件的生命周期 (`Starting` -> `Running` -> `Stopping`)。
*   **Module**: 为 Service 添加依赖管理。Manager 根据依赖图自动计算启动顺序。

### 实战 Demo
以下代码展示了如何构建一个包含 **Database -> UserStore -> ApiServer** 依赖链的系统。

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grafana/dskit/modules"
	"github.com/grafana/dskit/services"
)

// 模块名称
const (
	ModDatabase  = "database"
	ModUserStore = "user-store"
	ModApiServer = "api-server"
)

// --- 1. 定义 Service (业务逻辑) ---

// Database Service
type DBService struct {
	services.Service
}

func NewDBService() *DBService {
	s := &DBService{}
	s.Service = services.NewBasicService(s.starting, s.running, s.stopping)
	return s
}

func (s *DBService) starting(ctx context.Context) error {
	fmt.Println("[DB] Connecting...")
	time.Sleep(500 * time.Millisecond)
	fmt.Println("[DB] Connected!")
	return nil
}

func (s *DBService) running(ctx context.Context) error {
	fmt.Println("[DB] Ready.")
	<-ctx.Done()
	return nil
}

func (s *DBService) stopping(_ error) error {
	fmt.Println("[DB] Closing...")
	return nil
}

// ApiServer Service
type ApiServer struct {
	services.Service
}

func NewApiServer() *ApiServer {
	s := &ApiServer{}
	s.Service = services.NewBasicService(nil, s.running, nil)
	return s
}

func (s *ApiServer) running(ctx context.Context) error {
	fmt.Println("[API] Server Listening on :8080")
	<-ctx.Done()
	return nil
}

// --- 2. 编排 (main) ---

func main() {
	manager := modules.NewManager(nil)

	// 注册模块
	manager.RegisterModule(ModDatabase, func() (services.Service, error) {
		return NewDBService(), nil
	})
	
	manager.RegisterModule(ModUserStore, func() (services.Service, error) {
		fmt.Println("[UserStore] Init...")
		return services.NewIdleService(), nil 
	})

	manager.RegisterModule(ModApiServer, func() (services.Service, error) {
		return NewApiServer(), nil
	})

	// 定义依赖: API -> UserStore -> Database
	deps := map[string][]string{
		ModUserStore: {ModDatabase},
		ModApiServer: {ModUserStore},
	}
	manager.BuildModuleMap(deps)

	fmt.Println("--- System Starting ---")
	
	// 初始化
	serviceMap, err := manager.InitModuleServices(ModApiServer)
	if err != nil { panic(err) }

	// 统一管理服务
	var svcs []services.Service
	for _, s := range serviceMap { svcs = append(svcs, s) }
	servManager, _ := services.NewManager(svcs...)

	// 信号监听
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		fmt.Println("\n--- Stopping ---")
		cancel()
	}()

	// 启动！
	if err := servManager.StartAsync(ctx); err != nil { panic(err) }
	
	servManager.AwaitStopped(context.Background())
	fmt.Println("--- Stopped ---")
}
```

[查看详细 dskit 指南 (dskit_guide.md)](dskit_guide.md)
