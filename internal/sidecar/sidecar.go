package sidecar

import (
	"context"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/viper"

	"github.com/xataio/cnpg-i-scale-to-zero/pkg/metadata"
)

// Start starts the passive PostgreSQL connections probe.
func Start(ctx context.Context) error {
	setupLog := log.FromContext(ctx)

	listenAddress := viper.GetString("listen-address")

	setupLog.Info("starting scale to zero sidecar", "version", metadata.Data.Version)

	return serve(ctx, Config{
		ListenAddress: listenAddress,
	})
}
