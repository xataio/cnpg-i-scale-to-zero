package sidecar

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"syscall"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/machinery/pkg/log"
	_ "github.com/lib/pq"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/postgres"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// scaleToZero manages the scale to zero functionality for a CloudNativePG
// cluster.
type scaleToZero struct {
	client           clusterClient
	pgQuerier        postgres.Querier
	pgQuerierFactory func(ctx context.Context, url string) (postgres.Querier, error)

	currentPodName string
	clusterName    string

	checkInterval time.Duration
	lastActive    time.Time
}

type clusterClient interface {
	getCluster(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error)
	updateCluster(ctx context.Context, cluster *cnpgv1.Cluster) error
	getClusterCredentials(ctx context.Context) (*postgreSQLCredentials, error)
	getClusterScheduledBackup(ctx context.Context) (*cnpgv1.ScheduledBackup, error)
	updateClusterScheduledBackup(ctx context.Context, scheduledBackup *cnpgv1.ScheduledBackup) error
}

type config struct {
	podName    string
	clusterKey types.NamespacedName
}

const (
	healthyClusterStatus  = "Cluster in healthy state"
	hibernationAnnotation = "cnpg.io/hibernation"

	scaleToZeroEnabledAnnotation = "xata.io/scale-to-zero-enabled"
	inactivityMinutesAnnotation  = "xata.io/scale-to-zero-inactivity-minutes"

	defaultInactivityMinutes = 30              // default inactivity minutes if not set in the annotation
	defaultCheckInterval     = 1 * time.Minute // default check interval for cluster activity
)

var errReplicaInstance = errors.New("current pod is not the primary instance")

// newScaleToZero creates a new scaleToZero instance with the provided configuration and client.
func newScaleToZero(ctx context.Context, cfg config, client client.Client) (*scaleToZero, error) {
	s := &scaleToZero{
		client:         newClusterClient(client, cfg.clusterKey, defaultRefreshInterval),
		currentPodName: cfg.podName,
		clusterName:    cfg.clusterKey.Name,
		checkInterval:  defaultCheckInterval,
		pgQuerierFactory: func(ctx context.Context, url string) (postgres.Querier, error) {
			return postgres.NewConnPool(ctx, url)
		},
		lastActive: time.Time{},
	}

	if err := s.initQuerier(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize PostgreSQL querier: %w", err)
	}

	return s, nil
}

// Start starts the scale to zero sidecar
// It periodically checks if the cluster is active and hibernates it if not.
func (s *scaleToZero) Start(ctx context.Context) error {
	contextLogger := log.FromContext(ctx)

	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cluster, err := s.client.getCluster(ctx, doNotForceUpdate)
			if err != nil {
				contextLogger.Error(err, "failed to get cluster")
				continue
			}

			// only the primary keeps track of activity and hibernation
			if !s.isPrimary(cluster) {
				// reset last active time when it's not the primary to make sure
				// when there's a switchover, the new primary has a clean state
				s.lastActive = time.Time{}
				contextLogger.Info("running on non-primary pod, skipping activity monitoring", "primary", cluster.Status.CurrentPrimary)
				continue
			}

			scaleToZeroConfig := s.getClusterScaleToZeroConfig(ctx, cluster)

			if !scaleToZeroConfig.enabled {
				// reset last active time if scale to zero is disabled. This
				// prevents old activity tracking from kicking in when scale to
				// zero is re-enabled.
				s.lastActive = time.Time{}
				contextLogger.Info("scale to zero is disabled, skipping check")
				continue
			}

			isActive, err := s.isClusterActive(ctx, scaleToZeroConfig.inactivityMinutes)
			if err != nil {
				contextLogger.Error(err, "failed to check cluster activity")
				continue
			}

			if !isActive {
				if err := s.hibernate(ctx); err != nil {
					contextLogger.Error(err, "hibernation failed")
					// we stop the scale to zero sidecar if this is not the primary instance
					if errors.Is(err, errReplicaInstance) {
						return nil
					}
					// if hibernation fails, do not try pausing the scheduled backup
					continue
				}

				// pause the scheduled backup if the cluster is hibernated
				if err := s.pauseScheduledBackup(ctx); err != nil {
					contextLogger.Error(err, "failed to pause scheduled backup")
				}
			}
		}
	}
}

func (s *scaleToZero) Stop(ctx context.Context) {
	if s.pgQuerier != nil {
		if err := s.pgQuerier.Close(ctx); err != nil {
			log.FromContext(ctx).Error(err, "failed to close PostgreSQL querier")
		}
	}
}

func (s *scaleToZero) initQuerier(ctx context.Context) error {
	if s.pgQuerier != nil {
		// close the existing querier before reinitializing
		_ = s.pgQuerier.Close(ctx)
	}

	// refresh the credentials
	credentials, err := s.client.getClusterCredentials(ctx)
	if err != nil {
		return err
	}

	s.pgQuerier, err = s.pgQuerierFactory(ctx, credentials.connString())
	return err
}

func (s *scaleToZero) isPrimary(cluster *cnpgv1.Cluster) bool {
	// when the cluster is first initialised, the current primary might not be
	// set yet. Assume it's the primary if it's not set to avoid blocking the
	// scale to zero checks.
	return cluster.Status.CurrentPrimary == "" || (cluster.Status.CurrentPrimary == s.currentPodName)
}

// isClusterActive checks if the cluster has any open connections.
func (s *scaleToZero) isClusterActive(ctx context.Context, inactivityMinutes int) (bool, error) {
	openConns, err := s.openConnections(ctx)
	if err != nil {
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			// try reseting the conn pool with updated credentials
			if err := s.initQuerier(ctx); err != nil {
				return false, fmt.Errorf("failed to reinitialize PostgreSQL querier: %w", err)
			}
			openConns, err = s.openConnections(ctx)
			if err != nil {
				return false, fmt.Errorf("failed to query open connections after reinitialization: %w", err)
			}
		default:
			return false, fmt.Errorf("failed to query open connections: %w", err)
		}
	}
	log.FromContext(ctx).Info("open connections count", "count", openConns)

	// if there are open connections or if the last active time is not set, we
	// consider the cluster active. The last active time not being set means
	// either the sidecar has just started or the scale to zero setting has been
	// re-enabled and we need to restart the activity tracking.
	if openConns > 0 || s.lastActive.IsZero() {
		s.lastActive = time.Now()
		return true, nil
	}

	log.FromContext(ctx).Debug("time since last active", "duration", time.Since(s.lastActive).String())
	if time.Since(s.lastActive).Minutes() >= float64(inactivityMinutes) {
		return false, nil
	}

	return true, nil
}

// openConnections queries the PostgreSQL database to count the number of open connections.
func (s *scaleToZero) openConnections(ctx context.Context) (int, error) {
	const query = `SELECT COUNT(*) FROM pg_stat_activity WHERE state IN ('active', 'idle', 'idle in transaction') AND pg_backend_pid() != pg_stat_activity.pid AND usename != 'streaming_replica';`
	var count int
	if err := s.pgQuerier.QueryRow(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to query open connections: %w", err)
	}

	return count, nil
}

// hibernate attempts to hibernate the cluster.
// If the current pod is not the primary instance, it returns an error.
// If the cluster is not healthy, it skips hibernation.
// If the cluster is already hibernated, it does nothing.
// If successful, it adds the hibernation annotation to the cluster.
// Returns an error if the operation fails.
func (s *scaleToZero) hibernate(ctx context.Context) error {
	cluster, err := s.client.getCluster(ctx, forceUpdate)
	if err != nil {
		return fmt.Errorf("failed to retrieve cluster: %w", err)
	}

	if cluster.Status.Phase != healthyClusterStatus {
		log.FromContext(ctx).Info("cluster is not healthy, skipping hibernation", "status", cluster.Status.Phase)
		return nil
	}
	if cluster.Annotations != nil && cluster.Annotations[hibernationAnnotation] == "on" {
		log.FromContext(ctx).Info("cluster is already hibernated")
		return nil
	}

	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}

	// hibernate the cluster by adding the annotation
	cluster.Annotations[hibernationAnnotation] = "on"
	log.FromContext(ctx).Info("annotating cluster for hibernation", "pod", s.currentPodName, "cluster", cluster.Name)
	if err := s.client.updateCluster(ctx, cluster); err != nil {
		log.FromContext(ctx).Error(err, "failed to annotate cluster for hibernation")
		return err
	}

	return nil
}

// getClusterScaleToZeroConfig retrieves the scale to zero configuration from
// the cluster annotations. It returns the enabled status and inactivity
// minutes. If the annotation is not set, it uses default values.
func (s *scaleToZero) getClusterScaleToZeroConfig(ctx context.Context, cluster *cnpgv1.Cluster) *scaleToZeroConfig {
	enabled := false
	inactivityMinutes := defaultInactivityMinutes

	if value, exists := cluster.Annotations[scaleToZeroEnabledAnnotation]; exists && value == "true" {
		enabled = true
	}

	if value, exists := cluster.Annotations[inactivityMinutesAnnotation]; exists {
		var err error
		inactivityMinutes, err = strconv.Atoi(value)
		if err != nil {
			log.FromContext(ctx).Error(err, "invalid inactivity minutes annotation, using default value", "default", defaultInactivityMinutes)
			inactivityMinutes = defaultInactivityMinutes
		}
	}

	return &scaleToZeroConfig{
		enabled:           enabled,
		inactivityMinutes: inactivityMinutes,
	}
}

func (s *scaleToZero) pauseScheduledBackup(ctx context.Context) error {
	scheduledBackup, err := s.client.getClusterScheduledBackup(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.FromContext(ctx).Debug("scheduled backup not found, skipping pause")
			return nil
		}

		return fmt.Errorf("failed to get scheduled backup for cluster %s: %w", s.clusterName, err)
	}

	log.FromContext(ctx).Info("pausing scheduled backup", "cluster", s.clusterName)
	scheduledBackup.Spec.Suspend = ptr.To(true)
	if err := s.client.updateClusterScheduledBackup(ctx, scheduledBackup); err != nil {
		return fmt.Errorf("failed to update scheduled backup for cluster %s: %w", s.clusterName, err)
	}

	return nil
}
