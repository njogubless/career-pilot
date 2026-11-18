package workflows

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

// PlanVersion changes whenever a template's task graph changes.
const PlanVersion = "plan-v1"

const (
	TemplateDiscoverRank    = "DISCOVER_AND_RANK"
	TemplateDiscoverPrepare = "DISCOVER_PREPARE_APPROVE"
)

// Money is a minimum salary stated per month.
type Money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Params are the only knobs a plan has. Whether they come from a form or
// from AI interpretation, they are validated here before anything runs.
type Params struct {
	Query                  string  `json:"query"`
	RemoteOnly             bool    `json:"remote_only"`
	Country                string  `json:"country"`
	MinMonthlySalary       *Money  `json:"min_monthly_salary"`
	RequireDisclosedSalary bool    `json:"require_disclosed_salary"`
	PostedWithinDays       int     `json:"posted_within_days"`
	Count                  int     `json:"count"`
	MinScore               float64 `json:"min_score"`
	UseAI                  bool    `json:"use_ai"`
}

var (
	countryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
)

// Normalize fills defaults and validates. It is the single gate every plan
// passes, including AI-proposed ones.
func (p *Params) Normalize() error {
	errs := map[string]string{}
	p.Query = strings.Join(strings.Fields(p.Query), " ")
	if utf8.RuneCountInString(p.Query) > 200 {
		errs["query"] = "Use at most 200 characters."
	}
	p.Country = strings.ToUpper(strings.TrimSpace(p.Country))
	if p.Country != "" && !countryRe.MatchString(p.Country) {
		errs["country"] = "Use a two-letter country code."
	}
	if m := p.MinMonthlySalary; m != nil {
		m.Currency = strings.ToUpper(strings.TrimSpace(m.Currency))
		if m.Amount <= 0 || m.Amount > 1e7 {
			errs["min_monthly_salary.amount"] = "Enter a positive monthly amount."
		}
		if !currencyRe.MatchString(m.Currency) {
			errs["min_monthly_salary.currency"] = "Use a three-letter currency code."
		}
	}
	if p.PostedWithinDays == 0 {
		p.PostedWithinDays = 30
	}
	if p.PostedWithinDays < 1 || p.PostedWithinDays > 90 {
		errs["posted_within_days"] = "Between 1 and 90 days."
	}
	if p.Count == 0 {
		p.Count = 5
	}
	if p.Count < 1 || p.Count > 10 {
		errs["count"] = "Between 1 and 10 jobs."
	}
	if p.MinScore == 0 {
		p.MinScore = 50
	}
	if p.MinScore < 0 || p.MinScore > 100 {
		errs["min_score"] = "Between 0 and 100."
	}
	if len(errs) > 0 {
		return httpx.Validation(errs)
	}
	return nil
}

// TaskSpec is one step of a template plan.
type TaskSpec struct {
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Agent     string   `json:"agent"`
	Tool      string   `json:"tool"`
	DependsOn []string `json:"depends_on"` // task types
	RunAlways bool     `json:"run_always"`
	Approval  bool     `json:"approval"`
}

type Template struct {
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Tasks       []TaskSpec `json:"tasks"`
}

var discoverTasks = []TaskSpec{
	{Type: "load_profile", Title: "Review your verified profile", Agent: "PROFILE_INTELLIGENCE", Tool: "analyse_professional_profile"},
	{Type: "search_jobs", Title: "Search discovered jobs", Agent: "DISCOVERY", Tool: "search_jobs", DependsOn: []string{"load_profile"}},
	{Type: "match_jobs", Title: "Score suitability and eligibility", Agent: "MATCHING", Tool: "calculate_job_match", DependsOn: []string{"search_jobs"}},
	{Type: "rank", Title: "Apply hard filters and rank", Agent: "MATCHING", Tool: "rank_opportunities", DependsOn: []string{"match_jobs"}},
	{Type: "shortlist", Title: "Add the best matches to your pipeline", Agent: "EXECUTION", Tool: "update_application_status", DependsOn: []string{"rank"}},
}

var templates = map[string]Template{
	TemplateDiscoverRank: {Name: TemplateDiscoverRank, Title: "Find and shortlist jobs",
		Description: "Searches discovered jobs, scores them against your verified profile, applies your hard filters and shortlists the best.",
		Tasks: append(append([]TaskSpec{}, discoverTasks...),
			TaskSpec{Type: "report", Title: "Write the execution report", Agent: "PERFORMANCE", Tool: "generate_career_report", DependsOn: []string{"shortlist"}, RunAlways: true})},
	TemplateDiscoverPrepare: {Name: TemplateDiscoverPrepare, Title: "Find, prepare and ask me to approve",
		Description: "As above, then prepares a tailored CV for each shortlisted job, runs compliance checks and asks for your approval. " +
			"Approved applications are ready for you to submit on the official sites.",
		Tasks: append(append([]TaskSpec{}, discoverTasks...),
			TaskSpec{Type: "prepare_documents", Title: "Prepare tailored CVs", Agent: "APPLICATION_PREPARATION", Tool: "generate_tailored_cv", DependsOn: []string{"shortlist"}},
			TaskSpec{Type: "compliance_check", Title: "Check facts, eligibility and postings", Agent: "COMPLIANCE", Tool: "check_compliance", DependsOn: []string{"prepare_documents"}},
			TaskSpec{Type: "request_approval", Title: "Ask for your approval", Agent: "EXECUTION", Tool: "request_approval", DependsOn: []string{"compliance_check"}, Approval: true},
			TaskSpec{Type: "apply_approval", Title: "Approve the packages you selected", Agent: "EXECUTION", Tool: "approve_application_package", DependsOn: []string{"request_approval"}},
			TaskSpec{Type: "report", Title: "Write the execution report", Agent: "PERFORMANCE", Tool: "generate_career_report", DependsOn: []string{"apply_approval"}, RunAlways: true})},
}

func Templates() []Template {
	return []Template{templates[TemplateDiscoverRank], templates[TemplateDiscoverPrepare]}
}

// validatePlan checks a template's graph against the registries: every agent
// must hold its task's tool, dependencies must refer to earlier tasks. It
// runs on every plan before it is persisted (and in tests for all templates).
func validatePlan(t Template) error {
	seen := map[string]bool{}
	for _, ts := range t.Tasks {
		if _, ok := tools[ts.Tool]; !ok {
			return fmt.Errorf("task %s uses unknown tool %s", ts.Type, ts.Tool)
		}
		if !Grants(ts.Agent, ts.Tool) {
			return fmt.Errorf("agent %s is not granted tool %s", ts.Agent, ts.Tool)
		}
		for _, d := range ts.DependsOn {
			if !seen[d] {
				return fmt.Errorf("task %s depends on %s, which is not an earlier task", ts.Type, d)
			}
		}
		if seen[ts.Type] {
			return fmt.Errorf("duplicate task %s", ts.Type)
		}
		seen[ts.Type] = true
	}
	return nil
}

// Proposal is a validated plan ready to run (or to show before running).
type Proposal struct {
	Template Template `json:"template"`
	Params   Params   `json:"params"`
}

func Propose(template string, raw json.RawMessage) (Proposal, error) {
	t, ok := templates[template]
	if !ok {
		return Proposal{}, httpx.Validation(map[string]string{"template": "Choose DISCOVER_AND_RANK or DISCOVER_PREPARE_APPROVE."})
	}
	var p Params
	if len(raw) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return Proposal{}, httpx.Validation(map[string]string{"params": "Unknown or invalid parameters."})
		}
	}
	if err := p.Normalize(); err != nil {
		return Proposal{}, err
	}
	if err := validatePlan(t); err != nil {
		return Proposal{}, err
	}
	return Proposal{Template: t, Params: p}, nil
}
