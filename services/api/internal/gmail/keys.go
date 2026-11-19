package gmail

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/crypto"
)

// KeyStatus counts sealed values by the key version that sealed them.
type KeyStatus struct {
	Table   string
	Version byte
	Count   int64
}

// KeyVersions reports which key versions sealed the stored secrets, so an
// operator can confirm a rotation finished before removing a retired key.
func KeyVersions(ctx context.Context, pool *pgxpool.Pool) ([]KeyStatus, error) {
	rows, err := pool.Query(ctx, `
		SELECT 'email_accounts', get_byte(access_token_enc, 0), count(*) FROM email_accounts GROUP BY 2
		UNION ALL
		SELECT 'email_accounts.refresh', get_byte(refresh_token_enc, 0), count(*) FROM email_accounts GROUP BY 2
		UNION ALL
		SELECT 'oauth_states', get_byte(verifier_enc, 0), count(*) FROM oauth_states WHERE expires_at > now() GROUP BY 2
		ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyStatus
	for rows.Next() {
		var s KeyStatus
		var v int32
		if err := rows.Scan(&s.Table, &v, &s.Count); err != nil {
			return nil, err
		}
		s.Version = byte(v)
		out = append(out, s)
	}
	return out, rows.Err()
}

// RotateResult summarises a re-seal pass.
type RotateResult struct {
	Resealed   int
	Current    int
	Unreadable int
	// StatesDropped counts short-lived OAuth states removed instead of
	// re-sealed (an in-progress connect simply restarts).
	StatesDropped int64
}

// RotateKeys re-seals every stored Gmail token with the current key, in
// batches, each row under a row lock so a concurrent refresh cannot be lost.
// Values no configured key can open are left untouched and counted: the
// account must reconnect. Plaintext never leaves this function.
func RotateKeys(ctx context.Context, pool *pgxpool.Pool, sealer *crypto.Sealer) (RotateResult, error) {
	var res RotateResult
	tag, err := pool.Exec(ctx, `DELETE FROM oauth_states WHERE get_byte(verifier_enc, 0) <> $1 OR expires_at <= now()`, int32(sealer.Current()))
	if err != nil {
		return res, err
	}
	res.StatesDropped = tag.RowsAffected()

	var after uuid.UUID
	for {
		var ids []uuid.UUID
		rows, err := pool.Query(ctx, `SELECT user_id FROM email_accounts WHERE user_id > $1 ORDER BY user_id LIMIT 100`, after)
		if err != nil {
			return res, err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return res, err
		}
		if len(ids) == 0 {
			return res, nil
		}
		for _, id := range ids {
			outcome, err := rotateOne(ctx, pool, sealer, id)
			if err != nil {
				return res, fmt.Errorf("rotate %s: %w", id, err)
			}
			switch outcome {
			case "resealed":
				res.Resealed++
			case "current":
				res.Current++
			default:
				res.Unreadable++
			}
		}
		after = ids[len(ids)-1]
	}
}

func rotateOne(ctx context.Context, pool *pgxpool.Pool, sealer *crypto.Sealer, userID uuid.UUID) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var acc, ref []byte
	if err := tx.QueryRow(ctx, `SELECT access_token_enc, refresh_token_enc FROM email_accounts WHERE user_id = $1 FOR UPDATE`, userID).
		Scan(&acc, &ref); err != nil {
		return "", err
	}
	if crypto.Version(acc) == sealer.Current() && crypto.Version(ref) == sealer.Current() {
		return "current", nil
	}
	reseal := func(v []byte) ([]byte, bool) {
		pt, err := sealer.Open(v, userID[:])
		if err != nil {
			return nil, false
		}
		out, err := sealer.Seal(pt, userID[:])
		return out, err == nil
	}
	newAcc, ok1 := reseal(acc)
	newRef, ok2 := reseal(ref)
	if !ok1 || !ok2 {
		return "unreadable", nil
	}
	if _, err := tx.Exec(ctx, `UPDATE email_accounts SET access_token_enc = $2, refresh_token_enc = $3, updated_at = now() WHERE user_id = $1`,
		userID, newAcc, newRef); err != nil {
		return "", err
	}
	return "resealed", tx.Commit(ctx)
}
