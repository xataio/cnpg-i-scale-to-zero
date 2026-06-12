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

Some examples of what can be achieved through the lifecycle:

- add volume, volume mounts, sidecar containers, labels, annotations to pods,
  especially necessary when implementing custom backup solutions
- modify any resource with some annotations or labels
- add/remove finalizers

[API reference](https://github.com/cloudnative-pg/cnpg-i/blob/main/proto/operator_lifecycle.proto)

The scale-to-zero plugin uses this to inject a passive sidecar container into
PostgreSQL pods. The central plugin deployment scrapes the current primary pod
and hibernates clusters when successful probe data shows inactivity for the
configured duration.

## Implementation

### Identity

1. Define a struct inside the
   [`internal/plugin/identity`](../internal/plugin/identity) package that
   implements the CNPG-I `identity.IdentityServer` interface.

2. Implement the following methods:

   - `GetPluginMetadata`: return human-readable information about the plugin.
   - `GetPluginCapabilities`: specify the features supported by the plugin. In
     the scale-to-zero plugin, only the
     `PluginCapability_Service_TYPE_LIFECYCLE_SERVICE` is defined in the
     corresponding Go [file](../internal/plugin/identity/impl.go).
   - `Probe`: indicate whether the plugin is ready to serve requests; this
     implementation currently always reports ready.

### Lifecycle

This plugin implements the lifecycle service capabilities to inject a sidecar
container into PostgreSQL pods. The `OperatorLifecycleServer` interface is
implemented in
[`internal/plugin/lifecycle`](../internal/plugin/lifecycle).

The `OperatorLifecycleServer` interface requires several methods:

- `GetCapabilities`: describe the resources and operations the plugin should be
  notified for

- `LifecycleHook`: is invoked for every operation against the Kubernetes API
  server that matches the specifications returned by `GetCapabilities`

  In this function, the plugin is expected to do pattern matching using
  the `Kind` and the operation `Type` and proceed with the proper logic.

The scale-to-zero plugin specifically:

- Handles Pod create and evaluate operations
- Injects a sidecar container into all PostgreSQL cluster pods
- Copies CNPG's `PGHOST` and `PGPORT` values and the matching Unix socket mount
  into the sidecar
- The central scraper watches clusters, scheduled backups, and pods
- The scraper probes only the cached current primary pod
- The scraper manages hibernation and scheduled backup suspension

### Sidecar Implementation

The sidecar is a separate component that runs alongside the PostgreSQL container.
It's implemented in the `internal/sidecar` package and exposes passive activity
data over HTTP.

#### Sidecar Startup ([`sidecar.go`](../internal/sidecar/sidecar.go))

The sidecar startup code:

- Reads the probe listen address
- Serves `GET /connections` on the configured listen address

#### Activity Probe ([`probe.go`](../internal/sidecar/probe.go))

The sidecar connections probe reports PostgreSQL connection counts:

- **Connections Probe**: Connects to PostgreSQL over the CNPG Unix socket and
  checks for open connections
- **HTTP API**: Returns the open connection count as a JSON integer from
  `GET /connections`
- **Error Handling**: PostgreSQL errors return non-200 responses, so the central
  scraper treats the result as unknown rather than inactive

Key features:

- PostgreSQL connection pooling for activity monitoring
- Graceful shutdown on context cancellation
- No Kubernetes client, CNPG API dependency, or Kubernetes writes

#### Environment Variables

The lifecycle hook injects these environment variables into the sidecar:

- `LOG_LEVEL`: The log level for the sidecar
- `LISTEN_ADDRESS`: The HTTP probe listen address (default: `:9188`)
- `PGHOST`: The CNPG PostgreSQL Unix socket directory
- `PGPORT`: The CNPG PostgreSQL server port

### Startup Command

The plugin runs in its own pod. The executable entry point is
[`cmd/plugin/plugin.go`](../cmd/plugin/plugin.go), and the command is constructed
in [`pkg/plugin/plugin.go`](../pkg/plugin/plugin.go).

This function uses the plugin helper library to create a gRPC server and manage
TLS.

The command passes the identity implementation to `http.CreateMainCmd`,
constructs the controller-runtime manager and scraper, and registers the
lifecycle implementation with the gRPC server. The manager and gRPC server
share a context so either one terminating stops the plugin.

```go
lifecycle.RegisterOperatorLifecycleServer(
    server,
    lifecycleImpl.NewImplementation(cfg),
)
```

## Scale-to-Zero Functionality

### How It Works

The scale-to-zero plugin automatically hibernates PostgreSQL clusters when they
are inactive for a specified period. Here's how it operates:

1. **Sidecar Injection**: When a PostgreSQL pod is created, the plugin injects a
   sidecar container that exposes database activity to the central scraper.

2. **Activity Monitoring**: The central scraper watches CNPG objects and scrapes
   the primary pod sidecar over HTTP.

3. **Hibernation**: When the cluster has been inactive for the configured duration,
   the central plugin sets the `cnpg.io/hibernation` annotation on the cluster, causing
   CloudNativePG to scale it down to zero replicas.

4. **Scheduled Backup Management**: After hibernating a cluster, the plugin
   pauses the `ScheduledBackup` with the same namespace and name as the cluster.

### Configuration

The plugin behavior can be configured through cluster annotations:

- `xata.io/scale-to-zero-enabled`: If the scale to zero behaviour should be applied for the cluster (default: false)
- `xata.io/scale-to-zero-inactivity-minutes`: Sets the inactivity threshold in minutes before
  hibernation (default: 30 minutes)
- `cnpg.io/hibernation`: Used by the plugin to trigger hibernation (set automatically)

### Sidecar Container

The injected sidecar container is configurable and uses environment-based configuration:

- **Default image**: `ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar:main`
- **Configurable via**: `SIDECAR_IMAGE` on the plugin deployment
- Access to PostgreSQL through the shared CNPG Unix socket
- HTTP connections probe on the port configured by `SIDECAR_SCRAPE_PORT`

### Central Scraper

The plugin process runs a controller-runtime cache and scraper alongside the
CNPG-I gRPC server:

- Watches CNPG `Cluster`, `ScheduledBackup`, and Kubernetes `Pod` objects
- Scrapes only `status.currentPrimary`
- Treats a missing pod, timeout, non-200 response, or invalid response as
  unknown and resets the inactivity window
- Patches `cnpg.io/hibernation=on` on the CNPG `Cluster`
- Pauses the same-name `ScheduledBackup` by setting `spec.suspend=true`

The central scraper is configured on the plugin deployment:

- `SCRAPER_INTERVAL`: Time between scrape cycles (default: `60s`)
- `SCRAPER_TIMEOUT`: Timeout for each sidecar request (default: `2s`)
- `SCRAPER_CONCURRENCY`: Maximum concurrent sidecar requests (default: `200`)
- `SIDECAR_SCRAPE_PORT`: Sidecar HTTP port injected into pods and used for
  scraping (default: `9188`)
- `METRICS_ADDRESS`: Plugin metrics listen address (default: `:8080`)

The plugin exposes Prometheus metrics at `/metrics` on the `metrics` service
port.

## Build and deploy the plugin

For installation instructions, see the [installation guide](../INSTALL.md).

Build and test from source:

```bash
make build
make test
make lint
make docker-build-dev
```

Regenerate `manifest.yaml` after changing files under `kubernetes/`:

```bash
make manifest
```

### Local testing with Tilt

The root `Tiltfile` installs cert-manager and CloudNativePG, builds the plugin
and sidecar images, deploys the plugin, and applies
`doc/examples/cluster-example.yaml`. It uses the current Kubernetes context:

```bash
tilt up
```

For an isolated kind environment:

```bash
kind create cluster --name s2z
tilt up
```

Verify the injected sidecar:

```bash
kubectl get pod -l cnpg.io/cluster=cluster-example,role=primary \
  -o jsonpath='{.items[0].spec.containers[*].name}'
kubectl logs deployment/scale-to-zero -n cnpg-system
```

The example cluster uses a two-minute inactivity threshold.
