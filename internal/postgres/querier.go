package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type Querier interface {
	QueryRow(ctx context.Context, query string, args ...any) Row
	Close(ctx context.Context) error
}

type Row interface {
	pgx.Row
}
