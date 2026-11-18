package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
)

type HTTP struct {
	e       *Engine
	authH   *auth.HTTP
	limiter ratelimit.Limiter
}

func NewHTTP(e *Engine, authH *auth.HTTP, limiter ratelimit.Limiter) *HTTP {
	return &HTTP{e: e, authH: authH, limiter: limiter}
}

func (h *HTTP) Routes(r chi.Router) {
	r.Get("/agents", h.registry)
	r.Get("/workflows/templates", h.templates)
	r.Post("/workflows/interpret", h.interpret)
	r.Post("/workflows/preview", h.preview)
	r.Get("/workflows", h.list)
	r.Post("/workflows", h.create)
	r.Get("/workflows/{id}", h.get)
	r.Post("/workflows/{id}/cancel", h.control((*Engine).Cancel))
	r.Post("/workflows/{id}/pause", h.control((*Engine).Pause))
	r.Post("/workflows/{id}/resume", h.control((*Engine).Resume))
	r.Post("/workflows/{id}/retry", h.control((*Engine).Retry))
	r.Get("/approvals", h.approvals)
	r.Post("/approvals/{id}/decision", h.decide)
}

func uid(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func idParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	}
	return id, ok
}

func (h *HTTP) registry(w http.ResponseWriter, r *http.Request) {
	agents, tools := Registry()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"agents": agents, "tools": tools})
}

func (h *HTTP) templates(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": Templates(), "plan_version": PlanVersion})
}

func (h *HTTP) interpret(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Objective string `json:"objective"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if ok, _, err := h.limiter.Allow(r.Context(), "workflows:interpret:"+uid(r).String(), 20, time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "Too many interpretations recently. Please wait."))
		return
	}
	out, err := h.e.Interpret(r.Context(), in.Objective)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *HTTP) preview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Template string          `json:"template"`
		Params   json.RawMessage `json:"params"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, err := Propose(in.Template, in.Params)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.e.List(r.Context(), uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *HTTP) create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user := uid(r)
	if ok, _, err := h.limiter.Allow(r.Context(), "workflows:create:"+user.String(), 10, time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "Too many workflows started recently. Please wait."))
		return
	}
	id, err := h.e.Create(r.Context(), user, in, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.e.Get(r.Context(), user, id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	d, err := h.e.Get(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *HTTP) control(fn func(*Engine, context.Context, uuid.UUID, uuid.UUID, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := idParam(w, r)
		if !ok {
			return
		}
		if err := fn(h.e, r.Context(), uid(r), id, h.authH.ClientIP(r)); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		d, err := h.e.Get(r.Context(), uid(r), id)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, d)
	}
}

func (h *HTTP) approvals(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "PENDING", "APPROVED", "REJECTED", "EXPIRED", "CANCELLED":
	default:
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"status": "Unknown status."}))
		return
	}
	out, err := h.e.Approvals(r.Context(), uid(r), status)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *HTTP) decide(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in DecisionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.e.Decide(r.Context(), uid(r), id, in, h.authH.ClientIP(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}
