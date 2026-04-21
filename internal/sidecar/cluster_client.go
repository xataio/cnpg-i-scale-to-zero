package sidecar

import (
	"context"
	"fmt"
	"os"
	"strings"
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

const credentialsPath = "/etc/superuser" //nolint:gosec // filesystem path, not a credential

func (r *cnpgClusterClient) getClusterCredentials(_ context.Context) (*postgreSQLCredentials, error) {
	username, err := readCredentialFile(credentialsPath + "/username")
	if err != nil {
		return nil, fmt.Errorf("read superuser username: %w", err)
	}
	password, err := readCredentialFile(credentialsPath + "/password")
	if err != nil {
		return nil, fmt.Errorf("read superuser password: %w", err)
	}
	database, err := readCredentialFile(credentialsPath + "/dbname")
	if err != nil {
		return nil, fmt.Errorf("read superuser dbname: %w", err)
	}
	if database == "*" || database == "" {
		database = "postgres"
	}

	return &postgreSQLCredentials{
		username: username,
		password: password,
		database: database,
		host:     "localhost",
		port:     "5432",
	}, nil
}

func readCredentialFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
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

func (p *postgreSQLCredentials) connString() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=require",
		p.host, p.port, p.username, p.password, p.database)
}
