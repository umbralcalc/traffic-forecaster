package burden

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/streetmanager"
)

func TestBorough(t *testing.T) {
	cases := map[string]string{
		"LONDON BOROUGH OF SOUTHWARK":             "Southwark",
		"LONDON BOROUGH OF TOWER HAMLETS":         "Tower Hamlets",
		"LONDON BOROUGH OF BARKING AND DAGENHAM":  "Barking & Dagenham",
		"ROYAL BOROUGH OF KENSINGTON AND CHELSEA": "Kensington & Chelsea",
		"ROYAL BOROUGH OF KINGSTON UPON THAMES":   "Kingston upon Thames",
		"CITY OF WESTMINSTER":                     "Westminster",
		"CITY OF LONDON CORPORATION":              "City of London",
		"TRANSPORT FOR LONDON":                    "TfL (TLRN)",
		"DERBY CITY COUNCIL":                      "",
	}
	for in, want := range cases {
		if got := Borough(in); got != want {
			t.Errorf("Borough(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTMWeightAndEmergency(t *testing.T) {
	if TMWeight("Road closure") <= TMWeight("Lane closure") {
		t.Error("road closure should outweigh lane closure")
	}
	if TMWeight("Lane closure") <= TMWeight("No carriageway incursion") {
		t.Error("lane closure should outweigh no incursion")
	}
	if !IsEmergency("Immediate - urgent") || IsEmergency("Minor") {
		t.Error("emergency classification wrong")
	}
}

func TestOverlapDays(t *testing.T) {
	jun := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	jul := jun.AddDate(0, 1, 0)
	// A work spanning 25 May .. 10 Jun contributes 10 days to June.
	start := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	if d := overlapDays(start, end, jun, jul); d != 10 {
		t.Errorf("overlap = %v, want 10", d)
	}
}

func rec(eventTime, ha, cat, tm, ps, pe, as, ae string) streetmanager.Record {
	obj := map[string]string{
		"work_reference_number":   "WR-1",
		"highway_authority":       ha,
		"work_category":           cat,
		"traffic_management_type": tm,
		"proposed_start_date":     ps,
		"proposed_end_date":       pe,
		"actual_start_date_time":  as,
		"actual_end_date_time":    ae,
	}
	b, _ := json.Marshal(obj)
	return streetmanager.Record{EventTime: eventTime, ObjectData: b}
}

func TestWorksLatestWinsAndSeries(t *testing.T) {
	ws := Works{}
	// Older event: planned only.
	ws.Apply(rec("2026-05-01T00:00:00Z", "LONDON BOROUGH OF SOUTHWARK", "Standard", "Lane closure",
		"2026-06-01T00:00:00Z", "2026-06-21T00:00:00Z", "", ""))
	// Newer event: realised dates supersede, and shorten the window.
	ws.Apply(rec("2026-06-25T00:00:00Z", "LONDON BOROUGH OF SOUTHWARK", "Standard", "Lane closure",
		"2026-06-01T00:00:00Z", "2026-06-21T00:00:00Z",
		"2026-06-01T00:00:00Z", "2026-06-11T00:00:00Z"))

	w := ws["WR-1"]
	start, end, ok := w.Window()
	if !ok || start.Day() != 1 || end.Day() != 11 {
		t.Fatalf("expected realised 1..11 Jun, got %v..%v ok=%v", start, end, ok)
	}

	rows := ws.Series(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Month != "2026-06" || r.Borough != "Southwark" {
		t.Errorf("row key = %s/%s", r.Month, r.Borough)
	}
	if r.Works != 1 || r.WorkDays != 10 {
		t.Errorf("works=%d workdays=%v, want 1 / 10", r.Works, r.WorkDays)
	}
	if r.WeightedDays != 0.6*10 {
		t.Errorf("weighted=%v, want 6", r.WeightedDays)
	}
	if r.PlannedDays != 10 || r.EmergencyDays != 0 {
		t.Errorf("planned=%v emergency=%v", r.PlannedDays, r.EmergencyDays)
	}
}
