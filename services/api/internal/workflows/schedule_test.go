package workflows

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNextRunDailyAndWeekly(t *testing.T) {
	nbo, _ := time.LoadLocation("Africa/Nairobi")           // UTC+3, no DST
	after := time.Date(2026, 11, 18, 5, 30, 0, 0, time.UTC) // 08:30 in Nairobi
	if got := NextRun("DAILY", nil, 7, nbo, after); !got.Equal(time.Date(2026, 11, 19, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily (already past 07:00 local) = %s", got.UTC())
	}
	if got := NextRun("DAILY", nil, 9, nbo, after); !got.Equal(time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily (later today) = %s", got.UTC())
	}
	mon := 1
	if got := NextRun("WEEKLY", &mon, 8, nbo, after); got.In(nbo).Weekday() != time.Monday || got.In(nbo).Hour() != 8 || !got.After(after) {
		t.Fatalf("weekly = %s", got.In(nbo))
	}
	wed := 3 // 2026-11-18 is a Wednesday; 08:00 has passed, so next week
	if got := NextRun("WEEKLY", &wed, 8, nbo, after); got.In(nbo).Format("2006-01-02 15") != "2026-11-25 08" {
		t.Fatalf("weekly same day passed = %s", got.In(nbo))
	}
}

func TestNextRunAcrossDST(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York") // DST ended 2026-11-01
	after := time.Date(2026, 10, 31, 15, 0, 0, 0, ny)
	got := NextRun("DAILY", nil, 9, ny, after)
	if got.In(ny).Format("2006-01-02 15:04") != "2026-11-01 09:00" {
		t.Fatalf("local wall time not kept across DST: %s", got.In(ny))
	}
	if got.UTC().Hour() != 14 { // EST = UTC-5 after the change
		t.Fatalf("UTC hour = %d", got.UTC().Hour())
	}
}

func TestScheduleValidation(t *testing.T) {
	bad := []ScheduleInput{
		{Name: "", Template: TemplateDiscoverRank, Cadence: "DAILY", Hour: 7, Timezone: "UTC"},
		{Name: "x", Template: TemplateDiscoverRank, Cadence: "HOURLY", Hour: 7, Timezone: "UTC"},
		{Name: "x", Template: TemplateDiscoverRank, Cadence: "WEEKLY", Hour: 7, Timezone: "UTC"},
		{Name: "x", Template: TemplateDiscoverRank, Cadence: "DAILY", Hour: 24, Timezone: "UTC"},
		{Name: "x", Template: TemplateDiscoverRank, Cadence: "DAILY", Hour: 7, Timezone: "Mars/Olympus"},
		{Name: "x", Template: TemplateDiscoverRank, Cadence: "DAILY", Hour: 7, Timezone: "UTC", Params: json.RawMessage(`{"count":99}`)},
	}
	for i, in := range bad {
		if _, _, err := in.validate(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	ok := ScheduleInput{Name: "Mornings", Template: TemplateDiscoverRank, Cadence: "DAILY", Hour: 7, Timezone: "Africa/Nairobi"}
	if _, _, err := ok.validate(); err != nil {
		t.Fatal(err)
	}
}
