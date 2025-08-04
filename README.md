# 0️⃣ CNPG-I Scale-to-Zero Plugin

A [CNPG-I](https://github.com/cloudnative-pg/cnpg-i) plugin that automatically hibernates inactive [CloudNativePG](https://github.com/cloudnative-pg/cloudnative-pg/) clusters to optimize resource usage and reduce costs.

## Overview

This plugin monitors PostgreSQL database activity and automatically scales clusters down to zero replicas when they've been inactive for a configurable period. It injects a monitoring sidecar into the primary PostgreSQL pod that tracks database connections and query activity, then hibernates the cluster by setting the `cnpg.io/hibernation` annotation when the inactivity threshold is reached.

### How It Works

1. **Sidecar Injection**: Automatically adds a monitoring sidecar to the primary PostgreSQL pod
2. **Activity Monitoring**: The sidecar periodically checks for active database connections and recent queries
3. **Automatic Hibernation**: When the cluster is inactive for the configured duration, it sets the hibernation annotation
4. **Resource Optimization**: Inactive clusters are scaled to zero, freeing up cluster resources

## Installation

For detailed installation instructions, see [INSTALL.md](INSTALL.md).

Quick start:

```bash
kubectl apply -f manifest.yaml
```

## Container Images

The plugin consists of two container images:

- **Plugin**: `ghcr.io/xataio/cnpg-i-scale-to-zero`
- **Sidecar**: `ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar`

### Image Tags

We publish different image tags for different use cases:

#### Local Docker library

- `dev`: local docker images built using `make docker-build-dev`

#### GHCR

##### Development Tags

- `main`: Latest development build from the main branch
- `main-<sha>`: Specific commit builds from main branch

##### Release Tags

- `latest`: Latest stable release
- `v1.0.0`, `v1.1.0`, etc.: Specific version releases

## Usage

Enable scale-to-zero for a PostgreSQL cluster by adding the plugin and configuration annotations:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: my-cluster
  annotations:
    xata.io/scale-to-zero-enabled: "true"
    xata.io/scale-to-zero-inactivity-minutes: "10"
spec:
  instances: 3
  enableSuperuserAccess: true
  plugins:
    - name: cnpg-i-scale-to-zero.xata.io
  storage:
    size: 1Gi
```

### Configuration

The plugin behavior is configured through cluster annotations:

- `xata.io/scale-to-zero-enabled`: Set to `"true"` to enable scale-to-zero functionality
- `xata.io/scale-to-zero-inactivity-minutes`: Sets the inactivity threshold in minutes before hibernation (default: 30 minutes)

The plugin automatically manages the `cnpg.io/hibernation` annotation to trigger cluster hibernation.

See the [cluster example](doc/examples/cluster-example.yaml) for a complete configuration.

#### RBAC

**Important**: Each cluster that uses scale-to-zero functionality requires specific RBAC permissions for the sidecar to update cluster resources.

Create the required RBAC using the template:

```bash
# Copy the RBAC template
curl -O https://raw.githubusercontent.com/xataio/cnpg-i-scale-to-zero/main/doc/examples/rbac-template.yaml

# Edit the template to replace CLUSTER_NAME and NAMESPACE
sed -i 's/CLUSTER_NAME/my-cluster/g; s/NAMESPACE/default/g' rbac-template.yaml

# Apply the RBAC configuration
kubectl apply -f rbac-template.yaml
```

Or see the [RBAC template](doc/examples/rbac-template.yaml) for manual customization.

#### Resource Configuration

The plugin allows you to configure resource requests and limits for the injected sidecar containers through environment variables in the plugin deployment. This enables you to tune resource allocation based on your cluster requirements.

**Default Sidecar Resources:**

- CPU Request: `50m` (0.05 cores)
- CPU Limit: `200m` (0.2 cores)
- Memory Request: `64Mi`
- Memory Limit: `128Mi`

**Override via Environment Variables:**

You can override these defaults by modifying the plugin deployment manifest before applying it:

```yaml
# In manifest.yaml, find the deployment and modify the env section:
env:
  - name: LOG_LEVEL
    value: info
  - name: SIDECAR_CPU_REQUEST
    value: "100m"
  - name: SIDECAR_CPU_LIMIT
    value: "500m"
  - name: SIDECAR_MEMORY_REQUEST
    value: "128Mi"
  - name: SIDECAR_MEMORY_LIMIT
    value: "256Mi"
```

**Override at Runtime:**

You can also update resource configuration after deployment using kubectl:

```bash
# Update sidecar resource configuration
kubectl set env deployment/scale-to-zero -n cnpg-system \
  SIDECAR_CPU_REQUEST=100m \
  SIDECAR_CPU_LIMIT=500m \
  SIDECAR_MEMORY_REQUEST=128Mi \
  SIDECAR_MEMORY_LIMIT=256Mi

# Restart the plugin to apply changes
kubectl rollout restart deployment/scale-to-zero -n cnpg-system
```

**ConfigMap Override:**

For environment-specific configurations, you can create a ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: scale-to-zero-resource-overrides
  namespace: cnpg-system
data:
  SIDECAR_CPU_REQUEST: "200m"
  SIDECAR_MEMORY_LIMIT: "512Mi"
---
# Then reference it in the deployment by adding to envFrom:
envFrom:
  - configMapRef:
      name: scale-to-zero-resource-overrides
      optional: true
```

These resource configurations apply to all sidecar containers injected by the plugin across all clusters.

## Monitoring and Observability

The plugin provides logging to help monitor its operation:

- Sidecar injection events are logged during pod creation
- Activity monitoring status is logged at each check interval
- Hibernation events are logged when clusters are scaled down

You can view the plugin logs using:

```shell
kubectl logs -n cnpg-system deployment/cnpg-i-scale-to-zero-plugin
```

And monitor the sidecar logs in the PostgreSQL pods:

```shell
kubectl logs <pod-name> -c scale-to-zero
```

## Development

For local development and building from source:

```bash
# Build binaries
make build

# Build Docker images
make docker-build-dev

# Run tests and linting
make test
make lint

# Local development with kind
make kind-deploy-dev
```

This plugin uses the [pluginhelper](https://github.com/cloudnative-pg/cnpg-i-machinery/tree/main/pkg/pluginhelper) from [`cnpg-i-machinery`](https://github.com/cloudnative-pg/cnpg-i-machinery) to simplify the plugin's implementation.

For additional details on the plugin implementation, refer to the [development documentation](doc/development.md).
