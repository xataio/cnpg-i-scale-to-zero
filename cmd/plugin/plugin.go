package main

import (
	"os"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"

	"github.com/xataio/cnpg-i-scale-to-zero/internal/config"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/identity"
	lifecycleImpl "github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/lifecycle"
)

func main() {
	cobra.EnableTraverseRunHooks = true

	logFlags := &log.Flags{}
	rootCmd := &cobra.Command{
		Use:   "cnpg-i-scale-to-zero",
		Short: "A plugin to scale to zero for CloudNativePG",
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			log.SetLogLevel(viper.GetString("log-level"))
			logFlags.ConfigureLogging()
			cmd.SetContext(log.IntoContext(cmd.Context(), log.GetLogger()))
		},
	}

	_ = viper.BindEnv("sidecar-image", "SIDECAR_IMAGE")
	_ = viper.BindEnv("log-level", "LOG_LEVEL")
	_ = viper.BindEnv("sidecar-cpu-request", "SIDECAR_CPU_REQUEST")
	_ = viper.BindEnv("sidecar-cpu-limit", "SIDECAR_CPU_LIMIT")
	_ = viper.BindEnv("sidecar-memory-request", "SIDECAR_MEMORY_REQUEST")
	_ = viper.BindEnv("sidecar-memory-limit", "SIDECAR_MEMORY_LIMIT")

	logFlags.AddFlags(rootCmd.PersistentFlags())

	rootCmd.AddCommand(newCmd())

	if err := rootCmd.Execute(); err != nil {
		log.FromContext(rootCmd.Context()).Error(err, "failed to execute command")
		os.Exit(1)
	}
}

// NewCmd creates the `plugin` command
func newCmd() *cobra.Command {
	cmd := http.CreateMainCmd(identity.Implementation{}, func(server *grpc.Server) error {
		// Create config at execution time to ensure viper has loaded environment variables
		cfg := config.New(viper.GetString("sidecar-image"), viper.GetString("log-level"), newResourceConfig())

		// Register the declared implementations
		lifecycle.RegisterOperatorLifecycleServer(server, lifecycleImpl.NewImplementation(cfg))
		return nil
	})

	cmd.Use = "plugin"

	return cmd
}

// NewResourceConfig creates a new ResourceConfig with environment variable overrides
func newResourceConfig() *config.ResourceConfig {
	return &config.ResourceConfig{
		CPURequest:    viper.GetString("sidecar-cpu-request"),
		CPULimit:      viper.GetString("sidecar-cpu-limit"),
		MemoryRequest: viper.GetString("sidecar-memory-request"),
		MemoryLimit:   viper.GetString("sidecar-memory-limit"),
	}
}
