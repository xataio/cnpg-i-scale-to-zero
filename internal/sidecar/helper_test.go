package sidecar

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/postgres"
)

type mockClusterClient struct {
	getClusterFunc                   func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error)
	updateClusterFunc                func(ctx context.Context, cluster *cnpgv1.Cluster) error
	getClusterCredentialsFunc        func(ctx context.Context) (*postgreSQLCredentials, error)
	getClusterScheduledBackupFunc    func(ctx context.Context) (*cnpgv1.ScheduledBackup, error)
	updateClusterScheduledBackupFunc func(ctx context.Context, scheduledBackup *cnpgv1.ScheduledBackup) error
	getClusterBackupsFunc            func(ctx context.Context, i uint) ([]cnpgv1.Backup, error)
	getClusterBackupsCalls           uint
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

func (m *mockClusterClient) getClusterScheduledBackup(ctx context.Context) (*cnpgv1.ScheduledBackup, error) {
	if m.getClusterScheduledBackupFunc != nil {
		return m.getClusterScheduledBackupFunc(ctx)
	}
	return nil, nil
}

func (m *mockClusterClient) updateClusterScheduledBackup(ctx context.Context, scheduledBackup *cnpgv1.ScheduledBackup) error {
	if m.updateClusterScheduledBackupFunc != nil {
		return m.updateClusterScheduledBackupFunc(ctx, scheduledBackup)
	}
	return nil
}

func (m *mockClusterClient) getClusterBackups(ctx context.Context) ([]cnpgv1.Backup, error) {
	m.getClusterBackupsCalls++
	if m.getClusterBackupsFunc != nil {
		return m.getClusterBackupsFunc(ctx, m.getClusterBackupsCalls)
	}
	return []cnpgv1.Backup{}, nil
}

type mockQuerier struct {
	queryFunc func(ctx context.Context, query string, args ...any) (postgres.Row, error)
}

func (m *mockQuerier) QueryRow(ctx context.Context, query string, args ...any) postgres.Row {
	if m.queryFunc != nil {
		row, err := m.queryFunc(ctx, query, args...)
		if err != nil {
			return &mockRow{
				scanFn: func(dest ...any) error {
					return err
				},
			}
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
