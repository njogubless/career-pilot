package analytics

import (
	"testing"
	"time"
)

func TestRateCarriesBasisAndFlagsSmallSamples(t *testing.T) {
	if r := rate(1, 0); r.Value != nil || !r.LowSample {
		t.Fatalf("zero denominator: %+v", r)
	}
	if r := rate(2, 4); *r.Value != 0.5 || !r.LowSample {
		t.Fatalf("small sample: %+v", r)
	}
	if r := rate(3, 10); *r.Value != 0.3 || r.LowSample {
		t.Fatalf("normal: %+v", r)
	}
}

func TestMedianAndWeekStart(t *testing.T) {
	if median(nil) != nil || *median([]float64{3, 1, 2}) != 2 || *median([]float64{4, 1, 2, 3}) != 2.5 {
		t.Fatal("median wrong")
	}
	wed := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	if ws := weekStart(wed); ws.Weekday() != time.Monday || ws.Day() != 16 || ws.Hour() != 0 {
		t.Fatalf("week start = %s", ws)
	}
	sun := time.Date(2026, 11, 22, 23, 0, 0, 0, time.UTC)
	if weekStart(sun).Day() != 16 {
		t.Fatal("Sunday belongs to the week starting Monday")
	}
}

func TestStageRanks(t *testing.T) {
	order := []string{"SHORTLISTED", "PREPARING", "APPROVED", "SUBMITTED", "ACKNOWLEDGED", "INTERVIEWING", "OFFER"}
	for i := 1; i < len(order); i++ {
		if rank(order[i]) <= rank(order[i-1]) {
			t.Fatalf("%s should rank above %s", order[i], order[i-1])
		}
	}
	if rank("WITHDRAWN") != -1 {
		t.Fatal("withdrawn is not a progress stage")
	}
}
