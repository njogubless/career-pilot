// Package jobs wraps River, the Postgres-backed durable job queue (ADR-002).
// Jobs are enqueued in the same transaction as the business write that
// requires them, so a committed record always has its job and vice versa.
package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type Client = river.Client[pgx.Tx]

// Migrate applies River's own schema migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("river migrator: %w", err)
	}
	res, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	for _, v := range res.Versions {
		log.Info("river migration applied", "migration_version", v.Version)
	}
	return nil
}

// NewInsertClient returns a client that can only enqueue jobs (used by the API).
func NewInsertClient(pool *pgxpool.Pool, log *slog.Logger) (*Client, error) {
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log})
	if err != nil {
		return nil, fmt.Errorf("river insert client: %w", err)
	}
	return c, nil
}

// NewWorkerClient returns a client that executes jobs (used by `careerpilot worker`).
func NewWorkerClient(pool *pgxpool.Pool, workers *river.Workers, log *slog.Logger, maxWorkers int, periodic ...*river.PeriodicJob) (*Client, error) {
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:       log,
		Workers:      workers,
		Queues:       map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: maxWorkers}},
		PeriodicJobs: periodic,
	})
	if err != nil {
		return nil, fmt.Errorf("river worker client: %w", err)
	}
	return c, nil
}
