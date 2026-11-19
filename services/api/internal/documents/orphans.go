package documents

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// OrphanGrace protects uploads in flight: an object is written before its
// database row, so only objects older than this can be orphans.
const OrphanGrace = 24 * time.Hour

// SweepOrphans deletes stored objects that no document row references
// (KI-016: a failed delete after the row was removed, or a crash between
// upload and insert). It returns how many objects it examined and removed.
func SweepOrphans(ctx context.Context, pool *pgxpool.Pool, store Storage, now time.Time, log *slog.Logger) (seen, removed int, err error) {
	var batch []string
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		rows, err := pool.Query(ctx, `SELECT storage_key FROM profile_documents WHERE storage_key = ANY($1)`, batch)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			known[k] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, k := range batch {
			if known[k] {
				continue
			}
			if err := store.Delete(ctx, k); err != nil {
				log.Warn("orphan sweep: delete failed", "key", k, "err", err)
				continue
			}
			removed++
			log.Info("orphan sweep: removed unreferenced object", "key", k)
		}
		batch = batch[:0]
		return nil
	}
	err = store.List(ctx, "documents/", func(o ObjectInfo) error {
		seen++
		if now.Sub(o.Modified) < OrphanGrace {
			return nil
		}
		batch = append(batch, o.Key)
		if len(batch) == 500 {
			return flush()
		}
		return nil
	})
	if err == nil {
		err = flush()
	}
	return seen, removed, err
}

type OrphanSweepArgs struct{}

func (OrphanSweepArgs) Kind() string { return "documents_orphan_sweep" }

func (OrphanSweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type OrphanSweepWorker struct {
	river.WorkerDefaults[OrphanSweepArgs]
	Pool  *pgxpool.Pool
	Store Storage
	Log   *slog.Logger
}

func (w *OrphanSweepWorker) Work(ctx context.Context, _ *river.Job[OrphanSweepArgs]) error {
	if w.Store == nil {
		return nil
	}
	seen, removed, err := SweepOrphans(ctx, w.Pool, w.Store, time.Now(), w.Log)
	if err != nil {
		return err
	}
	w.Log.Info("orphan sweep finished", "objects", seen, "removed", removed)
	return nil
}

// PeriodicJobs runs the orphan sweep daily.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return OrphanSweepArgs{}, nil }, nil)}
}
