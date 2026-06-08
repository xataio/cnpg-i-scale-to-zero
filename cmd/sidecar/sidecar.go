package main

import (
	"os"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/sidecar"
)

func main() {
	cobra.EnableTraverseRunHooks = true
	rootCmd := newCmd()

	if err := rootCmd.Execute(); err != nil {
		log.FromContext(rootCmd.Context()).Error(err, "failed to execute command")
		os.Exit(1)
	}
}

// newCmd creates a new sidecar command
func newCmd() *cobra.Command {
	logFlags := &log.Flags{}
	cmd := &cobra.Command{
		Use:   "sidecar",
		Short: "Starts the scale to zero plugin sidecar",
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			log.SetLogLevel(viper.GetString("log-level"))
			logFlags.ConfigureLogging()
			cmd.SetContext(log.IntoContext(cmd.Context(), log.GetLogger()))
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return sidecar.Start(cmd.Context())
		},
	}

	_ = viper.BindEnv("log-level", "LOG_LEVEL")
	_ = viper.BindEnv("listen-address", "LISTEN_ADDRESS")
	viper.SetDefault("listen-address", ":9188")

	return cmd
}
