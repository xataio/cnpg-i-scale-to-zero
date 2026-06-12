// Package config provides configuration for the scale-to-zero plugin
package config

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Config holds the configuration for the scale-to-zero plugin
type Config struct {
	// SidecarImage is the container image to use for the sidecar
	SidecarImage   string
	LogLevel       string
	MetricsAddress string
	// SidecarResources defines resource requirements for the sidecar container
	SidecarResources *ResourceConfig
	Scraper          ScraperConfig
}

type ScraperConfig struct {
	Interval          time.Duration
	Timeout           time.Duration
	Concurrency       int
	SidecarScrapePort int32
}

// ResourceConfig defines resource configuration for a container
type ResourceConfig struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

const (
	defaultSidecarImage   = "ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar:main"
	defaultLogLevel       = "info"
	defaultMetricsAddress = ":8080"
	defaultCPURequest     = "50m"
	defaultCPULimit       = "200m"
	// for memory the request and limit are set to the same value to prevent OOM
	// issues
	defaultMemoryRequest = "64Mi"
	defaultMemoryLimit   = "64Mi"
	defaultInterval      = 60 * time.Second
	defaultTimeout       = 2 * time.Second
	defaultConcurrency   = 200
	defaultScrapePort    = int32(9188)
)

// New creates a new Config instance with the provided parameters.
// Environment variables are used to override defaults if the parameters are empty.
func New(sidecarImage, logLevel, metricsAddress string, resourceConfig *ResourceConfig, scraperConfig ScraperConfig) *Config {
	if sidecarImage == "" {
		sidecarImage = defaultSidecarImage
	}

	if logLevel == "" {
		logLevel = defaultLogLevel
	}
	if metricsAddress == "" {
		metricsAddress = defaultMetricsAddress
	}

	return &Config{
		SidecarImage:     sidecarImage,
		LogLevel:         logLevel,
		MetricsAddress:   metricsAddress,
		SidecarResources: resourceConfig,
		Scraper:          scraperConfig.WithDefaults(),
	}
}

func NewScraperConfig(interval, timeout, concurrency, sidecarScrapePort string) ScraperConfig {
	return ScraperConfig{
		Interval:          parseDuration(interval, defaultInterval),
		Timeout:           parseDuration(timeout, defaultTimeout),
		Concurrency:       parseInt(concurrency, defaultConcurrency),
		SidecarScrapePort: int32(parseInt(sidecarScrapePort, int(defaultScrapePort))),
	}.WithDefaults()
}

func (cfg ScraperConfig) WithDefaults() ScraperConfig {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultConcurrency
	}
	if cfg.SidecarScrapePort <= 0 {
		cfg.SidecarScrapePort = defaultScrapePort
	}
	return cfg
}

// ToResourceRequirements converts the SidecarResourceConfig to Kubernetes
// ResourceRequirements, applying defaults if necessary.
// Defaults to 50m CPU request, 200m CPU limit, and 64Mi memory request and memory limit.
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

func parseDuration(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return duration
}

func parseInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
