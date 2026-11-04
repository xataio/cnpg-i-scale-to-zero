package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestSidecarContainerIsHardened(t *testing.T) {
	t.Parallel()

	impl := Implementation{sidecarImage: "sidecar:test"}

	container := impl.sidecarContainer("ns", "cluster", "cluster-1")

	require.Equal(t, &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Privileged:               ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		RunAsNonRoot:             ptr.To(true),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}, container.SecurityContext)
	require.Equal(t, []corev1.VolumeMount{{
		Name:      "scratch-data",
		MountPath: "/controller/run",
		SubPath:   "run",
	}}, container.VolumeMounts)
	require.Equal(t, []corev1.EnvVar{
		{Name: "NAMESPACE", Value: "ns"},
		{Name: "CLUSTER_NAME", Value: "cluster"},
		{Name: "POD_NAME", Value: "cluster-1"},
		{Name: "LOG_LEVEL", Value: ""},
	}, container.Env)
}
