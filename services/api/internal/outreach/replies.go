package outreach

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/gmail"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

const (
	replyWindow  = 45 * 24 * time.Hour
	sendingStale = 10 * time.Minute
)

var bounceFrom = regexp.MustCompile(`(?i)mailer-daemon|postmaster@|mail delivery (subsystem|system)`)

type PollStats struct {
	Threads int `json:"threads_checked"`
	Replies int `json:"replies_found"`
	Bounces int `json:"bounces_found"`
}

type sentMsg struct {
	ID, ContactID   uuid.UUID
	ApplicationID   *uuid.UUID
	GmailID, Thread string
	SentAt          time.Time
}

// PollUser checks the threads of the user's recent emails for replies and
// bounces, using header metadata only. A reply is recorded once, associated
// with the message's application, and cancels pending follow-ups.
func (s *Service) PollUser(ctx context.Context, userID uuid.UUID) (PollStats, error) {
	var st PollStats
	if s.mail == nil {
		return st, httpx.NewError(httpx.CodeUnavailable, "Gmail is not configured on this server.")
	}
	// Surfaces a missing or revoked connection even when there is nothing to check.
	if _, err := s.mail.From(ctx, userID); err != nil {
		return st, err
	}
	// A send interrupted after SENDING has an unknown outcome; never resend it.
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_messages SET status = 'UNKNOWN',
		error_message = 'The send was interrupted. Check your Gmail Sent folder before trying again.', updated_at = now()
		WHERE user_id = $1 AND status = 'SENDING' AND updated_at < $2`, userID, s.now().Add(-sendingStale)); err != nil {
		return st, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, contact_id, application_id, gmail_message_id, gmail_thread_id, sent_at FROM outreach_messages
		WHERE user_id = $1 AND status = 'SENT' AND sent_at > $2 ORDER BY sent_at`, userID, s.now().Add(-replyWindow))
	if err != nil {
		return st, err
	}
	sent, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sentMsg, error) {
		var m sentMsg
		return m, r.Scan(&m.ID, &m.ContactID, &m.ApplicationID, &m.GmailID, &m.Thread, &m.SentAt)
	})
	if err != nil {
		return st, err
	}
	threads := map[string][]sentMsg{}
	var order []string
	for _, m := range sent {
		if _, ok := threads[m.Thread]; !ok {
			order = append(order, m.Thread)
		}
		threads[m.Thread] = append(threads[m.Thread], m)
	}
	for _, thread := range order {
		ours := threads[thread]
		msgs, me, err := s.mail.Thread(ctx, userID, thread)
		if errors.Is(err, gmail.ErrNeedsReconnect) || errors.Is(err, gmail.ErrNotConnected) {
			return st, err
		}
		if err != nil {
			s.log.Warn("thread poll failed", "err", err)
			continue
		}
		st.Threads++
		ourIDs := map[string]bool{}
		for _, m := range ours {
			ourIDs[m.GmailID] = true
		}
		for _, tm := range msgs {
			if ourIDs[tm.ID] || slices.Contains(tm.LabelIDs, "SENT") || (me != "" && strings.Contains(strings.ToLower(tm.From), strings.ToLower(me))) {
				continue
			}
			// Attribute the reply to our latest message sent before it.
			var target *sentMsg
			for i := range ours {
				if !ours[i].SentAt.After(tm.ReceivedAt) {
					target = &ours[i]
				}
			}
			if target == nil {
				continue
			}
			bounce := bounceFrom.MatchString(tm.From)
			created, err := s.recordReply(ctx, userID, *target, ours, tm, bounce)
			if err != nil {
				return st, err
			}
			if created {
				if bounce {
					st.Bounces++
				} else {
					st.Replies++
				}
			}
		}
	}
	return st, nil
}

func (s *Service) recordReply(ctx context.Context, userID uuid.UUID, target sentMsg, thread []sentMsg, tm gmail.ThreadMessage, bounce bool) (bool, error) {
	created := false
	from := tm.From
	if len(from) > 320 {
		from = from[:320]
	}
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		id := ids.New()
		tag, err := tx.Exec(ctx, `INSERT INTO outreach_replies (id, user_id, message_id, application_id, gmail_message_id, from_address, is_bounce, received_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (user_id, gmail_message_id) DO NOTHING`,
			id, userID, target.ID, target.ApplicationID, tm.ID, from, bounce, tm.ReceivedAt)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		created = true
		threadIDs := make([]uuid.UUID, 0, len(thread))
		for _, m := range thread {
			threadIDs = append(threadIDs, m.ID)
		}
		reason := "Reply received"
		if bounce {
			reason = "Message bounced"
			if _, err := tx.Exec(ctx, `UPDATE recruiter_contacts SET email_verification = 'BOUNCED', verified_at = now(), updated_at = now()
				WHERE id = $1`, target.ContactID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET replied_at = coalesce(replied_at, $2), updated_at = now()
			WHERE id = ANY($1) AND sent_at <= $2`, threadIDs, tm.ReceivedAt); err != nil {
			return err
		}
		if err := cancelFollowUps(ctx, tx, `m.id = ANY($1)`, threadIDs, reason); err != nil {
			return err
		}
		// Unsent follow-up drafts in this thread are no longer appropriate.
		if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET status = 'CANCELLED', error_message = $2, updated_at = now()
			WHERE parent_id = ANY($1) AND status IN ('DRAFT','APPROVED') AND origin = 'FOLLOW_UP_SCHEDULER'`, threadIDs, reason); err != nil {
			return err
		}
		action := "outreach.reply.detected"
		if bounce {
			action = "outreach.bounce.detected"
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Actor: audit.ActorSystem, Action: action, SubjectType: "outreach_message",
			SubjectID: &target.ID, Outcome: audit.Success, Metadata: map[string]any{"application_id": target.ApplicationID}})
	})
	return created, err
}

// RunFollowUps turns due follow-ups into DRAFTS for the user to approve.
// Follow-ups are never sent automatically. They are cancelled when a reply
// arrived, follow-ups were turned off, or the contact must not be emailed;
// they wait (stay scheduled) while automation is paused.
func (s *Service) RunFollowUps(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM outreach_follow_ups WHERE status = 'SCHEDULED' AND due_at <= $1 ORDER BY due_at LIMIT 200`, s.now())
	if err != nil {
		return 0, err
	}
	due, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	drafted := 0
	for _, fid := range due {
		ok, err := s.followUp(ctx, fid)
		if err != nil {
			s.log.Warn("follow-up failed", "follow_up_id", fid, "err", err)
			continue
		}
		if ok {
			drafted++
		}
	}
	return drafted, nil
}

func (s *Service) followUp(ctx context.Context, fid uuid.UUID) (bool, error) {
	drafted := false
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var userID, msgID uuid.UUID
		var seq int
		err := tx.QueryRow(ctx, `SELECT user_id, message_id, sequence FROM outreach_follow_ups WHERE id = $1 AND status = 'SCHEDULED' FOR UPDATE SKIP LOCKED`,
			fid).Scan(&userID, &msgID, &seq)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		cancel := func(reason string) error {
			_, err := tx.Exec(ctx, `UPDATE outreach_follow_ups SET status = 'CANCELLED', cancelled_reason = $2, updated_at = now() WHERE id = $1`, fid, reason)
			return err
		}
		root, err := getMessage(ctx, tx, userID, msgID)
		if err != nil {
			return err
		}
		c, err := getContact(ctx, tx, userID, root.ContactID)
		if err != nil {
			return err
		}
		set, err := loadSettings(ctx, tx, userID)
		if err != nil {
			return err
		}
		switch {
		case root.RepliedAt != nil:
			return cancel("Reply received")
		case !set.FollowUpsEnabled || seq > set.MaxFollowUps:
			return cancel("Follow-ups turned off")
		case c.DoNotContact:
			return cancel("Contact marked do-not-contact")
		case c.EmailVerification == "BOUNCED" || c.EmailVerification == "INVALID":
			return cancel("Address bounced or invalid")
		}
		// The previous follow-up must have been sent before drafting the next.
		var parent uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM outreach_messages WHERE user_id = $1 AND (id = $2 OR parent_id = $2) AND status = 'SENT'
			ORDER BY sent_at DESC LIMIT 1`, userID, msgID).Scan(&parent)
		if err != nil {
			return err
		}
		if seq > 1 {
			var prevSent bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outreach_messages WHERE parent_id = $1 AND kind = 'FOLLOW_UP' AND status = 'SENT')`,
				msgID).Scan(&prevSent); err != nil {
				return err
			}
			if !prevSent {
				return cancel("The previous follow-up was not sent")
			}
		}
		if err := s.guard.Authorize(ctx, tx, userID, audit.ActorSystem, automation.ActionPrepare); err != nil {
			var d *automation.Denied
			if errors.As(err, &d) {
				return nil // paused or discovery-only: leave scheduled, try later
			}
			return err
		}
		var name, role string
		_ = tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, userID).Scan(&name)
		if root.JobID != nil {
			_ = tx.QueryRow(ctx, `SELECT 'the ' || title || ' role' FROM jobs WHERE id = $1`, *root.JobID).Scan(&role)
		}
		if role == "" {
			role = "the role"
		}
		greeting := "Hello,"
		if fn := firstName(c.Name); fn != "" {
			greeting = "Hi " + fn + ","
		}
		body := assemble(greeting, []string{"I wanted to follow up on my earlier email about " + role +
			", in case it got buried. I'd welcome the chance to talk if the role is still open."}, false, "Best regards,", name, set.Signature)
		draftID := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO outreach_messages (id, user_id, contact_id, job_id, application_id, parent_id, kind, status, origin,
			to_email, subject, body) VALUES ($1,$2,$3,$4,$5,$6,'FOLLOW_UP','DRAFT','FOLLOW_UP_SCHEDULER',$7,$8,$9)`,
			draftID, userID, root.ContactID, root.JobID, root.ApplicationID, parent, root.ToEmail, root.Subject, body); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE outreach_follow_ups SET status = 'DRAFTED', draft_id = $2, updated_at = now() WHERE id = $1`, fid, draftID); err != nil {
			return err
		}
		drafted = true
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Actor: audit.ActorSystem, Action: "outreach.follow_up.drafted",
			SubjectType: "outreach_message", SubjectID: &draftID, Outcome: audit.Success, Metadata: map[string]any{"sequence": seq}})
	})
	return drafted, err
}

// ---- Background jobs ----

type PollRepliesArgs struct{}

func (PollRepliesArgs) Kind() string { return "outreach_poll_replies" }
func (PollRepliesArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByPeriod: 5 * time.Minute}}
}

type PollRepliesWorker struct {
	river.WorkerDefaults[PollRepliesArgs]
	Service *Service
}

func (w *PollRepliesWorker) Timeout(*river.Job[PollRepliesArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *PollRepliesWorker) Work(ctx context.Context, _ *river.Job[PollRepliesArgs]) error {
	s := w.Service
	if s.mail == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT m.user_id FROM outreach_messages m JOIN email_accounts a ON a.user_id = m.user_id
		WHERE a.status = 'CONNECTED' AND ((m.status = 'SENT' AND m.sent_at > $1) OR m.status = 'SENDING')`, s.now().Add(-replyWindow))
	if err != nil {
		return err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, u := range users {
		if st, err := s.PollUser(ctx, u); err != nil {
			s.log.Warn("reply poll failed", "user_id", u, "err", err)
		} else if st.Replies+st.Bounces > 0 {
			s.log.Info("replies detected", "user_id", u, "replies", st.Replies, "bounces", st.Bounces)
		}
	}
	return nil
}

type FollowUpsArgs struct{}

func (FollowUpsArgs) Kind() string { return "outreach_follow_ups" }
func (FollowUpsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByPeriod: 5 * time.Minute}}
}

type FollowUpsWorker struct {
	river.WorkerDefaults[FollowUpsArgs]
	Service *Service
}

func (w *FollowUpsWorker) Work(ctx context.Context, _ *river.Job[FollowUpsArgs]) error {
	// Replies first, so a reply received since the last poll cancels its follow-up.
	if w.Service.mail != nil {
		_ = (&PollRepliesWorker{Service: w.Service}).Work(ctx, nil)
	}
	n, err := w.Service.RunFollowUps(ctx)
	if n > 0 {
		w.Service.log.Info("follow-up drafts created", "count", n)
	}
	return err
}

func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return PollRepliesArgs{}, nil }, nil),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return FollowUpsArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
