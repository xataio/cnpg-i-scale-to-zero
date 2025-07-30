package main

import (
	"os"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

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
			logFlags.ConfigureLogging()
			cmd.SetContext(log.IntoContext(cmd.Context(), log.GetLogger()))
		},
	}

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
		// Register the declared implementations
		lifecycle.RegisterOperatorLifecycleServer(server, lifecycleImpl.Implementation{})
		return nil
	})

	cmd.Use = "plugin"

	return cmd
}
