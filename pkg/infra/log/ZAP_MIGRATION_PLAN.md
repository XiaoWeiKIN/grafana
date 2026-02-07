# 可行性研究：使用 Zap 替换 Grafana 日志方案

直接回答：**是可以替换的**，且这是一个非常合理的现代化改造方向。

但是，由于 Grafana 的 `log.Logger` 接口被全库 5000+ 文件引用，直接修改调用处（Call Sites）是不现实的。最稳妥的方案是采用 **适配器模式（Adapter Pattern）**：保留 `pkg/infra/log` 的接口定义，但在底层将实现替换为 `zap`。

## 1. 核心替换方案

我们不需要修改业务代码中的 `log.New("name")` 或 `logger.Info(...)`，而是重写 `pkg/infra/log/log.go` 中的 `ConcreteLogger`，让它持有一个 `*zap.SugaredLogger`。

### 1.1 代码实现预览

你需要创建一个新的结构体来适配 Zap：

```go
// pkg/infra/log/zap_adapter.go

import (
    "go.uber.org/zap"
)

type ConcreteLogger struct {
    // 替换原本的 gokitlog.SwapLogger
    zapLogger *zap.SugaredLogger
}

// 适配 New 方法：Grafana 的 New 是创建带有 Context 的子 Logger
func (cl *ConcreteLogger) New(ctx ...any) *ConcreteLogger {
    // Zap 的 With 刚好对应这个功能
    newZap := cl.zapLogger.With(ctx...)
    return &ConcreteLogger{zapLogger: newZap}
}

// 适配 Debug/Info/Warn/Error
func (cl *ConcreteLogger) Info(msg string, ctx ...any) {
    // Zap 的 SugaredLogger 支持这种 key-value 变长参数
    cl.zapLogger.Infow(msg, ctx...)
}

func (cl *ConcreteLogger) Error(msg string, ctx ...any) {
    cl.zapLogger.Errorw(msg, ctx...)
}

// 关键点：Caller Skip
// 因为封装了一层，需要跳过调用栈，否则日志行号会显示在这个文件里
func NewZapLogger(core zap.Core) *ConcreteLogger {
    l := zap.New(core, zap.AddCallerSkip(1)) 
    return &ConcreteLogger{zapLogger: l.Sugar()}
}
```

## 2. 挑战与解决方案

### 2.1 性能 vs 接口兼容性
`zap` 最大的优势是 `zap.String("key", "val")` 这种**强类型**带来的零内存分配。
但是，Grafana 的接口是 `ctx ...any`（`interface{}`）。
*   **妥协**：我们只能使用 `zap.SugaredLogger`，它内部还是要进行反射（Reflection）。
*   **结论**：性能会比 Grafana 原生的 `go-kit/log` 略好（Zap 的序列化通常更快），但达不到 Zap 最佳性能。不过这通常已经足够了。

### 2.2 配置迁移 (ini -> Zap Config)
Grafana 目前使用 `ini` 文件配置输出（console, file, syslog）。
*   **Console**: 对应 `zapcore.NewConsoleEncoder`。
*   **File**: Zap 原生不支持文件轮转。你需要引入 `gopkg.in/natefinch/lumberjack.v2` 作为 `zapcore.WriteSyncer` 来实现同样的 `MaxLines`/`MaxSize` 轮转逻辑。

### 2.3 上下文 (Context)
Grafana 有一个 `FromContext` 方法用于从 `context.Context` 中提取 traceID 等信息。
*   你需要保留 `log.go` 中的 `ContextualLogProviderFunc` 逻辑，在创建 Logger 时提取这些字段传递给 `zap.With()`。

## 3. 实施步骤

1.  **引入依赖**：`go get -u go.uber.org/zap gopkg.in/natefinch/lumberjack.v2`
2.  **重构接口实现**：
    *   修改 `pkg/infra/log/log.go`。
    *   删除对 `github.com/go-kit/log` 的依赖。
    *   将 `ConcreteLogger` 的字段改为 `*zap.SugaredLogger`。
3.  **重写 LogManager**：
    *   在 `ReadLoggingConfig` 中，不再创建 `FileLogWriter`（Grafana 自实现的），而是初始化 `lumberjack.Logger` 并传给 `zapcore.AddSync`。
4.  **验证**：
    *   运行 `pkg/infra/log` 下的单元测试（需要大幅修改测试代码，因为测试可能依赖了具体实现）。
    *   启动 Grafana，检查日志格式是否和以前一致。

## 4. 结论

**可以使用 Zap 替换。**

**优点**：
*   **生态更好**：Zap 是 Go 社区标准，插件和中间件更多。
*   **维护性**：甩掉了 Grafana 自己维护的复杂的 `FileLogWriter` 代码。
*   **性能潜力**：未来新的模块可以直接 assert 成 `*zap.Logger` 使用高性能 API。

**缺点**：
*   **工作量大**：虽然不用改业务代码，但 `pkg/infra/log` 内部逻辑（尤其配置加载部分）需要重写。
*   **并不是“零分配”**：为了兼容旧接口，必须用 `SugaredLogger`，牺牲了 Zap 的部分性能优势。
