// Package applications is the application pipeline: an explicit state
// machine, the approval workflow, assisted submission with evidence,
// idempotency and execution history (master prompt §9–10, ADR-022).
package applications

// Statuses (master prompt §9).
const (
	Discovered       = "DISCOVERED"
	Shortlisted      = "SHORTLISTED"
	Preparing        = "PREPARING"
	AwaitingApproval = "AWAITING_APPROVAL"
	Approved         = "APPROVED"
	Submitted        = "SUBMITTED"
	Acknowledged     = "ACKNOWLEDGED"
	Interviewing     = "INTERVIEWING"
	Assessment       = "ASSESSMENT"
	Offer            = "OFFER"
	Rejected         = "REJECTED"
	Withdrawn        = "WITHDRAWN"
	Failed           = "FAILED"
)

// AllStatuses in pipeline order.
var AllStatuses = []string{Discovered, Shortlisted, Preparing, AwaitingApproval, Approved, Failed, Submitted, Acknowledged,
	Assessment, Interviewing, Offer, Rejected, Withdrawn}

// transitions lists every valid edge. Edges marked guarded can only be taken
// by a dedicated action, never by a plain status change:
//   - → APPROVED only through the approval action (which pins the package),
//   - → SUBMITTED only by recording submission evidence,
//   - → FAILED only by recording a failed submission attempt,
//   - APPROVED/FAILED → AWAITING_APPROVAL only by revoking the approval.
var transitions = map[string]map[string]bool{
	Discovered:       {Shortlisted: false, Preparing: false, Withdrawn: false},
	Shortlisted:      {Preparing: false, Withdrawn: false},
	Preparing:        {AwaitingApproval: false, Shortlisted: false, Withdrawn: false},
	AwaitingApproval: {Approved: true, Preparing: false, Withdrawn: false},
	Approved:         {Submitted: true, Failed: true, AwaitingApproval: true, Preparing: false, Withdrawn: false},
	Failed:           {Approved: false, AwaitingApproval: true, Preparing: false, Withdrawn: false},
	Submitted:        {Acknowledged: false, Assessment: false, Interviewing: false, Offer: false, Rejected: false, Withdrawn: false},
	Acknowledged:     {Assessment: false, Interviewing: false, Offer: false, Rejected: false, Withdrawn: false},
	Assessment:       {Interviewing: false, Offer: false, Rejected: false, Withdrawn: false},
	Interviewing:     {Assessment: false, Offer: false, Rejected: false, Withdrawn: false},
	Offer:            {Rejected: false, Withdrawn: false},
	Rejected:         {},
	Withdrawn:        {},
}

// CanTransition reports whether from → to is a valid edge and whether it is
// reserved for a dedicated action.
func CanTransition(from, to string) (valid, guarded bool) {
	g, ok := transitions[from][to]
	return ok, g
}

// ManualTargets are the statuses a user may move to with a plain status change.
func ManualTargets(from string) []string {
	out := []string{}
	for _, s := range AllStatuses {
		if g, ok := transitions[from][s]; ok && !g {
			out = append(out, s)
		}
	}
	return out
}

// IsTerminal reports statuses with no outgoing edges.
func IsTerminal(s string) bool { return len(transitions[s]) == 0 }

// IsValid reports a known status.
func IsValid(s string) bool { _, ok := transitions[s]; return ok }

// hasSubmitted reports statuses that imply a recorded submission.
func hasSubmitted(s string) bool {
	switch s {
	case Submitted, Acknowledged, Assessment, Interviewing, Offer:
		return true
	}
	return false
}
