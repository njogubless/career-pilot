package discovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

// validPosting rejects postings that cannot be shown truthfully or safely.
func validPosting(p sources.Posting) bool {
	return p.ExternalID != "" && len(p.ExternalID) <= 200 &&
		strings.TrimSpace(p.Title) != "" && strings.TrimSpace(p.Company) != "" &&
		sources.IsHTTPURL(p.CanonicalURL) && sources.IsHTTPURL(p.ApplyURL) &&
		utf8.ValidString(p.Title) && utf8.ValidString(p.DescriptionText)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ingest applies a full, successful snapshot inside tx. Postings absent from
// the snapshot are expired; duplicates across sources are reconciled.
func ingest(ctx context.Context, tx pgx.Tx, src sources.Source, postings []sources.Posting, runStart time.Time) (Counts, error) {
	c := Counts{Fetched: len(postings)}
	keys := map[string]bool{}
	seen := map[string]bool{}

	for _, p := range postings {
		if !validPosting(p) || seen[p.ExternalID] {
			c.Invalid++
			continue
		}
		seen[p.ExternalID] = true
		p.Title = clip(strings.TrimSpace(p.Title), 300)
		p.Company = clip(strings.TrimSpace(p.Company), 200)
		p.DescriptionHTML = clip(p.DescriptionHTML, 200_000)
		p.DescriptionText = clip(p.DescriptionText, 100_000)
		key := sources.DedupeKey(p)
		hash := sources.ContentHash(p)
		keys[key] = true

		var id uuid.UUID
		var oldHash []byte
		var expiredAt *time.Time
		err := tx.QueryRow(ctx, `SELECT id, content_hash, expired_at FROM jobs WHERE source_id = $1 AND external_id = $2 FOR UPDATE`,
			src.ID, p.ExternalID).Scan(&id, &oldHash, &expiredAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			var repostOf *uuid.UUID
			var prev uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM jobs WHERE dedupe_key = $1 AND expired_at IS NOT NULL ORDER BY expired_at DESC LIMIT 1`, key).Scan(&prev); err == nil {
				repostOf = &prev
				c.Reposted++
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return c, err
			}
			if err := insertJob(ctx, tx, ids.New(), src.ID, p, key, hash, repostOf, runStart); err != nil {
				return c, fmt.Errorf("insert job %s: %w", p.ExternalID, err)
			}
			c.Created++
		case err != nil:
			return c, err
		default:
			if expiredAt != nil {
				c.Reposted++ // the same posting came back
			}
			if expiredAt == nil && bytes.Equal(oldHash, hash) {
				if _, err := tx.Exec(ctx, `UPDATE jobs SET last_seen_at = $2 WHERE id = $1`, id, runStart); err != nil {
					return c, err
				}
				c.Unchanged++
				continue
			}
			if err := updateJob(ctx, tx, id, p, key, hash, runStart); err != nil {
				return c, fmt.Errorf("update job %s: %w", p.ExternalID, err)
			}
			c.Updated++
		}
	}

	rows, err := tx.Query(ctx, `UPDATE jobs SET expired_at = $2, updated_at = now()
		WHERE source_id = $1 AND expired_at IS NULL AND last_seen_at < $2 RETURNING dedupe_key`, src.ID, runStart)
	if err != nil {
		return c, err
	}
	expiredKeys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return c, err
	}
	c.Expired = len(expiredKeys)
	for _, k := range expiredKeys {
		keys[k] = true
	}
	return c, reconcileDuplicates(ctx, tx, keys)
}

func salaryArgs(p sources.Posting) (minV, maxV *float64, cur, period *string, amin, amax *float64) {
	if p.Salary == nil {
		return
	}
	s := p.Salary
	return s.Min, s.Max, &s.Currency, &s.Period, sources.Annualize(s.Min, s.Period), sources.Annualize(s.Max, s.Period)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func insertJob(ctx context.Context, tx pgx.Tx, id, sourceID uuid.UUID, p sources.Posting, key string, hash []byte, repostOf *uuid.UUID, now time.Time) error {
	smin, smax, cur, per, amin, amax := salaryArgs(p)
	_, err := tx.Exec(ctx, `INSERT INTO jobs (id, source_id, external_id, canonical_url, apply_url, title, company_name, department,
		location_raw, location_countries, remote_status, remote_scope, remote_countries, remote_region, employment_type,
		salary_min, salary_max, salary_currency, salary_period, salary_annual_min, salary_annual_max, salary_text,
		description_html, description_text, tags, posted_at, first_seen_at, last_seen_at, content_hash, dedupe_key, repost_of)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$27,$28,$29,$30)`,
		id, sourceID, p.ExternalID, p.CanonicalURL, p.ApplyURL, p.Title, p.Company, p.Department,
		p.LocationRaw, nonNil(p.LocationCountries), p.RemoteStatus, p.RemoteScope, nonNil(p.RemoteCountries), p.RemoteRegion, p.EmploymentType,
		smin, smax, cur, per, amin, amax, clip(p.SalaryText, 300),
		p.DescriptionHTML, p.DescriptionText, nonNil(p.Tags), p.PostedAt, now, hash, key, repostOf)
	return err
}

func updateJob(ctx context.Context, tx pgx.Tx, id uuid.UUID, p sources.Posting, key string, hash []byte, now time.Time) error {
	smin, smax, cur, per, amin, amax := salaryArgs(p)
	_, err := tx.Exec(ctx, `UPDATE jobs SET canonical_url=$2, apply_url=$3, title=$4, company_name=$5, department=$6,
		location_raw=$7, location_countries=$8, remote_status=$9, remote_scope=$10, remote_countries=$11, remote_region=$12,
		employment_type=$13, salary_min=$14, salary_max=$15, salary_currency=$16, salary_period=$17, salary_annual_min=$18,
		salary_annual_max=$19, salary_text=$20, description_html=$21, description_text=$22, tags=$23, posted_at=$24,
		last_seen_at=$25, expired_at=NULL, content_hash=$26, dedupe_key=$27, updated_at=now() WHERE id=$1`,
		id, p.CanonicalURL, p.ApplyURL, p.Title, p.Company, p.Department,
		p.LocationRaw, nonNil(p.LocationCountries), p.RemoteStatus, p.RemoteScope, nonNil(p.RemoteCountries), p.RemoteRegion,
		p.EmploymentType, smin, smax, cur, per, amin, amax, clip(p.SalaryText, 300), p.DescriptionHTML, p.DescriptionText,
		nonNil(p.Tags), p.PostedAt, now, hash, key)
	return err
}

// reconcileDuplicates marks cross-source copies of the same role. Within a
// dedupe key, the canonical job is the active one from the highest-priority
// source (an employer ATS beats an aggregator), then the earliest seen.
// Same-source matches are never collapsed: a company may legitimately post
// several identical-looking requisitions.
func reconcileDuplicates(ctx context.Context, tx pgx.Tx, keys map[string]bool) error {
	if len(keys) == 0 {
		return nil
	}
	list := make([]string, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	_, err := tx.Exec(ctx, `
		WITH ranked AS (
			SELECT j.id, j.source_id,
				first_value(j.id) OVER w AS canonical_id,
				first_value(j.source_id) OVER w AS canonical_source
			FROM jobs j JOIN job_sources s ON s.id = j.source_id
			WHERE j.dedupe_key = ANY($1) AND j.expired_at IS NULL
			WINDOW w AS (PARTITION BY j.dedupe_key ORDER BY CASE s.kind WHEN 'REMOTEOK' THEN 2 ELSE 1 END, j.first_seen_at, j.id)
		)
		UPDATE jobs j SET duplicate_of = CASE WHEN r.source_id <> r.canonical_source THEN r.canonical_id END
		FROM ranked r
		WHERE j.id = r.id AND j.duplicate_of IS DISTINCT FROM (CASE WHEN r.source_id <> r.canonical_source THEN r.canonical_id END)`, list)
	if err != nil {
		return fmt.Errorf("reconcile duplicates: %w", err)
	}
	// Expired jobs never act as canonical copies.
	_, err = tx.Exec(ctx, `UPDATE jobs SET duplicate_of = NULL WHERE dedupe_key = ANY($1) AND expired_at IS NOT NULL AND duplicate_of IS NOT NULL`, list)
	return err
}
