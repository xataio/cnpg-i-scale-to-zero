package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestSidecarContainerIsHardened(t *testing.T) {
	t.Parallel()

	impl := Implementation{sidecarImage: "sidecar:test", sidecarPort: 8080}
	mount := corev1.VolumeMount{Name: "scratch-data", MountPath: "/controller/run"}

	container := impl.sidecarContainer(nil, mount)

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
	require.Equal(t, []corev1.VolumeMount{mount}, container.VolumeMounts)
}

func TestPostgresRuntimeUsesCNPGContainerConfiguration(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "postgres",
					Env: []corev1.EnvVar{
						{Name: "PGHOST", Value: "/changed/run"},
						{Name: "PGPORT", Value: "5433"},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "unrelated", MountPath: "/run"},
						{Name: "socket-parent", MountPath: "/changed"},
						{Name: "socket", MountPath: "/changed/run"},
					},
				},
			},
		},
	}

	env, mount, err := postgresRuntime(pod)

	require.NoError(t, err)
	require.Equal(t, pod.Spec.Containers[0].Env, env)
	require.Equal(t, pod.Spec.Containers[0].VolumeMounts[2], mount)
}

func TestPostgresRuntimeRequiresCNPGConfiguration(t *testing.T) {
	t.Parallel()

	_, _, err := postgresRuntime(&corev1.Pod{})

	require.EqualError(t, err, "CNPG PostgreSQL runtime environment not found")
}
