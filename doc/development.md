# Plugin Development

This section of the documentation illustrates the CNPG-I capabilities used by
the scale-to-zero plugin, how the plugin implementation uses them, and how
developers can build and deploy the plugin.

## Concepts

### Identity

The Identity interface defines the features supported by the plugin and is the
only interface that must always be implemented.

This information is essential for the operator to discover the plugin's
capabilities during startup.

The Identity interface provides:

- A mechanism for plugins to report readiness probes. Readiness is a
  prerequisite for receiving events, and plugins are expected to always report
  the most accurate readiness data available.
- The capabilities reported by the plugin, which determine the subsequent calls
  the plugin will receive.
- Metadata about the plugin.

[API reference](https://github.com/cloudnative-pg/cnpg-i/blob/main/proto/identity.proto)

### Capabilities

This plugin implements only the Lifecycle capability.

#### Lifecycle

This feature enables the plugin to receive events and create patches for
Kubernetes resources `before` they are submitted to the API server.

To use this feature, the plugin must specify the resource and operation it wants
to be notified of.

Some examples of what it can be achieved through the lifecycle:

- add volume, volume mounts, sidecar containers, labels, annotations to pods,
  especially necessary when implementing custom backup solutions
- modify any resource with some annotations or labels
- add/remove finalizers

[API reference](https://github.com/cloudnative-pg/cnpg-i/blob/main/proto/operator_lifecycle.proto):

The scale-to-zero plugin uses this to inject a sidecar container into the primary
PostgreSQL pod that monitors database activity and can automatically hibernate the
cluster when it's inactive for a specified duration.

## Implementation

### Identity

1. Define a struct inside the `internal/identity` package that implements
   the `pluginhelper.IdentityServer` interface.

2. Implement the following methods:

   - `GetPluginMetadata`: return human-readable information about the plugin.
   - `GetPluginCapabilities`: specify the features supported by the plugin. In
     the scale-to-zero plugin, only the
     `PluginCapability_Service_TYPE_LIFECYCLE_SERVICE` is defined in the
     corresponding Go [file](../internal/identity/impl.go).
   - `Probe`: indicate whether the plugin is ready to serve requests; this
     plugin is stateless, so it will always be ready.

### Lifecycle

This plugin implements the lifecycle service capabilities to inject a sidecar
container into PostgreSQL pods. The `OperatorLifecycleServer` interface is implemented
inside the `internal/lifecycle` package.

The `OperatorLifecycleServer` interface requires several methods:

- `GetCapabilities`: describe the resources and operations the plugin should be
  notified for

- `LifecycleHook`: is invoked for every operation against the Kubernetes API
  server that matches the specifications returned by `GetCapabilities`

  In this function, the plugin is expected to do pattern matching using
  the `Kind` and the operation `Type` and proceed with the proper logic.

The scale-to-zero plugin specifically:

- Monitors Pod creation events
- Injects a sidecar container into the primary PostgreSQL pod only
- The sidecar monitors database activity and hibernates inactive clusters
- Manages scheduled backups by pausing them during hibernation

### Sidecar Implementation

The sidecar is a separate component that runs alongside the PostgreSQL container
in the primary pod. It's implemented in the `internal/sidecar` package and provides
the core scale-to-zero functionality.

#### Sidecar Manager (`sidecar_manager.go`)

The sidecar manager handles the startup and configuration of the sidecar process:

- Sets up the Kubernetes client and controller manager
- Configures the runtime scheme to work with CNPG resources
- Starts the scale-to-zero monitoring process
- Supports custom CNPG group/version through environment variables

#### Scale-to-Zero Logic (`scale_to_zero.go`)

The main scale-to-zero functionality monitors database activity and hibernates inactive clusters:

- **Activity Monitoring**: Connects to PostgreSQL to check for open connections
- **Switchover Handling**: Automatically detects primary changes and transfers monitoring responsibility
- **Configurable Inactivity Threshold**: Uses the `xata.io/scale-to-zero-inactivity-minutes`
  annotation to determine when a cluster should be hibernated (defaults to 30 minutes)
- **Hibernation**: Sets the `cnpg.io/hibernation` annotation to scale the cluster to zero
- **Scheduled Backup Management**: Automatically pauses scheduled backups when hibernating clusters to prevent backup failures on inactive clusters

Key features:

- Periodic checks at configurable intervals (default: 1 minute)
- PostgreSQL connection pooling for activity monitoring
- Graceful shutdown on context cancellation
- Error handling for replica instances (stops monitoring if not primary)
- Automatic scheduled backup pause operations

#### Environment Variables

The sidecar requires several environment variables that are automatically injected
by the lifecycle hook:

- `LOG_LEVEL`: The log level for the sidecar
- `NAMESPACE`: The Kubernetes namespace of the PostgreSQL cluster
- `CLUSTER_NAME`: The name of the PostgreSQL cluster
- `POD_NAME`: The name of the current pod

### Startup Command

The plugin runs in its own pod, and its main command is implemented in
the [`cmd/plugin.go`](<(../cmd/plugin/plugin.go)>) file.

This function uses the plugin helper library to create a GRPC server and manage
TLS.

Plugin developers are expected to use the `pluginhelper.CreateMainCmd`
to implement the `main` function, passing an implemented `Identity`
struct.

Further implementations can be registered within the callback function.

```go
lifecycle.RegisterOperatorLifecycleServer(server, lifecycleImpl.Implementation{})
```

## Scale-to-Zero Functionality

### How It Works

The scale-to-zero plugin automatically hibernates PostgreSQL clusters when they
are inactive for a specified period. Here's how it operates:

1. **Sidecar Injection**: When a PostgreSQL pod is created, the plugin injects a
   sidecar container that monitors database activity.

2. **Activity Monitoring**: The sidecar periodically connects to PostgreSQL to check open database connections.

3. **Hibernation**: When the cluster has been inactive for the configured duration,
   the primary sidecar sets the `cnpg.io/hibernation` annotation on the cluster, causing
   CloudNativePG to scale it down to zero replicas.

4. **Scheduled Backup Management**: After hibernating a cluster, the sidecar automatically
   pauses any associated scheduled backups to prevent backup operations from failing
   on hibernated clusters.

### Configuration

The plugin behavior can be configured through cluster annotations:

- `xata.io/scale-to-zero-enabled`: If the scale to zero behaviour should be applied for the cluster (default: false)
- `xata.io/scale-to-zero-inactivity-minutes`: Sets the inactivity threshold in minutes before
  hibernation (default: 30 minutes)
- `cnpg.io/hibernation`: Used by the plugin to trigger hibernation (set automatically)

### Sidecar Container

The injected sidecar container is configurable and uses environment-based configuration:

- **Default image**: `ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar:main`
- **Configurable via**: `SIDECAR_IMAGE` environment variable or `--sidecar-image` flag
- Environment variables for cluster identification
- Direct access to the PostgreSQL database
- Kubernetes API access for cluster and scheduled backup management
- Configurable check intervals and inactivity thresholds

## Build and deploy the plugin

For more information about deploying the plugin, check out the [install documentation](../INSTALL.md).
