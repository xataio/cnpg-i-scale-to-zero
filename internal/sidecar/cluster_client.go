package sidecar

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	password string
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

func (r *cnpgClusterClient) getClusterCredentials(ctx context.Context) (*postgreSQLCredentials, error) {
	// The secret name follows the pattern: <cluster-name>-superuser We require
	// superuser credentials to connect to the PostgreSQL instance and be able
	// to see the open connections for all users. Using app user will only show
	// connections for that user.
	secretName := r.clusterKey.Name + "-superuser"
	secretKey := types.NamespacedName{
		Namespace: r.clusterKey.Namespace,
		Name:      secretName,
	}

	var secret corev1.Secret
	if err := r.client.Get(ctx, secretKey, &secret); err != nil {
		return nil, err
	}

	// Extract credentials from the secret
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	database := string(secret.Data["dbname"])
	if database == "*" || database == "" {
		database = "postgres" // Default database if not specified
	}

	// The host is localhost since the sidecar runs in the same pod
	host := "localhost"
	port := "5432" // Default PostgreSQL port

	// Check if port is specified in the secret
	if portData, exists := secret.Data["port"]; exists {
		port = string(portData)
	}

	creds := &postgreSQLCredentials{
		username: username,
		password: password,
		database: database,
		host:     host,
		port:     port,
	}

	log.FromContext(ctx).Info("Retrieved PostgreSQL credentials")

	return creds, nil
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
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=require",
		p.host, p.port, p.username, p.password, p.database)
}
