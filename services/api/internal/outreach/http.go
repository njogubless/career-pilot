package outreach

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
)

type HTTP struct {
	svc     *Service
	authH   *auth.HTTP
	limiter ratelimit.Limiter
}

func NewHTTP(svc *Service, authH *auth.HTTP, limiter ratelimit.Limiter) *HTTP {
	return &HTTP{svc: svc, authH: authH, limiter: limiter}
}

// Routes are mounted behind RequireAuth.
func (h *HTTP) Routes(r chi.Router) {
	r.Get("/contacts", h.listContacts)
	r.Post("/contacts", h.createContact)
	r.Get("/contacts/{id}", h.getContact)
	r.Put("/contacts/{id}", h.updateContact)
	r.Delete("/contacts/{id}", h.deleteContact)
	r.Post("/contacts/{id}/verify", h.verifyContact)
	r.Post("/jobs/{id}/contacts/discover", h.discover)

	r.Get("/outreach/settings", h.getSettings)
	r.Put("/outreach/settings", h.putSettings)
	r.Post("/outreach/poll", h.poll)
	r.Get("/outreach/messages", h.listMessages)
	r.Post("/outreach/messages", h.createDraft)
	r.Get("/outreach/messages/{id}", h.getMessage)
	r.Put("/outreach/messages/{id}", h.edit)
	r.Post("/outreach/messages/{id}/approve", h.approve)
	r.Post("/outreach/messages/{id}/send", h.send)
	r.Post("/outreach/messages/{id}/cancel", h.cancel)
	r.Post("/outreach/messages/{id}/mark-not-sent", h.markNotSent)
	r.Put("/outreach/messages/{id}/application", h.linkApplication)
}

func uid(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func param(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	}
	return id, ok
}

func optUUID(w http.ResponseWriter, r *http.Request, name string) (*uuid.UUID, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, true
	}
	id, ok := ids.Parse(v)
	if !ok {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{name: "Invalid id."}))
		return nil, false
	}
	return &id, true
}

func reply[T any](w http.ResponseWriter, r *http.Request, v T, err error, status int) {
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, v)
}

func (h *HTTP) listContacts(w http.ResponseWriter, r *http.Request) {
	job, ok := optUUID(w, r, "job_id")
	if !ok {
		return
	}
	cs, err := h.svc.ListContacts(r.Context(), uid(r), job)
	reply(w, r, map[string]any{"data": cs}, err, http.StatusOK)
}

func (h *HTTP) createContact(w http.ResponseWriter, r *http.Request) {
	var in ContactInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.svc.CreateContact(r.Context(), uid(r), in, h.authH.ClientIP(r))
	reply(w, r, c, err, http.StatusCreated)
}

func (h *HTTP) getContact(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		c, err := h.svc.GetContact(r.Context(), uid(r), id)
		reply(w, r, c, err, http.StatusOK)
	}
}

func (h *HTTP) updateContact(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r)
	if !ok {
		return
	}
	var in ContactInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.svc.UpdateContact(r.Context(), uid(r), id, in, h.authH.ClientIP(r))
	reply(w, r, c, err, http.StatusOK)
}

func (h *HTTP) deleteContact(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		if err := h.svc.DeleteContact(r.Context(), uid(r), id, h.authH.ClientIP(r)); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *HTTP) verifyContact(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		c, err := h.svc.VerifyContact(r.Context(), uid(r), id)
		reply(w, r, c, err, http.StatusOK)
	}
}

func (h *HTTP) discover(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		found, err := h.svc.DiscoverFromJob(r.Context(), uid(r), id, h.authH.ClientIP(r))
		reply(w, r, map[string]any{"data": found}, err, http.StatusOK)
	}
}

func (h *HTTP) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.Settings(r.Context(), uid(r))
	reply(w, r, s, err, http.StatusOK)
}

func (h *HTTP) putSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s, err := h.svc.UpdateSettings(r.Context(), uid(r), in)
	reply(w, r, s, err, http.StatusOK)
}

// poll checks for replies now (also runs every 15 minutes in the worker).
func (h *HTTP) poll(w http.ResponseWriter, r *http.Request) {
	user := uid(r)
	if ok, _, err := h.limiter.Allow(r.Context(), "outreach:poll:"+user.String(), 1, time.Minute); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "Replies were checked less than a minute ago."))
		return
	}
	st, err := h.svc.PollUser(r.Context(), user)
	reply(w, r, st, err, http.StatusOK)
}

func (h *HTTP) listMessages(w http.ResponseWriter, r *http.Request) {
	f := MessageFilter{Status: r.URL.Query().Get("status")}
	var ok bool
	if f.ContactID, ok = optUUID(w, r, "contact_id"); !ok {
		return
	}
	if f.ApplicationID, ok = optUUID(w, r, "application_id"); !ok {
		return
	}
	if f.JobID, ok = optUUID(w, r, "job_id"); !ok {
		return
	}
	ms, err := h.svc.ListMessages(r.Context(), uid(r), f)
	reply(w, r, map[string]any{"data": ms}, err, http.StatusOK)
}

func (h *HTTP) createDraft(w http.ResponseWriter, r *http.Request) {
	var in DraftInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user := uid(r)
	if in.Mode == "AI" {
		if ok, _, err := h.limiter.Allow(r.Context(), "outreach:draft:"+user.String(), 30, time.Hour); err == nil && !ok {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "You have drafted many emails recently. Please wait a while."))
			return
		}
	}
	m, err := h.svc.CreateDraft(r.Context(), user, in, h.authH.ClientIP(r))
	reply(w, r, m, err, http.StatusCreated)
}

func (h *HTTP) getMessage(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		m, err := h.svc.GetMessage(r.Context(), uid(r), id)
		reply(w, r, m, err, http.StatusOK)
	}
}

func (h *HTTP) edit(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r)
	if !ok {
		return
	}
	var in EditInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.svc.Edit(r.Context(), uid(r), id, in, h.authH.ClientIP(r))
	reply(w, r, m, err, http.StatusOK)
}

func (h *HTTP) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r)
	if !ok {
		return
	}
	var in ApproveInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.svc.Approve(r.Context(), uid(r), id, in, h.authH.ClientIP(r))
	reply(w, r, m, err, http.StatusOK)
}

// send requires an Idempotency-Key: a retried request returns the same
// message instead of sending a second email.
func (h *HTTP) send(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		m, err := h.svc.Send(r.Context(), uid(r), id, r.Header.Get("Idempotency-Key"), audit.ActorUser, h.authH.ClientIP(r))
		reply(w, r, m, err, http.StatusOK)
	}
}

func (h *HTTP) cancel(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		m, err := h.svc.Cancel(r.Context(), uid(r), id, h.authH.ClientIP(r))
		reply(w, r, m, err, http.StatusOK)
	}
}

func (h *HTTP) markNotSent(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r); ok {
		m, err := h.svc.MarkNotSent(r.Context(), uid(r), id, h.authH.ClientIP(r))
		reply(w, r, m, err, http.StatusOK)
	}
}

func (h *HTTP) linkApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r)
	if !ok {
		return
	}
	var in struct {
		ApplicationID *uuid.UUID `json:"application_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.svc.LinkApplication(r.Context(), uid(r), id, in.ApplicationID)
	reply(w, r, m, err, http.StatusOK)
}
