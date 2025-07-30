package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		sidecarImage         string
		logLevel             string
		expectedSidecarImage string
		expectedLogLevel     string
	}{
		{
			name:                 "custom values",
			sidecarImage:         "custom/image:tag",
			logLevel:             "debug",
			expectedSidecarImage: "custom/image:tag",
			expectedLogLevel:     "debug",
		},
		{
			name:                 "default values",
			sidecarImage:         "",
			logLevel:             "",
			expectedSidecarImage: defaultSidecarImage,
			expectedLogLevel:     defaultLogLevel,
		},
		{
			name:                 "empty sidecar image",
			sidecarImage:         "",
			logLevel:             "warn",
			expectedSidecarImage: defaultSidecarImage,
			expectedLogLevel:     "warn",
		},
		{
			name:                 "empty log level",
			sidecarImage:         "another/image:latest",
			logLevel:             "",
			expectedSidecarImage: "another/image:latest",
			expectedLogLevel:     defaultLogLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := New(tt.sidecarImage, tt.logLevel)
			require.Equal(t, tt.expectedSidecarImage, cfg.SidecarImage)
			require.Equal(t, tt.expectedLogLevel, cfg.LogLevel)
		})
	}
}
