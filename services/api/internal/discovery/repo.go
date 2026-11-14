// Package discovery runs job-source snapshots, ingests normalised postings
// (dedupe, expiry, reposts), and tracks per-source health.
package discovery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

var ErrSourceNotFound = errors.New("source not found")

const sourceCols = `s.id, s.kind, s.identifier, s.display_name, s.enabled, s.min_interval_minutes, s.last_run_at,
	s.last_success_at, s.last_error, s.consecutive_failures,
	(SELECT count(*) FROM jobs j WHERE j.source_id = s.id AND j.expired_at IS NULL)::int`

func scanSource(row pgx.Row) (sources.Source, error) {
	var s sources.Source
	err := row.Scan(&s.ID, &s.Kind, &s.Identifier, &s.DisplayName, &s.Enabled, &s.MinIntervalMinutes, &s.LastRunAt,
		&s.LastSuccessAt, &s.LastError, &s.ConsecutiveFailures, &s.ActiveJobs)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrSourceNotFound
	}
	return s, err
}

func ListSources(ctx context.Context, q db.Querier, onlyEnabled bool) ([]sources.Source, error) {
	rows, err := q.Query(ctx, `SELECT `+sourceCols+` FROM job_sources s WHERE ($1 = false OR s.enabled) ORDER BY s.kind, s.display_name`, onlyEnabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sources.Source{}
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetSource(ctx context.Context, q db.Querier, id uuid.UUID) (sources.Source, error) {
	return scanSource(q.QueryRow(ctx, `SELECT `+sourceCols+` FROM job_sources s WHERE s.id = $1`, id))
}

func InsertSource(ctx context.Context, q db.Querier, s sources.Source) error {
	_, err := q.Exec(ctx, `INSERT INTO job_sources (id, kind, identifier, display_name, enabled, min_interval_minutes)
		VALUES ($1, $2, $3, $4, $5, $6)`, s.ID, s.Kind, s.Identifier, s.DisplayName, s.Enabled, s.MinIntervalMinutes)
	return err
}

func SetEnabled(ctx context.Context, q db.Querier, id uuid.UUID, enabled bool) error {
	tag, err := q.Exec(ctx, `UPDATE job_sources SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrSourceNotFound
	}
	return err
}

type Run struct {
	ID         uuid.UUID  `json:"id"`
	SourceID   uuid.UUID  `json:"source_id"`
	SourceName string     `json:"source_name"`
	Trigger    string     `json:"trigger"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Counts
	Error *string `json:"error"`
}

type Counts struct {
	Fetched   int `json:"fetched"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Expired   int `json:"expired"`
	Reposted  int `json:"reposted"`
	Invalid   int `json:"invalid"`
}

func RecentRuns(ctx context.Context, q db.Querier, limit int) ([]Run, error) {
	rows, err := q.Query(ctx, `SELECT r.id, r.source_id, s.display_name, r.trigger, r.status, r.started_at, r.finished_at,
		r.fetched, r.created, r.updated, r.unchanged, r.expired, r.reposted, r.invalid, r.error
		FROM discovery_runs r JOIN job_sources s ON s.id = r.source_id ORDER BY r.started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) {
		var x Run
		err := r.Scan(&x.ID, &x.SourceID, &x.SourceName, &x.Trigger, &x.Status, &x.StartedAt, &x.FinishedAt,
			&x.Fetched, &x.Created, &x.Updated, &x.Unchanged, &x.Expired, &x.Reposted, &x.Invalid, &x.Error)
		return x, err
	})
}
