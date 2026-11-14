package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Fetcher is the only HTTP client adapters use. It enforces a host allowlist
// (also on redirects), per-host rate limits, bounded responses, and retries
// with backoff on 429/5xx, honouring Retry-After.
type Fetcher struct {
	client      *http.Client
	userAgent   string
	allowed     map[string]bool
	maxBytes    int64
	maxAttempts int
	baseBackoff time.Duration
	perSecond   float64

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// DefaultHosts are the only hosts discovery may contact.
var DefaultHosts = []string{"boards-api.greenhouse.io", "api.lever.co", "api.ashbyhq.com", "remoteok.com"}

type FetcherOptions struct {
	UserAgent   string
	Hosts       []string
	Timeout     time.Duration
	MaxBytes    int64
	MaxAttempts int
	BaseBackoff time.Duration
	PerSecond   float64
	Transport   http.RoundTripper // tests
}

func NewFetcher(o FetcherOptions) *Fetcher {
	if o.Timeout == 0 {
		o.Timeout = 60 * time.Second
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 64 << 20
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	if o.BaseBackoff == 0 {
		o.BaseBackoff = time.Second
	}
	if o.PerSecond == 0 {
		o.PerSecond = 1
	}
	if o.Hosts == nil {
		o.Hosts = DefaultHosts
	}
	f := &Fetcher{
		userAgent: o.UserAgent, allowed: map[string]bool{}, maxBytes: o.MaxBytes, maxAttempts: o.MaxAttempts,
		baseBackoff: o.BaseBackoff, perSecond: o.PerSecond, limiters: map[string]*rate.Limiter{},
	}
	for _, h := range o.Hosts {
		f.allowed[h] = true
	}
	f.client = &http.Client{
		Timeout:   o.Timeout,
		Transport: o.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if !f.allowed[req.URL.Hostname()] || req.URL.Scheme != "https" && o.Transport == nil {
				return fmt.Errorf("redirect to disallowed host %q", req.URL.Hostname())
			}
			return nil
		},
	}
	return f
}

// SourceError classifies fetch failures. Permanent errors (e.g. a board that
// no longer exists) should not be retried by the job queue.
type SourceError struct {
	Status    int
	Permanent bool
	Msg       string
}

func (e *SourceError) Error() string {
	return fmt.Sprintf("source fetch failed (%d): %s", e.Status, e.Msg)
}

func (f *Fetcher) limiter(host string) *rate.Limiter {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.limiters[host]
	if !ok {
		l = rate.NewLimiter(rate.Limit(f.perSecond), 1)
		f.limiters[host] = l
	}
	return l
}

// GetJSON fetches rawURL and decodes JSON into dst.
func (f *Fetcher) GetJSON(ctx context.Context, rawURL string, dst any) error {
	u, err := url.Parse(rawURL)
	if err != nil || !f.allowed[u.Hostname()] {
		return &SourceError{Permanent: true, Msg: "host not in allowlist"}
	}
	var last error
	for attempt := 1; attempt <= f.maxAttempts; attempt++ {
		if err := f.limiter(u.Hostname()).Wait(ctx); err != nil {
			return err
		}
		retryAfter, err := f.once(ctx, u.String(), dst)
		if err == nil {
			return nil
		}
		last = err
		var se *SourceError
		if errors.As(err, &se) && se.Permanent {
			return err
		}
		if attempt == f.maxAttempts {
			break
		}
		wait := retryAfter
		if wait == 0 {
			wait = f.baseBackoff * time.Duration(1<<(attempt-1))
			wait += time.Duration(rand.Int64N(int64(wait/2) + 1)) // jitter
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return last
}

func (f *Fetcher) once(ctx context.Context, rawURL string, dst any) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, &SourceError{Permanent: true, Msg: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", f.userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, &SourceError{Msg: err.Error()}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return retryAfter(resp.Header.Get("Retry-After")), &SourceError{Status: resp.StatusCode, Msg: http.StatusText(resp.StatusCode)}
	case resp.StatusCode == http.StatusNotFound:
		return 0, &SourceError{Status: 404, Permanent: true, Msg: "board not found"}
	default:
		return 0, &SourceError{Status: resp.StatusCode, Permanent: true, Msg: http.StatusText(resp.StatusCode)}
	}

	body := io.LimitReader(resp.Body, f.maxBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, &SourceError{Msg: "read body: " + err.Error()}
	}
	if int64(len(data)) > f.maxBytes {
		return 0, &SourceError{Permanent: true, Msg: "response exceeds size limit"}
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return 0, &SourceError{Permanent: true, Msg: "invalid JSON: " + err.Error()}
	}
	return 0, nil
}

func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if s, err := strconv.Atoi(h); err == nil && s >= 0 {
		return min(time.Duration(s)*time.Second, 60*time.Second)
	}
	if t, err := http.ParseTime(h); err == nil {
		return min(max(time.Until(t), 0), 60*time.Second)
	}
	return 0
}
