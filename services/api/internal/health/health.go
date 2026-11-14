// Package health implements liveness and readiness endpoints.
package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

// Checker verifies one dependency. Required checkers gate readiness;
// optional ones are reported but do not fail it.
type Checker struct {
	Name     string
	Required bool
	Check    func(ctx context.Context) error
}

// ErrNotConfigured marks an optional dependency that is intentionally absent.
var ErrNotConfigured = errors.New("not configured")

type Handler struct {
	checkers []Checker
	timeout  time.Duration
	version  string
}

func NewHandler(version string, timeout time.Duration, checkers ...Checker) *Handler {
	return &Handler{checkers: checkers, timeout: timeout, version: version}
}

type checkResult struct {
	Status    string `json:"status"` // ok | fail | not_configured
	Required  bool   `json:"required"`
	Error     string `json:"error,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}

type readiness struct {
	Status  string                 `json:"status"` // ready | not_ready
	Version string                 `json:"version"`
	Checks  map[string]checkResult `json:"checks"`
}

// Live reports that the process is running. It never touches dependencies.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": h.version})
}

// Ready runs all checks concurrently with a shared timeout.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	res := readiness{Status: "ready", Version: h.version, Checks: make(map[string]checkResult, len(h.checkers))}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range h.checkers {
		wg.Add(1)
		go func(c Checker) {
			defer wg.Done()
			start := time.Now()
			err := safeCheck(ctx, c)
			cr := checkResult{Status: "ok", Required: c.Required, LatencyMS: time.Since(start).Milliseconds()}
			switch {
			case errors.Is(err, ErrNotConfigured):
				cr.Status = "not_configured"
			case err != nil:
				cr.Status = "fail"
				// Dependency errors can include hostnames; acceptable on an
				// internal readiness probe, but keep them short.
				cr.Error = truncate(err.Error(), 200)
			}
			mu.Lock()
			res.Checks[c.Name] = cr
			if c.Required && cr.Status != "ok" {
				res.Status = "not_ready"
			}
			mu.Unlock()
		}(c)
	}
	wg.Wait()

	status := http.StatusOK
	if res.Status != "ready" {
		status = http.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, status, res)
}

func safeCheck(ctx context.Context, c Checker) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("check panicked: %v", v)
		}
	}()
	return c.Check(ctx)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
