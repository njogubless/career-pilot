package privacy

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/logging"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
)

type HTTP struct {
	svc     *Service
	auth    *auth.Service
	authH   *auth.HTTP
	limiter ratelimit.Limiter
}

func NewHTTP(svc *Service, authSvc *auth.Service, authH *auth.HTTP, limiter ratelimit.Limiter) *HTTP {
	return &HTTP{svc: svc, auth: authSvc, authH: authH, limiter: limiter}
}

// Routes are mounted behind RequireAuth (CSRF enforced for POST).
func (h *HTTP) Routes(r chi.Router) {
	r.Post("/users/me/export", h.export)
	r.Post("/users/me/delete", h.delete)
}

func (h *HTTP) export(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	var in struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.auth.Reauthenticate(r.Context(), p.UserID, in.Password); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if ok, _, err := h.limiter.Allow(r.Context(), "privacy:export:"+p.UserID.String(), 5, time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "You can export your data 5 times an hour. Try again later."))
		return
	}
	// Buffer so a failure mid-way returns an error envelope, not a truncated zip.
	var buf bytes.Buffer
	if err := h.svc.Export(r.Context(), p.UserID, &buf, h.authH.ClientIP(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="careerpilot-export-%s.zip"`, time.Now().UTC().Format("2006-01-02")))
	w.Header().Set("Content-Length", fmt.Sprint(buf.Len()))
	_, _ = buf.WriteTo(w)
}

func (h *HTTP) delete(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	var in struct {
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if in.Confirm != "DELETE" {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"confirm": `Type DELETE to confirm.`}))
		return
	}
	if err := h.auth.Reauthenticate(r.Context(), p.UserID, in.Password); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.svc.Delete(r.Context(), p.UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	logging.FromContext(r.Context()).Info("account deleted", "audit_pseudonymised", res.AuditEventsPseudonymised,
		"objects_deleted", res.ObjectsDeleted, "objects_left_for_sweep", res.ObjectsLeftForSweep, "gmail_revoked", res.GmailRevoked)
	h.authH.ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}
