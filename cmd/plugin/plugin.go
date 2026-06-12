package main

import (
	"os"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/xataio/cnpg-i-scale-to-zero/pkg/plugin"
)

func main() {
	rootCmd := plugin.NewCommand()
	if err := rootCmd.Execute(); err != nil {
		log.FromContext(rootCmd.Context()).Error(err, "failed to execute command")
		os.Exit(1)
	}
}
