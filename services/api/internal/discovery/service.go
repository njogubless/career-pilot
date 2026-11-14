package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

type Service struct {
	pool     *pgxpool.Pool
	adapters sources.Registry
	log      *slog.Logger
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, adapters sources.Registry, log *slog.Logger) *Service {
	return &Service{pool: pool, adapters: adapters, log: log, now: time.Now}
}

// SkipReason explains why a run did not fetch.
var (
	ErrDisabled    = errors.New("source disabled")
	ErrTooSoon     = errors.New("source polled recently (minimum interval)")
	ErrBackingOff  = errors.New("source backing off after repeated failures")
	ErrNoAdapter   = errors.New("no adapter for source kind")
	ErrSuspicious  = errors.New("empty snapshot for a source that had active jobs; not expiring")
	suspiciousSize = 10
)

// backoff returns how long to wait after n consecutive failures beyond the
// circuit threshold: 1h, 2h, 4h ... capped at 24h.
func backoff(failures int) time.Duration {
	if failures < sources.CircuitThreshold {
		return 0
	}
	h := math.Min(math.Pow(2, float64(failures-sources.CircuitThreshold)), 24)
	return time.Duration(h) * time.Hour
}

// RunSource fetches one source snapshot and ingests it. The returned error is
// a *sources.SourceError for fetch failures, so callers can decide on retries.
func (s *Service) RunSource(ctx context.Context, sourceID uuid.UUID, trigger string) (Run, error) {
	src, err := GetSource(ctx, s.pool, sourceID)
	if err != nil {
		return Run{}, err
	}
	now := s.now()
	run := Run{ID: ids.New(), SourceID: src.ID, SourceName: src.DisplayName, Trigger: trigger, StartedAt: now}

	if skip := s.skipReason(src, now); skip != nil {
		run.Status = "SKIPPED"
		msg := skip.Error()
		run.Error = &msg
		_, err := s.pool.Exec(ctx, `INSERT INTO discovery_runs (id, source_id, trigger, status, started_at, finished_at, error)
			VALUES ($1, $2, $3, 'SKIPPED', $4, $4, $5)`, run.ID, src.ID, trigger, now, msg)
		return run, err
	}
	adapter, ok := s.adapters[src.Kind]
	if !ok {
		return run, ErrNoAdapter
	}

	if _, err := s.pool.Exec(ctx, `INSERT INTO discovery_runs (id, source_id, trigger, status, started_at) VALUES ($1, $2, $3, 'RUNNING', $4)`,
		run.ID, src.ID, trigger, now); err != nil {
		return run, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE job_sources SET last_run_at = $2 WHERE id = $1`, src.ID, now); err != nil {
		return run, err
	}

	postings, fetchErr := adapter.Fetch(ctx, src.Identifier)
	// Lever and Ashby only expose the board slug; show the curated name.
	for i := range postings {
		if postings[i].Company == src.Identifier {
			postings[i].Company = src.DisplayName
		}
	}
	if fetchErr == nil && len(postings) == 0 && src.ActiveJobs >= suspiciousSize {
		fetchErr = ErrSuspicious
	}
	if fetchErr != nil {
		s.recordFailure(context.WithoutCancel(ctx), &run, fetchErr)
		return run, fetchErr
	}

	// Every posting in this snapshot gets last_seen_at = run start, so the
	// expiry step (last_seen_at < run start) only touches jobs that vanished.
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		counts, err := ingest(ctx, tx, src, postings, now)
		if err != nil {
			return err
		}
		run.Counts = counts
		run.Status = "SUCCEEDED"
		fin := s.now()
		run.FinishedAt = &fin
		if _, err := tx.Exec(ctx, `UPDATE discovery_runs SET status='SUCCEEDED', finished_at=$2, fetched=$3, created=$4, updated=$5,
			unchanged=$6, expired=$7, reposted=$8, invalid=$9 WHERE id=$1`, run.ID, fin, counts.Fetched, counts.Created, counts.Updated,
			counts.Unchanged, counts.Expired, counts.Reposted, counts.Invalid); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE job_sources SET last_success_at=$2, consecutive_failures=0, last_error=NULL, updated_at=now() WHERE id=$1`, src.ID, fin)
		return err
	})
	if err != nil {
		s.recordFailure(context.WithoutCancel(ctx), &run, fmt.Errorf("ingest: %w", err))
		return run, err
	}
	s.log.Info("discovery run succeeded", "source", src.DisplayName, "kind", src.Kind, "fetched", run.Fetched,
		"created", run.Created, "updated", run.Updated, "expired", run.Expired, "invalid", run.Invalid)
	return run, nil
}

func (s *Service) skipReason(src sources.Source, now time.Time) error {
	if !src.Enabled {
		return ErrDisabled
	}
	if src.LastSuccessAt != nil && now.Sub(*src.LastSuccessAt) < time.Duration(src.MinIntervalMinutes)*time.Minute {
		return ErrTooSoon
	}
	if b := backoff(src.ConsecutiveFailures); b > 0 && src.LastRunAt != nil && now.Sub(*src.LastRunAt) < b {
		return ErrBackingOff
	}
	return nil
}

func (s *Service) recordFailure(ctx context.Context, run *Run, cause error) {
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	run.Status = "FAILED"
	run.Error = &msg
	fin := s.now()
	run.FinishedAt = &fin
	if _, err := s.pool.Exec(ctx, `UPDATE discovery_runs SET status='FAILED', finished_at=$2, error=$3 WHERE id=$1`, run.ID, fin, msg); err != nil {
		s.log.Error("record run failure", "err", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE job_sources SET consecutive_failures = consecutive_failures + 1, last_error=$2, updated_at=now() WHERE id=$1`,
		run.SourceID, msg); err != nil {
		s.log.Error("record source failure", "err", err)
	}
	s.log.Warn("discovery run failed", "source", run.SourceName, "err", msg)
}
