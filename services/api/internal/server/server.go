// Package server assembles the HTTP router from module handlers.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/analytics"
	"github.com/careerpilot/careerpilot/services/api/internal/applications"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/discovery"
	"github.com/careerpilot/careerpilot/services/api/internal/documents"
	"github.com/careerpilot/careerpilot/services/api/internal/gmail"
	"github.com/careerpilot/careerpilot/services/api/internal/health"
	"github.com/careerpilot/careerpilot/services/api/internal/listings"
	"github.com/careerpilot/careerpilot/services/api/internal/matching"
	"github.com/careerpilot/careerpilot/services/api/internal/notifications"
	"github.com/careerpilot/careerpilot/services/api/internal/outreach"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/config"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/logging"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/observability"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
	"github.com/careerpilot/careerpilot/services/api/internal/preparation"
	"github.com/careerpilot/careerpilot/services/api/internal/privacy"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
	"github.com/careerpilot/careerpilot/services/api/internal/workflows"
)

// Deps are the collaborators the router needs. Nil optional deps are reported
// honestly as not configured, and their routes are not mounted.
type Deps struct {
	Config    config.Config
	Logger    *slog.Logger
	Checkers  []health.Checker
	AI        *aiclient.Client // nil when AI_SERVICE_URL is unset
	Auth      *auth.HTTP
	Profiles  *profiles.HTTP
	Documents *documents.HTTP
	Discovery *discovery.HTTP
	Listings  *listings.HTTP
	Matching  *matching.HTTP
	// Preparation serves tailored application documents (Phase 5).
	Preparation *preparation.HTTP
	// Applications and Automation (Phase 6).
	Applications *applications.HTTP
	Automation   *automation.HTTP
	// Gmail and Outreach (Phase 7). Gmail handlers report "not configured" themselves.
	Gmail    *gmail.HTTP
	Outreach *outreach.HTTP
	// Workflows is the agent orchestrator (Phase 8).
	Workflows *workflows.HTTP
	// Notifications and Analytics (Phase 9).
	Notifications *notifications.HTTP
	Analytics     *analytics.HTTP
	// GmailReady reports whether Gmail OAuth is configured.
	GmailReady bool
	// MatchScheduler recomputes matches after profile writes (nil disables).
	MatchScheduler *matching.Scheduler
	// StorageReady reports whether document storage is configured.
	StorageReady bool
	// Metrics records request metrics (nil disables). Limiter enforces the
	// global per-user/per-IP limit (nil disables). Draining fails readiness
	// during graceful shutdown.
	// Privacy serves data export and account deletion (Phase 10).
	Privacy  *privacy.HTTP
	Metrics  *observability.Metrics
	Limiter  ratelimit.Limiter
	Draining *atomic.Bool
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	if d.Metrics != nil {
		r.Use(d.Metrics.HTTP)
	}
	r.Use(
		httpx.RequestID(d.Logger),
		httpx.Recover,
		httpx.AccessLog,
		httpx.SecurityHeaders(d.Config.IsProduction()),
	)

	h := health.NewHandler(d.Config.Version, 3*time.Second, d.Checkers...).WithDrain(d.Draining)
	r.Get("/healthz", h.Live)
	r.Get("/readyz", h.Ready)

	r.Route("/api/v1", func(r chi.Router) {
		if d.Auth != nil {
			r.Use(d.Auth.LoadSession)
			if d.Limiter != nil && d.Config.RateLimitPerMinute > 0 {
				r.Use(globalLimit(d.Limiter, d.Auth, d.Config.RateLimitPerMinute))
			}
		}
		r.Group(func(r chi.Router) {
			r.Use(httpx.MaxBody(d.Config.MaxBodyBytes))
			r.Get("/meta", metaHandler(d))
			if d.Auth == nil {
				return
			}
			d.Auth.PublicRoutes(r)
			r.Group(func(r chi.Router) {
				r.Use(d.Auth.RequireAuth)
				if d.MatchScheduler != nil {
					r.Use(d.MatchScheduler.RecomputeOnWrite)
				}
				d.Auth.PrivateRoutes(r)
				if d.Profiles != nil {
					d.Profiles.Routes(r)
				}
				if d.Documents != nil {
					d.Documents.Routes(r)
				}
				if d.Discovery != nil {
					d.Discovery.Routes(r)
				}
				if d.Listings != nil {
					d.Listings.Routes(r)
				}
				if d.Matching != nil {
					d.Matching.Routes(r)
				}
				if d.Preparation != nil {
					d.Preparation.Routes(r)
				}
				if d.Applications != nil {
					d.Applications.Routes(r)
				}
				if d.Automation != nil {
					d.Automation.Routes(r)
				}
				if d.Gmail != nil {
					d.Gmail.Routes(r)
				}
				if d.Outreach != nil {
					d.Outreach.Routes(r)
				}
				if d.Workflows != nil {
					d.Workflows.Routes(r)
				}
				if d.Privacy != nil {
					d.Privacy.Routes(r)
				}
				if d.Notifications != nil {
					d.Notifications.Routes(r)
				}
				if d.Analytics != nil {
					d.Analytics.Routes(r)
				}
				if d.Discovery != nil {
					r.Group(func(r chi.Router) {
						r.Use(d.Auth.RequireAdmin)
						d.Discovery.AdminRoutes(r)
					})
				}
			})
		})
		if d.Auth != nil && d.Documents != nil {
			r.Group(func(r chi.Router) {
				// Multipart overhead allowance on top of the file limit.
				r.Use(httpx.MaxBody(d.Config.MaxUploadBytes+64<<10), d.Auth.RequireAuth)
				d.Documents.UploadRoutes(r)
			})
		}
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	})
	return observability.HTTPHandler(r)
}

// IntegrationStatus values are deliberately explicit so the UI never implies
// an integration works when it does not (master prompt §31, §52).
type IntegrationStatus string

const (
	StatusAvailable      IntegrationStatus = "available"
	StatusUnavailable    IntegrationStatus = "unavailable"
	StatusNotConfigured  IntegrationStatus = "not_configured"
	StatusNotImplemented IntegrationStatus = "not_implemented"
)

type integration struct {
	Status IntegrationStatus `json:"status"`
	Detail string            `json:"detail,omitempty"`
}

type metaResponse struct {
	Version      string                 `json:"version"`
	Environment  string                 `json:"environment"`
	Integrations map[string]integration `json:"integrations"`
}

func metaHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		storage := integration{Status: StatusAvailable}
		if !d.StorageReady {
			storage = integration{Status: StatusNotConfigured, Detail: "Document uploads are disabled until S3 storage is configured."}
		}
		service, llm := aiStatus(r.Context(), d.AI)
		resp := metaResponse{
			Version:     d.Config.Version,
			Environment: string(d.Config.Env),
			Integrations: map[string]integration{
				"ai_service":       service,
				"llm":              llm,
				"document_storage": storage,
				"job_sources":      jobSourcesStatus(d),
				"gmail":            gmailStatus(d),
			},
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}

// aiStatus reports the AI service and its LLM provider from one /info call.
func aiStatus(ctx context.Context, c *aiclient.Client) (service, llm integration) {
	if c == nil {
		ni := integration{Status: StatusNotConfigured, Detail: "AI_SERVICE_URL is not set."}
		return ni, ni
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		down := integration{Status: StatusUnavailable, Detail: "AI service is unreachable."}
		return down, down
	}
	if !info.LLMConfigured {
		return integration{Status: StatusAvailable}, integration{Status: StatusNotConfigured, Detail: "No LLM provider credentials configured."}
	}
	return integration{Status: StatusAvailable}, integration{Status: StatusAvailable, Detail: info.LLMProvider}
}

func jobSourcesStatus(d Deps) integration {
	if d.Discovery == nil {
		return integration{Status: StatusNotConfigured, Detail: "Job discovery is not enabled on this server."}
	}
	return integration{Status: StatusAvailable, Detail: "Greenhouse, Lever, Ashby and Remote OK public boards."}
}

func gmailStatus(d Deps) integration {
	if !d.GmailReady {
		return integration{Status: StatusNotConfigured, Detail: "Gmail needs Google OAuth credentials (GOOGLE_OAUTH_CLIENT_ID/SECRET) and DATA_ENCRYPTION_KEY."}
	}
	return integration{Status: StatusAvailable, Detail: "Connect your account in Settings."}
}

// globalLimit bounds requests per signed-in user, or per client IP when
// signed out, across the whole API (per-action limits still apply). It fails
// open when the limiter backend errors, so a Redis outage degrades rate
// limiting rather than the product; the error is logged.
func globalLimit(l ratelimit.Limiter, a *auth.HTTP, perMinute int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "global:ip:" + a.ClientIP(r)
			if p, ok := auth.PrincipalFrom(r.Context()); ok {
				key = "global:user:" + p.UserID.String()
			}
			ok, retry, err := l.Allow(r.Context(), key, perMinute, time.Minute)
			if err != nil {
				logging.FromContext(r.Context()).Warn("global rate limit unavailable", "err", err)
			} else if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "Too many requests. Please slow down and try again shortly."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
