# Installation Guide

This guide explains how to install and use the CNPG-I Scale-to-Zero plugin.

## Prerequisites

- Kubernetes cluster (1.24+)
- CloudNativePG operator installed (1.24+)
- cert-manager installed for TLS certificate management

## Quick Installation

### Option 1: Using the manifest (Recommended)

```bash
# Install the plugin
kubectl apply -f https://raw.githubusercontent.com/xataio/cnpg-i-scale-to-zero/main/manifest.yaml

# Wait for the plugin to be ready
kubectl wait --for=condition=available --timeout=300s deployment/scale-to-zero -n cnpg-system
```

### Option 2: Building from source

```bash
# Clone the repository
git clone https://github.com/xataio/cnpg-i-scale-to-zero.git
cd cnpg-i-scale-to-zero

make deploy
```

### Option 3: Local development with kind

```bash
# Create a kind cluster
kind create cluster --name cnpg-i-scale-to-zero

# Install CloudNativePG
kubectl apply --server-side -f \
  https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v1.24.1/cnpg-1.24.1.yaml

# Install cert-manager
kubectl apply -f \
  https://github.com/cert-manager/cert-manager/releases/download/v1.16.1/cert-manager.yaml

# Build and deploy the plugin
make kind-deploy-dev
```

## Usage

Create a PostgreSQL cluster with the scale-to-zero plugin:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: my-cluster
  annotations:
    xata.io/inactivity-minutes: "10" # Hibernate after 10 minutes of inactivity
spec:
  instances: 3
  enableSuperuserAccess: true
  plugins:
    - name: cnpg-i-scale-to-zero.xata.io
  storage:
    size: 1Gi
```

Apply the configuration:

```bash
kubectl apply -f cluster.yaml
```

## Configuration

The plugin behavior is controlled through cluster annotations:

- `xata.io/inactivity-minutes`: Inactivity threshold in minutes before hibernation (default: 30)

## Monitoring

Check plugin status:

```bash
# Plugin logs
kubectl logs -n cnpg-system deployment/scale-to-zero -f

# Sidecar logs (in PostgreSQL pods)
kubectl logs <pod-name> -c scale-to-zero -f

# Cluster status
kubectl get clusters
```

## Uninstallation

```bash
# Remove the plugin
kubectl delete -f manifest.yaml

# Or using make
make undeploy
```

## Troubleshooting

### Plugin not starting

1. Check if cert-manager is installed and running
2. Verify the certificates are created:
   ```bash
   kubectl get certificates -n cnpg-system
   ```

### Sidecar not injected

1. Check if the cluster has the plugin configured in the spec
2. Verify the plugin service is running and accessible
3. Check CloudNativePG operator logs for plugin communication errors

### Hibernation not working

1. Check sidecar logs for database connection issues
2. Verify the cluster annotations are correctly set
3. Ensure the sidecar has proper RBAC permissions
