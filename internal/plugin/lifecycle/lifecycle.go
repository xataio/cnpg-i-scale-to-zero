// Package lifecycle implements the lifecycle hooks
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"
	corev1 "k8s.io/api/core/v1"

	"github.com/xataio/cnpg-i-scale-to-zero/internal/config"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/scaletozero"
)

// Implementation is the implementation of the lifecycle handler
type Implementation struct {
	lifecycle.UnimplementedOperatorLifecycleServer
	logLevel         string
	sidecarImage     string
	sidecarResources corev1.ResourceRequirements
	sidecarPort      int32
}

// NewImplementation creates a new lifecycle implementation with the given config
func NewImplementation(cfg *config.Config) *Implementation {
	return &Implementation{
		logLevel:         cfg.LogLevel,
		sidecarImage:     cfg.SidecarImage,
		sidecarResources: cfg.SidecarResources.ToResourceRequirements(),
		sidecarPort:      cfg.Scraper.SidecarScrapePort,
	}
}

// GetCapabilities exposes the lifecycle capabilities
func (impl Implementation) GetCapabilities(
	_ context.Context,
	_ *lifecycle.OperatorLifecycleCapabilitiesRequest,
) (*lifecycle.OperatorLifecycleCapabilitiesResponse, error) {
	return &lifecycle.OperatorLifecycleCapabilitiesResponse{
		LifecycleCapabilities: []*lifecycle.OperatorLifecycleCapabilities{
			{
				Group: "",
				Kind:  "Pod",
				OperationTypes: []*lifecycle.OperatorOperationType{
					{
						Type: lifecycle.OperatorOperationType_TYPE_CREATE,
					},
					{
						Type: lifecycle.OperatorOperationType_TYPE_EVALUATE,
					},
				},
			},
		},
	}, nil
}

// LifecycleHook is called when creating Kubernetes services
func (impl Implementation) LifecycleHook(
	ctx context.Context,
	request *lifecycle.OperatorLifecycleRequest,
) (*lifecycle.OperatorLifecycleResponse, error) {
	kind, err := getKind(request.GetObjectDefinition())
	if err != nil {
		return nil, err
	}
	operation := request.GetOperationType().GetType().Enum()
	if operation == nil {
		return nil, errors.New("no operation set")
	}

	log.FromContext(ctx).Info("reconciling object", "kind", kind, "operation", operation)

	//nolint: gocritic
	switch kind {
	case "Pod":
		switch *operation {
		case lifecycle.OperatorOperationType_TYPE_CREATE,
			lifecycle.OperatorOperationType_TYPE_EVALUATE:
			return impl.reconcileMetadata(ctx, request)
		}
	}

	return &lifecycle.OperatorLifecycleResponse{}, nil
}

// reconcileMetadata reconciles metadata for pods, specifically handling logic related to the current primary pod in a cluster
func (impl Implementation) reconcileMetadata(
	ctx context.Context,
	request *lifecycle.OperatorLifecycleRequest,
) (*lifecycle.OperatorLifecycleResponse, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetClusterDefinition())
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx).WithName("cnpg_i_scale_to_zero_lifecycle")

	pod, err := decoder.DecodePodJSON(request.GetObjectDefinition())
	if err != nil {
		return nil, err
	}

	mutatedPod := pod.DeepCopy()

	postgresEnv, scratchDataMount, err := postgresRuntime(pod)
	if err != nil {
		return nil, err
	}

	sidecarContainer := &corev1.Container{
		Name:  "scale-to-zero",
		Image: impl.sidecarImage,
		Ports: []corev1.ContainerPort{
			{
				Name:          "connections",
				ContainerPort: impl.sidecarPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "LOG_LEVEL",
				Value: impl.logLevel,
			},
			{
				Name:  "LISTEN_ADDRESS",
				Value: fmt.Sprintf(":%d", impl.sidecarPort),
			},
		},
		VolumeMounts: []corev1.VolumeMount{scratchDataMount},
		Resources:    impl.sidecarResources,
	}
	sidecarContainer.Env = append(sidecarContainer.Env, postgresEnv...)

	if mutatedPod.Labels == nil {
		mutatedPod.Labels = make(map[string]string)
	}
	mutatedPod.Labels[scaletozero.SidecarLabel] = scaletozero.SidecarLabelTrue

	logger.Info("injecting sidecar into cluster pod",
		"namespace", pod.Namespace,
		"cluster", cluster.Name,
		"pod", pod.Name,
		"primary", cluster.Status.CurrentPrimary,
		"resources", sidecarContainer.Resources)

	// migrate to InjectPluginInitContainerSidecarSpec (means dropping support for k8s < 1.29)
	//nolint:staticcheck
	err = object.InjectPluginSidecar(mutatedPod, sidecarContainer, false)
	if err != nil {
		return nil, err
	}

	patch, err := object.CreatePatch(mutatedPod, pod)
	if err != nil {
		return nil, err
	}

	logger.Info("generated patch", "content", string(patch))

	return &lifecycle.OperatorLifecycleResponse{
		JsonPatch: patch,
	}, nil
}

// postgresRuntime copies CNPG's PostgreSQL connection environment and finds
// the most specific volume mount containing PGHOST. The sidecar needs that
// mount to access the Unix socket, while the CNPG volume name is an
// implementation detail that may change.
func postgresRuntime(pod *corev1.Pod) ([]corev1.EnvVar, corev1.VolumeMount, error) {
	requiredEnv := []string{"PGHOST", "PGPORT"}

	for _, container := range pod.Spec.Containers {
		envByName := make(map[string]corev1.EnvVar, len(container.Env))
		for _, env := range container.Env {
			envByName[env.Name] = env
		}

		env := make([]corev1.EnvVar, 0, len(requiredEnv))
		for _, name := range requiredEnv {
			value, ok := envByName[name]
			if !ok {
				env = nil
				break
			}
			env = append(env, value)
		}
		if env == nil {
			continue
		}

		var socketMount *corev1.VolumeMount
		for i := range container.VolumeMounts {
			mount := &container.VolumeMounts[i]
			if pathWithinMount(envByName["PGHOST"].Value, mount.MountPath) &&
				(socketMount == nil || len(path.Clean(mount.MountPath)) > len(path.Clean(socketMount.MountPath))) {
				socketMount = mount
			}
		}
		if socketMount != nil {
			return env, *socketMount, nil
		}
	}

	return nil, corev1.VolumeMount{}, errors.New("CNPG PostgreSQL runtime environment not found")
}

func pathWithinMount(target, mountPath string) bool {
	target = path.Clean(target)
	mountPath = path.Clean(mountPath)
	return target == mountPath || strings.HasPrefix(target, mountPath+"/")
}

// GetKind gets the Kubernetes object kind from its JSON representation
func getKind(definition []byte) (string, error) {
	var genericObject struct {
		Kind string `json:"kind"`
	}

	if err := json.Unmarshal(definition, &genericObject); err != nil {
		return "", err
	}

	return genericObject.Kind, nil
}
