# 前端数据请求

[BackendSrv](https://github.com/grafana/grafana/blob/main/packages/grafana-runtime/src/services/backendSrv.ts) 处理 Grafana 的所有出站 HTTP 请求。本文档解释了 `BackendSrv` 使用的高级概念。

## 取消请求

虽然数据源可以实现自己的取消概念，但我们建议你使用本节描述的方法。

数据请求可能需要很长时间才能完成。在请求开始和完成之间的时间内，用户可能会改变上下文。例如，用户可能会导航离开或再次发出相同的请求。

如果我们等待已取消的请求完成，它们可能会对数据源造成不必要的负载。

### 按 Grafana 版本划分的请求取消

Grafana 使用一种称为 _请求取消_ 的概念来取消 Grafana 不再需要的任何正在进行的请求。以这种方式取消请求的过程因 Grafana 版本而异。

#### Grafana 7.2 之前

在 Grafana 可以取消任何数据请求之前，它必须识别该请求。当你使用 [BackendSrv](https://github.com/grafana/grafana/blob/main/packages/grafana-runtime/src/services/backendSrv.ts) 时，Grafana 使用[作为选项传递](https://github.com/grafana/grafana/blob/main/packages/grafana-runtime/src/services/backendSrv.ts#L47)的 `requestId` 属性来识别请求。

取消逻辑如下：

- 当正在进行的请求发现另一个具有相同 `requestId` 的请求已启动时，Grafana 将取消正在进行的请求。
- 当正在进行的请求发现发送了特殊的"取消所有请求" `requestId` 时，Grafana 将取消正在进行的请求。

#### Grafana 7.2 之后

Grafana 7.2 引入了一种使用 [RxJs](https://github.com/ReactiveX/rxjs) 取消请求的额外方式。为了支持新的取消功能，数据源需要使用 [BackendSrv](https://github.com/grafana/grafana/blob/main/packages/grafana-runtime/src/services/backendSrv.ts) 中新的 `fetch` 函数。

将核心数据源迁移到新的 `fetch` 函数是一个持续的过程。要了解更多信息，请参阅[此 issue](https://github.com/grafana/grafana/issues/27222)。

## 请求队列

如果 Grafana 未配置支持 HTTP/2，使用 HTTP 1.1 连接的浏览器会强制限制 4 到 8 个并行请求（具体限制因浏览器而异）。由于此限制，如果某些请求需要很长时间，它们将阻塞后续请求，使与 Grafana 的交互变得非常缓慢。

[在 Grafana 中启用 HTTP/2 支持](https://grafana.com/docs/grafana/latest/administration/configuration/#protocol)允许更多的并行请求。

### Grafana 7.2 之前

不支持。

### Grafana 7.2 之后

Grafana 使用 _请求队列_ 按顺序处理所有传入的数据请求，同时为 Grafana API 的任何请求保留一个空闲"位置"。

由于请求队列的第一个实现没有考虑用户使用的浏览器，请求队列对并行数据源请求的限制硬编码为 5。

> **注意：** [配置了 HTTP/2](https://grafana.com/docs/grafana/latest/administration/configuration/#protocol) 的 Grafana 实例硬编码限制为 1000。
