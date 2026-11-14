package auth

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

const CookieName = "cp_session"

// HTTP wires the auth service to HTTP: middleware and handlers.
type HTTP struct {
	svc          *Service
	cookieSecure bool
	trustProxy   bool
}

func NewHTTP(svc *Service, cookieSecure, trustProxy bool) *HTTP {
	return &HTTP{svc: svc, cookieSecure: cookieSecure, trustProxy: trustProxy}
}

// ClientIP returns the client address. Behind a trusted proxy (nginx with
// $proxy_add_x_forwarded_for) the last X-Forwarded-For entry is the address
// the proxy saw; earlier entries are client-controlled and ignored.
func (h *HTTP) ClientIP(r *http.Request) string {
	if h.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *HTTP) meta(r *http.Request) RequestMeta {
	return RequestMeta{IP: h.ClientIP(r), UserAgent: r.UserAgent()}
}

// Meta is exported for other modules that audit requests.
func (h *HTTP) Meta(r *http.Request) RequestMeta { return h.meta(r) }

// RequireAdmin must be mounted after RequireAuth. Non-admins get 404 so the
// admin surface is not discoverable.
func (h *HTTP) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFrom(r.Context()); !ok || !p.IsAdmin() {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoadSession attaches the principal when a valid session cookie is present.
func (h *HTTP) LoadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err == nil && c.Value != "" {
			if p, err := h.svc.Authenticate(r.Context(), c.Value); err == nil {
				r = r.WithContext(withPrincipal(r.Context(), p))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth rejects unauthenticated requests and enforces the CSRF token
// on state-changing methods.
func (h *HTTP) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeUnauthenticated, "Sign in to continue."))
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !h.svc.validCSRF(p.SessionID, r.Header.Get("X-CSRF-Token")) {
				httpx.WriteError(w, r, httpx.NewError(httpx.CodeForbidden, "Missing or invalid CSRF token. Reload the page and try again."))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *HTTP) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: expires,
		MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (h *HTTP) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode})
}

type sessionResponse struct {
	User      User   `json:"user"`
	CSRFToken string `json:"csrf_token"`
}

// PublicRoutes are mounted without RequireAuth.
func (h *HTTP) PublicRoutes(r chi.Router) {
	r.Post("/auth/register", h.register)
	r.Post("/auth/login", h.login)
	r.Get("/auth/session", h.session)
	r.Post("/auth/password/forgot", h.forgot)
	r.Post("/auth/password/reset", h.reset)
}

// PrivateRoutes are mounted behind RequireAuth.
func (h *HTTP) PrivateRoutes(r chi.Router) {
	r.Post("/auth/logout", h.logout)
	r.Post("/auth/logout-all", h.logoutAll)
	r.Post("/auth/password/change", h.changePassword)
	r.Get("/users/me", h.me)
	r.Patch("/users/me", h.updateMe)
}

func (h *HTTP) register(w http.ResponseWriter, r *http.Request) {
	var in RegisterInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s, err := h.svc.Register(r.Context(), in, h.meta(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.setCookie(w, s.Token, s.ExpiresAt)
	httpx.WriteJSON(w, http.StatusCreated, sessionResponse{User: s.User, CSRFToken: s.CSRFToken})
}

func (h *HTTP) login(w http.ResponseWriter, r *http.Request) {
	var in LoginInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s, err := h.svc.Login(r.Context(), in, h.meta(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.setCookie(w, s.Token, s.ExpiresAt)
	httpx.WriteJSON(w, http.StatusOK, sessionResponse{User: s.User, CSRFToken: s.CSRFToken})
}

func (h *HTTP) session(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeUnauthenticated, "Not signed in."))
		return
	}
	u, err := h.svc.User(r.Context(), p.UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sessionResponse{User: u, CSRFToken: h.svc.CSRFToken(p.SessionID)})
}

func (h *HTTP) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Logout(r.Context(), MustPrincipal(r.Context()), h.meta(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) logoutAll(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.LogoutAll(r.Context(), MustPrincipal(r.Context()), h.meta(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) forgot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.svc.ForgotPassword(r.Context(), in.Email, h.meta(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{
		"message": "If an account exists for that email, a reset link has been sent.",
	})
}

func (h *HTTP) reset(w http.ResponseWriter, r *http.Request) {
	var in ResetInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.svc.ResetPassword(r.Context(), in, h.meta(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) changePassword(w http.ResponseWriter, r *http.Request) {
	var in ChangePasswordInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.svc.ChangePassword(r.Context(), MustPrincipal(r.Context()), in, h.meta(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.svc.User(r.Context(), MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u)
}

func (h *HTTP) updateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DisplayName string `json:"display_name"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.svc.UpdateDisplayName(r.Context(), MustPrincipal(r.Context()), in.DisplayName)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u)
}
