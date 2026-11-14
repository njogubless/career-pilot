package profiles

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type HTTP struct{ svc *Service }

func NewHTTP(svc *Service) *HTTP { return &HTTP{svc: svc} }

// Routes are mounted behind auth.RequireAuth.
func (h *HTTP) Routes(r chi.Router) {
	r.Get("/profiles/me", h.get)
	r.Put("/profiles/me/basics", h.putBasics)
	r.Get("/profiles/me/preferences", h.getPreferences)
	r.Put("/profiles/me/preferences", h.putPreferences)
	r.Get("/profiles/me/completeness", h.completeness)

	crud(r, "experiences", h.svc.CreateExperience, h.svc.UpdateExperience, func(e *Experience, id uuid.UUID) { e.ID = id })
	crud(r, "education", h.svc.CreateEducation, h.svc.UpdateEducation, func(e *Education, id uuid.UUID) { e.ID = id })
	crud(r, "skills", h.svc.CreateSkill, h.svc.UpdateSkill, func(s *Skill, id uuid.UUID) { s.ID = id })
	crud(r, "certifications", h.svc.CreateCertification, h.svc.UpdateCertification, func(c *Certification, id uuid.UUID) { c.ID = id })
	crud(r, "projects", h.svc.CreateProject, h.svc.UpdateProject, func(p *Project, id uuid.UUID) { p.ID = id })

	r.Delete("/profiles/me/{kind}/{id}", h.deleteFact)
	r.Put("/profiles/me/{kind}/{id}/verification", h.setVerification)
}

func userID(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	}
	return id, ok
}

// crud registers POST /profiles/me/{kind} and PUT /profiles/me/{kind}/{id}.
func crud[T any](r chi.Router, kind string,
	create func(ctx context.Context, userID uuid.UUID, v T) (T, error),
	update func(ctx context.Context, userID uuid.UUID, v T) error,
	setID func(*T, uuid.UUID),
) {
	r.Post("/profiles/me/"+kind, func(w http.ResponseWriter, r *http.Request) {
		var in T
		if err := httpx.DecodeJSON(r, &in); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		out, err := create(r.Context(), userID(r), in)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, out)
	})
	r.Put("/profiles/me/"+kind+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in T
		if err := httpx.DecodeJSON(r, &in); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		setID(&in, id)
		if err := update(r.Context(), userID(r), in); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Get(r.Context(), userID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *HTTP) completeness(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Completeness(r.Context(), userID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *HTTP) putBasics(w http.ResponseWriter, r *http.Request) {
	var in Basics
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out, err := h.svc.UpdateBasics(r.Context(), userID(r), in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *HTTP) getPreferences(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Get(r.Context(), userID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p.Preferences)
}

func (h *HTTP) putPreferences(w http.ResponseWriter, r *http.Request) {
	var in Preferences
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out, err := h.svc.UpdatePreferences(r.Context(), userID(r), in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *HTTP) deleteFact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteFact(r.Context(), userID(r), chi.URLParam(r, "kind"), id); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) setVerification(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Verification Verification `json:"verification"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.svc.SetVerification(r.Context(), userID(r), chi.URLParam(r, "kind"), id, in.Verification); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
