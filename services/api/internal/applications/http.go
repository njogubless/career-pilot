package applications

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type HTTP struct {
	svc   *Service
	authH *auth.HTTP
}

func NewHTTP(svc *Service, authH *auth.HTTP) *HTTP { return &HTTP{svc: svc, authH: authH} }

// Routes are mounted behind RequireAuth.
func (h *HTTP) Routes(r chi.Router) {
	r.Get("/applications", h.list)
	r.Post("/applications", h.create)
	r.Get("/applications/stats", h.stats)
	r.Get("/applications/activity", h.activity)
	r.Get("/jobs/{id}/application", h.byJob)
	r.Get("/applications/{id}", h.get)
	r.Delete("/applications/{id}", h.delete)
	r.Put("/applications/{id}/notes", h.notes)
	r.Post("/applications/{id}/transitions", h.transition)
	r.Post("/applications/{id}/approval", h.approve)
	r.Delete("/applications/{id}/approval", h.revoke)
	r.Post("/applications/{id}/submissions", h.startSubmission)
	r.Post("/applications/{id}/submissions/{aid}/confirm", h.confirm)
	r.Post("/applications/{id}/submissions/{aid}/fail", h.fail)
	r.Post("/applications/{id}/submissions/{aid}/cancel", h.cancel)
}

func uid(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func param(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, ok := ids.Parse(chi.URLParam(r, name))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	}
	return id, ok
}

// respond returns the fresh application detail after a state change.
func (h *HTTP) respond(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int) {
	d, err := h.svc.Get(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, d)
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := ListFilter{Status: q.Get("status"), Query: q.Get("q")}
	if f.Status != "" && !IsValid(f.Status) {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"status": "Unknown status."}))
		return
	}
	if len(f.Query) > 200 {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"q": "At most 200 characters."}))
		return
	}
	items, err := h.svc.List(r.Context(), uid(r), f)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "statuses": AllStatuses})
}

func (h *HTTP) create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.svc.Create(r.Context(), uid(r), in, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.respond(w, r, id, http.StatusCreated)
}

func (h *HTTP) stats(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Stats(r.Context(), uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *HTTP) activity(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			httpx.WriteError(w, r, httpx.Validation(map[string]string{"limit": "Between 1 and 100."}))
			return
		}
		limit = n
	}
	items, err := h.svc.Activity(r.Context(), uid(r), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *HTTP) byJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := param(w, r, "id")
	if !ok {
		return
	}
	sm, err := h.svc.ByJob(r.Context(), uid(r), jobID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": sm})
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	if id, ok := param(w, r, "id"); ok {
		h.respond(w, r, id, http.StatusOK)
	}
}

func (h *HTTP) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), uid(r), id, h.authH.ClientIP(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) notes(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Notes string `json:"notes"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.svc.UpdateNotes(r.Context(), uid(r), id, in.Notes); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.respond(w, r, id, http.StatusOK)
}

// action decodes a body of type T, runs fn and returns the fresh detail.
func action[T any](h *HTTP, fn func(r *http.Request, id uuid.UUID, in T) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := param(w, r, "id")
		if !ok {
			return
		}
		var in T
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(r, &in); err != nil {
				httpx.WriteError(w, r, err)
				return
			}
		}
		if err := fn(r, id, in); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		h.respond(w, r, id, http.StatusOK)
	}
}

func (h *HTTP) transition(w http.ResponseWriter, r *http.Request) {
	action(h, func(r *http.Request, id uuid.UUID, in TransitionInput) error {
		return h.svc.Transition(r.Context(), uid(r), id, in, h.authH.ClientIP(r))
	})(w, r)
}

func (h *HTTP) approve(w http.ResponseWriter, r *http.Request) {
	action(h, func(r *http.Request, id uuid.UUID, in ApproveInput) error {
		return h.svc.Approve(r.Context(), uid(r), id, in, h.authH.ClientIP(r))
	})(w, r)
}

func (h *HTTP) revoke(w http.ResponseWriter, r *http.Request) {
	action(h, func(r *http.Request, id uuid.UUID, _ struct{}) error {
		return h.svc.RevokeApproval(r.Context(), uid(r), id, h.authH.ClientIP(r))
	})(w, r)
}

// startSubmission requires an Idempotency-Key header: a retried request
// returns the same attempt instead of opening a second one.
func (h *HTTP) startSubmission(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	att, created, err := h.svc.StartSubmission(r.Context(), uid(r), id, r.Header.Get("Idempotency-Key"), audit.ActorUser, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"attempt": att, "replayed": !created})
}

func attemptAction[T any](h *HTTP, fn func(r *http.Request, id, aid uuid.UUID, in T) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		aid, ok := param(w, r, "aid")
		if !ok {
			return
		}
		action(h, func(r *http.Request, id uuid.UUID, in T) error { return fn(r, id, aid, in) })(w, r)
	}
}

func (h *HTTP) confirm(w http.ResponseWriter, r *http.Request) {
	attemptAction(h, func(r *http.Request, id, aid uuid.UUID, in EvidenceInput) error {
		return h.svc.ConfirmSubmission(r.Context(), uid(r), id, aid, in, h.authH.ClientIP(r))
	})(w, r)
}

func (h *HTTP) fail(w http.ResponseWriter, r *http.Request) {
	attemptAction(h, func(r *http.Request, id, aid uuid.UUID, in FailureInput) error {
		return h.svc.FailSubmission(r.Context(), uid(r), id, aid, in, h.authH.ClientIP(r))
	})(w, r)
}

func (h *HTTP) cancel(w http.ResponseWriter, r *http.Request) {
	attemptAction(h, func(r *http.Request, id, aid uuid.UUID, _ struct{}) error {
		return h.svc.CancelSubmission(r.Context(), uid(r), id, aid, h.authH.ClientIP(r))
	})(w, r)
}
