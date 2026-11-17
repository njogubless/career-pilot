package applications

import (
	"slices"
	"testing"
)

func TestEveryStatusHasTransitions(t *testing.T) {
	if len(transitions) != len(AllStatuses) {
		t.Fatalf("transitions cover %d statuses, want %d", len(transitions), len(AllStatuses))
	}
	for from, tos := range transitions {
		for to := range tos {
			if !IsValid(to) {
				t.Errorf("%s → unknown %s", from, to)
			}
		}
	}
}

func TestGuardedEdgesCannotBeTakenManually(t *testing.T) {
	// SUBMITTED, APPROVED and FAILED are only reachable through dedicated actions.
	for _, from := range AllStatuses {
		for _, to := range ManualTargets(from) {
			if to == Submitted || to == Failed || (to == Approved && from != Failed) {
				t.Errorf("manual edge %s → %s must be guarded", from, to)
			}
		}
	}
	for _, e := range [][2]string{{AwaitingApproval, Approved}, {Approved, Submitted}, {Approved, Failed}, {Approved, AwaitingApproval}} {
		if v, g := CanTransition(e[0], e[1]); !v || !g {
			t.Errorf("%s → %s: valid=%v guarded=%v", e[0], e[1], v, g)
		}
	}
}

func TestInvalidEdgesAndTerminals(t *testing.T) {
	for _, e := range [][2]string{{Shortlisted, Submitted}, {Discovered, Approved}, {Submitted, Approved}, {Rejected, Interviewing},
		{Withdrawn, Shortlisted}, {Offer, Interviewing}, {Preparing, Approved}} {
		if v, _ := CanTransition(e[0], e[1]); v {
			t.Errorf("%s → %s must be invalid", e[0], e[1])
		}
	}
	if !IsTerminal(Rejected) || !IsTerminal(Withdrawn) || IsTerminal(Offer) {
		t.Fatal("terminal statuses wrong")
	}
	// Every non-terminal status can be withdrawn.
	for _, s := range AllStatuses {
		if !IsTerminal(s) && !slices.Contains(ManualTargets(s), Withdrawn) {
			t.Errorf("%s cannot be withdrawn", s)
		}
	}
}

func TestSubmittedOnlyAfterApproval(t *testing.T) {
	// The only edge into SUBMITTED starts from APPROVED.
	for from, tos := range transitions {
		if _, ok := tos[Submitted]; ok && from != Approved {
			t.Errorf("SUBMITTED reachable from %s", from)
		}
	}
}
