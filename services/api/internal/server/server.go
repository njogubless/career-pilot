// Package server assembles the HTTP router from module handlers.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/applications"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/discovery"
	"github.com/careerpilot/careerpilot/services/api/internal/documents"
	"github.com/careerpilot/careerpilot/services/api/internal/gmail"
	"github.com/careerpilot/careerpilot/services/api/internal/health"
	"github.com/careerpilot/careerpilot/services/api/internal/listings"
	"github.com/careerpilot/careerpilot/services/api/internal/matching"
	"github.com/careerpilot/careerpilot/services/api/internal/outreach"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/config"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/preparation"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
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
	// GmailReady reports whether Gmail OAuth is configured.
	GmailReady bool
	// MatchScheduler recomputes matches after profile writes (nil disables).
	MatchScheduler *matching.Scheduler
	// StorageReady reports whether document storage is configured.
	StorageReady bool
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(
		httpx.RequestID(d.Logger),
		httpx.Recover,
		httpx.AccessLog,
		httpx.SecurityHeaders(d.Config.IsProduction()),
	)

	h := health.NewHandler(d.Config.Version, 3*time.Second, d.Checkers...)
	r.Get("/healthz", h.Live)
	r.Get("/readyz", h.Ready)

	r.Route("/api/v1", func(r chi.Router) {
		if d.Auth != nil {
			r.Use(d.Auth.LoadSession)
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
	return r
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
		resp := metaResponse{
			Version:     d.Config.Version,
			Environment: string(d.Config.Env),
			Integrations: map[string]integration{
				"ai_service":       aiStatus(r.Context(), d.AI, "service"),
				"llm":              aiStatus(r.Context(), d.AI, "llm"),
				"document_storage": storage,
				"job_sources":      jobSourcesStatus(d),
				"gmail":            gmailStatus(d),
			},
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}

func aiStatus(ctx context.Context, c *aiclient.Client, which string) integration {
	if c == nil {
		return integration{Status: StatusNotConfigured, Detail: "AI_SERVICE_URL is not set."}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		return integration{Status: StatusUnavailable, Detail: "AI service is unreachable."}
	}
	if which == "service" {
		return integration{Status: StatusAvailable}
	}
	if !info.LLMConfigured {
		return integration{Status: StatusNotConfigured, Detail: "No LLM provider credentials configured."}
	}
	return integration{Status: StatusAvailable, Detail: info.LLMProvider}
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
