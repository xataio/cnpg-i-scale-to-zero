// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool struct {
	*pgxpool.Pool
}

const maxConns = 50

func NewConnPool(ctx context.Context, url string) (*Pool, error) {
	pgCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("failed parsing postgres connection string: %w", err)
	}
	pgCfg.MaxConns = maxConns

	pool, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create a postgres connection pool: %w", err)
	}

	return &Pool{Pool: pool}, nil
}

func (c *Pool) QueryRow(ctx context.Context, query string, args ...any) Row {
	return c.Pool.QueryRow(ctx, query, args...)
}

func (c *Pool) Close(_ context.Context) error {
	c.Pool.Close()
	return nil
}
