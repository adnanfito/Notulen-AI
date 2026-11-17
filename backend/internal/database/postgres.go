// Package database owns the PostgreSQL connection pool.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

// Connect opens a pool and waits for the server to accept queries.
//
// Compose reports postgres healthy as soon as pg_isready succeeds, which can
// still be a moment before it serves connections, so we retry with backoff
// rather than crash-looping the container.
func Connect(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	const attempts = 10
	backoff := 500 * time.Millisecond
	for i := 1; i <= attempts; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return &DB{Pool: pool}, nil
		}
		if ctx.Err() != nil {
			break
		}
		slog.Warn("database_ping_retry", "attempt", i, "of", attempts, "error", err)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}

	pool.Close()
	return nil, fmt.Errorf("database unreachable after %d attempts: %w", attempts, err)
}

// Health reports whether the database is currently reachable.
func (db *DB) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return db.Pool.Ping(ctx)
}

func (db *DB) Close() {
	if db.Pool != nil {
		db.Pool.Close()
	}
}
