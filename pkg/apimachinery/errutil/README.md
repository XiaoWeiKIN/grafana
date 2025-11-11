# errutil - Grafana 标准化错误处理包

## 概述

`errutil` 是 Grafana 的统一错误处理工具包，为开发者、系统管理员和最终用户提供结构化的错误信息管理。它将静态错误信息（错误类别、状态码、日志级别）与动态错误信息（具体上下文、错误原因）相结合，实现标准化的错误处理流程。

## 核心特性

- **静态与动态信息分离**：通过 `Base` 定义错误类别，通过 `Error` 携带运行时信息
- **自动状态码映射**：自动将错误映射到 HTTP 状态码和 Kubernetes API 状态
- **公私信息隔离**：日志信息可包含敏感数据，公开信息只展示给最终用户
- **智能日志级别**：根据错误类型自动设置合适的日志级别
- **错误源追踪**：区分服务器内部错误和下游服务错误
- **模板化消息**：支持 Go template 构建动态错误消息，便于国际化
- **标准兼容**：兼容 Go 1.13+ 错误处理（`Unwrap`、`Is`）和 Kubernetes `APIStatus` 接口

## 核心类型

### Base

静态错误基础类型，包含错误类别的通用信息：

- `reason`：状态原因（如 `StatusNotFound`、`StatusInternal`）
- `messageID`：唯一的错误消息标识符
- `publicMessage`：面向用户的公开消息
- `logLevel`：建议的日志级别
- `source`：错误来源（服务器或下游）

### Error

具体错误实例，包含静态和动态信息：

- `Reason`：错误状态原因
- `MessageID`：错误消息 ID
- `LogMessage`：服务器日志消息（可包含敏感信息）
- `PublicMessage`：用户可见的公开消息
- `PublicPayload`：用于国际化的结构化数据
- `Underlying`：底层包装的错误
- `LogLevel`：建议的日志级别
- `Source`：错误来源

### Template

支持模板化的错误构建器，用于动态生成错误消息。

## 使用方法

### 基础用法

```go
package myservice

import "github.com/grafana/grafana/pkg/apimachinery/errutil"

// 在包级别定义错误基础类型
var (
    errNotFound = errutil.NotFound("user.notFound")
    errInvalidInput = errutil.BadRequest("user.invalidInput")
    errInternal = errutil.Internal("user.internalError")
)

func GetUser(userID string) (*User, error) {
    user, err := db.FindUser(userID)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            // 返回标准化的 404 错误
            return nil, errNotFound.Errorf("user with ID %s not found", userID)
        }
        // 返回标准化的 500 错误并包装原始错误
        return nil, errInternal.Errorf("failed to query user: %w", err)
    }
    return user, nil
}
```

### 自定义公开消息

```go
var errNotFound = errutil.NotFound(
    "service.notFound",
    errutil.WithPublicMessage("请求的资源不存在"),
)
```

### 使用模板

```go
var errRateLimited = errutil.TooManyRequests("service.rateLimited").MustTemplate(
    "用户 {{ .Private.userID }} 达到速率限制，请在 {{ .Public.retryAfter }} 后重试",
    errutil.WithPublic("请求过于频繁，请在 {{ .Public.retryAfter }} 后重试"),
)

// 使用模板构建错误
err := errRateLimited.Build(errutil.TemplateData{
    Private: map[string]interface{}{
        "userID": userID,
    },
    Public: map[string]interface{}{
        "retryAfter": time.Now().Add(time.Minute).Format(time.RFC3339),
    },
})
```

### 错误检查

```go
// 使用 errors.Is 检查错误类型
if errors.Is(err, errNotFound) {
    // 处理 404 错误
}

// 使用 errors.As 提取错误信息
var e errutil.Error
if errors.As(err, &e) {
    fmt.Println("状态码:", e.Reason.Status().HTTPStatus())
    fmt.Println("消息 ID:", e.MessageID)
    fmt.Println("日志消息:", e.LogMessage)
}
```

### 获取公开错误信息

```go
var e errutil.Error
if errors.As(err, &e) {
    // 转换为公开错误，可安全返回给客户端
    publicErr := e.Public()
    
    // publicErr 包含：
    // - StatusCode: HTTP 状态码
    // - MessageID: 错误消息 ID
    // - Message: 公开消息
    // - Extra: 公开的附加数据
    
    json.NewEncoder(w).Encode(publicErr)
}
```

## 预定义错误类型

| 函数 | 状态原因 | HTTP 状态码 | 使用场景 |
|------|----------|-------------|----------|
| `NotFound` | StatusNotFound | 404 | 资源不存在 |
| `BadRequest` | StatusBadRequest | 400 | 请求参数错误 |
| `ValidationFailed` | StatusValidationFailed | 400 | 数据验证失败 |
| `Unauthorized` | StatusUnauthorized | 401 | 未认证 |
| `Forbidden` | StatusForbidden | 403 | 无权限 |
| `Conflict` | StatusConflict | 409 | 资源冲突 |
| `TooManyRequests` | StatusTooManyRequests | 429 | 请求过于频繁 |
| `Internal` | StatusInternal | 500 | 服务器内部错误 |
| `NotImplemented` | StatusNotImplemented | 501 | 功能未实现 |
| `BadGateway` | StatusBadGateway | 502 | 下游服务错误 |
| `GatewayTimeout` | StatusGatewayTimeout | 504 | 下游服务超时 |
| `Timeout` | StatusTimeout | 504 | 请求超时 |

## 日志级别

不同错误类型自动映射到相应的日志级别：

- `LevelError`：内部错误（500）、未知错误
- `LevelInfo`：客户端错误（400、401、403、404、409、422、429 等）
- `LevelDebug`：功能未实现（501）
- `LevelNever`：不记录日志

可通过 `WithLogLevel` 选项自定义：

```go
var errDebug = errutil.Internal(
    "debug.error",
    errutil.WithLogLevel(errutil.LevelDebug),
)
```

## 错误源

- `SourceServer`：错误源于服务器内部（默认）
- `SourceDownstream`：错误源于下游服务（`BadGateway` 和 `GatewayTimeout` 自动设置）

可通过 `WithDownstream()` 选项设置：

```go
var errDownstream = errutil.Internal(
    "proxy.error",
    errutil.WithDownstream(),
)
```

## 完整示例

```go
package shorturl

import (
    "errors"
    "path"
    "strings"
    
    "github.com/grafana/grafana/pkg/apimachinery/errutil"
)

var (
    errAbsPath     = errutil.BadRequest("shorturl.absolutePath")
    errInvalidPath = errutil.BadRequest("shorturl.invalidPath")
    errUnexpected  = errutil.Internal("shorturl.unexpected")
)

func CreateShortURL(longURL string) (string, error) {
    if path.IsAbs(longURL) {
        return "", errAbsPath.Errorf("unexpected absolute path")
    }
    if strings.Contains(longURL, "../") {
        return "", errInvalidPath.Errorf("path mustn't contain '..': '%s'", longURL)
    }
    if strings.Contains(longURL, "@") {
        return "", errInvalidPath.Errorf("cannot shorten email addresses")
    }

    shortURL, err := createShortURL(longURL)
    if err != nil {
        return "", errUnexpected.Errorf("failed to create short URL: %w", err)
    }

    return shortURL, nil
}
```

## 最佳实践

1. **在包级别定义错误**：将 `Base` 定义为包级别变量，便于重用和维护
2. **使用描述性的 MessageID**：遵循 `component.errorBrief` 格式，如 `user.notFound`、`dashboard.validationError`
3. **包装底层错误**：使用 `%w` 包装原始错误，保留错误链
4. **区分日志和公开消息**：敏感信息只放在 `LogMessage`，`PublicMessage` 只包含用户需要的信息
5. **合理选择错误类型**：根据实际场景选择合适的状态码，避免滥用 500
6. **使用模板处理复杂场景**：当需要动态消息或国际化支持时使用 `Template`

## 与标准库的兼容性

`errutil.Error` 完全兼容 Go 标准库的错误处理：

```go
// 实现 error 接口
func (e Error) Error() string

// 支持 errors.Unwrap
func (e Error) Unwrap() error

// 支持 errors.Is
func (e Error) Is(target error) bool
```

## 与 Kubernetes 的集成

`errutil.Error` 实现了 Kubernetes `APIStatus` 接口，可直接用于 K8s API Server：

```go
// 实现 k8s.io/apimachinery/pkg/api/errors.APIStatus
func (e Error) Status() metav1.Status
```

## 注意事项

- `Error` 不能直接序列化为 JSON，必须先调用 `Public()` 转换为 `PublicError`
- `Base` 的成员是私有的，应通过 `NewBase` 或预定义函数创建
- `MustTemplate` 会在模板编译失败时 panic，仅用于包级别初始化
- `MessageID` 应保持唯一性，建议使用 `组件.错误类型` 的命名规范