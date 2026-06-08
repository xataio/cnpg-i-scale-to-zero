package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/config"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/scaletozero"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	scrapeResultAttribute = "result"
	scrapeResultSuccess   = "success"
	scrapeResultError     = "error"

	decisionAttribute         = "reason"
	decisionDisabled          = "disabled"
	decisionAlreadyHibernated = "already_hibernated"
	decisionUnhealthy         = "unhealthy"
	decisionNotScrapeable     = "not_scrapeable"
	decisionProbeError        = "probe_error"
	decisionActive            = "active"
	decisionInactive          = "inactive"
)

type ConnectionsClient interface {
	GetConnections(ctx context.Context, url string) (int, error)
}

type HTTPConnectionsClient struct {
	client *http.Client
}

func NewHTTPConnectionsClient(timeout time.Duration) *HTTPConnectionsClient {
	return &HTTPConnectionsClient{
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *HTTPConnectionsClient) GetConnections(ctx context.Context, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("connections probe returned status %d", resp.StatusCode)
	}

	var result int
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return result, nil
}

type Scraper struct {
	client                  client.Client
	connectionsClient       ConnectionsClient
	cfg                     config.ScraperConfig
	scrapeDuration          metric.Float64Histogram
	cycleDuration           metric.Float64Histogram
	clusterDecisions        metric.Int64Counter
	hibernateAttempts       metric.Int64Counter
	eligibleTargets         metric.Int64Gauge
	pendingInactiveClusters metric.Int64Gauge

	mu         sync.Mutex
	lastActive map[types.NamespacedName]time.Time
}

type clusterResult struct {
	decision         string
	eligible         bool
	inactivityWindow bool
}

func New(kubeClient client.Client, connectionsClient ConnectionsClient, cfg config.ScraperConfig, meter metric.Meter) (*Scraper, error) {
	cfg = cfg.WithDefaults()
	if connectionsClient == nil {
		connectionsClient = NewHTTPConnectionsClient(cfg.Timeout)
	}

	scrapeDuration, err := meter.Float64Histogram(
		"cnpg_scale_to_zero_scraper_scrape_duration",
		metric.WithDescription("Duration of sidecar connection scrapes"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(.005, .01, .025, .05, .1, .25, .5, 1, 2, 5),
	)
	if err != nil {
		return nil, fmt.Errorf("create scrape duration histogram: %w", err)
	}
	cycleDuration, err := meter.Float64Histogram(
		"cnpg_scale_to_zero_scraper_cycle_duration",
		metric.WithDescription("Duration of complete scraper cycles"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(.1, .25, .5, 1, 2, 5, 10, 30, 60),
	)
	if err != nil {
		return nil, fmt.Errorf("create cycle duration histogram: %w", err)
	}
	clusterDecisions, err := meter.Int64Counter(
		"cnpg_scale_to_zero_scraper_cluster_decisions",
		metric.WithDescription("Number of cluster scrape decisions"),
	)
	if err != nil {
		return nil, fmt.Errorf("create cluster decisions counter: %w", err)
	}
	hibernateAttempts, err := meter.Int64Counter(
		"cnpg_scale_to_zero_scraper_hibernations",
		metric.WithDescription("Number of cluster hibernations"),
	)
	if err != nil {
		return nil, fmt.Errorf("create hibernations counter: %w", err)
	}
	eligibleTargets, err := meter.Int64Gauge(
		"cnpg_scale_to_zero_scraper_scrape_targets",
		metric.WithDescription("Number of sidecar scrape targets in the latest cycle"),
	)
	if err != nil {
		return nil, fmt.Errorf("create scrape targets gauge: %w", err)
	}
	pendingInactiveClusters, err := meter.Int64Gauge(
		"cnpg_scale_to_zero_scraper_pending_inactive_clusters",
		metric.WithDescription("Number of inactive clusters pending hibernation after the latest cycle"),
	)
	if err != nil {
		return nil, fmt.Errorf("create pending inactive clusters gauge: %w", err)
	}

	return &Scraper{
		client:                  kubeClient,
		connectionsClient:       connectionsClient,
		cfg:                     cfg,
		scrapeDuration:          scrapeDuration,
		cycleDuration:           cycleDuration,
		clusterDecisions:        clusterDecisions,
		hibernateAttempts:       hibernateAttempts,
		eligibleTargets:         eligibleTargets,
		pendingInactiveClusters: pendingInactiveClusters,
		lastActive:              make(map[types.NamespacedName]time.Time),
	}, nil
}

func (s *Scraper) Start(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			start := time.Now()
			if err := s.RunOnce(ctx, start); err != nil {
				log.FromContext(ctx).Error(err, "scrape tick failed")
			}
			drainTicker(ticker)
		}
	}
}

func drainTicker(ticker *time.Ticker) {
	for {
		select {
		case <-ticker.C:
		default:
			return
		}
	}
}

func (s *Scraper) RunOnce(ctx context.Context, now time.Time) (runErr error) {
	cycleStart := time.Now()
	defer func() {
		result := scrapeResultSuccess
		if runErr != nil {
			result = scrapeResultError
		}
		s.cycleDuration.Record(
			ctx,
			time.Since(cycleStart).Seconds(),
			metric.WithAttributes(attribute.String(scrapeResultAttribute, result)),
		)
	}()

	clusters := &cnpgv1.ClusterList{}
	if err := s.client.List(ctx, clusters); err != nil {
		return fmt.Errorf("list clusters: %w", err)
	}

	sem := make(chan struct{}, s.cfg.Concurrency)
	results := make(chan clusterResult, len(clusters.Items))
	var wg sync.WaitGroup

	for i := range clusters.Items {
		cluster := clusters.Items[i].DeepCopy()
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results <- s.processCluster(ctx, cluster, now)
		}()
	}

	wg.Wait()
	close(results)

	var eligibleTargets, pendingInactiveClusters int64
	for result := range results {
		s.clusterDecisions.Add(
			ctx,
			1,
			metric.WithAttributes(attribute.String(decisionAttribute, result.decision)),
		)
		if result.eligible {
			eligibleTargets++
		}
		if result.inactivityWindow {
			pendingInactiveClusters++
		}
	}
	s.eligibleTargets.Record(ctx, eligibleTargets)
	s.pendingInactiveClusters.Record(ctx, pendingInactiveClusters)
	return nil
}

func (s *Scraper) processCluster(ctx context.Context, cluster *cnpgv1.Cluster, now time.Time) clusterResult {
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
	logger := log.FromContext(ctx).WithValues("namespace", cluster.Namespace, "cluster", cluster.Name)

	cfg := getClusterScaleToZeroConfig(cluster)
	if !cfg.enabled {
		s.clearLastActive(key)
		return clusterResult{decision: decisionDisabled}
	}

	if cluster.Annotations != nil && cluster.Annotations[scaletozero.HibernationAnnotation] == scaletozero.HibernationAnnotationValueOn {
		s.clearLastActive(key)
		return clusterResult{decision: decisionAlreadyHibernated}
	}
	if cluster.Status.Phase != scaletozero.HealthyClusterStatus {
		s.clearLastActive(key)
		logger.Info("cluster is not healthy, skipping hibernation", "phase", cluster.Status.Phase)
		return clusterResult{decision: decisionUnhealthy}
	}
	if cluster.Status.CurrentPrimary == "" {
		s.clearLastActive(key)
		logger.Info("cluster has no current primary, skipping hibernation")
		return clusterResult{decision: decisionNotScrapeable}
	}

	pod := &corev1.Pod{}
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Status.CurrentPrimary}, pod); err != nil {
		s.clearLastActive(key)
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "primary pod cache lookup error")
		}
		return clusterResult{decision: decisionNotScrapeable}
	}

	if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" || pod.Labels[scaletozero.SidecarLabel] != scaletozero.SidecarLabelTrue {
		s.clearLastActive(key)
		logger.Info("primary pod is not scrapeable", "pod", pod.Name, "phase", pod.Status.Phase, "podIP", pod.Status.PodIP)
		return clusterResult{decision: decisionNotScrapeable}
	}

	result := clusterResult{eligible: true}
	scrapeCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	scrapeStart := time.Now()
	openConnections, err := s.connectionsClient.GetConnections(scrapeCtx, fmt.Sprintf("http://%s:%d/connections", pod.Status.PodIP, s.cfg.SidecarScrapePort))
	scrapeResult := scrapeResultSuccess
	if err != nil {
		scrapeResult = scrapeResultError
	}
	metricOptions := metric.WithAttributes(attribute.String(scrapeResultAttribute, scrapeResult))
	s.scrapeDuration.Record(ctx, time.Since(scrapeStart).Seconds(), metricOptions)
	if err != nil {
		s.clearLastActive(key)
		logger.Error(err, "sidecar connection scrape error", "pod", pod.Name)
		result.decision = decisionProbeError
		return result
	}

	if openConnections > 0 {
		s.setLastActive(key, now)
		result.decision = decisionActive
		return result
	}

	result.decision = decisionInactive
	result.inactivityWindow = true
	lastActive, exists := s.getLastActive(key)
	if !exists {
		s.setLastActive(key, now)
		return result
	}

	if now.Sub(lastActive) < time.Duration(cfg.inactivityMinutes)*time.Minute {
		return result
	}

	if err := s.hibernate(ctx, cluster); err != nil {
		s.hibernateAttempts.Add(ctx, 1, metric.WithAttributes(attribute.String(scrapeResultAttribute, scrapeResultError)))
		logger.Error(err, "hibernation failed")
		return result
	}
	s.hibernateAttempts.Add(ctx, 1, metric.WithAttributes(attribute.String(scrapeResultAttribute, scrapeResultSuccess)))
	result.inactivityWindow = false
	if err := s.pauseScheduledBackup(ctx, cluster); err != nil {
		logger.Error(err, "scheduled backup pause error")
	}
	return result
}

func (s *Scraper) hibernate(ctx context.Context, cluster *cnpgv1.Cluster) error {
	latest := &cnpgv1.Cluster{}
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
	if err := s.client.Get(ctx, key, latest); err != nil {
		return fmt.Errorf("retrieve cluster: %w", err)
	}

	if latest.Status.Phase != scaletozero.HealthyClusterStatus {
		return nil
	}
	if latest.Annotations != nil && latest.Annotations[scaletozero.HibernationAnnotation] == scaletozero.HibernationAnnotationValueOn {
		return nil
	}

	patchBase := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = make(map[string]string)
	}
	latest.Annotations[scaletozero.HibernationAnnotation] = scaletozero.HibernationAnnotationValueOn
	return s.client.Patch(ctx, latest, client.MergeFrom(patchBase))
}

func (s *Scraper) pauseScheduledBackup(ctx context.Context, cluster *cnpgv1.Cluster) error {
	scheduledBackup := &cnpgv1.ScheduledBackup{}
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
	if err := s.client.Get(ctx, key, scheduledBackup); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get scheduled backup: %w", err)
	}

	patchBase := scheduledBackup.DeepCopy()
	scheduledBackup.Spec.Suspend = ptr.To(true)
	return s.client.Patch(ctx, scheduledBackup, client.MergeFrom(patchBase))
}

func (s *Scraper) getLastActive(key types.NamespacedName) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lastActive, exists := s.lastActive[key]
	return lastActive, exists
}

func (s *Scraper) setLastActive(key types.NamespacedName, value time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActive[key] = value
}

func (s *Scraper) clearLastActive(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lastActive, key)
}

type clusterScaleToZeroConfig struct {
	enabled           bool
	inactivityMinutes int
}

func getClusterScaleToZeroConfig(cluster *cnpgv1.Cluster) clusterScaleToZeroConfig {
	result := clusterScaleToZeroConfig{
		inactivityMinutes: scaletozero.DefaultInactivityMinutes,
	}
	if cluster.Annotations == nil {
		return result
	}

	if cluster.Annotations[scaletozero.EnabledAnnotation] == scaletozero.EnabledAnnotationTrue {
		result.enabled = true
	}
	if value, exists := cluster.Annotations[scaletozero.InactivityAnnotation]; exists {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			result.inactivityMinutes = parsed
		}
	}

	return result
}
