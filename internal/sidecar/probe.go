package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/xataio/cnpg-i-scale-to-zero/internal/postgres"
)

const defaultListenAddress = ":9188"

type Config struct {
	ListenAddress string
}

type probe struct {
	pgQuerier        postgres.Querier
	pgQuerierFactory func(ctx context.Context, url string) (postgres.Querier, error)
}

func newProbe(ctx context.Context) (*probe, error) {
	p := &probe{
		pgQuerierFactory: func(ctx context.Context, url string) (postgres.Querier, error) {
			return postgres.NewConnPool(ctx, url)
		},
	}

	if err := p.initQuerier(ctx); err != nil {
		return nil, fmt.Errorf("initialize PostgreSQL querier: %w", err)
	}

	return p, nil
}

func (p *probe) close(ctx context.Context) {
	if p.pgQuerier == nil {
		return
	}
	if err := p.pgQuerier.Close(ctx); err != nil {
		log.FromContext(ctx).Error(err, "PostgreSQL querier close error")
	}
}

func (p *probe) initQuerier(ctx context.Context) error {
	if p.pgQuerier != nil {
		_ = p.pgQuerier.Close(ctx)
	}

	querier, err := p.pgQuerierFactory(ctx, postgresConnString())
	if err != nil {
		return err
	}

	p.pgQuerier = querier
	return nil
}

func postgresConnString() string {
	return "user=postgres dbname=postgres sslmode=disable application_name=scale-to-zero"
}

func (p *probe) connections(ctx context.Context) (int, error) {
	openConns, err := p.openConnections(ctx)
	if err != nil {
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return 0, fmt.Errorf("query open connections: %w", err)
		}

		if err := p.initQuerier(ctx); err != nil {
			return 0, fmt.Errorf("reinitialize PostgreSQL querier: %w", err)
		}
		openConns, err = p.openConnections(ctx)
		if err != nil {
			return 0, fmt.Errorf("query open connections after reinitialization: %w", err)
		}
	}

	return openConns, nil
}

func (p *probe) openConnections(ctx context.Context) (int, error) {
	const query = `SELECT COUNT(*) FROM pg_stat_activity WHERE state IN ('active', 'idle', 'idle in transaction') AND pg_backend_pid() != pg_stat_activity.pid AND usename != 'streaming_replica';`
	var count int
	if err := p.pgQuerier.QueryRow(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("query open connections: %w", err)
	}

	return count, nil
}

func (p *probe) handler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/connections", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		response, err := p.connections(r.Context())
		if err != nil {
			log.FromContext(ctx).Error(err, "PostgreSQL connection check error")
			http.Error(w, "connections probe failed", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.FromContext(ctx).Error(err, "connections response encode error")
		}
	})

	return mux
}

func serve(ctx context.Context, cfg Config) error {
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = defaultListenAddress
	}

	p, err := newProbe(ctx)
	if err != nil {
		return err
	}
	defer p.close(ctx)

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           p.handler(ctx),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.FromContext(ctx).Error(err, "connections probe server stop error")
		}
	}()

	log.FromContext(ctx).Info("starting connections probe", "address", cfg.ListenAddress)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
