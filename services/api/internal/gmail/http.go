package gmail

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

// HTTP serves /integrations/gmail. svc may be nil (not configured); the
// status endpoint then says so and the others answer 503.
type HTTP struct {
	svc        *Service
	authH      *auth.HTTP
	appBaseURL string
}

func NewHTTP(svc *Service, authH *auth.HTTP, appBaseURL string) *HTTP {
	return &HTTP{svc: svc, authH: authH, appBaseURL: appBaseURL}
}

func (h *HTTP) Routes(r chi.Router) {
	r.Get("/integrations/gmail", h.status)
	r.Post("/integrations/gmail/connect", h.connect)
	r.Get("/integrations/gmail/callback", h.callback)
	r.Delete("/integrations/gmail", h.disconnect)
}

var errNotConfigured = httpx.NewError(httpx.CodeUnavailable,
	"Gmail is not configured on this server. An operator must set GOOGLE_OAUTH_CLIENT_ID, GOOGLE_OAUTH_CLIENT_SECRET and DATA_ENCRYPTION_KEY.")

func (h *HTTP) status(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"configured": false, "account": nil, "scopes_requested": Scopes})
		return
	}
	acc, err := h.svc.Account(r.Context(), auth.MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"configured": true, "account": acc, "scopes_requested": Scopes})
}

func (h *HTTP) connect(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		httpx.WriteError(w, r, errNotConfigured)
		return
	}
	u, err := h.svc.StartConnect(r.Context(), auth.MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"auth_url": u})
}

// callback is the browser redirect from Google. It always redirects back to
// Settings with a result flag; the state parameter is the CSRF protection.
func (h *HTTP) callback(w http.ResponseWriter, r *http.Request) {
	back := func(result, msg string) {
		q := url.Values{"gmail": {result}}
		if msg != "" {
			q.Set("message", msg)
		}
		http.Redirect(w, r, h.appBaseURL+"/settings?"+q.Encode()+"#gmail", http.StatusFound)
	}
	if h.svc == nil {
		back("error", "Gmail is not configured on this server.")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		back("error", "Google did not grant access ("+e+").")
		return
	}
	_, err := h.svc.Callback(r.Context(), auth.MustPrincipal(r.Context()).UserID, q.Get("state"), q.Get("code"), h.authH.ClientIP(r))
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Code == httpx.CodeValidationFailed {
			back("error", he.Message)
			return
		}
		back("error", "The connection failed. Please try again.")
		return
	}
	back("connected", "")
}

func (h *HTTP) disconnect(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		httpx.WriteError(w, r, errNotConfigured)
		return
	}
	if err := h.svc.Disconnect(r.Context(), auth.MustPrincipal(r.Context()).UserID, h.authH.ClientIP(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
