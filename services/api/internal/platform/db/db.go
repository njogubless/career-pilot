// Package db manages the PostgreSQL connection pool and schema migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/careerpilot/careerpilot/services/api/migrations"
)

// Connect opens a pgx pool and verifies connectivity.
func Connect(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func newProvider(pool *pgxpool.Pool) (*goose.Provider, *sql.DB, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("create migration provider: %w", err)
	}
	return p, sqlDB, nil
}

// MigrateUp applies all pending migrations.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	p, sqlDB, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	results, err := p.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "migration_version", r.Source.Version, "path", r.Source.Path, "duration_ms", r.Duration.Milliseconds())
	}
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// MigrationStatus reports the current and latest available versions.
func MigrationStatus(ctx context.Context, pool *pgxpool.Pool) (current, latest int64, err error) {
	p, sqlDB, err := newProvider(pool)
	if err != nil {
		return 0, 0, err
	}
	defer sqlDB.Close()
	current, err = p.GetDBVersion(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read db version: %w", err)
	}
	sources := p.ListSources()
	if len(sources) > 0 {
		latest = sources[len(sources)-1].Version
	}
	return current, latest, nil
}
