package main

import (
	"context"
	"os"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xataio/cnpg-i-scale-to-zero/internal/config"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/identity"
	lifecycleImpl "github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/lifecycle"
	pluginmetrics "github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/metrics"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/scraper"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
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
	_ = viper.BindEnv("metrics-address", "METRICS_ADDRESS")
	_ = viper.BindEnv("sidecar-cpu-request", "SIDECAR_CPU_REQUEST")
	_ = viper.BindEnv("sidecar-cpu-limit", "SIDECAR_CPU_LIMIT")
	_ = viper.BindEnv("sidecar-memory-request", "SIDECAR_MEMORY_REQUEST")
	_ = viper.BindEnv("sidecar-memory-limit", "SIDECAR_MEMORY_LIMIT")
	_ = viper.BindEnv("scraper-interval", "SCRAPER_INTERVAL")
	_ = viper.BindEnv("scraper-timeout", "SCRAPER_TIMEOUT")
	_ = viper.BindEnv("scraper-concurrency", "SCRAPER_CONCURRENCY")
	_ = viper.BindEnv("sidecar-scrape-port", "SIDECAR_SCRAPE_PORT")

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
		cfg := newConfig()

		// Register the declared implementations
		lifecycle.RegisterOperatorLifecycleServer(server, lifecycleImpl.NewImplementation(cfg))
		return nil
	})

	cmd.Use = "plugin"
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg := newConfig()
		if err := startScraper(cmd.Context(), cfg.Scraper, cfg.MetricsAddress); err != nil {
			return err
		}
		return originalRunE(cmd, args)
	}

	return cmd
}

func newConfig() *config.Config {
	return config.New(
		viper.GetString("sidecar-image"),
		viper.GetString("log-level"),
		viper.GetString("metrics-address"),
		newResourceConfig(),
		config.NewScraperConfig(
			viper.GetString("scraper-interval"),
			viper.GetString("scraper-timeout"),
			viper.GetString("scraper-concurrency"),
			viper.GetString("sidecar-scrape-port"),
		),
	)
}

func startScraper(ctx context.Context, cfg config.ScraperConfig, metricsAddress string) error {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))

	meterProvider, err := pluginmetrics.NewProvider(ctrlmetrics.Registry)
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: metricsAddress},
	})
	if err != nil {
		return err
	}
	for _, object := range []client.Object{
		&cnpgv1.Cluster{},
		&cnpgv1.ScheduledBackup{},
		&corev1.Pod{},
	} {
		if _, err := mgr.GetCache().GetInformer(ctx, object); err != nil {
			return err
		}
	}

	s, err := scraper.New(mgr.GetClient(), nil, cfg, meterProvider.Meter("github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/scraper"))
	if err != nil {
		return err
	}
	if err := mgr.Add(managerRunnable{fn: s.Start}); err != nil {
		return err
	}
	if err := mgr.Add(managerRunnable{fn: func(ctx context.Context) error {
		<-ctx.Done()
		return meterProvider.Shutdown(context.WithoutCancel(ctx))
	}}); err != nil {
		return err
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			log.FromContext(ctx).Error(err, "scraper manager stopped")
		}
	}()

	return nil
}

type managerRunnable struct {
	fn func(context.Context) error
}

func (r managerRunnable) Start(ctx context.Context) error {
	return r.fn(ctx)
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
