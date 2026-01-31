# Grafana MSSQL 数据源

## 概述

这是 Grafana 的 Microsoft SQL Server 数据源实现，支持通过标准 SQL 语句查询 MSSQL 数据库并将结果可视化。该数据源支持多种认证方式，包括 SQL Server 认证、Windows 认证、Kerberos 认证以及 Azure AD 认证。

## 目录结构

```
pkg/tsdb/mssql/
├── mssql.go                  # 主服务入口，定义 Service 和依赖注入
├── mssql_test.go             # 集成测试
├── sqleng/                   # SQL 查询引擎核心实现
│   ├── sql_engine.go         # 主查询处理逻辑
│   ├── connection.go         # 数据库连接和连接字符串生成
│   ├── macros.go             # SQL 宏扩展
│   ├── handler_checkhealth.go# 健康检查处理
│   └── proxy.go              # 代理支持
├── azure/                    # Azure AD 认证支持
│   └── connection.go         # Azure 凭据连接字符串生成
├── kerberos/                 # Kerberos 认证支持
│   └── kerberos.go           # Kerberos 配置处理
├── utils/                    # 工具函数
│   └── utils.go              # URL 解析、类型转换等
└── standalone/               # 独立运行模式
    └── main.go               # 插件独立运行入口
```

## 核心组件

### Service (`mssql.go`)

`Service` 是 MSSQL 数据源的顶层服务，实现了以下功能：

```go
type Service struct {
    im     instancemgmt.InstanceManager  // 实例管理器
    logger log.Logger                    // 日志记录器
}
```

**主要方法：**

| 方法 | 描述 |
|------|------|
| `ProvideService()` | 创建并返回 Service 实例，使用依赖注入模式 |
| `QueryData()` | 执行数据查询，支持用户上下文注入 |
| `CheckHealth()` | 健康检查，验证数据库连接状态 |
| `getDataSourceHandler()` | 获取数据源处理器实例 |
| `NewInstanceSettings()` | 创建数据源实例配置的工厂函数 |

### DataSourceHandler (`sqleng/sql_engine.go`)

`DataSourceHandler` 是查询处理的核心，负责：

```go
type DataSourceHandler struct {
    macroEngine            SQLMacroEngine        // SQL 宏引擎
    queryResultTransformer SqlQueryResultTransformer // 结果转换器
    db                     *sql.DB               // 数据库连接
    timeColumnNames        []string              // 时间列名称
    metricColumnTypes      []string              // 指标列类型
    dsInfo                 DataSourceInfo        // 数据源信息
    azureCredentials       azcredentials.AzureCredentials  // Azure 凭据
    kerberosAuth           kerberos.KerberosAuth // Kerberos 认证
    proxyClient            proxy.Client          // 代理客户端
    dbConnections          sync.Map              // 用户特定连接缓存
}
```

**主要方法：**

| 方法 | 描述 |
|------|------|
| `NewQueryDataHandler()` | 创建查询数据处理器 |
| `QueryData()` | 执行多个查询，并发生行 |
| `executeQuery()` | 执行单个查询 |
| `processResponse()` | 处理查询结果，转换为 Grafana Frame |
| `CheckHealth()` | 执行健康检查 |
| `Dispose()` | 清理资源，关闭数据库连接 |
| `newProcessCfg()` | 创建查询配置模型 |

## 认证方式

`sqleng/connection.go` 中定义了以下认证类型常量：

| 认证方式 | 常量 | 描述 |
|----------|------|------|
| SQL Server 认证 | `sqlServerAuthentication` | 使用用户名和密码 |
| Windows 认证 | `windowsAuthentication` | 使用 Windows 单点登录 |
| Kerberos 用户名+密码 | `kerberosRaw` | Windows AD 认证 |
| Kerberos Keytab | `kerberosKeytab` | 使用 Keytab 文件 |
| Kerberos 凭证缓存 | `kerberosCredentialCache` | 使用凭证缓存 |
| Kerberos 凭证缓存文件 | `kerberosCredentialCacheFile` | 使用凭证缓存文件 |
| Azure AD 认证 | `azureAuthentication` | Azure AD 多种认证方式 |

### Azure AD 认证类型 (`azure/connection.go`)

支持以下 Azure 认证方式：

1. **托管标识 (Managed Identity)**
   ```go
   fedauth=ActiveDirectoryManagedIdentity
   ```

2. **客户端密钥 (Client Secret)**
   ```go
   user id=<client_id>@<tenant_id>;password=<secret>;fedauth=ActiveDirectoryApplication
   ```

3. **Entra 密码 (Entra Password)**
   ```go
   user id=<user_id>;password=<password>;applicationclientid=<client_id>;fedauth=ActiveDirectoryPassword
   ```

4. **当前用户 (Current User - On-Behalf-Of)**
   ```go
   user id=<client_id>;userassertion=<token>;<password>;fedauth=ActiveDirectoryOnBehalfOf
   ```

### Kerberos 认证 (`kerberos/kerberos.go`)

Kerberos 认证结构：

```go
type KerberosAuth struct {
    KeytabFilePath            string  // Keytab 文件路径
    CredentialCache           string  // 凭证缓存文件
    CredentialCacheLookupFile string  // 凭证缓存查找文件
    ConfigFilePath            string  // Kerberos 配置文件
    UDPConnectionLimit        int     // UDP 连接限制
    EnableDNSLookupKDC        string  // DNS 查找 KDC 开关
}
```

凭证缓存查找文件格式：

```json
[
  {
    "user": "username",
    "database": "database_name",
    "address": "host:port",
    "credentialCache": "/path/to/ccache"
  }
]
```

## SQL 宏 (`sqleng/macros.go`)

MSSQL 数据源提供了丰富的 SQL 宏，用于在查询中动态插入时间和时间范围参数。

### 时间宏

| 宏 | 描述 | 示例 |
|----|------|------|
| `$__time(col)` | 将列命名为 time | `$__time(created_at)` → `created_at AS time` |
| `$__timeEpoch(col)` | 将日期时间列转换为 Unix 时间戳 | `$__timeEpoch(created_at)` → `DATEDIFF(second, '1970-01-01', created_at) AS time` |
| `$__timeFilter(col)` | 添加时间范围过滤 | `$__timeFilter(time)` → `time BETWEEN '...' AND '...'` |
| `$__timeFrom()` | 查询起始时间 (RFC3339) | `$__timeFrom()` → `'2024-01-01T00:00:00Z'` |
| `$__timeTo()` | 查询结束时间 (RFC3339) | `$__timeTo()` → `'2024-01-31T23:59:59Z'` |

### 时间分组宏

| 宏 | 描述 | 示例 |
|----|------|------|
| `$__timeGroup(col, interval)` | 按时间间隔分组 | `$__timeGroup(time, '5m')` → `FLOOR(DATEDIFF(second, '1970-01-01', time)/300)*300` |
| `$__timeGroup(col, interval, fill)` | 按时间间隔分组并填充缺失值 | `$__timeGroup(time, '5m', NULL)` |
| `$__timeGroupAlias(col, interval)` | 分组并别名为 time | - |

### Unix 时间宏

| 宏 | 描述 |
|----|------|
| `$__unixEpochFrom()` | Unix 纪元起始时间戳 |
| `$__unixEpochTo()` | Unix 纪元结束时间戳 |
| `$__unixEpochNanoFrom()` | Unix 纳秒起始时间戳 |
| `$__unixEpochNanoTo()` | Unix 纳秒结束时间戳 |
| `$__unixEpochFilter(col)` | Unix 时间戳范围过滤 |
| `$__unixEpochNanoFilter(col)` | Unix 纳秒时间戳范围过滤 |
| `$__unixEpochGroup(col, interval)` | 按 Unix 时间戳分组 |
| `$__unixEpochGroupAlias(col, interval)` | 分组并别名为 time |

### 全局宏

这些宏在所有 SQL 数据源中都可用：

| 宏 | 描述 |
|----|------|
| `$__interval_ms` | 查询间隔（毫秒） |
| `$__interval` | 查询间隔（格式化如 `5m`、`1h`） |

## 查询格式

### 时间序列查询 (time_series)

```sql
SELECT
  $__timeGroup(time, '5m') AS time,
  measurement AS metric,
  avg(value) AS value
FROM metrics
WHERE $__timeFilter(time)
GROUP BY $__timeGroup(time, '5m'), measurement
ORDER BY 1
```

### 表格查询 (table)

```sql
SELECT
  id,
  name,
  $__time(created_at) AS time,
  value
FROM my_table
WHERE $__timeFilter(created_at)
ORDER BY time DESC
```

### 注解查询

```sql
SELECT
  $__timeEpoch(start_time) AS time,
  $__timeEpoch(end_time) AS timeend,
  description AS text,
  tags
FROM annotations
WHERE $__unixEpochFilter(start_time)
```

## 数据类型转换 (`utils/utils.go`)

`MSSQLQueryResultTransformer` 提供了 MSSQL 特有类型的转换：

| MSSQL 类型 | Go 类型 | 转换说明 |
|------------|---------|----------|
| MONEY | float64 | 货币值转换为浮点数 |
| SMALLMONEY | float64 | 小货币值转换为浮点数 |
| DECIMAL | float64 | 十进制值转换为浮点数 |
| UNIQUEIDENTIFIER | string | UUID 转换为字符串 |
| SQL_VARIANT | string | SQL 变体类型转换为字符串 |

## 时间列处理

`sql_engine.go` 中的时间处理逻辑：

1. **时间列识别**：列名为 `time` 或配置的 `timeColumnNames`
2. **时间格式支持**：
   - DATETIME/TIME 等原生时间类型
   - Unix 时间戳（秒、毫秒）
   - 整数时间戳（int32, int64）
   - 浮点时间戳（float32, float64）

3. **时间转换**：`convertSQLTimeColumnToEpochMS` 将各种时间格式统一转换为 `NullableTime` 类型

4. **Epoch 精度处理**：`epochPrecisionToMS` 自动检测秒/毫秒/纳秒精度并转换为毫秒

## 填充模式 (`SetupFillmode`)

支持多种时间序列数据填充模式：

| 模式 | 值 | 描述 |
|------|-----|------|
| NULL | `null` | 缺失值填充为 NULL |
| Previous | `previous` | 使用前一个值填充 |
| Value | `1.5` | 使用指定值填充 |

## 健康检查 (`handler_checkhealth.go`)

健康检查功能：

1. **数据库连接测试**：调用 `db.Ping()`
2. **错误转换**：将连接错误转换为用户友好的消息
3. **权限控制**：管理员用户可以看到详细错误，普通用户看到精简消息
4. **日志记录**：记录配置摘要以便调试

## 代理支持 (`sqleng/proxy.go`)

支持通过 Secure SOCKS Proxy 连接数据库：

```go
type HostTransportDialer struct {
    Dialer proxy.ContextDialer  // 上下文拨号器
    Host   string               // 目标主机
}
```

## 配置结构

### JsonData 配置项

```go
type JsonData struct {
    MaxOpenConns            int    // 最大打开连接数
    MaxIdleConns            int    // 最大空闲连接数
    ConnMaxLifetime         int    // 连接最大生命周期（秒）
    ConnectionTimeout       int    // 连接超时（秒）
    Timescaledb             bool   // TimescaleDB 支持
    Mode                    string // SSL 模式
    ConfigurationMethod     string // TLS 配置方法
    TlsSkipVerify           bool   // 跳过 TLS 验证
    RootCertFile            string // 根证书文件
    CertFile                string // 客户端证书文件
    CertKeyFile             string // 客户端证书密钥文件
    Timezone                string // 时区
    Encrypt                 string // 加密模式
    Servername              string // 证书中的服务器名称
    TimeInterval            string // 时间间隔
    Database                string // 数据库名称
    SecureDSProxy           bool   // 启用安全 SOCKS 代理
    SecureDSProxyUsername   string // 代理用户名
    AllowCleartextPasswords bool   // 允许明文密码
    AuthenticationType      string // 认证类型
}
```

### QueryJson 查询配置

```go
type QueryJson struct {
    RawSql       string  // 原始 SQL
    Fill         bool    // 是否填充
    FillInterval float64 // 填充间隔
    FillMode     string  // 填充模式
    FillValue    float64 // 填充值
    Format       string  // 格式 (time_series/table)
}
```

## 查询执行流程

```
┌─────────────────────────────────────────────────────────────┐
│                        QueryData                            │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  1. 解析查询 JSON，验证参数                                 │
│     - 获取 rawSql                                          │
│     - 验证 fill 参数                                       │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  2. 宏替换                                                   │
│     - 全局宏替换 ($__interval, $__unixEpochFrom 等)       │
│     - MSSQL 宏替换 ($__timeFilter, $__timeGroup 等)        │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  3. 获取数据库连接                                           │
│     - 普通认证：使用共享连接池                              │
│     - Azure 当前用户：为每个用户创建独立连接                │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  4. 执行 SQL 查询                                            │
│     - 使用 QueryContext 执行                                │
│     - 支持 context 取消                                    │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  5. 结果转换与处理                                           │
│     - 转换为 Frame 对象                                     │
│     - 时间列转换                                            │
│     - 值列类型转换                                          │
│     - Long to Wide 转换（时间序列）                         │
│     - 重采样（填充模式）                                    │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  6. 返回 QueryDataResponse                                  │
└─────────────────────────────────────────────────────────────┘
```

## 行限制

查询结果受 `rowLimit` 配置限制，当结果集超过限制时：
- 返回被截断的结果
- 添加警告通知
- 通知严重性：`data.NoticeSeverityWarning`

## 独立运行模式

`standalone/main.go` 提供了插件独立运行的能力：

```go
datasource.Manage("mssql", mssql.NewInstanceSettings(logger), datasource.ManageOpts{})
```

这允许 MSSQL 插件作为独立进程运行，而不是内嵌在 Grafana 主进程中。

## 依赖项

- `github.com/microsoft/go-mssqldb` - MSSQL 驱动
- `github.com/microsoft/go-mssqldb/azuread` - Azure AD 认证
- `github.com/microsoft/go-mssqldb/integratedauth/krb5` - Kerberos 认证
- `github.com/grafana/grafana-plugin-sdk-go` - Grafana 插件 SDK
- `github.com/grafana/grafana-azure-sdk-go` - Azure SDK

## 测试

集成测试位于 `mssql_test.go`，涵盖：

- 数据类型映射测试
- 时间序列查询测试
- 宏功能测试
- 填充模式测试
- 注解查询测试
- 行限制测试
- 健康覆盖