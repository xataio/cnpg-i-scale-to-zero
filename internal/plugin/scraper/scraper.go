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
	"github.com/xataio/cnpg-i-scale-to-zero/pkg/hibernation"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	hibernator              hibernation.Hibernator

	mu         sync.Mutex
	lastActive map[types.NamespacedName]time.Time
}

type Option func(*Scraper)

// WithHibernator replaces the default CNPG hibernation behavior.
func WithHibernator(hibernator hibernation.Hibernator) Option {
	return func(scraper *Scraper) {
		scraper.hibernator = hibernator
	}
}

type clusterResult struct {
	decision         string
	eligible         bool
	inactivityWindow bool
}

func New(kubeClient client.Client, connectionsClient ConnectionsClient, cfg config.ScraperConfig, meter metric.Meter, options ...Option) (*Scraper, error) {
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

	result := &Scraper{
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
	}
	result.hibernator = &defaultHibernator{client: kubeClient}
	for _, apply := range options {
		apply(result)
	}
	return result, nil
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
	s.pruneLastActive(clusters.Items)

	// A fixed worker pool bounds goroutine and request growth when one cycle
	// contains tens of thousands of clusters.
	workerCount := min(s.cfg.Concurrency, len(clusters.Items))
	jobs := make(chan *cnpgv1.Cluster)
	results := make(chan clusterResult, workerCount)
	var wg sync.WaitGroup

	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cluster := range jobs {
				results <- s.processCluster(ctx, cluster, now)
			}
		}()
	}

	go func() {
		for i := range clusters.Items {
			jobs <- &clusters.Items[i]
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

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

// processCluster clears pending inactivity whenever activity cannot be
// determined reliably. Hibernation requires consecutive successful scrapes.
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
	return result
}

func (s *Scraper) hibernate(ctx context.Context, cluster *cnpgv1.Cluster) error {
	// The cluster list came from the cache at the start of the cycle. Re-read it
	// before mutation so a stale scrape cannot hibernate a changed cluster.
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

	return s.hibernator.Hibernate(ctx, hibernation.Target{
		Key:             key,
		UID:             latest.UID,
		OwnerReferences: append([]metav1.OwnerReference(nil), latest.OwnerReferences...),
	})
}

type defaultHibernator struct {
	client client.Client
}

func (h *defaultHibernator) Hibernate(ctx context.Context, target hibernation.Target) error {
	cluster := &cnpgv1.Cluster{}
	if err := h.client.Get(ctx, target.Key, cluster); err != nil {
		return fmt.Errorf("retrieve cluster: %w", err)
	}
	if cluster.UID != target.UID {
		return fmt.Errorf("cluster UID changed")
	}
	if cluster.Status.Phase != scaletozero.HealthyClusterStatus {
		return nil
	}
	if cluster.Annotations != nil && cluster.Annotations[scaletozero.HibernationAnnotation] == scaletozero.HibernationAnnotationValueOn {
		return nil
	}

	patchBase := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}
	cluster.Annotations[scaletozero.HibernationAnnotation] = scaletozero.HibernationAnnotationValueOn
	if err := h.client.Patch(ctx, cluster, client.MergeFrom(patchBase)); err != nil {
		return err
	}

	scheduledBackup := &cnpgv1.ScheduledBackup{}
	if err := h.client.Get(ctx, target.Key, scheduledBackup); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.FromContext(ctx).Error(err, "scheduled backup lookup error")
		return nil
	}

	backupPatchBase := scheduledBackup.DeepCopy()
	scheduledBackup.Spec.Suspend = ptr.To(true)
	if err := h.client.Patch(ctx, scheduledBackup, client.MergeFrom(backupPatchBase)); err != nil {
		log.FromContext(ctx).Error(err, "scheduled backup pause error")
	}
	return nil
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

func (s *Scraper) pruneLastActive(clusters []cnpgv1.Cluster) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The inactivity map outlives each cache list, so deleted clusters must be
	// removed explicitly.
	stale := make(map[types.NamespacedName]struct{}, len(s.lastActive))
	for key := range s.lastActive {
		stale[key] = struct{}{}
	}
	for i := range clusters {
		delete(stale, types.NamespacedName{
			Namespace: clusters[i].Namespace,
			Name:      clusters[i].Name,
		})
	}
	for key := range stale {
		delete(s.lastActive, key)
	}
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
