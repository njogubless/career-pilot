package discovery

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

// ---- Durable jobs (River) ----

// DiscoverAllArgs fans out one DiscoverSource job per enabled source.
type DiscoverAllArgs struct {
	Trigger string `json:"trigger"`
}

func (DiscoverAllArgs) Kind() string { return "discover_all" }

func (DiscoverAllArgs) InsertOpts() river.InsertOpts {
	// Collapse repeated triggers (button presses, overlapping schedules).
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Minute}}
}

type DiscoverSourceArgs struct {
	SourceID uuid.UUID `json:"source_id"`
	Trigger  string    `json:"trigger"`
}

func (DiscoverSourceArgs) Kind() string { return "discover_source" }

func (DiscoverSourceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 10 * time.Minute}}
}

type DiscoverAllWorker struct {
	river.WorkerDefaults[DiscoverAllArgs]
	Pool *pgxpool.Pool
}

func (w *DiscoverAllWorker) Work(ctx context.Context, job *river.Job[DiscoverAllArgs]) error {
	list, err := ListSources(ctx, w.Pool, true)
	if err != nil {
		return err
	}
	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}
	params := make([]river.InsertManyParams, 0, len(list))
	for _, s := range list {
		params = append(params, river.InsertManyParams{Args: DiscoverSourceArgs{SourceID: s.ID, Trigger: job.Args.Trigger}})
	}
	if len(params) == 0 {
		return nil
	}
	_, err = client.InsertMany(ctx, params)
	return err
}

type DiscoverSourceWorker struct {
	river.WorkerDefaults[DiscoverSourceArgs]
	Service *Service
	Log     *slog.Logger
}

func (w *DiscoverSourceWorker) Timeout(*river.Job[DiscoverSourceArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *DiscoverSourceWorker) Work(ctx context.Context, job *river.Job[DiscoverSourceArgs]) error {
	_, err := w.Service.RunSource(ctx, job.Args.SourceID, job.Args.Trigger)
	if err == nil {
		return nil
	}
	var se *sources.SourceError
	if errors.Is(err, ErrSourceNotFound) || errors.Is(err, ErrNoAdapter) || errors.Is(err, ErrSuspicious) ||
		(errors.As(err, &se) && se.Permanent) {
		return river.JobCancel(err) // retrying will not help; health already records it
	}
	return err
}

// PeriodicJobs schedules a full discovery cycle every interval (0 disables).
func PeriodicJobs(interval time.Duration) []*river.PeriodicJob {
	if interval <= 0 {
		return nil
	}
	return []*river.PeriodicJob{river.NewPeriodicJob(
		river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) { return DiscoverAllArgs{Trigger: "SCHEDULED"}, nil },
		&river.PeriodicJobOpts{RunOnStart: true},
	)}
}

// ---- Seeds ----

//go:embed seeds.json
var seedsJSON []byte

type seed struct {
	Kind               sources.Kind `json:"kind"`
	Identifier         string       `json:"identifier"`
	DisplayName        string       `json:"display_name"`
	MinIntervalMinutes int          `json:"min_interval_minutes"`
}

// SeedSources inserts the curated default boards if missing (idempotent).
func SeedSources(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var seeds []seed
	if err := json.Unmarshal(seedsJSON, &seeds); err != nil {
		return 0, fmt.Errorf("parse seeds: %w", err)
	}
	added := 0
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		for _, s := range seeds {
			if !s.Kind.Valid() || !sources.ValidIdentifier(s.Identifier) {
				return fmt.Errorf("invalid seed %+v", s)
			}
			interval := s.MinIntervalMinutes
			if interval == 0 {
				interval = 60
			}
			tag, err := tx.Exec(ctx, `INSERT INTO job_sources (id, kind, identifier, display_name, min_interval_minutes)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT (kind, identifier) DO NOTHING`, ids.New(), s.Kind, s.Identifier, s.DisplayName, interval)
			if err != nil {
				return err
			}
			added += int(tag.RowsAffected())
		}
		return nil
	})
	return added, err
}
