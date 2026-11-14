package listings

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type HTTP struct{ pool *pgxpool.Pool }

func NewHTTP(pool *pgxpool.Pool) *HTTP { return &HTTP{pool: pool} }

// Routes are mounted behind RequireAuth.
func (h *HTTP) Routes(r chi.Router) {
	r.Get("/jobs", h.search)
	r.Get("/jobs/{id}", h.get)
	r.Get("/saved-searches", h.listSaved)
	r.Post("/saved-searches", h.createSaved)
	r.Delete("/saved-searches/{id}", h.deleteSaved)
	r.Get("/dashboard/overview", h.overview)
}

func (h *HTTP) search(w http.ResponseWriter, r *http.Request) {
	f, err := ParseFilters(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	limit := 20
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 50 {
			httpx.WriteError(w, r, httpx.Validation(map[string]string{"limit": "Between 1 and 50."}))
			return
		}
		limit = n
	}
	page, err := Search(r.Context(), h.pool, f, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Job not found."))
		return
	}
	d, err := Get(r.Context(), h.pool, id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Job not found."))
		return
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// ---- Saved searches ----

type SavedSearch struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Filters   Filters   `json:"filters"`
	CreatedAt time.Time `json:"created_at"`
}

const maxSavedSearches = 25

func (h *HTTP) listSaved(w http.ResponseWriter, r *http.Request) {
	uid := auth.MustPrincipal(r.Context()).UserID
	rows, err := h.pool.Query(r.Context(), `SELECT id, name, filters, created_at FROM saved_searches WHERE user_id = $1 ORDER BY created_at DESC`, uid)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SavedSearch, error) {
		var s SavedSearch
		var raw []byte
		if err := row.Scan(&s.ID, &s.Name, &raw, &s.CreatedAt); err != nil {
			return s, err
		}
		return s, json.Unmarshal(raw, &s.Filters)
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *HTTP) createSaved(w http.ResponseWriter, r *http.Request) {
	uid := auth.MustPrincipal(r.Context()).UserID
	var in struct {
		Name  string `json:"name"`
		Query string `json:"query"` // the search query string, e.g. "q=go&remote=REMOTE"
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"name": "Enter a name (max 80 characters)."}))
		return
	}
	vals, err := parseQuery(in.Query)
	if err != nil {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"query": "Invalid search."}))
		return
	}
	f, err := ParseFilters(vals)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, _ := json.Marshal(f)
	s := SavedSearch{ID: ids.New(), Name: in.Name, Filters: f, CreatedAt: time.Now()}
	err = db.InTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM saved_searches WHERE user_id = $1`, uid).Scan(&n); err != nil {
			return err
		}
		if n >= maxSavedSearches {
			return httpx.NewError(httpx.CodeConflict, "You can keep at most 25 saved searches.")
		}
		_, err := tx.Exec(r.Context(), `INSERT INTO saved_searches (id, user_id, name, filters) VALUES ($1, $2, $3, $4)`, s.ID, uid, s.Name, raw)
		if db.IsUniqueViolation(err, "") {
			return &httpx.Error{Code: httpx.CodeConflict, Message: "A saved search with this name exists.", Details: map[string]string{"name": "Already used."}}
		}
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, s)
}

func (h *HTTP) deleteSaved(w http.ResponseWriter, r *http.Request) {
	uid := auth.MustPrincipal(r.Context()).UserID
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
		return
	}
	tag, err := h.pool.Exec(r.Context(), `DELETE FROM saved_searches WHERE id = $1 AND user_id = $2`, id, uid)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Dashboard ----

type Overview struct {
	JobsActive      int        `json:"jobs_active"`
	JobsNew24h      int        `json:"jobs_new_24h"`
	SourcesTotal    int        `json:"sources_total"`
	SourcesHealthy  int        `json:"sources_healthy"`
	LastDiscoveryAt *time.Time `json:"last_discovery_at"`
}

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	var o Overview
	err := h.pool.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM jobs WHERE expired_at IS NULL AND duplicate_of IS NULL),
		(SELECT count(*) FROM jobs WHERE expired_at IS NULL AND duplicate_of IS NULL AND first_seen_at >= now() - interval '24 hours'),
		(SELECT count(*) FROM job_sources WHERE enabled),
		(SELECT count(*) FROM job_sources WHERE enabled AND last_success_at IS NOT NULL AND consecutive_failures < 5),
		(SELECT max(last_success_at) FROM job_sources)`).Scan(&o.JobsActive, &o.JobsNew24h, &o.SourcesTotal, &o.SourcesHealthy, &o.LastDiscoveryAt)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}
