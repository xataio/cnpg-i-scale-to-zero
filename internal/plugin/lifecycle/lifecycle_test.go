package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

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
