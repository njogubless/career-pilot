package observability

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolCollector exports pgx pool statistics at scrape time.
type PoolCollector struct {
	pool                                     *pgxpool.Pool
	acquired, idle, total, max, waits, empty *prometheus.Desc
}

func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector {
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc("careerpilot_db_pool_"+name, help, nil, nil)
	}
	return &PoolCollector{
		pool:     pool,
		acquired: d("acquired_connections", "Connections currently in use."),
		idle:     d("idle_connections", "Idle connections."),
		total:    d("total_connections", "Open connections."),
		max:      d("max_connections", "Configured maximum connections."),
		waits:    d("acquire_wait_seconds_total", "Cumulative time spent waiting for a connection."),
		empty:    d("empty_acquire_total", "Acquires that had to wait because the pool was empty."),
	}
}

func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.waits, c.empty} {
		ch <- d
	}
}

func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(c.acquired, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(c.total, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(c.max, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(c.waits, prometheus.CounterValue, s.AcquireDuration().Seconds())
	ch <- prometheus.MustNewConstMetric(c.empty, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
}

// QueueCollector exports durable-queue and scheduler health from Postgres at
// scrape time: unfinished jobs by state and kind, the age of the oldest
// available job, jobs discarded (dead-lettered) in the last hour, and enabled
// workflow schedules that are overdue. Only the worker registers it, so one
// process reports these shared numbers.
type QueueCollector struct {
	pool                                 *pgxpool.Pool
	jobs, oldest, discarded, overdue, up *prometheus.Desc
}

func NewQueueCollector(pool *pgxpool.Pool) *QueueCollector {
	return &QueueCollector{
		pool:      pool,
		jobs:      prometheus.NewDesc("careerpilot_queue_jobs", "Unfinished background jobs by state and kind.", []string{"state", "kind"}, nil),
		oldest:    prometheus.NewDesc("careerpilot_queue_oldest_available_seconds", "Age of the oldest job waiting to run (0 when none).", nil, nil),
		discarded: prometheus.NewDesc("careerpilot_queue_discarded_last_hour", "Jobs discarded after exhausting their attempts in the last hour, by kind.", []string{"kind"}, nil),
		overdue:   prometheus.NewDesc("careerpilot_schedules_overdue", "Enabled workflow schedules more than 5 minutes past their next run.", nil, nil),
		up:        prometheus.NewDesc("careerpilot_queue_collector_up", "1 if the last queue scrape succeeded.", nil, nil),
	}
}

func (c *QueueCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.jobs, c.oldest, c.discarded, c.overdue, c.up} {
		ch <- d
	}
}

func (c *QueueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.collect(ctx, ch); err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
}

func (c *QueueCollector) collect(ctx context.Context, ch chan<- prometheus.Metric) error {
	rows, err := c.pool.Query(ctx, `SELECT state::text, kind, count(*) FROM river_job
		WHERE state IN ('available','running','retryable','scheduled','pending') GROUP BY 1, 2`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var state, kind string
		var n int64
		if err := rows.Scan(&state, &kind, &n); err != nil {
			rows.Close()
			return err
		}
		ch <- prometheus.MustNewConstMetric(c.jobs, prometheus.GaugeValue, float64(n), state, kind)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var oldest float64
	if err := c.pool.QueryRow(ctx, `SELECT coalesce(extract(epoch FROM now() - min(scheduled_at)), 0)::float8
		FROM river_job WHERE state = 'available' AND scheduled_at <= now()`).Scan(&oldest); err != nil {
		return err
	}
	ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, oldest)
	rows, err = c.pool.Query(ctx, `SELECT kind, count(*) FROM river_job
		WHERE state = 'discarded' AND finalized_at > now() - interval '1 hour' GROUP BY 1`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind string
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			rows.Close()
			return err
		}
		ch <- prometheus.MustNewConstMetric(c.discarded, prometheus.GaugeValue, float64(n), kind)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var overdue int64
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_schedules
		WHERE enabled AND next_run_at < now() - interval '5 minutes'`).Scan(&overdue); err != nil {
		return err
	}
	ch <- prometheus.MustNewConstMetric(c.overdue, prometheus.GaugeValue, float64(overdue))
	return nil
}
