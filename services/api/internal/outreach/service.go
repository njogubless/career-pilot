package outreach

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/gmail"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/preparation"
)

// Mailer is the user's connected mailbox (gmail.Service).
type Mailer interface {
	From(ctx context.Context, userID uuid.UUID) (string, error)
	Send(ctx context.Context, userID uuid.UUID, raw []byte, threadID string) (string, string, error)
	Thread(ctx context.Context, userID uuid.UUID, threadID string) ([]gmail.ThreadMessage, string, error)
}

type Service struct {
	pool     *pgxpool.Pool
	mail     Mailer // nil when Gmail is not configured
	ai       *aiclient.Client
	prep     *preparation.Service
	guard    *automation.Guard
	resolver Resolver
	log      *slog.Logger
	now      func() time.Time
}

type Deps struct {
	Pool     *pgxpool.Pool
	Mail     Mailer
	AI       *aiclient.Client
	Prep     *preparation.Service
	Guard    *automation.Guard
	Resolver Resolver
	Log      *slog.Logger
}

func NewService(d Deps) *Service {
	if d.Resolver == nil {
		d.Resolver = net.DefaultResolver
	}
	return &Service{pool: d.Pool, mail: d.Mail, ai: d.AI, prep: d.Prep, guard: d.Guard, resolver: d.Resolver, log: d.Log, now: time.Now}
}

// NewServiceWithGmail adapts a possibly-nil *gmail.Service (a typed nil
// must not become a non-nil interface).
func NewServiceWithGmail(d Deps, g *gmail.Service) *Service {
	if g != nil {
		d.Mail = g
	}
	return NewService(d)
}

type Settings struct {
	DailySendLimit        int    `json:"daily_send_limit"`
	RecipientCooldownDays int    `json:"recipient_cooldown_days"`
	FollowUpsEnabled      bool   `json:"follow_ups_enabled"`
	FollowUpAfterDays     int    `json:"follow_up_after_days"`
	MaxFollowUps          int    `json:"max_follow_ups"`
	Signature             string `json:"signature"`
}

func defaultSettings() Settings {
	return Settings{DailySendLimit: 10, RecipientCooldownDays: 3, FollowUpsEnabled: true, FollowUpAfterDays: 5, MaxFollowUps: 1}
}

func loadSettings(ctx context.Context, q db.Querier, userID uuid.UUID) (Settings, error) {
	s := defaultSettings()
	err := q.QueryRow(ctx, `SELECT daily_send_limit, recipient_cooldown_days, follow_ups_enabled, follow_up_after_days, max_follow_ups, signature
		FROM outreach_settings WHERE user_id = $1`, userID).Scan(&s.DailySendLimit, &s.RecipientCooldownDays, &s.FollowUpsEnabled,
		&s.FollowUpAfterDays, &s.MaxFollowUps, &s.Signature)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

func (s *Service) Settings(ctx context.Context, userID uuid.UUID) (Settings, error) {
	return loadSettings(ctx, s.pool, userID)
}

// UpdateSettings stores preferences. Disabling follow-ups, or lowering the
// maximum, cancels follow-ups that are no longer wanted.
func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, in Settings) (Settings, error) {
	errs := map[string]string{}
	if in.DailySendLimit < 1 || in.DailySendLimit > 50 {
		errs["daily_send_limit"] = "Between 1 and 50."
	}
	if in.RecipientCooldownDays < 1 || in.RecipientCooldownDays > 60 {
		errs["recipient_cooldown_days"] = "Between 1 and 60."
	}
	if in.FollowUpAfterDays < 2 || in.FollowUpAfterDays > 30 {
		errs["follow_up_after_days"] = "Between 2 and 30."
	}
	if in.MaxFollowUps < 0 || in.MaxFollowUps > 2 {
		errs["max_follow_ups"] = "Between 0 and 2."
	}
	sig := limitText(errs, "signature", in.Signature, 500)
	if len(errs) > 0 {
		return Settings{}, httpx.Validation(errs)
	}
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO outreach_settings (user_id, daily_send_limit, recipient_cooldown_days, follow_ups_enabled,
			follow_up_after_days, max_follow_ups, signature) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (user_id) DO UPDATE SET daily_send_limit=EXCLUDED.daily_send_limit, recipient_cooldown_days=EXCLUDED.recipient_cooldown_days,
			follow_ups_enabled=EXCLUDED.follow_ups_enabled, follow_up_after_days=EXCLUDED.follow_up_after_days,
			max_follow_ups=EXCLUDED.max_follow_ups, signature=EXCLUDED.signature, updated_at=now()`,
			userID, in.DailySendLimit, in.RecipientCooldownDays, in.FollowUpsEnabled, in.FollowUpAfterDays, in.MaxFollowUps, sig); err != nil {
			return err
		}
		if !in.FollowUpsEnabled {
			return cancelFollowUps(ctx, tx, `m.user_id = $1`, userID, "Follow-ups turned off")
		}
		_, err := tx.Exec(ctx, `UPDATE outreach_follow_ups SET status = 'CANCELLED', cancelled_reason = 'Follow-up limit lowered', updated_at = now()
			WHERE user_id = $1 AND status = 'SCHEDULED' AND sequence > $2`, userID, in.MaxFollowUps)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	return loadSettings(ctx, s.pool, userID)
}

// cancelFollowUps cancels SCHEDULED follow-ups of messages matching where
// (a condition on outreach_messages m with one parameter $1).
func cancelFollowUps(ctx context.Context, q db.Querier, where string, arg any, reason string) error {
	_, err := q.Exec(ctx, `UPDATE outreach_follow_ups f SET status = 'CANCELLED', cancelled_reason = $2, updated_at = now()
		FROM outreach_messages m WHERE m.id = f.message_id AND f.status = 'SCHEDULED' AND `+where, arg, reason)
	return err
}
