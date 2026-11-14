// Package audit records append-only audit events (SECURITY_ARCHITECTURE.md §7).
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type Outcome string

const (
	Success Outcome = "SUCCESS"
	Failure Outcome = "FAILURE"
	Denied  Outcome = "DENIED"
)

type Actor string

const (
	ActorUser   Actor = "USER"
	ActorAgent  Actor = "AGENT"
	ActorSystem Actor = "SYSTEM"
)

// Event is one audit record. Metadata must never contain secrets, tokens,
// passwords or document contents.
type Event struct {
	UserID      *uuid.UUID
	Actor       Actor
	Action      string // e.g. "auth.login", "profile.extraction.apply"
	SubjectType string
	SubjectID   *uuid.UUID
	Outcome     Outcome
	Metadata    map[string]any
	IP          string
}

// Record inserts an audit event using q (a pool or a transaction). Callers
// performing a state change should record within the same transaction.
func Record(ctx context.Context, q db.Querier, e Event) error {
	meta := e.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("audit: marshal metadata: %w", err)
	}
	actor := e.Actor
	if actor == "" {
		actor = ActorUser
	}
	_, err = q.Exec(ctx, `
		INSERT INTO audit_events (id, user_id, actor, action, subject_type, subject_id, outcome, metadata, ip, request_id)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''))`,
		ids.New(), e.UserID, actor, e.Action, e.SubjectType, e.SubjectID, e.Outcome, raw, e.IP, httpx.RequestIDFrom(ctx))
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}
