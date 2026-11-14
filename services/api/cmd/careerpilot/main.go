// Command careerpilot runs the CareerPilot core platform.
//
//	careerpilot serve            start the HTTP API (applies migrations first)
//	careerpilot migrate up       apply pending migrations
//	careerpilot migrate status   print current/latest migration version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/health"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/config"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/logging"
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
		return errors.New("usage: careerpilot <serve|migrate up|migrate status|healthcheck>")
	}
	if args[0] == "healthcheck" {
		// Container probe for distroless images (no shell/curl). Needs no config.
		return healthcheck()
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "service", "api", "version", cfg.Version)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "serve":
		return serve(ctx, cfg, log)
	case "migrate":
		return migrate(ctx, cfg, log, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
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
		return db.MigrateUp(ctx, pool, log)
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

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool, log); err != nil {
		return err
	}

	checkers := []health.Checker{
		{Name: "database", Required: true, Check: pool.Ping},
	}

	var rdb *redis.Client
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("parse REDIS_URL: %w", err)
		}
		rdb = redis.NewClient(opts)
		defer rdb.Close()
		checkers = append(checkers, health.Checker{Name: "redis", Required: cfg.IsProduction(),
			Check: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }})
	} else {
		checkers = append(checkers, health.Checker{Name: "redis", Check: notConfigured})
	}

	var ai *aiclient.Client
	if cfg.AIServiceURL != "" {
		ai = aiclient.New(cfg.AIServiceURL, cfg.AIServiceToken, cfg.AIServiceTimeout)
		checkers = append(checkers, health.Checker{Name: "ai_service", Check: ai.Health})
	} else {
		checkers = append(checkers, health.Checker{Name: "ai_service", Check: notConfigured})
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.NewRouter(server.Deps{Config: cfg, Logger: log, Checkers: checkers, AI: ai}),
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
