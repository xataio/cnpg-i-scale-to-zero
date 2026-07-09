# Installation

## Prerequisites

- Kubernetes 1.29 or newer
- CloudNativePG 1.26 or newer
- cert-manager

## Install the plugin

Apply the published manifest:

```bash
kubectl apply -f \
  https://raw.githubusercontent.com/xataio/cnpg-i-scale-to-zero/main/manifest.yaml

kubectl wait --for=condition=available --timeout=300s \
  deployment/scale-to-zero -n cnpg-system
```

The manifest installs the plugin and its TLS certificates in `cnpg-system`.

## Enable a cluster

Add the plugin and scale-to-zero annotations to a CloudNativePG cluster:

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
  plugins:
    - name: cnpg-i-scale-to-zero.xata.io
  storage:
    size: 1Gi
```

The plugin periodically checks the number of connections to the primary
PostgreSQL instance. After consecutive zero-connection responses for the
configured inactivity period, it hibernates the cluster. Errors or unavailable
connection data do not count as inactivity.

### Cluster configuration

- `xata.io/scale-to-zero-enabled`: Set to `"true"` to enable scale-to-zero.
- `xata.io/scale-to-zero-inactivity-minutes`: Inactivity threshold in minutes
  before hibernation. The default is `30`.

The plugin manages `cnpg.io/hibernation` and suspends the `ScheduledBackup`
with the same namespace and name as the cluster.

See the [cluster example](doc/examples/cluster-example.yaml) for a complete
configuration.

### Sidecar resources

The injected sidecar defaults to:

- CPU request: `50m`
- CPU limit: `200m`
- Memory request: `64Mi`
- Memory limit: `64Mi`

Override these values on the plugin deployment:

```bash
kubectl set env deployment/scale-to-zero -n cnpg-system \
  SIDECAR_CPU_REQUEST=100m \
  SIDECAR_CPU_LIMIT=500m \
  SIDECAR_MEMORY_REQUEST=128Mi \
  SIDECAR_MEMORY_LIMIT=128Mi

kubectl rollout restart deployment/scale-to-zero -n cnpg-system
```

## Verify the installation

```bash
kubectl get deployment/scale-to-zero -n cnpg-system
kubectl get pods -l cnpg.io/cluster=my-cluster
```

Confirm that a PostgreSQL pod contains the injected `scale-to-zero` container:

```bash
kubectl get pod -l cnpg.io/cluster=my-cluster \
  -o jsonpath='{.items[0].spec.containers[*].name}'
```

## Uninstall

Remove or update clusters that reference the plugin before removing the plugin:

```bash
kubectl delete -f \
  https://raw.githubusercontent.com/xataio/cnpg-i-scale-to-zero/main/manifest.yaml
```

Removing the plugin does not remove existing CloudNativePG clusters.

## Troubleshooting

Check certificate readiness:

```bash
kubectl get certificate,issuer -n cnpg-system
```

Check the plugin and sidecar logs:

```bash
kubectl logs deployment/scale-to-zero -n cnpg-system
kubectl logs <postgres-pod> -c scale-to-zero
```

Check that the cluster enables the plugin:

```bash
kubectl get cluster my-cluster \
  -o jsonpath='{.spec.plugins[*].name}{"\n"}'
```
