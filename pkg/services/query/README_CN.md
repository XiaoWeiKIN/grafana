# Grafana 查询服务 (Query Service)

本文档详细解读 `pkg/services/query` 包的核心逻辑。

## 🔌 查询执行客户端

在 `pkg/services/query/query.go` 的 `handleQuerySingleDatasource` 方法中，Query Service 会根据配置决定使用哪种客户端来执行数据源查询。这里涉及两个核心组件：

### 1. `pluginClient` (plugins.Client)
*   **字段**: `pluginClient`
*   **模式**: **传统/本地模式 (Single Tenant Flow)**
*   **机制**: 这是 Grafana 最标准的插件调用方式。Grafana Server 进程通过 gRPC 直接与部署在本地（或 sidecar）的插件后端进程通信。
*   **场景**: 开源版 (OSS) 和大多数自托管环境默认使用此方式。
*   **代码行为**: 当 `qsDatasourceClientBuilder.BuildClient` 返回 `ok=false` 时（默认情况），代码会回退到使用 `s.pluginClient.QueryData`。

### 2. `qsDsClient` (Query Service Datasource Client)
*   **字段**: 由 `qsDatasourceClientBuilder` 构建
*   **模式**: **查询服务模式 (Query Service Flow)**
*   **机制**: 这是一个为了更复杂的分布式架构（如 Grafana Cloud 或企业版大规模部署）设计的抽象。它允许将“查询执行”这一繁重任务从 Grafana 主服务中剥离出来，委托给一个独立的、专门的“查询服务 (Query Service)”集群（通常运行在 K8s 上）。
*   **场景**: 适用于需要将查询负载隔离、实现多租户（Multi-tenancy）或大规模扩展的场景。
*   **默认行为**: 在标准的 OSS 构建中，默认注入的是 `NewNullQSDatasourceClientBuilder`，它总是返回 `false`，因此默认情况下代码总是会走 `pluginClient` 的逻辑。

### 总结比较

| 特性 | pluginClient | qsDsClient |
| :--- | :--- | :--- |
| **执行位置** | 本地插件进程 | 远程 Query Service 微服务 |
| **适用场景** | 单机/OSS/常规部署 | Grafana Cloud/大规模集群 |
| **通信方式** | 本地 gRPC | 远程 API (通常是 K8s API 或 HTTP) |
| **默认开启** | ✅ 是 | ❌ 否 |
