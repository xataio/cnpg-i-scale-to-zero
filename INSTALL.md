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

The manifest installs the CNPG-I service, TLS certificates, and the central
scraper RBAC in `cnpg-system`.

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

The plugin injects a passive connection probe into each PostgreSQL pod. The
central plugin deployment scrapes the current primary and sets
`cnpg.io/hibernation=on` after the configured inactivity period.

## Verify the installation

```bash
kubectl get deployment/scale-to-zero -n cnpg-system
kubectl get pods -l cnpg.io/cluster=my-cluster
kubectl logs deployment/scale-to-zero -n cnpg-system
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

Check that the cluster enables the plugin and has a current primary:

```bash
kubectl get cluster my-cluster \
  -o jsonpath='{.spec.plugins[*].name}{"\n"}{.status.currentPrimary}{"\n"}'
```

The scraper treats missing pods, unhealthy clusters, timeouts, and probe errors
as unknown activity and will not hibernate the cluster.
