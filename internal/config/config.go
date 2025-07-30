// Package config provides configuration for the scale-to-zero plugin
package config

// Config holds the configuration for the scale-to-zero plugin
type Config struct {
	// SidecarImage is the container image to use for the sidecar
	SidecarImage string
	LogLevel     string
}

const (
	defaultSidecarImage = "ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar:main"
	defaultLogLevel     = "info"
)

// New creates a new Config instance with the provided sidecar image.
// If sidecarImage is empty, it defaults to the predefined default image.
func New(sidecarImage, logLevel string) *Config {
	if sidecarImage == "" {
		sidecarImage = defaultSidecarImage
	}

	if logLevel == "" {
		logLevel = defaultLogLevel
	}

	return &Config{
		SidecarImage: sidecarImage,
		LogLevel:     logLevel,
	}
}
