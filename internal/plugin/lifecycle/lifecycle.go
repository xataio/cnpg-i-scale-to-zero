// Package lifecycle implements the lifecycle hooks
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"
	corev1 "k8s.io/api/core/v1"

	"github.com/xataio/cnpg-i-scale-to-zero/internal/config"
)

// Implementation is the implementation of the lifecycle handler
type Implementation struct {
	lifecycle.UnimplementedOperatorLifecycleServer
	logLevel         string
	sidecarImage     string
	sidecarResources corev1.ResourceRequirements
}

// NewImplementation creates a new lifecycle implementation with the given config
func NewImplementation(cfg *config.Config) *Implementation {
	return &Implementation{
		logLevel:         cfg.LogLevel,
		sidecarImage:     cfg.SidecarImage,
		sidecarResources: cfg.SidecarResources.ToResourceRequirements(),
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

	//nolint: gocritic
	switch kind {
	case "Pod":
		switch *operation {
		case lifecycle.OperatorOperationType_TYPE_CREATE:
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

	if cluster.Status.CurrentPrimary != "" && pod.Name != cluster.Status.CurrentPrimary {
		logger.Info("pod is not the current primary, skipping sidecar injection", "pod", pod.Name, "primary", cluster.Status.CurrentPrimary)
		return &lifecycle.OperatorLifecycleResponse{}, nil
	}

	mutatedPod := pod.DeepCopy()

	sidecarContainer := &corev1.Container{
		Name:  "scale-to-zero",
		Image: impl.sidecarImage,
		Env: []corev1.EnvVar{
			{
				Name:  "NAMESPACE",
				Value: pod.Namespace,
			},
			{
				Name:  "CLUSTER_NAME",
				Value: cluster.Name,
			},
			{
				Name:  "POD_NAME",
				Value: pod.Name,
			},
			{
				Name:  "LOG_LEVEL",
				Value: impl.logLevel,
			},
		},
		Resources: impl.sidecarResources,
	}

	logger.Info("injecting sidecar into cluster pod",
		"namespace", pod.Namespace,
		"cluster", cluster.Name,
		"pod", pod.Name,
		"primary", cluster.Status.CurrentPrimary,
		"resources", sidecarContainer.Resources)

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
