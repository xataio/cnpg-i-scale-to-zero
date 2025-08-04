// Package config provides configuration for the scale-to-zero plugin
package config

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Config holds the configuration for the scale-to-zero plugin
type Config struct {
	// SidecarImage is the container image to use for the sidecar
	SidecarImage string
	LogLevel     string
	// SidecarResources defines resource requirements for the sidecar container
	SidecarResources *ResourceConfig
}

// ResourceConfig defines resource configuration for a container
type ResourceConfig struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

const (
	defaultSidecarImage  = "ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar:main"
	defaultLogLevel      = "info"
	defaultCPURequest    = "50m"
	defaultCPULimit      = "200m"
	defaultMemoryRequest = "64Mi"
	defaultMemoryLimit   = "128Mi"
)

// New creates a new Config instance with the provided parameters.
// Environment variables are used to override defaults if the parameters are empty.
func New(sidecarImage, logLevel string, resourceConfig *ResourceConfig) *Config {
	if sidecarImage == "" {
		sidecarImage = defaultSidecarImage
	}

	if logLevel == "" {
		logLevel = defaultLogLevel
	}

	return &Config{
		SidecarImage:     sidecarImage,
		LogLevel:         logLevel,
		SidecarResources: resourceConfig,
	}
}

// ToResourceRequirements converts the SidecarResourceConfig to Kubernetes
// ResourceRequirements, applying defaults if necessary.
// Defaults to 50m CPU request, 200m CPU limit, 64Mi memory request, and 128Mi memory limit.
func (src *ResourceConfig) ToResourceRequirements() corev1.ResourceRequirements {
	requirements := corev1.ResourceRequirements{
		Requests: make(corev1.ResourceList),
		Limits:   make(corev1.ResourceList),
	}

	applyResourceQuantity(requirements.Requests, corev1.ResourceCPU, src.CPURequest, defaultCPURequest)
	applyResourceQuantity(requirements.Requests, corev1.ResourceMemory, src.MemoryRequest, defaultMemoryRequest)
	applyResourceQuantity(requirements.Limits, corev1.ResourceCPU, src.CPULimit, defaultCPULimit)
	applyResourceQuantity(requirements.Limits, corev1.ResourceMemory, src.MemoryLimit, defaultMemoryLimit)

	return requirements
}

func applyResourceQuantity(resourceList corev1.ResourceList, resourceName corev1.ResourceName, quantity, defaultQuantity string) {
	if quantity == "" {
		quantity = defaultQuantity
	}
	parsedQuantity, err := resource.ParseQuantity(quantity)
	if err != nil {
		parsedQuantity = resource.MustParse(defaultQuantity)
	}
	resourceList[resourceName] = parsedQuantity
}
