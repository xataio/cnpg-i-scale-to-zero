package scraper

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/config"
	pluginmetrics "github.com/xataio/cnpg-i-scale-to-zero/internal/plugin/metrics"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/scaletozero"
	"github.com/xataio/cnpg-i-scale-to-zero/pkg/hibernation"
	"go.opentelemetry.io/otel/metric/noop"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestScraperActiveScrapePreventsHibernation(t *testing.T) {
	t.Parallel()

	kubeClient := fakeClient(
		enabledCluster("default", "cluster", "cluster-1", "10"),
		runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
	)
	s := newTestScraper(t, kubeClient, &fakeConnectionsClient{openConnections: 1}, testConfig())

	require.NoError(t, s.RunOnce(context.Background(), time.Now()))

	cluster := getCluster(t, kubeClient, "default", "cluster")
	require.NotEqual(t, scaletozero.HibernationAnnotationValueOn, cluster.Annotations[scaletozero.HibernationAnnotation])
}

func TestScraperZeroConnectionsHibernateAfterInactivityPeriod(t *testing.T) {
	t.Parallel()

	kubeClient := fakeClient(
		enabledCluster("default", "cluster", "cluster-1", "10"),
		runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
		scheduledBackup("default", "cluster"),
	)
	s := newTestScraper(t, kubeClient, &fakeConnectionsClient{openConnections: 0}, testConfig())
	now := time.Now()

	require.NoError(t, s.RunOnce(context.Background(), now))
	cluster := getCluster(t, kubeClient, "default", "cluster")
	require.NotEqual(t, scaletozero.HibernationAnnotationValueOn, cluster.Annotations[scaletozero.HibernationAnnotation])

	require.NoError(t, s.RunOnce(context.Background(), now.Add(11*time.Minute)))

	cluster = getCluster(t, kubeClient, "default", "cluster")
	require.Equal(t, scaletozero.HibernationAnnotationValueOn, cluster.Annotations[scaletozero.HibernationAnnotation])

	backup := &cnpgv1.ScheduledBackup{}
	require.NoError(t, kubeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cluster"}, backup))
	require.NotNil(t, backup.Spec.Suspend)
	require.True(t, *backup.Spec.Suspend)
}

func TestScraperDelegatesHibernationWithoutMutatingCNPGResources(t *testing.T) {
	t.Parallel()

	cluster := enabledCluster("default", "cluster", "cluster-1", "10")
	cluster.UID = "cluster-uid"
	cluster.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "xata.io/v1alpha1",
		Kind:       "Branch",
		Name:       "branch",
		UID:        "branch-uid",
		Controller: new(true),
	}}
	kubeClient := fakeClient(
		cluster,
		runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
		scheduledBackup("default", "cluster"),
	)
	hibernator := &recordingHibernator{}
	s := newTestScraper(
		t,
		kubeClient,
		&fakeConnectionsClient{openConnections: 0},
		testConfig(),
		WithHibernator(hibernator),
	)
	now := time.Now()

	require.NoError(t, s.RunOnce(context.Background(), now))
	require.NoError(t, s.RunOnce(context.Background(), now.Add(11*time.Minute)))

	require.Equal(t, hibernation.Target{
		Key:             types.NamespacedName{Namespace: "default", Name: "cluster"},
		UID:             "cluster-uid",
		OwnerReferences: cluster.OwnerReferences,
	}, hibernator.target)
	require.NotEqual(
		t,
		scaletozero.HibernationAnnotationValueOn,
		getCluster(t, kubeClient, "default", "cluster").Annotations[scaletozero.HibernationAnnotation],
	)
	backup := &cnpgv1.ScheduledBackup{}
	require.NoError(t, kubeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cluster"}, backup))
	require.Nil(t, backup.Spec.Suspend)
}

func TestDefaultHibernatorRejectsStaleTarget(t *testing.T) {
	t.Parallel()

	cluster := enabledCluster("default", "cluster", "cluster-1", "10")
	cluster.UID = "current-uid"
	kubeClient := fakeClient(cluster)
	hibernator := &defaultHibernator{client: kubeClient}

	err := hibernator.Hibernate(context.Background(), hibernation.Target{
		Key: types.NamespacedName{Namespace: "default", Name: "cluster"},
		UID: "stale-uid",
	})

	require.EqualError(t, err, "cluster UID changed")
	require.NotEqual(
		t,
		scaletozero.HibernationAnnotationValueOn,
		getCluster(t, kubeClient, "default", "cluster").Annotations[scaletozero.HibernationAnnotation],
	)
}

func TestScraperUnknownScrapeNeverHibernates(t *testing.T) {
	t.Parallel()

	kubeClient := fakeClient(
		enabledCluster("default", "cluster", "cluster-1", "10"),
		runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
	)
	probe := &fakeConnectionsClient{openConnections: 0}
	s := newTestScraper(t, kubeClient, probe, testConfig())
	now := time.Now()

	require.NoError(t, s.RunOnce(context.Background(), now))

	probe.err = errors.New("probe failed")
	require.NoError(t, s.RunOnce(context.Background(), now.Add(20*time.Minute)))
	cluster := getCluster(t, kubeClient, "default", "cluster")
	require.NotEqual(t, scaletozero.HibernationAnnotationValueOn, cluster.Annotations[scaletozero.HibernationAnnotation])

	probe.err = nil
	require.NoError(t, s.RunOnce(context.Background(), now.Add(21*time.Minute)))
	cluster = getCluster(t, kubeClient, "default", "cluster")
	require.NotEqual(t, scaletozero.HibernationAnnotationValueOn, cluster.Annotations[scaletozero.HibernationAnnotation])
}

func TestScraperRemovesInactivityStateForDeletedClusters(t *testing.T) {
	t.Parallel()

	cluster := enabledCluster("default", "cluster", "cluster-1", "10")
	kubeClient := fakeClient(
		cluster,
		runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
	)
	s := newTestScraper(t, kubeClient, &fakeConnectionsClient{openConnections: 0}, testConfig())
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}

	require.NoError(t, s.RunOnce(context.Background(), time.Now()))
	_, exists := s.getLastActive(key)
	require.True(t, exists)

	require.NoError(t, kubeClient.Delete(context.Background(), cluster))
	require.NoError(t, s.RunOnce(context.Background(), time.Now()))
	_, exists = s.getLastActive(key)
	require.False(t, exists)
}

func TestScraperSkipsUnsafeClusters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		objects []client.Object
	}{
		{
			name: "unhealthy cluster",
			objects: []client.Object{
				clusterWithPhase("default", "cluster", "cluster-1", "10", "Failing over", nil),
				runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
			},
		},
		{
			name: "unknown primary",
			objects: []client.Object{
				enabledCluster("default", "cluster", "", "10"),
			},
		},
		{
			name: "missing pod",
			objects: []client.Object{
				enabledCluster("default", "cluster", "cluster-1", "10"),
			},
		},
		{
			name: "non-running pod",
			objects: []client.Object{
				enabledCluster("default", "cluster", "cluster-1", "10"),
				primaryPod("default", "cluster", "cluster-1", "10.0.0.1", corev1.PodPending, true),
			},
		},
		{
			name: "empty pod ip",
			objects: []client.Object{
				enabledCluster("default", "cluster", "cluster-1", "10"),
				primaryPod("default", "cluster", "cluster-1", "", corev1.PodRunning, true),
			},
		},
		{
			name: "missing sidecar label",
			objects: []client.Object{
				enabledCluster("default", "cluster", "cluster-1", "10"),
				primaryPod("default", "cluster", "cluster-1", "10.0.0.1", corev1.PodRunning, false),
			},
		},
		{
			name: "already hibernated",
			objects: []client.Object{
				clusterWithPhase("default", "cluster", "cluster-1", "10", scaletozero.HealthyClusterStatus, map[string]string{scaletozero.HibernationAnnotation: scaletozero.HibernationAnnotationValueOn}),
				runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probe := &fakeConnectionsClient{openConnections: 0}
			s := newTestScraper(t, fakeClient(tc.objects...), probe, testConfig())
			now := time.Now()

			require.NoError(t, s.RunOnce(context.Background(), now))
			require.NoError(t, s.RunOnce(context.Background(), now.Add(20*time.Minute)))
			require.Zero(t, probe.calls)
		})
	}
}

func TestScraperUsesBoundedWorkers(t *testing.T) {
	kubeClient := fakeClient(scaleObjects(1000)...)
	release := make(chan struct{})
	probe := &fakeConnectionsClient{openConnections: 1, block: release}
	cfg := testConfig()
	cfg.Concurrency = 5
	s := newTestScraper(t, kubeClient, probe, cfg)

	baseline := goruntime.NumGoroutine()
	done := make(chan error, 1)
	go func() {
		done <- s.RunOnce(context.Background(), time.Now())
	}()

	require.Eventually(t, func() bool {
		return probe.callCount() == cfg.Concurrency
	}, time.Second, time.Millisecond)
	require.LessOrEqual(t, goruntime.NumGoroutine()-baseline, cfg.Concurrency+10)

	close(release)
	require.NoError(t, <-done)
	require.Equal(t, 1000, probe.calls)
}

func TestScraperSlowTargetsAreBoundedByTimeout(t *testing.T) {
	kubeClient := fakeClient(scaleObjects(20)...)
	probe := &fakeConnectionsClient{waitForContext: true}
	cfg := testConfig()
	cfg.Timeout = 20 * time.Millisecond
	cfg.Concurrency = 5
	s := newTestScraper(t, kubeClient, probe, cfg)

	start := time.Now()
	require.NoError(t, s.RunOnce(context.Background(), start))
	require.Less(t, time.Since(start), time.Second)
	require.LessOrEqual(t, probe.maxConcurrent, 5)
}

func TestScraperRecordsScrapeMetrics(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	provider, err := pluginmetrics.NewProvider(registry)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	probe := &fakeConnectionsClient{openConnections: 1}
	s, err := New(
		fakeClient(
			enabledCluster("default", "cluster", "cluster-1", "10"),
			runningPrimary("default", "cluster", "cluster-1", "10.0.0.1"),
		),
		probe,
		testConfig(),
		provider.Meter("test"),
	)
	require.NoError(t, err)

	require.NoError(t, s.RunOnce(context.Background(), time.Now()))
	probe.err = errors.New("probe failed")
	require.NoError(t, s.RunOnce(context.Background(), time.Now()))
	probe.err = nil
	probe.openConnections = 0
	now := time.Now()
	require.NoError(t, s.RunOnce(context.Background(), now))
	require.NoError(t, s.RunOnce(context.Background(), now.Add(11*time.Minute)))

	families, err := registry.Gather()
	require.NoError(t, err)

	metrics := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		metrics[family.GetName()] = family
	}
	require.NotContains(t, metrics, "otel_scope_info")

	duration := metrics["cnpg_scale_to_zero_scraper_scrape_duration_seconds"]
	require.NotNil(t, duration)
	require.Equal(t, map[string]uint64{scrapeResultSuccess: 3, scrapeResultError: 1}, histogramCountsByLabel(duration, scrapeResultAttribute))
	for _, current := range duration.Metric {
		require.Empty(t, labelValue(current, "otel_scope_name"))
		require.Empty(t, labelValue(current, "otel_scope_version"))
	}

	cycleDuration := metrics["cnpg_scale_to_zero_scraper_cycle_duration_seconds"]
	require.NotNil(t, cycleDuration)
	require.Equal(t, map[string]uint64{scrapeResultSuccess: 4}, histogramCountsByLabel(cycleDuration, scrapeResultAttribute))

	decisions := metrics["cnpg_scale_to_zero_scraper_cluster_decisions_total"]
	require.NotNil(t, decisions)
	require.Equal(t, map[string]float64{
		decisionActive:     1,
		decisionProbeError: 1,
		decisionInactive:   2,
	}, counterValuesByLabel(decisions, decisionAttribute))

	hibernateAttempts := metrics["cnpg_scale_to_zero_scraper_hibernations_total"]
	require.NotNil(t, hibernateAttempts)
	require.Equal(t, map[string]float64{scrapeResultSuccess: 1}, counterValuesByLabel(hibernateAttempts, scrapeResultAttribute))

	eligibleTargets := metrics["cnpg_scale_to_zero_scraper_scrape_targets"]
	require.NotNil(t, eligibleTargets)
	require.Equal(t, float64(1), gaugeValue(t, eligibleTargets))

	pendingInactiveClusters := metrics["cnpg_scale_to_zero_scraper_pending_inactive_clusters"]
	require.NotNil(t, pendingInactiveClusters)
	require.Equal(t, float64(0), gaugeValue(t, pendingInactiveClusters))
}

func TestHTTPConnectionsClientRejectsUnknownResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "non-200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "failed", http.StatusServiceUnavailable)
			},
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("not-json"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(tc.handler)
			defer server.Close()

			_, err := NewHTTPConnectionsClient(time.Second).GetConnections(context.Background(), server.URL)
			require.Error(t, err)
		})
	}
}

func TestHTTPConnectionsClientReturnsConnections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`3`))
	}))
	defer server.Close()

	connections, err := NewHTTPConnectionsClient(time.Second).GetConnections(context.Background(), server.URL)
	require.NoError(t, err)
	require.Equal(t, 3, connections)
}

func testConfig() config.ScraperConfig {
	return config.ScraperConfig{
		Interval:          time.Minute,
		Timeout:           2 * time.Second,
		Concurrency:       200,
		SidecarScrapePort: 9188,
	}
}

func newTestScraper(
	t *testing.T,
	kubeClient client.Client,
	connectionsClient ConnectionsClient,
	cfg config.ScraperConfig,
	options ...Option,
) *Scraper {
	t.Helper()
	s, err := New(kubeClient, connectionsClient, cfg, noop.NewMeterProvider().Meter("test"), options...)
	require.NoError(t, err)
	return s
}

type recordingHibernator struct {
	target hibernation.Target
}

func (h *recordingHibernator) Hibernate(_ context.Context, target hibernation.Target) error {
	h.target = target
	return nil
}

func histogramCountsByLabel(family *dto.MetricFamily, label string) map[string]uint64 {
	result := make(map[string]uint64, len(family.Metric))
	for _, current := range family.Metric {
		result[labelValue(current, label)] = current.GetHistogram().GetSampleCount()
	}
	return result
}

func counterValuesByLabel(family *dto.MetricFamily, label string) map[string]float64 {
	result := make(map[string]float64, len(family.Metric))
	for _, current := range family.Metric {
		result[labelValue(current, label)] = current.GetCounter().GetValue()
	}
	return result
}

func gaugeValue(t *testing.T, family *dto.MetricFamily) float64 {
	t.Helper()
	require.Len(t, family.Metric, 1)
	return family.Metric[0].GetGauge().GetValue()
}

func labelValue(current *dto.Metric, name string) string {
	for _, label := range current.Label {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func fakeClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func enabledCluster(namespace, name, primary, inactivityMinutes string) *cnpgv1.Cluster {
	return clusterWithPhase(namespace, name, primary, inactivityMinutes, scaletozero.HealthyClusterStatus, nil)
}

func clusterWithPhase(namespace, name, primary, inactivityMinutes, phase string, extraAnnotations map[string]string) *cnpgv1.Cluster {
	annotations := map[string]string{
		scaletozero.EnabledAnnotation:    scaletozero.EnabledAnnotationTrue,
		scaletozero.InactivityAnnotation: inactivityMinutes,
	}
	for key, value := range extraAnnotations {
		annotations[key] = value
	}

	return &cnpgv1.Cluster{
		ObjectMeta: objectMeta(namespace, name, nil, annotations),
		Status: cnpgv1.ClusterStatus{
			Phase:          phase,
			CurrentPrimary: primary,
		},
	}
}

func runningPrimary(namespace, cluster, name, ip string) *corev1.Pod {
	return primaryPod(namespace, cluster, name, ip, corev1.PodRunning, true)
}

func primaryPod(namespace, cluster, name, ip string, phase corev1.PodPhase, sidecarLabel bool) *corev1.Pod {
	labels := map[string]string{
		scaletozero.ClusterLabel: cluster,
	}
	if sidecarLabel {
		labels[scaletozero.SidecarLabel] = scaletozero.SidecarLabelTrue
	}
	return &corev1.Pod{
		ObjectMeta: objectMeta(namespace, name, labels, nil),
		Status: corev1.PodStatus{
			Phase: phase,
			PodIP: ip,
		},
	}
}

func scheduledBackup(namespace, name string) *cnpgv1.ScheduledBackup {
	return &cnpgv1.ScheduledBackup{
		ObjectMeta: objectMeta(namespace, name, nil, nil),
		Spec: cnpgv1.ScheduledBackupSpec{
			Cluster: cnpgv1.LocalObjectReference{Name: name},
		},
	}
}

func objectMeta(namespace, name string, labels, annotations map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace:   namespace,
		Name:        name,
		Labels:      labels,
		Annotations: annotations,
	}
}

func getCluster(t *testing.T, kubeClient client.Client, namespace, name string) *cnpgv1.Cluster {
	t.Helper()
	cluster := &cnpgv1.Cluster{}
	require.NoError(t, kubeClient.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, cluster))
	return cluster
}

func scaleObjects(count int) []client.Object {
	objects := make([]client.Object, 0, count*2)
	for i := 0; i < count; i++ {
		clusterName := fmt.Sprintf("cluster-%d", i)
		podName := fmt.Sprintf("%s-1", clusterName)
		ip := fmt.Sprintf("10.0.%d.%d", i/250, i%250+1)
		objects = append(objects,
			enabledCluster("default", clusterName, podName, "10"),
			runningPrimary("default", clusterName, podName, ip),
		)
	}
	return objects
}

type fakeConnectionsClient struct {
	mu              sync.Mutex
	openConnections int
	err             error
	waitForContext  bool
	block           <-chan struct{}
	calls           int
	current         int
	maxConcurrent   int
}

func (c *fakeConnectionsClient) GetConnections(ctx context.Context, url string) (int, error) {
	c.mu.Lock()
	c.calls++
	c.current++
	if c.current > c.maxConcurrent {
		c.maxConcurrent = c.current
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.current--
		c.mu.Unlock()
	}()

	if c.waitForContext {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if c.err != nil {
		return 0, c.err
	}
	return c.openConnections, nil
}

func (c *fakeConnectionsClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
