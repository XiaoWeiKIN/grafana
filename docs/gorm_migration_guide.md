# 使用 GORM 实现数据库迁移 (Migrator)

GORM 自身提供了自动迁移功能 (`AutoMigrate`)，但对于生产环境，通常需要更严格的版本控制迁移（如 Grafana 的 `sqlstore` 中所见）。

以下是两种主要方法的实现指南：

## 1. GORM 原生 AutoMigrate (适用于简单项目/开发环境)

GORM 的 `AutoMigrate` 会自动根据结构体创建表、缺失的列和索引。**注意：它不会删除未使用的列以保护数据。**

```go
package main

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"log"
)

type User struct {
	gorm.Model
	Name  string
	Email string `gorm:"unique"`
}

func main() {
	dsn := "user:pass@tcp(127.0.0.1:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local"
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	// 自动迁移模式
	// 仅用于创建/修改表结构，不会处理数据迁移或复杂的结构变更（如重命名列）
	if err := db.AutoMigrate(&User{}); err != nil {
		log.Fatal(err)
	}
}
```

## 2. 版本化迁移 (推荐生产环境)

为了实现类似 Grafana `sqlstore` 中基于版本的迁移（即 `migrations` 表记录已执行的迁移），推荐配合使用 **[go-gormigrate](https://github.com/go-gormigrate/gormigrate)**。

### 核心功能
- **版本控制**：每个迁移都有一个唯一的 ID。
- **原子性**：支持事务，失败回滚。
- **回调**：支持 `Migrate` (升级) 和 `Rollback` (降级)。

### 实现示例

```go
package main

import (
	"log"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	db, _ := gorm.Open(mysql.Open("user:pass@..."), &gorm.Config{})

	// 定义迁移列表
	m := gormigrate.New(db, gormigrate.DefaultOptions, []*gormigrate.Migration{
		// 迁移 1: 初始化用户表
		{
			ID: "2024020901_create_users_table",
			Migrate: func(tx *gorm.DB) error {
				type User struct {
					gorm.Model
					Username string
				}
				return tx.AutoMigrate(&User{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable("users")
			},
		},
		// 迁移 2: 添加 Email 列
		{
			ID: "2024020902_add_email_to_users",
			Migrate: func(tx *gorm.DB) error {
				// 使用匿名结构体定义当时的表结构
				type User struct {
					Email string `gorm:"size:100"`
				}
				return tx.Migrator().AddColumn(&User{}, "Email")
			},
			Rollback: func(tx *gorm.DB) error {
				type User struct{}
				return tx.Migrator().DropColumn(&User{}, "Email")
			},
		},
	})

	// 执行迁移
	if err := m.Migrate(); err != nil {
		log.Fatalf("Could not migrate: %v", err)
	}
	log.Printf("Migration did run successfully")
}
```

## 3. 对比 Grafana `sqlstore`

Grafana 的 `sqlstore` 本质上是一个自定义的迁移引擎。使用 `gormigrate` 可以达到类似的效果：

| 特性 | Grafana (sqlstore) | GORM + gormigrate |
| :--- | :--- | :--- |
| **ORM** | xorm | gorm |
| **迁移记录** | `migration_log` 表 | `migrations` 表 |
| **定义方式** | 流畅 API / SQL 字符串 | 结构体 `AutoMigrate` / GORM API |
| **多数据库支持** | 通过 `Dialect` 接口抽象 | GORM 原生支持 (MySQL, PG, SQLite, SQLServer) |
| **锁机制** | 自实现咨询锁 | 需自行实现或依赖外部工具 |

## 4. 迁移的最佳执行位置

对于 GORM 迁移代码的放置位置，主要取决于应用的部署架构。

### 方案 A：应用启动时同步执行 (简单，单体应用)

这是 Grafana 采用的方式，适合中小规模部署。在 HTTP 服务器启动前同步执行。

**实现位置**：`main.go` 或依赖注入的初始化阶段。

```go
func main() {
    // 1. 初始化 DB
    db := initDB()

    // 2. 执行迁移 (阻塞操作，失败则退出)
    // 必须在 StartHTTPServer 之前
    if err := runMigrations(db); err != nil {
        log.Fatalf("Migration failed: %v", err)
    }

    // 3. 启动 Web 服务
    StartHTTPServer()
}
```

### 方案 B：独立 CLI 命令 (推荐，生产环境/K8s)

将迁移逻辑封装为独立的 CLI 命令（如 `myapp migrate`）。这在 Kubernetes 环境中非常有用，可以作为 `Job` 或 `InitContainer` 运行。

**实现位置**：`cmd/migrate/main.go` 或使用 `cobra` 等库定义的子命令。

```go
// cmd/myapp/main.go
var migrateCmd = &cobra.Command{
    Use:   "migrate",
    Short: "Run database migrations",
    Run: func(cmd *cobra.Command, args []string) {
        db := initDB()
        if err := runMigrations(db); err != nil {
            os.Exit(1)
        }
    },
}
```

**优点**：
1.  **分离关注点**：Web 服务只处理请求，不负责架构变更。
2.  **安全性**：可以在受控的时间点执行迁移，而不是每次重启服务都尝试。
3.  **并发控制**：避免多个 Pod 同时启动时竞争执行迁移（虽然好的迁移库会有锁，但独立 Job 更稳健）。

### 建议
如果在构建一个新的 Go 应用，推荐使用 **GORM + gormigrate**。它结合了 GORM 强大的定义能力和必要的版本控制机制，且社区活跃度高。
