package sidecar

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// clusterRetriever is responsible for retrieving the CloudNativePG cluster and
// its credentials. It caches the cluster results to avoid frequent API calls.
// The refresh interval is configurable.
type cnpgClusterClient struct {
	client     client.Client
	clusterKey types.NamespacedName

	cluster                *cnpgv1.Cluster
	lastClusterUpdate      time.Time
	clusterRefreshInterval time.Duration
}

// postgreSQLCredentials holds the connection information for PostgreSQL
type postgreSQLCredentials struct {
	username string
	database string
	host     string
	port     string
}

type scaleToZeroConfig struct {
	enabled           bool
	inactivityMinutes int
}

const (
	defaultRefreshInterval = 30 * time.Second

	forceUpdate      = true
	doNotForceUpdate = false
)

// newClusterClient creates a new instance of clusterClient with the provided
// cnpg client and cluster key. It initializes the refresh interval to a default
// value if not provided.
func newClusterClient(client client.Client, clusterKey types.NamespacedName, refreshInterval time.Duration) *cnpgClusterClient {
	if refreshInterval == 0 {
		refreshInterval = defaultRefreshInterval
	}

	return &cnpgClusterClient{
		client:                 client,
		clusterKey:             clusterKey,
		clusterRefreshInterval: refreshInterval,
	}
}

func (r *cnpgClusterClient) updateCluster(ctx context.Context, cluster *cnpgv1.Cluster) error {
	return r.client.Update(ctx, cluster)
}

// getCluster retrieves the CloudNativePG cluster object, refreshing it if necessary
func (r *cnpgClusterClient) getCluster(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
	if !forceUpdate && time.Since(r.lastClusterUpdate) < r.clusterRefreshInterval {
		return r.cluster, nil
	}

	cluster := &cnpgv1.Cluster{}
	if err := r.client.Get(ctx, r.clusterKey, cluster); err != nil {
		return nil, err
	}

	r.cluster = cluster
	r.lastClusterUpdate = time.Now()
	return r.cluster, nil
}

// socketDir is the Unix socket directory used by CNPG for the PostgreSQL
// server. The sidecar container, running with the same UID as the postgres
// container, authenticates as the "postgres" role via peer auth over this
// socket — no password required.
const socketDir = "/controller/run"

func (r *cnpgClusterClient) getClusterCredentials(_ context.Context) (*postgreSQLCredentials, error) {
	return &postgreSQLCredentials{
		username: "postgres",
		database: "postgres",
		host:     socketDir,
		port:     "5432",
	}, nil
}

func (r *cnpgClusterClient) getClusterScheduledBackup(ctx context.Context) (*cnpgv1.ScheduledBackup, error) {
	scheduledBackup := &cnpgv1.ScheduledBackup{}
	if err := r.client.Get(ctx, r.clusterKey, scheduledBackup); err != nil {
		return nil, err
	}
	return scheduledBackup, nil
}

func (r *cnpgClusterClient) updateClusterScheduledBackup(ctx context.Context, scheduledBackup *cnpgv1.ScheduledBackup) error {
	return r.client.Update(ctx, scheduledBackup)
}

func (r *cnpgClusterClient) getClusterBackups(ctx context.Context) ([]cnpgv1.Backup, error) {
	// Use label selector to filter backups for this cluster
	listOptions := []client.ListOption{
		client.InNamespace(r.clusterKey.Namespace),
		client.MatchingLabels{"cnpg.io/cluster": r.clusterKey.Name},
	}
	var backupList cnpgv1.BackupList
	if err := r.client.List(ctx, &backupList, listOptions...); err != nil {
		return nil, err
	}
	return backupList.Items, nil
}

func (p *postgreSQLCredentials) connString() string {
	return fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=disable application_name=scale-to-zero",
		p.host, p.port, p.username, p.database)
}
