CERT_MANAGER_VERSION = 'v1.18.2'
CNPG_VERSION = '1.28.0'

update_settings(k8s_upsert_timeout_secs=300)

docker_build(
    'ghcr.io/xataio/cnpg-i-scale-to-zero',
    '.',
    dockerfile='Dockerfile.plugin',
    build_args={'VERSION': 'dev'},
    only=[
        'cmd',
        'internal',
        'pkg',
        'go.mod',
        'go.sum',
        'Dockerfile.plugin',
    ],
)

docker_build(
    'ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar',
    '.',
    dockerfile='Dockerfile.sidecar',
    build_args={'VERSION': 'dev'},
    match_in_env_vars=True,
    only=[
        'cmd',
        'internal',
        'pkg',
        'go.mod',
        'go.sum',
        'Dockerfile.sidecar',
    ],
)

local_resource(
    'cert-manager',
    cmd='''
set -eu

kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml
kubectl wait --for=condition=Available --timeout=300s \
    deployment/cert-manager deployment/cert-manager-cainjector deployment/cert-manager-webhook \
    -n cert-manager
''' % CERT_MANAGER_VERSION,
    deps=['Tiltfile'],
    labels='infra',
)

local_resource(
    'cnpg',
    cmd='''
set -eu

kubectl apply --server-side -f \
    https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.28/releases/cnpg-%s.yaml
kubectl wait --for=condition=Available --timeout=300s \
    deployment/cnpg-controller-manager -n cnpg-system
''' % CNPG_VERSION,
    deps=['Tiltfile'],
    labels='infra',
)

plugin_yaml = kustomize('kubernetes')
k8s_yaml(plugin_yaml)
k8s_kind('Cluster')
k8s_yaml('doc/examples/cluster-example.yaml')

k8s_resource(
    objects=[
        'cnpg-scale-to-zero-plugin:serviceaccount',
        'scaletozero-client:certificate',
        'scaletozero-server:certificate',
        'cnpg-scale-to-zero-selfsigned-issuer:issuer',
    ],
    new_name='scale-to-zero-config',
    resource_deps=['cert-manager'],
    pod_readiness='ignore',
    labels='plugin',
)

plugin_support_resources = [
    'cnpg-scale-to-zero-sidecar-role',
    'cnpg-scale-to-zero-plugin-binding',
]
for resource in plugin_support_resources:
    k8s_resource(
        resource,
        resource_deps=['cnpg'],
        pod_readiness='ignore',
        labels='plugin',
    )

k8s_resource(
    'scale-to-zero',
    resource_deps=plugin_support_resources + ['cnpg', 'scale-to-zero-config'],
    labels='plugin',
)

k8s_resource(
    'cluster-example',
    objects=['cluster-example:scheduledbackup'],
    resource_deps=['scale-to-zero'],
    extra_pod_selectors=[{
        'cnpg.io/cluster': 'cluster-example',
        'role': 'primary',
    }],
    pod_readiness='wait',
    labels='cluster',
)
