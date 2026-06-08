package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/postgres"
)

func TestProbeConnections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		openConnections int
	}{
		{
			name:            "active connections",
			openConnections: 3,
		},
		{
			name:            "zero connections",
			openConnections: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := &probe{
				pgQuerier: mockQuerier{count: tc.openConnections},
			}

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/connections", nil)
			p.handler(context.Background()).ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response int
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
			require.Equal(t, tc.openConnections, response)
		})
	}
}

func TestProbePostgresErrorReturnsNonOK(t *testing.T) {
	t.Parallel()

	p := &probe{
		pgQuerier: mockQuerier{err: errors.New("query failed")},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/connections", nil)
	p.handler(context.Background()).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	var response int
	require.Error(t, json.NewDecoder(recorder.Body).Decode(&response))
}

func TestProbeConnectionRefusedReinitializesPool(t *testing.T) {
	t.Parallel()

	reinitialized := false
	p := &probe{
		pgQuerier: mockQuerier{err: syscall.ECONNREFUSED},
		pgQuerierFactory: func(ctx context.Context, url string) (postgres.Querier, error) {
			reinitialized = true
			return mockQuerier{count: 2}, nil
		},
	}

	response, err := p.connections(context.Background())
	require.NoError(t, err)
	require.True(t, reinitialized)
	require.Equal(t, 2, response)
}

type mockQuerier struct {
	count int
	err   error
}

func (m mockQuerier) QueryRow(ctx context.Context, query string, args ...any) postgres.Row {
	return mockRow(m)
}

func (m mockQuerier) Close(ctx context.Context) error {
	return nil
}

type mockRow struct {
	count int
	err   error
}

func (m mockRow) Scan(dest ...any) error {
	if m.err != nil {
		return m.err
	}
	count, ok := dest[0].(*int)
	if !ok {
		return errors.New("expected *int")
	}
	*count = m.count
	return nil
}
