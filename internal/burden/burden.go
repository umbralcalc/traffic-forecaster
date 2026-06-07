// Package burden turns Street Manager work events into the monthly per-borough
// works-burden series that the forecast's planned+emergency terms are backtested
// against. It reconstructs each work's active window from its lifecycle events,
// then sums weighted work-days overlapping each calendar month.
//
// Scope and approximations (v1, documented deliberately):
//   - This is the WORKS burden only (planned + emergency, from Street Manager).
//     The non-works incident term comes from our own TfL snapshots, separately.
//   - A work is keyed by work_reference_number (falling back to the permit
//     reference). We keep the latest event's state per work, so phased works
//     collapse to their final window (a known simplification).
//   - Burden weight is a provisional function of traffic-management type; the
//     real weighting is calibrated later against TfL-feed severity.
//   - TLRN works (highway_authority "TRANSPORT FOR LONDON") are bucketed as
//     "TfL (TLRN)" rather than spatially attributed to a borough.
package burden

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/streetmanager"
)

// Work is the reconstructed state of a single street work.
type Work struct {
	Ref          string
	Borough      string
	Category     string
	TrafficMgmt  string
	PlannedStart time.Time
	PlannedEnd   time.Time
	ActualStart  time.Time
	ActualEnd    time.Time
	// FirstSeen is the earliest event_time for this work — when it became known.
	// The pipeline estimate uses it to count only works filed before a forecast
	// month began (so reactive emergency works correctly drop out of the forward
	// view).
	FirstSeen time.Time
	lastEvent time.Time
}

// Works accumulates the latest-known state per work reference.
type Works map[string]*Work

// Apply folds one event into the work table. It tracks FirstSeen (the earliest
// event) for vintaging, and keeps the latest event's state for the realised
// window (events carry the full object state, so the latest is most complete).
// object_data is unmarshalled once per event for speed. Records without a usable
// reference are ignored.
func (ws Works) Apply(r streetmanager.Record) {
	var od map[string]json.RawMessage
	if json.Unmarshal(r.ObjectData, &od) != nil {
		return
	}
	get := func(k string) string {
		raw, ok := od[k]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	ref := firstNonEmpty(get("work_reference_number"), get("permit_reference_number"))
	if ref == "" {
		return
	}
	et := parseTime(r.EventTime)
	w, ok := ws[ref]
	if !ok {
		w = &Work{Ref: ref}
		ws[ref] = w
	}
	if w.FirstSeen.IsZero() || (!et.IsZero() && et.Before(w.FirstSeen)) {
		w.FirstSeen = et
	}
	if ok && !et.After(w.lastEvent) {
		return // older event: FirstSeen already updated, keep latest state
	}
	w.lastEvent = et
	w.Borough = Borough(get("highway_authority"))
	w.Category = get("work_category")
	w.TrafficMgmt = firstNonEmpty(get("current_traffic_management_type"), get("traffic_management_type"))
	w.PlannedStart = parseTime(get("proposed_start_date"))
	w.PlannedEnd = parseTime(get("proposed_end_date"))
	w.ActualStart = parseTime(get("actual_start_date_time"))
	w.ActualEnd = parseTime(get("actual_end_date_time"))
}

// Window returns the work's active interval, preferring realised (actual) dates
// over proposed ones, and whether it is usable.
func (w *Work) Window() (start, end time.Time, ok bool) {
	if !w.ActualStart.IsZero() && !w.ActualEnd.IsZero() && w.ActualEnd.After(w.ActualStart) {
		return w.ActualStart, w.ActualEnd, true
	}
	if !w.PlannedStart.IsZero() && !w.PlannedEnd.IsZero() && w.PlannedEnd.After(w.PlannedStart) {
		return w.PlannedStart, w.PlannedEnd, true
	}
	return time.Time{}, time.Time{}, false
}

// PlannedWindow returns the work's proposed interval, used for the forward
// pipeline estimate (what was scheduled, regardless of what actually happened).
func (w *Work) PlannedWindow() (start, end time.Time, ok bool) {
	if !w.PlannedStart.IsZero() && !w.PlannedEnd.IsZero() && w.PlannedEnd.After(w.PlannedStart) {
		return w.PlannedStart, w.PlannedEnd, true
	}
	return time.Time{}, time.Time{}, false
}

// IsEmergency reports whether a work is a reactive "Immediate" category.
func IsEmergency(category string) bool {
	return strings.HasPrefix(strings.TrimSpace(category), "Immediate")
}

// Cell is the burden of one borough in one month.
type Cell struct {
	Works         int     // distinct works active in the month
	WorkDays      float64 // unweighted work-days
	WeightedDays  float64 // traffic-management-weighted work-days
	PlannedDays   float64 // work-days from planned works
	EmergencyDays float64 // work-days from emergency (Immediate) works
}

// Row is a flattened (month, borough) burden record.
type Row struct {
	Month   string // YYYY-MM
	Borough string
	Cell
}

// Series computes the per-borough monthly burden over [from, to] (inclusive of
// the months containing those instants), returned sorted by month then borough.
func (ws Works) Series(from, to time.Time) []Row {
	grid := map[string]map[string]*Cell{} // month -> borough -> cell
	for _, w := range ws {
		start, end, ok := w.Window()
		if !ok || w.Borough == "" {
			continue
		}
		weight := TMWeight(w.TrafficMgmt)
		emergency := IsEmergency(w.Category)
		for m := monthStart(maxTime(start, from)); !m.After(end) && !m.After(to); m = m.AddDate(0, 1, 0) {
			days := overlapDays(start, end, m, m.AddDate(0, 1, 0))
			if days <= 0 {
				continue
			}
			key := m.Format("2006-01")
			byBorough := grid[key]
			if byBorough == nil {
				byBorough = map[string]*Cell{}
				grid[key] = byBorough
			}
			c := byBorough[w.Borough]
			if c == nil {
				c = &Cell{}
				byBorough[w.Borough] = c
			}
			c.Works++
			c.WorkDays += days
			c.WeightedDays += weight * days
			if emergency {
				c.EmergencyDays += days
			} else {
				c.PlannedDays += days
			}
		}
	}
	return flatten(grid)
}

// PipelineSeries computes the FORWARD pipeline estimate of works burden per
// borough-month over [from, to], vintaged honestly: a work contributes to month
// m only if it was first seen (filed) before m began, and only via its PROPOSED
// window. Reactive emergency works — filed at the time they happen — therefore
// drop out of the estimate for any month they would otherwise fall in, which is
// exactly the known-ahead planned signal we want as a covariate.
//
// v1 simplification: proposed dates are taken from the work's latest known state,
// not re-vintaged to the forecast instant; only the filing (FirstSeen) is gated.
func (ws Works) PipelineSeries(from, to time.Time) []Row {
	grid := map[string]map[string]*Cell{}
	for _, w := range ws {
		start, end, ok := w.PlannedWindow()
		if !ok || w.Borough == "" {
			continue
		}
		weight := TMWeight(w.TrafficMgmt)
		for m := monthStart(maxTime(start, from)); !m.After(end) && !m.After(to); m = m.AddDate(0, 1, 0) {
			// As-of gate: only works filed before this month began are "known".
			if !w.FirstSeen.IsZero() && !w.FirstSeen.Before(m) {
				continue
			}
			days := overlapDays(start, end, m, m.AddDate(0, 1, 0))
			if days <= 0 {
				continue
			}
			key := m.Format("2006-01")
			byBorough := grid[key]
			if byBorough == nil {
				byBorough = map[string]*Cell{}
				grid[key] = byBorough
			}
			c := byBorough[w.Borough]
			if c == nil {
				c = &Cell{}
				byBorough[w.Borough] = c
			}
			c.Works++
			c.WorkDays += days
			c.WeightedDays += weight * days
		}
	}
	return flatten(grid)
}

// TMWeight maps a traffic-management type to a provisional burden weight. The
// strings are the permit-file vocabulary observed live (2026-05). These weights
// are placeholders to be calibrated against TfL-feed severity later.
func TMWeight(tm string) float64 {
	switch strings.TrimSpace(tm) {
	case "Road closure":
		return 1.0
	case "Contra-flow":
		return 0.8
	case "Lane closure", "Multi-way signals":
		return 0.6
	case "Two-way signals", "Give and take", "Priority working", "Stop/go boards", "Convoy workings":
		return 0.3
	case "Some carriageway incursion", "Temp obstruction 15 minute delay":
		return 0.2
	case "No carriageway incursion":
		return 0.05
	case "":
		return 0.3 // unknown: a middling default
	default:
		return 0.3
	}
}

// Borough normalises a Street Manager highway_authority to a borough label,
// aligned where possible with the TfL feed's borough names.
func Borough(highwayAuthority string) string {
	ha := strings.ToUpper(strings.TrimSpace(highwayAuthority))
	if special, ok := boroughSpecials[ha]; ok {
		return special
	}
	if rest, ok := strings.CutPrefix(ha, "LONDON BOROUGH OF "); ok {
		return titleBorough(rest)
	}
	return "" // not a London authority we attribute
}

var boroughSpecials = map[string]string{
	"TRANSPORT FOR LONDON":                    "TfL (TLRN)",
	"CITY OF LONDON CORPORATION":              "City of London",
	"CITY OF LONDON":                          "City of London",
	"CITY OF WESTMINSTER":                     "Westminster",
	"ROYAL BOROUGH OF KENSINGTON AND CHELSEA": "Kensington & Chelsea",
	"ROYAL BOROUGH OF KINGSTON UPON THAMES":   "Kingston upon Thames",
	"ROYAL BOROUGH OF GREENWICH":              "Greenwich",
	"LONDON LEGACY DEVELOPMENT CORPORATION":   "LLDC",
}

// titleBorough title-cases a borough name and renders " AND " as " & " to match
// the TfL feed (e.g. "HAMMERSMITH & FULHAM" -> "Hammersmith & Fulham").
func titleBorough(s string) string {
	words := strings.Fields(strings.ReplaceAll(s, " AND ", " & "))
	for i, w := range words {
		if w == "&" {
			continue
		}
		lower := strings.ToLower(w)
		if lower == "upon" || lower == "of" || lower == "on" {
			words[i] = lower
			continue
		}
		words[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(words, " ")
}

// flatten turns the month->borough->cell grid into a sorted row slice.
func flatten(grid map[string]map[string]*Cell) []Row {
	var rows []Row
	for month, byBorough := range grid {
		for borough, c := range byBorough {
			rows = append(rows, Row{Month: month, Borough: borough, Cell: *c})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Month != rows[j].Month {
			return rows[i].Month < rows[j].Month
		}
		return rows[i].Borough < rows[j].Borough
	})
	return rows
}

// --- helpers ---

func overlapDays(start, end, mStart, mEnd time.Time) float64 {
	s := maxTime(start, mStart)
	e := minTime(end, mEnd)
	if !e.After(s) {
		return 0
	}
	return e.Sub(s).Hours() / 24
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
