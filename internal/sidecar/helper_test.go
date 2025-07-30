package sidecar

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/postgres"
)

type mockClusterClient struct {
	getClusterFunc            func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error)
	updateClusterFunc         func(ctx context.Context, cluster *cnpgv1.Cluster) error
	getClusterCredentialsFunc func(ctx context.Context) (*postgreSQLCredentials, error)
}

func (m *mockClusterClient) getCluster(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
	if m.getClusterFunc != nil {
		return m.getClusterFunc(ctx, forceUpdate)
	}
	return nil, nil
}

func (m *mockClusterClient) updateCluster(ctx context.Context, cluster *cnpgv1.Cluster) error {
	if m.updateClusterFunc != nil {
		return m.updateClusterFunc(ctx, cluster)
	}
	return nil
}

func (m *mockClusterClient) getClusterCredentials(ctx context.Context) (*postgreSQLCredentials, error) {
	if m.getClusterCredentialsFunc != nil {
		return m.getClusterCredentialsFunc(ctx)
	}
	return nil, nil
}

type mockQuerier struct {
	queryFunc func(ctx context.Context, query string, args ...any) (postgres.Row, error)
}

func (m *mockQuerier) QueryRow(ctx context.Context, query string, args ...any) postgres.Row {
	if m.queryFunc != nil {
		row, err := m.queryFunc(ctx, query, args...)
		if err != nil {
			return nil // Handle error appropriately in real code
		}
		return row
	}
	return nil
}

func (m *mockQuerier) Close(ctx context.Context) error {
	// Implement close logic if needed
	return nil
}

type mockRow struct {
	scanFn func(dest ...any) error
}

func (m *mockRow) Scan(dest ...any) error {
	if m.scanFn != nil {
		return m.scanFn(dest...)
	}
	return nil
}
