package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		sidecarImage         string
		logLevel             string
		resourceConfig       *ResourceConfig
		expectedSidecarImage string
		expectedLogLevel     string
	}{
		{
			name:                 "custom values",
			sidecarImage:         "custom/image:tag",
			logLevel:             "debug",
			resourceConfig:       &ResourceConfig{CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "256Mi"},
			expectedSidecarImage: "custom/image:tag",
			expectedLogLevel:     "debug",
		},
		{
			name:                 "default values",
			sidecarImage:         "",
			logLevel:             "",
			resourceConfig:       nil,
			expectedSidecarImage: defaultSidecarImage,
			expectedLogLevel:     defaultLogLevel,
		},
		{
			name:                 "empty sidecar image",
			sidecarImage:         "",
			logLevel:             "warn",
			resourceConfig:       &ResourceConfig{CPURequest: "50m"},
			expectedSidecarImage: defaultSidecarImage,
			expectedLogLevel:     "warn",
		},
		{
			name:                 "empty log level",
			sidecarImage:         "another/image:latest",
			logLevel:             "",
			resourceConfig:       nil,
			expectedSidecarImage: "another/image:latest",
			expectedLogLevel:     defaultLogLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := New(tt.sidecarImage, tt.logLevel, tt.resourceConfig)
			require.Equal(t, tt.expectedSidecarImage, cfg.SidecarImage)
			require.Equal(t, tt.expectedLogLevel, cfg.LogLevel)
			require.Equal(t, tt.resourceConfig, cfg.SidecarResources)
		})
	}
}

func TestResourceConfig_ToResourceRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		resourceConfig *ResourceConfig
		expected       corev1.ResourceRequirements
	}{
		{
			name: "all resources specified",
			resourceConfig: &ResourceConfig{
				CPURequest:    "100m",
				CPULimit:      "500m",
				MemoryRequest: "128Mi",
				MemoryLimit:   "256Mi",
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
		},
		{
			name: "only requests specified",
			resourceConfig: &ResourceConfig{
				CPURequest:    "50m",
				MemoryRequest: "64Mi",
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(defaultCPULimit),
					corev1.ResourceMemory: resource.MustParse(defaultMemoryLimit),
				},
			},
		},
		{
			name: "only limits specified",
			resourceConfig: &ResourceConfig{
				CPULimit:    "1000m",
				MemoryLimit: "512Mi",
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(defaultCPURequest),
					corev1.ResourceMemory: resource.MustParse(defaultMemoryRequest),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1000m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		},
		{
			name:           "empty resource config",
			resourceConfig: &ResourceConfig{},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(defaultCPURequest),
					corev1.ResourceMemory: resource.MustParse(defaultMemoryRequest),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(defaultCPULimit),
					corev1.ResourceMemory: resource.MustParse(defaultMemoryLimit),
				},
			},
		},
		{
			name: "invalid resource values are ignored and defaults are used",
			resourceConfig: &ResourceConfig{
				CPURequest:    "invalid",
				CPULimit:      "200m",
				MemoryRequest: "64Mi",
				MemoryLimit:   "invalid-memory",
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(defaultCPURequest),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse(defaultMemoryLimit),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := tt.resourceConfig.ToResourceRequirements()
			require.Equal(t, tt.expected, result)
		})
	}
}
