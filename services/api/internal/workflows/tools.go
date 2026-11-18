package workflows

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/applications"
	"github.com/careerpilot/careerpilot/services/api/internal/listings"
	"github.com/careerpilot/careerpilot/services/api/internal/matching"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/preparation"
)

// ---- Inputs ----

type noInput struct{}

func (*noInput) Validate() error { return nil }

type searchInput struct {
	Query                  string `json:"query"`
	RemoteOnly             bool   `json:"remote_only"`
	Country                string `json:"country"`
	MinMonthlySalary       *Money `json:"min_monthly_salary"`
	RequireDisclosedSalary bool   `json:"require_disclosed_salary"`
	PostedWithinDays       int    `json:"posted_within_days"`
	Limit                  int    `json:"limit"`
}

func (in *searchInput) Validate() error {
	if in.Limit < 1 || in.Limit > 200 || in.PostedWithinDays < 1 || in.PostedWithinDays > 90 || len(in.Query) > 200 {
		return errors.New("search parameters out of bounds")
	}
	return nil
}

type rankInput struct {
	Count    int     `json:"count"`
	MinScore float64 `json:"min_score"`
}

func (in *rankInput) Validate() error {
	if in.Count < 1 || in.Count > 10 || in.MinScore < 0 || in.MinScore > 100 {
		return errors.New("rank parameters out of bounds")
	}
	return nil
}

type prepareInput struct {
	UseAI bool `json:"use_ai"`
}

func (*prepareInput) Validate() error { return nil }

// inputFor derives a task's static input from the validated params.
func inputFor(taskType string, p Params) any {
	switch taskType {
	case "search_jobs":
		return searchInput{Query: p.Query, RemoteOnly: p.RemoteOnly, MinMonthlySalary: p.MinMonthlySalary,
			RequireDisclosedSalary: p.RequireDisclosedSalary, PostedWithinDays: p.PostedWithinDays, Limit: 150}
	case "rank":
		return rankInput{Count: p.Count, MinScore: p.MinScore}
	case "prepare_documents":
		return prepareInput{UseAI: p.UseAI}
	}
	return struct{}{}
}

// dep decodes a completed dependency's result.
func dep[T any](env *Env, taskType string) (T, error) {
	var out T
	raw, ok := env.Deps[taskType]
	if !ok {
		return out, permanent("MISSING_DEPENDENCY", "A required earlier step has no result.", fmt.Errorf("no result for %s", taskType))
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, permanent("BAD_DEPENDENCY", "An earlier step's result could not be read.", err)
	}
	return out, nil
}

// ---- Results ----

type ProfileResult struct {
	CompletenessScore    int      `json:"completeness_score"`
	VerifiedSkills       int      `json:"verified_skills"`
	VerifiedExperiences  int      `json:"verified_experiences"`
	WorkEligibleCountry  []string `json:"work_eligible_countries"`
	Missing              []string `json:"missing"`
	UnverifiedFactsCount int      `json:"unverified_facts"`
}

type JobRef struct {
	JobID   uuid.UUID `json:"job_id"`
	Title   string    `json:"title"`
	Company string    `json:"company"`
}

type SearchResult struct {
	Jobs  []JobRef `json:"jobs"`
	Total int      `json:"total"`
}

type Scored struct {
	JobRef
	Score       float64  `json:"score"`
	Eligibility string   `json:"eligibility"`
	Confidence  string   `json:"confidence"`
	Blockers    []string `json:"blockers"`
}

type MatchResult struct {
	Scored   []Scored `json:"scored"`
	Unscored int      `json:"unscored"`
}

type RankResult struct {
	Selected   []Scored `json:"selected"`
	Ineligible int      `json:"ineligible"`
	BelowScore int      `json:"below_score"`
	Considered int      `json:"considered"`
}

type Item struct {
	JobRef
	Score         float64    `json:"score"`
	ApplicationID *uuid.UUID `json:"application_id,omitempty"`
	Created       bool       `json:"created,omitempty"`
	DocumentID    *uuid.UUID `json:"document_id,omitempty"`
	VersionID     *uuid.UUID `json:"version_id,omitempty"`
	Validation    string     `json:"validation,omitempty"`
	OK            bool       `json:"ok"`
	Flags         []string   `json:"flags,omitempty"`
	Error         string     `json:"error,omitempty"`
}

type ItemsResult struct {
	Items []Item `json:"items"`
}

// ---- Tools ----

func init() {
	register(&Tool{Name: "analyse_professional_profile", Description: "Read the verified profile and identify missing information needed for matching.",
		Effect: EffectRead, Timeout: 30 * time.Second, MaxAttempts: 3, Idempotency: "Read-only.",
		Output:   "Counts of verified facts, work-eligible countries and missing information.",
		NewInput: func() Input { return &noInput{} }, Run: runProfile})
	register(&Tool{Name: "search_jobs", Description: "Search discovered jobs from approved sources with the objective's filters.",
		Effect: EffectRead, Timeout: 30 * time.Second, MaxAttempts: 3, Idempotency: "Read-only.",
		Output:   "Up to 150 active, de-duplicated jobs matching the filters.",
		NewInput: func() Input { return &searchInput{} }, Run: runSearch})
	register(&Tool{Name: "calculate_job_match", Description: "Score suitability and eligibility of the found jobs against the verified profile (match-v1).",
		Effect: EffectInternal, Timeout: 2 * time.Minute, MaxAttempts: 3, Idempotency: "Deterministic and versioned; results are upserted.",
		Output:   "Score, eligibility and confidence per job.",
		NewInput: func() Input { return &noInput{} }, Run: runMatch})
	register(&Tool{Name: "rank_opportunities", Description: "Apply hard filters (no ineligible jobs, minimum score) and pick the best N.",
		Effect: EffectRead, Timeout: 10 * time.Second, MaxAttempts: 1, Idempotency: "Pure function of the match results.",
		Output:   "Selected jobs, with counts of jobs filtered out.",
		NewInput: func() Input { return &rankInput{} }, Run: runRank})
	register(&Tool{Name: "update_application_status", Description: "Add selected jobs to the pipeline as SHORTLISTED applications.",
		Action: permShortlist, Effect: EffectInternal, Timeout: 30 * time.Second, MaxAttempts: 3,
		Idempotency: "One application per job (duplicates are reused, never re-created).",
		Output:      "Application per selected job.",
		NewInput:    func() Input { return &noInput{} }, Run: runShortlist})
	register(&Tool{Name: "generate_tailored_cv", Description: "Prepare a tailored CV per shortlisted job from verified facts (template, or a grounded AI draft).",
		Action: permPrepare, Effect: EffectInternal, Timeout: 10 * time.Minute, MaxAttempts: 2,
		Idempotency: "Reuses an existing CV for the job instead of drafting another.",
		Output:      "A CV version and its fact-check status per job.",
		NewInput:    func() Input { return &prepareInput{} }, Run: runPrepare})
	register(&Tool{Name: "check_compliance", Description: "Advisory checks before approval: fact-check errors, closed postings, ineligibility, suspicious instructions inside postings.",
		Effect: EffectRead, Timeout: 30 * time.Second, MaxAttempts: 3, Idempotency: "Read-only.",
		Output:   "Per job: whether it can be approved, and why not.",
		NewInput: func() Input { return &noInput{} }, Run: runCompliance})
	register(&Tool{Name: "request_approval", Description: "Create an approval request listing the prepared packages; the workflow waits for your decision.",
		Effect: EffectInternal, Timeout: 30 * time.Second, MaxAttempts: 3, Idempotency: "One approval request per task (unique).",
		Output:   "Your decision and the jobs you approved.",
		NewInput: func() Input { return &noInput{} }, Run: runRequestApproval})
	register(&Tool{Name: "approve_application_package", Description: "For the jobs you approved: approve the CV version and the application for assisted submission.",
		Action: permPrepare, Effect: EffectInternal, Timeout: time.Minute, MaxAttempts: 3,
		Idempotency: "Approvals that already exist are left as they are.",
		Output:      "Per job: approved, or why not.",
		NewInput:    func() Input { return &noInput{} }, Run: runApplyApproval})
	register(&Tool{Name: "generate_career_report", Description: "Summarise what the workflow did, what failed and what to do next.",
		Effect: EffectRead, Timeout: 30 * time.Second, MaxAttempts: 3, Idempotency: "Read-only.",
		Output:   "The execution report.",
		NewInput: func() Input { return &noInput{} }, Run: runReport})
}

func runProfile(env *Env, _ Input) (any, error) {
	p, err := env.E.prof.Get(env.Ctx, env.UserID)
	if err != nil {
		return nil, retryable("PROFILE_UNAVAILABLE", "The profile could not be loaded.", err)
	}
	r := ProfileResult{CompletenessScore: p.Completeness.Score, WorkEligibleCountry: p.Preferences.WorkEligibleCountries,
		UnverifiedFactsCount: p.Completeness.UnverifiedFacts, Missing: []string{}}
	for _, s := range p.Skills {
		if s.Verification == "VERIFIED" {
			r.VerifiedSkills++
		}
	}
	for _, e := range p.Experiences {
		if e.Verification == "VERIFIED" {
			r.VerifiedExperiences++
		}
	}
	for _, m := range p.Completeness.Missing {
		r.Missing = append(r.Missing, m.Label)
	}
	if r.VerifiedSkills == 0 && r.VerifiedExperiences == 0 {
		return nil, needsInput("Your profile has no verified skills or experience, so jobs cannot be matched.",
			"Add or verify your skills and experience on your profile, then resume this workflow.")
	}
	if env.Params.Country != "" && !slices.Contains(r.WorkEligibleCountry, env.Params.Country) {
		// Matching enforces eligibility from verified preferences, so the
		// objective's country must be one of them.
		return nil, needsInput(fmt.Sprintf("The objective asks for jobs open to %s, but your preferences do not list %s as a country you can work in.",
			env.Params.Country, env.Params.Country),
			fmt.Sprintf("Add %s to your work-eligible countries (Profile → Preferences) if that is correct, then resume.", env.Params.Country))
	}
	return r, nil
}

func runSearch(env *Env, in Input) (any, error) {
	s := in.(*searchInput)
	f := listings.Filters{Query: s.Query, PostedWithin: s.PostedWithinDays, SalaryDisclose: s.RequireDisclosedSalary, Sort: "relevance"}
	if s.Query == "" {
		f.Sort = "newest"
	}
	if s.RemoteOnly {
		f.Remote = []string{"REMOTE"}
	}
	if m := s.MinMonthlySalary; m != nil {
		f.MinSalary, f.Currency = m.Amount*12, m.Currency // the search compares annualised salaries
	}
	page, err := listings.Search(env.Ctx, env.E.pool, f, s.Limit, "", listings.Viewer{})
	if err != nil {
		return nil, retryable("SEARCH_FAILED", "The job search failed.", err)
	}
	r := SearchResult{Jobs: []JobRef{}, Total: page.Total}
	for _, j := range page.Data {
		r.Jobs = append(r.Jobs, JobRef{JobID: j.ID, Title: j.Title, Company: j.Company})
	}
	return r, nil
}

func runMatch(env *Env, _ Input) (any, error) {
	sr, err := dep[SearchResult](env, "search_jobs")
	if err != nil {
		return nil, err
	}
	r := MatchResult{Scored: []Scored{}}
	if len(sr.Jobs) == 0 {
		return r, nil
	}
	ids := make([]uuid.UUID, 0, len(sr.Jobs))
	for _, j := range sr.Jobs {
		ids = append(ids, j.JobID)
	}
	res, err := env.E.match.EvaluateJobs(env.Ctx, env.UserID, ids)
	switch {
	case errors.Is(err, matching.ErrProfileIncomplete):
		return nil, needsInput("Your profile does not have enough verified information to be matched.", "Add skills and experience, then resume.")
	case errors.Is(err, matching.ErrAIUnavailable):
		return nil, retryable("AI_UNAVAILABLE", "The AI service is unavailable for matching.", err)
	case err != nil:
		return nil, retryable("MATCH_FAILED", "Matching failed.", err)
	}
	for _, j := range sr.Jobs {
		m, ok := res[j.JobID]
		if !ok {
			r.Unscored++ // not analysed yet; honestly left out
			continue
		}
		r.Scored = append(r.Scored, Scored{JobRef: j, Score: m.Score, Eligibility: m.Eligibility, Confidence: m.Confidence, Blockers: m.Blockers})
	}
	return r, nil
}

func runRank(env *Env, in Input) (any, error) {
	ri := in.(*rankInput)
	mr, err := dep[MatchResult](env, "match_jobs")
	if err != nil {
		return nil, err
	}
	r := RankResult{Selected: []Scored{}, Considered: len(mr.Scored)}
	var pool []Scored
	for _, s := range mr.Scored {
		switch {
		case s.Eligibility == matching.Ineligible:
			r.Ineligible++
		case s.Score < ri.MinScore:
			r.BelowScore++
		default:
			pool = append(pool, s)
		}
	}
	// Eligible before unknown eligibility, then by score; ties by job id (deterministic).
	slices.SortStableFunc(pool, func(a, b Scored) int {
		if (a.Eligibility == matching.Eligible) != (b.Eligibility == matching.Eligible) {
			if a.Eligibility == matching.Eligible {
				return -1
			}
			return 1
		}
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.JobID.String(), b.JobID.String())
	})
	if len(pool) > ri.Count {
		pool = pool[:ri.Count]
	}
	r.Selected = append(r.Selected, pool...)
	return r, nil
}

func runShortlist(env *Env, _ Input) (any, error) {
	rr, err := dep[RankResult](env, "rank")
	if err != nil {
		return nil, err
	}
	out := ItemsResult{Items: []Item{}}
	for _, s := range rr.Selected {
		it := Item{JobRef: s.JobRef, Score: s.Score, OK: true}
		id, err := env.E.apps.Create(env.Ctx, env.UserID, applications.CreateInput{JobID: s.JobID, Status: applications.Shortlisted,
			Notes: "Shortlisted by workflow " + env.WorkflowID.String()[:8]}, "")
		var he *httpx.Error
		switch {
		case err == nil:
			it.ApplicationID, it.Created = &id, true
		case errors.As(err, &he) && he.Code == httpx.CodeConflict && he.Details["application_id"] != "":
			existing := uuid.MustParse(he.Details["application_id"])
			it.ApplicationID = &existing // already tracked: reuse (idempotent)
		default:
			it.OK, it.Error = false, "Could not add to the pipeline."
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

func runPrepare(env *Env, in Input) (any, error) {
	pi := in.(*prepareInput)
	sl, err := dep[ItemsResult](env, "shortlist")
	if err != nil {
		return nil, err
	}
	out := ItemsResult{Items: []Item{}}
	failed := 0
	for _, it := range sl.Items {
		if !it.OK || it.ApplicationID == nil {
			out.Items = append(out.Items, it)
			continue
		}
		doc, v, err := env.E.prep.PrepareForWorkflow(env.Ctx, env.UserID, it.JobID, preparation.KindCV, pi.UseAI)
		if err != nil {
			failed++
			it.OK, it.Error = false, errorText(err)
			out.Items = append(out.Items, it)
			continue
		}
		it.DocumentID, it.VersionID, it.Validation = &doc.ID, &v.ID, v.ValidationStatus
		// Move the application into preparation when it has not progressed further.
		if d, err := env.E.apps.Get(env.Ctx, env.UserID, *it.ApplicationID); err == nil &&
			(d.Status == applications.Shortlisted || d.Status == applications.Discovered) {
			_ = env.E.apps.Transition(env.Ctx, env.UserID, *it.ApplicationID, applications.TransitionInput{To: applications.Preparing,
				Reason: "CV prepared by workflow"}, "")
		}
		out.Items = append(out.Items, it)
	}
	if failed > 0 && failed == len(sl.Items) {
		return nil, retryable("PREPARATION_FAILED", "No CV could be prepared.", errors.New(out.Items[0].Error))
	}
	return out, nil
}

// Instruction-like text inside third-party postings. Postings are data: this
// never changes what the workflow does, it only flags the job for review.
var suspicious = regexp.MustCompile(`(?i)ignore (all |any )?(previous|prior|above) instructions|disregard (the |your )?(rules|instructions)|` +
	`system prompt|you are (an? )?(ai|assistant|language model)|as an ai\b|(email|contact|message) (all|every) (recruiters|contacts)|` +
	`rate (this|the) candidate|automated (screening|systems?)[^.]{0,40}(must|should)`)

func runCompliance(env *Env, _ Input) (any, error) {
	pr, err := dep[ItemsResult](env, "prepare_documents")
	if err != nil {
		return nil, err
	}
	out := ItemsResult{Items: []Item{}}
	for _, it := range pr.Items {
		it.Flags = []string{}
		if !it.OK {
			it.Flags = append(it.Flags, "Preparation failed: "+it.Error)
		}
		var expired bool
		var text string
		if err := env.E.pool.QueryRow(env.Ctx, `SELECT expired_at IS NOT NULL, description_text FROM jobs WHERE id = $1`, it.JobID).Scan(&expired, &text); err != nil {
			return nil, retryable("JOB_UNAVAILABLE", "A job could not be read.", err)
		}
		if expired {
			it.Flags = append(it.Flags, "The posting is no longer listed.")
		}
		if suspicious.MatchString(text) {
			it.Flags = append(it.Flags, "The posting contains instruction-like text aimed at automated systems. It was treated as data; review the job yourself.")
		}
		if it.Validation == "FAILED" {
			it.Flags = append(it.Flags, "The CV has fact-check errors. Review it in the editor before approving.")
		}
		if it.Validation == "NOT_VALIDATED" {
			it.Flags = append(it.Flags, "The CV could not be fact-checked.")
		}
		it.OK = len(it.Flags) == 0
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// awaitApproval is returned by request_approval: the task waits for a decision.
type awaitApproval struct {
	Payload ApprovalPayload
}

type ApprovalPayload struct {
	Items   []Item `json:"items"`
	Channel string `json:"channel"`
	Note    string `json:"note"`
}

type ApprovalOutcome struct {
	Decision string      `json:"decision"` // APPROVED | REJECTED | EXPIRED | NONE
	Approved []uuid.UUID `json:"approved_job_ids"`
}

func runRequestApproval(env *Env, _ Input) (any, error) {
	cr, err := dep[ItemsResult](env, "compliance_check")
	if err != nil {
		return nil, err
	}
	approvable := 0
	for _, it := range cr.Items {
		if it.OK {
			approvable++
		}
	}
	if approvable == 0 {
		return ApprovalOutcome{Decision: "NONE", Approved: []uuid.UUID{}}, nil
	}
	return awaitApproval{Payload: ApprovalPayload{Items: cr.Items, Channel: applications.ChannelAssisted,
		Note: "Approving a job approves its CV version and authorises an assisted submission: you submit it yourself on the employer's site."}}, nil
}

func runApplyApproval(env *Env, _ Input) (any, error) {
	ao, err := dep[ApprovalOutcome](env, "request_approval")
	if err != nil {
		return nil, err
	}
	cr, err := dep[ItemsResult](env, "compliance_check")
	if err != nil {
		return nil, err
	}
	out := ItemsResult{Items: []Item{}}
	for _, it := range cr.Items {
		if !slices.Contains(ao.Approved, it.JobID) {
			continue
		}
		if !it.OK || it.DocumentID == nil || it.ApplicationID == nil {
			it.OK, it.Error = false, "Not approvable."
			out.Items = append(out.Items, it)
			continue
		}
		if err := env.E.prep.ApproveCurrentForWorkflow(env.Ctx, env.UserID, *it.DocumentID); err != nil {
			it.OK, it.Error = false, "CV not approved: "+errorText(err)
			out.Items = append(out.Items, it)
			continue
		}
		d, err := env.E.apps.Get(env.Ctx, env.UserID, *it.ApplicationID)
		if err != nil {
			it.OK, it.Error = false, errorText(err)
			out.Items = append(out.Items, it)
			continue
		}
		if d.Status == applications.Preparing {
			if err := env.E.apps.Transition(env.Ctx, env.UserID, *it.ApplicationID, applications.TransitionInput{To: applications.AwaitingApproval,
				Reason: "Approved in workflow"}, ""); err != nil {
				it.OK, it.Error = false, errorText(err)
				out.Items = append(out.Items, it)
				continue
			}
			d.Status = applications.AwaitingApproval
		}
		if d.Status == applications.AwaitingApproval {
			if err := env.E.apps.Approve(env.Ctx, env.UserID, *it.ApplicationID, applications.ApproveInput{Channel: applications.ChannelAssisted, Confirm: true}, ""); err != nil {
				it.OK, it.Error = false, errorText(err)
				out.Items = append(out.Items, it)
				continue
			}
		}
		it.OK = true
		out.Items = append(out.Items, it)
	}
	return out, nil
}

type Report struct {
	Headline  string   `json:"headline"`
	Lines     []string `json:"lines"`
	NextSteps []string `json:"next_steps"`
	Items     []Item   `json:"items"`
}

func runReport(env *Env, _ Input) (any, error) {
	r := Report{Lines: []string{}, NextSteps: []string{}, Items: []Item{}}
	if sr, err := dep[SearchResult](env, "search_jobs"); err == nil {
		r.Lines = append(r.Lines, fmt.Sprintf("Searched discovered jobs: %d matched the filters (up to 150 considered).", sr.Total))
	}
	if mr, err := dep[MatchResult](env, "match_jobs"); err == nil {
		line := fmt.Sprintf("Scored %d jobs against your verified profile.", len(mr.Scored))
		if mr.Unscored > 0 {
			line += fmt.Sprintf(" %d are not analysed yet and were left out.", mr.Unscored)
		}
		r.Lines = append(r.Lines, line)
	}
	if rr, err := dep[RankResult](env, "rank"); err == nil {
		r.Lines = append(r.Lines, fmt.Sprintf("Selected %d; filtered out %d ineligible and %d below your minimum score.", len(rr.Selected), rr.Ineligible, rr.BelowScore))
		unconfirmed := 0
		for _, s := range rr.Selected {
			if s.Eligibility != matching.Eligible {
				unconfirmed++
			}
		}
		if unconfirmed > 0 {
			r.Lines = append(r.Lines, fmt.Sprintf("%d selected job(s) do not state who can apply; check eligibility on the posting.", unconfirmed))
		}
	}
	items := map[uuid.UUID]Item{}
	order := []uuid.UUID{}
	for _, step := range []string{"shortlist", "prepare_documents", "compliance_check", "apply_approval"} {
		if ir, err := dep[ItemsResult](env, step); err == nil {
			for _, it := range ir.Items {
				if _, ok := items[it.JobID]; !ok {
					order = append(order, it.JobID)
				}
				items[it.JobID] = it
			}
		}
	}
	for _, id := range order {
		r.Items = append(r.Items, items[id])
	}
	approved := 0
	if ar, err := dep[ItemsResult](env, "apply_approval"); err == nil {
		for _, it := range ar.Items {
			if it.OK {
				approved++
			}
		}
	}
	if ao, err := dep[ApprovalOutcome](env, "request_approval"); err == nil {
		switch ao.Decision {
		case "REJECTED":
			r.Lines = append(r.Lines, "You declined the approval request; nothing was approved.")
		case "NONE":
			r.Lines = append(r.Lines, "No package passed the compliance checks, so there was nothing to approve.")
		case "APPROVED":
			r.Lines = append(r.Lines, fmt.Sprintf("You approved %d package(s); %d are now approved for assisted submission.", len(ao.Approved), approved))
		}
	}
	failed := 0
	for _, t := range env.Tasks {
		if t.Status == "FAILED" {
			failed++
			r.Lines = append(r.Lines, fmt.Sprintf("Step failed: %s (%s).", t.Type, deref(t.ErrorMessage)))
		}
	}
	switch {
	case approved > 0:
		r.Headline = fmt.Sprintf("%d application(s) approved and ready for you to submit.", approved)
		r.NextSteps = append(r.NextSteps, "Open Applications, start each submission, apply on the employer's official site, then record the confirmation.")
	case len(r.Items) > 0:
		r.Headline = fmt.Sprintf("%d job(s) shortlisted.", len(r.Items))
		r.NextSteps = append(r.NextSteps, "Review the shortlisted jobs in Applications and prepare or approve their documents.")
	default:
		r.Headline = "No jobs met your criteria this time."
		r.NextSteps = append(r.NextSteps, "Try a broader query, a lower minimum score, or wait for the next discovery cycle.")
	}
	if failed > 0 {
		r.NextSteps = append(r.NextSteps, "Some steps failed; you can retry them from this page.")
	}
	return r, nil
}

func errorText(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Message
	}
	return "unexpected error"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
