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
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
		scraperManager, err := newScraperManager(cmd.Context(), cfg.Scraper, cfg.MetricsAddress, options)
		if err != nil {
			return err
		}
		return runPlugin(
			cmd.Context(),
			func(ctx context.Context) error {
				cmd.SetContext(ctx)
				return originalRunE(cmd, args)
			},
			scraperManager.Start,
		)
	}
	return cmd
}

func runPlugin(
	ctx context.Context,
	serve func(context.Context) error,
	scrape func(context.Context) error,
) error {
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return serve(ctx)
	})
	group.Go(func() error {
		return scrape(ctx)
	})
	return group.Wait()
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

func newScraperManager(
	ctx context.Context,
	cfg config.ScraperConfig,
	metricsAddress string,
	options options,
) (manager.Manager, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
	for _, registration := range options.schemeRegistrations {
		if err := registration(scheme); err != nil {
			return nil, err
		}
	}

	meterProvider, err := pluginmetrics.NewProvider(ctrlmetrics.Registry)
	if err != nil {
		return nil, err
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Metrics: server.Options{BindAddress: metricsAddress},
	})
	if err != nil {
		return nil, err
	}
	for _, object := range []client.Object{
		&cnpgv1.Cluster{},
		&cnpgv1.ScheduledBackup{},
		&corev1.Pod{},
	} {
		if _, err := mgr.GetCache().GetInformer(ctx, object); err != nil {
			return nil, err
		}
	}

	var scraperOptions []scraper.Option
	if options.hibernatorFactory != nil {
		hibernator := options.hibernatorFactory(mgr.GetClient(), mgr.GetAPIReader())
		if hibernator == nil {
			return nil, errors.New("hibernator factory returned nil")
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
		return nil, err
	}
	if err := mgr.Add(managerRunnable{fn: s.Start}); err != nil {
		return nil, err
	}
	if err := mgr.Add(managerRunnable{fn: func(ctx context.Context) error {
		<-ctx.Done()
		return meterProvider.Shutdown(context.WithoutCancel(ctx))
	}}); err != nil {
		return nil, err
	}

	return mgr, nil
}

type managerRunnable struct {
	fn func(context.Context) error
}

func (r managerRunnable) Start(ctx context.Context) error {
	return r.fn(ctx)
}
