// Command careerpilot runs the CareerPilot core platform.
//
//	careerpilot serve            start the HTTP API (applies migrations first)
//	careerpilot worker           run durable background jobs (River)
//	careerpilot migrate up       apply pending migrations
//	careerpilot migrate status   print current/latest migration version
//	careerpilot healthcheck      probe the local API (container healthcheck)
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/documents"
	"github.com/careerpilot/careerpilot/services/api/internal/health"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/config"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/crypto"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/logging"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/mailer"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
	"github.com/careerpilot/careerpilot/services/api/internal/server"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: careerpilot <serve|worker|migrate up|migrate status|healthcheck>")
	}
	if args[0] == "healthcheck" {
		// Container probe for distroless images (no shell/curl). Needs no config.
		return healthcheck()
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "service", "api", "app_version", cfg.Version)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "serve":
		return serve(ctx, cfg, log)
	case "worker":
		return worker(ctx, cfg, log)
	case "migrate":
		return migrate(ctx, cfg, log, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func migrateAll(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	if err := db.MigrateUp(ctx, pool, log); err != nil {
		return err
	}
	return jobs.Migrate(ctx, pool, log)
}

func migrate(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()
	sub := "up"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "up":
		return migrateAll(ctx, pool, log)
	case "status":
		cur, latest, err := db.MigrationStatus(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Printf("current=%d latest=%d\n", cur, latest)
		return nil
	default:
		return fmt.Errorf("unknown migrate subcommand %q", sub)
	}
}

// infra holds shared dependencies for serve and worker.
type infra struct {
	pool     *pgxpool.Pool
	redis    *redis.Client
	store    documents.Storage
	ai       *aiclient.Client
	checkers []health.Checker
}

func (i *infra) close() {
	if i.redis != nil {
		_ = i.redis.Close()
	}
	i.pool.Close()
}

func buildInfra(ctx context.Context, cfg config.Config, log *slog.Logger, aiTimeout time.Duration) (*infra, error) {
	pool, err := db.Connect(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConn)
	if err != nil {
		return nil, err
	}
	in := &infra{pool: pool}
	in.checkers = append(in.checkers, health.Checker{Name: "database", Required: true, Check: pool.Ping})

	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			in.close()
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		in.redis = redis.NewClient(opts)
		rdb := in.redis
		in.checkers = append(in.checkers, health.Checker{Name: "redis", Required: cfg.IsProduction(),
			Check: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }})
	} else {
		in.checkers = append(in.checkers, health.Checker{Name: "redis", Check: notConfigured})
	}

	if cfg.StorageConfigured() {
		s3, err := documents.NewS3(documents.S3Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, UseSSL: cfg.S3UseSSL})
		if err != nil {
			in.close()
			return nil, err
		}
		if !cfg.IsProduction() {
			ectx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := s3.EnsureBucket(ectx, cfg.S3Region); err != nil {
				log.Warn("could not ensure dev bucket", "err", err)
			}
			cancel()
		}
		in.store = s3
		in.checkers = append(in.checkers, health.Checker{Name: "storage", Required: cfg.IsProduction(), Check: s3.Ping})
	} else {
		log.Warn("object storage not configured: document uploads disabled")
		in.checkers = append(in.checkers, health.Checker{Name: "storage", Check: notConfigured})
	}

	if cfg.AIServiceURL != "" {
		in.ai = aiclient.New(cfg.AIServiceURL, cfg.AIServiceToken, aiTimeout)
		in.checkers = append(in.checkers, health.Checker{Name: "ai_service", Check: in.ai.Health})
	} else {
		in.checkers = append(in.checkers, health.Checker{Name: "ai_service", Check: notConfigured})
	}
	return in, nil
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	in, err := buildInfra(ctx, cfg, log, cfg.AIServiceTimeout)
	if err != nil {
		return err
	}
	defer in.close()
	if err := migrateAll(ctx, in.pool, log); err != nil {
		return err
	}

	jobClient, err := jobs.NewInsertClient(in.pool, log)
	if err != nil {
		return err
	}

	var limiter ratelimit.Limiter = ratelimit.NewMemory()
	if in.redis != nil {
		limiter = ratelimit.NewRedis(in.redis)
	} else {
		log.Warn("rate limiting is process-local (no REDIS_URL)")
	}

	var mail mailer.Mailer
	if cfg.SMTPAddr != "" {
		mail = mailer.SMTP{Addr: cfg.SMTPAddr, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom}
	} else {
		// Config validation forbids this in production.
		log.Warn("SMTP not configured: emails are written to the log (development only)")
		mail = mailer.Log{L: log}
	}

	secret := []byte(cfg.SessionSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
		log.Warn("SESSION_SECRET not set: using an ephemeral secret; CSRF tokens reset on restart (development only)")
	}

	authSvc := auth.NewService(in.pool, limiter, mail, auth.Settings{
		IdleTTL: cfg.SessionIdleTTL, AbsoluteTTL: cfg.SessionAbsTTL, AppBaseURL: cfg.AppBaseURL,
		Secret: secret, Argon2: crypto.DefaultArgon2,
	}, log)
	authH := auth.NewHTTP(authSvc, cfg.CookieSecure, cfg.TrustProxyHeaders)
	profilesH := profiles.NewHTTP(profiles.NewService(in.pool))
	docSvc := documents.NewService(in.pool, in.store, jobClient, in.ai, cfg.MaxUploadBytes, log)
	docH := documents.NewHTTP(docSvc, authH, cfg.MaxUploadBytes)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: server.NewRouter(server.Deps{
			Config: cfg, Logger: log, Checkers: in.checkers, AI: in.ai,
			Auth: authH, Profiles: profilesH, Documents: docH, StorageReady: in.store != nil,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down", "timeout", cfg.HTTPShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

func worker(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	// Extraction calls an LLM and can take minutes; the per-job timeout is
	// enforced by River, so the HTTP client timeout is generous.
	in, err := buildInfra(ctx, cfg, log, 4*time.Minute)
	if err != nil {
		return err
	}
	defer in.close()
	if err := migrateAll(ctx, in.pool, log); err != nil {
		return err
	}

	workers := river.NewWorkers()
	if in.store == nil || in.ai == nil {
		// Still registered, so queued jobs fail explicitly instead of waiting forever.
		log.Warn("CV extraction cannot run: storage or AI service not configured")
	}
	extract := &documents.ExtractCVWorker{Pool: in.pool, AI: in.ai, Log: log}
	if in.store != nil {
		extract.Store = in.store
	}
	river.AddWorker(workers, extract)

	client, err := jobs.NewWorkerClient(in.pool, workers, log, 4)
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}
	log.Info("worker started")
	<-ctx.Done()
	log.Info("worker stopping")
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.Stop(stopCtx)
}

func notConfigured(context.Context) error { return health.ErrNotConfigured }

func healthcheck() error {
	addr := os.Getenv("API_HTTP_ADDR")
	switch {
	case addr == "":
		addr = "127.0.0.1:8080"
	case strings.HasPrefix(addr, ":"):
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz status %d", resp.StatusCode)
	}
	return nil
}
