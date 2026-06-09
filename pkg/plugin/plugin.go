// Package plugin constructs the scale-to-zero plugin command.
package plugin

import (
	"context"
	"errors"

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
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/xataio/cnpg-i-scale-to-zero/internal/config"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/identity"
	lifecycleimpl "github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/lifecycle"
	pluginmetrics "github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/metrics"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/scraper"
	"github.com/xataio/cnpg-i-scale-to-zero/pkg/hibernation"
)

// Target identifies the cluster whose owning resource should be hibernated.
type Target = hibernation.Target

// Hibernator applies the mutations required to hibernate a target.
type Hibernator = hibernation.Hibernator

// HibernatorFactory constructs a hibernator using cached and direct clients.
type HibernatorFactory func(client.Client, client.Reader) Hibernator

// SchemeRegistration adds custom resource types to the plugin scheme.
type SchemeRegistration func(*runtime.Scheme) error

// Option configures the plugin command.
type Option func(*options)

type options struct {
	hibernatorFactory   HibernatorFactory
	schemeRegistrations []SchemeRegistration
}

// WithHibernatorFactory replaces the default CNPG hibernation behavior.
func WithHibernatorFactory(factory HibernatorFactory) Option {
	return func(options *options) {
		options.hibernatorFactory = factory
	}
}

// WithScheme registers custom resource types used by a hibernator.
func WithScheme(registration SchemeRegistration) Option {
	return func(options *options) {
		options.schemeRegistrations = append(options.schemeRegistrations, registration)
	}
}

// NewCommand creates the scale-to-zero plugin root command.
func NewCommand(pluginOptions ...Option) *cobra.Command {
	var options options
	for _, apply := range pluginOptions {
		apply(&options)
	}

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

	bindEnvironment()
	logFlags.AddFlags(rootCmd.PersistentFlags())
	rootCmd.AddCommand(newPluginCommand(options))
	return rootCmd
}

func bindEnvironment() {
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
}

func newPluginCommand(options options) *cobra.Command {
	cmd := http.CreateMainCmd(identity.Implementation{}, func(server *grpc.Server) error {
		lifecycle.RegisterOperatorLifecycleServer(server, lifecycleimpl.NewImplementation(newConfig()))
		return nil
	})

	cmd.Use = "plugin"
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg := newConfig()
		if err := startScraper(cmd.Context(), cfg.Scraper, cfg.MetricsAddress, options); err != nil {
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
		&config.ResourceConfig{
			CPURequest:    viper.GetString("sidecar-cpu-request"),
			CPULimit:      viper.GetString("sidecar-cpu-limit"),
			MemoryRequest: viper.GetString("sidecar-memory-request"),
			MemoryLimit:   viper.GetString("sidecar-memory-limit"),
		},
		config.NewScraperConfig(
			viper.GetString("scraper-interval"),
			viper.GetString("scraper-timeout"),
			viper.GetString("scraper-concurrency"),
			viper.GetString("sidecar-scrape-port"),
		),
	)
}

func startScraper(ctx context.Context, cfg config.ScraperConfig, metricsAddress string, options options) error {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
	for _, registration := range options.schemeRegistrations {
		if err := registration(scheme); err != nil {
			return err
		}
	}

	meterProvider, err := pluginmetrics.NewProvider(ctrlmetrics.Registry)
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Metrics: server.Options{BindAddress: metricsAddress},
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

	var scraperOptions []scraper.Option
	if options.hibernatorFactory != nil {
		hibernator := options.hibernatorFactory(mgr.GetClient(), mgr.GetAPIReader())
		if hibernator == nil {
			return errors.New("hibernator factory returned nil")
		}
		scraperOptions = append(scraperOptions, scraper.WithHibernator(hibernator))
	}
	s, err := scraper.New(
		mgr.GetClient(),
		nil,
		cfg,
		meterProvider.Meter("github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/scraper"),
		scraperOptions...,
	)
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
