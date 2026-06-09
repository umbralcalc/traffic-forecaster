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

func recAt(eventTime, ref, ha, tm, ps, pe string) streetmanager.Record {
	obj := map[string]string{
		"work_reference_number":   ref,
		"highway_authority":       ha,
		"work_category":           "Standard",
		"traffic_management_type": tm,
		"proposed_start_date":     ps,
		"proposed_end_date":       pe,
	}
	b, _ := json.Marshal(obj)
	return streetmanager.Record{EventTime: eventTime, ObjectData: b}
}

func TestPipelineSeriesAsOfGate(t *testing.T) {
	ws := Works{}
	// Work A: filed in April, scheduled for June -> KNOWN before June starts.
	ws.Apply(recAt("2026-04-10T00:00:00Z", "A", "LONDON BOROUGH OF SOUTHWARK", "Lane closure",
		"2026-06-05T00:00:00Z", "2026-06-15T00:00:00Z"))
	// Work B: filed mid-June, active in June -> NOT known before June starts
	// (mimics a reactive/late work). Must be excluded from June's pipeline.
	ws.Apply(recAt("2026-06-12T00:00:00Z", "B", "LONDON BOROUGH OF SOUTHWARK", "Lane closure",
		"2026-06-12T00:00:00Z", "2026-06-22T00:00:00Z"))

	rows := ws.PipelineSeries(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	var june *Row
	for i := range rows {
		if rows[i].Month == "2026-06" {
			june = &rows[i]
		}
	}
	if june == nil {
		t.Fatal("expected a June pipeline row")
	}
	if june.Works != 1 {
		t.Errorf("June pipeline works = %d, want 1 (only the pre-filed work A)", june.Works)
	}
	// A spans 5..15 Jun = 10 days, weight 0.6 -> 6 weighted days.
	if june.WeightedDays != 6 {
		t.Errorf("June pipeline weighted = %v, want 6", june.WeightedDays)
	}
}

func TestParsePoint(t *testing.T) {
	e, n, ok := parsePoint("POINT(523431.10 182057.55)")
	if !ok || e != 523431.10 || n != 182057.55 {
		t.Errorf("parsePoint = %v,%v,%v", e, n, ok)
	}
	if _, _, ok := parsePoint(""); ok {
		t.Error("empty should not parse")
	}
	if _, _, ok := parsePoint("LINESTRING(1 2,3 4)"); ok {
		t.Error("non-point should not parse")
	}
}

func TestCellIDAndCentroid(t *testing.T) {
	// 2km cells: easting 523431 -> floor(523431/2000)=261, northing 182057 -> 91.
	id := CellID(523431, 182057, 2000)
	if id != "261_91" {
		t.Fatalf("CellID = %q, want 261_91", id)
	}
	e, n, ok := CellCentroid(id, 2000)
	if !ok || e != 261.5*2000 || n != 91.5*2000 {
		t.Errorf("CellCentroid = %v,%v,%v", e, n, ok)
	}
}

func recGeo(eventTime, ref, point string) streetmanager.Record {
	obj := map[string]string{
		"work_reference_number":      ref,
		"highway_authority":          "LONDON BOROUGH OF SOUTHWARK",
		"work_category":              "Standard",
		"traffic_management_type":    "Lane closure",
		"actual_start_date_time":     "2026-06-01T00:00:00Z",
		"actual_end_date_time":       "2026-06-11T00:00:00Z",
		"works_location_coordinates": point,
	}
	b, _ := json.Marshal(obj)
	return streetmanager.Record{EventTime: eventTime, ObjectData: b}
}

func TestCellSeriesAttributesByCoordinate(t *testing.T) {
	ws := Works{}
	ws.Apply(recGeo("2026-06-02T00:00:00Z", "W1", "POINT(523431 182057)"))
	ws.Apply(recGeo("2026-06-02T00:00:00Z", "W2", "POINT(523900 182400)")) // same 2km cell
	ws.Apply(recGeo("2026-06-02T00:00:00Z", "W3", "POINT(530000 182057)")) // different cell

	rows := ws.CellSeries(2000, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	byCell := map[string]int{}
	for _, r := range rows {
		byCell[r.Key] = r.Works
	}
	if byCell["261_91"] != 2 {
		t.Errorf("cell 261_91 works = %d, want 2", byCell["261_91"])
	}
	if len(byCell) != 2 {
		t.Errorf("distinct cells = %d, want 2: %v", len(byCell), byCell)
	}
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
	if r.Month != "2026-06" || r.Key != "Southwark" {
		t.Errorf("row key = %s/%s", r.Month, r.Key)
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
