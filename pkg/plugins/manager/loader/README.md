# Plugin Loader

The `loader` package is responsible for the complete lifecycle of loading plugins from a source (like the file system) into the Grafana system. It orchestrates a sophisticated pipeline that transforms raw files into initialized `plugins.Plugin` objects.

## Responsibilities

The `Loader` coordinates the following stages for each plugin source:

1.  **Discovery**: Scans the provided source (e.g., directory paths) to find potential plugin bundles.
2.  **Bootstrap**: Reads the basic plugin metadata (`plugin.json`), creating primary and secondary plugin bundles.
3.  **Validation**: Ensures the plugin is valid (e.g., signature verification, structure checks).
4.  **Initialization**: Performs final setup of the plugin object, preparing it for registration.

## Architecture

The `Loader` itself is a high-level orchestrator that delegates actual work to specialized components injected via its constructor:

```go
type Loader struct {
    discovery    discovery.Discoverer
    bootstrap    bootstrap.Bootstrapper
    validation   validation.Validator
    initializer  initialization.Initializer
    termination  termination.Terminator
    // ...
}
```

### Key Interfaces

-   **`Service`**: The main public interface implemented by `Loader`.
    -   `Load(ctx, src)`: Loads plugins from a source.
    -   `Unload(ctx, p)`: Unloads a specific plugin.

### 1. Difference between Loader and gRPC Plugin
**Loader is the "Registrar," gRPC Plugin is the "Executor."**

| Feature | Loader | gRPC Plugin (Backend Plugin) |
| :--- | :--- | :--- |
| **Responsibility** | **Static Analysis**: Scans disk, reads `plugin.json`, verifies signatures. | **Dynamic Execution**: Starts external processes, handles data queries. |
| **Focus** | Plugin **Metadata** (how it looks). | Plugin **Behavior** (what it does). |
| **Environment** | Inside the main Grafana process. | Independent external binary processes. |

### 2. Does Loader handle Panels and Apps?
**Yes.** Even though Panels and Apps are primarily frontend code (JS/TS), their "registry information" resides in the Go backend.
- **Metadata Extraction**: The Go side reads and parses `plugin.json` so the UI knows which plugins are available.
- **Security**: The Go side is responsible for verifying digital signatures of frontend code.
- **Static Asset Serving**: The Go backend provides HTTP services for JS files based on the paths recorded by the Loader.

### 3. Relationship between Go code and Frontend Plugins
- **Landlord and Tenant**: The Go backend acts as the "Landlord," managing space, routing, and security. Frontend plugins are "Tenants" running within allocated containers.
- **Data Bridge**: Frontend plugins cannot connect directly to databases. They must use the proxy interface provided by the Go backend to indirectly call backend plugins or external APIs.

```go
// Example instantiation (usually handled by dependency injection)
loader := loader.New(
    cfg,
    discoverySvc,
    bootstrapSvc,
    validationSvc,
    initializationSvc,
    terminationSvc,
    errorTracker,
)

// Loading plugins
plugins, err := loader.Load(ctx, pluginSource)
```
