package preparation

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
)

// Generation spends LLM tokens; cap it per user.
const generationsPerHour = 20

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
	r.Post("/jobs/{id}/application-documents", h.generate)
	r.Get("/application-documents", h.list)
	r.Get("/application-documents/{id}", h.get)
	r.Delete("/application-documents/{id}", h.delete)
	r.Put("/application-documents/{id}/content", h.saveEdit)
	r.Post("/application-documents/{id}/recheck", h.recheck)
	r.Post("/application-documents/{id}/approval", h.approve)
	r.Delete("/application-documents/{id}/approval", h.revoke)
	r.Get("/application-documents/{id}/versions", h.versions)
	r.Get("/application-documents/{id}/versions/{vid}", h.version)
	r.Post("/application-documents/{id}/versions/{vid}/restore", h.restore)
	r.Get("/application-documents/{id}/versions/{vid}/export", h.export)
}

func uid(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func param(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, ok := ids.Parse(chi.URLParam(r, name))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	}
	return id, ok
}

func (h *HTTP) generate(w http.ResponseWriter, r *http.Request) {
	jobID, ok := param(w, r, "id")
	if !ok {
		return
	}
	var in GenerateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user := uid(r)
	if in.Source == SourceProfile {
		doc, err := h.svc.CreateFromProfile(r.Context(), user, jobID, in, h.authH.ClientIP(r))
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"document": doc, "generation": nil})
		return
	}
	if ok, _, err := h.limiter.Allow(r.Context(), "prep:generate:"+user.String(), generationsPerHour, time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "You have generated many drafts recently. Please wait before generating more."))
		return
	}
	doc, gen, err := h.svc.RequestGeneration(r.Context(), user, jobID, in, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"document": doc, "generation": gen})
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	var jobID *uuid.UUID
	if s := r.URL.Query().Get("job_id"); s != "" {
		id, ok := ids.Parse(s)
		if !ok {
			httpx.WriteError(w, r, httpx.Validation(map[string]string{"job_id": "Invalid job id."}))
			return
		}
		jobID = &id
	}
	docs, err := h.svc.ListDocuments(r.Context(), uid(r), jobID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": docs})
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	v, err := h.svc.Get(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
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

type editInput struct {
	BaseVersionID uuid.UUID `json:"base_version_id"`
	Content       Content   `json:"content"`
}

func (h *HTTP) saveEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	var in editInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if in.BaseVersionID == uuid.Nil {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"base_version_id": "Required."}))
		return
	}
	v, err := h.svc.SaveEdit(r.Context(), uid(r), id, in.BaseVersionID, in.Content, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *HTTP) recheck(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	val, err := h.svc.Recheck(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, val)
}

func (h *HTTP) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	var in ApproveInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	doc, val, err := h.svc.Approve(r.Context(), uid(r), id, in, h.authH.ClientIP(r))
	var he *httpx.Error
	if err != nil && val != nil && errors.As(err, &he) && he.Code == httpx.CodeValidationFailed {
		// The standard envelope plus the fresh fact check, so the UI can show
		// exactly which statements block approval.
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{"code": he.Code, "message": he.Message, "details": he.Details,
				"request_id": httpx.RequestIDFrom(r.Context())},
			"validation": val,
		})
		return
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"document": doc, "validation": val})
}

func (h *HTTP) revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	doc, err := h.svc.RevokeApproval(r.Context(), uid(r), id, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, doc)
}

func (h *HTTP) versions(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	vs, err := h.svc.ListVersions(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": vs})
}

func (h *HTTP) version(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	vid, ok := param(w, r, "vid")
	if !ok {
		return
	}
	v, err := h.svc.GetVersion(r.Context(), uid(r), id, vid)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *HTTP) restore(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	vid, ok := param(w, r, "vid")
	if !ok {
		return
	}
	v, err := h.svc.Restore(r.Context(), uid(r), id, vid, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *HTTP) export(w http.ResponseWriter, r *http.Request) {
	id, ok := param(w, r, "id")
	if !ok {
		return
	}
	vid, ok := param(w, r, "vid")
	if !ok {
		return
	}
	data, mime, name, err := h.svc.Export(r.Context(), uid(r), id, vid, r.URL.Query().Get("format"), h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, name, url.PathEscape(name)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
