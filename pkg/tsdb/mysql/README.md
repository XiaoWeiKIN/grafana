# Grafana MySQL 数据源插件 - 源码详解

## 概述

`pkg/tsdb/mysql` 是 Grafana 中 MySQL 数据源的实现包，提供了与 MySQL 数据库的连接、查询、宏替换和结果转换等功能。该插件支持时间序列查询和表格查询两种模式。

## 目录结构

```
pkg/tsdb/mysql/
├── main.go                 # 插件入口（用于独立运行）
├── mysql.go                # MySQL 数据源实例创建和查询结果转换
├── mysql_service.go        # 数据源服务层，提供健康检查和查询接口
├── macros.go               # MySQL 宏引擎，实现宏替换功能
├── proxy.go                # SOCKS 代理支持
├── macros_test.go          # 宏引擎单元测试
├── mysql_test.go           # MySQL 集成测试
├── mysql_snapshot_test.go  # 快照测试（使用 golden file）
├── proxy_test.go           # 代理功能测试
├── sqleng/                 # SQL 引擎通用实现
│   ├── handler_checkhealth.go        # 健康检查处理器
│   ├── sql_engine.go                 # 核心查询引擎
│   ├── sql_engine_test.go            # 测试文件
│   └── util/                         # 工具函数
├── standalone/            # 独立插件入口
└── testdata/              # 测试数据
    ├── table/             # 表格查询测试数据
    └── time_series/       # 时间序列查询测试数据
```

---

## 核心模块详解

### 1. Service 层 (mysql_service.go)

#### 1.1 Service 结构体

```go
type Service struct {
    im     instancemgmt.InstanceManager  // 实例管理器，负责数据源实例的生命周期
    logger log.Logger                    // 日志记录器
}
```

#### 1.2 ProvideService() 服务构造函数

```go
func ProvideService() *Service {
    logger := backend.NewLoggerWith("logger", "tsdb.mysql")
    return &Service{
        im:     datasource.NewInstanceManager(NewInstanceSettings(logger)),
        logger: logger,
    }
}
```

**功能说明：**
- 创建一个新的 Service 实例
- 初始化一个数据源实例管理器 `InstanceManager`
- 实例管理器负责为每个数据源 ID 创建独立的数据源实例
- 当数据源配置变更时，旧的实例会被释放（Dispose）并创建新实例

#### 1.3 getDataSourceHandler() 获取数据源处理器

```go
func (s *Service) getDataSourceHandler(ctx context.Context, pluginCtx backend.PluginContext) (*sqleng.DataSourceHandler, error) {
    i, err := s.im.Get(ctx, pluginCtx)
    if err != nil {
        return nil, err
    }
    instance := i.(*sqleng.DataSourceHandler)
    return instance, nil
}
```

**功能说明：**
- 从实例管理器中获取指定插件上下文对应的数据源处理器
- 如果实例不存在，会调用 `NewInstanceSettings` 工厂函数创建新实例

#### 1.4 CheckHealth() 健康检查接口

```go
func (s *Service) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
    dsHandler, err := s.getDataSourceHandler(ctx, req.PluginContext)
    if err != nil {
        return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: err.Error()}, nil
    }
    return dsHandler.CheckHealth(ctx, req)
}
```

**功能说明：**
- 仅作为转发层，调用底层处理器的健康检查方法
- 返回包含健康状态和消息的 `CheckHealthResult`

#### 1.5 QueryData() 数据查询接口

```go
func (s *Service) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    dsHandler, err := s.getDataSourceHandler(ctx, req.PluginContext)
    if err != nil {
        return nil, err
    }
    return dsHandler.QueryData(ctx, req)
}
```

**功能说明：**
- 转发查询请求到底层数据源处理器
- 支持并发执行多个查询（每个查询一个 goroutine）

---

### 2. 数据源实例创建 (mysql.go)

#### 2.1 NewInstanceSettings() 工厂函数

这是数据源实例的核心工厂函数，负责创建 SQL 连接和配置。

```go
func NewInstanceSettings(logger log.Logger) datasource.InstanceFactoryFunc {
    return func(ctx context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
        // 1. 从上下文中获取 Grafana 配置
        cfg := backend.GrafanaConfigFromContext(ctx)
        sqlCfg, err := cfg.SQL()

        // 2. 初始化默认 JSON 配置
        jsonData := sqleng.JsonData{
            MaxOpenConns:            sqlCfg.DefaultMaxOpenConns,
            MaxIdleConns:            sqlCfg.DefaultMaxIdleConns,
            ConnMaxLifetime:         sqlCfg.DefaultMaxConnLifetimeSeconds,
            SecureDSProxy:           false,
            AllowCleartextPasswords: false,
        }

        // 3. 覆盖用户配置
        err = json.Unmarshal(settings.JSONData, &jsonData)

        // 4. 确定数据库名称
        database := jsonData.Database
        if database == "" {
            database = settings.Database  // 回退到数据库级别的配置
        }

        // 5. 构建数据源信息
        dsInfo := sqleng.DataSourceInfo{...}

        // 6. 确定连接协议 (tcp 或 unix)
        protocol := "tcp"
        if strings.HasPrefix(dsInfo.URL, "/") {
            protocol = "unix"  // Unix 域套接字
        }

        // 7. 处理 SOCKS 代理
        proxyClient, err := settings.ProxyClient(ctx)
        if proxyClient.SecureSocksProxyEnabled() {
            // 注册代理拨号器
            protocol, err = registerProxyDialerContext(protocol, uniqueIdentifier, dialer)
        }

        // 8. 构建 MySQL 连接字符串
        cnnstr := fmt.Sprintf("%s:%s@%s(%s)/%s?collation=utf8mb4_unicode_ci&parseTime=true&loc=UTC&allowNativePasswords=true",
            characterEscape(dsInfo.User, ":"),
            dsInfo.DecryptedSecureJSONData["password"],
            protocol,
            characterEscape(dsInfo.URL, ")"),
            characterEscape(dsInfo.Database, "?"),
        )

        // 9. 处理 TLS 配置
        tlsConfig, err := sdkhttpclient.GetTLSConfig(opts)
        if tlsConfig.RootCAs != nil || len(tlsConfig.Certificates) > 0 {
            // 注册自定义 TLS 配置
            mysql.RegisterTLSConfig(tlsConfigString, tlsConfig)
            cnnstr += "&tls=" + tlsConfigString
        } else if tlsConfig.InsecureSkipVerify {
            cnnstr += "&tls=skip-verify"
        }

        // 10. 处理时区配置
        if dsInfo.JsonData.Timezone != "" {
            cnnstr += fmt.Sprintf("&time_zone='%s'", url.QueryEscape(dsInfo.JsonData.Timezone))
        }

        // 11. 配置查询处理
        config := sqleng.DataPluginConfiguration{
            DSInfo:            dsInfo,
            TimeColumnNames:   []string{"time", "time_sec"},  // 识别的时间列
            MetricColumnTypes: []string{"CHAR", "VARCHAR", "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT"},  // 指标列类型
            RowLimit:          sqlCfg.RowLimit,
        }

        // 12. 创建结果转换器和宏引擎
        rowTransformer := mysqlQueryResultTransformer{userError: userFacingDefaultError}

        // 13. 打开数据库连接
        db, err := sql.Open("mysql", cnnstr)

        // 14. 设置连接池参数
        db.SetMaxOpenConns(config.DSInfo.JsonData.MaxOpenConns)
        db.SetMaxIdleConns(config.DSInfo.JsonData.MaxIdleConns)
        db.SetConnMaxLifetime(time.Duration(config.DSInfo.JsonData.ConnMaxLifetime) * time.Second)

        // 15. 返回查询处理器
        return sqleng.NewQueryDataHandler(userFacingDefaultError, db, config, &rowTransformer, newMysqlMacroEngine(logger, userFacingDefaultError), logger)
    }
}
```

#### 2.2 连接字符串参数说明

| 参数 | 说明 | 示例 |
|------|------|------|
| `collation` | 字符集排序规则 | `utf8mb4_unicode_ci` |
| `parseTime` | 解析时间类型 | `true` |
| `loc` | 时区 | `UTC` |
| `allowNativePasswords` | 允许原生密码认证 | `true` |
| `allowCleartextPasswords` | 允许明文密码 | `true` |
| `tls` | TLS 配置 | `skip-verify` 或自定义配置名 |
| `time_zone` | 数据库时区 | `'+08:00'` |

---

### 3. 查询结果转换器 (mysql.go)

#### 3.1 mysqlQueryResultTransformer 结构体

```go
type mysqlQueryResultTransformer struct {
    userError string  // 用户友好的错误消息
}
```

#### 3.2 TransformQueryError() 错误转换

```go
func (t *mysqlQueryResultTransformer) TransformQueryError(logger log.Logger, err error) error {
    var driverErr *mysql.MySQLError
    if errors.As(err, &driverErr) {
        // 对于解析错误、字段错误、表不存在的错误，返回详细错误信息
        if driverErr.Number != mysqlerr.ER_PARSE_ERROR &&
            driverErr.Number != mysqlerr.ER_BAD_FIELD_ERROR &&
            driverErr.Number != mysqlerr.ER_NO_SUCH_TABLE {
            logger.Error("Query error", "error", err)
            return fmt.Errorf(("query failed - %s"), t.userError)  // 返回通用错误消息（安全考虑）
        }
    }
    return err  // 返回原始错误
}
```

**安全处理逻辑：**
- **显示详细错误的错误：**
  - `ER_PARSE_ERROR` (1064) - SQL 语法错误
  - `ER_BAD_FIELD_ERROR` (1054) - 字段不存在
  - `ER_NO_SUCH_TABLE` (1146) - 表不存在
- **隐藏详细错误**（返回通用消息）：其他所有错误

#### 3.3 GetConverterList() 类型转换列表

这是 MySQL 插件的核心类型转换系统，定义了如何将 MySQL 数据类型转换为 Grafana 可识别的数据类型。

```go
func (t *mysqlQueryResultTransformer) GetConverterList() []sqlutil.StringConverter {
    return []sqlutil.StringConverter{
        // 1. DOUBLE -> Float64
        {
            Name:           "handle DOUBLE",
            InputScanKind:  reflect.Struct,
            InputTypeName:  "DOUBLE",
            Replacer: &sqlutil.StringFieldReplacer{
                OutputFieldType: data.FieldTypeNullableFloat64,
                ReplaceFunc: func(in *string) (any, error) {
                    if in == nil { return nil, nil }
                    v, err := strconv.ParseFloat(*in, 64)
                    return &v, err
                },
            },
        },
        // 2. BIGINT -> Int64
        // ... (其他整数类型)
        // 3. DECIMAL -> Float64
        {
            Name:           "handle DECIMAL",
            InputScanKind:  reflect.Slice,  // DECIMAL 作为字节切片读取
            InputTypeName:  "DECIMAL",
            OutputFieldType: data.FieldTypeNullableFloat64,
            ReplaceFunc: func(in *string) (any, error) {
                if in == nil { return nil, nil }
                v, err := strconv.ParseFloat(*in, 64)
                return &v, err
            },
        },
        // 4. DATETIME -> Time
        {
            Name:           "handle DATETIME",
            InputScanKind:  reflect.Struct,
            InputTypeName:  "DATETIME",
            OutputFieldType: data.FieldTypeNullableTime,
            ReplaceFunc: func(in *string) (any, error) {
                if in == nil { return nil, nil }
                // 尝试多种时间格式
                v, err := time.Parse("2006-01-02 15:04:05", *in)
                if err == nil { return &v, nil }
                v, err = time.Parse("2006-01-02T15:04:05Z", *in)
                return &v, err
            },
        },
        // ... (其他日期时间类型和整数类型)
    }
}
```

#### 3.4 完整类型转换映射

| MySQL 类型 | Go 类型 | 输出类型 | 转换方法 |
|------------|---------|----------|----------|
| DOUBLE | string | NullableFloat64 | `strconv.ParseFloat` |
| FLOAT | string | NullableFloat64 | `strconv.ParseFloat` |
| DECIMAL | string | NullableFloat64 | `strconv.ParseFloat` |
| BIGINT | string | NullableInt64 | `strconv.ParseInt` (base 10, 64-bit) |
| INT | string | NullableInt64 | `strconv.ParseInt` (base 10, 64-bit) |
| SMALLINT | string | NullableInt64 | `strconv.ParseInt` (base 10, 64-bit) |
| TINYINT | string | NullableInt64 | `strconv.ParseInt` (base 10, 64-bit) |
| YEAR | string | NullableInt64 | `strconv.ParseInt` (base 10, 64-bit) |
| DATETIME | string | NullableTime | `time.Parse` (两种格式) |
| DATE | string | NullableTime | `time.Parse` (三种格式) |
| TIMESTAMP | string | NullableTime | `time.Parse` (两种格式) |

---

### 4. 宏引擎 (macros.go)

#### 4.1 mySQLMacroEngine 结构体

```go
type mySQLMacroEngine struct {
    *sqleng.SQLMacroEngineBase  // 继承基础宏引擎
    logger    log.Logger
    userError string
}
```

#### 4.2 安全限制正则表达式

```go
var restrictedRegExp = regexp.MustCompile(`(?im)([\s]*show[\s]+grants|[\s,]session_user\([^\)]*\)|[\s,]current_user(\([^\)]*\))?|[\s,]system_user\([^\)]*\)|[\s,]user\([^\)]*\))([\s,;]|$)`)
```

**禁止的查询模式（防止信息泄露）：**
- `SHOW GRANTS` - 显示用户权限
- `SESSION_USER()` - 获取会话用户
- `CURRENT_USER()` - 获取当前用户
- `SYSTEM_USER()` - 获取系统用户
- `USER()` - 获取用户名

#### 4.3 Interpolate() 宏替换主函数

```go
func (m *mySQLMacroEngine) Interpolate(query *backend.DataQuery, timeRange backend.TimeRange, sql string) (string, error) {
    // 1. 安全检查 - 检测是否包含禁止的函数
    matches := restrictedRegExp.FindAllStringSubmatch(sql, 1)
    if len(matches) > 0 {
        m.logger.Error("Show grants, session_user(), current_user(), system_user() or user() not allowed in query")
        return "", fmt.Errorf("invalid query - %s", m.userError)
    }

    // 2. 编译宏正则表达式：\$([_a-zA-Z0-9]+)\(([^\)]*)\)
    rExp, _ := regexp.Compile(sExpr)

    // 3. 替换所有宏
    var macroError error
    sql = m.ReplaceAllStringSubmatchFunc(rExp, sql, func(groups []string) string {
        args := strings.Split(groups[2], ",")
        for i, arg := range args {
            args[i] = strings.Trim(arg, " ")
        }
        res, err := m.evaluateMacro(timeRange, query, groups[1], args)
        if err != nil && macroError == nil {
            macroError = err
            return "macro_error()"  // 占位符，错误稍后处理
        }
        return res
    })

    // 4. 返回结果或错误
    if macroError != nil {
        return "", macroError
    }
    return sql, nil
}
```

#### 4.4 evaluateMacro() 宏求值函数

```go
func (m *mySQLMacroEngine) evaluateMacro(timeRange backend.TimeRange, query *backend.DataQuery, name string, args []string) (string, error) {
    switch name {
    // ===== 时间转换宏 =====
    case "__time", "__timeEpoch":
        return fmt.Sprintf("UNIX_TIMESTAMP(%s) as time_sec", args[0]), nil

    // ===== 时间过滤宏 =====
    case "__timeFilter":
        // 处理 1970 年之前的负时间戳
        if timeRange.From.UTC().Unix() < 0 {
            return fmt.Sprintf("%s BETWEEN DATE_ADD(FROM_UNIXTIME(0), INTERVAL %d SECOND) AND FROM_UNIXTIME(%d)",
                args[0], timeRange.From.UTC().Unix(), timeRange.To.UTC().Unix()), nil
        }
        return fmt.Sprintf("%s BETWEEN FROM_UNIXTIME(%d) AND FROM_UNIXTIME(%d)",
            args[0], timeRange.From.UTC().Unix(), timeRange.To.UTC().Unix()), nil

    // ===== 时间边界宏 =====
    case "__timeFrom":
        return fmt.Sprintf("FROM_UNIXTIME(%d)", timeRange.From.UTC().Unix()), nil
    case "__timeTo":
        return fmt.Sprintf("FROM_UNIXTIME(%d)", timeRange.To.UTC().Unix()), nil

    // ===== 时间分组宏 =====
    case "__timeGroup":
        interval, err := gtime.ParseInterval(strings.Trim(args[1], `'"`))
        // 设置填充模式
        if len(args) == 3 {
            err := sqleng.SetupFillmode(query, interval, args[2])
        }
        // 分桶算法：UNIX_TIMESTAMP(time) DIV 间隔 * 间隔
        return fmt.Sprintf("UNIX_TIMESTAMP(%s) DIV %.0f * %.0f", args[0], interval.Seconds(), interval.Seconds()), nil

    case "__timeGroupAlias":
        tg, err := m.evaluateMacro(timeRange, query, "__timeGroup", args)
        return tg + ` AS "time"`, nil

    // ===== Unix 时间戳过滤宏 =====
    case "__unixEpochFilter":
        return fmt.Sprintf("%s >= %d AND %s <= %d", args[0],
            timeRange.From.UTC().Unix(), args[0], timeRange.To.UTC().Unix()), nil

    case "__unixEpochNanoFilter":
        return fmt.Sprintf("%s >= %d AND %s <= %d", args[0],
            timeRange.From.UTC().UnixNano(), args[0], timeRange.To.UTC().UnixNano()), nil

    // ===== Unix 时间戳分组宏 =====
    case "__unixEpochGroup":
        interval, err := gtime.ParseInterval(strings.Trim(args[1], `'`))
        if len(args) == 3 {
            err := sqleng.SetupFillmode(query, interval, args[2])
        }
        return fmt.Sprintf("%s DIV %v * %v", args[0], interval.Seconds(), interval.Seconds()), nil

    case "__unixEpochGroupAlias":
        tg, err := m.evaluateMacro(timeRange, query, "__unixEpochGroup", args)
        return tg + ` AS "time"`, nil

    default:
        return "", fmt.Errorf("unknown macro %v", name)
    }
}
```

#### 4.5 宏使用示例表

| 宏 | 输入 SQL | 输出 SQL | 说明 |
|---|---|---|---|
| `__time(t)` | `SELECT $__time(created_at)` | `SELECT UNIX_TIMESTAMP(created_at) as time_sec` | 转换为 Unix 时间戳 |
| `__timeFilter(t)` | `WHERE $__timeFilter(t)` | `WHERE t BETWEEN FROM_UNIXTIME(123) AND FROM_UNIXTIME(456)` | 时间范围过滤 |
| `__timeFrom()` | `SELECT $__timeFrom()` | `SELECT FROM_UNIXTIME(123)` | 起始时间 |
| `__timeTo()` | `SELECT $__timeTo()` | `SELECT FROM_UNIXTIME(456)` | 结束时间 |
| `__timeGroup(t, '1h')` | `GROUP BY $__timeGroup(t, '1h')` | `GROUP BY UNIX_TIMESTAMP(t) DIV 3600 * 3600` | 按小时分桶 |
| `__timeGroupAlias(t, '1h')` | `GROUP BY $__timeGroupAlias(t, '1h')` | `GROUP BY UNIX_TIMESTAMP(t) DIV 3600 * 3600 AS "time"` | 分桶并命名为 time |
| `__unixEpochFilter(t)` | `WHERE $__unixEpochFilter(t)` | `WHERE t >= 123 AND t <= 456` | Unix 时间戳过滤 |
| `__unixEpochNanoFilter(t)` | `WHERE $__unixEpochNanoFilter(t)` | `WHERE t >= 123000000000 AND t <= 456000000000` | 纳秒级过滤 |
| `__unixEpochGroup(t, '1h')` | `GROUP BY $__unixEpochGroup(t, '1h')` | `GROUP BY t DIV 3600 * 3600` | Unix 时间戳分桶 |

#### 4.6 填充模式设置

填充参数通过第三个可选参数传递给 `__timeGroup` 或 `__unixEpochGroup` 宏：

```go
func SetupFillmode(query *backend.DataQuery, interval time.Duration, fillmode string) error {
    rawQueryProp := make(map[string]any)

    // 解析现有 JSON
    queryBytes, _ := query.JSON.MarshalJSON()
    json.Unmarshal(queryBytes, &rawQueryProp)

    rawQueryProp["fill"] = true
    rawQueryProp["fillInterval"] = interval.Seconds()

    switch fillmode {
    case "NULL":
        rawQueryProp["fillMode"] = "null"
    case "previous":
        rawQueryProp["fillMode"] = "previous"
    default:  // 数值
        rawQueryProp["fillMode"] = "value"
        floatVal, err := strconv.ParseFloat(fillmode, 64)
        rawQueryProp["fillValue"] = floatVal
    }

    // 更新查询 JSON
    query.JSON, _ = json.Marshal(rawQueryProp)
    return nil
}
```

**填充模式：**
- `'NULL'` - 缺失值填充为 NULL
- `'previous'` - 使用前一个有效值
- `'0'`, `'1.5'` 等 - 使用指定数值

---

### 5. SOCKS 代理支持 (proxy.go)

#### 5.1 registerProxyDialerContext() 注册代理拨号器

```go
func registerProxyDialerContext(protocol, cnnstr string, dialer proxy.Dialer) (string, error) {
    // 获取 MySQL 代理拨号器
    mysqlDialer, err := getProxyDialerContext(protocol, dialer)
    if err != nil {
        return "", err
    }

    // 使用 MD5 哈希生成唯一网络标识符
    hash := fmt.Sprintf("%x", md5.Sum([]byte(cnnstr)))
    network := "proxy-" + hash

    // 注册到 MySQL 驱动
    mysql.RegisterDialContext(network, mysqlDialer.DialContext)

    return network, nil
}
```

#### 5.2 mySQLContextDialer 适配器

```go
type mySQLContextDialer struct {
    dialer  proxy.ContextDialer  // 底层 golang 代理拨号器
    network string                // 实际网络类型 (tcp/unix)
}

func getProxyDialerContext(actualNetwork string, dialer proxy.Dialer) (*mySQLContextDialer, error) {
    // 类型断言，确保拨号器支持上下文
    contextDialer, ok := dialer.(proxy.ContextDialer)
    if !ok {
        return nil, fmt.Errorf("mysql proxy creation failed")
    }
    return &mySQLContextDialer{dialer: contextDialer, network: actualNetwork}, nil
}

// 实现 MySQL 的 DialContext 接口
func (d *mySQLContextDialer) DialContext(ctx context.Context, addr string) (net.Conn, error) {
    return d.dialer.DialContext(ctx, d.network, addr)
}
```

**工作原理：**
1. 创建 SOCKS 代理拨号器
2. 适配为 MySQL 兼容的拨号器
3. 使用 MD5 哈希生成唯一网络名称
4. 注册到 MySQL 驱动供连接字符串使用

---

### 6. SQL 引擎核心 (sqleng/sql_engine.go)

#### 6.1 DataSourceHandler 结构体

```go
type DataSourceHandler struct {
    macroEngine            SQLMacroEngine           // 宏引擎
    queryResultTransformer SqlQueryResultTransformer // 结果转换器
    db                     *sql.DB                  // 数据库连接
    timeColumnNames        []string                 // 时间列名称列表
    metricColumnTypes      []string                 // 指标列类型列表
    log                    log.Logger               // 日志记录器
    dsInfo                 DataSourceInfo           // 数据源配置
    rowLimit               int64                    // 行数限制
    userError              string                   // 用户友好错误消息
}
```

#### 6.2 QueryData() 主查询函数

```go
func (e *DataSourceHandler) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    result := backend.NewQueryDataResponse()
    ch := make(chan DBDataResponse, len(req.Queries))
    var wg sync.WaitGroup

    // 为每个查询启动一个 goroutine
    for _, query := range req.Queries {
        // 解析查询 JSON
        queryjson := QueryJson{Fill: false, Format: "time_series"}
        json.Unmarshal(query.JSON, &queryjson)

        // 验证填充参数
        if queryjson.Fill || queryjson.FillInterval != 0.0 || queryjson.FillMode != "" || queryjson.FillValue != 0.0 {
            return nil, fmt.Errorf("query fill-parameters not supported")
        }

        // 跳过空查询
        if queryjson.RawSql == "" {
            continue
        }

        // 并发执行查询
        wg.Add(1)
        go e.executeQuery(query, &wg, ctx, ch, queryjson)
    }

    wg.Wait()

    // 收集结果
    close(ch)
    result.Responses = make(map[string]backend.DataResponse)
    for queryResult := range ch {
        result.Responses[queryResult.refID] = queryResult.dataResponse
    }

    return result, nil
}
```

#### 6.3 executeQuery() 单查询执行

这是最核心的执行函数，包含完整的查询处理流程：

```go
func (e *DataSourceHandler) executeQuery(query backend.DataQuery, wg *sync.WaitGroup,
    queryContext context.Context, ch chan DBDataResponse, queryJson QueryJson) {
    defer wg.Done()

    // ===== 1. 错误处理 (Panic Recovery) =====
    defer func() {
        if r := recover(); r != nil {
            logger.Error("ExecuteQuery panic", "error", r, "stack", string(debug.Stack()))
            // 转换 panic 为错误
            queryResult.dataResponse.Error = ...
        }
    }()

    // ===== 2. 全局宏替换 =====
    interpolatedQuery := Interpolate(query, timeRange, e.dsInfo.JsonData.TimeInterval, queryJson.RawSql)
    // 替换 $__interval_ms、$__interval、$__unixEpochFrom()、$__unixEpochTo()

    // ===== 3. MySQL 宏替换 =====
    interpolatedQuery, err := e.macroEngine.Interpolate(&query, timeRange, interpolatedQuery)

    // ===== 4. 执行查询 =====
    rows, err := e.db.QueryContext(queryContext, interpolatedQuery)
    defer rows.Close()

    // ===== 5. 构建查询处理配置 =====
    qm, err := e.newProcessCfg(query, queryContext, rows, interpolatedQuery)

    // ===== 6. 转换结果到 Frame =====
    stringConverters := e.queryResultTransformer.GetConverterList()
    frame, err := sqlutil.FrameFromRows(rows, e.rowLimit, sqlutil.ToConverters(stringConverters...)...)

    // ===== 7. 处理空结果 =====
    if frame.Rows() == 0 {
        frame.Fields = []*data.Field{}  // 清空字段
        ch <- queryResult
        return
    }

    // ===== 8. 转换时间列（ epoch 精度调整）=====
    if err := convertSQLTimeColumnsToEpochMS(frame, qm); err != nil { ... }

    // ===== 9. 时间序列格式处理 =====
    if qm.Format == dataQueryFormatSeries {
        // 9.1 确保有时间列
        if qm.timeIndex == -1 {
            return errors.New("time column is missing")
        }

        // 9.2 重命名时间列为 "Time"（向后兼容 v8 之前）
        frame.Fields[qm.timeIndex].Name = data.TimeSeriesTimeFieldName

        // 9.3 转换数值列为 Float64
        for i := range qm.columnNames {
            if i == qm.timeIndex || i == qm.metricIndex { continue }
            if frame.Fields[i].Type() == data.FieldTypeString { continue }
            frame, err = convertSQLValueColumnToFloat(frame, i)
        }

        // 9.4 Long 转 Wide 格式（多系列处理）
        tsSchema := frame.TimeSeriesSchema()
        if tsSchema.Type == data.TimeSeriesTypeLong {
            frame, err = data.LongToWide(frame, qm.FillMissing)

            // 移除 metric 标签，将值迁移到字段名
            if len(originalData.Fields) == 3 {
                for _, field := range frame.Fields {
                    if len(field.Labels) == 1 {
                        name, ok := field.Labels["metric"]
                        if ok {
                            field.Name = name
                            field.Labels = nil
                        }
                    }
                }
            }
        }

        // 9.5 缺失数据填充
        if qm.FillMissing != nil {
            alignedTimeRange := backend.TimeRange{...}
            frame, err = sqlutil.ResampleWideFrame(frame, qm.FillMissing, alignedTimeRange, qm.Interval)
        }
    }

    ch <- queryResult
}
```

#### 6.4 newProcessCfg() 构建查询配置

```go
func (e *DataSourceHandler) newProcessCfg(query backend.DataQuery, queryContext context.Context,
    rows *sql.Rows, interpolatedQuery string) (*dataQueryModel, error) {

    // 获取列信息
    columnNames, _ := rows.Columns()
    columnTypes, _ := rows.ColumnTypes()

    qm := &dataQueryModel{
        columnTypes:  columnTypes,
        columnNames:  columnNames,
        timeIndex:    -1,
        timeEndIndex: -1,
        metricIndex:  -1,
    }

    // 解析查询格式
    queryJson := QueryJson{}
    json.Unmarshal(query.JSON, &queryJson)

    // 处理填充配置
    if queryJson.Fill {
        qm.FillMissing = &data.FillMissing{}
        switch strings.ToLower(queryJson.FillMode) {
        case "null":    qm.FillMissing.Mode = data.FillModeNull
        case "previous": qm.FillMissing.Mode = data.FillModePrevious
        case "value":   qm.FillMissing.Mode = data.FillModeValue; qm.FillMissing.Value = queryJson.FillValue
        }
    }

    // 确定查询格式
    switch queryJson.Format {
    case "time_series": qm.Format = dataQueryFormatSeries
    case "table":       qm.Format = dataQueryFormatTable
    }

    // 识别时间列
    for i, col := range qm.columnNames {
        for _, tc := range e.timeColumnNames {
            if col == tc {
                qm.timeIndex = i
                break
            }
        }
    }

    // 识别 timeend 列（表格格式）
    if qm.Format == dataQueryFormatTable && strings.EqualFold(col, "timeend") {
        qm.timeEndIndex = i
    }

    // 识别 metric 列
    switch col {
    case "metric":
        qm.metricIndex = i
    default:
        // 检查是否为字符串类型的指标列
        if qm.metricIndex == -1 {
            columnType := qm.columnTypes[i].DatabaseTypeName()
            for _, mct := range e.metricColumnTypes {
                if columnType == mct {
                    qm.metricIndex = i
                    continue
                }
            }
        }
    }

    return qm, nil
}
```

#### 6.5 时间列转换

```go
func convertSQLTimeColumnToEpochMS(frame *data.Frame, timeIndex int) error {
    origin := frame.Fields[timeIndex]
    valueType := origin.Type()

    // 已经是时间类型，不需要转换
    if valueType == data.FieldTypeTime || valueType == data.FieldTypeNullableTime {
        return nil
    }

    // 创建新的时间字段
    newField := data.NewFieldFromFieldType(data.FieldTypeNullableTime, 0)
    newField.Name = origin.Name
    newField.Labels = origin.Labels

    // 转换每个值
    valueLength := origin.Len()
    for i := 0; i < valueLength; i++ {
        v, err := origin.NullableFloatAt(i)
        if v == nil {
            newField.Append(nil)
        } else {
            // 处理不同的 epoch 精度
            timestamp := time.Unix(0, int64(epochPrecisionToMS(*v))*int64(time.Millisecond))
            newField.Append(&timestamp)
        }
    }
    frame.Fields[timeIndex] = newField
    return nil
}
```

**epoch 精度转换说明：**

```go
func epochPrecisionToMS(value float64) float64 {
    s := strconv.FormatFloat(value, 'e', -1, 64)
    if strings.HasSuffix(s, "e+09") {   // 纳秒 -> 毫秒
        return value * float64(1e3)
    }
    if strings.HasSuffix(s, "e+18") {   // 微秒 -> 毫秒
        return value / float64(time.Millisecond)
    }
    return value  // 已经是秒或毫秒
}
```

#### 6.6 数值列转换

```go
func convertSQLValueColumnToFloat(frame *data.Frame, Index int) (*data.Frame, error) {
    origin := frame.Fields[Index]
    valueType := origin.Type()

    // 已经是 Float64，不需要转换
    if valueType == data.FieldTypeFloat64 || valueType == data.FieldTypeNullableFloat64 {
        return frame, nil
    }

    // 创建新的 Float64 字段
    newField := data.NewFieldFromFieldType(data.FieldTypeNullableFloat64, origin.Len())
    newField.Name = origin.Name
    newField.Labels = origin.Labels

    // 转换每个值
    for i := 0; i < origin.Len(); i++ {
        v, err := origin.NullableFloatAt(i)
        newField.Set(i, v)
    }

    frame.Fields[Index] = newField
    return frame, nil
}
```

#### 6.7 全局宏替换

```go
var Interpolate = func(query backend.DataQuery, timeRange backend.TimeRange, timeInterval string, sql string) string {
    interval := query.Interval

    sql = strings.ReplaceAll(sql, "$__interval_ms", strconv.FormatInt(interval.Milliseconds(), 10))
    sql = strings.ReplaceAll(sql, "$__interval", gtime.FormatInterval(interval))
    sql = strings.ReplaceAll(sql, "$__unixEpochFrom()", fmt.Sprintf("%d", timeRange.From.UTC().Unix()))
    sql = strings.ReplaceAll(sql, "$__unixEpochTo()", fmt.Sprintf("%d", timeRange.To.UTC().Unix()))

    return sql
}
```

---

### 7. 健康检查 (sqleng/handler_checkhealth.go)

#### 7.1 CheckHealth() 主函数

```go
func (e *DataSourceHandler) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
    // 使用 Ping 验证连接
    err := e.db.Ping()
    if err != nil {
        logCheckHealthError(ctx, e.dsInfo, err)

        // 管理员可以看到详细错误
        if strings.EqualFold(req.PluginContext.User.Role, "Admin") {
            return ErrToHealthCheckResult(err)
        }

        // 非管理员只能看到通用错误
        var driverErr *mysql.MySQLError
        if errors.As(err, &driverErr) {
            return &backend.CheckHealthResult{Status: backend.HealthStatusError,
                Message: e.TransformQueryError(e.log, driverErr).Error()}, nil
        }
        return &backend.CheckHealthResult{Status: backend.HealthStatusError,
            Message: e.TransformQueryError(e.log, err).Error()}, nil
    }
    return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "Database Connection OK"}, nil
}
```

#### 7.2 ErrToHealthCheckResult() 错误转换

```go
func ErrToHealthCheckResult(err error) (*backend.CheckHealthResult, error) {
    res := &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: err.Error()}
    details := map[string]string{}

    // 网络错误处理
    var opErr *net.OpError
    if errors.As(err, &opErr) {
        res.Message = "Network error: Failed to connect to the server"
        if opErr.Err != nil {
            errMessage := opErr.Err.Error()
            // 提取并格式化常见错误
            if strings.HasSuffix(errMessage, "no such host") {
                errMessage = "no such host"
            }
            if strings.HasSuffix(errMessage, "unknown port") {
                errMessage = "unknown port"
            }
            if strings.HasSuffix(errMessage, "invalid port") {
                errMessage = "invalid port"
            }
            res.Message += fmt.Sprintf(". Error message: %s", errMessage)
        }
        details["verboseMessage"] = err.Error()
        details["errorDetailsLink"] = "https://grafana.com/docs/grafana/latest/datasources/mysql/#configure-the-data-source"
    }

    // MySQL 错误处理
    var driverErr *mysql.MySQLError
    if errors.As(err, &driverErr) {
        res.Message = "Database error: Failed to connect to the MySQL server"
        if driverErr.Number > 0 {
            res.Message += fmt.Sprintf(". MySQL error number: %d", driverErr.Number)
        }
        details["verboseMessage"] = err.Error()
        details["errorDetailsLink"] = "https://dev.mysql.com/doc/mysql-errors/8.4/en/"
    }

    detailBytes, _ := json.Marshal(details)
    res.JSONDetails = detailBytes
    return res, nil
}
```

#### 7.3 健康检查错误日志

```go
func logCheckHealthError(ctx context.Context, dsInfo DataSourceInfo, err error) {
    logger := log.DefaultLogger.FromContext(ctx)

    // 记录脱敏的配置信息（只记录长度，不记录实际值）
    configSummary := map[string]any{
        "config_url_length":                 len(dsInfo.URL),
        "config_user_length":                len(dsInfo.User),
        "config_database_length":            len(dsInfo.Database),
        "config_max_open_conns":             dsInfo.JsonData.MaxOpenConns,
        "config_max_idle_conns":             dsInfo.JsonData.MaxIdleConns,
        "config_conn_max_life_time":         dsInfo.JsonData.ConnMaxLifetime,
        "config_timezone":                   dsInfo.JsonData.Timezone,
        "config_enable_secure_proxy":        dsInfo.JsonData.SecureDSProxy,
        "config_allow_clear_text_passwords": dsInfo.JsonData.AllowCleartextPasswords,
        // ... 更多配置
        "config_password_length":            len(dsInfo.DecryptedSecureJSONData["password"]),
        // ...证书长度
    }

    logger.Error("Check health failed", "error", err,
        "message_type", "ds_config_health_check_error_detailed",
        "details", string(configSummaryJson))
}
```

---

### 8. 独立插件运行 (standalone/main.go)

```go
func main() {
    logger := backend.NewLoggerWith("logger", "tsdb.mysql")

    // 数据源管理器会自动管理实例的生命周期
    // NewInstanceSettings 工厂函数会在需要时创建实例
    // 配置变更时会调用 Dispose 并创建新实例
    if err := datasource.Manage("mysql", mysql.NewInstanceSettings(logger), datasource.ManageOpts{}); err != nil {
        log.DefaultLogger.Error(err.Error())
        os.Exit(1)
    }
}
```

**工作模式：**
1插件运行时等待 Grafana 的请求
2. 每个数据源 ID 有独立的实例
3. 支持热重载配置

---

## 查询流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Grafana 前端                                │
└───────────────────────────┬─────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     Service.QueryData()                             │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  for each query:                                             │   │
│  │    1. 解析 query.JSON (rawSql, format)                       │   │
│  │    2. 验证 fill 参数                                         │   │
│  │    3. 启动 goroutine 执行查询                                │   │
│  └─────────────────────────────────────────────────────────────┘   │
└───────────────────────────┬─────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│                 DataSourceHandler.executeQuery()                    │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  1. 全局宏替换                                               │   │
│  │     - $__interval_ms → "1000"                               │   │
│  │     - $__interval → "1s"                                    │   │
│  │     - $__unixEpochFrom() → "1521118500"                     │   │
│  │     - $__unixEpochTo() → "1521118800"                       │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  2. MySQL 宏替换                                            │   │
│  │     - 安全检查（禁止 user(), current_user() 等）            │   │
│  │     - __time(col) → UNIX_TIMESTAMP(col) as time_sec         │   │
│  │     - __timeFilter(col) → col BETWEEN ... AND ...           │   │
│  │     - __timeGroup(col, '1m') → UNIX_TIMESTAMP(col) DIV 60*60│   │
│  │     - 设置填充模式 (fill, fillMode, fillValue)             │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  3. 执行 SQL 查询                                           │   │
│  │     - db.QueryContext(ctx, interpolatedQuery)               │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  4. 转换结果到 Frame                                        │   │
│  │     - FrameFromRows(rows, rowLimit, converters)             │   │
│  │     - 类型转换 (MySQL 类型 → Grafana 类型)                  │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  5. 处理时间列                                               │   │
│  │     - convertSQLTimeColumnsToEpochMS()                      │   │
│  │     - epoch 精度调整 (秒/毫秒/纳秒 → 毫秒)                   │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  6. 时间序列格式处理 (format="time_series")                 │   │
│  │     a. 验证存在时间列                                        │   │
│  │     b. 重命名时间列为 "Time"                                │   │
│  │     c. 转换数值列为 Float64                                 │   │
│  │     d. Long → Wide 格式转换                                 │   │
│  │     e. 缺失数据填充                                         │   │
│  └─────────────────────────────────────────────────────────────┘   │
└───────────────────────────┬─────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         返回 QueryDataResponse                       │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  Responses: map[RefID]DataResponse                          │   │
│  │    ├── RefID = "A"                                          │   │
│  │    │   └── DataResponse                                     │   │
│  │    │       ├── Frames: []*data.Frame                        │   │
│  │    │       │   └── Frame                                    │   │
│  │    │       │       ├── Fields: []*data.Field                │   │
│  │    │       │       │   ├── Field 0: Time                   │   │
│  │    │       │       │   ├── Field 1: Value (metric A)       │   │
│  │    │       │       │   └── Field 2: Value (metric B)       │   │
│  │    │       │       └── Meta: *data.FrameMeta               │   │
│  │    │       │           └── ExecutedQueryString              │   │
│  │    │       └── Error: error (if any)                        │   │
│  │    └── RefID = "B" ...                                      │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 数据类型转换流程

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────────┐
│   MySQL 类型     │    │   Driver 扫描    │    │   Grafana 类型      │
├─────────────────┤    ├──────────────────┤    ├─────────────────────┤
│ DOUBLE          │───▶│ string           │───▶│ NullableFloat64     │
│ FLOAT           │───▶│ string           │───▶│ NullableFloat64     │
│ DECIMAL         │───▶│ string           │───▶│ NullableFloat64     │
├─────────────────┤    ├──────────────────┤    ├─────────────────────┤
│ BIGINT          │───▶│ string           │───▶│ NullableInt64       │
│ INT             │───▶│ string           │───▶│ NullableInt64       │
│ SMALLINT        │───▶│ string           │───▶│ NullableInt64       │
│ TINYINT         │───▶│ string           │───▶│ NullableInt64       │
│ YEAR            │───▶│ string           │───▶│ NullableInt64       │
├─────────────────┤    ├──────────────────┤    ├─────────────────────┤
│ DATETIME        │───▶│ string           │───▶│ NullableTime        │
│ DATE            │───▶│ string           │───▶│ NullableTime        │
│ TIMESTAMP       │───▶│ string           │───▶│ NullableTime        │
├─────────────────┤    ├──────────────────┤    ├─────────────────────┤
│ CHAR/VARCHAR    │───▶│ string           │───▶│ NullableString      │
│ TEXT 系列类型    │───▶│ string           │───▶│ NullableString      │
│ BLOB 系列类型    │───▶│ []byte / string  │───▶│ NullableString      │
└─────────────────┘    └──────────────────┘    └─────────────────────┘
```

---

## 时间序列格式转换

### Long 格式 (长格式)

```
时间列的格式:    指标列      数值列
┌─────────┬─────────────┬─────────┐
│  Time   │   metric    │  value  │
├─────────┼─────────────┼─────────┤
│  10:00  │  metric_A   │   100   │
│  10:00  │  metric_B   │   200   │
│  10:10  │  metric_A   │   110   │
│  10:10  │  metric_B   │   210   │
└─────────┴─────────────┴─────────┘
```

### Wide 格式 (宽格式)

```
时间列的格式:     metric_A    metric_B    ...
┌─────────┬─────────────┬─────────────┬─────┐
│  Time   │ metric_A    │ metric_B    │ ... │
├─────────┼─────────────┼─────────────┼─────┤
│  10:00  │    100      │    200      │ ... │
│  10:10  │    110      │    210      │ ... │
└─────────┴─────────────┴─────────────┴─────┘
```

**转换逻辑：**
```go
// Long → Wide 转换
if tsSchema.Type == data.TimeSeriesTypeLong {
    frame, err = data.LongToWide(frame, qm.FillMissing)

    // 处理 v7 兼容：将 metric 标签移入字段名
    if len(originalData.Fields) == 3 {
        for _, field := range frame.Fields {
            if len(field.Labels) == 1 {
                if name, ok := field.Labels["metric"]; ok {
                    field.Name = name  // 字段名 = metric 值
                    field.Labels = nil
                }
            }
        }
    }
}
```

---

## 使用示例

### 示例 1: 基本时间序列查询

```sql
SELECT
  $__time(created_at) as time,
  avg(value) as value
FROM metrics
WHERE $__timeFilter(created_at)
GROUP BY $__timeGroup(created_at, $__interval)
ORDER BY $__timeGroup(created_at, $__interval)
```

**转换后的 SQL（假设时间范围 2024-01-01 10:00 到 10:30，间隔 5m）：**
```sql
SELECT
  UNIX_TIMESTAMP(created_at) as time,
  avg(value) as value
FROM metrics
WHERE created_at BETWEEN FROM_UNIXTIME(1704105600) AND FROM_UNIXTIME(1704107400)
GROUP BY UNIX_TIMESTAMP(created_at) DIV 300 * 300
ORDER BY UNIX_TIMESTAMP(created_at) DIV 300 * 300
```

### 示例 2: 多系列时间序列查询（使用 metric 列）

```sql
SELECT
  $__time(created_at) as time,
  measurement as metric,
  value
FROM metrics
WHERE $__timeFilter(created_at)
ORDER BY 1, 2
```

**结果格式：**
- Long 格式转换为 Wide 格式
- 每个唯一的 measurement 值生成一个字段
- 字段名 = measurement 的值

### 示例 3: 带填充的时间序列查询

```sql
SELECT
  $__timeGroup(time, $__interval, '0') as time,
  avg(value) as value
FROM metrics
WHERE $__timeFilter(time)
GROUP BY 1
ORDER BY 1
```

**填充行为：**
- 缺失的时间桶会被填充
- 填充值 = 0

### 示例 4: 表格查询

```sql
SELECT
  id,
  name,
  created_at as time
FROM users
WHERE $__timeFilter(created_at)
ORDER BY created_at DESC
LIMIT 100
```

### 示例 5: 使用 Unix 时间戳列

```sql
SELECT
  $__unixEpochGroup(time_sec, $__interval, 'previous') as time,
  avg(value) as value
FROM metrics
WHERE $__unixEpochFilter(time_sec)
GROUP BY 1
ORDER BY 1
```

---

## 安全机制

### 1. 查询宏安全检查

```go
var restrictedRegExp = regexp.MustCompile(
    `(?im)([\s]*show[\s]+grants|[\s,]session_user\([^\)]*\)|[\s,]current_user(\([^\)]*\))?|[\s,]system_user\([^\)]*\)|[\s,]user\([^\)]*\))([\s,;]|$)`
)
```

**防护目的：** 防止通过 SQL 注入获取数据库用户信息

### 2. 错误消息脱敏

```go
// 非管理员用户只能看到通用错误
if driverErr.Number != mysqlerr.ER_PARSE_ERROR &&
    driverErr.Number != mysqlerr.ER_BAD_FIELD_ERROR &&
    driverErr.Number != mysqlerr.ER_NO_SUCH_TABLE {
    return fmt.Errorf("query failed - %s", t.userError)  // 通用错误
}
```

**安全目的：** 不泄露服务器配置和内部结构信息

### 3. 健康检查日志脱敏

```go
configSummary := map[string]any{
    "config_url_length":      len(dsInfo.URL),          // 只记录长度
    "config_user_length":     len(dsInfo.User),         // 只记录长度
    "config_password_length": len(dsInfo.DecryptedSecureJSONData["password"]),
    // ... 不记录实际值
}
```

**安全目的：** 防止敏感信息被记录到日志

---

## 测试覆盖

### 单元测试 (macros_test.go)

- 宏替换功能测试
- 安全限制测试（禁止的函数）
- 并发安全性测试

### 集成测试 (mysql_test.go)

需要 MySQL 测试数据库：
```bash
GRAFANA_TEST_DB=mysql go test -v ./pkg/tsdb/mysql
```

测试覆盖：
- 不同 MySQL 数据类型的转换
- 时间序列查询（各种格式）
- 填充模式（NULL、previous、value）
- 多系列查询
- 时间列精度处理
- 行数限制

### 快照测试 (mysql_snapshot_test.go)

使用 golden file 模式：
- SQL 查询结果与预期结果对比
- 支持更新 golden 文件 (`updateGoldenFiles = true`)

---

## 依赖包

```
github.com/VividCortex/mysqlerr       # MySQL 错误码常量
github.com/go-sql-driver/mysql        # MySQL 驱动
github.com/grafana/grafana-plugin-sdk-go  # Grafana 插件 SDK
  ├── backend                         # 后端接口
  ├── data                            # 数据结构和类型
  └── data/sqlutil                    # SQL 工具函数
golang.org/x/net/proxy                # SOCKS 代理支持
github.com/stretchr/testify           # 测试框架
```

---

## 配置字段说明

### JsonData 结构

```go
type JsonData struct {
    // 连接池配置
    MaxOpenConns            int    // 最大打开连接数
    MaxIdleConns            int    // 最大空闲连接数
    ConnMaxLifetime         int    // 连接最大生命周期（秒）
    ConnectionTimeout       int    // 连接超时

    // TLS/SSL 配置
    Mode                    string // SSL 模式
    ConfigurationMethod     string // TLS 配置方法
    TlsSkipVerify           bool   // 跳过 TLS 验证
    RootCertFile            string // CA 证书文件路径
    CertFile                string // 客户端证书文件路径
    CertKeyFile             string // 客户端私钥文件路径

    // 其他配置
    Timezone                string // 数据库时区
    TimeInterval            string // 默认时间间隔
    Database                string // 数据库名称（覆盖级别配置）
    SecureDSProxy           bool   // 启用安全 SOCKS 代理
    AllowCleartextPasswords bool   // 允许明文密码
    AuthenticationType      string // 认证类型
}
```

### DecryptedSecureJSONData

```go
DecryptedSecureJSONData: map[string]string{
    "password":        "用户密码",
    "tlsCACert":       "CA 证书内容",
    "tlsClientCert":   "客户端证书内容",
    "tlsClientKey":    "客户端私钥内容",
}
```
