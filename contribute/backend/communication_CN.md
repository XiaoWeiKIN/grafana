# 通信机制

Grafana 使用依赖注入和 Go 接口方法调用来实现后端不同组件之间的通信。

## 命令与查询

Grafana 使用"命令/查询"分离模式来组织[服务](services.md)的参数，其中命令（Command）是修改数据的指令，查询（Query）是从服务获取记录。

服务应按以下方式定义方法：

- `func[T, U any](ctx context.Context, args T) (U, error)`

每个函数应接受两个参数。第一个是 `context.Context`，用于传递链路追踪 span、取消信号及其他与调用相关的运行时信息。第二个是 `T`，一个在服务根包中定义的结构体。请参阅[包层级结构](package-hierarchy.md)的说明，该结构体包含零个或多个可传递给方法的参数。

返回值更加灵活，可以是零个、一个或两个值。如果函数返回两个值，第二个值应该是 `bool` 或 `error`，用于表示调用成功或失败。第一个值 `U` 携带适合该服务的任意导出类型的值。

以下示例展示了一个遵循这些准则的接口方法签名：

```go
type Alphabetical interface {
  // GetLetter 返回错误或字母
  GetLetter(context.Context, GetLetterQuery) (Letter, error)
  // ListCachedLetters 不会失败，不返回错误
  ListCachedLetters(context.Context, ListCachedLettersQuery) Letters
  // DeleteLetter 除了错误外没有其他返回值，因此只返回错误
  DeleteLetter(context.Contxt, DeleteLetterCommand) error
}
```

> **注意：** 因为我们请求执行某个操作，命令使用祈使语气编写，例如 `CreateFolderCommand`、`GetDashboardQuery` 和 `DeletePlaylistCommand`。

在 Go 中使用复杂类型作为参数意味着几个不同的事情。最重要的是，它为我们提供了其他语言中命名参数的等效功能，并减少了在三个或更多参数时经常出现的参数混淆问题。

然而，这意味着所有输入参数都是可选的，开发人员需要确保所有字段的零值是有用的或至少是安全的。此外，虽然添加另一个字段很容易，但该字段必须被正确设置才能使服务正常运行，而这在编译时是无法检测到的。

### 带有 Result 字段的查询

某些查询有一个 `Result` 字段，该字段由被调用的方法修改和填充。这是当 `_bus_` 用于发送命令、查询和事件时遗留下来的。

所有总线命令和查询都必须实现 Go 类型 `func(ctx context.Context, msg interface{}) error`，修改 `msg` 变量或在 `error` 中返回结构化信息是与调用者通信的两种最方便的方式。

你应该重构所有 `Result` 字段，使其从查询方法中返回。例如：

```go
type GetQuery struct {
  Something int

  Result ResultType
}

func (s *Service) Get(ctx context.Context, cmd *GetQuery) error {
  // ...执行某些操作
  cmd.Result = result
  return nil
}
```

应该变为

```go
type GetQuery struct {
  Something int
}

func (s *Service) Get(ctx context.Context, cmd GetQuery) (ResultType, error) {
  // ...执行某些操作
  return result, nil
}
```

## 事件

_事件_ 是过去发生的事情。由于事件已经发生，你无法改变它。但是，你可以通过在事件发生时触发额外的应用逻辑来响应事件。

> **注意：** 因为事件发生在过去，它们的名称使用过去时态编写，例如 `UserCreated` 和 `OrgUpdated`。

### 订阅事件

要响应事件，你首先需要 _订阅_ 它。

要订阅事件，请在服务的 `Init` 方法中注册 _事件监听器_：

```go
func (s *MyService) Init() error {
    s.bus.AddEventListener(s.UserCreated)
    return nil
}

func (s *MyService) UserCreated(event *events.UserCreated) error {
    // ...
}
```

> **提示：** 要了解可用的事件，请参阅 `events` 包中的文档。

### 发布事件

如果你想让应用程序的其他部分响应服务中的变化，可以发布自己的事件。例如：

```go
event := &events.StickersSentEvent {
    UserID: "taylor",
    Count:   1,
}
if err := s.bus.Publish(event); err != nil {
    return err
}
```
