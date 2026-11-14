// Package sources implements job-board adapters behind a common contract
// (INTEGRATION_STRATEGY §1). Adapters only read public job-board APIs from a
// fixed host allowlist; they never fetch user-supplied URLs.
package sources

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"
)

type Kind string

const (
	Greenhouse Kind = "GREENHOUSE"
	Lever      Kind = "LEVER"
	Ashby      Kind = "ASHBY"
	RemoteOK   Kind = "REMOTEOK"
)

// Priority decides which copy of a cross-posted job is canonical: the
// employer's own ATS beats an aggregator.
func (k Kind) Priority() int {
	switch k {
	case Greenhouse, Lever, Ashby:
		return 1
	default:
		return 2
	}
}

// AttributionRequired reports whether the source's terms require visible
// attribution and a followed link back (Remote OK API terms).
func (k Kind) AttributionRequired() bool { return k == RemoteOK }

func (k Kind) Valid() bool {
	switch k {
	case Greenhouse, Lever, Ashby, RemoteOK:
		return true
	}
	return false
}

var identifierRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ValidIdentifier guards path construction against injection/SSRF.
func ValidIdentifier(s string) bool { return identifierRe.MatchString(s) }

type Source struct {
	ID                  uuid.UUID  `json:"id"`
	Kind                Kind       `json:"kind"`
	Identifier          string     `json:"identifier"`
	DisplayName         string     `json:"display_name"`
	Enabled             bool       `json:"enabled"`
	MinIntervalMinutes  int        `json:"min_interval_minutes"`
	LastRunAt           *time.Time `json:"last_run_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastError           *string    `json:"last_error"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	ActiveJobs          int        `json:"active_jobs"`
}

// Health is derived, not stored, so it can never drift from the facts.
func (s Source) Health() string {
	switch {
	case !s.Enabled:
		return "DISABLED"
	case s.ConsecutiveFailures >= CircuitThreshold:
		return "DEGRADED"
	case s.LastSuccessAt == nil:
		return "PENDING"
	default:
		return "HEALTHY"
	}
}

// CircuitThreshold is the number of consecutive failures after which a
// source is backed off (exponentially, capped at a day).
const CircuitThreshold = 5

// Remote scope of a posting.
const (
	ScopeWorldwide = "WORLDWIDE"
	ScopeCountries = "COUNTRIES"
	ScopeRegion    = "REGION"
	ScopeUnknown   = "UNKNOWN"
	ScopeNotRemote = "NOT_REMOTE"
)

// Posting is a normalised job from one source. Fields that the source does
// not state explicitly are left empty / UNKNOWN; adapters never guess.
type Posting struct {
	ExternalID        string
	CanonicalURL      string
	ApplyURL          string
	Title             string
	Company           string
	Department        string
	LocationRaw       string
	LocationCountries []string // ISO-3166 alpha-2 where the role is located
	RemoteStatus      string   // REMOTE | HYBRID | ONSITE | UNKNOWN
	RemoteScope       string
	RemoteCountries   []string
	RemoteRegion      string
	EmploymentType    string // FULL_TIME | PART_TIME | CONTRACT | INTERNSHIP | TEMPORARY | UNKNOWN
	Salary            *Salary
	SalaryText        string // free-text salary as published (not parsed)
	DescriptionHTML   string // sanitised
	DescriptionText   string
	Tags              []string
	PostedAt          *time.Time
}

type Salary struct {
	Min      *float64
	Max      *float64
	Currency string // ISO-4217
	Period   string // YEAR | MONTH | HOUR
}

// Adapter fetches a complete snapshot of a board. A successful snapshot is
// authoritative: postings missing from it are treated as expired.
type Adapter interface {
	Kind() Kind
	Fetch(ctx context.Context, identifier string) ([]Posting, error)
}
