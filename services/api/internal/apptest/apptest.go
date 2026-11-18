// Package apptest builds a fully wired API against a throwaway PostgreSQL
// database for integration tests. Tests are skipped unless TEST_DATABASE_URL
// points at a server where the user may create databases.
package apptest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/applications"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/discovery"
	"github.com/careerpilot/careerpilot/services/api/internal/documents"
	"github.com/careerpilot/careerpilot/services/api/internal/gmail"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
	"github.com/careerpilot/careerpilot/services/api/internal/listings"
	"github.com/careerpilot/careerpilot/services/api/internal/matching"
	"github.com/careerpilot/careerpilot/services/api/internal/outreach"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/config"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/crypto"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/mailer"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
	"github.com/careerpilot/careerpilot/services/api/internal/preparation"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
	"github.com/careerpilot/careerpilot/services/api/internal/server"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
	"github.com/careerpilot/careerpilot/services/api/internal/workflows"
)

// NewDB creates an isolated, migrated database and drops it after the test.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	name := "cp_test_" + strings.ReplaceAll(ids.New().String(), "-", "")[:20]

	adminConn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create db: %v", err)
	}
	_ = adminConn.Close(ctx)

	u, _ := url.Parse(admin)
	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String(), 10)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.MigrateUp(ctx, pool, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := jobs.Migrate(ctx, pool, log); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	return pool
}

// CaptureMailer records sent messages.
type CaptureMailer struct {
	mu   sync.Mutex
	Sent []mailer.Message
}

func (m *CaptureMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sent = append(m.Sent, msg)
	return nil
}

func (m *CaptureMailer) Last() (mailer.Message, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Sent) == 0 {
		return mailer.Message{}, false
	}
	return m.Sent[len(m.Sent)-1], true
}

type App struct {
	Handler   http.Handler
	Pool      *pgxpool.Pool
	Store     *documents.Memory
	Mail      *CaptureMailer
	Jobs      *jobs.Client
	AI        *aiclient.Client
	Log       *slog.Logger
	Discovery *discovery.Service
	Matching  *matching.Service
	// Preparation runs generation in tests (its worker has no insert client).
	Preparation *preparation.Service
	// Applications exposes the service for agent-actor tests.
	Applications *applications.Service
	Outreach     *outreach.Service
	Workflows    *workflows.Engine
}

// Options customise the app under test.
type Options struct {
	AIURL     string // empty: AI service not configured
	NoStorage bool
	Adapters  sources.Registry // job-source adapters (fakes in tests)
	// GoogleURL points Google OAuth and the Gmail API at a fake server
	// (empty: Gmail not configured).
	GoogleURL string
	Resolver  outreach.Resolver
}

func New(t *testing.T, opts Options) *App {
	t.Helper()
	pool := NewDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{Env: config.EnvTest, Version: "test", MaxBodyBytes: 1 << 20, MaxUploadBytes: 2 << 20,
		AppBaseURL: "http://app.test", SessionIdleTTL: time.Hour, SessionAbsTTL: 24 * time.Hour}

	jobClient, err := jobs.NewInsertClient(pool, log)
	if err != nil {
		t.Fatal(err)
	}
	mail := &CaptureMailer{}
	authSvc := auth.NewService(pool, ratelimit.NewMemory(), mail, auth.Settings{
		IdleTTL: time.Hour, AbsoluteTTL: 24 * time.Hour, AppBaseURL: cfg.AppBaseURL,
		Secret: []byte("test-secret-test-secret-test-sec"), Argon2: crypto.Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1},
	}, log)
	authH := auth.NewHTTP(authSvc, false, false)

	var ai *aiclient.Client
	if opts.AIURL != "" {
		ai = aiclient.New(opts.AIURL, "svc-token", 5*time.Second)
	}
	store := documents.NewMemory()
	var st documents.Storage = store
	if opts.NoStorage {
		st = nil
	}
	docH := documents.NewHTTP(documents.NewService(pool, st, jobClient, ai, cfg.MaxUploadBytes, log), authH, cfg.MaxUploadBytes)

	if opts.Adapters == nil {
		opts.Adapters = sources.Registry{}
	}
	disc := discovery.NewService(pool, opts.Adapters, log)
	profSvc := profiles.NewService(pool)
	matchSvc := matching.NewService(pool, ai, profSvc, log)
	sched := &matching.Scheduler{Pool: pool, Queue: jobClient, Log: log}
	prep := preparation.NewService(pool, ai, profSvc, jobClient, log)
	appsSvc := applications.NewService(pool, automation.NewGuard(pool), matching.AlgorithmVersion, log)
	var gmailSvc *gmail.Service
	if opts.GoogleURL != "" {
		g := opts.GoogleURL
		var err error
		gmailSvc, err = gmail.New(pool, gmail.Config{ClientID: "test-client", ClientSecret: "test-secret",
			RedirectURL: "http://app.test/api/v1/integrations/gmail/callback", AuthURL: g + "/auth", TokenURL: g + "/token",
			RevokeURL: g + "/revoke", APIBase: g}, []byte("0123456789abcdef0123456789abcdef"), log)
		if err != nil {
			t.Fatal(err)
		}
	}
	engine := workflows.NewEngine(workflows.Deps{Pool: pool, Queue: jobClient, Prof: profSvc, Match: matchSvc, Apps: appsSvc,
		Prep: prep, Guard: automation.NewGuard(pool), AI: ai, Log: log})
	outSvc := outreach.NewServiceWithGmail(outreach.Deps{Pool: pool, AI: ai, Prep: prep, Guard: automation.NewGuard(pool),
		Resolver: opts.Resolver, Log: log}, gmailSvc)
	h := server.NewRouter(server.Deps{Config: cfg, Logger: log, AI: ai, Auth: authH,
		Profiles: profiles.NewHTTP(profSvc), Documents: docH, StorageReady: !opts.NoStorage,
		Discovery: discovery.NewHTTP(disc, jobClient, ratelimit.NewMemory()), Listings: listings.NewHTTP(pool, matching.AlgorithmVersion),
		Matching: matching.NewHTTP(matchSvc, sched, ratelimit.NewMemory()), MatchScheduler: sched,
		Preparation:  preparation.NewHTTP(prep, authH, ratelimit.NewMemory()),
		Applications: applications.NewHTTP(appsSvc, authH), Automation: automation.NewHTTP(automation.NewService(pool), authH),
		Gmail: gmail.NewHTTP(gmailSvc, authH, cfg.AppBaseURL), Outreach: outreach.NewHTTP(outSvc, authH, ratelimit.NewMemory()),
		GmailReady: gmailSvc != nil, Workflows: workflows.NewHTTP(engine, authH, ratelimit.NewMemory())})
	return &App{Handler: h, Pool: pool, Store: store, Mail: mail, Jobs: jobClient, AI: ai, Log: log, Discovery: disc, Matching: matchSvc,
		Preparation: prep, Applications: appsSvc, Outreach: outSvc, Workflows: engine}
}

// Exec runs SQL directly (assertions on persisted state).
func (a *App) Exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := a.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (a *App) QueryInt(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}
