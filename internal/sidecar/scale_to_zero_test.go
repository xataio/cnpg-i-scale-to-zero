package sidecar

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/stretchr/testify/require"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/postgres"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestScaleToZero_Start(t *testing.T) {
	t.Parallel()

	errTest := errors.New("oh noes")

	tests := []struct {
		name       string
		client     func(chan struct{}) *mockClusterClient
		querier    func(chan struct{}) *mockQuerier
		lastActive time.Time

		wantErr error
	}{
		{
			name: "cluster with scale to zero disabled, no hibernation triggered",
			client: func(done chan struct{}) *mockClusterClient {
				return &mockClusterClient{
					getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
						defer func() { done <- struct{}{} }()
						return &cnpgv1.Cluster{}, nil
					},
				}
			},
			querier: func(done chan struct{}) *mockQuerier {
				return &mockQuerier{}
			},
			wantErr: nil,
		},
		{
			name: "error getting cluster scale to zero config is ignored",
			client: func(done chan struct{}) *mockClusterClient {
				return &mockClusterClient{
					getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
						defer func() { done <- struct{}{} }()
						return nil, errTest
					},
				}
			},
			querier: func(_ chan struct{}) *mockQuerier {
				return &mockQuerier{}
			},
			wantErr: nil,
		},
		{
			name: "cluster with scale to zero enabled and active cluster, no hibernation triggered",
			client: func(_ chan struct{}) *mockClusterClient {
				return &mockClusterClient{
					getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
						return &cnpgv1.Cluster{
							Status: cnpgv1.ClusterStatus{
								Phase: healthyClusterStatus,
							},
							ObjectMeta: metav1.ObjectMeta{
								Annotations: map[string]string{
									scaleToZeroEnabledAnnotation: "true",
								},
							},
						}, nil
					},
				}
			},
			querier: func(done chan struct{}) *mockQuerier {
				return &mockQuerier{
					queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
						defer func() { done <- struct{}{} }()
						return &mockRow{
							scanFn: func(dest ...any) error {
								require.Len(t, dest, 1)
								count, ok := dest[0].(*int)
								require.True(t, ok)
								*count = 1 // Simulate an active cluster
								return nil
							},
						}, nil
					},
				}
			},
			wantErr: nil,
		},
		{
			name: "cluster with scale to zero enabled and inactive cluster, hibernation triggered",
			client: func(done chan struct{}) *mockClusterClient {
				return &mockClusterClient{
					getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
						return &cnpgv1.Cluster{
							Status: cnpgv1.ClusterStatus{
								Phase: healthyClusterStatus,
							},
							ObjectMeta: metav1.ObjectMeta{
								Annotations: map[string]string{
									scaleToZeroEnabledAnnotation: "true",
									inactivityMinutesAnnotation:  "5",
								},
							},
						}, nil
					},
					updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
						defer func() { done <- struct{}{} }()
						require.NotNil(t, cluster)
						require.Equal(t, "on", cluster.Annotations[hibernationAnnotation])
						return nil
					},
				}
			},
			querier: func(_ chan struct{}) *mockQuerier {
				return &mockQuerier{
					queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
						return &mockRow{
							scanFn: func(dest ...any) error {
								require.Len(t, dest, 1)
								count, ok := dest[0].(*int)
								require.True(t, ok)
								*count = 0 // Simulate an inactive cluster
								return nil
							},
						}, nil
					},
				}
			},
			lastActive: time.Now().Add(-time.Minute * 10), // Simulate inactivity

			wantErr: nil,
		},
		{
			name: "cluster with scale to zero enabled and inactive cluster, hibernation error ignored",
			client: func(done chan struct{}) *mockClusterClient {
				return &mockClusterClient{
					getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
						return &cnpgv1.Cluster{
							Status: cnpgv1.ClusterStatus{
								Phase: healthyClusterStatus,
							},
							ObjectMeta: metav1.ObjectMeta{
								Annotations: map[string]string{
									scaleToZeroEnabledAnnotation: "true",
									inactivityMinutesAnnotation:  "5",
								},
							},
						}, nil
					},
					updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
						defer func() { done <- struct{}{} }()
						require.NotNil(t, cluster)
						require.Equal(t, "on", cluster.Annotations[hibernationAnnotation])
						return errTest
					},
				}
			},
			querier: func(_ chan struct{}) *mockQuerier {
				return &mockQuerier{
					queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
						return &mockRow{
							scanFn: func(dest ...any) error {
								require.Len(t, dest, 1)
								count, ok := dest[0].(*int)
								require.True(t, ok)
								*count = 0 // Simulate an inactive cluster
								return nil
							},
						}, nil
					},
				}
			},
			lastActive: time.Now().Add(-time.Minute * 10), // Simulate inactivity

			wantErr: nil,
		},
		{
			name: "cluster with scale to zero enabled and inactive cluster, hibernation error replica instance stops the process",
			client: func(done chan struct{}) *mockClusterClient {
				return &mockClusterClient{
					getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
						return &cnpgv1.Cluster{
							Status: cnpgv1.ClusterStatus{
								Phase: healthyClusterStatus,
							},
							ObjectMeta: metav1.ObjectMeta{
								Annotations: map[string]string{
									scaleToZeroEnabledAnnotation: "true",
									inactivityMinutesAnnotation:  "5",
								},
							},
						}, nil
					},
					updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
						defer func() { done <- struct{}{} }()
						require.NotNil(t, cluster)
						require.Equal(t, "on", cluster.Annotations[hibernationAnnotation])
						return errReplicaInstance
					},
				}
			},
			querier: func(_ chan struct{}) *mockQuerier {
				return &mockQuerier{
					queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
						return &mockRow{
							scanFn: func(dest ...any) error {
								require.Len(t, dest, 1)
								count, ok := dest[0].(*int)
								require.True(t, ok)
								*count = 0 // Simulate an inactive cluster
								return nil
							},
						}, nil
					},
				}
			},
			lastActive: time.Now().Add(-time.Minute * 10), // Simulate inactivity

			wantErr: nil,
		},
		{
			name: "cluster with scale to zero enabled, unable to check activity, error ignored",
			client: func(done chan struct{}) *mockClusterClient {
				return &mockClusterClient{
					getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
						return &cnpgv1.Cluster{
							Status: cnpgv1.ClusterStatus{
								Phase: healthyClusterStatus,
							},
							ObjectMeta: metav1.ObjectMeta{
								Annotations: map[string]string{
									scaleToZeroEnabledAnnotation: "true",
									inactivityMinutesAnnotation:  "5",
								},
							},
						}, nil
					},
				}
			},
			querier: func(done chan struct{}) *mockQuerier {
				return &mockQuerier{
					queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
						defer func() { done <- struct{}{} }()
						return &mockRow{
							scanFn: func(dest ...any) error {
								return errTest
							},
						}, nil
					},
				}
			},
			lastActive: time.Now().Add(-time.Minute * 10), // Simulate inactivity

			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doneChan := make(chan struct{})
			defer close(doneChan)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			defer cancel()

			stz := &scaleToZero{
				client:         tc.client(doneChan),
				currentPodName: "test-pod",
				lastActive:     time.Now(),
				checkInterval:  time.Millisecond * 100,
				pgQuerier:      tc.querier(doneChan),
			}

			if !tc.lastActive.IsZero() {
				stz.lastActive = tc.lastActive
			}

			go func() {
				err := stz.Start(ctx)
				require.ErrorIs(t, err, tc.wantErr)
			}()

			select {
			case <-doneChan:
				return
			case <-ctx.Done():
				require.Fail(t, "timeout waiting for test to complete")
			}
		})
	}
}

func TestScaleToZero_isClusterActive(t *testing.T) {
	t.Parallel()
	errTest := errors.New("oh noes")

	inactivityMinutes := 5
	now := time.Now()
	twoMinutesBeforeNow := now.Add(-2 * time.Minute)
	tenMinutesBeforeNow := now.Add(-10 * time.Minute)

	tests := []struct {
		name           string
		client         *mockClusterClient
		querier        *mockQuerier
		querierFactory func(ctx context.Context, url string) (postgres.Querier, error)
		lastActive     time.Time

		wantLastActive time.Time
		wantActive     bool
		wantErr        error
	}{
		{
			name:   "openConnections returns error",
			client: &mockClusterClient{},
			querier: &mockQuerier{
				queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
					return &mockRow{
						scanFn: func(dest ...any) error {
							return errTest
						},
					}, nil
				},
			},
			lastActive: twoMinutesBeforeNow,

			wantLastActive: twoMinutesBeforeNow,
			wantActive:     false,
			wantErr:        errTest,
		},
		{
			name:   "openConnections returns > 0, cluster is active",
			client: &mockClusterClient{},
			querier: &mockQuerier{
				queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
					return &mockRow{
						scanFn: func(dest ...any) error {
							count, ok := dest[0].(*int)
							require.True(t, ok)
							*count = 3
							return nil
						},
					}, nil
				},
			},
			lastActive: tenMinutesBeforeNow,

			wantLastActive: now,
			wantActive:     true,
			wantErr:        nil,
		},
		{
			name:   "openConnections returns 0, lastActive within inactivity window, cluster is active",
			client: &mockClusterClient{},
			querier: &mockQuerier{
				queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
					return &mockRow{
						scanFn: func(dest ...any) error {
							count, ok := dest[0].(*int)
							require.True(t, ok)
							*count = 0
							return nil
						},
					}, nil
				},
			},
			lastActive: twoMinutesBeforeNow,

			wantLastActive: twoMinutesBeforeNow,
			wantActive:     true,
			wantErr:        nil,
		},
		{
			name:   "openConnections returns 0, lastActive outside inactivity window, cluster is inactive",
			client: &mockClusterClient{},
			querier: &mockQuerier{
				queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
					return &mockRow{
						scanFn: func(dest ...any) error {
							count, ok := dest[0].(*int)
							require.True(t, ok)
							*count = 0
							return nil
						},
					}, nil
				},
			},
			lastActive: tenMinutesBeforeNow,

			wantLastActive: tenMinutesBeforeNow,
			wantActive:     false,
			wantErr:        nil,
		},
		{
			name: "openConnections returns ECONNREFUSED, reinit succeeds, cluster active",
			client: &mockClusterClient{
				getClusterCredentialsFunc: func(ctx context.Context) (*postgreSQLCredentials, error) {
					return &postgreSQLCredentials{}, nil
				},
			},
			querier: &mockQuerier{
				queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
					return &mockRow{
						scanFn: func(dest ...any) error {
							return syscall.ECONNREFUSED
						},
					}, nil
				},
			},
			querierFactory: func(ctx context.Context, url string) (postgres.Querier, error) {
				return &mockQuerier{
					queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
						return &mockRow{
							scanFn: func(dest ...any) error {
								count, ok := dest[0].(*int)
								require.True(t, ok)
								*count = 2
								return nil
							},
						}, nil
					},
				}, nil
			},
			lastActive: tenMinutesBeforeNow,

			wantLastActive: now,
			wantActive:     true,
			wantErr:        nil,
		},
		{
			name: "openConnections returns ECONNREFUSED, reinit fails",
			client: &mockClusterClient{
				getClusterCredentialsFunc: func(ctx context.Context) (*postgreSQLCredentials, error) {
					return nil, errTest
				},
			},
			querier: &mockQuerier{
				queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
					return &mockRow{
						scanFn: func(dest ...any) error {
							return syscall.ECONNREFUSED
						},
					}, nil
				},
			},
			querierFactory: func(ctx context.Context, url string) (postgres.Querier, error) {
				return nil, errTest
			},
			lastActive: tenMinutesBeforeNow,

			wantLastActive: tenMinutesBeforeNow,
			wantActive:     false,
			wantErr:        errTest,
		},
		{
			name: "openConnections returns ECONNREFUSED, reinit ok, but query after reinit fails",
			client: &mockClusterClient{
				getClusterCredentialsFunc: func(ctx context.Context) (*postgreSQLCredentials, error) {
					return &postgreSQLCredentials{}, nil
				},
			},
			querier: &mockQuerier{
				queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
					return &mockRow{
						scanFn: func(dest ...any) error {
							return syscall.ECONNREFUSED
						},
					}, nil
				},
			},
			querierFactory: func(ctx context.Context, url string) (postgres.Querier, error) {
				return &mockQuerier{
					queryFunc: func(ctx context.Context, query string, args ...any) (postgres.Row, error) {
						return &mockRow{
							scanFn: func(dest ...any) error {
								return errTest
							},
						}, nil
					},
				}, nil
			},
			lastActive: tenMinutesBeforeNow,

			wantLastActive: tenMinutesBeforeNow,
			wantActive:     false,
			wantErr:        errTest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stz := &scaleToZero{
				client:           tc.client,
				pgQuerier:        tc.querier,
				pgQuerierFactory: tc.querierFactory,
				currentPodName:   "test-pod",
				lastActive:       tc.lastActive,
			}

			isActive, err := stz.isClusterActive(context.Background(), inactivityMinutes)
			require.Equal(t, tc.wantActive, isActive)
			require.WithinDuration(t, tc.wantLastActive, stz.lastActive, 5*time.Second)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func Test_hibernate(t *testing.T) {
	t.Parallel()

	errTest := errors.New("oh noes")

	tests := []struct {
		name   string
		client *mockClusterClient

		wantErr error
	}{
		{
			name: "cluster is not healthy, should skip hibernation",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						Status: cnpgv1.ClusterStatus{
							Phase: "NotHealthy",
						},
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{},
						},
					}, nil
				},
				updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
					return errors.New("updateClusterFn should not be called")
				},
			},
			wantErr: nil,
		},
		{
			name: "cluster is already hibernated, should do nothing",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						Status: cnpgv1.ClusterStatus{
							Phase: healthyClusterStatus,
						},
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								hibernationAnnotation: "on",
							},
						},
					}, nil
				},
				updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
					return errors.New("updateClusterFn should not be called")
				},
			},
			wantErr: nil,
		},
		{
			name: "cluster is healthy, nil annotations, should hibernate",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						Status: cnpgv1.ClusterStatus{
							Phase: healthyClusterStatus,
						},
						ObjectMeta: metav1.ObjectMeta{},
					}, nil
				},
				updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
					require.Equal(t, "on", cluster.Annotations[hibernationAnnotation])
					return nil
				},
			},
			wantErr: nil,
		},
		{
			name: "cluster is healthy and not hibernated, annotation succeeds",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						Status: cnpgv1.ClusterStatus{
							Phase: healthyClusterStatus,
						},
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{},
						},
					}, nil
				},
				updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
					require.Equal(t, "on", cluster.Annotations[hibernationAnnotation])
					return nil
				},
			},
			wantErr: nil,
		},
		{
			name: "cluster is healthy and not hibernated, updateCluster returns error",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						Status: cnpgv1.ClusterStatus{
							Phase: healthyClusterStatus,
						},
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{},
						},
					}, nil
				},
				updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
					return errTest
				},
			},
			wantErr: errTest,
		},
		{
			name: "getCluster returns error",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return nil, errTest
				},
			},
			wantErr: errTest,
		},
		{
			name: "updateCluster returns errReplicaInstance",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						Status: cnpgv1.ClusterStatus{
							Phase: healthyClusterStatus,
						},
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{},
						},
					}, nil
				},
				updateClusterFunc: func(ctx context.Context, cluster *cnpgv1.Cluster) error {
					return errReplicaInstance
				},
			},
			wantErr: errReplicaInstance,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stz := &scaleToZero{
				client:         tc.client,
				currentPodName: "test-pod",
			}

			err := stz.hibernate(context.Background())
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestScaleToZero_getScaleToZeroConfig(t *testing.T) {
	t.Parallel()

	errTest := errors.New("oh noes")

	tests := []struct {
		name   string
		client *mockClusterClient

		wantCfg *scaleToZeroConfig
		wantErr error
	}{
		{
			name: "scale to zero enabled with valid inactivity minutes",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								scaleToZeroEnabledAnnotation: "true",
								inactivityMinutesAnnotation:  "10",
							},
						},
					}, nil
				},
			},
			wantCfg: &scaleToZeroConfig{
				enabled:           true,
				inactivityMinutes: 10,
			},
			wantErr: nil,
		},
		{
			name: "scale to zero enabled with invalid inactivity minutes uses default",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								scaleToZeroEnabledAnnotation: "true",
								inactivityMinutesAnnotation:  "notanumber",
							},
						},
					}, nil
				},
			},
			wantCfg: &scaleToZeroConfig{
				enabled:           true,
				inactivityMinutes: defaultInactivityMinutes,
			},
			wantErr: nil,
		},
		{
			name: "no scale to zero annotations, uses default values",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{},
						},
					}, nil
				},
			},
			wantCfg: &scaleToZeroConfig{
				enabled:           false,
				inactivityMinutes: defaultInactivityMinutes,
			},
			wantErr: nil,
		},
		{
			name: "scale to zero enabled, no inactivity annotation, uses default inactivity minutes",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return &cnpgv1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								scaleToZeroEnabledAnnotation: "true",
							},
						},
					}, nil
				},
			},
			wantCfg: &scaleToZeroConfig{
				enabled:           true,
				inactivityMinutes: defaultInactivityMinutes,
			},
			wantErr: nil,
		},
		{
			name: "getCluster returns error",
			client: &mockClusterClient{
				getClusterFunc: func(ctx context.Context, forceUpdate bool) (*cnpgv1.Cluster, error) {
					return nil, errTest
				},
			},
			wantCfg: nil,
			wantErr: errTest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stz := &scaleToZero{
				client: tc.client,
			}
			cfg, err := stz.getClusterScaleToZeroConfig(context.Background())
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.wantCfg, cfg)
		})
	}
}
