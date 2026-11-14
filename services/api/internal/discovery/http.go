package discovery

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

type HTTP struct {
	svc     *Service
	queue   *jobs.Client
	limiter ratelimit.Limiter
}

func NewHTTP(svc *Service, queue *jobs.Client, limiter ratelimit.Limiter) *HTTP {
	return &HTTP{svc: svc, queue: queue, limiter: limiter}
}

// Routes are mounted behind RequireAuth.
func (h *HTTP) Routes(r chi.Router) {
	r.Get("/job-sources", h.listSources)
	r.Get("/discovery/runs", h.listRuns)
	r.Post("/discovery/runs", h.trigger)
}

// AdminRoutes are mounted behind RequireAuth + RequireAdmin.
func (h *HTTP) AdminRoutes(r chi.Router) {
	r.Get("/admin/job-sources", h.listSources)
	r.Post("/admin/job-sources", h.createSource)
	r.Patch("/admin/job-sources/{id}", h.updateSource)
}

type sourceView struct {
	sources.Source
	Health              string `json:"health"`
	AttributionRequired bool   `json:"attribution_required"`
}

func view(s sources.Source) sourceView {
	return sourceView{Source: s, Health: s.Health(), AttributionRequired: s.Kind.AttributionRequired()}
}

func (h *HTTP) listSources(w http.ResponseWriter, r *http.Request) {
	list, err := ListSources(r.Context(), h.svc.pool, false)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]sourceView, len(list))
	for i, s := range list {
		out[i] = view(s)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *HTTP) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	runs, err := RecentRuns(r.Context(), h.svc.pool, limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": runs})
}

// trigger queues a manual discovery cycle. Sources are shared, so repeated
// presses are collapsed by the queue's uniqueness window and each source's
// minimum polling interval still applies.
func (h *HTTP) trigger(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	if ok, _, err := h.limiter.Allow(r.Context(), "discovery:user:"+p.UserID.String(), 6, time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "Discovery was triggered recently. Please wait before running it again."))
		return
	}
	res, err := h.queue.Insert(r.Context(), DiscoverAllArgs{Trigger: "MANUAL"}, nil)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	_ = audit.Record(r.Context(), h.svc.pool, audit.Event{UserID: &p.UserID, Action: "discovery.trigger", Outcome: audit.Success,
		Metadata: map[string]any{"deduplicated": res.UniqueSkippedAsDuplicate}})
	msg := "Discovery cycle queued. Sources polled within their minimum interval are skipped."
	if res.UniqueSkippedAsDuplicate {
		msg = "A discovery cycle is already queued or ran in the last few minutes."
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"queued": !res.UniqueSkippedAsDuplicate, "message": msg})
}

type createSourceInput struct {
	Kind               sources.Kind `json:"kind"`
	Identifier         string       `json:"identifier"`
	DisplayName        string       `json:"display_name"`
	MinIntervalMinutes int          `json:"min_interval_minutes"`
}

// createSource validates the board with a live test fetch before saving.
func (h *HTTP) createSource(w http.ResponseWriter, r *http.Request) {
	var in createSourceInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in.Identifier = strings.TrimSpace(in.Identifier)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	v := map[string]string{}
	if !in.Kind.Valid() {
		v["kind"] = "Choose GREENHOUSE, LEVER, ASHBY or REMOTEOK."
	}
	if !sources.ValidIdentifier(in.Identifier) {
		v["identifier"] = "Use the board token from the job board URL (letters, digits, . _ -)."
	}
	if in.DisplayName == "" || len(in.DisplayName) > 120 {
		v["display_name"] = "Required (max 120 characters)."
	}
	if in.MinIntervalMinutes == 0 {
		in.MinIntervalMinutes = 60
	}
	if in.MinIntervalMinutes < 15 || in.MinIntervalMinutes > 7*24*60 {
		v["min_interval_minutes"] = "Between 15 minutes and 7 days."
	}
	if len(v) > 0 {
		httpx.WriteError(w, r, httpx.Validation(v))
		return
	}
	adapter := h.svc.adapters[in.Kind]
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	postings, err := adapter.Fetch(ctx, in.Identifier)
	if err != nil {
		httpx.WriteError(w, r, &httpx.Error{Code: httpx.CodeValidationFailed, Message: "The board could not be fetched. Check the identifier.",
			Details: map[string]string{"identifier": "Test fetch failed."}, Cause: err})
		return
	}
	src := sources.Source{ID: ids.New(), Kind: in.Kind, Identifier: in.Identifier, DisplayName: in.DisplayName,
		Enabled: true, MinIntervalMinutes: in.MinIntervalMinutes}
	p := auth.MustPrincipal(r.Context())
	err = db.InTx(r.Context(), h.svc.pool, func(tx pgx.Tx) error {
		if err := InsertSource(r.Context(), tx, src); err != nil {
			if db.IsUniqueViolation(err, "") {
				return httpx.NewError(httpx.CodeConflict, "This source already exists.")
			}
			return err
		}
		return audit.Record(r.Context(), tx, audit.Event{UserID: &p.UserID, Action: "admin.job_source.create", SubjectType: "job_source",
			SubjectID: &src.ID, Outcome: audit.Success, Metadata: map[string]any{"kind": in.Kind, "identifier": in.Identifier, "test_postings": len(postings)}})
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"source": view(src), "test_postings": len(postings)})
}

func (h *HTTP) updateSource(w http.ResponseWriter, r *http.Request) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if in.Enabled == nil {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"enabled": "Required."}))
		return
	}
	p := auth.MustPrincipal(r.Context())
	err := db.InTx(r.Context(), h.svc.pool, func(tx pgx.Tx) error {
		if err := SetEnabled(r.Context(), tx, id, *in.Enabled); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.Event{UserID: &p.UserID, Action: "admin.job_source.update", SubjectType: "job_source",
			SubjectID: &id, Outcome: audit.Success, Metadata: map[string]any{"enabled": *in.Enabled}})
	})
	if errors.Is(err, ErrSourceNotFound) {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
		return
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	src, err := GetSource(r.Context(), h.svc.pool, id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view(src))
}
